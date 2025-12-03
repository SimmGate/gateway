package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"
)

const (
	maxRequestSize = 2 * 1024 * 1024 // 2MB total JSON payload
	maxMessageSize = 512 * 1024      // 512KB per message content
)

func (c *client) ChatCompletion(parentCtx context.Context, req *ChatRequest) (*ChatResponse, error) {
	start := time.Now()

	if req == nil {
		return nil, fmt.Errorf("llmclient: request is nil")
	}

	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("llmclient: invalid request: %w", err)
	}

	// Per-message size guard
	for i, m := range req.Messages {
		if len(m.Content) > maxMessageSize {
			return nil, fmt.Errorf(
				"llmclient: message[%d] content too large (%d bytes, max %d)",
				i, len(m.Content), maxMessageSize,
			)
		}
	}

	c.logger.Debug("llm request starting",
		zap.String("model", req.Model),
		zap.Int("message_count", len(req.Messages)),
	)

	// Per-request timeout (0 = only use parentCtx)
	var ctx context.Context
	var cancel context.CancelFunc
	if c.cfg.UpstreamTimeout > 0 {
		ctx, cancel = context.WithTimeout(parentCtx, c.cfg.UpstreamTimeout)
	} else {
		ctx, cancel = context.WithCancel(parentCtx)
	}
	defer cancel()

	// Build provider request
	pReq := providerChatRequest{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		MaxTokens:   req.MaxTokens,
		Stop:        req.Stop,
		Stream:      false,
	}

	bodyBytes, err := json.Marshal(pReq)
	if err != nil {
		return nil, fmt.Errorf("llmclient: marshal request: %w", err)
	}

	// Sanity check total request size
	if len(bodyBytes) > maxRequestSize {
		return nil, fmt.Errorf(
			"llmclient: request too large (%d bytes, max %d)",
			len(bodyBytes), maxRequestSize,
		)
	}

	url := c.cfg.BaseURL + "/v1/chat/completions"

	// doOnce builds a fresh *http.Request for each attempt
	doOnce := func(ctx context.Context, body []byte) (*http.Response, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("llmclient: build HTTP request: %w", err)
		}
		httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
		httpReq.Header.Set("Content-Type", "application/json")
		return c.httpClient.Do(httpReq)
	}

	resp, err := c.doWithRetry(ctx, bodyBytes, doOnce)
	if err != nil {
		c.logger.Error("llm request failed",
			zap.Error(err),
			zap.Duration("duration", time.Since(start)),
		)
		return nil, err
	}
	defer resp.Body.Close()

	// Handle non-2xx responses
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)

		// Try to parse structured error
		var perr providerErrorResponse
		if err := json.Unmarshal(body, &perr); err == nil && perr.Error.Message != "" {
			c.logger.Error("llm provider error",
				zap.Int("status", resp.StatusCode),
				zap.String("error_type", perr.Error.Type),
				zap.String("error_message", perr.Error.Message),
			)
			return nil, fmt.Errorf("llmclient: upstream %d: %s (%s)",
				resp.StatusCode, perr.Error.Message, perr.Error.Type)
		}

		// Fallback to raw body
		c.logger.Error("llm upstream error",
			zap.Int("status", resp.StatusCode),
			zap.String("body", truncate(string(body), 200)),
		)
		return nil, fmt.Errorf("llmclient: upstream %d: %s",
			resp.StatusCode, truncate(string(body), 200))
	}

	// Decode success response
	var pResp providerChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&pResp); err != nil {
		return nil, fmt.Errorf("llmclient: decode upstream response: %w", err)
	}

	// Validate response has choices
	if len(pResp.Choices) == 0 {
		c.logger.Error("llm provider returned no choices",
			zap.String("model", req.Model),
		)
		return nil, fmt.Errorf("llmclient: provider returned no choices")
	}

	// Map provider - internal response
	out := &ChatResponse{
		ID:      pResp.ID,
		Created: time.Unix(pResp.Created, 0),
		Model:   pResp.Model,
		Choices: make([]ChatChoice, 0, len(pResp.Choices)),
	}

	for _, ch := range pResp.Choices {
		out.Choices = append(out.Choices, ChatChoice{
			Index:        ch.Index,
			Message:      ch.Message,
			FinishReason: ch.FinishReason,
		})
	}

	// Always include usage (even if zero)
	out.Usage = &Usage{}
	if pResp.Usage != nil {
		out.Usage.PromptTokens = pResp.Usage.PromptTokens
		out.Usage.CompletionTokens = pResp.Usage.CompletionTokens
		out.Usage.TotalTokens = pResp.Usage.TotalTokens
	}

	c.logger.Info("llm request completed",
		zap.String("model", out.Model),
		zap.Int("prompt_tokens", out.Usage.PromptTokens),
		zap.Int("completion_tokens", out.Usage.CompletionTokens),
		zap.Duration("duration", time.Since(start)),
	)

	return out, nil
}

// truncate limits string length for logging
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
