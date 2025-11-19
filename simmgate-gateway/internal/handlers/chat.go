package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"simmgate-gateway/internal/cache"
	"simmgate-gateway/pkg/logging/logging"
	"simmgate-gateway/pkg/types"

	"go.uber.org/zap"
)

// ChatHandler holds dependencies for the /v1/chat/completions endpoint.
type ChatHandler struct {
	Cache     cache.ExactCache
	CacheTTL  time.Duration
	VersionID string
}

func NewChatHandler(c cache.ExactCache, ttl time.Duration, versionID string) *ChatHandler {
	return &ChatHandler{
		Cache:     c,
		CacheTTL:  ttl,
		VersionID: versionID,
	}
}

// ChatCompletion handles POST /v1/chat/completions.
// For now, it just returns { "message": "ok" }, but it's fully wired with
// Tier 1 exact cache and structured logging.
func (h *ChatHandler) ChatCompletion(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	logger := logging.L(ctx)
	start := time.Now()

	var req types.ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("invalid request", zap.Error(err))
		http.Error(w, "invalid JSON", http.StatusBadRequest)
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

	key, err := cache.BuildExactCacheKeyFromChatRequest(req, userID, versionID)
	if err != nil {
		logger.Warn("key_builder_error", zap.Error(err))
		h.respondNoCache(w, logger, req, userID, modelID, versionID, start)
		return
	}

	cacheKey := key.String()
	hashKey := key.Hash

	// ---- Tier 1 exact cache lookup ----
	cacheLookupStart := time.Now()
	cachedBytes, hit, cacheErr := h.Cache.Get(ctx, cacheKey)
	cacheLookupLatency := time.Since(cacheLookupStart)

	if cacheErr != nil {
		// Cache is best-effort; log and treat as miss.
		logger.Warn("exact_cache_get_error", zap.Error(cacheErr))
	}

	if hit {
		var cachedResp types.ChatResponse
		if err := json.Unmarshal(cachedBytes, &cachedResp); err != nil {
			logger.Warn("exact_cache_unmarshal_error", zap.Error(err))
		} else {
			totalLatency := time.Since(start)

			logger.Info("cache_decision",
				zap.String("cache_tier", "exact"),
				zap.String("hash_key", hashKey),
				zap.String("user_id", userID),
				zap.String("model_id", modelID),
				zap.String("version_id", versionID),
				zap.Bool("cache_hit", true),
				zap.Duration("cache_lookup_latency_ms", cacheLookupLatency),
				zap.Duration("total_latency_ms", totalLatency),
			)

			h.writeJSON(w, cachedResp)
			return
		}
	}

	// ---- Cache miss: simulate upstream LLM (for now) ----
	llmStart := time.Now()
	// TODO: replace this with real LLM call in next sprint.
	resp := types.ChatResponse{Message: "ok"}
	llmLatency := time.Since(llmStart)

	respBytes, err := json.Marshal(resp)
	if err != nil {
		logger.Warn("marshal_response_error", zap.Error(err))
	} else {
		if err := h.Cache.Set(ctx, cacheKey, respBytes, h.CacheTTL); err != nil {
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
		zap.Bool("cache_hit", false),
		zap.Duration("cache_lookup_latency_ms", cacheLookupLatency),
		zap.Duration("llm_latency_ms", llmLatency),
		zap.Duration("total_latency_ms", totalLatency),
	)

	h.writeJSON(w, resp)
}

// respondNoCache is a fallback when key building fails.
// Behaves like the old handler: no cache, just respond "ok".
func (h *ChatHandler) respondNoCache(
	w http.ResponseWriter,
	logger *zap.Logger,
	req types.ChatRequest,
	userID, modelID, versionID string,
	start time.Time,
) {
	resp := types.ChatResponse{Message: "ok"}

	logger.Info("cache_decision_nohash",
		zap.String("cache_tier", "exact"),
		zap.String("user_id", userID),
		zap.String("model_id", modelID),
		zap.String("version_id", versionID),
		zap.Bool("cache_hit", false),
		zap.Duration("total_latency_ms", time.Since(start)),
	)

	h.writeJSON(w, resp)
}

// writeJSON is a small helper to send JSON responses consistently.
func (h *ChatHandler) writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
