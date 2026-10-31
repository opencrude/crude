package lspclient

import (
	"bufio"
	"bytes"
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
	"testing"
	"time"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const actionOriginal = `{"title":"Fix","kind":"custom.fix","data":{"n":9007199254740993,"fraction":1.2300e+09,"extra":[18446744073709551615,null]},"unknown":{"safe":false}}`
const actionDiagnostic = `{"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"message":"issue","code":9007199254740993,"data":{"n":18446744073709551615,"fraction":1.2300e+09},"unknown":[false,null]}`
const actionPointDiagnostic = `{"range":{"start":{"line":0,"character":3},"end":{"line":0,"character":3}},"message":"point","code":"0042"}`
const actionTailDiagnostic = `{"range":{"start":{"line":0,"character":3},"end":{"line":0,"character":5}},"message":"tail"}`

// A real framed subprocess with raw fixtures. It has no provider installation,
// command execution, workspace mutation or dependency on a live core.
func TestCodeActionAdapterServerHelper(t *testing.T) {
	mode := os.Getenv("CRUDE_CODE_ACTION_HELPER")
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
	save := func(name string, raw []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), raw, 0600); err != nil {
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
			result = `{"capabilities":{"codeActionProvider":{"resolveProvider":true}}}`
			if mode == "unsupported" {
				result = `{"capabilities":{}}`
			}
			if mode == "no-resolve" {
				result = `{"capabilities":{"codeActionProvider":true}}`
			}
		case "textDocument/codeAction", "codeAction/resolve":
			save("requested.json", request.Params)
			if strings.HasPrefix(mode, "block-") {
				deadline := time.Now().Add(4 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
						break
					}
					if time.Now().After(deadline) {
						t.Fatal("blocked fixture was not released")
					}
					time.Sleep(time.Millisecond)
				}
			}
			result = "[" + actionOriginal + "]"
			if request.Method == "codeAction/resolve" {
				result = string(request.Params)
			}
			if strings.HasSuffix(mode, "null") {
				result = "null"
			}
			if strings.HasSuffix(mode, "empty") {
				result = "[]"
			}
			if strings.HasSuffix(mode, "error") {
				write(`{"jsonrpc":"2.0","id":` + string(request.ID) + `,"error":{"code":-32603,"message":"fixture failure"}}`)
				continue
			}
		case "textDocument/hover":
			// The notification precedes the hover reply on the same reader.
			if publication, err := os.ReadFile(filepath.Join(root, "publication.json")); err == nil {
				write(`{"jsonrpc":"2.0","method":"textDocument/publishDiagnostics","params":` + string(publication) + `}`)
			}
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

func actionIntegration(t *testing.T, mode string) (*LSPIntegration, string, string, int) {
	t.Helper()
	root := t.TempDir()
	integration := NewLSPIntegration(os.Args[0], []string{"-test.run=^TestCodeActionAdapterServerHelper$"}, string(protocol.URIFromPath(root)),
		WithServerEnv(map[string]string{"CRUDE_CODE_ACTION_HELPER": mode}))
	t.Cleanup(func() {
		if err := integration.Stop(); err != nil {
			t.Error(err)
		}
	})
	if err := integration.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "held-out.fixture")
	version, err := integration.OpenFile(t.Context(), path, "held-out", "a😀bc\r\nnext", testBoundary(path))
	if err != nil {
		t.Fatal(err)
	}
	return integration, root, path, version
}

