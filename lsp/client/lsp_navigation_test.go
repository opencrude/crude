package lspclient

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/textproto"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// Deliberately opaque metadata and numeric spellings catch typed reconstruction.
const navigationLinks = `[{"targetUri":"file:///unopened%20target.go","targetRange":{"start":{"line":2,"character":0},"end":{"line":4,"character":8}},"targetSelectionRange":{"start":{"line":3,"character":1},"end":{"line":3,"character":5}},"originSelectionRange":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"unknown":{"n":9007199254740993,"fraction":1.2300e+09,"escaped":"\u0061"}}]`

// Like the code-action fixture, this uses the actual framed subprocess and raw
// result tokens, without a provider installation, disk source or running core.
func TestNavigationAdapterServerHelper(t *testing.T) {
	mode := os.Getenv("CRUDE_NAVIGATION_HELPER")
	if mode == "" {
		return
	}
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
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
			result = `{"capabilities":{"definitionProvider":true,"typeDefinitionProvider":{},"implementationProvider":{"id":"static","documentSelector":null},"referencesProvider":true}}`
			if mode == "unsupported" {
				result = `{"capabilities":{}}`
			}
			if mode == "false" {
				result = `{"capabilities":{"definitionProvider":false,"typeDefinitionProvider":false,"implementationProvider":false,"referencesProvider":false}}`
			}
		case "textDocument/definition", "textDocument/typeDefinition", "textDocument/implementation", "textDocument/references":
			if err := os.WriteFile(filepath.Join(root, "requested.json"), raw, 0600); err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(mode, "block-") {
				waitProtocolFile(t, filepath.Join(root, "release"))
			}
			result = navigationLinks
			if strings.HasSuffix(mode, "null") {
				result = "null"
			}
			if strings.HasSuffix(mode, "empty") {
				result = "[]"
			}
			if mode == "exact-bytes" || mode == "over-bytes" {
				size := 1 << 20
				if mode == "over-bytes" {
					size++
				}
				// A syntactically valid but semantically opaque result. The
				// adapter must not duplicate the passive core parser.
				result = `"` + strings.Repeat("x", size-2) + `"`
			}
			if strings.HasSuffix(mode, "error") {
				write(`{"jsonrpc":"2.0","id":` + string(request.ID) + `,"error":{"code":-32603,"message":"navigation fixture failure"}}`)
				continue
			}
		case "textDocument/hover":
			// FIFO barrier: all prior dispatches have now been observed.
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

func navigationIntegration(t *testing.T, mode string) (*LSPIntegration, string, string, int) {
	t.Helper()
	root := t.TempDir()
	integration := NewLSPIntegration(os.Args[0], []string{"-test.run=^TestNavigationAdapterServerHelper$"}, string(protocol.URIFromPath(root)),
		WithServerEnv(map[string]string{"CRUDE_NAVIGATION_HELPER": mode}))
	t.Cleanup(func() {
		if err := integration.Stop(); err != nil {
			t.Error(err)
		}
	})
	if err := integration.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "unsaved source.fixture")
	version, err := integration.OpenFile(t.Context(), path, "held-out", "a😀bc\r\nnext\n", testBoundary(path))
	if err != nil {
		t.Fatal(err)
	}
	return integration, root, path, version
}

func navigationExpectRefused(t *testing.T, got types.LSPNavigationResponse, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(got, types.LSPNavigationResponse{}) {
		t.Fatalf("refusal exposed response evidence: %+v %v", got, err)
	}
}

func navigationExpectNoDispatch(t *testing.T, integration *LSPIntegration, root, path string) {
	t.Helper()
	if _, err := integration.RequestHover(t.Context(), path, 0, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "requested.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid or unsupported navigation reached provider", err)
	}
}

