package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/charmbracelet/x/powernap/pkg/transport"
	"github.com/sourcegraph/jsonrpc2"
)

// Initialize and all requests use the real framed connection. Literal response
// suffixes retain malformed JSON, missing results and exact opaque tokens.
func completionWireClient(t *testing.T, capability string, reply func(codeActionWireRequest) string) (*Client, *atomic.Int32) {
	t.Helper()
	caps := "{}"
	if capability != "" {
		caps = `{"completionProvider":` + capability + `}`
	}
	return capabilityWireClient(t, caps, reply)
}

// capabilityWireClient initializes a real framed client whose server
// advertises exactly caps, then answers every request with reply.
func capabilityWireClient(t *testing.T, caps string, reply func(codeActionWireRequest) string) (*Client, *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	clientStream, serverStream := net.Pipe()
	conn, err := transport.NewConnection(ctx, clientStream, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{conn: conn}
	count := new(atomic.Int32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(serverStream)
		for {
			header, err := textproto.NewReader(reader).ReadMIMEHeader()
			if err != nil {
				return
			}
			length, err := strconv.Atoi(header.Get("Content-Length"))
			if err != nil || length < 0 || length > 2<<20 {
				t.Errorf("invalid request length: %q", header.Get("Content-Length"))
				return
			}
			raw := make([]byte, length)
			if _, err := io.ReadFull(reader, raw); err != nil {
				return
			}
			var request codeActionWireRequest
			if err := json.Unmarshal(raw, &request); err != nil {
				t.Errorf("invalid framed request: %v", err)
				return
			}
			if len(request.ID) == 0 {
				continue
			}
			var fields string
			if request.Method == "initialize" {
				fields = `,"result":{"capabilities":` + caps + `}`
			} else {
				count.Add(1)
				fields = reply(request)
				if fields == "unanswered" {
					continue
				}
			}
			response := `{"jsonrpc":"2.0","id":` + string(request.ID) + fields + `}`
			if _, err := fmt.Fprintf(serverStream, "Content-Length: %d\r\n\r\n%s", len(response), response); err != nil {
				return
			}
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = serverStream.Close()
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
		<-done
	})
	if err := c.Initialize(ctx, false); err != nil {
		t.Fatal(err)
	}
	return c, count
}

func TestCompletionCapabilityLeaf(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		leaf := CompletionClientCapabilities(enabled)
		raw, err := json.Marshal(leaf)
		want := `{"completionItem":{"commitCharactersSupport":true,"documentationFormat":["plaintext","markdown"],"insertReplaceSupport":true,"insertTextModeSupport":{"valueSet":[1,2]},"resolveSupport":{"properties":["documentation","detail"]},"snippetSupport":` +
			strconv.FormatBool(enabled) + `},"contextSupport":true,"dynamicRegistration":false,"insertTextMode":1}`
		if err != nil || string(raw) != want {
			t.Fatalf("capability grants changed:\n%s\nwant %s\n%v", raw, want, err)
		}
		item := leaf["completionItem"].(map[string]any)
		item["documentationFormat"].([]string)[0] = "changed"
		item["resolveSupport"].(map[string]any)["properties"].([]string)[0] = "textEdit"
		again, err := json.Marshal(CompletionClientCapabilities(enabled))
		if err != nil || string(again) != want {
			t.Fatal("constructors share mutable capabilities")
		}
	}
	// This constructor test does not establish parent-owned initialize wiring.
}

