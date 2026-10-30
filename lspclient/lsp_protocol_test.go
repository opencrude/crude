package lspclient

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

// The helper exercises the real framed subprocess path, without installing or
// starting a language server. Hover serves as a FIFO barrier and trace query.
func TestProtocolServerHelper(t *testing.T) {
	mode := os.Getenv("CRUDE_PROTOCOL_HELPER")
	if mode == "" {
		return
	}
	r := bufio.NewReader(os.Stdin)
	write := func(message any) {
		data, err := json.Marshal(message)
		if err != nil {
			os.Exit(2)
		}
		if _, err := fmt.Fprintf(os.Stdout, "Content-Length: %d\r\n\r\n%s", len(data), data); err != nil {
			os.Exit(2)
		}
	}
	var trace []map[string]any
	for {
		length := 0
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				os.Exit(0)
			}
			if line == "\r\n" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				length, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:")))
			}
		}
		data := make([]byte, length)
		if _, err := io.ReadFull(r, data); err != nil {
			os.Exit(0)
		}
		var msg map[string]any
		if json.Unmarshal(data, &msg) != nil {
			os.Exit(2)
		}
		trace = append(trace, msg)
		method, _ := msg["method"].(string)
		switch method {
		case "initialize":
			cwd, _ := os.Getwd()
			trace = append(trace, map[string]any{"cwd": cwd})
			// Notification before the initialize response catches the old mu
			// lock spanning Initialize (the response reader deadlocked).
			write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": "file:///initial.go", "diagnostics": []any{}}})
			if mode == "cancel" {
				_ = os.WriteFile(os.Getenv("CRUDE_PROTOCOL_READY"), []byte("ready"), 0600)
				continue
			}
			write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{"capabilities": map[string]any{}}})
		case "initialized":
			write(map[string]any{"jsonrpc": "2.0", "id": "configuration", "method": "workspace/configuration", "params": map[string]any{"items": []any{map[string]any{"section": "python.analysis"}, map[string]any{"section": "missing"}, map[string]any{"section": ""}}}})
		case "textDocument/hover":
			if mode == "stale" {
				root, _ := os.Getwd()
				_ = os.WriteFile(filepath.Join(root, "requested"), nil, 0600)
				for {
					if _, err := os.Stat(filepath.Join(root, "release")); err == nil {
						break
					}
					time.Sleep(time.Millisecond)
				}
			}
			data, _ := json.Marshal(trace)
			write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": map[string]any{"contents": map[string]any{"kind": "plaintext", "value": string(data)}}})
			if mode == "stdout-death" {
				_ = os.Stdout.Close()
				for {
					time.Sleep(time.Hour)
				}
			}
		case "textDocument/definition":
			uri := msg["params"].(map[string]any)["textDocument"].(map[string]any)["uri"]
			write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": []any{
				map[string]any{"targetUri": uri, "targetRange": map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 0, "character": 4}}, "targetSelectionRange": map[string]any{"start": map[string]any{"line": 0, "character": 3}, "end": map[string]any{"line": 0, "character": 4}}},
			}})
		case "textDocument/completion":
			write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": []any{
				map[string]any{"label": "color", "insertTextFormat": 2, "textEdit": map[string]any{"range": map[string]any{"start": map[string]any{"line": 0, "character": 3}, "end": map[string]any{"line": 0, "character": 4}}, "newText": "${1|red,blue|};$0"}},
				map[string]any{"label": "unsafe", "insertTextFormat": 2, "insertText": "$TM_FILENAME"},
			}})
		case "textDocument/documentSymbol":
			write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": []any{map[string]any{"name": "symbol", "kind": 12, "range": map[string]any{"start": map[string]any{"line": 0, "character": 3}, "end": map[string]any{"line": 0, "character": 4}}, "selectionRange": map[string]any{"start": map[string]any{"line": 0, "character": 3}, "end": map[string]any{"line": 0, "character": 4}}}}})
		case "shutdown":
			write(map[string]any{"jsonrpc": "2.0", "id": msg["id"], "result": nil})
		case "exit":
			os.Exit(0)
		case "textDocument/didOpen":
			if strings.HasPrefix(mode, "publication-") {
				doc := msg["params"].(map[string]any)["textDocument"].(map[string]any)
				writeProtocolPublications(write, mode, doc)
				continue
			}
			if mode != "progress" {
				continue
			}
			// Report progress the way gopls does: create, begin, publish while
			// still loading, then end. Only the post-end state is settled.
			doc := msg["params"].(map[string]any)["textDocument"].(map[string]any)
			write(map[string]any{"jsonrpc": "2.0", "id": "progress-create", "method": "window/workDoneProgress/create", "params": map[string]any{"token": 7}})
			write(map[string]any{"jsonrpc": "2.0", "method": "$/progress", "params": map[string]any{"token": 7, "value": map[string]any{"kind": "begin", "title": "Loading packages"}}})
			write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": doc["uri"], "version": doc["version"], "diagnostics": []any{map[string]any{"range": map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": map[string]any{"line": 0, "character": 1}}, "message": "could not import (loading)", "severity": 1}}}})
			time.Sleep(50 * time.Millisecond)
			write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": doc["uri"], "version": doc["version"], "diagnostics": []any{map[string]any{"range": map[string]any{"start": map[string]any{"line": 0, "character": 8}, "end": map[string]any{"line": 0, "character": 9}}, "message": "undefined: y", "severity": 1, "source": "compiler", "code": "UndeclaredName"}}}})
			write(map[string]any{"jsonrpc": "2.0", "method": "$/progress", "params": map[string]any{"token": 7, "value": map[string]any{"kind": "end"}}})
		case "textDocument/didChange":
			doc := msg["params"].(map[string]any)["textDocument"].(map[string]any)
			if strings.HasPrefix(mode, "publication-") {
				writeProtocolPublications(write, mode, doc)
				continue
			}
			for _, version := range []float64{doc["version"].(float64) - 1, doc["version"].(float64)} {
				write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": doc["uri"], "version": version, "diagnostics": []any{map[string]any{"range": map[string]any{"start": map[string]any{"line": 0, "character": 3}, "end": map[string]any{"line": 0, "character": 4}}, "message": "wire diagnostic"}}}})
			}
		}
	}
}

