package providersim

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var testWires = []Wire{Chat, Responses, Codex, Messages, Gemini, Interactions, Ollama}

// turnRequest uses the smallest valid model operation for each codec. Codex
// only supports streaming. Chat explicitly asks for usage when streaming.
func turnRequest(t *testing.T, ctx context.Context, s *Server, wire Wire, stream bool) *http.Request {
	t.Helper()
	var path, body string
	switch wire {
	case Chat:
		path, body = "/chat/completions", plainChat
		if stream {
			body = strings.TrimSuffix(streamingChat, "}") + `,"stream_options":{"include_usage":true}}`
		}
	case Responses:
		o := testInput("hi")
		o["stream"] = stream
		path, body = "/responses", string(jsonBytes(o))
	case Codex:
		path, body = "/responses", string(jsonBytes(codexInput(responseObject{"type": "message", "role": "user", "content": []any{responseObject{"type": "input_text", "text": "hi"}}})))
	case Messages:
		o := messageBody(messageInput("user", "hi"))
		o["stream"] = stream
		path, body = "/messages", string(jsonBytes(o))
	case Interactions:
		path, body = "/interactions", testInteractInput
		if stream {
			body = testInteractStream
		}
	case Gemini:
		path, body = strings.TrimPrefix(testGeminiJSON, googleAPIVersion), testGeminiInput
		if stream {
			path = strings.TrimPrefix(testGeminiSSE, googleAPIVersion)
		}
	case Ollama:
		path, body = "/chat", ollamaPrompt
		if !stream {
			body = strings.TrimSuffix(body, "}") + `,"stream":false}`
		}
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, s.BaseURL(wire)+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", "application/json")
	return r
}

// Count the server's bytes, not just the client's buffered bytes. Once the
// gate announces arrival, this proves that no part of the next frame exists.
type observedWriter struct {
	http.ResponseWriter
	written *atomic.Int64
}

func (o observedWriter) Unwrap() http.ResponseWriter { return o.ResponseWriter }
func (o observedWriter) Write(p []byte) (int, error) {
	n, err := o.ResponseWriter.Write(p)
	o.written.Add(int64(n))
	return n, err
}

func TestGateAfterEvents(t *testing.T) {
	for _, wire := range testWires {
		t.Run(string(wire), func(t *testing.T) {
			s := New(t)
			var written atomic.Int64
			probe := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				s.http.Config.Handler.ServeHTTP(observedWriter{w, &written}, r)
			}))
			t.Cleanup(probe.Close)
			for _, end := range []string{"release", "cancel"} {
				t.Run(end, func(t *testing.T) {
					// Chat's role, Responses' item lifecycle, Messages' ping
					// and Interactions' step start precede the first text.
					n := map[Wire]int{Chat: 2, Responses: 5, Codex: 5, Messages: 4, Gemini: 1, Interactions: 3, Ollama: 1}[wire]
					ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
					defer cancel()
					reqCtx, disconnect := context.WithCancel(ctx)
					defer disconnect()
					gate := NewGate()
					defer gate.Release()
					const first, last = "first ", "second"
					parts := []string{first, "", last}
					s.Script(Text("replaced").WithTextDeltas(parts...).WithGateAfter(n, gate))
					parts[0] = "changed"
					r := turnRequest(t, reqCtx, s, wire, true)
					r.URL.Host = probe.Listener.Addr().String()
					written.Store(0)
					res := do(t, r)
					if res.StatusCode != http.StatusOK {
						t.Fatalf("HTTP %s: %s", res.Status, readResponse(t, res))
					}
					reader := bufio.NewReader(res.Body)
					var before bytes.Buffer
					for frames := 0; frames < n; {
						line, err := reader.ReadString('\n')
						if err != nil {
							t.Fatalf("read %d of %d frames: %v", frames, n, err)
						}
						before.WriteString(line)
						if line == "\n" || wire == Ollama {
							frames++
						}
					}
					if err := gate.Wait(ctx); err != nil {
						t.Fatal(err)
					}
					if got := written.Load(); got != int64(before.Len()) || !strings.Contains(before.String(), `"first "`) || strings.Contains(before.String(), last) {
						t.Fatalf("before release: read %q in %d frames; server wrote %d bytes", before.String(), n, got)
					}
					if end == "cancel" {
						disconnect()
						if err := gate.WaitCanceled(ctx); err != nil {
							t.Fatalf("client cancellation did not wake the gate: %v", err)
						}
					} else {
						gate.Release()
						resCopy := *res
						resCopy.Body = io.NopCloser(io.MultiReader(&before, reader))
						rows := checkedTurn(t, wire, &resCopy, true, 200)
						if chunks := visibleDeltas(wire, rows); !reflect.DeepEqual(chunks, []string{first, "", last}) {
							t.Fatalf("wrong text deltas after release: %q", chunks)
						}
						assertNotCanceled(t, gate)
					}
				})
			}
		})
	}
}