func TestCompletionInitializedStaticAdvertisement(t *testing.T) {
	for _, tc := range []struct {
		name, capability string
		request, resolve bool
		commit           string
	}{
		{"absent", "", false, false, ""},
		{"null", "null", false, false, ""},
		{"options", "{}", true, false, ""},
		{"no-resolve", `{"resolveProvider":false}`, true, false, ""},
		{"resolve", `{"resolveProvider":true}`, true, true, ""},
		{"unknown", `{"future":{"resolveProvider":true}}`, true, false, ""},
		{"triggers", `{"triggerCharacters":["."],"allCommitCharacters":[";"],"resolveProvider":true}`, true, true, ";"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, calls := completionWireClient(t, tc.capability, func(request codeActionWireRequest) string {
				switch request.Method {
				case "textDocument/completion":
					return `,"result":null`
				case "completionItem/resolve":
					return `,"result":{}`
				default:
					t.Errorf("unexpected method: %s", request.Method)
					return `,"result":null`
				}
			})
			if c.CompletionResolveSupported() != tc.resolve {
				t.Fatal("resolve grant differs from initialized advertisement")
			}
			if commit := strings.Join(c.CompletionCommitCharacters(), ""); commit != tc.commit {
				t.Fatalf("commit characters %q differ from the advertisement %q", commit, tc.commit)
			}
			raw, err := c.RequestCompletionsRaw(context.Background(), "custom:source", 0, 0, protocol.CompletionContext{})
			if tc.request {
				if err != nil || string(raw) != "null" {
					t.Fatalf("supported empty: %s %v", raw, err)
				}
			} else if raw != nil || !errors.Is(err, ErrCompletionUnsupported) {
				t.Fatalf("unsupported request admitted: %s %v", raw, err)
			}
			raw, err = c.ResolveCompletionRaw(context.Background(), json.RawMessage(`{"label":"original"}`))
			if tc.resolve {
				if err != nil || string(raw) != "{}" {
					t.Fatalf("supported resolve: %s %v", raw, err)
				}
			} else if raw != nil || !errors.Is(err, ErrCompletionUnsupported) {
				t.Fatalf("unsupported resolve conflated with empty: %s %v", raw, err)
			}
			want := int32(0)
			if tc.request {
				want++
			}
			if tc.resolve {
				want++
			}
			if calls.Load() != want {
				t.Fatalf("unsupported/fallback calls: %d want %d", calls.Load(), want)
			}
		})
	}
}

func TestCompletionFramedOriginalResponseVariants(t *testing.T) {
	for _, original := range []string{
		"null", "[]",
		`[{"label":"雪","data":9007199254740993}]`,
		`{"isIncomplete":false,"items":[]}`,
		`{ "isIncomplete":true, "itemDefaults":{"data":{"n":9007199254740993}}, "items":[{"label":"a"}], "future":1.2300e+09 }`,
	} {
		t.Run(original, func(t *testing.T) {
			c, calls := completionWireClient(t, "{}", func(request codeActionWireRequest) string {
				const want = `{"textDocument":{"uri":"custom:space%20and%20%E9%9B%AA"},"position":{"line":2,"character":3},"context":{"triggerKind":1}}`
				if request.Method != "textDocument/completion" || string(request.Params) != want {
					t.Errorf("not one original explicit invocation: %s %s", request.Method, request.Params)
				}
				return `,"result":` + original
			})
			raw, err := c.RequestCompletionsRaw(context.Background(), "custom:space%20and%20%E9%9B%AA", 2, 3, protocol.CompletionContext{})
			if err != nil || string(raw) != original {
				t.Fatalf("original response lost: %s %v", raw, err)
			}
			for i := range raw {
				raw[i] = 'x'
			}
			again, err := c.RequestCompletionsRaw(context.Background(), "custom:space%20and%20%E9%9B%AA", 2, 3, protocol.CompletionContext{})
			if err != nil || string(again) != original || calls.Load() != 2 {
				t.Fatalf("response storage reused: %s %v calls=%d", again, err, calls.Load())
			}
			if bytes.Equal(raw, again) {
				t.Fatal("response aliases earlier result")
			}
		})
	}
}

func TestCompletionResolveOpaqueIdentityAndDetachment(t *testing.T) {
	const original = `{"label":"雪","data":{"n":9007199254740993,"max":18446744073709551615,"fraction":1.2300e+09,"dup":1,"dup":2},"future":[null,false,"\u0061"],"label":"duplicate","command":{"command":"never.execute","arguments":[9007199254740993]}}`
	const resolved = `{"label":"雪","data":{"n":9007199254740993,"max":18446744073709551615,"fraction":1.2300e+09,"dup":1,"dup":2},"future":[null,false,"\u0061"],"label":"duplicate","command":{"command":"never.execute","arguments":[9007199254740993]},"documentation":"docs","detail":"detail"}`
	c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(request codeActionWireRequest) string {
		if request.Method != "completionItem/resolve" || string(request.Params) != original {
			t.Errorf("opaque item changed or executed: %s %s", request.Method, request.Params)
		}
		return `,"result":` + resolved
	})
	input := json.RawMessage(original)
	ctx := &inlayHintCheckContext{Context: context.Background(), check: func(n int32) {
		if n == 2 { // After validation/copy, immediately before Call.
			for i := range input {
				input[i] = 'x'
			}
		}
	}}
	raw, err := c.ResolveCompletionRaw(ctx, input)
	if err != nil || string(raw) != resolved {
		t.Fatalf("opaque resolved result lost: %s %v", raw, err)
	}
	again, err := c.ResolveCompletionRaw(context.Background(), json.RawMessage(original))
	if err != nil || string(again) != resolved || calls.Load() != 2 {
		t.Fatalf("resolve retried or changed result: %s %v calls=%d", again, err, calls.Load())
	}
	for i := range raw {
		raw[i] = 'x'
	}
	if string(again) != resolved {
		t.Fatal("resolve results share storage")
	}
}