func writeProtocolPublications(write func(any), mode string, doc map[string]any) {
	// A known obsolete explicit version must never be mistaken for the new
	// source, including equal-text revisions and close/reopen.
	for i := 0; i < 2; i++ {
		p := map[string]any{"uri": doc["uri"], "diagnostics": []any{
			map[string]any{"range": map[string]any{"start": map[string]any{"line": 0, "character": 3},
				"end": map[string]any{"line": 0, "character": 4}}, "message": "current input", "source": "test", "code": 42},
		}}
		if i == 0 {
			p["version"] = doc["version"].(float64) - 1
			p["diagnostics"].([]any)[0].(map[string]any)["message"] = "obsolete input"
		} else if mode == "publication-versioned" {
			p["version"] = doc["version"]
		}
		write(map[string]any{"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics", "params": p})
	}
}

func protocolIntegration(t *testing.T, mode, root string) *LSPIntegration {
	t.Helper()
	l := NewLSPIntegration(os.Args[0], []string{"-test.run=^TestProtocolServerHelper$"}, string(protocol.URIFromPath(root)), WithServerConfiguration(
		map[string]string{"CRUDE_PROTOCOL_HELPER": mode, "CRUDE_PROTOCOL_READY": filepath.Join(root, "ready")},
		map[string]any{"python": map[string]any{"analysis": map[string]any{"mode": "openFilesOnly"}}},
		map[string]any{"testOption": true},
	))
	t.Cleanup(func() {
		if err := l.Stop(); err != nil {
			t.Error(err)
		}
	})
	return l
}

