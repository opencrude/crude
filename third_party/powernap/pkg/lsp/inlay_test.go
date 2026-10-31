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

// Real Initialize and calls over the real connection/codec. Reply is a literal
// envelope suffix so missing results, protocol errors and malformed JSON can be
// exercised without a fake Call implementation or decode/re-encode of results.
func inlayHintWireClient(t *testing.T, capability string, reply func(codeActionWireRequest) string) (*Client, *atomic.Int32) {
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
				continue // initialized/configuration notifications are not requests.
			}
			var fields string
			if request.Method == "initialize" {
				caps := "{}"
				if capability != "" {
					caps = `{"inlayHintProvider":` + capability + `}`
				}
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

func TestInlayHintCapabilityLeaf(t *testing.T) {
	leaf := InlayHintClientCapabilities()
	if leaf.DynamicRegistration {
		t.Fatal("dynamic registration advertised")
	}
	raw, err := json.Marshal(leaf)
	// The generated false bool is omitempty: absence means false on the wire.
	if err != nil || string(raw) != `{"resolveSupport":{"properties":["tooltip","label.tooltip"]}}` {
		t.Fatalf("unexpected capability grants: %s %v", raw, err)
	}
	leaf.ResolveSupport.Properties[0] = "textEdits"
	if InlayHintClientCapabilities().ResolveSupport.Properties[0] != "tooltip" {
		t.Fatal("capability constructors share mutable properties")
	}
	// This leaf is intentionally NOT evidence of parent-owned initialize wiring.
}

func TestInlayHintInitializedStaticCapabilityGates(t *testing.T) {
	for _, tc := range []struct {
		name, capability string
		request, resolve bool
	}{
		{"absent", "", false, false},
		{"null", "null", false, false},
		{"false", "false", false, false},
		{"true", "true", true, false},
		{"number", "1", false, false},
		{"string", `"true"`, false, false},
		{"array", "[]", false, false},
		{"options", "{}", true, false},
		{"resolve", `{"resolveProvider":true}`, true, true},
		{"no-resolve", `{"resolveProvider":false}`, true, false},
		{"null-resolve", `{"resolveProvider":null}`, false, false},
		{"string-resolve", `{"resolveProvider":"true"}`, false, false},
		{"number-resolve", `{"resolveProvider":1}`, false, false},
		{"progress", `{"workDoneProgress":true,"resolveProvider":true}`, true, true},
		{"no-progress", `{"workDoneProgress":false}`, true, false},
		{"null-progress", `{"workDoneProgress":null,"resolveProvider":true}`, false, false},
		{"bad-progress", `{"workDoneProgress":1}`, false, false},
		{"string-progress", `{"workDoneProgress":"false"}`, false, false},
		{"unknown", `{"futureOption":{"resolveProvider":true}}`, true, false},
		{"registration", `{"id":"static","documentSelector":[{"language":"custom","scheme":"custom","pattern":"**/*"}],"resolveProvider":true}`, true, true},
		{"null-selector", `{"id":"static","documentSelector":null}`, true, false},
		{"empty-selector", `{"documentSelector":[]}`, true, false},
		{"bad-id", `{"id":false}`, false, false},
		{"bad-selector", `{"documentSelector":{}}`, false, false},
		{"bad-filter", `{"documentSelector":[null]}`, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, calls := inlayHintWireClient(t, tc.capability, func(request codeActionWireRequest) string {
				switch request.Method {
				case "textDocument/inlayHint":
					return `,"result":null`
				case "inlayHint/resolve":
					return `,"result":{"label":"resolved"}`
				default:
					t.Errorf("unexpected method: %s", request.Method)
					return `,"result":null`
				}
			})
			if c.InlayHintResolveSupported() != tc.resolve {
				t.Fatal("resolve observation disagrees with initialized static capability")
			}
			raw, err := c.RequestInlayHints(context.Background(), "custom:source", protocol.Range{})
			if tc.request {
				if err != nil || string(raw) != "null" {
					t.Fatalf("request: %s %v", raw, err)
				}
			} else if raw != nil || !errors.Is(err, ErrInlayHintUnsupported) {
				t.Fatalf("unsupported request admitted: %s %v", raw, err)
			}
			raw, err = c.ResolveInlayHint(context.Background(), json.RawMessage(`{"label":"original"}`))
			if tc.resolve {
				if err != nil || string(raw) != `{"label":"resolved"}` {
					t.Fatalf("resolve: %s %v", raw, err)
				}
			} else if raw != nil || !errors.Is(err, ErrInlayHintUnsupported) {
				t.Fatalf("unsupported resolve admitted: %s %v", raw, err)
			}
			want := int32(0)
			if tc.request {
				want++
			}
			if tc.resolve {
				want++
			}
			if calls.Load() != want {
				t.Fatalf("unsupported/probing calls: %d want %d", calls.Load(), want)
			}
		})
	}
}