func TestCompletionRawMessageEncodingContract(t *testing.T) {
	// RawMessage preserves tokens, not insignificant whitespace or HTML spelling.
	const original = " { \"label\" : \"<a>&\", \"data\" : 9007199254740993, \"data\":2 } "
	const encoded = `{"label":"\u003ca\u003e\u0026","data":9007199254740993,"data":2}`
	c, _ := completionWireClient(t, `{"resolveProvider":true}`, func(request codeActionWireRequest) string {
		if request.Method != "completionItem/resolve" || string(request.Params) != encoded {
			t.Errorf("unexpected RawMessage encoding: %s %s", request.Method, request.Params)
		}
		return `,"result":` + original
	})
	raw, err := c.ResolveCompletionRaw(context.Background(), json.RawMessage(original))
	// Envelope parsing excludes whitespace outside the result value.
	if err != nil || string(raw) != strings.TrimSpace(original) {
		t.Fatalf("result was reconstructed: %s %v", raw, err)
	}
}

func TestCompletionInvalidInputsNeverDispatch(t *testing.T) {
	c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return `,"result":null` })
	for _, uri := range []string{"", strings.Repeat("a", 4097), "file:///te\x00st", "custom:\xff"} {
		if raw, err := c.RequestCompletionsRaw(context.Background(), uri, 0, 0, protocol.CompletionContext{}); raw != nil || !errors.Is(err, ErrCompletionInvalid) {
			t.Fatalf("invalid URI admitted: %v", err)
		}
	}
	for _, position := range [][2]int{{-1, 0}, {0, -1}, {math.MinInt, 0}, {0, math.MinInt}} {
		if raw, err := c.RequestCompletionsRaw(context.Background(), "file:///test", position[0], position[1], protocol.CompletionContext{}); raw != nil || !errors.Is(err, ErrCompletionInvalid) {
			t.Fatalf("invalid position admitted: %v %v", position, err)
		}
	}
	// Keep this test compilable on 32-bit targets, where int cannot exceed int32.
	if strconv.IntSize > 32 {
		overflow := int64(2147483648)
		for _, position := range [][2]int{{int(overflow), 0}, {0, int(overflow)}} {
			if raw, err := c.RequestCompletionsRaw(context.Background(), "file:///test", position[0], position[1], protocol.CompletionContext{}); raw != nil || !errors.Is(err, ErrCompletionInvalid) {
				t.Fatalf("overflow position admitted: %v %v", position, err)
			}
		}
	}
	for _, original := range []string{
		"", " ", "null", "[]", "[{}]", "true", "1", `"item"`, "{", "{}{}",
		`{"label":"` + "\xff" + `"}`, `{"data":"` + strings.Repeat("x", 1<<20) + `"}`,
	} {
		if raw, err := c.ResolveCompletionRaw(context.Background(), json.RawMessage(original)); raw != nil || !errors.Is(err, ErrCompletionInvalid) {
			t.Fatalf("invalid original length %d admitted: %v", len(original), err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input dispatched")
	}
}

func TestCompletionInclusiveBounds(t *testing.T) {
	uri := "custom:" + strings.Repeat("雪", 1363) // Exactly 4096 bytes.
	// Independent literal overhead is 11 bytes, so this object is exactly 1 MiB.
	original := `{"data":"` + strings.Repeat("x", 1048565) + `"}`
	c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(request codeActionWireRequest) string {
		if request.Method == "textDocument/completion" {
			want := `{"textDocument":{"uri":"` + uri + `"},"position":{"line":2147483647,"character":2147483647},"context":{"triggerKind":1}}`
			if string(request.Params) != want {
				t.Error("inclusive URI/position bounds changed")
			}
			return `,"result":` + original
		}
		if request.Method != "completionItem/resolve" || string(request.Params) != original {
			t.Error("inclusive original bound changed")
		}
		return `,"result":` + original
	})
	raw, err := c.RequestCompletionsRaw(context.Background(), uri, 2147483647, 2147483647, protocol.CompletionContext{})
	if err != nil || string(raw) != original {
		t.Fatalf("inclusive request bound refused: length=%d %v", len(raw), err)
	}
	raw, err = c.ResolveCompletionRaw(context.Background(), json.RawMessage(original))
	if err != nil || string(raw) != original || calls.Load() != 2 {
		t.Fatalf("inclusive resolve bound refused: length=%d %v", len(raw), err)
	}
}

