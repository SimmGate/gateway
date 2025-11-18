package middleware

import (
	"context"
	"net/http"
	"time"

	"simmgate-gateway/pkg/logging/logging"

	"go.uber.org/zap"
)

// Timeout cancels the request context after d and returns 504 if still running.

// Timeout cancels the request context after d and returns 504 if still running.
func Timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()

			r = r.WithContext(ctx)

			done := make(chan struct{})
			go func() {
				next.ServeHTTP(w, r)
				close(done)
			}()

			select {
			case <-done:

				return
			case <-ctx.Done():
				// context timeout/cancelled
				logger := logging.L(ctx)
				logger.Warn("request timeout", zap.Duration("timeout", d))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusGatewayTimeout)
				_, _ = w.Write([]byte(`{"error":"gateway_timeout"}`))
			}
		})
	}
}
