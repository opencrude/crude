package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	"github.com/charmbracelet/x/powernap/pkg/transport"
)

type codeActionWireRequest struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

// Uses the real connection/codec and an in-memory framed server, not another
// product exchange. Raw framing also permits genuinely malformed result tests.
func codeActionWireClient(t *testing.T, capability string, reply func(codeActionWireRequest) string) (*Client, *atomic.Int32) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	clientStream, serverStream := net.Pipe()
	conn, err := transport.NewConnection(ctx, clientStream, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := codeActionCapabilityClient(t, capability)
	c.conn = conn
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
				t.Errorf("invalid request: %v", err)
				return
			}
			count.Add(1)
			result := reply(request)
			if result == "" { // Deliberately leave the pending call unanswered.
				continue
			}
			response := `{"jsonrpc":"2.0","id":` + string(request.ID) + `,"result":` + result + `}`
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
	return c, count
}

func codeActionCapabilityClient(t *testing.T, capability string) *Client {
	t.Helper()
	c := new(Client)
	if capability != "" {
		if err := json.Unmarshal([]byte(`{"codeActionProvider":`+capability+`}`), &c.capabilities); err != nil {
			t.Fatal(err)
		}
	}
	c.initialized.Store(true)
	return c
}

func TestCodeActionStaticCapabilityGates(t *testing.T) {
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
		{"empty-options", "{}", true, false},
		{"resolve", `{"resolveProvider":true}`, true, true},
		{"no-resolve", `{"resolveProvider":false}`, true, false},
		{"null-resolve", `{"resolveProvider":null}`, false, false},
		{"string-resolve", `{"resolveProvider":"true"}`, false, false},
		{"number-resolve", `{"resolveProvider":1}`, false, false},
		{"kinds", `{"codeActionKinds":["","custom.fix"],"workDoneProgress":true,"resolveProvider":true}`, true, true},
		{"bad-kinds", `{"codeActionKinds":[1]}`, false, false},
		{"null-kinds", `{"codeActionKinds":null}`, false, false},
		{"string-kinds", `{"codeActionKinds":"quickfix"}`, false, false},
		{"bad-progress", `{"workDoneProgress":1}`, false, false},
		{"open-options", `{"futureOption":{"x":true}}`, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, calls := codeActionWireClient(t, tc.capability, func(request codeActionWireRequest) string {
				if request.Method == "textDocument/codeAction" {
					return "null"
				}
				if request.Method != "codeAction/resolve" {
					t.Errorf("unexpected method %s", request.Method)
				}
				return `{"title":"resolved"}`
			})
			raw, err := c.RequestCodeActions(context.Background(), "file:///test", protocol.Range{}, nil)
			if tc.request {
				if err != nil || string(raw) != "null" {
					t.Fatalf("request failed: %s %v", raw, err)
				}
			} else if !errors.Is(err, ErrCodeActionUnsupported) || raw != nil {
				t.Fatalf("request gate: %s %v", raw, err)
			}
			raw, err = c.ResolveCodeAction(context.Background(), json.RawMessage(`{"title":"original"}`))
			if tc.resolve {
				if err != nil || string(raw) != `{"title":"resolved"}` {
					t.Fatalf("resolve failed: %s %v", raw, err)
				}
			} else if !errors.Is(err, ErrCodeActionResolveUnsupported) || raw != nil {
				t.Fatalf("resolve gate: %s %v", raw, err)
			}
			want := int32(0)
			if tc.request {
				want++
			}
			if tc.resolve {
				want++
			}
			if got := calls.Load(); got != want {
				t.Fatalf("unsupported request dispatched: calls=%d want=%d", got, want)
			}
		})
	}
	// These internal Go values cannot result from initialized JSON. Refuse them
	// rather than broadening the static gate through reflection or marshaling.
	for _, capability := range []any{map[string]any(nil), protocol.CodeActionOptions{ResolveProvider: true}, json.RawMessage("true")} {
		c := new(Client)
		c.initialized.Store(true)
		c.capabilities.CodeActionProvider = capability
		if raw, err := c.RequestCodeActions(context.Background(), "", protocol.Range{}, nil); !errors.Is(err, ErrCodeActionUnsupported) || raw != nil {
			t.Fatalf("noninitialized capability representation admitted: %T %v", capability, err)
		}
	}
	c := codeActionCapabilityClient(t, `{"resolveProvider":true}`)
	c.initialized.Store(false)
	if raw, err := c.RequestCodeActions(context.Background(), "", protocol.Range{}, nil); err == nil || raw != nil {
		t.Fatal("uninitialized request admitted")
	}
	if raw, err := c.ResolveCodeAction(context.Background(), json.RawMessage(`{}`)); err == nil || raw != nil {
		t.Fatal("uninitialized resolve admitted")
	}
}