func actionFile(t *testing.T, root, name string) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func actionPublish(t *testing.T, integration *LSPIntegration, root, path, raw string) {
	t.Helper()
	if !json.Valid([]byte(raw)) {
		// A malformed params body cannot be carried in a syntactically valid
		// enclosing JSON-RPC frame. Exercise that handler refusal directly.
		integration.mu.Lock()
		launch := integration.launch
		integration.mu.Unlock()
		integration.receiveDiagnostics(launch, []byte(raw))
		return
	}
	if err := os.WriteFile(filepath.Join(root, "publication.json"), []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := integration.RequestHover(t.Context(), path, 0, 0); err != nil {
		t.Fatal(err)
	}
}

func actionEnvelope(path, version, diagnostics string) string {
	return fmt.Sprintf(`{"uri":%q,%s"diagnostics":%s}`, protocol.URIFromPath(path), version, diagnostics)
}

func actionExpectEmpty(t *testing.T, got types.LSPCodeActionResponse, err error) {
	t.Helper()
	if err == nil || !reflect.DeepEqual(got, types.LSPCodeActionResponse{}) {
		t.Fatalf("refusal exposed response evidence: %+v %v", got, err)
	}
}

func TestCodeActionAdapterInitializeAndOpaqueTransport(t *testing.T) {
	integration, root, path, version := actionIntegration(t, "normal")
	var initialize struct {
		Capabilities struct {
			TextDocument map[string]json.RawMessage `json:"textDocument"`
			Workspace    map[string]json.RawMessage `json:"workspace"`
		} `json:"capabilities"`
	}
	if err := json.Unmarshal(actionFile(t, root, "initialize.json"), &initialize); err != nil {
		t.Fatal(err)
	}
	const wantCapability = `{"codeActionLiteralSupport":{"codeActionKind":{"valueSet":["","quickfix","refactor","refactor.extract","refactor.inline","refactor.rewrite","source","source.organizeImports","source.fixAll"]}},"dataSupport":true,"disabledSupport":true,"isPreferredSupport":true,"resolveSupport":{"properties":["edit"]}}`
	if string(initialize.Capabilities.TextDocument["codeAction"]) != wantCapability ||
		string(initialize.Capabilities.TextDocument["publishDiagnostics"]) != `{"versionSupport":true}` {
		t.Fatal("initialize did not advertise the narrow actual capability")
	}
	for _, forbidden := range []string{"applyEdit", "workspaceEdit", "executeCommand"} {
		if _, present := initialize.Capabilities.Workspace[forbidden]; present {
			t.Fatalf("initialize granted %s", forbidden)
		}
	}
	actionPublish(t, integration, root, path, actionEnvelope(path, fmt.Sprintf(`"version":%d,`, version), "["+actionDiagnostic+"]"))
	out, err := integration.RequestCodeActions(t.Context(), path, 0, 1, 0, 3)
	if err != nil || string(out.JSON) != "["+actionOriginal+"]" || out.WireVersion != version ||
		!out.ResolveSupported || out.DiagnosticsIncluded != 1 || out.DiagnosticsDetail != "original diagnostics from exact versioned publication" ||
		!reflect.DeepEqual(out.Boundary, types.InsightBoundary{}) {
		t.Fatalf("wrong internal original evidence: %+v %v", out, err)
	}
	wantParams := fmt.Sprintf(`{"textDocument":{"uri":%q},"range":{"start":{"line":0,"character":1},"end":{"line":0,"character":3}},"context":{"diagnostics":[%s],"triggerKind":1}}`, protocol.URIFromPath(path), actionDiagnostic)
	if got := string(actionFile(t, root, "requested.json")); got != wantParams {
		t.Fatalf("wire request changed original numeric/data tokens:\ngot %s\nwant %s", got, wantParams)
	}
	original := []byte(actionOriginal)
	resolved, err := integration.ResolveCodeAction(t.Context(), path, version, original)
	if err != nil || string(resolved.JSON) != actionOriginal || resolved.WireVersion != version ||
		!resolved.ResolveSupported || !reflect.DeepEqual(resolved.Boundary, types.InsightBoundary{}) ||
		string(actionFile(t, root, "requested.json")) != actionOriginal {
		t.Fatalf("resolve reconstructed original evidence: %+v %v", resolved, err)
	}
	resolved.JSON[0] = 'x'
	out.JSON[0] = 'x'
	if string(original) != actionOriginal {
		t.Fatal("caller input aliased result")
	}
	again, err := integration.RequestCodeActions(t.Context(), path, 0, 1, 0, 3)
	if err != nil || string(again.JSON) != "["+actionOriginal+"]" || again.DiagnosticsIncluded != 1 ||
		!bytes.Contains(actionFile(t, root, "requested.json"), []byte(actionDiagnostic)) {
		t.Fatal("caller mutation corrupted later response or retained diagnostics")
	}
}

func TestCodeActionAdapterOverlapAndCaret(t *testing.T) {
	integration, root, path, version := actionIntegration(t, "empty")
	actionPublish(t, integration, root, path, actionEnvelope(path, fmt.Sprintf(`"version":%d,`, version),
		"["+actionDiagnostic+","+actionPointDiagnostic+","+actionTailDiagnostic+"]"))
	for _, tc := range []struct {
		name       string
		from, to   int
		diagnostic string
		count      int
	}{
		{"containing caret", 1, 1, actionDiagnostic, 1},
		{"touching caret", 3, 3, actionPointDiagnostic + "," + actionTailDiagnostic, 2},
		{"at right endpoint", 5, 5, "", 0},
		{"left half-open", 0, 1, "", 0},
		{"exact half-open", 1, 3, actionDiagnostic, 1},
		{"point at selection start", 3, 5, actionPointDiagnostic + "," + actionTailDiagnostic, 2},
		{"selection overlap", 0, 4, actionDiagnostic + "," + actionPointDiagnostic + "," + actionTailDiagnostic, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := integration.RequestCodeActions(t.Context(), path, 0, tc.from, 0, tc.to)
			if err != nil || string(out.JSON) != "[]" || out.DiagnosticsIncluded != tc.count {
				t.Fatalf("wrong overlap/caret diagnostics: %+v %v", out, err)
			}
			want := `"diagnostics":[` + tc.diagnostic + `]`
			if !bytes.Contains(actionFile(t, root, "requested.json"), []byte(want)) {
				t.Fatalf("half-open selection picked incorrect original objects: %s", actionFile(t, root, "requested.json"))
			}
			if tc.count == 0 && out.DiagnosticsDetail != "versioned publication has no matching diagnostics" {
				t.Fatal("no-match evidence conflated with missing or clean publication", out.DiagnosticsDetail)
			}
		})
	}
}

