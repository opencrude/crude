package provider

import (
	"context"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestStreamRetriesTransientGatewayEventBeforeOutput(t *testing.T) {
	var calls atomic.Int32
	s := testStallStream(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"error\":{\"type\":\"gateway_stream_terminated\",\"message\":\"Upstream stream ended before terminal chunk\"}}\n\n"))}, nil
		}
		return sseResponse("recovered"), nil
	}))
	defer s.Close()
	u, err := s.Next()
	if err != nil || u.ContentDelta != "recovered" || calls.Load() != 2 {
		t.Fatalf("transient stream error killed request: %+v %v requests=%d", u, err, calls.Load())
	}
}

func TestStreamEOFWithoutTerminalIsNotSuccess(t *testing.T) {
	var calls atomic.Int32
	s := testStallStream(t, roundTripFunc(func(*http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("data: {\"choices\":[{\"delta\":{\"content\":\"accepted prefix\"}}]}\n\n"))}, nil
	}))
	defer s.Close()
	if _, err := s.Next(); err != nil {
		t.Fatal(err)
	}
	_, err := s.Next()
	if !errors.Is(err, io.ErrUnexpectedEOF) || calls.Load() != 1 {
		t.Fatalf("truncated stream was accepted or replayed: %v requests=%d", err, calls.Load())
	}
	if _, again := s.Next(); !errors.Is(again, io.EOF) || calls.Load() != 1 {
		t.Fatalf("reading after terminal failure reissued request: %v requests=%d", again, calls.Load())
	}
}

func TestExplicitRetryHeadersAndPermanentQuota(t *testing.T) {
	for _, tc := range []struct {
		status  int
		headers http.Header
		body    string
		want    bool
	}{
		{503, http.Header{"X-Should-Retry": []string{"false"}}, `{"error":{"type":"server_error"}}`, false},
		{429, http.Header{}, `{"error":{"code":"insufficient_quota","message":"billing limit"}}`, false},
		{529, http.Header{}, `{"error":{"type":"overloaded_error"}}`, true},
	} {
		e := readStatusError(&http.Response{StatusCode: tc.status, Header: tc.headers, Body: io.NopCloser(strings.NewReader(tc.body))}, 1<<20)
		if got := e.Retryable(); got != tc.want {
			t.Errorf("status %d retryable=%v want=%v", tc.status, got, tc.want)
		}
	}
	cfg := DefaultDialConfig()
	cfg.MaxBackoff = time.Second
	if got := serverHintedDelay(&http.Response{Header: http.Header{"Retry-After-Ms": []string{"1250"}}}, 1, cfg); got != 1250*time.Millisecond {
		t.Fatalf("server minimum retry delay shortened: %s", got)
	}
}

func TestRecoveryClassificationIsSpecific(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&net.OpError{Op: "read", Net: "tcp", Err: syscall.ECONNRESET}, true},
		{&net.DNSError{Err: "server misbehaving", IsTemporary: true}, true},
		{&net.DNSError{Err: "no such host", IsNotFound: true}, false},
		{x509.UnknownAuthorityError{}, false},
		{context.Canceled, false},
		{context.DeadlineExceeded, true},
		{&APIError{Type: "invalid_request_error", Message: "invalid tool schema"}, false},
		{&APIError{Code: "insufficient_quota"}, false},
		{&APIError{Type: "overloaded_error"}, true},
		{&APIError{Code: "502"}, true},
		{&APIError{Code: "401"}, false},
	} {
		if got := Retryable(tc.err); got != tc.want {
			t.Errorf("%T: retryable=%v want=%v", tc.err, got, tc.want)
		}
	}
	cfg := DefaultDialConfig()
	if got := serverHintedDelay(&http.Response{Header: http.Header{"Retry-After": []string{"900"}}}, 1, cfg); got >= 0 {
		t.Fatalf("long server cooldown shortened to %s instead of stopping", got)
	}
	if got := backoffDelay(1, DialConfig{BaseBackoff: time.Millisecond, MaxBackoff: time.Millisecond}); got > time.Millisecond {
		t.Fatalf("backoff ignored its configured ceiling: %s", got)
	}
}
