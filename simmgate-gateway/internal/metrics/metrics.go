package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	// Counter: how many times we served from exact cache.
	ExactHitsTotal = prometheus.NewCounter(
		prometheus.CounterOpts{
			Name: "exact_hits_total",
			Help: "Total number of exact cache hits.",
		},
	)

	// Histogram: gateway HTTP latency in seconds.
	GatewayLatencySeconds = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "gateway_latency_seconds",
			Help:    "HTTP request latency for the gateway in seconds.",
			Buckets: []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2, 5},
		},
		[]string{"path", "method", "status_code"},
	)
)

// Register is called once in main() to register metrics.
func Register() {
	prometheus.MustRegister(
		ExactHitsTotal,
		GatewayLatencySeconds,
	)
}

// Handler exposes the /metrics endpoint for Prometheus to scrape.
func Handler() http.Handler {
	return promhttp.Handler()
}

// Middleware measures gateway latency for each HTTP request.
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// capture status code
		rec := &statusRecorder{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(rec, r)

		duration := time.Since(start).Seconds()

		path := r.URL.Path
		method := r.Method
		status := strconv.Itoa(rec.statusCode)

		GatewayLatencySeconds.
			WithLabelValues(path, method, status).
			Observe(duration)
	})
}

type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.statusCode = code
	r.ResponseWriter.WriteHeader(code)
}