func TestCodeActionWireParametersAndOpaqueLeaves(t *testing.T) {
	const diagnostic = `{"range":{"start":{"line":2,"character":3},"end":{"line":2,"character":5}},"message":"issue","code":9007199254740993,"data":{"n":18446744073709551615,"fraction":1.2300e+09,"custom":[null,false]}}`
	const original = `{"title":"Fix","kind":"future.custom","isPreferred":false,"data":{"n":9007199254740993,"nested":[18446744073709551615,1.2300e+09,null]},"future":{"present":true}}`
	const resolved = `{"title":"Fix","kind":"future.custom","isPreferred":false,"data":{"n":9007199254740993,"nested":[18446744073709551615,1.2300e+09,null]},"future":{"present":true},"edit":{"changes":{}}}`
	span := protocol.Range{
		Start: protocol.Position{Line: 2, Character: 3},
		End:   protocol.Position{Line: 4, Character: 5},
	}
	c, calls := codeActionWireClient(t, `{"resolveProvider":true}`, func(request codeActionWireRequest) string {
		switch request.Method {
		case "textDocument/codeAction":
			want := `{"textDocument":{"uri":"file:///space%20and%20%E9%9B%AA"},"range":{"start":{"line":2,"character":3},"end":{"line":4,"character":5}},"context":{"diagnostics":[` + diagnostic + `],"triggerKind":1}}`
			if string(request.Params) != want {
				t.Errorf("request parameters changed opaque context or shape:\ngot %s\nwant %s", request.Params, want)
			}
			// A bare command is raw evidence, never an executable transport path.
			return `[` + original + `,{"title":"command","command":"never.execute","arguments":[9007199254740993]}]`
		case "codeAction/resolve":
			if string(request.Params) != original {
				t.Errorf("resolve changed original opaque tokens: %s", request.Params)
			}
			return resolved
		default:
			t.Errorf("unexpected executable method: %s", request.Method)
			return "null"
		}
	})
	diagnostics := []json.RawMessage{json.RawMessage(diagnostic)}
	list, err := c.RequestCodeActions(context.Background(), "file:///space%20and%20%E9%9B%AA", span, diagnostics)
	if err != nil || !bytes.Contains(list, []byte(original)) {
		t.Fatalf("raw action list lost: %s %v", list, err)
	}
	if string(diagnostics[0]) != diagnostic {
		t.Fatal("caller diagnostics mutated")
	}
	action := json.RawMessage(original)
	got, err := c.ResolveCodeAction(context.Background(), action)
	if err != nil || string(got) != resolved {
		t.Fatalf("resolved raw changed: %s %v", got, err)
	}
	if string(action) != original {
		t.Fatal("resolve mutated original")
	}
	for i := range got {
		got[i] = 'x'
	}
	if string(action) != original || !bytes.Contains(list, []byte(original)) {
		t.Fatal("returned result aliases caller input or previous result")
	}
	if calls.Load() != 2 {
		t.Fatal("extra/executable request sent")
	}
}