func visibleDeltas(w Wire, rows []map[string]any) []string {
	var out []string
	for _, e := range rows {
		var v any
		switch w {
		case Chat:
			if len(e["choices"].([]any)) != 0 {
				v = delta(e)["content"]
			}
		case Responses, Codex:
			if e["type"] == "response.output_text.delta" {
				v = e["delta"]
			}
		case Messages, Interactions:
			if d, ok := e["delta"].(map[string]any); ok {
				v = d["text"]
			}
		case Gemini:
			c := googleArray(e["candidates"])[0].(googleObject)
			if parts := googleArray(googleObjectAt(c, "content")["parts"]); len(parts) != 0 {
				v = parts[0].(googleObject)["text"]
			}
		case Ollama:
			if e["done"] == false {
				v = e["message"].(map[string]any)["content"]
			}
		}
		if text, ok := v.(string); ok {
			out = append(out, text)
		}
	}
	return out
}

func TestTextDeltaAssembly(t *testing.T) {
	for _, wire := range testWires {
		t.Run(string(wire), func(t *testing.T) {
			s := New(t)
			parts := Text("discarded").WithTextDeltas("first ", "second")
			s.Script(parts, parts.WithText("replacement"))
			stream := wire == Codex
			rows := checkedTurn(t, wire, do(t, turnRequest(t, t.Context(), s, wire, stream)), stream, 200)
			if stream {
				rows = rows[len(rows)-1:] // Final Codex envelope, not its intermediate deltas.
			}
			if raw := string(jsonBytes(rows)); !strings.Contains(raw, `"first second"`) || strings.Contains(raw, "discarded") {
				t.Fatal("missing assembled answer:", raw)
			}
			rows = checkedTurn(t, wire, do(t, turnRequest(t, t.Context(), s, wire, true)), true, 200)
			if got := visibleDeltas(wire, rows); !reflect.DeepEqual(got, []string{"replacement"}) {
				t.Fatalf("WithText did not replace scripted chunks: %q", got)
			}
		})
	}
}

func assertNotCanceled(t *testing.T, g *Gate) {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := g.WaitCanceled(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("WaitCanceled after normal release or shutdown = %v", err)
	}
}

func TestGateAfterWholeBodies(t *testing.T) {
	for _, wire := range testWires {
		for _, fault := range []bool{false, true} {
			if wire == Codex && !fault {
				continue // No non-stream success body.
			}
			s := New(t)
			g := NewGate()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			tr, status := Text("ok"), 200
			if fault {
				tr, status = HTTPError(Auth), 401
				if wire == Gemini {
					status = 403
				}
			}
			s.Script(tr.WithGateAfter(10, g))
			r := turnRequest(t, ctx, s, wire, fault)
			type result struct {
				res *http.Response
				err error
			}
			ch := make(chan result, 1)
			go func() { res, err := http.DefaultClient.Do(r); ch <- result{res, err} }()
			if err := g.Wait(ctx); err != nil {
				cancel()
				t.Fatalf("%s (fault=%v) never held the body: %v", wire, fault, err)
			}
			g.Release()
			v := <-ch
			if v.err != nil {
				cancel()
				t.Fatal(v.err)
			}
			checkedTurn(t, wire, v.res, false, status)
			cancel()
		}
	}
}

