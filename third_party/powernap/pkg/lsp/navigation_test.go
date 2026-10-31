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

var navigationKinds = []string{"definition", "type-definition", "implementation", "references"}

func setNavigationCapability(c *Client, kind string, value any) {
	switch kind {
	case "definition":
		c.capabilities.DefinitionProvider = &protocol.Or_ServerCapabilities_definitionProvider{Value: value}
	case "type-definition":
		c.capabilities.TypeDefinitionProvider = &protocol.Or_ServerCapabilities_typeDefinitionProvider{Value: value}
	case "implementation":
		c.capabilities.ImplementationProvider = &protocol.Or_ServerCapabilities_implementationProvider{Value: value}
	case "references":
		c.capabilities.ReferencesProvider = &protocol.Or_ServerCapabilities_referencesProvider{Value: value}
	}
}

func TestNavigationGeneratedStaticCapabilities(t *testing.T) {
	for _, provider := range []struct {
		kind, field string
	}{
		{"definition", "definitionProvider"},
		{"type-definition", "typeDefinitionProvider"},
		{"implementation", "implementationProvider"},
		{"references", "referencesProvider"},
	} {
		for _, capability := range []string{"absent", "null", "false", "true", "{}", `{"workDoneProgress":true}`} {
			t.Run(provider.kind+"/"+capability, func(t *testing.T) {
				c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string { return "null" })
				if capability != "absent" {
					if err := json.Unmarshal([]byte(`{"`+provider.field+`":`+capability+`}`), &c.capabilities); err != nil {
						t.Fatal(err)
					}
				}
				raw, err := c.RequestNavigation(context.Background(), provider.kind, "file:///test", protocol.Position{}, false)
				wantCalls := int32(0)
				switch capability {
				case "true", "{}", `{"workDoneProgress":true}`:
					wantCalls = 1
					if err != nil || string(raw) != "null" {
						t.Fatalf("supported provider refused: %q %v", raw, err)
					}
				default:
					if !errors.Is(err, ErrNavigationUnsupported) || raw != nil {
						t.Fatalf("unsupported provider admitted: %q %v", raw, err)
					}
				}
				if calls.Load() != wantCalls {
					t.Fatalf("requests = %d, want %d", calls.Load(), wantCalls)
				}
			})
		}
	}
	// The generated decoder produces concrete registration options for exactly
	// these two providers. Exercise the actual decoder rather than only fixtures.
	for _, tc := range []struct{ kind, field string }{
		{"type-definition", "typeDefinitionProvider"},
		{"implementation", "implementationProvider"},
	} {
		t.Run(tc.kind+"/registration", func(t *testing.T) {
			c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string { return "[]" })
			rawCapability := `{"` + tc.field + `":{"id":"static","documentSelector":[{"language":"custom"}],"workDoneProgress":false}}`
			if err := json.Unmarshal([]byte(rawCapability), &c.capabilities); err != nil {
				t.Fatal(err)
			}
			raw, err := c.RequestNavigation(context.Background(), tc.kind, "custom:source", protocol.Position{}, false)
			if err != nil || string(raw) != "[]" || calls.Load() != 1 {
				t.Fatalf("static registration refused: %q %v calls=%d", raw, err, calls.Load())
			}
		})
	}
}

func TestNavigationUnknownAndTypedNilCapabilitiesDoNotProbe(t *testing.T) {
	values := []any{
		nil, false, "true", 1, map[string]any{}, map[string]any(nil), json.RawMessage("true"),
		(*bool)(nil),
		(*protocol.DefinitionOptions)(nil), (*protocol.TypeDefinitionOptions)(nil),
		(*protocol.ImplementationOptions)(nil), (*protocol.ReferenceOptions)(nil),
		(*protocol.DefinitionRegistrationOptions)(nil), (*protocol.TypeDefinitionRegistrationOptions)(nil),
		(*protocol.ImplementationRegistrationOptions)(nil), (*protocol.ReferenceRegistrationOptions)(nil),
		&protocol.DefinitionOptions{}, &protocol.TypeDefinitionOptions{},
		&protocol.ImplementationOptions{}, &protocol.ReferenceOptions{},
		protocol.DefinitionRegistrationOptions{}, protocol.ReferenceRegistrationOptions{},
	}
	for _, kind := range navigationKinds {
		t.Run(kind, func(t *testing.T) {
			c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string { return "null" })
			for _, value := range values {
				setNavigationCapability(c, kind, value)
				raw, err := c.RequestNavigation(context.Background(), kind, "file:///test", protocol.Position{}, false)
				if !errors.Is(err, ErrNavigationUnsupported) || raw != nil {
					t.Fatalf("unsupported %T admitted: %q %v", value, raw, err)
				}
			}
			// A concrete option for a different provider is not a grant either.
			wrong := protocol.ReferenceOptions{}
			if kind == "references" {
				setNavigationCapability(c, kind, protocol.DefinitionOptions{})
			} else {
				setNavigationCapability(c, kind, wrong)
			}
			if _, err := c.RequestNavigation(context.Background(), kind, "file:///test", protocol.Position{}, false); !errors.Is(err, ErrNavigationUnsupported) {
				t.Fatalf("mismatched options admitted: %v", err)
			}
			if calls.Load() != 0 {
				t.Fatal("unsupported capability dispatched a speculative request")
			}
		})
	}
}

