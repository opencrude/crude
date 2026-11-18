package lspclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const inlayOriginal = `{"position":{"line":0,"character":3},"label":[{"value":": "},{"value":"Type","location":{"uri":"file:///inert","range":{"start":{"line":2,"character":0},"end":{"line":2,"character":1}}},"command":{"title":"inert","command":"never.execute","arguments":[true]}}],"paddingLeft":true,"data":{"large":9007199254740993,"fraction":1.2300e+09,"escaped":"\u0061"},"unknown":[false,null]}`

// This fixture exercises the initialized framed transport and real document
// synchronization, without provider installation or source files on disk.
func TestInlayAdapterServerHelper(t *testing.T) {
	mode := os.Getenv("CRUDE_INLAY_HELPER")
	if mode == "" {
		return
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	save := func(name string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	write := func(raw string) {
		t.Helper()
		if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(raw), raw); err != nil {
			t.Fatal(err)
		}
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		header, err := textproto.NewReader(reader).ReadMIMEHeader()
		if err != nil {
			return
		}
		size, err := strconv.Atoi(header.Get("Content-Length"))
		if err != nil || size < 0 || size > 4<<20 {
			t.Fatal("bad framed request size")
		}
		raw := make([]byte, size)
		if _, err := io.ReadFull(reader, raw); err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Fatal(err)
		}
		result := "null"
		switch request.Method {
		case "initialize":
			save("initialize.json", request.Params)
			result = `{"capabilities":{"inlayHintProvider":{"resolveProvider":true}}}`
			switch mode {
			case "unsupported":
				result = `{"capabilities":{}}`
			case "false":
				result = `{"capabilities":{"inlayHintProvider":false}}`
			case "no-resolve":
				result = `{"capabilities":{"inlayHintProvider":true}}`
			}
		case "textDocument/inlayHint", "inlayHint/resolve":
			save("requested.json", raw)
			if strings.HasPrefix(mode, "block-") {
				waitProtocolFile(t, filepath.Join(root, "release"))
			}
			result = "[" + inlayOriginal + "]"
			if request.Method == "inlayHint/resolve" {
				result = strings.TrimSuffix(string(request.Params), "}") + `,"tooltip":{"kind":"markdown","value":"read-only **details**"}}`
			}
			switch mode {
			case "null":
				result = "null"
			case "empty":
				result = "[]"
			case "oversized":
				result = `[{"position":{"line":0,"character":0},"label":"` + strings.Repeat("x", 1<<20) + `"}]`
			case "error":
				write(`{"jsonrpc":"2.0","id":` + string(request.ID) + `,"error":{"code":-32603,"message":"inlay fixture failure"}}`)
				continue
			}
		case "textDocument/hover":
			result = `{"contents":{"kind":"plaintext","value":"barrier"}}`
		case "exit":
			return
		default:
			if len(request.ID) == 0 {
				continue
			}
		}
		if len(request.ID) != 0 {
			write(`{"jsonrpc":"2.0","id":` + string(request.ID) + `,"result":` + result + `}`)
		}
	}
}

func inlayIntegration(t *testing.T, mode string) (*LSPIntegration, string, string, int) {
	t.Helper()
	root := t.TempDir()
	client := NewLSPIntegration(os.Args[0], []string{"-test.run=^TestInlayAdapterServerHelper$"}, string(protocol.URIFromPath(root)),
		WithServerEnv(map[string]string{"CRUDE_INLAY_HELPER": mode}))
	t.Cleanup(func() {
		if err := client.Stop(); err != nil {
			t.Error(err)
		}
	})
	if err := client.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "unsaved source.fixture")
	version, err := client.OpenFile(t.Context(), path, "held-out", "a😀bc\r\nnext\n", testBoundary(path))
	if err != nil {
		t.Fatal(err)
	}
	return client, root, path, version
}

func inlayExpectRefused(t *testing.T, got types.LSPInlayResponse, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(got, types.LSPInlayResponse{}) {
		t.Fatalf("refusal exposed inlay evidence: %+v %v", got, err)
	}
}

