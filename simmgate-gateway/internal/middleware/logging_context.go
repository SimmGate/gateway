package middleware

import (
	"net/http"

	chimw "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"simmgate-gateway/pkg/logging/logging"
)

// LoggingContext attaches a request-scoped logger to the context.
func LoggingContext(baseLogger *zap.Logger) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			// Get request ID from chi middleware (or empty string if not set)
			reqID := chimw.GetReqID(ctx)

			// Start from the base logger
			reqLogger := baseLogger.With(
				zap.String("method", r.Method),
				zap.String("path", r.URL.Path),
			)

			// Include request_id if available
			if reqID != "" {
				reqLogger = reqLogger.With(zap.String("request_id", reqID))
			}

			// Real IP from chi's RealIP middleware (or RemoteAddr fallback)
			remoteIP := r.RemoteAddr
			if remoteIP != "" {
				reqLogger = reqLogger.With(zap.String("remote_ip", remoteIP))
			}

			// Optional: user agent
			if ua := r.UserAgent(); ua != "" {
				reqLogger = reqLogger.With(zap.String("user_agent", ua))
			}

			// Put into context using your helper
			ctx = logging.WithLogger(ctx, reqLogger)

			// Continue with updated context
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