func TestCodeActionEmptyDiagnosticsAndPositionBoundaries(t *testing.T) {
	c, calls := codeActionWireClient(t, "true", func(request codeActionWireRequest) string {
		const want = `{"textDocument":{"uri":"file:///test"},"range":{"start":{"line":2147483647,"character":2147483647},"end":{"line":2147483647,"character":2147483647}},"context":{"diagnostics":[],"triggerKind":1}}`
		if string(request.Params) != want {
			t.Errorf("empty context or signed-int32 boundary changed: %s", request.Params)
		}
		return "[]"
	})
	span := protocol.Range{
		Start: protocol.Position{Line: 1<<31 - 1, Character: 1<<31 - 1},
		End:   protocol.Position{Line: 1<<31 - 1, Character: 1<<31 - 1},
	}
	for _, diagnostics := range [][]json.RawMessage{nil, {}} {
		if result, err := c.RequestCodeActions(context.Background(), "file:///test", span, diagnostics); err != nil || string(result) != "[]" {
			t.Fatalf("empty list not retained: %s %v", result, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("valid boundary request not sent")
	}
}

func TestCodeActionInvalidInputsNeverCallConnection(t *testing.T) {
	// A nil connection makes any attempted dispatch panic: validation must finish
	// first, including failures at the last diagnostic in a full adjunct.
	c := codeActionCapabilityClient(t, `{"resolveProvider":true}`)
	for _, span := range []protocol.Range{
		{Start: protocol.Position{Line: 1 << 31}, End: protocol.Position{Line: 1 << 31}},
		{Start: protocol.Position{Character: 1 << 31}, End: protocol.Position{Line: 1}},
		{End: protocol.Position{Line: 1 << 31}},
		{End: protocol.Position{Character: 1 << 31}},
		{Start: protocol.Position{Line: 2}, End: protocol.Position{Line: 1, Character: 99}},
		{Start: protocol.Position{Character: 2}, End: protocol.Position{Character: 1}},
	} {
		if raw, err := c.RequestCodeActions(context.Background(), "", span, nil); !errors.Is(err, ErrCodeActionInvalid) || raw != nil {
			t.Fatalf("invalid range admitted: %+v %s %v", span, raw, err)
		}
	}
	badJSON := []json.RawMessage{
		nil, json.RawMessage(""), json.RawMessage("null"), json.RawMessage("true"),
		json.RawMessage("[]"), json.RawMessage("1"), json.RawMessage(`"object"`),
		json.RawMessage(`{"x":`), json.RawMessage(`{} {}`),
		{'{', '"', 'x', '"', ':', '"', 0xff, '"', '}'},
	}
	for _, raw := range badJSON {
		if got, err := c.RequestCodeActions(context.Background(), "", protocol.Range{}, []json.RawMessage{raw}); !errors.Is(err, ErrCodeActionInvalid) || got != nil {
			t.Fatalf("invalid diagnostic admitted: %q %v", raw, err)
		}
		if got, err := c.ResolveCodeAction(context.Background(), raw); !errors.Is(err, ErrCodeActionInvalid) || got != nil {
			t.Fatalf("invalid action admitted: %q %v", raw, err)
		}
	}
	tooMany := make([]json.RawMessage, 257)
	for i := range tooMany {
		tooMany[i] = json.RawMessage(`{}`)
	}
	lastInvalid := append([]json.RawMessage(nil), tooMany[:256]...)
	lastInvalid[255] = json.RawMessage("null")
	for _, diagnostics := range [][]json.RawMessage{
		tooMany, lastInvalid,
		{codeActionSizedObject(262145)},
		{codeActionSizedObject(131072), codeActionSizedObject(131073)},
	} {
		if raw, err := c.RequestCodeActions(context.Background(), "", protocol.Range{}, diagnostics); !errors.Is(err, ErrCodeActionInvalid) || raw != nil {
			t.Fatalf("invalid diagnostic bound admitted: %v", err)
		}
	}
	if raw, err := c.ResolveCodeAction(context.Background(), codeActionSizedObject(65537)); !errors.Is(err, ErrCodeActionInvalid) || raw != nil {
		t.Fatalf("oversized original admitted: %v", err)
	}
}

func codeActionSizedObject(size int) json.RawMessage {
	return json.RawMessage(`{"x":"` + strings.Repeat("a", size-8) + `"}`)
}

func TestCodeActionInputBoundsAreInclusive(t *testing.T) {
	for _, diagnostics := range [][]json.RawMessage{
		{codeActionSizedObject(262144)},
		{codeActionSizedObject(131072), codeActionSizedObject(131072)},
	} {
		c, calls := codeActionWireClient(t, "true", func(codeActionWireRequest) string { return "null" })
		if raw, err := c.RequestCodeActions(context.Background(), "", protocol.Range{}, diagnostics); err != nil || string(raw) != "null" || calls.Load() != 1 {
			t.Fatalf("exact diagnostic byte limit refused: %v", err)
		}
	}
	diagnostics := make([]json.RawMessage, 256)
	for i := range diagnostics {
		diagnostics[i] = json.RawMessage(`{}`)
	}
	c, _ := codeActionWireClient(t, `{"resolveProvider":true}`, func(request codeActionWireRequest) string {
		if request.Method == "codeAction/resolve" {
			if len(request.Params) != 65536 {
				t.Errorf("original bound changed: %d", len(request.Params))
			}
			return string(codeActionSizedObject(65536))
		}
		return "[]"
	})
	if _, err := c.RequestCodeActions(context.Background(), "", protocol.Range{}, diagnostics); err != nil {
		t.Fatalf("exact diagnostic count limit refused: %v", err)
	}
	if raw, err := c.ResolveCodeAction(context.Background(), codeActionSizedObject(65536)); err != nil || len(raw) != 65536 {
		t.Fatalf("exact original/result limit refused: len=%d %v", len(raw), err)
	}
}

func TestCodeActionResultBoundsAndShapes(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		resolve, valid bool
	}{
		{"null-list", "null", false, true},
		{"empty-list", "[]", false, true},
		{"objects", `[{"title":"one"},{"title":"two","data":9007199254740993}]`, false, true},
		{"whitespace", " [ {\"title\": \"one\"} ] ", false, true},
		{"list-limit", "[" + string(codeActionSizedObject(1048574)) + "]", false, true},
		{"list-oversize", "[" + string(codeActionSizedObject(1048575)) + "]", false, false},
		{"not-list", `{}`, false, false},
		{"scalar", `true`, false, false},
		{"string-list", `["action"]`, false, false},
		{"null-item", `[null]`, false, false},
		{"nested-list", `[[]]`, false, false},
		{"malformed", `[{"title":}]`, false, false},
		{"invalid-utf8", "[{\"title\":\"\xff\"}]", false, false},
		{"resolve-object", `{"title":"resolved","data":9007199254740993}`, true, true},
		{"resolve-whitespace", " {\"title\": \"resolved\"} ", true, true},
		{"resolve-limit", string(codeActionSizedObject(65536)), true, true},
		{"resolve-oversize", string(codeActionSizedObject(65537)), true, false},
		{"resolve-null", "null", true, false},
		{"resolve-list", "[]", true, false},
		{"resolve-malformed", `{"title":}`, true, false},
		{"resolve-invalid-utf8", "{\"title\":\"\xff\"}", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, calls := codeActionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string { return tc.response })
			var raw json.RawMessage
			var err error
			if tc.resolve {
				raw, err = c.ResolveCodeAction(context.Background(), json.RawMessage(`{"title":"original"}`))
			} else {
				raw, err = c.RequestCodeActions(context.Background(), "", protocol.Range{}, nil)
			}
			if tc.valid {
				// JSON-RPC RawMessage retains interior bytes, but not surrounding
				// whitespace separating the result from its enclosing object.
				if err != nil || string(raw) != strings.TrimSpace(tc.response) {
					t.Fatalf("valid raw reply changed: bytes=%d err=%v", len(raw), err)
				}
			} else if err == nil || raw != nil {
				t.Fatalf("invalid reply exposed: bytes=%d err=%v", len(raw), err)
			}
			if calls.Load() != 1 {
				t.Fatal("response validation retried or dispatched another method")
			}
		})
	}
}