func TestNavigationAdapterMethodsAndOriginalJSON(t *testing.T) {
	integration, root, path, version := navigationIntegration(t, "normal")
	for _, tc := range []struct {
		kind, method       string
		includeDeclaration bool
	}{
		{types.LSPNavigationDefinition, "textDocument/definition", false},
		{types.LSPNavigationTypeDefinition, "textDocument/typeDefinition", false},
		{types.LSPNavigationImplementation, "textDocument/implementation", false},
		{types.LSPNavigationReferences, "textDocument/references", false},
		{types.LSPNavigationReferences, "textDocument/references", true},
	} {
		t.Run(fmt.Sprintf("%s/%v", tc.kind, tc.includeDeclaration), func(t *testing.T) {
			out, err := integration.RequestNavigation(t.Context(), path, tc.kind, 0, 3, tc.includeDeclaration)
			if err != nil || out.WireVersion != version || out.Kind != tc.kind || out.Unsupported ||
				string(out.JSON) != navigationLinks || !reflect.DeepEqual(out.Boundary, types.InsightBoundary{}) {
				t.Fatalf("wrong original unqualified navigation evidence: %+v %v", out, err)
			}
			var request struct {
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := json.Unmarshal(actionFile(t, root, "requested.json"), &request); err != nil {
				t.Fatal(err)
			}
			wantParams := fmt.Sprintf(`{"textDocument":{"uri":%q},"position":{"line":0,"character":3}`, protocol.URIFromPath(path))
			if tc.kind == types.LSPNavigationReferences {
				wantParams += fmt.Sprintf(`,"context":{"includeDeclaration":%v}`, tc.includeDeclaration)
			}
			wantParams += "}"
			if request.Method != tc.method || string(request.Params) != wantParams {
				t.Fatalf("wrong navigation dispatch: %s %s; want %s %s", request.Method, request.Params, tc.method, wantParams)
			}
			// Caller mutation must not affect a later request's raw evidence.
			for i := range out.JSON {
				out.JSON[i] = 'x'
			}
			again, err := integration.RequestNavigation(t.Context(), path, tc.kind, 0, 3, tc.includeDeclaration)
			if err != nil || string(again.JSON) != navigationLinks {
				t.Fatal("result aliased later transport evidence", err)
			}
		})
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("test must inspect unsaved synchronized bytes, not disk")
	}
}

func TestNavigationAdapterNegativeAnswers(t *testing.T) {
	for _, mode := range []string{"null", "empty", "unsupported", "false", "error"} {
		t.Run(mode, func(t *testing.T) {
			integration, root, path, version := navigationIntegration(t, mode)
			for _, kind := range []string{types.LSPNavigationDefinition, types.LSPNavigationTypeDefinition, types.LSPNavigationImplementation, types.LSPNavigationReferences} {
				out, err := integration.RequestNavigation(t.Context(), path, kind, 0, 1, false)
				if mode == "error" {
					navigationExpectRefused(t, out, err)
					if !strings.Contains(err.Error(), "navigation fixture failure") {
						t.Fatal("provider failure was not preserved", err)
					}
					continue
				}
				want := "null"
				unsupported := mode == "unsupported" || mode == "false"
				if mode == "empty" {
					want = "[]"
				}
				if unsupported {
					want = ""
				}
				if err != nil || out.WireVersion != version || out.Kind != kind || out.Unsupported != unsupported ||
					string(out.JSON) != want || !reflect.DeepEqual(out.Boundary, types.InsightBoundary{}) {
					t.Fatalf("negative answer collapsed or lost exact source: %+v %v", out, err)
				}
				if unsupported && out.JSON != nil {
					t.Fatal("unsupported answer invented JSON")
				}
			}
			if mode == "unsupported" || mode == "false" {
				navigationExpectNoDispatch(t, integration, root, path)
			}
		})
	}
}

func TestNavigationAdapterInvalidSourceAndInvocation(t *testing.T) {
	integration, root, path, _ := navigationIntegration(t, "normal")
	for _, kind := range []string{"", "Definition", "typeDefinition", "textDocument/definition", "rename"} {
		out, err := integration.RequestNavigation(t.Context(), path, kind, 0, 1, false)
		navigationExpectRefused(t, out, err)
	}
	for _, kind := range []string{types.LSPNavigationDefinition, types.LSPNavigationTypeDefinition, types.LSPNavigationImplementation} {
		out, err := integration.RequestNavigation(t.Context(), path, kind, 0, 1, true)
		navigationExpectRefused(t, out, err)
	}
	for _, position := range [][2]int{
		{-1, 0}, {0, -1}, {1 << 31, 0}, {0, 1 << 31},
		{0, 2}, // Inside the emoji's UTF-16 surrogate pair.
		{0, 6}, // CRLF is not source-line content.
		{1, 5}, {2, 1}, {3, 0},
	} {
		out, err := integration.RequestNavigation(t.Context(), path, types.LSPNavigationDefinition, position[0], position[1], false)
		navigationExpectRefused(t, out, err)
	}
	out, err := integration.RequestNavigation(t.Context(), path+".not-open", types.LSPNavigationDefinition, 0, 0, false)
	navigationExpectRefused(t, out, err)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out, err = integration.RequestNavigation(ctx, path, types.LSPNavigationDefinition, 0, 1, false)
	navigationExpectRefused(t, out, err)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("pre-cancellation was not preserved", err)
	}
	for _, text := range []string{strings.Repeat("x", (2<<20)+1), "\xff", "\xed\xa0\x80"} {
		b := testBoundary(path)
		b.Source.Revision++
		if _, err := integration.UpdateFile(t.Context(), path, text, b); err != nil {
			t.Fatal(err)
		}
		out, err := integration.RequestNavigation(t.Context(), path, types.LSPNavigationDefinition, 0, 0, false)
		navigationExpectRefused(t, out, err)
	}
	navigationExpectNoDispatch(t, integration, root, path)
}