func TestLSPProtocolConfigurationAndDuplicateOpen(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root with spaces")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	l := protocolIntegration(t, "normal", root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "test.py")
	diagnostics := make(chan []LSPDiagnostic, 2)
	l.OnDiagnostics(func(p types.InsightDiagnosticPublication) {
		if p.Boundary.Source.Path == path {
			diagnostics <- p.Diagnostics
		}
	})
	openTestFile(t, l, ctx, path, "python", "a😀b")
	openTestFile(t, l, ctx, path, "python", "a😀b")
	openTestFile(t, l, ctx, path, "python", "a😀bc")
	definitions := l.RequestDefinition(ctx, path, 0, 3)
	if len(definitions) != 1 || definitions[0].Path != path || definitions[0].Col != 3 {
		t.Fatalf("LocationLink result: %v", definitions)
	}
	items := l.RequestCompletions(ctx, path, 0, 3)
	if len(items) != 1 || items[0].InsertText != "red;" || items[0].TextEdit == nil || items[0].TextEdit.Col != 3 || items[0].TextEdit.NewText != "red;" {
		t.Fatalf("completion edit/snippet: %+v", items)
	}
	symbols := l.RequestDocumentSymbols(ctx, path)
	if len(symbols) != 1 || symbols[0].Col != 3 {
		t.Fatalf("UTF16 symbols: %+v", symbols)
	}
	var trace []map[string]any
	// Two barriers allow the server-initiated configuration response to arrive
	// even when it races the first outgoing hover request.
	for i := 0; i < 2; i++ {
		value, err := l.RequestHover(ctx, path, 0, 3)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(value), &trace); err != nil {
			t.Fatal(err)
		}
	}
	opens, changes, configured, reply := 0, 0, false, false
	for _, msg := range trace {
		if cwd, ok := msg["cwd"]; ok && cwd != root {
			t.Fatalf("cwd = %v", cwd)
		}
		switch msg["method"] {
		case "initialize":
			p := msg["params"].(map[string]any)
			caps := p["capabilities"].(map[string]any)
			window, _ := caps["window"].(map[string]any)
			if len(window) != 1 || window["workDoneProgress"] != true || caps["workspace"].(map[string]any)["applyEdit"] != nil {
				t.Fatalf("unsupported capabilities: %v", caps)
			}
			for _, feature := range caps["textDocument"].(map[string]any) {
				if feature.(map[string]any)["dynamicRegistration"] == true {
					t.Fatal("unsupported dynamic registration")
				}
			}
			if p["rootPath"] != root || p["rootUri"] != string(protocol.URIFromPath(root)) {
				t.Fatalf("root params: %v", p)
			}
			folders := p["workspaceFolders"].([]any)
			if len(folders) != 1 || folders[0].(map[string]any)["uri"] != p["rootUri"] {
				t.Fatalf("folders: %v", folders)
			}
			if p["initializationOptions"].(map[string]any)["testOption"] != true {
				t.Fatal("missing init options")
			}
		case "textDocument/didOpen":
			opens++
		case "textDocument/didChange":
			changes++
		case "workspace/didChangeConfiguration":
			configured = true
		case "workspace/didChangeWatchedFiles":
			t.Fatal("fake root-created notification")
		case "textDocument/hover":
			p := msg["params"].(map[string]any)["position"].(map[string]any)
			if p["character"] != float64(3) {
				t.Fatalf("rune-to-UTF16: %v", p)
			}
		}
		if msg["id"] == "configuration" && msg["method"] == nil {
			result := msg["result"].([]any)
			if result[0].(map[string]any)["mode"] != "openFilesOnly" || result[1] != nil || result[2].(map[string]any)["python"] == nil {
				t.Fatalf("configuration reply: %v", result)
			}
			reply = true
		}
	}
	if opens != 1 || changes != 1 || !configured || !reply {
		t.Fatalf("opens=%d changes=%d configured=%v reply=%v", opens, changes, configured, reply)
	}
	select {
	case ds := <-diagnostics:
		if len(ds) != 1 || ds[0].Col != 3 || ds[0].EndCol != 4 {
			t.Fatalf("wire diagnostics: %v", ds)
		}
	default:
		t.Fatal("current-version diagnostics missing")
	}
	select {
	case ds := <-diagnostics:
		t.Fatalf("stale diagnostics delivered: %v", ds)
	default:
	}
}

func TestLSPProtocolStopCancelsInitialize(t *testing.T) {
	root := t.TempDir()
	l := protocolIntegration(t, "cancel", root)
	started := make(chan error, 1)
	go func() { started <- l.Start(context.Background()) }()
	waitProtocolReady(t, root)
	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("cancelled Start succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Stop failed to join Start")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client != nil || l.launch != nil {
		t.Fatal("cancelled launch installed")
	}
}

