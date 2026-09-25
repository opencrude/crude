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
	// first. 0 means the default (6).
	MaxAttempts int `json:"max_attempts,omitempty"`
	// MaxBackoffSeconds caps the exponential retry delay. 0 means the
	// default (30s). Server Retry-After minimums are respected independently.
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
	DefaultMaxAttempts       = 6
	DefaultMaxBackoff        = 30 * time.Second
	DefaultConnectTimeout    = 30 * time.Second
	DefaultBaseBackoff       = 500 * time.Millisecond
	DefaultRequestTimeout    = 0 // streaming turns may legitimately run for minutes
	DefaultMaxResponseBytes  = 64 << 20
	maxServerRetryDelay      = 2 * time.Minute
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
// (1-based). Retry waits start at the configured base and double up to the
// configured ceiling, with jitter to avoid synchronized retries.
func backoffDelay(attempt int, cfg DialConfig) time.Duration {
	base := min(cfg.BaseBackoff*time.Duration(1<<min(max(0, attempt-1), 16)), cfg.MaxBackoff)
	return base*3/4 + time.Duration(rand.Int63n(max(1, int64(base/4))))
}

// serverHintedDelay returns the delay the server asked for via
// Retry-After / retry-after-ms, falling back to the computed backoff.
// A negative result stops automatic recovery rather than violating an excessive
// server minimum or waiting indefinitely.
func serverHintedDelay(resp *http.Response, attempt int, cfg DialConfig) time.Duration {
	if resp != nil {
		if value := strings.TrimSpace(resp.Header.Get("retry-after-ms")); value != "" {
			if milliseconds, err := strconv.ParseFloat(value, 64); err == nil && milliseconds >= 0 {
				if milliseconds <= float64(maxServerRetryDelay/time.Millisecond) {
					return time.Duration(milliseconds * float64(time.Millisecond))
				}
				return -1
			}
		}
		if value := strings.TrimSpace(resp.Header.Get("Retry-After")); value != "" {
			if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds >= 0 {
				if seconds <= maxServerRetryDelay.Seconds() {
					return time.Duration(seconds * float64(time.Second))
				}
				return -1
			}
			if when, err := http.ParseTime(value); err == nil {
				if delay := max(0, time.Until(when)); delay <= maxServerRetryDelay {
					return delay
				}
				return -1
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
