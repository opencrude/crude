package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"crude/internal/providersim"
)

func simulated(t *testing.T, s scenario, parts []string) []frame {
	t.Helper()
	sim := providersim.New(t)
	sim.Script(providersim.Turn{}.WithTextDeltas(parts...))
	r, err := s.request(t.Context(), sim.BaseURL(providersim.Wire(s.name)), s.fixtureModel, "")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 || streamType(resp.Header.Get("Content-Type")) != s.media {
		t.Fatalf("%s: %s", s.name, resp.Status)
	}
	raw, err := readBounded(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	frames, err := parse(s, string(raw))
	if err != nil {
		t.Fatal(err)
	}
	return frames
}

// Compare every frame in order, including nested field presence, types,
// discriminators, stop reasons, indexes and ID relationships. Usage and timing
// numbers are observations, not predictions of a provider's tokenizer/clock.
func shape(v any, key string) any {
	switch x := v.(type) {
	case map[string]any:
		m := map[string]any{}
		for k, val := range x {
			m[k] = shape(val, k)
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i, val := range x {
			a[i] = shape(val, key)
		}
		return a
	case float64:
		if key == "sequence_number" || strings.HasSuffix(key, "index") {
			return x
		}
		return "<number>"
	default:
		return v
	}
}
func differs(want, got []frame) error {
	if len(want) != len(got) {
		return fmt.Errorf("frame count: recorded %d, simulated %d", len(want), len(got))
	}
	for i, w := range want {
		g := got[i]
		if w.event != g.event || w.done != g.done || w.id != g.id || !reflect.DeepEqual(shape(w.obj, ""), shape(g.obj, "")) {
			return fmt.Errorf("frame %d differs (recorded %q, simulated %q); inspect the sanitized bodies", i, w.event, g.event)
		}
	}
	return nil
}
func compare(t *testing.T, s scenario, dir string) {
	t.Helper()
	frames, err := replay(t.Context(), dir, s)
	if err != nil {
		t.Fatal(err)
	}
	parts, err := inspectTurn(s, frames)
	if err != nil {
		t.Fatal(err)
	}
	if err = differs(frames, simulated(t, s, parts)); err != nil {
		t.Fatal(err)
	}
}
func TestRecordedTurn(t *testing.T) {
	dir := os.Getenv("CRUDE_FIXTURE_DIR")
	wire := os.Getenv("CRUDE_FIXTURE_WIRE")
	if dir == "" && wire == "" {
		dir = "../../internal/providersim/recordings"
		wire = "chat"
	}
	s, err := lookup(wire)
	if err != nil || dir == "" {
		t.Fatal("supply both CRUDE_FIXTURE_DIR and CRUDE_FIXTURE_WIRE")
	}
	compare(t, s, dir)
}
func TestCaptureAndOfflineReplay(t *testing.T) {
	for _, s := range scenarios {
		t.Run(s.name, func(t *testing.T) {
			sim := providersim.New(t)
			sim.Script(providersim.Turn{}.WithTextDeltas("O", "K"))
			dir := filepath.Join(t.TempDir(), "capture")
			if err := record(t.Context(), dir, s, sim.BaseURL(providersim.Wire(s.name)), s.fixtureModel, "sentinel-key", http.DefaultTransport); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(filepath.Join(dir, s.name+".yaml"))
			if err != nil || bytes.Contains(data, []byte("sentinel-key")) {
				t.Fatal("missing or unsanitized cassette")
			}
			compare(t, s, dir)
		})
	}
}
func TestPrivacyAndReplayBoundary(t *testing.T) {
	s, _ := lookup("chat")
	source, err := replay(t.Context(), "../../internal/providersim/recordings", s)
	if err != nil {
		t.Fatal(err)
	}
	const secret = "PRIVATE-CANARY"
	body := strings.ReplaceAll(marshalFrames(s, source), `"id":"fixture_1"`, `"id":"`+secret+`","encrypted_content":"`+secret+`-opaque"`)
	if !strings.Contains(body, secret) {
		t.Fatal("independent fixture lost id")
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Error("capture did not authenticate at the synthetic target")
		}
		w.Header().Set("Content-Type", sse)
		w.Header().Set("Request-Id", secret)
		w.Header().Set("Set-Cookie", secret)
		_, _ = io.WriteString(w, ": "+secret+"\n\n"+body)
	}))
	defer server.Close()
	dir := filepath.Join(t.TempDir(), "new")
	if err := record(t.Context(), dir, s, server.URL, "private-model", secret, server.Client().Transport); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "chat.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []string{secret, "private-model", server.URL, "Set-Cookie", "Authorization"} {
		if strings.Contains(string(data), v) {
			t.Fatal("cassette contains private data")
		}
	}
	rec, err := playback(dir, s)
	if err != nil {
		t.Fatal(err)
	}
	defer rec.Stop()
	r := s.canonical(t.Context())
	r.Body = io.NopCloser(strings.NewReader(`{"stream":false}`))
	if _, err = rec.RoundTrip(r); err == nil {
		t.Fatal("wrong body matched replay")
	}
	r = s.canonical(t.Context())
	r.URL.RawQuery = "key=unrecognized"
	if _, err = rec.RoundTrip(r); err == nil {
		t.Fatal("wrong URL matched replay")
	}
	r = s.canonical(t.Context())
	r.Header.Set("Accept", "application/json")
	if _, err = rec.RoundTrip(r); err == nil {
		t.Fatal("wrong stream transport matched replay")
	}
	if err = record(t.Context(), dir, s, server.URL, "private-model", secret, server.Client().Transport); err == nil {
		t.Fatal("overwrote an existing directory")
	}
}
func TestMalformedOrPrivateResponseNeverSaved(t *testing.T) {
	s, _ := lookup("chat")
	source, err := replay(t.Context(), "../../internal/providersim/recordings", s)
	if err != nil {
		t.Fatal(err)
	}
	good := marshalFrames(s, source)
	for _, tc := range []struct {
		name, media, body string
		code              int
	}{{"unknown-key", sse, strings.Replace(good, `"created":`, `"PRIVATE-KEY":`, 1), 200}, {"private-text", sse, strings.Replace(good, `"content":"OK"`, `"content":"PRIVATE-VALUE"`, 1), 200}, {"no-terminal", sse, strings.TrimSuffix(good, "data: [DONE]\n\n"), 200}, {"wrong-transport", "application/json", good, 200}, {"error-status", sse, good, 403}} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tc.media)
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer srv.Close()
			dir := filepath.Join(t.TempDir(), "capture")
			err := record(t.Context(), dir, s, srv.URL, s.fixtureModel, "", srv.Client().Transport)
			if err == nil || strings.Contains(err.Error(), "PRIVATE-") {
				t.Fatal("did not safely reject response")
			}
			files, e := os.ReadDir(dir)
			if e != nil || len(files) != 0 {
				t.Fatal("saved rejected capture")
			}
		})
	}
}
func TestComparisonDetectsDrift(t *testing.T) {
	s, _ := lookup("chat")
	ref, err := replay(t.Context(), "../../internal/providersim/recordings", s)
	if err != nil {
		t.Fatal(err)
	}
	for _, fn := range []func([]frame){func(f []frame) { f[0], f[1] = f[1], f[0] }, func(f []frame) { delete(f[0].obj, "id") }, func(f []frame) { first(f[0].obj, "choices")["index"] = 2 }} {
		test, err := parse(s, marshalFrames(s, ref))
		if err != nil {
			t.Fatal(err)
		}
		fn(test)
		if differs(ref, test) == nil {
			t.Fatal("event or required-field drift went undetected")
		}
	}
}
func TestServeAndOptIn(t *testing.T) {
	dir := "../../internal/providersim/recordings"
	s, _ := lookup("chat")
	f, err := replay(t.Context(), dir, s)
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(handler(s, f))
	defer srv.Close()
	r, _ := s.request(t.Context(), srv.URL+s.basePath(), s.fixtureModel, "")
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	got, err := parse(s, string(p))
	if resp.StatusCode != 200 || err != nil || differs(f, got) != nil {
		t.Fatal("served stream changed")
	}
	r, _ = s.request(t.Context(), srv.URL+s.basePath(), "another-model", "")
	resp, err = srv.Client().Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatal("served unmatched request")
	}
	var out bytes.Buffer
	if err = run([]string{"capture", "-wire", "chat", "-dir", dir}, nil, &out); err == nil {
		t.Fatal("enabled live transport without opt-in")
	}
	if err = run([]string{"replay", "-wire", "chat", "-dir", dir}, nil, &out); err != nil || out.String() != marshalFrames(s, f) {
		t.Fatal("offline CLI did not replay synthetic cassette")
	}
}
