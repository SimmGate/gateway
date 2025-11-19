package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"simmgate-gateway/internal/cache"
	"simmgate-gateway/internal/handlers"
	"simmgate-gateway/internal/httpserver"
	"simmgate-gateway/internal/metrics"
	"simmgate-gateway/pkg/logging/logging"
)

type Config struct {
	Port         string
	CacheBackend string // "memory" or "redis"
	VersionID    string
	RedisAddr    string
}

func LoadConfig() Config {
	return Config{
		Port:         getenv("PORT", "8080"),
		CacheBackend: getenv("CACHE_BACKEND", "memory"),
		VersionID:    getenv("GATEWAY_VERSION", "v1"),
		RedisAddr:    getenv("REDIS_ADDR", "127.0.0.1:6379"),
	}
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("gateway exited with error: %v", err)
	}
}

func run() error {
	// ----- Logger -----
	logger := logging.DefaultLogger()
	defer logger.Sync()

	// ----- Metrics -----
	metrics.Register()

	// ----- Config -----
	cfg := LoadConfig()

	logger.Info("loaded config",
		zap.String("port", cfg.Port),
		zap.String("cache_backend", cfg.CacheBackend),
		zap.String("version_id", cfg.VersionID),
		zap.String("redis_addr", cfg.RedisAddr),
	)

	// ----- Redis client (only if needed) -----
	var redisClient *redis.Client
	if cfg.CacheBackend == "redis" {
		redisClient = redis.NewClient(&redis.Options{
			Addr: cfg.RedisAddr,
		})

		// Fail fast if Redis is misconfigured
		if err := redisClient.Ping(context.Background()).Err(); err != nil {
			logger.Error("redis connection failed", zap.Error(err))
			return err
		}
		logger.Info("redis connection established",
			zap.String("addr", cfg.RedisAddr),
		)
	}

	// ----- Cache (Tier 1 Exact Cache) -----
	cacheCfg := cache.Config{
		Backend: cfg.CacheBackend,
		TTL:     5 * time.Minute,
		Prefix:  "simmgate",
	}
	exactCache := cache.NewExactCache(cacheCfg, redisClient)
	exactCache = cache.NewLoggingExactCache(exactCache)

	// ----- Handlers -----
	chatHandler := handlers.NewChatHandler(
		exactCache,
		cacheCfg.TTL,
		cfg.VersionID,
	)

	// ----- Router + middleware -----
	r := chi.NewRouter()
	httpserver.SetupRouter(r, logger, chatHandler)

	// ----- HTTP server -----
	srv := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	logger.Info("starting gateway",
		zap.String("addr", srv.Addr),
		zap.String("cache_backend", cfg.CacheBackend),
		zap.String("version_id", cfg.VersionID),
	)

	// Start server in background
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", zap.Error(err))
		}
	}()

	// ----- Graceful shutdown -----
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	<-stop
	logger.Info("shutdown signal received")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server shutdown error", zap.Error(err))
		return err
	}

	logger.Info("server shutdown complete")
	return nil
}

// getenv returns the value of the environment variable key or def if not set.
func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