func TestInlayAdapterRangeOriginalJSONAndInitialize(t *testing.T) {
	client, root, path, version := inlayIntegration(t, "normal")
	span := types.LSPInlayRange{Start: types.LSPInlayPosition{Line: 0, Character: 1}, End: types.LSPInlayPosition{Line: 1, Character: 4}}
	got, err := client.RequestInlayHints(t.Context(), path, span)
	if err != nil || got.WireVersion != version || !got.ResolveSupported || got.Unsupported ||
		string(got.JSON) != "["+inlayOriginal+"]" || !reflect.DeepEqual(got.Boundary, types.InsightBoundary{}) {
		t.Fatalf("wrong original unqualified inlay response: %+v %v", got, err)
	}
	var request struct {
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(actionFile(t, root, "requested.json"), &request); err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`{"textDocument":{"uri":%q},"range":{"start":{"line":0,"character":1},"end":{"line":1,"character":4}}}`, protocol.URIFromPath(path))
	if request.Method != "textDocument/inlayHint" || string(request.Params) != want {
		t.Fatalf("wrong source/range dispatch: %s %s", request.Method, request.Params)
	}
	var initialization struct {
		Capabilities struct {
			TextDocument map[string]json.RawMessage `json:"textDocument"`
			Workspace    map[string]json.RawMessage `json:"workspace"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(actionFile(t, root, "initialize.json"), &initialization); err != nil {
		t.Fatal(err)
	}
	var advertised struct {
		DynamicRegistration bool `json:"dynamicRegistration"`
		ResolveSupport      struct {
			Properties []string `json:"properties"`
		} `json:"resolveSupport"`
	}
	if err := json.Unmarshal(initialization.Capabilities.TextDocument["inlayHint"], &advertised); err != nil {
		t.Fatal("actual initialized client did not advertise the implemented inlay leaf", err)
	}
	if advertised.DynamicRegistration || !reflect.DeepEqual(advertised.ResolveSupport.Properties, []string{"tooltip", "label.tooltip"}) ||
		len(initialization.Capabilities.Workspace["inlayHint"]) != 0 {
		t.Fatal("initialize advertised unimplemented lazy actions or global refresh")
	}
	got.JSON[0] = 'x'
	again, err := client.RequestInlayHints(t.Context(), path, span)
	if err != nil || string(again.JSON) != "["+inlayOriginal+"]" {
		t.Fatal("caller mutation aliased later inlay evidence", err)
	}
	resolved, err := client.ResolveInlayHint(t.Context(), path, version, []byte(inlayOriginal))
	if err != nil || !resolved.ResolveSupported || resolved.WireVersion != version || resolved.Unsupported {
		t.Fatalf("resolve lost synchronized evidence: %+v %v", resolved, err)
	}
	if err := json.Unmarshal(actionFile(t, root, "requested.json"), &request); err != nil {
		t.Fatal(err)
	}
	if request.Method != "inlayHint/resolve" || string(request.Params) != inlayOriginal {
		t.Fatal("resolve reconstructed or substituted the original provider object")
	}
	if !strings.Contains(string(resolved.JSON), `"large":9007199254740993,"fraction":1.2300e+09,"escaped":"\u0061"`) {
		t.Fatal("resolve lost opaque original numeric/string tokens")
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("adapter observed source from disk instead of synchronization", err)
	}
}

func TestInlayAdapterNegativeAnswersAndInvalidRanges(t *testing.T) {
	for _, mode := range []string{"unsupported", "false", "no-resolve", "null", "empty", "error", "oversized"} {
		t.Run(mode, func(t *testing.T) {
			client, root, path, version := inlayIntegration(t, mode)
			got, err := client.RequestInlayHints(t.Context(), path, types.LSPInlayRange{})
			if mode == "error" || mode == "oversized" {
				inlayExpectRefused(t, got, err)
				return
			}
			want := "[" + inlayOriginal + "]"
			unsupported := mode == "unsupported" || mode == "false"
			switch {
			case unsupported:
				want = ""
			case mode == "null":
				want = "null"
			case mode == "empty":
				want = "[]"
			}
			if err != nil || got.WireVersion != version || got.Unsupported != unsupported || string(got.JSON) != want {
				t.Fatalf("negative outcome lost its exact meaning: %+v %v", got, err)
			}
			if unsupported {
				navigationExpectNoDispatch(t, client, root, path)
			}
			if mode == "no-resolve" {
				got, err := client.ResolveInlayHint(t.Context(), path, version, []byte(inlayOriginal))
				if err != nil || !got.Unsupported || got.JSON != nil || got.ResolveSupported {
					t.Fatalf("request-only advertisement gained resolve: %+v %v", got, err)
				}
			}
		})
	}
	client, root, path, _ := inlayIntegration(t, "normal")
	for _, span := range []types.LSPInlayRange{
		{Start: types.LSPInlayPosition{Character: -1}},
		{Start: types.LSPInlayPosition{Line: 1}, End: types.LSPInlayPosition{Character: 1}},
		{End: types.LSPInlayPosition{Character: 2}}, // inside the emoji's surrogate pair
		{End: types.LSPInlayPosition{Character: 6}}, // CR is not source line content
		{End: types.LSPInlayPosition{Line: 3}},
		{End: types.LSPInlayPosition{Line: math.MaxInt32 + 1}},
	} {
		got, err := client.RequestInlayHints(t.Context(), path, span)
		inlayExpectRefused(t, got, err)
	}
	navigationExpectNoDispatch(t, client, root, path)
	if _, err := client.RequestInlayHints(t.Context(), path, types.LSPInlayRange{
		Start: types.LSPInlayPosition{Line: 2}, End: types.LSPInlayPosition{Line: 2},
	}); err != nil {
		t.Fatal("valid zero-length EOF range was refused", err)
	}
}

func TestInlayAdapterRetiresEveryInflightSourceOutcome(t *testing.T) {
	for _, change := range []string{"update", "same-text-reopen", "cancel"} {
		t.Run(change, func(t *testing.T) {
			client, root, path, _ := inlayIntegration(t, "block-normal")
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			type answer struct {
				response types.LSPInlayResponse
				err      error
			}
			done := make(chan answer, 1)
			go func() {
				out, err := client.RequestInlayHints(ctx, path, types.LSPInlayRange{})
				done <- answer{out, err}
			}()
			waitProtocolFile(t, filepath.Join(root, "requested.json"))
			switch change {
			case "update":
				if _, err := client.UpdateFile(t.Context(), path, "changed", testBoundary(path)); err != nil {
					t.Fatal(err)
				}
			case "same-text-reopen":
				client.CloseFile(t.Context(), path)
				if _, err := client.OpenFile(t.Context(), path, "held-out", "a😀bc\r\nnext\n", testBoundary(path)); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				cancel()
			}
			if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			got := <-done
			inlayExpectRefused(t, got.response, got.err)
			if !errors.Is(got.err, context.Canceled) {
				t.Fatal("late inlay result did not retain cancellation identity", got.err)
			}
			if _, err := client.RequestHover(t.Context(), path, 0, 0); err != nil {
				t.Fatal("cancelled inlay broke the reusable connection", err)
			}
		})
	}
}

func TestInlayAdapterResolveRequiresExactWireAndOriginalObject(t *testing.T) {
	client, root, path, version := inlayIntegration(t, "normal")
	got, err := client.ResolveInlayHint(t.Context(), path, version+1, []byte(inlayOriginal))
	inlayExpectRefused(t, got, err)
	got, err = client.ResolveInlayHint(t.Context(), path, version, []byte(`[]`))
	inlayExpectRefused(t, got, err)
	if !errors.Is(err, lsp.ErrInlayHintInvalid) {
		t.Fatal("malformed raw resolve lost protocol error identity", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	got, err = client.RequestInlayHints(ctx, path, types.LSPInlayRange{})
	inlayExpectRefused(t, got, err)
	navigationExpectNoDispatch(t, client, root, path)
}
