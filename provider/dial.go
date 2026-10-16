package provider

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"crude/network"
	"crude/retry"
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
	// ConnectTimeoutSeconds bounds each TCP connection and TLS handshake.
	ConnectTimeoutSeconds int `json:"connect_timeout_seconds,omitempty"`
	// ProgressStartupTimeoutSeconds bounds silence before the first byte in
	// each upload/header/download phase. The adaptive floor and ceiling apply
	// once positive byte progress has been observed.
	ProgressStartupTimeoutSeconds int `json:"progress_startup_timeout_seconds,omitempty"`
	ProgressTimeoutFloorSeconds   int `json:"progress_timeout_floor_seconds,omitempty"`
	ProgressTimeoutCeilingSeconds int `json:"progress_timeout_ceiling_seconds,omitempty"`
	// MaxAttempts is the total number of tries per request, including the
	// first. 0 means the default (6).
	MaxAttempts int `json:"max_attempts,omitempty"`
	// MaxBackoffSeconds caps the exponential retry delay. 0 means the
	// default (30s). Every cooldown, including server advice, is at most 5m.
	MaxBackoffSeconds int `json:"max_backoff_seconds,omitempty"`
}

// DialConfig is the resolved transport configuration used by Client. All
// values are final; there are no zero-means-default semantics.
type DialConfig struct {
	RequestTimeout         time.Duration
	ConnectTimeout         time.Duration
	TLSHandshakeTimeout    time.Duration
	ProgressStartupTimeout time.Duration
	ProgressTimeoutFloor   time.Duration
	ProgressTimeoutCeiling time.Duration
	MaxAttempts            int
	BaseBackoff            time.Duration
	MaxBackoff             time.Duration
	// MaxResponseBytes bounds a fully-buffered response body (non-streaming
	// JSON and error bodies).
	MaxResponseBytes int64
}

// Defaults for DialConfig fields.
const (
	DefaultMaxAttempts            = 6
	DefaultMaxBackoff             = 30 * time.Second
	DefaultConnectTimeout         = 30 * time.Second
	DefaultProgressStartupTimeout = 60 * time.Second
	DefaultProgressTimeoutFloor   = 45 * time.Second
	DefaultProgressTimeoutCeiling = 5 * time.Minute
	DefaultBaseBackoff            = 500 * time.Millisecond
	DefaultRequestTimeout         = 0 // streaming turns may legitimately run for minutes
	DefaultMaxResponseBytes       = 64 << 20
	listingRequestTimeout         = 30 * time.Second
	defaultHTTPClientTimeout      = 0 // overall timeout is enforced via context, not transport
)

// DefaultDialConfig returns the built-in transport configuration.
func DefaultDialConfig() DialConfig {
	networkDefaults := network.DefaultSettings()
	return DialConfig{
		RequestTimeout:         DefaultRequestTimeout,
		ConnectTimeout:         DefaultConnectTimeout,
		TLSHandshakeTimeout:    time.Duration(networkDefaults.TLSHandshakeTimeoutSeconds) * time.Second,
		ProgressStartupTimeout: DefaultProgressStartupTimeout,
		ProgressTimeoutFloor:   DefaultProgressTimeoutFloor,
		ProgressTimeoutCeiling: DefaultProgressTimeoutCeiling,
		MaxAttempts:            DefaultMaxAttempts,
		BaseBackoff:            DefaultBaseBackoff,
		MaxBackoff:             DefaultMaxBackoff,
		MaxResponseBytes:       DefaultMaxResponseBytes,
	}
}

// resolve merges user-tunable settings over the defaults. Values <= 0 fall
// back to the default; MaxAttempts is clamped to at least 1.
func (s DialSettings) resolve() DialConfig {
	cfg := DefaultDialConfig()
	defaults := network.Current()
	cfg.ConnectTimeout = time.Duration(defaults.ConnectTimeoutSeconds) * time.Second
	cfg.TLSHandshakeTimeout = time.Duration(defaults.TLSHandshakeTimeoutSeconds) * time.Second
	if s.RequestTimeoutSeconds > 0 {
		cfg.RequestTimeout = time.Duration(s.RequestTimeoutSeconds) * time.Second
	}
	if s.ConnectTimeoutSeconds > 0 {
		cfg.ConnectTimeout = time.Duration(s.ConnectTimeoutSeconds) * time.Second
		cfg.TLSHandshakeTimeout = cfg.ConnectTimeout
	}
	if s.ProgressStartupTimeoutSeconds > 0 {
		cfg.ProgressStartupTimeout = time.Duration(s.ProgressStartupTimeoutSeconds) * time.Second
	}
	if s.ProgressTimeoutFloorSeconds > 0 {
		cfg.ProgressTimeoutFloor = time.Duration(s.ProgressTimeoutFloorSeconds) * time.Second
	}
	if s.ProgressTimeoutCeilingSeconds > 0 {
		cfg.ProgressTimeoutCeiling = time.Duration(s.ProgressTimeoutCeilingSeconds) * time.Second
	}
	if s.MaxAttempts > 0 {
		cfg.MaxAttempts = s.MaxAttempts
	}
	if s.MaxBackoffSeconds > 0 {
		cfg.MaxBackoff = time.Duration(min(s.MaxBackoffSeconds, int(retry.MaxCooldown/time.Second))) * time.Second
	}
	return cfg
}

// Validate rejects conflicting watchdog limits rather than silently clamping
// a configured timeout to a different value.
func (s DialSettings) Validate() error {
	if s.ConnectTimeoutSeconds < 0 || s.ConnectTimeoutSeconds > 300 {
		return fmt.Errorf("connect timeout override must be 1-300 seconds (0 inherits separate Network budgets)")
	}
	if s.MaxBackoffSeconds < 0 || s.MaxBackoffSeconds > int(retry.MaxCooldown/time.Second) {
		return fmt.Errorf("maximum retry cooldown must be between 1 and 300 seconds (0 uses the default)")
	}
	return s.resolve().progressTimeouts().validate()
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
	return retry.Jitter(attempt, cfg.BaseBackoff, cfg.MaxBackoff)
}

// serverHintedDelay returns the delay the server asked for via
// Retry-After / retry-after-ms, falling back to the computed backoff.
// All hints obey the same absolute ceiling as every other automatic retry.
func serverHintedDelay(resp *http.Response, attempt int, cfg DialConfig) time.Duration {
	return retry.ServerDelay(resp, backoffDelay(attempt, cfg), time.Now())
}

// waitForRetry blocks for delay or until ctx is cancelled.
func waitForRetry(ctx context.Context, delay time.Duration) error {
	return retry.Wait(ctx, delay)
}
