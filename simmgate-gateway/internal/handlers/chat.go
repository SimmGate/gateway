package handlers

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"simmgate-gateway/internal/cache"
	"simmgate-gateway/internal/llm"
	"simmgate-gateway/pkg/logging/logging"

	"go.uber.org/zap"
)

// ChatHandler holds dependencies for the /v1/chat/completions endpoint.
type ChatHandler struct {
	Cache     cache.ExactCache
	CacheTTL  time.Duration
	VersionID string
	LLM       llm.Client
}

func NewChatHandler(c cache.ExactCache, ttl time.Duration, versionID string, client llm.Client) *ChatHandler {
	return &ChatHandler{
		Cache:     c,
		CacheTTL:  ttl,
		VersionID: versionID,
		LLM:       client,
	}
}

// ChatCompletion handles POST /v1/chat/completions.
// Routes non-stream responses through the exact cache and forwards stream
// requests directly to the upstream LLM.
func (h *ChatHandler) ChatCompletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.L(ctx)
	start := time.Now()

	var req llm.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("invalid_request", zap.Error(err))
		writeErrorJSON(ctx, w, http.StatusBadRequest, "invalid_json")
		return
	}

	modelID := req.Model
	if modelID == "" {
		modelID = "unknown-model"
	}

	userID := r.Header.Get("X-User-ID")
	if userID == "" {
		userID = "anon"
	}

	versionID := h.VersionID
	if versionID == "" {
		versionID = "v1"
	}

	if req.Stream {
		h.streamChatCompletion(ctx, w, logger, &req, userID, modelID, versionID, start)
		return
	}

	var (
		cacheKey           string
		hashKey            string
		cacheLookupLatency time.Duration
		cacheHit           bool
	)

	key, err := cache.BuildExactCacheKeyFromChatRequest(req, userID, versionID)
	if err != nil {
		logger.Warn("key_builder_error", zap.Error(err))
	} else {
		cacheKey = key.String()
		hashKey = key.Hash

		cacheLookupStart := time.Now()
		cachedBytes, hit, cacheErr := h.Cache.Get(ctx, cacheKey)
		cacheLookupLatency = time.Since(cacheLookupStart)

		if cacheErr != nil {
			logger.Warn("exact_cache_get_error", zap.Error(cacheErr))
		}

		if hit {
			var cachedResp llm.ChatResponse
			if err := json.Unmarshal(cachedBytes, &cachedResp); err != nil {
				logger.Warn("exact_cache_unmarshal_error", zap.Error(err))
			} else {
				cacheHit = true
				totalLatency := time.Since(start)

				logger.Info("cache_decision",
					zap.String("cache_tier", "exact"),
					zap.String("hash_key", hashKey),
					zap.String("user_id", userID),
					zap.String("model_id", modelID),
					zap.String("version_id", versionID),
					zap.Bool("cache_hit", true),
					zap.Duration("cache_lookup_latency", cacheLookupLatency),
					zap.Duration("total_latency", totalLatency),
				)

				h.writeJSON(ctx, w, cachedResp)
				return
			}
		}
	}

	llmStart := time.Now()
	resp, err := h.LLM.ChatCompletion(ctx, &req)
	llmLatency := time.Since(llmStart)
	if err != nil {
		logger.Error("llm_request_failed", zap.Error(err))
		writeErrorJSON(ctx, w, http.StatusBadGateway, "upstream_error")
		return
	}

	if cacheKey != "" {
		respBytes, err := json.Marshal(resp)
		if err != nil {
			logger.Warn("marshal_response_error", zap.Error(err))
		} else if err := h.Cache.Set(ctx, cacheKey, respBytes, h.CacheTTL); err != nil {
			logger.Warn("exact_cache_set_error", zap.Error(err))
		}
	}

	totalLatency := time.Since(start)

	logger.Info("cache_decision",
		zap.String("cache_tier", "exact"),
		zap.String("hash_key", hashKey),
		zap.String("user_id", userID),
		zap.String("model_id", modelID),
		zap.String("version_id", versionID),
		zap.Bool("cache_hit", cacheHit),
		zap.Duration("cache_lookup_latency", cacheLookupLatency),
		zap.Duration("llm_latency", llmLatency),
		zap.Duration("total_latency", totalLatency),
	)

	h.writeJSON(ctx, w, resp)
}

