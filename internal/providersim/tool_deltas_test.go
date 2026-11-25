package providersim

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestToolArgumentDeltas(t *testing.T) {
	// Split the serialized JSON inside a string, an escape, and a Unicode
	// escape sequence. Individual deltas need not be valid JSON documents.
	first := []string{`{"patch":"pre`, `fix\`, `nλ\u0`, "", `0e9"}`}
	second := []string{`{ "x":`, `2 }`}
	for _, tc := range []struct {
		wire  Wire
		after int // Through the second argument delta, with no text or reasoning.
	}{{Chat, 4}, {Responses, 5}, {Codex, 5}, {Messages, 5}, {Interactions, 4}} {
		t.Run(string(tc.wire), func(t *testing.T) {
			s := New(t)
			g := NewGate()
			defer g.Release()
			turn := Turn{}.WithToolArgumentDeltas("p1", "patch", first...).
				WithToolArgumentDeltas("p2", "other", second...)
			s.Script(turn.WithGateAfter(tc.after, g))
			var written atomic.Int64
			probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.http.Config.Handler.ServeHTTP(observedWriter{w, &written}, r)
			}))
			t.Cleanup(probe.Close)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			req := turnRequest(t, ctx, s, tc.wire, true)
			req.URL.Host = probe.Listener.Addr().String()
			res := do(t, req)
			if err := g.Wait(ctx); err != nil {
				t.Fatal(err)
			}
			// Read exactly what the server flushed before stopping.
			before := make([]byte, written.Load())
			if _, err := io.ReadFull(res.Body, before); err != nil {
				t.Fatal(err)
			}
			frames := bytes.Split(bytes.TrimSpace(before), []byte("\n\n"))
			last := frames[len(frames)-1]
			if len(frames) != tc.after || !bytes.Contains(last, jsonBytes(first[1])) {
				t.Fatalf("gate did not stop after second argument fragment: %s", before)
			}
			g.Release()
			resCopy := *res
			resCopy.Body = io.NopCloser(io.MultiReader(bytes.NewReader(before), res.Body))
			rows := checkedTurn(t, tc.wire, &resCopy, true, 200)
			var got [2][]string
			for _, row := range rows {
				i, part, found := toolFragment(t, tc.wire, row)
				if found {
					if i < 0 || i >= len(got) {
						t.Fatalf("wrong tool index: %d", i)
					}
					got[i] = append(got[i], part)
				}
			}
			if !slices.Equal(got[0], first) || !slices.Equal(got[1], second) {
				t.Fatalf("changed argument bytes or boundaries: %q", got)
			}
		})
	}
}

func toolFragment(t *testing.T, wire Wire, row map[string]any) (int, string, bool) {
	t.Helper()
	var value any
	key := "index"
	switch wire {
	case Chat:
		if len(googleArray(row["choices"])) > 0 {
			for _, raw := range googleArray(delta(row)["tool_calls"]) {
				call := raw.(map[string]any)
				if call["id"] == nil {
					row, value = call, googleObjectAt(call, "function")["arguments"]
				}
			}
		}
	case Responses, Codex:
		if row["type"] == "response.function_call_arguments.delta" {
			key, value = "output_index", row["delta"]
		}
	case Messages, Interactions:
		d := googleObjectAt(row, "delta")
		switch d["type"] {
		case "input_json_delta":
			value = d["partial_json"]
		case "arguments_delta":
			value = d["arguments"]
		}
	}
	part, ok := value.(string)
	if !ok {
		return 0, "", false
	}
	return numField(t, row, key), part, true
}

func TestToolArgumentDeltaMisuse(t *testing.T) {
	for _, wire := range testWires {
		t.Run(string(wire), func(t *testing.T) {
			reports := make(chan string, 1)
			s := newServer(t, func(msg string, args ...any) { reports <- fmt.Sprintf(msg, args...) }, nil)
			parts, want := []string{`{"p":`, `"π"}`}, "does not stream serialized"
			if wire != Ollama && wire != Gemini {
				// JSON strings are Unicode text. Splitting a UTF-8 scalar would
				// make encoding/json silently replace both halves with U+FFFD.
				const scalar = "π"
				parts, want = []string{`{"p":"` + scalar[:1], scalar[1:] + `"}`}, "valid UTF-8"
			}
			s.Respond(func(Request) Turn { return Turn{}.WithToolArgumentDeltas("call", "read", parts...) })
			checkedTurn(t, wire, do(t, turnRequest(t, t.Context(), s, wire, true)), false, 500)
			select {
			case msg := <-reports:
				if !strings.Contains(msg, want) {
					t.Fatal(msg)
				}
			default:
				t.Fatal("unsupported argument fragments were silently accepted")
			}
		})
	}
}