func TestNavigationAdapterExactSourceEndpointsAndBounds(t *testing.T) {
	integration, _, path, _ := navigationIntegration(t, "empty")
	for _, position := range [][2]int{{0, 0}, {0, 1}, {0, 3}, {0, 5}, {1, 4}, {2, 0}} {
		out, err := integration.RequestNavigation(t.Context(), path, types.LSPNavigationDefinition, position[0], position[1], false)
		if err != nil || string(out.JSON) != "[]" {
			t.Fatalf("valid exact UTF-16 endpoint %v refused: %+v %v", position, out, err)
		}
	}
	b := testBoundary(path)
	b.Source.Revision++
	if _, err := integration.UpdateFile(t.Context(), path, strings.Repeat("x", 2<<20), b); err != nil {
		t.Fatal(err)
	}
	out, err := integration.RequestNavigation(t.Context(), path, types.LSPNavigationDefinition, 0, 2<<20, false)
	if err != nil || string(out.JSON) != "[]" {
		t.Fatal("exact source-byte bound or endpoint refused", err)
	}
	for _, mode := range []string{"exact-bytes", "over-bytes"} {
		t.Run(mode, func(t *testing.T) {
			integration, _, path, _ := navigationIntegration(t, mode)
			out, err := integration.RequestNavigation(t.Context(), path, types.LSPNavigationDefinition, 0, 1, false)
			if mode == "over-bytes" {
				navigationExpectRefused(t, out, err)
				if !errors.Is(err, lsp.ErrNavigationInvalid) {
					t.Fatal("raw bound refusal lost transport error", err)
				}
				return
			}
			if err != nil || len(out.JSON) != 1<<20 || out.JSON[0] != '"' || out.JSON[len(out.JSON)-1] != '"' {
				t.Fatal("exact raw bound refused, reconstructed or truncated", len(out.JSON), err)
			}
		})
	}
}

