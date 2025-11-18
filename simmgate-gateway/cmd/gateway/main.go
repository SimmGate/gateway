package main

import (
	"log"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"simmgate-gateway/internal/httpserver"
	"simmgate-gateway/pkg/logging/logging"
)

func main() {
	// Create base logger
	logger := logging.DefaultLogger()
	defer logger.Sync()

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	r := chi.NewRouter()

	// Setup routes + middleware
	httpserver.SetupRouter(r, logger)

	srv := &http.Server{
		Addr:              ":" + port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("starting gateway")

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}