func TestInlayHintNonJSONCapabilitiesNeverGrant(t *testing.T) {
	c, calls := inlayHintWireClient(t, "", func(codeActionWireRequest) string { return `,"result":null` })
	for _, value := range []any{
		nil, map[string]any(nil), json.RawMessage("true"), (*bool)(nil),
		protocol.InlayHintOptions{ResolveProvider: true}, &protocol.InlayHintOptions{ResolveProvider: true},
		(*protocol.InlayHintOptions)(nil), protocol.InlayHintRegistrationOptions{},
		&protocol.InlayHintRegistrationOptions{}, (*protocol.InlayHintRegistrationOptions)(nil),
	} {
		c.capabilities.InlayHintProvider = value
		raw, err := c.RequestInlayHints(context.Background(), "file:///test", protocol.Range{})
		if raw != nil || !errors.Is(err, ErrInlayHintUnsupported) || c.InlayHintResolveSupported() {
			t.Fatalf("arbitrary %T grants support: %s %v", value, raw, err)
		}
		if raw, err := c.ResolveInlayHint(context.Background(), json.RawMessage(`{}`)); raw != nil || !errors.Is(err, ErrInlayHintUnsupported) {
			t.Fatalf("arbitrary %T grants resolve: %s %v", value, raw, err)
		}
	}
	c.capabilities.InlayHintProvider = map[string]any{"resolveProvider": true}
	c.initialized.Store(false)
	if c.InlayHintResolveSupported() {
		t.Fatal("uninitialized resolve grant")
	}
	if raw, err := c.RequestInlayHints(context.Background(), "file:///test", protocol.Range{}); raw != nil || err == nil {
		t.Fatal("uninitialized request admitted")
	}
	if raw, err := c.ResolveInlayHint(context.Background(), json.RawMessage(`{}`)); raw != nil || err == nil {
		t.Fatal("uninitialized resolve admitted")
	}
	if calls.Load() != 0 {
		t.Fatal("uninitialized/non-JSON capability probed server")
	}
}

func TestInlayHintFramedOriginalLabelsAndOpaqueData(t *testing.T) {
	for _, original := range []string{
		`{"position":{"line":2,"character":3},"label":"type: 雪","paddingLeft":false,"data":{"n":9007199254740993,"large":18446744073709551615,"fraction":1.2300e+09,"dup":1,"dup":2},"future":[null,false,"\u0061"]}`,
		`{"position":{"line":2,"character":3},"label":[{"value":"type: ","command":{"title":"opaque","command":"never.execute","arguments":[9007199254740993]}},{"value":"雪","location":{"uri":"custom:target","range":{"start":{"line":0,"character":0},"end":{"line":0,"character":0}}}}],"data":{"n":9007199254740993}}`,
	} {
		t.Run(original[0:30]+strconv.Itoa(len(original)), func(t *testing.T) {
			resolved := strings.TrimSuffix(original, "}") + `,"tooltip":{"kind":"plaintext","value":"detail"}}`
			c, calls := inlayHintWireClient(t, `{"resolveProvider":true}`, func(request codeActionWireRequest) string {
				switch request.Method {
				case "textDocument/inlayHint":
					const want = `{"textDocument":{"uri":"custom:space%20and%20%E9%9B%AA"},"range":{"start":{"line":2,"character":3},"end":{"line":4,"character":5}}}`
					if string(request.Params) != want {
						t.Errorf("added/changed request fields:\ngot %s\nwant %s", request.Params, want)
					}
					return `,"result":[` + original + `]`
				case "inlayHint/resolve":
					if string(request.Params) != original {
						t.Errorf("original object transformed:\ngot %s\nwant %s", request.Params, original)
					}
					return `,"result":` + resolved
				default:
					t.Errorf("unexpected executable/fallback method: %s", request.Method)
					return `,"result":null`
				}
			})
			list, err := c.RequestInlayHints(context.Background(), "custom:space%20and%20%E9%9B%AA",
				protocol.Range{Start: protocol.Position{Line: 2, Character: 3}, End: protocol.Position{Line: 4, Character: 5}})
			if err != nil || string(list) != "["+original+"]" {
				t.Fatalf("original list lost: %s %v", list, err)
			}
			input := json.RawMessage(original)
			base := context.Background()
			// Mutate the caller bytes exactly after validation and cloning, before
			// Call serializes them. This deterministically guards bytes.Clone.
			ctx := &inlayHintCheckContext{Context: base, check: func(n int32) {
				if n == 2 {
					for i := range input {
						input[i] = 'x'
					}
				}
			}}
			got, err := c.ResolveInlayHint(ctx, input)
			if err != nil || string(got) != resolved {
				t.Fatalf("original resolve lost: %s %v", got, err)
			}
			for i := range got {
				got[i] = 'x'
			}
			if string(list) != "["+original+"]" {
				t.Fatal("resolve output aliases earlier result")
			}
			again, err := c.ResolveInlayHint(context.Background(), json.RawMessage(original))
			if err != nil || string(again) != resolved || calls.Load() != 3 {
				t.Fatalf("detachment/reuse: %s %v calls=%d", again, err, calls.Load())
			}
		})
	}
}