func navigationRetire(t *testing.T, integration *LSPIntegration, path, boundary string, cancel context.CancelFunc) func() {
	t.Helper()
	switch boundary {
	case "update", "equal-text":
		text := "changed"
		if boundary == "equal-text" {
			text = "a😀bc\r\nnext\n"
		}
		b := testBoundary(path)
		b.Source.Revision++
		if _, err := integration.UpdateFile(t.Context(), path, text, b); err != nil {
			t.Fatal(err)
		}
	case "reopen":
		integration.CloseFile(t.Context(), path)
		if _, err := integration.OpenFile(t.Context(), path, "held-out", "a😀bc\r\nnext\n", testBoundary(path)); err != nil {
			t.Fatal(err)
		}
	case "provider":
		integration.mu.Lock()
		retired := integration.client
		integration.client = new(lsp.Client)
		integration.mu.Unlock()
		return func() {
			integration.mu.Lock()
			integration.client = retired
			integration.mu.Unlock()
		}
	case "cancel":
		cancel()
	default:
		t.Fatal("unknown retirement boundary", boundary)
	}
	return func() {}
}

func TestNavigationAdapterLateReplies(t *testing.T) {
	for _, reply := range []string{"positive", "null", "empty", "error"} {
		for _, boundary := range []string{"update", "equal-text", "reopen", "provider", "cancel"} {
			t.Run(reply+"/"+boundary, func(t *testing.T) {
				integration, root, path, _ := navigationIntegration(t, "block-"+reply)
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				type response struct {
					out types.LSPNavigationResponse
					err error
				}
				done := make(chan response, 1)
				go func() {
					out, err := integration.RequestNavigation(ctx, path, types.LSPNavigationDefinition, 0, 1, false)
					done <- response{out, err}
				}()
				waitProtocolFile(t, filepath.Join(root, "requested.json"))
				restore := navigationRetire(t, integration, path, boundary, cancel)
				defer restore()
				if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
					t.Fatal(err)
				}
				select {
				case got := <-done:
					navigationExpectRefused(t, got.out, got.err)
					if !errors.Is(got.err, context.Canceled) {
						t.Fatal("late provider answer or error survived retirement", got.err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("request ignored retirement")
				}
			})
		}
	}
}

// The fourth Err observation is Powernap's return-time cancellation fence for
// static unsupported: adapter admission, adapter pre-call, client admission,
// client return. Gate that instant to retire a source after the static decision
// but before the adapter can publish it; there is deliberately no RPC to block.
type navigationReturnContext struct {
	context.Context
	calls   atomic.Int32
	reached chan struct{}
	release chan struct{}
}

func (c *navigationReturnContext) Err() error {
	if c.calls.Add(1) == 4 {
		close(c.reached)
		<-c.release
	}
	return c.Context.Err()
}

func TestNavigationAdapterLateUnsupported(t *testing.T) {
	for _, boundary := range []string{"update", "equal-text", "reopen", "provider", "cancel"} {
		t.Run(boundary, func(t *testing.T) {
			integration, root, path, _ := navigationIntegration(t, "false")
			base, cancel := context.WithCancel(t.Context())
			defer cancel()
			ctx := &navigationReturnContext{Context: base, reached: make(chan struct{}), release: make(chan struct{})}
			type response struct {
				out types.LSPNavigationResponse
				err error
			}
			done := make(chan response, 1)
			go func() {
				out, err := integration.RequestNavigation(ctx, path, types.LSPNavigationDefinition, 0, 1, false)
				done <- response{out, err}
			}()
			select {
			case <-ctx.reached:
			case <-time.After(3 * time.Second):
				close(ctx.release)
				t.Fatal("static return cancellation fence not reached")
			}
			restore := navigationRetire(t, integration, path, boundary, cancel)
			defer restore()
			close(ctx.release)
			select {
			case got := <-done:
				navigationExpectRefused(t, got.out, got.err)
				if !errors.Is(got.err, context.Canceled) {
					t.Fatal("unsupported result survived source/provider retirement", got.err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("unsupported request ignored retirement")
			}
			// Restore a temporarily substituted client before the FIFO barrier.
			restore()
			navigationExpectNoDispatch(t, integration, root, path)
		})
	}
}
