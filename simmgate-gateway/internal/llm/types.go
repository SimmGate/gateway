package llm

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	RoleSystem    = "system"
	RoleUser      = "user"
	RoleAssistant = "assistant"
)

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float32       `json:"temperature,omitempty"`
	TopP        float32       `json:"top_p,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stop        []string      `json:"stop,omitempty"`
	Stream      bool          `json:"stream,omitempty"`
}

func (r *ChatRequest) Validate() error {
	if r.Model == "" {
		return errors.New("model is required")
	}

	if len(r.Messages) == 0 {
		return errors.New("at least one message is required")
	}

	for i, m := range r.Messages {
		if m.Role != RoleSystem && m.Role != RoleUser && m.Role != RoleAssistant {
			return fmt.Errorf("invalid role %q in messages[%d]", m.Role, i)
		}
		if m.Content == "" && m.Role != RoleSystem {
			return fmt.Errorf("content is required for messages[%d]", i)
		}
	}

	if r.Temperature < 0 || r.Temperature > 2 {
		return errors.New("temperature must be between 0 and 2")
	}
	if r.TopP < 0 || r.TopP > 1 {
		return errors.New("top_p must be between 0 and 1")
	}

	return nil
}

type ChatChoice struct {
	Index        int         `json:"index"`
	Message      ChatMessage `json:"message"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type ChatResponse struct {
	ID      string       `json:"id,omitempty"`
	Created time.Time    `json:"created,omitempty"`
	Model   string       `json:"model,omitempty"`
	Choices []ChatChoice `json:"choices"`
	Usage   *Usage       `json:"usage,omitempty"`
}

type StreamChunk struct {
	Index        int    `json:"index"`
	Delta        string `json:"delta"`
	FinishReason string `json:"finish_reason,omitempty"`
}

type StreamResult struct {
	Chunk *StreamChunk
	Err   error
}

type Client interface {
	ChatCompletion(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
	ChatCompletionStream(ctx context.Context, req *ChatRequest) (<-chan StreamResult, error)
}
