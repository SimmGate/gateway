package llm

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"
)

type Config struct {
	//required fields
	BaseURL string
	APIKey  string

	UpstreamTimeout time.Duration // per-request timeout (default: 30s)
	MaxRetries      int           // retry attempts (default: 2)
	BaseBackoff     time.Duration // initial backoff (default: 100ms)

	// Optional connection pool settings
	MaxIdleConns        int // default: 100
	MaxIdleConnsPerHost int // default: 100

	// Custom HTTP client (for testing or special configs)
	HTTPClient *http.Client
}

// Validate checks required fields only.
func (c *Config) Validate() error {
	if c.BaseURL == "" {
		return errors.New("BaseURL is required")
	}
	if c.APIKey == "" {
		return errors.New("APIKey is required")
	}
	return nil
}

// WithDefaults returns a copy of Config with sane defaults applied.
func (c *Config) WithDefaults() Config {
	cfg := *c

	// Normalize BaseURL: trim trailing slashes so we can safely append paths.
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	if cfg.UpstreamTimeout <= 0 {
		cfg.UpstreamTimeout = 30 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 2
	}
	if cfg.BaseBackoff <= 0 {
		cfg.BaseBackoff = 100 * time.Millisecond
	}
	if cfg.MaxIdleConns <= 0 {
		cfg.MaxIdleConns = 100
	}
	if cfg.MaxIdleConnsPerHost <= 0 {
		cfg.MaxIdleConnsPerHost = 100
	}

	return cfg
}

type client struct {
	cfg        Config
	httpClient *http.Client
	logger     *zap.Logger
}

// NewClient creates a new LLM client with the given configuration.
func NewClient(cfg Config, logger *zap.Logger) (Client, error) {
	// Apply defaults + normalize BaseURL
	cfg = cfg.WithDefaults()

	// Validate required fields
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid config: %w", err)
	}

	// Use provided logger or no-op
	if logger == nil {
		logger = zap.NewNop()
	}

	// Use custom HTTP client if provided, otherwise create default
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{
			Transport: defaultTransport(cfg),
		}
	}

	return &client{
		cfg:        cfg,
		httpClient: httpClient,
		logger:     logger.Named("llmclient"),
	}, nil
}

// defaultTransport creates a production-ready HTTP transport
// with connection pooling and reasonable timeouts.
func defaultTransport(cfg Config) *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:     90 * time.Second,

		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// Close releases resources held by the client.
func (c *client) Close() error {
	c.httpClient.CloseIdleConnections()
	return nil
}
