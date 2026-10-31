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
	"crude/types"
	"crude/workspace"
)

func TestLiveUTF16NavigationAndPrivateFocus(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1", func(w *workspace.Workspace) {
		grantSemanticFixture(t, w)
	})
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
	shell.app.queueLiveNavigation(destination, wire)
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
	// Coordinate navigation requires the owner's actual diagnostic-display
	// policy. Before the first LSP replica publication, the editor's default
	// visibility is not evidence that Static-only mode permits these facts.
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		generation, _ := shell.app.ws.LSPMgr().DiagnosticCursor()
		return generation == b.DocumentSnapshot().AnalysisGeneration && shell.app.ws.LSPMgr().ShowDiagnosticsInEditor()
	})
	b.SetDiagnostics(editorDiagnosticFacts(b, []editor.LSPDiagnostic{wire}))
	b.SetCursorPosition(1, 10)
	if !shell.app.jumpToNextError() || b.CursorCol() != 9 {
		t.Fatalf("F8 did not reach the once-projected diagnostic: %d:%d (%s)", b.CursorLine(), b.CursorCol(), b.DiagnosticStatus())
	}
	if !shell.app.jumpToPrevError() || b.CursorCol() != 9 {
		t.Fatal("Shift-F8 converted visual diagnostics a second time")
	}
	shell.app.activateEditorTab(source)
	shell.app.queueLiveNavigation(destination, wire)
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

func TestDiagnosticFirstErrorCanonicalUTF16Projection(t *testing.T) {
	app, ed, service, _ := qualifiedDiagnosticApp(t)
	snapshot := ed.DocumentSnapshot()
	snapshot.Text = []byte("\t界é😀abc\r\nab\t界é😀XYZ\r\n")
	snapshot.Saved = append([]byte(nil), snapshot.Text...)
	if err := ed.InstallDocument(snapshot); err != nil {
		t.Fatal(err)
	}
	wire := []editor.LSPDiagnostic{{Line: 0, Col: 6, EndLine: 1, EndCol: 9, Severity: editor.DiagError}}
	facts := editorDiagnosticFacts(ed, wire)
	service.facts[ed.FilePath()] = facts
	app.drainLSPDiagnostics()
	app.gotoFirstDiagnostic()
	if ed.CursorLine() != 0 || ed.CursorCol() != 9 || ed.SelectedText() != "abc\r\nab\t界é😀X" {
		t.Fatalf("canonical first-error used raw coordinates as visual: %d:%d %q", ed.CursorLine(), ed.CursorCol(), ed.SelectedText())
	}
	if !reflect.DeepEqual(diagnosticList(service, ed.FilePath()), wire) {
		t.Fatal("navigation mutated wire cache")
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
	authority := make(chan *workspace.LSPManager, 1)
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		w.LSPMgr.SetRuntime(server)
		grantSemanticFixture(t, w)
		authority <- w.LSPMgr
	})
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	manager := <-authority
	first := liveTestOpen(t, one.app, path)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool {
		return len(diagnosticList(one.app.ws.LSPMgr(), path)) == 2 && len(diagnosticList(two.app.ws.LSPMgr(), path)) == 2
	})
	assertLSPStartedForRoot(t, server, root)
	second := liveTestOpen(t, two.app, path) // cached diagnostics before open
	three := newRemoteJourneyShell(t, runtime, root)
	shells = append(shells, three)
	third := liveTestOpen(t, three.app, path) // new attachment's retained snapshot
	editors := []*editor.Editor{first, second, third}
	var previous types.InsightDiagnostics
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
			facts, ok := manager.FileDiagnostics(path)
			replica, replicaOK := shell.app.ws.LSPMgr().FileDiagnostics(path)
			if !ok || !replicaOK || facts.ID == "" || replica.ID != facts.ID ||
				ed.DiagnosticFacts().ID != facts.ID || ed.DocumentBuffer().Diagnostics().ID != facts.ID ||
				!reflect.DeepEqual(facts.Boundary, ed.DiagnosticFacts().Boundary) {
				t.Fatalf("shell %d restamped core publication: core=%+v replica=%+v editor=%+v", i, facts, replica, ed.DiagnosticFacts())
			}
			node := shell.app.Tree.Model.NodeByPath(path)
			if node == nil || node.DiagErrors != 2 {
				t.Fatalf("shell %d canonical diagnostics missing from tree: %+v", i, node)
			}
			found := false
			for _, item := range shell.app.problemPanelItems() {
				if item.ID == "problem:"+facts.ID+":0" {
					found = item.PrimaryEnabled && strings.Contains(item.Summary, "partial")
				}
			}
			if !found {
				t.Fatalf("shell %d Problems lost literal core fact identity or qualified navigation", i)
			}
			if previous.ID != "" {
				shell.app.handleLSPDiagnosticsUpdate(workspace.LSPDiagnosticsMsg{
					Generation: shell.app.workspaceSwitchGen.Load(), FilePath: path, Facts: previous,
				})
				if ed.DiagnosticFacts().ID != facts.ID || shell.app.lspFacts[path].ID != facts.ID {
					t.Fatalf("shell %d old event replaced newer replica authority", i)
				}
			}
			if !reflect.DeepEqual(diagnosticList(shell.app.ws.LSPMgr(), path), next) || !reflect.DeepEqual(ed.DocumentBuffer().Diagnostics().Diagnostics, next) {
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
		previous, _ = manager.FileDiagnostics(path)
	}
	first.ClearSelection()
	one.app.setFocusEditor()
	_, editCmd := one.app.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if first.FirstError() != nil {
		t.Fatal("optimistic pending edit kept authorizing old source diagnostics")
	}
	runAppCommand(t, one.app, editCmd)
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	liveTestSync(t, one.app)
	remoteJourneyUntil(t, shells, func() bool {
		for i, shell := range shells {
			shell.app.drainLSPDiagnostics()
			if editors[i].DocumentSnapshot().Revision <= previous.Boundary.Source.Revision || editors[i].FirstError() == nil {
				return false
			}
		}
		return true
	})
	current, ok := manager.FileDiagnostics(path)
	if !ok || current.ID == previous.ID || current.Boundary.Source.Revision <= previous.Boundary.Source.Revision {
		t.Fatal("canonical edit did not create a new source-bound observation")
	}
	for i, shell := range shells {
		shell.app.handleLSPDiagnosticsUpdate(workspace.LSPDiagnosticsMsg{
			Generation: shell.app.workspaceSwitchGen.Load(), FilePath: path, Facts: previous,
		})
		if editors[i].DiagnosticFacts().ID != current.ID ||
			editors[i].DocumentSnapshot().AnalysisGeneration != current.Boundary.Source.WorkspaceGeneration {
			t.Fatalf("shell %d accepted old-source event or restamped generation", i)
		}
	}
}
