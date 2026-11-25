package providersim

import (
	"context"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// FaultKind identifies a provider-neutral error for a scripted turn.
type FaultKind string

const (
	// RateLimit represents a rate-limit response.
	RateLimit FaultKind = "rate_limit"
	// ContextOverflow represents a request beyond the model's context limit.
	ContextOverflow FaultKind = "context_overflow"
	// Overload represents temporary provider overload.
	Overload FaultKind = "overload"
	// Auth represents a rejected credential.
	Auth FaultKind = "auth"
	// ModelNotFound represents a request for a missing model.
	ModelNotFound FaultKind = "model_not_found"
	// Quota represents exhausted credit or quota, not a short-term rate limit.
	Quota FaultKind = "quota"
	// BadGateway represents a gateway that could not obtain an upstream response.
	BadGateway FaultKind = "bad_gateway"
	// InvalidRequest represents a generic invalid request.
	InvalidRequest FaultKind = "invalid_request"
	// RequestTimeout represents an HTTP 408 request timeout.
	RequestTimeout FaultKind = "request_timeout"
)

// RFC 9110 §15.5.9 defines 408, not provider-specific payloads. Its message
// and per-wire type/code selections are synthetic. Both SDKs retry HTTP 408:
// https://github.com/openai/openai-python#retries
// https://github.com/anthropics/anthropic-sdk-python#retries
const requestTimedOut = "Request timed out."

// RetryHint is an explicit retry hint. The zero value sends no hint.
type RetryHint struct {
	header string
	value  string
}

// RetryAfterSeconds sets Retry-After to seconds.
func RetryAfterSeconds(seconds int) RetryHint {
	return RetryHint{header: "Retry-After", value: strconv.Itoa(seconds)}
}

// RetryAfterMilliseconds sets retry-after-ms to milliseconds.
func RetryAfterMilliseconds(milliseconds int) RetryHint {
	return RetryHint{header: "retry-after-ms", value: strconv.Itoa(milliseconds)}
}

// RetryAfterDate sets Retry-After to an HTTP date.
func RetryAfterDate(date time.Time) RetryHint {
	return RetryHint{header: "Retry-After", value: date.UTC().Format(http.TimeFormat)}
}

type faultMode uint8

const (
	faultNone faultMode = iota
	faultHTTP
	faultStream
	faultCut
)

type fault struct {
	mode    faultMode
	kind    FaultKind
	after   int
	hint    RetryHint
	message *string
}

// WithFaultMessage replaces the human-readable message of t's HTTP or
// in-stream error. Its envelope, code and status do not change.
func (t Turn) WithFaultMessage(message string) Turn {
	t.fault.message = &message
	return t
}

func (f fault) text(fallback string) string {
	if f.message != nil {
		return *f.message
	}
	return fallback
}

// HTTPError builds a turn that returns an HTTP error before any stream starts.
func HTTPError(kind FaultKind) Turn { return Turn{fault: fault{mode: faultHTTP, kind: kind}} }

// WithRetryHint adds an optional retry header to an HTTP error turn.
func (t Turn) WithRetryHint(hint RetryHint) Turn {
	t.fault.hint = hint
	return t
}

// StreamError builds an empty turn with an in-band fault. The after argument
// counts successful events. Use WithStreamError to put content before the fault.
func StreamError(after int, kind FaultKind) Turn { return Turn{}.WithStreamError(after, kind) }

// WithStreamError emits an in-band error after the given number of successful
// events. Zero means the error is the first event.
func (t Turn) WithStreamError(after int, kind FaultKind) Turn {
	t.fault = fault{mode: faultStream, after: after, kind: kind}
	return t
}

// CutStream builds an empty turn that aborts the HTTP stream after the given
// number of successful events. Use WithCut to put content before the cut.
func CutStream(after int) Turn { return Turn{}.WithCut(after) }

// WithCut aborts the HTTP stream after the given number of successful events.
func (t Turn) WithCut(after int) Turn {
	t.fault = fault{mode: faultCut, after: after}
	return t
}

// Gate synchronizes a paused turn with the test. Create it with NewGate and
// attach it to a turn with WithGate. A gate can be released only once.
type Gate struct {
	arrival    chan struct{}
	release    chan struct{}
	canceled   chan struct{}
	arrived    sync.Once
	released   sync.Once
	clientGone sync.Once
}

// NewGate creates a closed gate for a scripted turn.
func NewGate() *Gate {
	return &Gate{arrival: make(chan struct{}), release: make(chan struct{}), canceled: make(chan struct{})}
}

// Wait waits until a request reaches the gate, or ctx is canceled.
func (g *Gate) Wait(ctx context.Context) error {
	select {
	case <-g.arrival:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Release allows the response to continue. Calling it again has no effect.
func (g *Gate) Release() { g.released.Do(func() { close(g.release) }) }

// WaitCanceled returns nil once a request held at the gate ended because its
// client went away before Release. Otherwise it returns ctx's error.
// Server shutdown is not a client cancellation.
func (g *Gate) WaitCanceled(ctx context.Context) error {
	select {
	case <-g.canceled:
		return nil
	case <-ctx.Done():
		select {
		case <-g.canceled:
			return nil
		default:
			return ctx.Err()
		}
	}
}

func (g *Gate) await(ctx context.Context, done <-chan struct{}) bool {
	if g == nil {
		return true
	}
	g.arrived.Do(func() { close(g.arrival) })
	select {
	case <-g.release:
		return true
	case <-ctx.Done():
		// Cleanup closes done before closing client connections. A release
		// or shutdown that already won must not count as a client departure.
		select {
		case <-g.release:
		case <-done:
		default:
			g.clientGone.Do(func() { close(g.canceled) })
		}
		return false
	case <-done:
		return false
	}
}