func TestCompletionRawResponseFailures(t *testing.T) {
	for _, tc := range []struct {
		name, result string
		requestOK    bool
		resolveOK    bool
	}{
		{"null", "null", true, false},
		{"empty-array", "[]", true, false},
		{"array", `[{"label":"a"}]`, true, false},
		{"completion-list", `{"isIncomplete":true,"items":[{"label":"a"}]}`, true, true},
		{"minimal-object", "{}", true, true},
		{"schema-consumer-owned", `[null,1,[]]`, true, false},
		{"duplicate-unicode-evidence", `{"label":"\ud800","data":{"dup":1,"dup":2}}`, true, true},
		{"oversize-array", `["` + strings.Repeat("x", 1048573) + `"]`, false, false},
		{"oversize-object", `{"data":"` + strings.Repeat("x", 1048566) + `"}`, false, false},
		{"string", `"item"`, false, false},
		{"number", "1", false, false},
		{"bool", "false", false, false},
		{"invalid-utf8", `{"label":"` + "\xff" + `"}`, false, false},
		{"malformed", "[", false, false},
	} {
		for _, resolve := range []bool{false, true} {
			t.Run(tc.name+"/"+strconv.FormatBool(resolve), func(t *testing.T) {
				c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string {
					return `,"result":` + tc.result
				})
				raw, err := callCompletionRaw(c, context.Background(), resolve)
				ok := tc.requestOK
				if resolve {
					ok = tc.resolveOK
				}
				if ok {
					if err != nil || string(raw) != tc.result {
						t.Fatalf("raw result changed: length=%d %v", len(raw), err)
					}
				} else if raw != nil || err == nil {
					t.Fatalf("invalid response exposed: length=%d %v", len(raw), err)
				} else if tc.name != "malformed" && !errors.Is(err, ErrCompletionInvalid) {
					t.Fatalf("structural error classification lost: %v", err)
				}
				if calls.Load() != 1 {
					t.Fatal("response failure retried/probed")
				}
			})
		}
	}
}

func callCompletionRaw(c *Client, ctx context.Context, resolve bool) (json.RawMessage, error) {
	if resolve {
		return c.ResolveCompletionRaw(ctx, json.RawMessage(`{"label":"original"}`))
	}
	return c.RequestCompletionsRaw(ctx, "file:///test", 0, 0, protocol.CompletionContext{})
}

func TestCompletionRPCAndConnectionErrorIdentity(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		for _, fields := range []string{"", `,"error":{"code":-32603,"message":"provider failed","data":{"n":9007199254740993}}`} {
			t.Run(strconv.FormatBool(resolve)+fields, func(t *testing.T) {
				c, _ := completionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return fields })
				raw, err := callCompletionRaw(c, context.Background(), resolve)
				if raw != nil || err == nil || errors.Is(err, ErrCompletionInvalid) || errors.Is(err, ErrCompletionUnsupported) {
					t.Fatalf("RPC failure conflated: %s %v", raw, err)
				}
				if fields != "" {
					var rpcError *jsonrpc2.Error
					if !errors.As(err, &rpcError) || rpcError.Code != -32603 || rpcError.Message != "provider failed" || !errors.Is(err, rpcError) {
						t.Fatalf("RPC error identity lost: %v", err)
					}
				}
			})
		}
		t.Run("closed/"+strconv.FormatBool(resolve), func(t *testing.T) {
			c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return `,"result":null` })
			if err := c.conn.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := callCompletionRaw(c, context.Background(), resolve)
			original := errors.Unwrap(err)
			if raw != nil || original == nil || original.Error() != "connection is closed" ||
				errors.Is(err, ErrCompletionInvalid) || errors.Is(err, ErrCompletionUnsupported) || calls.Load() != 0 {
				t.Fatalf("connection error lost: %s %v", raw, err)
			}
		})
	}
}