func TestCodeActionAdapterDiagnosticOmissionReasons(t *testing.T) {
	integration, root, path, version := actionIntegration(t, "null")
	versionField := fmt.Sprintf(`"version":%d,`, version)
	valid := actionEnvelope(path, versionField, "["+actionDiagnostic+"]")
	for _, tc := range []struct {
		name, raw, detail string
	}{
		{"versioned empty", actionEnvelope(path, versionField, "[]"), "versioned publication contains no diagnostics"},
		{"unversioned", actionEnvelope(path, "", "["+actionDiagnostic+"]"), "unversioned"},
		{"null version", actionEnvelope(path, `"version":null,`, "["+actionDiagnostic+"]"), "unversioned"},
		{"stale", actionEnvelope(path, `"version":0,`, "[]"), "stale"},
		{"future", actionEnvelope(path, fmt.Sprintf(`"version":%d,`, version+1), "[]"), "stale"},
		{"duplicate version", strings.Replace(valid, versionField, versionField+versionField, 1), "malformed"},
		{"duplicate escaped key", strings.Replace(valid, `"message":"issue"`, `"message":"issue","messa\u0067e":"other"`, 1), "ambiguous"},
		{"duplicate opaque key", strings.Replace(valid, `"n":18446744073709551615`, `"n":1,"\u006e":2`, 1), "ambiguous"},
		{"unpaired opaque surrogate", strings.Replace(valid, `"unknown":[false,null]`, `"unknown":"\ud800"`, 1), "malformed"},
		{"unpaired message surrogate", strings.Replace(valid, `"message":"issue"`, `"message":"\udc00"`, 1), "malformed"},
		{"invalid UTF8", strings.Replace(valid, `"message":"issue"`, "\"message\":\"\xff\"", 1), "malformed"},
		{"missing array", actionEnvelope(path, versionField, "null"), "malformed"},
		{"wrong version type", actionEnvelope(path, `"version":"1",`, "[]"), "malformed"},
		{"missing range", actionEnvelope(path, versionField, `[{"message":"bad"}]`), "malformed"},
		{"missing endpoint", actionEnvelope(path, versionField, `[{"range":{"start":{"line":0,"character":1}},"message":"bad"}]`), "malformed"},
		{"missing coordinate", strings.Replace(valid, `"line":0,"character":1`, `"line":0`, 1), "malformed"},
		{"case coordinate", strings.Replace(valid, `"line":0`, `"Line":0`, 1), "malformed"},
		{"null coordinate", strings.Replace(valid, `"character":1`, `"character":null`, 1), "malformed"},
		{"fractional coordinate", strings.Replace(valid, `"character":1`, `"character":1.0`, 1), "malformed"},
		{"negative coordinate", strings.Replace(valid, `"character":1`, `"character":-1`, 1), "malformed"},
		{"split surrogate", strings.Replace(valid, `"character":1`, `"character":2`, 1), "malformed"},
		{"out of source", strings.Replace(valid, `"character":3`, `"character":99`, 1), "malformed"},
		{"reversed", strings.Replace(valid, `"character":1`, `"character":4`, 1), "malformed"},
		{"wrong message type", strings.Replace(valid, `"message":"issue"`, `"message":null`, 1), "malformed"},
		{"wrong code type", strings.Replace(valid, `"code":9007199254740993`, `"code":{"x":1}`, 1), "malformed"},
		{"malformed after valid", strings.TrimSuffix(valid, "}"), "malformed"},
		{"one invalid disqualifies all", actionEnvelope(path, versionField, "["+actionDiagnostic+`,{"message":"bad"}]`), "malformed"},
		{"overbound count", actionEnvelope(path, versionField, "["+strings.Repeat(actionDiagnostic+",", 256)+actionDiagnostic+"]"), "overbound"},
		{"overbound bytes", strings.Replace(valid, `"unknown":[false,null]`, `"unknown":"`+strings.Repeat("x", 262144)+`"`, 1), "overbound"},
		{"overbound envelope", strings.TrimSuffix(valid, "}") + `,"envelope":"` + strings.Repeat("x", 280000) + `"}`, "overbound"},
		{"limited visible publication", actionEnvelope(path, versionField, "["+strings.Repeat(`{"message":"x"},`, 4096)+`{"message":"x"}]`), "limited"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Install usable old evidence first: omission must not keep it.
			actionPublish(t, integration, root, path, valid)
			actionPublish(t, integration, root, path, tc.raw)
			out, err := integration.RequestCodeActions(t.Context(), path, 0, 1, 0, 3)
			if err != nil || string(out.JSON) != "null" || out.DiagnosticsIncluded != 0 ||
				!strings.Contains(out.DiagnosticsDetail, tc.detail) ||
				!bytes.Contains(actionFile(t, root, "requested.json"), []byte(`"diagnostics":[]`)) {
				t.Fatalf("unsafe adjunct leaked or query itself was refused: %+v %v", out, err)
			}
		})
	}
}