func TestNavigationFramedMethodsAndParameters(t *testing.T) {
	for _, tc := range []struct {
		kind, method string
		include      bool
	}{
		{"definition", "textDocument/definition", false},
		{"type-definition", "textDocument/typeDefinition", false},
		{"implementation", "textDocument/implementation", false},
		{"references", "textDocument/references", false},
		{"references", "textDocument/references", true},
	} {
		t.Run(tc.kind+"/"+strconv.FormatBool(tc.include), func(t *testing.T) {
			c, calls := codeActionWireClient(t, "", func(request codeActionWireRequest) string {
				if request.Method != tc.method {
					t.Errorf("method = %q, want %q", request.Method, tc.method)
				}
				want := `{"textDocument":{"uri":"custom:space%20and%20%E9%9B%AA"},"position":{"line":23,"character":7}`
				if tc.kind == "references" {
					want += `,"context":{"includeDeclaration":` + strconv.FormatBool(tc.include) + `}`
				}
				want += "}"
				if string(request.Params) != want {
					t.Errorf("params:\ngot  %s\nwant %s", request.Params, want)
				}
				return "null"
			})
			setNavigationCapability(c, tc.kind, true)
			raw, err := c.RequestNavigation(context.Background(), tc.kind, "custom:space%20and%20%E9%9B%AA", protocol.Position{Line: 23, Character: 7}, tc.include)
			if err != nil || string(raw) != "null" || calls.Load() != 1 {
				t.Fatalf("request: %q %v calls=%d", raw, err, calls.Load())
			}
		})
	}
}

func TestNavigationInvalidInputsNeverDispatch(t *testing.T) {
	c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string { return "null" })
	for _, kind := range navigationKinds {
		setNavigationCapability(c, kind, true)
	}
	for _, tc := range []struct {
		name, kind, uri string
		position        protocol.Position
		include         bool
	}{
		{name: "empty-kind", uri: "file:///test"},
		{name: "unknown-kind", kind: "declaration", uri: "file:///test"},
		{name: "wire-kind", kind: "textDocument/definition", uri: "file:///test"},
		{name: "empty-uri", kind: "definition"},
		{name: "large-uri", kind: "definition", uri: strings.Repeat("a", 4097)},
		{name: "nul-uri", kind: "definition", uri: "file:///te\x00st"},
		{name: "invalid-utf8-uri", kind: "definition", uri: "file:///\xff"},
		{name: "line-overflow", kind: "definition", uri: "file:///test", position: protocol.Position{Line: math.MaxInt32 + 1}},
		{name: "character-overflow", kind: "references", uri: "file:///test", position: protocol.Position{Character: math.MaxUint32}},
		{name: "definition-context", kind: "definition", uri: "file:///test", include: true},
		{name: "type-context", kind: "type-definition", uri: "file:///test", include: true},
		{name: "implementation-context", kind: "implementation", uri: "file:///test", include: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := c.RequestNavigation(context.Background(), tc.kind, protocol.DocumentURI(tc.uri), tc.position, tc.include)
			if !errors.Is(err, ErrNavigationInvalid) || raw != nil {
				t.Fatalf("invalid input admitted: %q %v", raw, err)
			}
		})
	}
	c.initialized.Store(false)
	if raw, err := c.RequestNavigation(context.Background(), "definition", "file:///test", protocol.Position{}, false); err == nil || raw != nil {
		t.Fatal("uninitialized request admitted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid or uninitialized request dispatched")
	}
}