func TestInlayHintInvalidInputsNeverDispatch(t *testing.T) {
	c, calls := inlayHintWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return `,"result":null` })
	for _, uri := range []string{"", strings.Repeat("a", 4097), "file:///te\x00st", "custom:\xff"} {
		if raw, err := c.RequestInlayHints(context.Background(), uri, protocol.Range{}); raw != nil || !errors.Is(err, ErrInlayHintInvalid) {
			t.Fatalf("invalid URI admitted: %v", err)
		}
	}
	for _, span := range []protocol.Range{
		{Start: protocol.Position{Line: math.MaxInt32 + 1}},
		{Start: protocol.Position{Character: math.MaxInt32 + 1}},
		{End: protocol.Position{Line: math.MaxUint32}},
		{End: protocol.Position{Character: math.MaxUint32}},
		{Start: protocol.Position{Line: 2}, End: protocol.Position{Line: 1, Character: 100}},
		{Start: protocol.Position{Line: 1, Character: 3}, End: protocol.Position{Line: 1, Character: 2}},
	} {
		if raw, err := c.RequestInlayHints(context.Background(), "file:///test", span); raw != nil || !errors.Is(err, ErrInlayHintInvalid) {
			t.Fatalf("invalid range admitted: %+v %v", span, err)
		}
	}
	for _, original := range []string{
		"", " ", "null", "[]", "[{}]", "true", "1", `"hint"`, "{", "{}{}", `{"label":"` + "\xff" + `"}`,
		`{"data":"` + strings.Repeat("x", 1<<20) + `"}`,
	} {
		if raw, err := c.ResolveInlayHint(context.Background(), json.RawMessage(original)); raw != nil || !errors.Is(err, ErrInlayHintInvalid) {
			t.Fatalf("invalid original length %d admitted: %v", len(original), err)
		}
	}
	if calls.Load() != 0 {
		t.Fatal("invalid input sent request")
	}
}

func TestInlayHintInclusiveBounds(t *testing.T) {
	uri := "custom:" + strings.Repeat("雪", 1363) // Exactly 4096 bytes.
	// Exact limit independently constructed: object overhead is 11 bytes.
	original := `{"data":"` + strings.Repeat("x", 1048565) + `"}`
	c, calls := inlayHintWireClient(t, `{"resolveProvider":true}`, func(request codeActionWireRequest) string {
		if request.Method == "textDocument/inlayHint" {
			want := `{"textDocument":{"uri":"` + uri + `"},"range":{"start":{"line":2147483647,"character":2147483647},"end":{"line":2147483647,"character":2147483647}}}`
			if string(request.Params) != want {
				t.Error("inclusive URI/range bounds changed")
			}
			return `,"result":[]`
		}
		if request.Method != "inlayHint/resolve" || string(request.Params) != original {
			t.Error("inclusive original bound changed")
		}
		return `,"result":` + original
	})
	max := protocol.Position{Line: 2147483647, Character: 2147483647}
	if raw, err := c.RequestInlayHints(context.Background(), uri, protocol.Range{Start: max, End: max}); err != nil || string(raw) != "[]" {
		t.Fatalf("inclusive bounds refused: %v", err)
	}
	raw, err := c.ResolveInlayHint(context.Background(), json.RawMessage(original))
	if err != nil || string(raw) != original || calls.Load() != 2 {
		t.Fatalf("exact 1 MiB object refused: length=%d %v", len(raw), err)
	}
}