func TestCodeActionAdapterBoundsEmptyAndUnsupported(t *testing.T) {
	for _, mode := range []string{"null", "empty", "unsupported", "no-resolve"} {
		t.Run(mode, func(t *testing.T) {
			integration, root, path, version := actionIntegration(t, mode)
			out, err := integration.RequestCodeActions(t.Context(), path, 0, 3, 1, 4)
			if mode == "unsupported" {
				actionExpectEmpty(t, out, err)
				if !errors.Is(err, lsp.ErrCodeActionUnsupported) {
					t.Fatal(err)
				}
				if _, err := os.Stat(filepath.Join(root, "requested.json")); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("unsupported request reached provider")
				}
				return
			}
			want := "[" + actionOriginal + "]"
			if mode == "null" {
				want = "null"
			}
			if mode == "empty" {
				want = "[]"
			}
			if err != nil || string(out.JSON) != want || out.WireVersion != version ||
				out.DiagnosticsIncluded != 0 || out.DiagnosticsDetail != "diagnostics omitted: not yet published for this source" {
				t.Fatalf("absence collapsed, lost fencing or invented diagnostics: %+v %v", out, err)
			}
			if mode == "no-resolve" {
				if out.ResolveSupported {
					t.Fatal("boolean provider advertised resolve")
				}
				out, err := integration.ResolveCodeAction(t.Context(), path, version, []byte(actionOriginal))
				actionExpectEmpty(t, out, err)
				if !errors.Is(err, lsp.ErrCodeActionResolveUnsupported) {
					t.Fatal(err)
				}
			}
		})
	}
	integration, root, path, version := actionIntegration(t, "normal")
	for _, coordinates := range [][4]int{
		{-1, 0, 0, 0}, {0, -1, 0, 0}, {0, 0, -1, 0}, {0, 0, 0, -1},
		{0, 2, 0, 3}, {0, 1, 0, 2}, {0, 4, 0, 3}, {1, 0, 0, 3},
		{0, 0, 0, 6}, {0, 0, 1, 5}, {0, 0, 2, 0},
		{1 << 31, 0, 1 << 31, 0}, {0, 1 << 31, 0, 1 << 31},
	} {
		out, err := integration.RequestCodeActions(t.Context(), path, coordinates[0], coordinates[1], coordinates[2], coordinates[3])
		actionExpectEmpty(t, out, err)
	}
	out, err := integration.ResolveCodeAction(t.Context(), path, version+1, []byte(actionOriginal))
	actionExpectEmpty(t, out, err)
	if _, err := os.Stat(filepath.Join(root, "requested.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid source position or stale expected version reached provider")
	}
	for _, text := range []string{strings.Repeat("x", (2<<20)+1), "\xff"} {
		boundary := testBoundary(path)
		boundary.Source.Revision++
		if _, err := integration.UpdateFile(t.Context(), path, text, boundary); err != nil {
			t.Fatal(err)
		}
		out, err := integration.RequestCodeActions(t.Context(), path, 0, 0, 0, 0)
		actionExpectEmpty(t, out, err)
	}
}