func (h *ChatHandler) streamChatCompletion(
	ctx context.Context,
	w http.ResponseWriter,
	logger *zap.Logger,
	req *llm.ChatRequest,
	userID, modelID, versionID string,
	start time.Time,
) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeErrorJSON(ctx, w, http.StatusInternalServerError, "streaming_not_supported")
		return
	}

	stream, err := h.LLM.ChatCompletionStream(ctx, req)
	if err != nil {
		logger.Error("llm_stream_connect_failed", zap.Error(err))
		writeErrorJSON(ctx, w, http.StatusBadGateway, "upstream_error")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Flush headers so the client can start receiving chunks immediately.
	flusher.Flush()

	chunks := 0

	for {
		select {
		case <-ctx.Done():
			logger.Info("stream_cancelled",
				zap.String("user_id", userID),
				zap.String("model_id", modelID),
				zap.String("version_id", versionID),
				zap.Int("chunks", chunks),
				zap.Duration("total_latency", time.Since(start)),
				zap.Error(ctx.Err()),
			)
			return

		case res, ok := <-stream:
			if !ok {
				if _, err := w.Write([]byte("data: [DONE]\n\n")); err != nil {
					logger.Warn("stream_done_write_error", zap.Error(err))
				} else {
					flusher.Flush()
				}

				logger.Info("stream_completed",
					zap.String("user_id", userID),
					zap.String("model_id", modelID),
					zap.String("version_id", versionID),
					zap.Int("chunks", chunks),
					zap.Duration("total_latency", time.Since(start)),
				)
				return
			}

			if res.Err != nil {
				logger.Error("llm_stream_error", zap.Error(res.Err))
				_ = writeSSEJSON(w, map[string]string{"error": "upstream_error"})
				if _, err := w.Write([]byte("data: [DONE]\n\n")); err != nil {
					logger.Warn("stream_error_done_write_error", zap.Error(err))
				}
				flusher.Flush()
				return
			}

			if res.Chunk == nil {
				continue
			}

			payload := streamResponse{
				Choices: []streamChoice{{
					Index: res.Chunk.Index,
					Delta: streamDelta{
						Content: res.Chunk.Delta,
					},
					FinishReason: res.Chunk.FinishReason,
				}},
			}

			if err := writeSSEJSON(w, payload); err != nil {
				logger.Warn("stream_write_error", zap.Error(err))
				return
			}

			flusher.Flush()
			chunks++
		}
	}
}

func writeSSEJSON(w http.ResponseWriter, v interface{}) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	if _, err := w.Write([]byte("data: ")); err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if _, err := w.Write([]byte("\n\n")); err != nil {
		return err
	}
	return nil
}

// writeJSON is a small helper to send JSON responses consistently.
func (h *ChatHandler) writeJSON(ctx context.Context, w http.ResponseWriter, v interface{}) {
	logger := logging.L(ctx)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		logger.Warn("write_json_failed", zap.Error(err))
	}
}

func writeErrorJSON(ctx context.Context, w http.ResponseWriter, status int, msg string) {
	logger := logging.L(ctx)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(map[string]string{"error": msg}); err != nil {
		logger.Warn("write_error_json_failed", zap.Error(err))
	}
}

type streamResponse struct {
	Choices []streamChoice `json:"choices"`
}

type streamChoice struct {
	Index        int         `json:"index"`
	Delta        streamDelta `json:"delta"`
	FinishReason string      `json:"finish_reason,omitempty"`
}

type streamDelta struct {
	Content string `json:"content,omitempty"`
}
