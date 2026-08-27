package provider

import (
	"context"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// DialSettings is the user-tunable transport configuration for one provider.
// It is persisted per provider (config.json "providers.<id>.dialer") and
// resolved into a DialConfig when a Client is built. Zero-valued fields mean
// "use the built-in default", so settings layers can inherit sparsely.
type DialSettings struct {
	// RequestTimeoutSeconds caps the entire request/response exchange,
	// including streaming. 0 means no overall timeout for chat; model
	// listing always applies a bounded fallback.
	RequestTimeoutSeconds int `json:"request_timeout_seconds,omitempty"`
	// ConnectTimeoutSeconds bounds TCP/TLS establishment per attempt.
	ConnectTimeoutSeconds int `json:"connect_timeout_seconds,omitempty"`
	// MaxAttempts is the total number of tries per request, including the
	// first. 0 means the default (10).
	MaxAttempts int `json:"max_attempts,omitempty"`
	// MaxBackoffSeconds caps the exponential retry delay. 0 means the
	// default (120s).
	MaxBackoffSeconds int `json:"max_backoff_seconds,omitempty"`
}

// DialConfig is the resolved transport configuration used by Client. All
// values are final — no zero-means-default semantics.
type DialConfig struct {
	RequestTimeout time.Duration
	ConnectTimeout time.Duration
	MaxAttempts    int
	BaseBackoff    time.Duration
	MaxBackoff     time.Duration
	// MaxResponseBytes bounds a fully-buffered response body (non-streaming
	// JSON and error bodies).
	MaxResponseBytes int64
}

// Defaults for DialConfig fields.
const (
	DefaultMaxAttempts       = 10
	DefaultMaxBackoff        = 2 * time.Minute
	DefaultConnectTimeout    = 30 * time.Second
	DefaultBaseBackoff       = 500 * time.Millisecond
	DefaultRequestTimeout    = 0 // streaming turns may legitimately run for minutes
	DefaultMaxResponseBytes  = 64 << 20
	listingRequestTimeout    = 30 * time.Second
	defaultHTTPClientTimeout = 0 // overall timeout is enforced via context, not transport
)

// DefaultDialConfig returns the built-in transport configuration.
func DefaultDialConfig() DialConfig {
	return DialConfig{
		RequestTimeout:   DefaultRequestTimeout,
		ConnectTimeout:   DefaultConnectTimeout,
		MaxAttempts:      DefaultMaxAttempts,
		BaseBackoff:      DefaultBaseBackoff,
		MaxBackoff:       DefaultMaxBackoff,
		MaxResponseBytes: DefaultMaxResponseBytes,
	}
}

// resolve merges user-tunable settings over the defaults. Values <= 0 fall
// back to the default; MaxAttempts is clamped to at least 1.
func (s DialSettings) resolve() DialConfig {
	cfg := DefaultDialConfig()
	if s.RequestTimeoutSeconds > 0 {
		cfg.RequestTimeout = time.Duration(s.RequestTimeoutSeconds) * time.Second
	}
	if s.ConnectTimeoutSeconds > 0 {
		cfg.ConnectTimeout = time.Duration(s.ConnectTimeoutSeconds) * time.Second
	}
	if s.MaxAttempts > 0 {
		cfg.MaxAttempts = s.MaxAttempts
	}
	if s.MaxBackoffSeconds > 0 {
		cfg.MaxBackoff = time.Duration(s.MaxBackoffSeconds) * time.Second
	}
	return cfg
}

// isRetryableStatus reports whether an HTTP status warrants another attempt.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooEarly, http.StatusTooManyRequests:
		return true
	default:
		return code >= 500
	}
}

// backoffDelay returns the exponential backoff with jitter for an attempt
// (1-based). The first retry waits a randomized sub-2s interval to avoid
// synchronized hammering; later attempts double up to MaxBackoff.
func backoffDelay(attempt int, cfg DialConfig) time.Duration {
	if attempt <= 1 {
		return DefaultBaseBackoff + time.Duration(rand.Intn(1500))*time.Millisecond
	}
	base := time.Duration(1<<min(attempt-1, 6)) * time.Second
	jitter := time.Duration(rand.Intn(max(1, int(base/4))))
	return min(base+jitter, cfg.MaxBackoff)
}

// serverHintedDelay returns the delay the server asked for via
// Retry-After / retry-after-ms, falling back to the computed backoff.
func serverHintedDelay(resp *http.Response, attempt int, cfg DialConfig) time.Duration {
	if resp != nil {
		if value := strings.TrimSpace(resp.Header.Get("retry-after-ms")); value != "" {
			if milliseconds, err := strconv.ParseFloat(value, 64); err == nil && milliseconds >= 0 {
				return min(time.Duration(milliseconds*float64(time.Millisecond)), cfg.MaxBackoff)
			}
		}
		if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds >= 0 {
				return min(time.Duration(seconds*float64(time.Second)), cfg.MaxBackoff)
			}
			if when, err := http.ParseTime(value); err == nil {
				return min(max(0, time.Until(when)), cfg.MaxBackoff)
			}
		}
	}
	return backoffDelay(attempt, cfg)
}

// waitForRetry blocks for delay or until ctx is cancelled.
func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