func TestNavigationInclusivePositionAndURIBounds(t *testing.T) {
	uri := "custom:" + strings.Repeat("雪", 1363) // Exactly 4096 UTF-8 bytes.
	c, calls := codeActionWireClient(t, "", func(request codeActionWireRequest) string {
		var got struct {
			TextDocument struct{ URI string } `json:"textDocument"`
			Position     struct {
				Line      uint64 `json:"line"`
				Character uint64 `json:"character"`
			} `json:"position"`
		}
		if err := json.Unmarshal(request.Params, &got); err != nil {
			t.Error(err)
		}
		if got.TextDocument.URI != uri || got.Position.Line != 2147483647 || got.Position.Character != 2147483647 {
			t.Errorf("boundary parameters changed: %s", request.Params)
		}
		return "[]"
	})
	setNavigationCapability(c, "definition", true)
	raw, err := c.RequestNavigation(context.Background(), "definition", protocol.DocumentURI(uri), protocol.Position{Line: math.MaxInt32, Character: math.MaxInt32}, false)
	if err != nil || string(raw) != "[]" || calls.Load() != 1 {
		t.Fatalf("inclusive boundary refused: %q %v", raw, err)
	}
}

func TestNavigationRawResultsRemainOriginalAndDetached(t *testing.T) {
	const object = `{"uri":"custom:target","range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"future":{"n":9007199254740993,"n":18446744073709551615,"fraction":1.2300e+09,"text":"\u0061"}}`
	const link = `[ { "targetUri":"custom:target", "targetRange":{"start":{"line":0,"character":0},"end":{"line":1,"character":0}}, "targetSelectionRange":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}} } ]`
	// Even semantically invalid shapes remain raw evidence for the passive
	// consumer parser. The transport must not normalize or fall back on them.
	responses := []string{"null", "[]", object, "[" + object + "]", link, `{"notALocation":true}`, `[null]`, `true`, `"opaque"`}
	var index atomic.Int32
	c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string {
		return responses[int(index.Add(1))-1]
	})
	setNavigationCapability(c, "definition", true)
	var previous []byte
	for _, want := range responses {
		raw, err := c.RequestNavigation(context.Background(), "definition", "file:///test", protocol.Position{}, false)
		if err != nil || string(raw) != want {
			t.Fatalf("raw result changed: got %q want %q err=%v", raw, want, err)
		}
		if previous != nil && !bytes.Equal(previous, bytes.Repeat([]byte{'x'}, len(previous))) {
			t.Fatal("a later request mutated an earlier returned result")
		}
		for i := range raw {
			raw[i] = 'x'
		}
		previous = raw
	}
	if calls.Load() != int32(len(responses)) {
		t.Fatal("raw response triggered an alternate method or execution")
	}
}

func TestNavigationRawResultBoundsAndSyntax(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		valid          bool
	}{
		{"limit", `"` + strings.Repeat("a", 1048574) + `"`, true},
		{"oversize", `"` + strings.Repeat("a", 1048575) + `"`, false},
		{"invalid-utf8", "\"\xff\"", false},
		{"malformed", `[{"uri":}]`, false},
		{"multiple-values", `[] []`, false},
		{"empty-result", " ", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string { return tc.response })
			setNavigationCapability(c, "definition", true)
			raw, err := c.RequestNavigation(context.Background(), "definition", "file:///test", protocol.Position{}, false)
			if tc.valid {
				if err != nil || string(raw) != tc.response {
					t.Fatalf("exact raw boundary refused: len=%d %v", len(raw), err)
				}
			} else if err == nil || raw != nil {
				t.Fatalf("invalid result exposed: len=%d %v", len(raw), err)
			}
			if tc.name == "oversize" || tc.name == "invalid-utf8" {
				if !errors.Is(err, ErrNavigationInvalid) {
					t.Fatalf("raw validation identity lost: %v", err)
				}
			}
			if calls.Load() != 1 {
				t.Fatal("result failure retried the request")
			}
		})
	}
}

// The shared result fixture fits the successful/raw/canceled paths above. These
// cases need an original envelope without a result, or an error instead of one.
func navigationEnvelopeClient(t *testing.T, fields string) *Client {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	clientStream, serverStream := net.Pipe()
	conn, err := transport.NewConnection(ctx, clientStream, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{conn: conn}
	c.initialized.Store(true)
	setNavigationCapability(c, "definition", true)
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader := bufio.NewReader(serverStream)
		header, err := textproto.NewReader(reader).ReadMIMEHeader()
		if err != nil {
			return
		}
		size, err := strconv.Atoi(header.Get("Content-Length"))
		if err != nil || size < 0 || size > 16384 {
			t.Errorf("invalid request length: %q", header.Get("Content-Length"))
			return
		}
		raw := make([]byte, size)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return
		}
		var request codeActionWireRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Error(err)
			return
		}
		response := `{"jsonrpc":"2.0","id":` + string(request.ID) + fields + `}`
		_, _ = fmt.Fprintf(serverStream, "Content-Length: %d\r\n\r\n%s", len(response), response)
	}()
	t.Cleanup(func() {
		cancel()
		_ = serverStream.Close()
		if err := conn.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			t.Error(err)
		}
		<-done
	})
	return c
}