func TestCompletionCanceledAndUninitializedNeverDispatch(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		for _, state := range []string{"supported", "unsupported", "invalid", "uninitialized"} {
			for _, at := range []int32{1, 2} {
				t.Run(strconv.FormatBool(resolve)+"/"+state+"/"+strconv.Itoa(int(at)), func(t *testing.T) {
					c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return `,"result":null` })
					uri, original := "file:///test", json.RawMessage(`{}`)
					switch state {
					case "unsupported":
						c.capabilities.CompletionProvider = nil
					case "invalid":
						uri, original = "", nil
					case "uninitialized":
						c.initialized.Store(false)
					}
					base, cancel := context.WithCancel(context.Background())
					defer cancel()
					ctx := &inlayHintCheckContext{Context: base, check: func(n int32) {
						if n == at {
							cancel()
						}
					}}
					var raw json.RawMessage
					var err error
					if resolve {
						raw, err = c.ResolveCompletionRaw(ctx, original)
					} else {
						raw, err = c.RequestCompletionsRaw(ctx, uri, 0, 0, protocol.CompletionContext{})
					}
					if raw != nil || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
						t.Fatalf("cancellation lost on early outcome: %s %v calls=%d", raw, err, calls.Load())
					}
				})
			}
		}
	}
	c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return `,"result":null` })
	c.initialized.Store(false)
	if c.CompletionResolveSupported() {
		t.Fatal("uninitialized resolve grant")
	}
	for _, resolve := range []bool{false, true} {
		if raw, err := callCompletionRaw(c, context.Background(), resolve); raw != nil || err == nil {
			t.Fatal("uninitialized call admitted")
		}
	}
	if calls.Load() != 0 {
		t.Fatal("uninitialized calls reached server")
	}
}

func TestCompletionCancellationAfterEveryProviderOutcome(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		for _, fields := range []string{
			`,"result":null`, `,"result":[]`, `,"result":{"label":"item"}`,
			`,"result":[`, `,"result":{"label":"` + "\xff" + `"}`,
			`,"result":{"data":"` + strings.Repeat("x", 1<<20) + `"}`,
			`,"error":{"code":-32603,"message":"failed"}`,
		} {
			t.Run(strconv.FormatBool(resolve)+"/"+strconv.Itoa(len(fields)), func(t *testing.T) {
				c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return fields })
				base, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx := &inlayHintCheckContext{Context: base, check: func(n int32) {
					if n == 3 { // Entry, pre-Call, final outcome.
						cancel()
					}
				}}
				raw, err := callCompletionRaw(c, ctx, resolve)
				if raw != nil || !errors.Is(err, context.Canceled) || calls.Load() != 1 {
					t.Fatalf("post-provider cancellation lost: %s %v", raw, err)
				}
				if strings.Contains(fields, `"error"`) {
					var rpcError *jsonrpc2.Error
					if !errors.As(err, &rpcError) || !errors.Is(err, rpcError) {
						t.Fatalf("cancellation erased RPC error identity: %v", err)
					}
				}
			})
		}
	}
}

func TestCompletionInFlightCancellationAndConnectionReuse(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		t.Run(strconv.FormatBool(resolve), func(t *testing.T) {
			started := make(chan struct{})
			c, calls := completionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string {
				select {
				case <-started:
					if resolve {
						return `,"result":{"label":"item"}`
					}
					return `,"result":[]`
				default:
					close(started)
					return "unanswered"
				}
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				raw, err := callCompletionRaw(c, ctx, resolve)
				if raw != nil {
					t.Error("canceled call exposed bytes")
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("call did not reach framed server")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("in-flight cancellation lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("call ignored cancellation")
			}
			raw, err := callCompletionRaw(c, context.Background(), resolve)
			if err != nil || !json.Valid(raw) || calls.Load() != 2 || !c.conn.IsConnected() {
				t.Fatalf("cancellation retired connection or retried: %s %v calls=%d", raw, err, calls.Load())
			}
		})
	}
}