func TestCodeActionCancellationUsesExistingConnection(t *testing.T) {
	for _, resolve := range []bool{false, true} {
		t.Run(strconv.FormatBool(resolve), func(t *testing.T) {
			started := make(chan struct{})
			c, calls := codeActionWireClient(t, `{"resolveProvider":true}`, func(codeActionWireRequest) string {
				close(started)
				return ""
			})
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				var raw json.RawMessage
				var err error
				if resolve {
					raw, err = c.ResolveCodeAction(ctx, json.RawMessage(`{"title":"original"}`))
				} else {
					raw, err = c.RequestCodeActions(ctx, "", protocol.Range{}, nil)
				}
				if raw != nil {
					t.Error("cancelled call exposed a raw result")
				}
				done <- err
			}()
			select {
			case <-started:
			case <-time.After(time.Second):
				t.Fatal("request did not reach connection")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("cancellation identity lost: %v", err)
				}
			case <-time.After(time.Second):
				t.Fatal("call ignored cancellation")
			}
			if calls.Load() != 1 || !c.conn.IsConnected() {
				t.Fatal("cancellation retried or retired the shared connection")
			}
		})
	}
}

func TestCodeActionClientCapabilityJSONIsNarrowAndDetached(t *testing.T) {
	const want = `{"codeActionLiteralSupport":{"codeActionKind":{"valueSet":["","quickfix","refactor","refactor.extract","refactor.inline","refactor.rewrite","source","source.organizeImports","source.fixAll"]}},"dataSupport":true,"disabledSupport":true,"isPreferredSupport":true,"resolveSupport":{"properties":["edit"]}}`
	capabilities := codeActionClientCapabilities()
	got, err := json.Marshal(capabilities)
	if err != nil || string(got) != want {
		t.Fatalf("capability leaf differs:\n%s\n%v", got, err)
	}
	for _, forbidden := range []string{
		"documentChanges", "changeAnnotations", "resourceOperations", "honorsChangeAnnotations",
		"executeCommand", "applyEdit", "dynamicRegistration", "publishDiagnostics",
	} {
		if bytes.Contains(got, []byte(forbidden)) {
			t.Fatalf("unsupported global capability claimed: %s", forbidden)
		}
	}
	capabilities["resolveSupport"].(map[string]any)["properties"].([]string)[0] = "command"
	capabilities["codeActionLiteralSupport"].(map[string]any)["codeActionKind"].(map[string]any)["valueSet"].([]string)[0] = "changed"
	again, err := json.Marshal(codeActionClientCapabilities())
	if err != nil || string(again) != want {
		t.Fatal("capability constructor returned shared mutable leaves")
	}
	// This test deliberately checks the constructor, not initialize wiring.
	// makeClientCapabilities hookup belongs to the parent integration slice.
}
