package viewmodel

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/editor"
	"crude/tui/screens/statusoverview"
	"crude/workspace"
)

func TestLiveDiagnosticNavigationUTF16AndPrivateFocus(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	source, destination := filepath.Join(root, "source.txt"), filepath.Join(root, "destination.txt")
	for path, text := range map[string]string{
		source:      "unsaved source",
		destination: "\t界é😀abc\r\nab\t界é😀XYZ\r\n",
	} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	shell := newRemoteJourneyShell(t, runtime, root)
	a := liveTestOpen(t, shell.app, source)
	a.InsertRune('!')
	wire := editor.LSPDiagnostic{Line: 0, Col: 6, EndLine: 1, EndCol: 9, Severity: editor.DiagError}
	shell.app.jumpToDiagnostic(destination, wire)
	if a.CursorLine() != 0 || a.CursorCol() != 1 || a.Value() != "!unsaved source" {
		t.Fatal("asynchronous navigation moved or replaced old private editor")
	}
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	b := shell.app.EditorState.Editor
	if b.FilePath() != destination || b.CursorLine() != 0 || b.CursorCol() != 9 {
		t.Fatalf("raw diagnostic did not reach visual destination: %s %d:%d", b.FilePath(), b.CursorLine(), b.CursorCol())
	}
	if sl, sc, el, ec := b.SelectionRange(); sl != 0 || sc != 9 || el != 1 || ec != 10 {
		t.Fatalf("selection=%d:%d-%d:%d", sl, sc, el, ec)
	}
	b.SetDiagnostics([]editor.LSPDiagnostic{wire})
	b.SetCursorPosition(1, 10)
	if !shell.app.jumpToNextError() || b.CursorCol() != 9 {
		t.Fatal("F8 converted visual diagnostics a second time")
	}
	if !shell.app.jumpToPrevError() || b.CursorCol() != 9 {
		t.Fatal("Shift-F8 converted visual diagnostics a second time")
	}
	shell.app.activateEditorTab(source)
	shell.app.jumpToDiagnostic(destination, wire)
	queued := shell.app.editorCommands
	shell.app.editorCommands = nil
	result := queued[0]().(paneFilesOpenedMsg)
	shell.app.activateEditorTab(destination)
	b.SetCursorPosition(1, 4)
	runAppCommand(t, shell.app, shell.app.applyPaneFilesOpened(result))
	if shell.app.EditorState.Editor != b || b.CursorLine() != 1 || b.CursorCol() != 4 || a.Value() != "!unsaved source" {
		t.Fatal("late result stole focus, moved later cursor, or lost unsaved source")
	}
	if got := ansi.Strip(shell.app.View().RenderContent()); !strings.Contains(got, "XYZ") || strings.Contains(got, "!unsaved source") {
		t.Fatalf("App painted wrong source after diagnostic navigation:\n%s", got)
	}
	for _, navigate := range []func(){
		func() {
			shell.app.handleDefinitionResult(lspDefinitionResultMsg{locations: []editor.LSPLocation{{Path: destination, Line: 1, Col: 9}}})
		},
		func() { shell.app.jumpToReference(refRow{File: destination, Line: 1, Col: 9}) },
		func() {
			_, cmd := shell.app.Update(statusoverview.NavigateMsg{Action: "diagnostic:1:9:" + destination})
			runAppCommand(t, shell.app, cmd)
		},
	} {
		shell.app.activateEditorTab(source)
		b.SetCursorPosition(1, 0)
		b.ClearSelection()
		before := a.CursorCol()
		navigate()
		if a.CursorCol() != before {
			t.Fatal("definition/reference positioned old editor before destination open")
		}
		runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
		if shell.app.EditorState.Editor != b || b.CursorLine() != 1 || b.CursorCol() != 10 || b.HasSelection() {
			t.Fatal("definition/reference/status point did not use UTF-16 without a range")
		}
	}
}