func TestGateShutdownIsNotClientCancellation(t *testing.T) {
	gate := NewGate()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	var body io.ReadCloser
	t.Run("server", func(t *testing.T) {
		s := New(t)
		s.Script(Text("held").WithGate(gate))
		// Keep the client open across this subtest's server cleanup.
		resp, err := http.DefaultClient.Do(turnRequest(t, ctx, s, Chat, true))
		if err != nil {
			t.Fatal(err)
		}
		body = resp.Body
		if err := gate.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if body != nil {
		_ = body.Close()
	}
	assertNotCanceled(t, gate)
}

// checkedTurn reuses the independent bundle and framing checks of each codec.
func checkedTurn(t *testing.T, wire Wire, res *http.Response, stream bool, status int) []map[string]any {
	t.Helper()
	if !stream || status != http.StatusOK {
		root := "ErrorResponse"
		if status == http.StatusOK {
			root = map[Wire]string{Chat: chatResponseSchema, Responses: "Response", Gemini: geminiReplySchema,
				Interactions: interactionsReplySchema, Ollama: "ChatResponse", Messages: "Message"}[wire]
		}
		// Codex inherits the public HTTP envelope. Its private bundle has
		// ResponseItem, not a second HTTP error schema.
		if wire == Codex {
			wire = Responses
		}
		return []map[string]any{googleJSON(t, wire, root, res, status)}
	}
	var rows []map[string]any
	var err error
	switch wire {
	case Chat:
		rows, _, err = chatEvents(t, res)
	case Responses, Codex:
		rows = completeEvents(t, res, wire == Codex)
	case Messages:
		rows = messageStream(t, res, "MessageStreamEvent")
	case Gemini, Interactions:
		var events []googleEvent
		events, _, err = googleEvents(t, wire, res)
		for _, e := range events {
			rows = append(rows, e.body)
		}
	case Ollama:
		rows, err = ollamaLines(t, res)
	}
	if err != nil || len(rows) == 0 {
		t.Fatalf("invalid %s stream: %d events, %v", wire, len(rows), err)
	}
	return rows
}

func TestFaultMessageChangesOnlyWording(t *testing.T) {
	const custom = "Invalid item:\n\"this item\"."
	for _, wire := range testWires {
		for _, stream := range []bool{false, true} {
			turn, status := HTTPError(RateLimit), http.StatusTooManyRequests
			if stream {
				turn, status = Text("accepted").WithStreamError(1, RateLimit), http.StatusOK
			}
			// Separate servers keep the synthetic IDs and sequence identical.
			var original []map[string]any
			for i, v := range []Turn{turn, turn.WithFaultMessage(custom)} {
				s := New(t)
				s.Script(v.WithRetryHint(RetryAfterSeconds(7)))
				res := do(t, turnRequest(t, t.Context(), s, wire, true))
				rows := checkedTurn(t, wire, res, stream, status)
				end := rows[len(rows)-1]
				if wire == Messages {
					// This wire allocates a different request ID for each HTTP request.
					if end["request_id"] != res.Header.Get("request-id") || end["request_id"] == "" {
						t.Fatalf("unattributed Messages error: %v", end)
					}
					delete(end, "request_id")
				}
				container, key := end, "error"
				if wire != Ollama {
					if response, ok := end["response"].(map[string]any); ok {
						end = response
					}
					container, key = end["error"].(map[string]any), "message"
				}
				msg := container[key]
				if i == 0 && (msg == "" || msg == custom) || i == 1 && msg != custom {
					t.Fatalf("%s stream=%v override=%v: %v", wire, stream, i == 1, msg)
				}
				delete(container, key)
				if i == 0 {
					original = rows
				} else if !reflect.DeepEqual(original, rows) {
					t.Fatalf("%s stream=%v: override changed other fields: %v != %v", wire, stream, original, rows)
				}
			}
		}
	}
}
