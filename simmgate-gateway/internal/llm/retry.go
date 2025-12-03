package llm

import (
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
)

func init() {
	// Seed random for jitter
	// Note: In Go 1.20+, rand is automatically seeded
	// This is for backwards compatibility
	rand.Seed(time.Now().UnixNano())
}

// doWithRetry wraps an HTTP call with retry logic.
// It will attempt the request up to MaxRetries+1 times (initial + retries).
// - Retries only on transient network errors, 429, and 5xx statuses.
// - Respects Retry-After headers from rate limiting responses.
// - Uses exponential backoff with full jitter to prevent thundering herd.
// - Respects the provided ctx (deadline / cancellation).
func (c *client) doWithRetry(
	ctx context.Context,
	body []byte,
	do func(ctx context.Context, body []byte) (*http.Response, error),
) (*http.Response, error) {
	var lastErr error
	maxAttempts := c.cfg.MaxRetries + 1
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	for attempt := 0; attempt < maxAttempts; attempt++ {
		// Check context before attempting
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		start := time.Now()
		resp, err := do(ctx, body)
		duration := time.Since(start)

		status := 0
		if resp != nil {
			status = resp.StatusCode
		}

		c.logger.Debug("llm upstream request",
			zap.Int("attempt", attempt+1),
			zap.Int("max_attempts", maxAttempts),
			zap.Int("status", status),
			zap.Duration("duration", duration),
			zap.Error(err),
		)

		// Handle errors
		if err != nil {
			// Context errors → never retry
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				c.logger.Debug("request cancelled or timed out",
					zap.Error(err),
				)
				return nil, err
			}

			// Check if error is retryable
			if !isTransientNetError(err) {
				c.logger.Debug("non-retryable network error",
					zap.Error(err),
				)
				return nil, err
			}

			// Transient error - will retry
			lastErr = err
			c.logger.Debug("transient network error, will retry",
				zap.Error(err),
			)
		} else if !shouldRetryStatus(status) {
			// Success or non-retryable HTTP status (e.g., 4xx)
			c.logger.Debug("request completed successfully or with client error",
				zap.Int("status", status),
			)
			return resp, nil
		} else {
			// Retryable HTTP status (429, 5xx)
			lastErr = fmt.Errorf("upstream status %d", status)
			c.logger.Debug("retryable status code",
				zap.Int("status", status),
			)

			// Check for Retry-After header before closing body
			retryAfter := parseRetryAfter(resp)

			// Important: close body before retrying so connection can be reused
			if resp != nil && resp.Body != nil {
				resp.Body.Close()
			}

			// Honor Retry-After if present
			if retryAfter > 0 && attempt < maxAttempts-1 {
				c.logger.Info("honoring Retry-After header",
					zap.Duration("wait", retryAfter),
					zap.Int("status", status),
				)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(retryAfter):
					continue
				}
			}
		}

		// No more attempts left
		if attempt == maxAttempts-1 {
			c.logger.Debug("no more retry attempts remaining")
			break
		}

		// Compute backoff with full jitter
		backoff := computeBackoff(c.cfg.BaseBackoff, attempt)
		c.logger.Debug("backing off before retry",
			zap.Duration("backoff", backoff),
			zap.Int("next_attempt", attempt+2),
		)

		// Wait for backoff period, respecting context cancellation
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(backoff):
			// Continue to next attempt
		}
	}

	// All retries exhausted
	c.logger.Warn("llm request exhausted all retries",
		zap.Int("attempts", maxAttempts),
		zap.Error(lastErr),
	)

	if lastErr == nil {
		lastErr = errors.New("unknown upstream error")
	}
	return nil, fmt.Errorf("llmclient: max retries (%d) exceeded: %w", maxAttempts, lastErr)
}