func TestInlayHintRawResultsAndFailures(t *testing.T) {
	exactList := `[{"data":"` + strings.Repeat("x", 1048563) + `"}]`
	for _, tc := range []struct {
		name, result string
		requestOK    bool
		resolveOK    bool
	}{
		{"null", "null", true, false},
		{"empty", "[]", true, false},
		{"list", `[{"label":"string"},{"label":[{"value":"parts"}]}]`, true, false},
		{"object", `{"label":"string","data":9007199254740993}`, false, true},
		{"minimal-object", `{}`, false, true}, // Strict hint schema is consumer-owned.
		{"exact-list", exactList, true, false},
		{"oversize-list", `[{"data":"` + strings.Repeat("x", 1048564) + `"}]`, false, false},
		{"oversize-object", `{"data":"` + strings.Repeat("x", 1048566) + `"}`, false, false},
		{"scalar", `"hint"`, false, false},
		{"number", "1", false, false},
		{"null-item", "[null]", false, false},
		{"scalar-item", `["hint"]`, false, false},
		{"nested-list", "[[]]", false, false},
		{"invalid-utf8", `{"label":"` + "\xff" + `"}`, false, false},
		{"malformed", "[", false, false},
	} {
		for _, resolve := range []bool{false, true} {
			t.Run(tc.name+"/"+strconv.FormatBool(resolve), func(t *testing.T) {
				c, calls := inlayHintWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string {
					return `,"result":` + tc.result
				})
				var raw json.RawMessage
				var err error
				ok := tc.requestOK
				if resolve {
					ok = tc.resolveOK
					raw, err = c.ResolveInlayHint(context.Background(), json.RawMessage(`{"label":"original"}`))
				} else {
					raw, err = c.RequestInlayHints(context.Background(), "file:///test", protocol.Range{})
				}
				if ok {
					if err != nil || string(raw) != tc.result {
						t.Fatalf("raw result changed: length %d %v", len(raw), err)
					}
				} else if raw != nil || err == nil {
					t.Fatalf("invalid response exposed: length %d %v", len(raw), err)
				} else if tc.name != "malformed" && !errors.Is(err, ErrInlayHintInvalid) {
					t.Fatalf("structural error lost classification: %v", err)
				}
				if calls.Load() != 1 {
					t.Fatal("response failure retried/probed")
				}
			})
		}
	}
}

func TestInlayHintRPCAndConnectionErrors(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		for _, fields := range []string{"", `,"error":{"code":-32603,"message":"provider failed","data":{"n":9007199254740993}}`} {
			t.Run(strconv.FormatBool(resolve)+fields, func(t *testing.T) {
				c, _ := inlayHintWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return fields })
				raw, err := callInlayHint(c, context.Background(), resolve)
				if raw != nil || err == nil || errors.Is(err, ErrInlayHintInvalid) || errors.Is(err, ErrInlayHintUnsupported) {
					t.Fatalf("protocol error conflated: %s %v", raw, err)
				}
				if fields != "" {
					var rpcError *jsonrpc2.Error
					if !errors.As(err, &rpcError) || rpcError.Code != -32603 || rpcError.Message != "provider failed" || !errors.Is(err, rpcError) {
						t.Fatalf("original provider error lost: %v", err)
					}
				}
			})
		}
		t.Run("closed/"+strconv.FormatBool(resolve), func(t *testing.T) {
			c, calls := inlayHintWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return `,"result":null` })
			if err := c.conn.Close(); err != nil {
				t.Fatal(err)
			}
			raw, err := callInlayHint(c, context.Background(), resolve)
			original := errors.Unwrap(err)
			if raw != nil || original == nil || original.Error() != "connection is closed" ||
				errors.Is(err, ErrInlayHintInvalid) || errors.Is(err, ErrInlayHintUnsupported) || calls.Load() != 0 {
				t.Fatalf("connection error lost: %s %v", raw, err)
			}
		})
	}
}

