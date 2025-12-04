package httpserver

import (
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"go.uber.org/zap"

	"simmgate-gateway/internal/handlers"
	"simmgate-gateway/internal/metrics"
	"simmgate-gateway/internal/middleware"
)

func SetupRouter(r *chi.Mux, baseLogger *zap.Logger, chatHandler *handlers.ChatHandler) {

	r.Use(metrics.Middleware)

	// base middleware
	r.Use(chimw.RequestID)
	r.Use(chimw.RealIP)

	r.Use(middleware.LoggingContext(baseLogger))
	r.Use(middleware.Recoverer())               // panic recovery
	r.Use(middleware.Timeout(15 * time.Second)) // request timeout
	r.Use(middleware.MaxBodySize(512 * 1024))   // 512 KB max body

	// routes
	r.Route("/v1", func(r chi.Router) {
		r.Post("/chat/completions", chatHandler.ChatCompletion)
	})

	// health check
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Handle("/metrics", metrics.Handler())

	registerPprof(r)
}

func registerPprof(r chi.Router) {
	r.Route("/debug/pprof", func(r chi.Router) {
		r.Get("/", http.HandlerFunc(pprof.Index))
		r.Get("/cmdline", http.HandlerFunc(pprof.Cmdline))
		r.Get("/profile", http.HandlerFunc(pprof.Profile))
		r.Get("/symbol", http.HandlerFunc(pprof.Symbol))
		r.Post("/symbol", http.HandlerFunc(pprof.Symbol))
		r.Get("/trace", http.HandlerFunc(pprof.Trace))

		r.Handle("/allocs", pprof.Handler("allocs"))
		r.Handle("/block", pprof.Handler("block"))
		r.Handle("/goroutine", pprof.Handler("goroutine"))
		r.Handle("/heap", pprof.Handler("heap"))
		r.Handle("/mutex", pprof.Handler("mutex"))
		r.Handle("/threadcreate", pprof.Handler("threadcreate"))
	})
}
