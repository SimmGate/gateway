package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"go.uber.org/zap"
)

func (c *client) ChatCompletionStream(parentCtx context.Context, req *ChatRequest) (<-chan StreamResult, error) {
	if req == nil {
		return nil, fmt.Errorf("llmclient: request is nil")
	}

	// Validate request
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("llmclient: invalid request: %w", err)
	}

	// Per-message size guard (same as non-streaming)
	for i, m := range req.Messages {
		if len(m.Content) > maxMessageSize {
			return nil, fmt.Errorf(
				"llmclient: message[%d] content too large (%d bytes, max %d)",
				i, len(m.Content), maxMessageSize,
			)
		}
	}

	c.logger.Debug("llm stream request starting",
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

	results := make(chan StreamResult, 16)

	go func() {
		defer close(results)
		defer cancel()

		// ---------- Build provider request ----------

		pReq := providerChatRequest{
			Model:       req.Model,
			Messages:    req.Messages,
			Temperature: req.Temperature,
			TopP:        req.TopP,
			MaxTokens:   req.MaxTokens,
			Stop:        req.Stop,
			Stream:      true,
		}

		bodyBytes, err := json.Marshal(pReq)
		if err != nil {
			results <- StreamResult{Err: fmt.Errorf("llmclient: marshal stream request: %w", err)}
			return
		}

		// Total request size guard
		if len(bodyBytes) > maxRequestSize {
			results <- StreamResult{Err: fmt.Errorf(
				"llmclient: request too large (%d bytes, max %d)",
				len(bodyBytes), maxRequestSize,
			)}
			return
		}

		url := c.cfg.BaseURL + "/v1/chat/completions"

		doOnce := func(ctx context.Context, body []byte) (*http.Response, error) {
			httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
			if err != nil {
				return nil, fmt.Errorf("llmclient: build HTTP stream request: %w", err)
			}
			httpReq.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
			httpReq.Header.Set("Content-Type", "application/json")
			return c.httpClient.Do(httpReq)
		}

		// ---------- Connect with retries (no mid-stream retries) ----------

		resp, err := c.doWithRetry(ctx, bodyBytes, doOnce)
		if err != nil {
			c.logger.Error("llm stream connect failed",
				zap.String("model", req.Model),
				zap.Error(err),
			)
			results <- StreamResult{Err: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			body, _ := io.ReadAll(resp.Body)

			var perr providerErrorResponse
			if err := json.Unmarshal(body, &perr); err == nil && perr.Error.Message != "" {
				c.logger.Error("llm stream provider error",
					zap.String("model", req.Model),
					zap.Int("status", resp.StatusCode),
					zap.String("error_type", perr.Error.Type),
					zap.String("error_message", perr.Error.Message),
				)
				results <- StreamResult{Err: fmt.Errorf("llmclient: upstream stream %d: %s (%s)",
					resp.StatusCode, perr.Error.Message, perr.Error.Type)}
				return
			}

			c.logger.Error("llm stream upstream error",
				zap.String("model", req.Model),
				zap.Int("status", resp.StatusCode),
				zap.String("body", truncate(string(body), 200)),
			)
			results <- StreamResult{Err: fmt.Errorf("llmclient: upstream stream %d: %s",
				resp.StatusCode, truncate(string(body), 200))}
			return
		}

		// ---------- Read SSE stream ----------

		reader := bufio.NewReader(resp.Body)
		chunkCount := 0

		for {
			// Respect context cancellation (timeout / caller cancel)
			select {
			case <-ctx.Done():
				c.logger.Info("llm stream cancelled",
					zap.String("model", req.Model),
					zap.Error(ctx.Err()),
				)
				return
			default:
			}

			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					// Normal end of stream without explicit [DONE]
					c.logger.Info("llm stream completed (EOF)",
						zap.String("model", req.Model),
						zap.Int("chunks", chunkCount),
					)
					return
				}
				results <- StreamResult{Err: fmt.Errorf("llmclient: read stream line: %w", err)}
				return
			}

			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}

			const prefix = "data: "
			if !bytes.HasPrefix(line, []byte(prefix)) {
				// Ignore non-data SSE lines
				continue
			}

			payload := bytes.TrimSpace(line[len(prefix):])

			// End-of-stream sentinel from provider
			if bytes.Equal(payload, []byte("[DONE]")) {
				c.logger.Info("llm stream received [DONE]",
					zap.String("model", req.Model),
					zap.Int("chunks", chunkCount),
				)
				return
			}

			var chunk providerStreamChunk
			if err := json.Unmarshal(payload, &chunk); err != nil {
				results <- StreamResult{Err: fmt.Errorf("llmclient: unmarshal stream chunk: %w", err)}
				return
			}

			for _, choice := range chunk.Choices {
				deltaText := choice.Delta.Content
				if deltaText == "" && choice.FinishReason == "" {
					continue
				}

				sc := &StreamChunk{
					Index:        choice.Index,
					Delta:        deltaText,
					FinishReason: choice.FinishReason,
				}
				chunkCount++

				select {
				case <-ctx.Done():
					c.logger.Info("llm stream cancelled while sending chunk",
						zap.String("model", req.Model),
						zap.Int("chunks", chunkCount),
						zap.Error(ctx.Err()),
					)
					return
				case results <- StreamResult{Chunk: sc}:
				}
			}
		}
	}()

	return results, nil
}