func callInlayHint(c *Client, ctx context.Context, resolve bool) (json.RawMessage, error) {
	if resolve {
		return c.ResolveInlayHint(ctx, json.RawMessage(`{"label":"original"}`))
	}
	return c.RequestInlayHints(ctx, "file:///test", protocol.Range{})
}

type inlayHintCheckContext struct {
	context.Context
	checks atomic.Int32
	check  func(int32)
}

func (c *inlayHintCheckContext) Err() error {
	c.check(c.checks.Add(1))
	return c.Context.Err()
}

func TestInlayHintPreCanceledAndEarlyReturnOutcomes(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		for _, state := range []string{"supported", "unsupported", "invalid", "uninitialized"} {
			for _, at := range []int32{1, 2} {
				t.Run(strconv.FormatBool(resolve)+"/"+state+"/"+strconv.Itoa(int(at)), func(t *testing.T) {
					c, calls := inlayHintWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return `,"result":null` })
					uri, original := "file:///test", json.RawMessage(`{}`)
					switch state {
					case "unsupported":
						c.capabilities.InlayHintProvider = false
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
						raw, err = c.ResolveInlayHint(ctx, original)
					} else {
						raw, err = c.RequestInlayHints(ctx, uri, protocol.Range{})
					}
					if raw != nil || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
						t.Fatalf("cancellation lost on early outcome: %s %v calls=%d", raw, err, calls.Load())
					}
				})
			}
		}
	}
}

func TestInlayHintCancellationAfterEveryProviderOutcome(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		for _, fields := range []string{
			`,"result":null`, `,"result":[]`, `,"result":{"label":"hint"}`,
			`,"result":[`, `,"result":{"label":"` + "\xff" + `"}`,
			`,"result":{"data":"` + strings.Repeat("x", 1<<20) + `"}`,
			`,"error":{"code":-32603,"message":"failed"}`,
		} {
			t.Run(strconv.FormatBool(resolve)+"/"+strconv.Itoa(len(fields)), func(t *testing.T) {
				c, calls := inlayHintWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return fields })
				base, cancel := context.WithCancel(context.Background())
				defer cancel()
				ctx := &inlayHintCheckContext{Context: base, check: func(n int32) {
					if n == 3 { // Entry, pre-Call, final outcome.
						cancel()
					}
				}}
				raw, err := callInlayHint(c, ctx, resolve)
				if raw != nil || !errors.Is(err, context.Canceled) || calls.Load() != 1 {
					t.Fatalf("post-provider cancellation lost: %s %v", raw, err)
				}
				if strings.Contains(fields, `"error"`) {
					var rpcError *jsonrpc2.Error
					if !errors.As(err, &rpcError) || !errors.Is(err, rpcError) {
						t.Fatalf("cancellation erased original error: %v", err)
					}
				}
			})
		}
	}
}

func TestInlayHintInFlightCancellationAndReuse(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		t.Run(strconv.FormatBool(resolve), func(t *testing.T) {
			started := make(chan struct{})
			c, calls := inlayHintWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string {
				select {
				case <-started:
					if resolve {
						return `,"result":{"label":"hint"}`
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
				raw, err := callInlayHint(c, ctx, resolve)
				if raw != nil {
					t.Error("canceled request exposed bytes")
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
			raw, err := callInlayHint(c, context.Background(), resolve)
			if err != nil || !json.Valid(raw) || calls.Load() != 2 || !c.conn.IsConnected() {
				t.Fatalf("cancellation retried/retired connection: %s %v calls=%d", raw, err, calls.Load())
			}
		})
	}
}

func TestInlayHintBasicValidationLeavesStrictSchemaToConsumer(t *testing.T) {
	const original = `{"label":"\ud800","data":{"dup":1,"dup":2},"future":9007199254740993}`
	c, _ := inlayHintWireClient(t, `{"resolveProvider":true}`, func(request codeActionWireRequest) string {
		if !bytes.Equal(request.Params, []byte(original)) {
			t.Errorf("strict/opaque evidence altered: %s", request.Params)
		}
		return `,"result":` + original
	})
	raw, err := c.ResolveInlayHint(context.Background(), json.RawMessage(original))
	if err != nil || string(raw) != original {
		t.Fatalf("transport erased strict consumer evidence: %s %v", raw, err)
	}
}
