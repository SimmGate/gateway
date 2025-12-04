package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"simmgate-gateway/internal/cache"
	"simmgate-gateway/internal/llm"
)

type mockLLMClient struct {
	resp           *llm.ChatResponse
	stream         chan llm.StreamResult
	err            error
	streamErr      error
	nonStreamCalls int
	streamCalls    int
	lastRequest    *llm.ChatRequest
}

func (m *mockLLMClient) ChatCompletion(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	m.nonStreamCalls++
	m.lastRequest = req
	if m.err != nil {
		return nil, m.err
	}
	return m.resp, nil
}

func (m *mockLLMClient) ChatCompletionStream(ctx context.Context, req *llm.ChatRequest) (<-chan llm.StreamResult, error) {
	m.streamCalls++
	m.lastRequest = req
	if m.streamErr != nil {
		return nil, m.streamErr
	}
	if m.stream == nil {
		m.stream = make(chan llm.StreamResult)
	}
	return m.stream, nil
}

func TestChatHandlerNonStream(t *testing.T) {
	cacheStore := cache.NewMemoryExactCache(time.Minute)
	t.Cleanup(func() { cacheStore.Close() })

	fakeLLM := &mockLLMClient{
		resp: &llm.ChatResponse{
			Model: "gpt-4",
			Choices: []llm.ChatChoice{
				{Index: 0, Message: llm.ChatMessage{Role: llm.RoleAssistant, Content: "hello!"}},
			},
		},
	}

	h := NewChatHandler(cacheStore, time.Minute, "vtest", fakeLLM)

	requestBody := llm.ChatRequest{
		Model: "gpt-4",
		Messages: []llm.ChatMessage{
			{Role: llm.RoleUser, Content: "hi"},
		},
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "user-42")

	rr := httptest.NewRecorder()
	h.ChatCompletion(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rr.Code)
	}

	var resp llm.ChatResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.Choices[0].Message.Content != "hello!" {
		t.Fatalf("unexpected response message: %#v", resp.Choices[0].Message)
	}

	if fakeLLM.nonStreamCalls != 1 {
		t.Fatalf("expected non-stream call once, got %d", fakeLLM.nonStreamCalls)
	}

	cacheKey, err := cache.BuildExactCacheKeyFromChatRequest(requestBody, "user-42", "vtest")
	if err != nil {
		t.Fatalf("build cache key: %v", err)
	}
	if _, hit, _ := cacheStore.Get(context.Background(), cacheKey.String()); !hit {
		t.Fatalf("expected response to be cached")
	}
}

func TestChatHandlerStream(t *testing.T) {
	cacheStore := cache.NewMemoryExactCache(time.Minute)
	t.Cleanup(func() { cacheStore.Close() })

	streamChan := make(chan llm.StreamResult, 2)
	fakeLLM := &mockLLMClient{
		stream: streamChan,
	}

	h := NewChatHandler(cacheStore, time.Minute, "vtest", fakeLLM)

	requestBody := llm.ChatRequest{
		Model:  "gpt-4",
		Stream: true,
		Messages: []llm.ChatMessage{
			{Role: llm.RoleUser, Content: "stream please"},
		},
	}
	payload, err := json.Marshal(requestBody)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-ID", "user-stream")

	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ChatCompletion(rr, req)
		close(done)
	}()

	streamChan <- llm.StreamResult{Chunk: &llm.StreamChunk{Index: 0, Delta: "hel"}}
	streamChan <- llm.StreamResult{Chunk: &llm.StreamChunk{Index: 0, Delta: "lo", FinishReason: "stop"}}
	close(streamChan)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatalf("handler did not finish streaming")
	}

	if fakeLLM.streamCalls != 1 {
		t.Fatalf("expected stream call once, got %d", fakeLLM.streamCalls)
	}

	body := rr.Body.String()
	if !strings.Contains(body, `"content":"hel"`) {
		t.Fatalf("expected first chunk in body: %s", body)
	}
	if !strings.Contains(body, `"content":"lo"`) {
		t.Fatalf("expected second chunk in body: %s", body)
	}
	if !strings.Contains(body, "data: [DONE]") {
		t.Fatalf("expected DONE sentinel in body: %s", body)
	}
}
