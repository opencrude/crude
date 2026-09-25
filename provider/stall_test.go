package provider

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestProgressEstimatorAdaptsAndClamps(t *testing.T) {
	estimator := progressEstimator{policy: progressTimeouts{
		Startup: 60 * time.Millisecond, Floor: 20 * time.Millisecond,
		Ceiling: 200 * time.Millisecond, Margin: 5 * time.Millisecond,
	}}
	if got := estimator.timeout(); got != 60*time.Millisecond {
		t.Fatalf("startup timeout=%s", got)
	}
	for _, gap := range []time.Duration{10, 12, 11, 13} {
		estimator.observe(gap * time.Millisecond)
	}
	if got := estimator.timeout(); got < 20*time.Millisecond || got > 80*time.Millisecond {
		t.Fatalf("adapted timeout=%s", got)
	}
	estimator.observe(time.Second)
	if got := estimator.timeout(); got > 200*time.Millisecond {
		t.Fatalf("ceiling not enforced: %s", got)
	}
}

func TestProgressWatchKeepsSlowMovingTransferAlive(t *testing.T) {
	watch, ctx := newProgressWatch(context.Background(), progressTimeouts{
		Startup: 35 * time.Millisecond, Floor: 30 * time.Millisecond,
		Ceiling: 100 * time.Millisecond, Margin: 5 * time.Millisecond,
	})
	defer watch.stop()
	for i := int64(1); i <= 8; i++ {
		time.Sleep(15 * time.Millisecond)
		watch.progress(i)
		if err := context.Cause(ctx); err != nil {
			t.Fatalf("slow moving transfer was killed: %v", err)
		}
	}
}

func TestProgressWatchCancelsGenuineStall(t *testing.T) {
	watch, ctx := newProgressWatch(context.Background(), progressTimeouts{
		Startup: 20 * time.Millisecond, Floor: 20 * time.Millisecond,
		Ceiling: 40 * time.Millisecond, Margin: time.Millisecond,
	})
	defer watch.stop()
	select {
	case <-ctx.Done():
		var stalled *ProgressStallError
		if !errors.As(context.Cause(ctx), &stalled) || stalled.Phase != "upload" {
			t.Fatalf("cause=%v", context.Cause(ctx))
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("stalled transfer was not cancelled")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return fn(request) }

func TestStreamRetriesUploadBlackholeWithoutRST(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			buffer := make([]byte, 1)
			_, _ = request.Body.Read(buffer)
			<-request.Context().Done()
			return nil, request.Context().Err()
		}
		_, _ = io.Copy(io.Discard, request.Body)
		return sseResponse("ok"), nil
	})
	stream := testStallStream(t, transport)
	defer stream.Close()
	update, err := stream.Next()
	if err != nil || update.ContentDelta != "ok" || calls.Load() != 2 {
		t.Fatalf("update=%#v err=%v calls=%d", update, err, calls.Load())
	}
}

func TestStreamRetriesDownloadBlackholeWithoutRST(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: &blockingResponseBody{ctx: request.Context()}, Request: request,
			}, nil
		}
		return sseResponse("ok"), nil
	})
	stream := testStallStream(t, transport)
	defer stream.Close()
	update, err := stream.Next()
	if err != nil || update.ContentDelta != "ok" || calls.Load() != 2 {
		t.Fatalf("update=%#v err=%v calls=%d", update, err, calls.Load())
	}
}

func TestStreamNeverReplaysAcceptedPartialOutput(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"par\"}}]}\n\n"
			return &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: &prefixThenBlockBody{ctx: request.Context(), prefix: []byte(prefix)}, Request: request,
			}, nil
		}
		return sseResponse("partial-ok"), nil
	})
	stream := testStallStream(t, transport)
	defer stream.Close()
	update, err := stream.Next()
	if err != nil || update.ContentDelta != "par" {
		t.Fatalf("partial update=%+v err=%v", update, err)
	}
	_, err = stream.Next()
	var stalled *ProgressStallError
	if !errors.As(err, &stalled) || calls.Load() != 1 {
		t.Fatalf("accepted output replayed: err=%v calls=%d", err, calls.Load())
	}
}

func TestStreamDoesNotRequestDivergentPartialReplay(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			prefix := "data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\n"
			return &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
				Body: &prefixThenBlockBody{ctx: request.Context(), prefix: []byte(prefix)}, Request: request,
			}, nil
		}
		return sseResponse("different"), nil
	})
	stream := testStallStream(t, transport)
	defer stream.Close()
	if _, err := stream.Next(); err != nil {
		t.Fatal(err)
	}
	_, err := stream.Next()
	var stalled *ProgressStallError
	if !errors.As(err, &stalled) || calls.Load() != 1 {
		t.Fatalf("divergent retry error=%v", err)
	}
}

func TestCompleteRetriesDownloadBlackholeWithoutRST(t *testing.T) {
	var calls atomic.Int32
	transport := roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if calls.Add(1) == 1 {
			return &http.Response{
				StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: &blockingResponseBody{ctx: request.Context()}, Request: request,
			}, nil
		}
		body := `{"id":"r","model":"m","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`
		return &http.Response{
			StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}},
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	p := FromPreset("openai-chat", "key", "https://stall.invalid")
	p.SetDialSettings(DialSettings{MaxAttempts: 2})
	client, err := p.Client(
		WithHTTPClient(&http.Client{Transport: transport}),
		WithProgressTimeouts(20*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	client.dial.BaseBackoff, client.dial.MaxBackoff = time.Millisecond, time.Millisecond
	response, err := client.Complete(t.Context(), &ChatRequest{Model: "m", Messages: []ChatMessage{NewTextMessage("user", "hi")}})
	if err != nil || response == nil || response.Choices[0].Message.Content.String() != "ok" || calls.Load() != 2 {
		t.Fatalf("response=%#v err=%v calls=%d", response, err, calls.Load())
	}
}

type blockingResponseBody struct {
	ctx context.Context
}

func (b *blockingResponseBody) Read([]byte) (int, error) {
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b *blockingResponseBody) Close() error { return nil }

type prefixThenBlockBody struct {
	ctx    context.Context
	prefix []byte
	offset int
}

func (b *prefixThenBlockBody) Read(buffer []byte) (int, error) {
	if b.offset < len(b.prefix) {
		n := copy(buffer, b.prefix[b.offset:])
		b.offset += n
		return n, nil
	}
	<-b.ctx.Done()
	return 0, b.ctx.Err()
}
func (b *prefixThenBlockBody) Close() error { return nil }

func testStallStream(t *testing.T, transport http.RoundTripper) *ChatStream {
	t.Helper()
	p := FromPreset("openai-chat", "key", "https://stall.invalid")
	p.SetDialSettings(DialSettings{MaxAttempts: 2})
	client, err := p.Client(
		WithHTTPClient(&http.Client{Transport: transport}),
		WithProgressTimeouts(20*time.Millisecond, 20*time.Millisecond, 50*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	client.dial.BaseBackoff = time.Millisecond
	client.dial.MaxBackoff = time.Millisecond
	stream, err := client.Stream(t.Context(), &ChatRequest{
		Model: "m", Messages: []ChatMessage{NewTextMessage("user", strings.Repeat("payload", 64))},
	})
	if err != nil {
		t.Fatal(err)
	}
	return stream
}

func sseResponse(content string) *http.Response {
	payload := fmt.Sprintf("data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", content)
	return &http.Response{
		StatusCode: 200,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(payload)),
	}
}
