package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Do not let VCR_DEBUG enable header/body tracing during a capture.
type silent struct{}

func (silent) Write(p []byte) (int, error) { return len(p), nil }
func readBounded(r io.Reader) ([]byte, error) {
	p, err := io.ReadAll(io.LimitReader(r, maxBytes+1))
	if err != nil || len(p) > maxBytes {
		return nil, errors.New("body is incomplete or exceeds 1 MiB")
	}
	return p, nil
}
func streamType(value string) string {
	mt, _, err := mime.ParseMediaType(value)
	if err != nil {
		return ""
	}
	return mt
}
func bounded(t http.RoundTripper) http.RoundTripper {
	return roundTrip(func(r *http.Request) (*http.Response, error) {
		resp, err := t.RoundTrip(r)
		if err != nil {
			return nil, errors.New("model transport failed")
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("model returned HTTP %d; no cassette saved", resp.StatusCode)
		}
		p, err := readBounded(resp.Body)
		if err != nil {
			return nil, err
		}
		resp.Body = io.NopCloser(bytes.NewReader(p))
		return resp, nil
	})
}
func canonicalRequest(r *http.Request) (cassette.Request, error) {
	raw, err := readBounded(r.Body)
	if err != nil {
		return cassette.Request{}, err
	}
	_ = r.Body.Close()
	if err = r.ParseForm(); err != nil {
		return cassette.Request{}, err
	}
	return cassette.Request{Proto: r.Proto, ProtoMajor: r.ProtoMajor, ProtoMinor: r.ProtoMinor, Method: r.Method, URL: r.URL.String(), Host: r.Host, Headers: r.Header, Body: string(raw), ContentLength: int64(len(raw)), Form: r.Form}, nil
}
func record(ctx context.Context, dir string, s scenario, base, model, key string, t http.RoundTripper) error {
	r, err := s.request(ctx, base, model, key)
	if err != nil {
		return err
	}
	ref, err := canonicalRequest(s.canonical(ctx))
	if err != nil {
		return err
	}
	expected, err := r.GetBody()
	if err != nil {
		return err
	}
	raw, err := readBounded(expected)
	_ = expected.Close()
	if err != nil {
		return err
	}
	expectedHeaders := r.Header.Clone()
	if err = os.Mkdir(dir, 0700); err != nil {
		return fmt.Errorf("select a new output directory: %w", err)
	}
	hook := func(i *cassette.Interaction) error {
		if i.Request.Method != r.Method || i.Request.URL != r.URL.String() || i.Request.Body != string(raw) || !reflect.DeepEqual(i.Request.Headers, expectedHeaders) {
			return errors.New("request differs from the fixed synthetic scenario")
		}
		if i.Response.Code != 200 || streamType(i.Response.Headers.Get("Content-Type")) != s.media {
			return errors.New("wrong status or stream media type")
		}
		frames, err := parse(s, i.Response.Body)
		if err != nil {
			return err
		}
		if _, err = inspectTurn(s, frames); err != nil {
			return err
		}
		body := marshalFrames(s, frames)
		if key != "" && strings.Contains(body, key) {
			return errors.New("credential found in the sanitized response")
		}
		i.Request = ref
		i.Response = cassette.Response{Code: 200, Status: "200 OK", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1, Headers: http.Header{"Content-Type": {s.media}}, Body: body, ContentLength: int64(len(body))}
		return nil
	}
	rec, err := recorder.New(filepath.Join(dir, s.name), recorder.WithMode(recorder.ModeRecordOnly), recorder.WithRealTransport(bounded(t)), recorder.WithHook(hook, recorder.AfterCaptureHook), recorder.WithDebugWriter(silent{}), recorder.WithSkipRequestLatency(true))
	if err != nil {
		return err
	}
	resp, err := rec.RoundTrip(r)
	if err != nil {
		return err
	}
	_ = resp.Body.Close()
	return rec.Stop()
}
func playback(dir string, s scenario) (*recorder.Recorder, error) {
	name := filepath.Join(dir, s.name)
	info, err := os.Stat(cassette.New(name).File)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > 4*maxBytes {
		return nil, errors.New("invalid cassette file")
	}
	return recorder.New(name, recorder.WithMode(recorder.ModeReplayOnly), recorder.WithSkipRequestLatency(true), recorder.WithDebugWriter(silent{}), recorder.WithRealTransport(roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("network disabled for playback") })))
}
func replay(ctx context.Context, dir string, s scenario) ([]frame, error) {
	rec, err := playback(dir, s)
	if err != nil {
		return nil, err
	}
	defer rec.Stop()
	resp, err := rec.RoundTrip(s.canonical(ctx))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || streamType(resp.Header.Get("Content-Type")) != s.media {
		return nil, errors.New("recorded status or media type changed")
	}
	p, err := readBounded(resp.Body)
	if err != nil {
		return nil, err
	}
	frames, err := parse(s, string(p))
	if err != nil {
		return nil, err
	}
	_, err = inspectTurn(s, frames)
	return frames, err
}

// The fixture server never forwards headers, loads credentials or passes through
// a miss. It serves only the exact recorded synthetic operation and request.
func handler(s scenario, frames []frame) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		expected := s.canonical(r.Context())
		ref, _ := canonicalRequest(expected)
		op := expected.URL.RequestURI()
		if r.Method != expected.Method || r.URL.RequestURI() != op || streamType(r.Header.Get("Content-Type")) != "application/json" {
			http.Error(w, "unmatched fixture operation", 400)
			return
		}
		for _, k := range []string{"Api-Revision", "Anthropic-Version", "OpenAI-Beta"} {
			if r.Header.Get(k) != expected.Header.Get(k) {
				http.Error(w, "unmatched fixture header", 400)
				return
			}
		}
		b, err := readBounded(r.Body)
		if err != nil || string(b) != ref.Body {
			http.Error(w, "unmatched fixture request", 400)
			return
		}
		w.Header().Set("Content-Type", s.media)
		for _, f := range frames {
			if _, err := io.WriteString(w, marshalFrames(s, []frame{f})); err != nil {
				return
			}
			if err := http.NewResponseController(w).Flush(); err != nil {
				return
			}
		}
	})
}