// isTransientNetError determines whether a network error is worth retrying.
// Returns true for temporary network issues that might resolve on retry.
func isTransientNetError(err error) bool {
	if err == nil {
		return false
	}

	// Timeout errors are always retryable
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	// DNS errors with timeout/temporary flag
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return dnsErr.IsTimeout || dnsErr.IsTemporary
	}

	// Connection errors (service might be restarting)
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		// Dial errors are usually retryable (connection refused, etc.)
		if opErr.Op == "dial" {
			return true
		}
		// Read/write errors might also be retryable
		if opErr.Op == "read" || opErr.Op == "write" {
			return true
		}
	}

	// Check error string for common transient patterns
	// This is not ideal but sometimes necessary for wrapped errors
	errStr := strings.ToLower(err.Error())
	transientPatterns := []string{
		"connection refused",
		"connection reset",
		"broken pipe",
		"no such host",
		"temporary failure",
	}

	for _, pattern := range transientPatterns {
		if strings.Contains(errStr, pattern) {
			return true
		}
	}

	return false
}

// shouldRetryStatus returns true if the HTTP status code indicates
// the request should be retried.
func shouldRetryStatus(status int) bool {
	switch {
	case status == 0:
		// No response received (network error)
		return true
	case status == http.StatusTooManyRequests: // 429
		// Rate limited - should retry with backoff
		return true
	case status == http.StatusRequestTimeout: // 408
		// Request timeout - can retry
		return true
	case status >= 500 && status <= 599:
		// Server errors - usually transient
		return true
	default:
		// 2xx success, 3xx redirects, 4xx client errors - don't retry
		return false
	}
}

// parseRetryAfter extracts the retry delay from a Retry-After header.
// Returns 0 if header is missing or invalid.
//
// Retry-After can be:
// - Number of seconds: "120"
// - HTTP date: "Wed, 21 Oct 2015 07:28:00 GMT"
func parseRetryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}

	retryAfter := resp.Header.Get("Retry-After")
	if retryAfter == "" {
		return 0
	}

	// Try parsing as seconds (integer)
	if seconds, err := strconv.Atoi(strings.TrimSpace(retryAfter)); err == nil {
		if seconds > 0 {
			// Cap at a reasonable maximum (e.g., 5 minutes)
			const maxRetryAfter = 5 * 60 // 5 minutes in seconds
			if seconds > maxRetryAfter {
				seconds = maxRetryAfter
			}
			return time.Duration(seconds) * time.Second
		}
	}

	// Try parsing as HTTP date
	if t, err := http.ParseTime(retryAfter); err == nil {
		duration := time.Until(t)
		if duration > 0 {
			// Cap at a reasonable maximum
			const maxRetryAfter = 5 * time.Minute
			if duration > maxRetryAfter {
				duration = maxRetryAfter
			}
			return duration
		}
	}

	return 0
}

// computeBackoff calculates exponential backoff with full jitter.
//
// Full jitter provides the best distribution of retry attempts:
// - Prevents "thundering herd" where all clients retry simultaneously
// - Returns random value between 0 and (base * 2^attempt)
// - Caps the maximum backoff to prevent excessive delays
//
// Example progression (base=100ms):
// Attempt 0: 0-100ms    (avg 50ms)
// Attempt 1: 0-200ms    (avg 100ms)
// Attempt 2: 0-400ms    (avg 200ms)
// Attempt 3: 0-800ms    (avg 400ms)
// ...capped at maxAllowed
func computeBackoff(base time.Duration, attempt int) time.Duration {
	if base <= 0 {
		base = 100 * time.Millisecond
	}

	// Cap the exponent to prevent overflow
	// 2^10 = 1024x multiplier is more than enough
	const maxExponent = 10
	if attempt > maxExponent {
		attempt = maxExponent
	}

	// Calculate exponential backoff: base * 2^attempt
	multiplier := math.Pow(2, float64(attempt))
	exponentialBackoff := float64(base) * multiplier
	maxBackoff := time.Duration(exponentialBackoff)

	// Cap the absolute maximum backoff
	// Even with exponential growth, don't wait longer than this
	const maxAllowed = 60 * time.Second
	if maxBackoff > maxAllowed {
		maxBackoff = maxAllowed
	}

	// Apply full jitter: random value between 0 and maxBackoff
	// This provides optimal distribution and prevents synchronized retries
	jitteredBackoff := time.Duration(rand.Float64() * float64(maxBackoff))

	return jitteredBackoff
}