func TestDiagnosticFirstErrorLocalUTF16Projection(t *testing.T) {
	root, path := createDiagnosticsProject(t)
	if err := os.WriteFile(path, []byte("\t界é😀abc\r\nab\t界é😀XYZ\r\n"), 0600); err != nil {
		t.Fatal(err)
	}
	app := newDiagnosticsTreeApp(t, root)
	wire := []editor.LSPDiagnostic{{Line: 0, Col: 6, EndLine: 1, EndCol: 9, Severity: editor.DiagError}}
	server := &immediateTreeDiagRuntime{filePath: path, diags: wire}
	wsOf(app).LSPMgr.SetRuntime(server)
	cmd, err := app.openEditorTab(path)
	if err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, app, cmd)
	awaitTreeLSP(t, app, path)
	app.gotoFirstDiagnostic()
	ed := app.EditorState.Editor
	if ed.CursorLine() != 0 || ed.CursorCol() != 9 || ed.SelectedText() != "abc\r\nab\t界é😀X" {
		t.Fatalf("local first-error used raw coordinates as visual: %d:%d %q", ed.CursorLine(), ed.CursorCol(), ed.SelectedText())
	}
	if !reflect.DeepEqual(app.ws.LSPMgr().FileDiagnostics(path), wire) {
		t.Fatal("local first-error mutated raw manager cache")
	}
}

func TestLiveDiagnosticsUTF16PublicationCachedOpenThreeShells(t *testing.T) {
	root, path := createDiagnosticsProject(t)
	text := "\t界é😀abc\r\nab\t界é😀XYZ\r\n"
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	wire := []editor.LSPDiagnostic{
		{Line: 0, Col: 6, EndLine: 1, EndCol: 9, Severity: editor.DiagError, Message: "first Unicode error"},
		{Line: 1, Col: 9, EndLine: 1, EndCol: 10, Severity: editor.DiagError, Message: "second Unicode error"},
	}
	server := &immediateTreeDiagRuntime{filePath: path, diags: wire}
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) { w.LSPMgr.SetRuntime(server) })
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	first := liveTestOpen(t, one.app, path)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool {
		return len(one.app.ws.LSPMgr().FileDiagnostics(path)) == 2 && len(two.app.ws.LSPMgr().FileDiagnostics(path)) == 2
	})
	second := liveTestOpen(t, two.app, path) // cached diagnostics before open
	three := newRemoteJourneyShell(t, runtime, root)
	shells = append(shells, three)
	third := liveTestOpen(t, three.app, path) // new attachment's retained snapshot
	editors := []*editor.Editor{first, second, third}
	for publication := range 3 {
		server.mu.Lock()
		client := server.client
		server.mu.Unlock()
		// Different text ensures each publication has an observable state boundary.
		next := append([]editor.LSPDiagnostic(nil), wire...)
		next[0].Message += strings.Repeat("!", publication+1)
		client.publishDiagnostics(next)
		remoteJourneyUntil(t, shells, func() bool {
			for i, shell := range shells {
				shell.app.drainLSPDiagnostics()
				if got := editors[i].Diagnostics(); len(got) != 2 || got[0].Message != next[0].Message {
					return false
				}
			}
			return true
		})
		for i, shell := range shells {
			ed := editors[i]
			if !reflect.DeepEqual(shell.app.ws.LSPMgr().FileDiagnostics(path), next) || !reflect.DeepEqual(ed.DocumentBuffer().Diagnostics(), next) {
				t.Fatalf("shell %d converted raw replica/buffer cache", i)
			}
			ed.SetCursorPosition(0, 0)
			_, cmd := shell.app.Update(tea.KeyPressMsg{Code: tea.KeyF8})
			runAppCommand(t, shell.app, cmd)
			runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
			if ed.CursorLine() != 0 || ed.CursorCol() != 9 {
				t.Fatalf("shell %d first-error UTF-16 landing=%d:%d", i, ed.CursorLine(), ed.CursorCol())
			}
			_, cmd = shell.app.Update(tea.KeyPressMsg{Code: tea.KeyF8})
			runAppCommand(t, shell.app, cmd)
			if ed.CursorLine() != 1 || ed.CursorCol() != 10 {
				t.Fatalf("shell %d F8 double-converted visual coordinates: %d:%d", i, ed.CursorLine(), ed.CursorCol())
			}
			_, cmd = shell.app.Update(tea.KeyPressMsg{Code: tea.KeyF8, Mod: tea.ModShift})
			runAppCommand(t, shell.app, cmd)
			if ed.CursorLine() != 0 || ed.CursorCol() != 9 {
				t.Fatalf("shell %d Shift-F8 double-converted visual coordinates", i)
			}
			frame := ansi.Strip(shell.app.View().RenderContent())
			if !strings.Contains(frame, "XYZ") || ed.Value() != text {
				t.Fatalf("shell %d navigation altered or failed to paint shared source", i)
			}
		}
	}
}