func TestCodeActionAdapterStaleAndCancelledReplies(t *testing.T) {
	// Each completion shape crosses the identical final check, including a
	// failed RPC and a valid no-action answer. Resolve uses the same tests.
	for _, resolve := range []bool{false, true} {
		for _, reply := range []string{"positive", "null", "empty", "error"} {
			if resolve && (reply == "null" || reply == "empty") {
				continue // Resolve's protocol requires an object.
			}
			for _, boundary := range []string{"version", "equal-text", "reopen", "provider", "cancel"} {
				t.Run(fmt.Sprintf("resolve=%v/%s/%s", resolve, reply, boundary), func(t *testing.T) {
					integration, root, path, version := actionIntegration(t, "block-"+reply)
					ctx, cancel := context.WithCancel(t.Context())
					defer cancel()
					type response struct {
						out types.LSPCodeActionResponse
						err error
					}
					done := make(chan response, 1)
					go func() {
						var r response
						if resolve {
							r.out, r.err = integration.ResolveCodeAction(ctx, path, version, []byte(actionOriginal))
						} else {
							r.out, r.err = integration.RequestCodeActions(ctx, path, 0, 1, 0, 3)
						}
						done <- r
					}()
					waitProtocolFile(t, filepath.Join(root, "requested.json"))
					var retired *lsp.Client
					switch boundary {
					case "version", "equal-text":
						text := "changed"
						if boundary == "equal-text" {
							text = "a😀bc\r\nnext"
						}
						b := testBoundary(path)
						b.Source.Revision++
						if _, err := integration.UpdateFile(t.Context(), path, text, b); err != nil {
							t.Fatal(err)
						}
					case "reopen":
						integration.CloseFile(t.Context(), path)
						if _, err := integration.OpenFile(t.Context(), path, "held-out", "a😀bc\r\nnext", testBoundary(path)); err != nil {
							t.Fatal(err)
						}
					case "provider":
						integration.mu.Lock()
						retired, integration.client = integration.client, new(lsp.Client)
						integration.mu.Unlock()
					case "cancel":
						cancel()
					}
					if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
						t.Fatal(err)
					}
					var result response
					select {
					case result = <-done:
					case <-time.After(2 * time.Second):
						t.Fatal("request ignored retirement")
					}
					if retired != nil {
						integration.mu.Lock()
						integration.client = retired
						integration.mu.Unlock()
					}
					actionExpectEmpty(t, result.out, result.err)
					if !errors.Is(result.err, context.Canceled) {
						t.Fatal("stale error/empty/positive answer not retired", result.err)
					}
				})
			}
		}
	}
}