func TestNavigationMissingResultAndRPCError(t *testing.T) {
	t.Run("missing-result", func(t *testing.T) {
		c := navigationEnvelopeClient(t, "")
		raw, err := c.RequestNavigation(context.Background(), "definition", "file:///test", protocol.Position{}, false)
		if err == nil || raw != nil {
			t.Fatalf("missing result became null: %q %v", raw, err)
		}
	})
	t.Run("rpc-error", func(t *testing.T) {
		c := navigationEnvelopeClient(t, `,"error":{"code":-32603,"message":"provider failed","data":{"opaque":9007199254740993}}`)
		raw, err := c.RequestNavigation(context.Background(), "definition", "file:///test", protocol.Position{}, false)
		var rpcError *jsonrpc2.Error
		if raw != nil || !errors.As(err, &rpcError) || rpcError.Code != -32603 || rpcError.Message != "provider failed" {
			t.Fatalf("original RPC error lost: %q %v", raw, err)
		}
		if !errors.Is(err, rpcError) {
			t.Fatal("RPC error identity not retained by wrapping")
		}
	})
}

func TestNavigationPreCanceledOutcomesNeverDispatch(t *testing.T) {
	for _, state := range []string{"supported", "unsupported", "invalid-kind", "invalid-uri", "uninitialized"} {
		t.Run(state, func(t *testing.T) {
			c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string { return "null" })
			setNavigationCapability(c, "definition", state != "unsupported")
			kind, uri := "definition", protocol.DocumentURI("file:///test")
			switch state {
			case "invalid-kind":
				kind = "unknown"
			case "invalid-uri":
				uri = ""
			case "uninitialized":
				c.initialized.Store(false)
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			raw, err := c.RequestNavigation(ctx, kind, uri, protocol.Position{}, false)
			if raw != nil || !errors.Is(err, context.Canceled) || calls.Load() != 0 {
				t.Fatalf("canceled %s: raw=%q err=%v calls=%d", state, raw, err, calls.Load())
			}
		})
	}
}

func TestNavigationCancellationAndConnectionReuse(t *testing.T) {
	started := make(chan struct{})
	c, calls := codeActionWireClient(t, "", func(codeActionWireRequest) string {
		select {
		case <-started:
			return "[]"
		default:
			close(started)
			return ""
		}
	})
	setNavigationCapability(c, "references", true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		raw, err := c.RequestNavigation(ctx, "references", "file:///test", protocol.Position{}, false)
		if raw != nil {
			t.Error("canceled request exposed raw result")
		}
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request never reached the framed server")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation identity lost: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request ignored cancellation")
	}
	raw, err := c.RequestNavigation(context.Background(), "references", "file:///test", protocol.Position{}, true)
	if err != nil || string(raw) != "[]" || calls.Load() != 2 || !c.conn.IsConnected() {
		t.Fatalf("cancellation retired or retried connection: %q %v calls=%d", raw, err, calls.Load())
	}
}

// Cancel on the final Err observation to deterministically exercise cancellation
// after Call has settled, without depending on a race between server and caller.
// The underlying context still owns Done and the sticky cancellation state.
type navigationCancelOnCheck struct {
	context.Context
	cancel context.CancelFunc
	checks atomic.Int32
	at     int32
}

func (c *navigationCancelOnCheck) Err() error {
	if c.checks.Add(1) == c.at {
		c.cancel()
	}
	return c.Context.Err()
}

func TestNavigationCancellationAfterResponseAppliesToEveryOutcome(t *testing.T) {
	for _, tc := range []struct{ name, response string }{
		{"null", "null"},
		{"empty", "[]"},
		{"invalid-utf8", "\"\xff\""},
		{"oversize", `"` + strings.Repeat("a", 1048575) + `"`},
		{"malformed", `[`},
		{"rpc-error", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var c *Client
			if tc.name == "rpc-error" {
				c = navigationEnvelopeClient(t, `,"error":{"code":-32603,"message":"failed"}`)
			} else {
				c, _ = codeActionWireClient(t, "", func(codeActionWireRequest) string { return tc.response })
				setNavigationCapability(c, "definition", true)
			}
			base, cancel := context.WithCancel(context.Background())
			defer cancel()
			ctx := &navigationCancelOnCheck{Context: base, cancel: cancel, at: 3}
			raw, err := c.RequestNavigation(ctx, "definition", "file:///test", protocol.Position{}, false)
			if raw != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("post-response cancellation ignored: %q %v", raw, err)
			}
			if tc.name == "rpc-error" {
				var original *jsonrpc2.Error
				if !errors.As(err, &original) || !errors.Is(err, original) {
					t.Fatalf("concurrent cancellation erased RPC identity: %v", err)
				}
			}
		})
	}
}
