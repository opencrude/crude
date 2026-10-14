package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
)

// errHTTP2ConnectionLost reproduces the unexported sentinel the bundled
// net/http HTTP/2 client returns when its connection dies mid-body. It is not
// a net.Error, a syscall code or io.EOF; an allowlist never recognized it.
var errHTTP2ConnectionLost = errors.New("http2: client connection lost")

// failingBody yields one SSE event, then fails the way a dying transport does.
type failingBody struct {
	data []byte
	err  error
}

func (b *failingBody) Read(p []byte) (int, error) {
	if len(b.data) == 0 {
		return 0, b.err
	}
	n := copy(p, b.data)
	b.data = b.data[n:]
	return n, nil
}

func (b *failingBody) Close() error { return nil }

func TestFailureClassRequiresPositiveEvidenceOfPermanence(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want FailureClass
	}{
		{"unknown transport sentinel", errHTTP2ConnectionLost, FailureTransient},
		{"wrapped unknown transport sentinel", fmt.Errorf("read body: %w", errHTTP2ConnectionLost), FailureTransient},
		{"never-seen transport prose", errors.New("proxy: upstream went away"), FailureTransient},
		{"typed stream interruption", &StreamInterruptionError{Kind: StreamInterruptionTransport, Cause: errHTTP2ConnectionLost}, FailureTransient},
		{"gateway 502", &StatusError{StatusCode: 502}, FailureTransient},
		{"authentication status", &StatusError{StatusCode: 401}, FailurePermanent},
		{"validation status", &StatusError{StatusCode: 422}, FailurePermanent},
		{"explicit retry veto", &streamRetryVetoError{cause: errHTTP2ConnectionLost}, FailurePermanent},
		{"identified invalid parameter", &APIError{Type: "api_error", Param: "tools[0]"}, FailurePermanent},
		{"quota", &APIError{Code: "insufficient_quota"}, FailurePermanent},
		{"unsupported value", &APIError{Code: "unsupported_value"}, FailurePermanent},
		{"untrusted certificate", x509.UnknownAuthorityError{}, FailurePermanent},
		{"unparseable endpoint", &url.Error{Op: "parse", URL: "::", Err: errors.New("missing protocol scheme")}, FailurePermanent},
		{"oversized response", fmt.Errorf("%w: response exceeds 10 bytes", ErrResponseTooLarge), FailurePermanent},
		{"known transient verdict", &APIError{Type: "overloaded_error"}, FailureTransient},
		{"unrecognized provider verdict", &APIError{Message: "unrecognized backend failure"}, FailureUnrecognizedVerdict},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(tc.err); got != tc.want {
				t.Fatalf("Classify(%v) = %d, want %d", tc.err, got, tc.want)
			}
			if Permanent(tc.err) != (tc.want == FailurePermanent) {
				t.Fatalf("Permanent(%v) disagrees with its class", tc.err)
			}
		})
	}
	if Permanent(context.Canceled) || Retryable(context.Canceled) {
		t.Fatal("cancellation became a failure to recover")
	}
	if Retryable(&APIError{Message: "unrecognized backend failure"}) {
		t.Fatal("an unrecognized provider verdict was fast-retried inside one request")
	}
	if !Retryable(errHTTP2ConnectionLost) {
		t.Fatal("an unknown transport failure was not retried before output")
	}
}

// TestBodyReadFailureIsATypedTransportInterruption covers the stream boundary:
// once output was accepted, a failed body read surfaces as a typed transport
// interruption (never a bare sentinel), so the agent continues the partial
// response and the supervisor recovers automatically.
func TestBodyReadFailureIsATypedTransportInterruption(t *testing.T) {
	var calls atomic.Int32
	stream := testStallStream(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: 200,
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Body:       &failingBody{data: []byte("data: {\"choices\":[{\"delta\":{\"content\":\"accepted\"}}]}\n\n"), err: errHTTP2ConnectionLost},
		}, nil
	}))
	defer stream.Close()
	if update, err := stream.Next(); err != nil || update.ContentDelta != "accepted" {
		t.Fatalf("partial output = %+v, %v", update, err)
	}
	_, err := stream.Next()
	var interrupted *StreamInterruptionError
	if !errors.As(err, &interrupted) || interrupted.Kind != StreamInterruptionTransport || !errors.Is(err, errHTTP2ConnectionLost) {
		t.Fatalf("body read failure was not typed as a transport interruption: %T %v", err, err)
	}
	if Classify(err) != FailureTransient || calls.Load() != 1 {
		t.Fatalf("accepted output was replayed or misclassified: class=%d requests=%d", Classify(err), calls.Load())
	}
}

func TestBodyReadFailureBeforeOutputRetriesTransparently(t *testing.T) {
	var calls atomic.Int32
	stream := testStallStream(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       &failingBody{err: errHTTP2ConnectionLost},
			}, nil
		}
		return sseResponse("recovered"), nil
	}))
	defer stream.Close()
	update, err := stream.Next()
	if err != nil || update.ContentDelta != "recovered" || calls.Load() != 2 {
		t.Fatalf("lost connection before output was not retried: %+v %v requests=%d", update, err, calls.Load())
	}
}

func TestOversizedStreamFrameIsNeverRetried(t *testing.T) {
	var calls atomic.Int32
	stream := testStallStream(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		huge := "data: " + strings.Repeat("x", 64<<20) + "\n\n"
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(huge))}, nil
	}))
	defer stream.Close()
	if _, err := stream.Next(); !errors.Is(err, ErrResponseTooLarge) || Retryable(err) || calls.Load() != 1 {
		t.Fatalf("oversized frame: err=%v retryable=%v requests=%d", err, Retryable(err), calls.Load())
	}
}
