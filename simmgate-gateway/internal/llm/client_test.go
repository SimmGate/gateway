package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap/zaptest"
)

func TestNewClientValidation(t *testing.T) {
	t.Parallel()

	_, err := NewClient(Config{}, zaptest.NewLogger(t))
	if err == nil {
		t.Fatalf("expected validation error, got nil")
	}
}

func TestChatCompletionSuccess(t *testing.T) {
	t.Parallel()

	var gotReq providerChatRequest
	var gotAuth string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}

		gotAuth = r.Header.Get("Authorization")
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Fatalf("unmarshal request: %v", err)
		}

		resp := providerChatResponse{
			ID:      "chatcmpl-1",
			Object:  "chat.completion",
			Created: time.Unix(1_700_000_000, 0).Unix(),
			Model:   "gpt-4",
			Choices: []providerChatChoice{
				{
					Index: 0,
					Message: ChatMessage{
						Role:    RoleAssistant,
						Content: "response",
					},
					FinishReason: "stop",
				},
			},
			Usage: &providerUsage{
				PromptTokens:     3,
				CompletionTokens: 2,
				TotalTokens:      5,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(resp); err != nil {
			t.Fatalf("encode response: %v", err)
		}
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		BaseURL: srv.URL,
		APIKey:  "test-key",
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer closeClient(client)

	req := &ChatRequest{
		Model: "gpt-4",
		Messages: []ChatMessage{
			{Role: RoleUser, Content: "ping"},
		},
		Temperature: 0.3,
		TopP:        0.9,
		MaxTokens:   50,
	}

	resp, err := client.ChatCompletion(context.Background(), req)
	if err != nil {
		t.Fatalf("ChatCompletion: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Fatalf("unexpected Authorization header: %s", gotAuth)
	}
	if gotReq.Stream {
		t.Fatalf("non-stream request should not set stream=true")
	}
	if gotReq.Model != req.Model {
		t.Fatalf("expected model %s, got %s", req.Model, gotReq.Model)
	}
	if len(gotReq.Messages) != len(req.Messages) || gotReq.Messages[0].Content != "ping" {
		t.Fatalf("unexpected request messages: %#v", gotReq.Messages)
	}

	if resp == nil || len(resp.Choices) != 1 {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if resp.Choices[0].Message.Content != "response" {
		t.Fatalf("unexpected response message: %#v", resp.Choices[0].Message)
	}
	if resp.Usage == nil || resp.Usage.TotalTokens != 5 {
		t.Fatalf("usage not mapped correctly: %#v", resp.Usage)
	}
}

func TestChatCompletionValidationError(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatalf("server should not be called for invalid request")
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		BaseURL: srv.URL,
		APIKey:  "key",
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer closeClient(client)

	_, err = client.ChatCompletion(context.Background(), &ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "invalid request") {
		t.Fatalf("expected validation error, got %v", err)
	}
}

func TestChatCompletionStream(t *testing.T) {
	t.Parallel()

	var gotReq providerChatRequest

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read body: %v", err)
		}
		if err := json.Unmarshal(body, &gotReq); err != nil {
			t.Fatalf("unmarshal body: %v", err)
		}

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("response writer does not support flushing")
		}

		chunks := []string{
			`{"choices":[{"index":0,"delta":{"content":"hel"}}]}`,
			`{"choices":[{"index":0,"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		}

		for _, chunk := range chunks {
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}
		fmt.Fprint(w, "data: [DONE]\n\n")
		flusher.Flush()
	}))
	defer srv.Close()

	client, err := NewClient(Config{
		BaseURL: srv.URL,
		APIKey:  "stream-key",
	}, zaptest.NewLogger(t))
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}
	defer closeClient(client)

	req := &ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: RoleUser, Content: "hello"},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	stream, err := client.ChatCompletionStream(ctx, req)
	if err != nil {
		t.Fatalf("ChatCompletionStream: %v", err)
	}

	var deltas strings.Builder
	var finishReason string

	for res := range stream {
		if res.Err != nil {
			t.Fatalf("received stream error: %v", res.Err)
		}
		if res.Chunk == nil {
			continue
		}

		deltas.WriteString(res.Chunk.Delta)
		if res.Chunk.FinishReason != "" {
			finishReason = res.Chunk.FinishReason
		}
	}

	if !gotReq.Stream {
		t.Fatalf("stream requests must set stream=true")
	}
	if gotReq.Model != req.Model {
		t.Fatalf("expected model %s, got %s", req.Model, gotReq.Model)
	}
	if gotReq.Messages[0].Content != "hello" {
		t.Fatalf("unexpected request body: %#v", gotReq.Messages)
	}

	if deltas.String() != "hello" {
		t.Fatalf("unexpected stream deltas: %s", deltas.String())
	}
	if finishReason != "stop" {
		t.Fatalf("unexpected finish reason: %s", finishReason)
	}
}

func closeClient(c Client) {
	if closer, ok := c.(interface{ Close() error }); ok {
		_ = closer.Close()
	}
}
