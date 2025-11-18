package middleware

import (
	"net/http"
	"runtime/debug"

	"simmgate-gateway/pkg/logging/logging"

	"go.uber.org/zap"
)

//recover from panic , log 500

func Recoverer() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				rec := recover()
				if rec != nil {
					logger := logging.L(r.Context())
					logger.Error("panic recovered",
						zap.Any("error", rec),
						zap.ByteString("stack", debug.Stack()),
					)

					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusInternalServerError)
					_, _ = w.Write([]byte(`{"error":"internal_server_error"}`))
				}
			}()

			next.ServeHTTP(w, r)
		})
	}
}
