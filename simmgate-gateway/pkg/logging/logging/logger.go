package logging

import (
	"context"
	"os"
	"sync"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type ctxKey int

// prevents differences when adding new constants
const loggerKey ctxKey = iota

var (
	defaultLogger     *zap.Logger
	defaultLoggerOnce sync.Once
)

// custom logger
func NewLogger() *zap.Logger {
	env := os.Getenv("ENV")

	var config zap.Config

	if env == "dev" || env == "development" {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	} else {
		config = zap.NewProductionConfig()
		//to see who calls it
		config.DisableCaller = false
	}
	//adjustable log level so it can change at runtime
	logLevel := os.Getenv("LOG_LEVEL")

	if logLevel != "" {
		var level zapcore.Level
		err := level.UnmarshalText([]byte(logLevel))

		if err == nil {
			config.Level = zap.NewAtomicLevelAt(level)
		}
	}

	logger, err := config.Build(
		zap.AddCallerSkip(1), // Skip wrapper function in stack trace
	)

	if err != nil {
		_, _ = os.Stderr.WriteString("failed to create logger: " + err.Error() + "\n")
		os.Exit(1)
	}

	return logger

}

// Singleton logger
func DefaultLogger() *zap.Logger {
	defaultLoggerOnce.Do(func() {
		defaultLogger = NewLogger()
	})
	return defaultLogger
}

// attach a logger to context
func WithLogger(ctx context.Context, logger *zap.Logger) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, loggerKey, logger)
}

//retrieve logger from context

func FromContext(ctx context.Context) *zap.Logger {
	if ctx == nil {
		return DefaultLogger()
	}

	logger, ok := ctx.Value(loggerKey).(*zap.Logger)

	if ok && logger != nil {
		return logger
	} else {
		return DefaultLogger()
	}
}

func L(ctx context.Context) *zap.Logger {
	return FromContext(ctx)
}

// WithFields adds structured fields to the logger in context.
func WithFields(ctx context.Context, fields ...zap.Field) context.Context {
	logger := FromContext(ctx).With(fields...)
	return WithLogger(ctx, logger)
}
