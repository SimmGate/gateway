package handlers

import (
	"encoding/json"
	"net/http"
	"time"

	"simmgate-gateway/pkg/logging/logging"

	"go.uber.org/zap"
)

type ChatRequest struct {
	Model string `json:"model"`
}

type ChatResponse struct {
	Message string `json:"message"`
}

func ChatCompletion(w http.ResponseWriter, r *http.Request) {
	logger := logging.L(r.Context())
	start := time.Now()

	var req ChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		logger.Warn("invalid request", zap.Error(err))
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	logger.Info("received chat completion request",
		zap.String("model", req.Model),
	)

	resp := ChatResponse{Message: "ok"}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)

	logger.Info("handled chat completion",
		zap.Duration("latency", time.Since(start)),
	)
}