func TestCodeActionOriginalAdjunctLifetimesAndIsolation(t *testing.T) {
	integration, root, path, version := actionIntegration(t, "empty")
	raw := []byte(actionEnvelope(path, fmt.Sprintf(`"version":%d,`, version), "["+actionDiagnostic+"]"))
	integration.mu.Lock()
	launch := integration.launch
	integration.mu.Unlock()
	// Exercise the actual handler with caller-owned bytes, then overwrite them.
	integration.receiveDiagnostics(launch, raw)
	for i := range raw {
		raw[i] = 'x'
	}
	out, err := integration.RequestCodeActions(t.Context(), path, 0, 1, 0, 3)
	if err != nil || out.DiagnosticsIncluded != 1 ||
		!bytes.Contains(actionFile(t, root, "requested.json"), []byte(actionDiagnostic)) {
		t.Fatal("adjunct retained caller-owned notification memory", out, err)
	}
	// Selection results must also detach from the retained originals.
	integration.mu.Lock()
	state := integration.openFiles[path]
	adjunct := state.actionDiagnostics
	sequence := state.publication.Sequence
	integration.mu.Unlock()
	selected, _ := adjunct.selectRange(version, sequence, 1, 5)
	selected[0][0] = 'x'
	if string(adjunct.diagnostics[0].raw) != actionDiagnostic {
		t.Fatal("adjunct selection returned mutable retained bytes")
	}
	// A retired launch cannot clear or replace current producer evidence.
	integration.receiveDiagnostics(new(lspLaunch), []byte(`{`))
	out, err = integration.RequestCodeActions(t.Context(), path, 0, 1, 0, 3)
	if err != nil || out.DiagnosticsIncluded != 1 {
		t.Fatal("retired launch invalidated current original evidence", out, err)
	}
	b := testBoundary(path)
	b.Source.Revision++
	newVersion, err := integration.UpdateFile(t.Context(), path, "a😀bc\r\nnext", b)
	if err != nil || newVersion == version {
		t.Fatal("equal-text boundary did not advance transport version", err)
	}
	out, err = integration.RequestCodeActions(t.Context(), path, 0, 1, 0, 3)
	if err != nil || out.DiagnosticsIncluded != 0 || !strings.Contains(out.DiagnosticsDetail, "not yet published") {
		t.Fatal("source lifetime reused original adjunct", out, err)
	}
	integration.receiveDiagnostics(launch, []byte(actionEnvelope(path, fmt.Sprintf(`"version":%d,`, newVersion), "["+actionDiagnostic+"]")))
	integration.CloseFile(t.Context(), path)
	if _, err := integration.OpenFile(t.Context(), path, "held-out", "a😀bc\r\nnext", b); err != nil {
		t.Fatal(err)
	}
	out, err = integration.RequestCodeActions(t.Context(), path, 0, 1, 0, 3)
	if err != nil || out.DiagnosticsIncluded != 0 || !strings.Contains(out.DiagnosticsDetail, "not yet published") {
		t.Fatal("equal-text close/reopen reused original adjunct", out, err)
	}
}