func TestLSPProtocolCancelledStartDoesNotInstall(t *testing.T) {
	root := t.TempDir()
	l := protocolIntegration(t, "cancel", root)
	ctx, cancel := context.WithCancel(context.Background())
	started := make(chan error, 1)
	go func() { started <- l.Start(ctx) }()
	waitProtocolReady(t, root)
	cancel()
	select {
	case err := <-started:
		if err == nil {
			t.Fatal("cancelled Start succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("Start ignored cancellation")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.client != nil || l.launch != nil {
		t.Fatal("cancelled launch installed")
	}
}

func TestLSPDiagnosticsVersionsAndUTF16Columns(t *testing.T) {
	l := NewLSPIntegration("unused", nil, "")
	launch := &lspLaunch{}
	l.launch, l.client = launch, &lsp.Client{}
	path := filepath.Join(t.TempDir(), "test.go")
	l.openFiles[path] = &openFileState{text: "a😀bc", version: 3, boundary: testBoundary(path)}
	called := 0
	l.OnDiagnostics(func(p types.InsightDiagnosticPublication) {
		ds := p.Diagnostics
		called++
		if ds[0].Col != 3 || ds[0].EndCol != 4 {
			t.Fatalf("diagnostic columns = %v", ds)
		}

	})
	ds := []protocol.Diagnostic{{Range: protocol.Range{Start: protocol.Position{Character: 3}, End: protocol.Position{Character: 4}}}}
	v := int32(2)
	l.updateDiagnostics(launch, protocol.URIFromPath(path), &v, ds)
	v = 3
	l.updateDiagnostics(launch, protocol.URIFromPath(path), &v, ds)
	l.updateDiagnostics(&lspLaunch{}, protocol.URIFromPath(path), &v, ds)
	delete(l.openFiles, path)
	l.updateDiagnostics(launch, protocol.URIFromPath(path), nil, ds)
	reopened := testBoundary(path)
	reopened.Source.Lifetime = "open-2"
	l.openFiles[path] = &openFileState{text: "a😀bc", version: 4, boundary: reopened}
	l.updateDiagnostics(launch, protocol.URIFromPath(path), nil, ds)
	l.updateDiagnostics(launch, protocol.URIFromPath(path), &v, ds)
	if called != 2 {
		t.Fatalf("diagnostics callbacks = %d", called)
	}
}

func TestLSPVersionsAdvanceOnlyOnAdmittedWrites(t *testing.T) {
	l := protocolIntegration(t, "normal", t.TempDir())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "test.go")
	openTestFile(t, l, ctx, path, "go", "hello")
	openTestFile(t, l, ctx, path, "go", "hello")
	if l.versionCounter != 1 {
		t.Fatalf("duplicate open advanced version: %d", l.versionCounter)
	}
	if err := l.client.Kill(); err != nil {
		t.Fatal(err)
	}
	if version, err := l.UpdateFile(ctx, path, "changed", testBoundary(path)); err == nil || version != 0 {
		t.Fatalf("failed write = %d, %v", version, err)
	}
	// Exercise Notify's own failure too, as when the connection dies between
	// the availability check and the send. This is still the real dead client.
	l.mu.Lock()
	l.pipe = nil
	l.mu.Unlock()
	if version, err := l.UpdateFile(ctx, path, "changed", testBoundary(path)); err == nil || version != 0 ||
		!strings.Contains(err.Error(), "send didChange") {
		t.Fatalf("failed didChange = %d, %v", version, err)
	}
	other := filepath.Join(filepath.Dir(path), "other.go")
	if version, err := l.OpenFile(ctx, other, "go", "hello", testBoundary(other)); err == nil || version != 0 ||
		!strings.Contains(err.Error(), "send didOpen") {
		t.Fatalf("failed didOpen = %d, %v", version, err)
	}
	if l.versionCounter != 1 || l.openFiles[path].text != "hello" {
		t.Fatal("failed write admitted state")
	}
	if l.openFiles[other] != nil {
		t.Fatal("failed open installed document")
	}
}

func TestSnippetPlainTextExpansion(t *testing.T) {
	for _, tc := range []struct {
		in, out string
		ok      bool
	}{
		{"color: ${1|red,blue|};$0", "color: red;", true},
		{"${1:hello} $1 ${2:${3:world}}", "hello hello world", true},
		{`\$ ${1:foo\}bar}`, "$ foo}bar", true},
		{"$TM_FILENAME", "", false},
		{"${1/foo/bar/}", "", false},
		{"${1:unterminated", "", false},
	} {
		got, ok := expandSnippet(tc.in)
		if got != tc.out || ok != tc.ok {
			t.Fatalf("expand %q = %q,%v; want %q,%v", tc.in, got, ok, tc.out, tc.ok)
		}
	}
}

func waitProtocolReady(t *testing.T, root string) {
	waitProtocolFile(t, filepath.Join(root, "ready"))
}

func waitProtocolFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("initialize notification deadlocked or server did not begin")
}

func TestLSPProtocolRequestCannotAttachAfterCloseReopen(t *testing.T) {
	root := t.TempDir()
	l := protocolIntegration(t, "stale", root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "test.go")
	openTestFile(t, l, ctx, path, "go", "a😀b")
	result := make(chan error, 1)
	go func() { _, err := l.RequestHover(ctx, path, 0, 3); result <- err }()
	waitProtocolFile(t, filepath.Join(root, "requested"))
	l.CloseFile(ctx, path)
	openTestFile(t, l, ctx, path, "go", "a😀b")
	if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := <-result; err == nil {
		t.Fatal("stale hover survived close/reopen")
	}
}

func TestLSPProtocolStdoutDeathWithoutWrites(t *testing.T) {
	root := t.TempDir()
	l := protocolIntegration(t, "stdout-death", root)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "test.go")
	openTestFile(t, l, ctx, path, "go", "hello")
	_, _ = l.RequestHover(ctx, path, 0, 0)
	select {
	case <-l.Done():
	case <-time.After(time.Second):
		t.Fatal("stdout EOF not observed")
	}
	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
}
