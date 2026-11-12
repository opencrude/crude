package viewmodel

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/types"
	"crude/workspace"
)

const highlightConnectedSource = "package journey\n\nvar value = 1\n\nfunc use() int { value = 2; return value }\n"

// The provider marks value's three occurrences and nothing else.
const highlightConnectedAnswer = `[{"range":{"start":{"line":2,"character":4},"end":{"line":2,"character":9}},"kind":3},` +
	`{"range":{"start":{"line":4,"character":17},"end":{"line":4,"character":22}},"kind":3},` +
	`{"range":{"start":{"line":4,"character":35},"end":{"line":4,"character":40}},"kind":2}]`

type highlightConnectedCall struct {
	text      string
	line, col int
}

type highlightConnectedRuntime struct {
	renameConnectedRuntime
	calls []highlightConnectedCall
}

type highlightConnectedClient struct {
	*renameConnectedClient
	engine *highlightConnectedRuntime
}

func (r *highlightConnectedRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	base := r.renameConnectedRuntime.NewClient(spec, root, admit).(*renameConnectedClient)
	return &highlightConnectedClient{renameConnectedClient: base, engine: r}
}

func (c *highlightConnectedClient) RequestDocumentHighlights(_ context.Context, path string, line, col int) (types.LSPHighlightResponse, error) {
	r := c.engine
	r.mu.Lock()
	defer r.mu.Unlock()
	doc := c.docs[path]
	r.calls = append(r.calls, highlightConnectedCall{text: doc.text, line: line, col: col})
	answer := "null"
	if line == 2 && col >= 4 && col <= 9 || line == 4 && (col >= 17 && col <= 22 || col >= 35 && col <= 40) {
		answer = highlightConnectedAnswer
	}
	return types.LSPHighlightResponse{WireVersion: doc.version, JSON: []byte(answer)}, nil
}

func (r *highlightConnectedRuntime) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}

// rest drives one ordinary update and lets the caret rest long enough for the
// occurrence question and its answer.
func highlightRest(t *testing.T, shell remoteJourneyShell) {
	t.Helper()
	_, cmd := shell.app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	runAppCommand(t, shell.app, cmd)
}

func TestConnectedOccurrencesFollowTheRestingCaretOnly(t *testing.T) {
	root := t.TempDir()
	engine := &highlightConnectedRuntime{renameConnectedRuntime: renameConnectedRuntime{main: filepath.Join(root, "source.go")}}
	for path, text := range map[string]string{engine.main: highlightConnectedSource, filepath.Join(root, "go.mod"): "module journey\n\ngo 1.22\n"} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	var owned *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		w.LSPMgr.SetRuntime(engine)
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect, settings.LSP.AutoComplete = false, false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {Enabled: true, Command: executable, RootMarkers: []string{"go.mod"}}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	shells := []remoteJourneyShell{one, two}
	a, b := liveTestOpen(t, one.app, engine.main), liveTestOpen(t, two.app, engine.main)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, _, err := owned.LSPMgr.ProbeHover(ctx, engine.main, types.LSPSourcePosition{Line: 1, Column: 1}); err != nil {
		t.Fatal("fixture source did not synchronize", err)
	}
	remoteJourneyUntil(t, shells, func() bool { return one.app.ws.LSPMgr().ServerReady() && two.app.ws.LSPMgr().ServerReady() })
	one.app.setFocusEditor()
	two.app.setFocusEditor()
	one.app.Input.SetInput("private occurrence draft")

	a.SetCursorPosition(4, 37) // inside "return value"
	b.SetCursorPosition(0, 3)  // "package": nothing to mark
	for _, shell := range shells {
		highlightRest(t, shell)
	}
	remoteJourneyUntil(t, shells, func() bool { return a.OccurrencesCoverCaret() })
	settled := engine.callCount()
	if b.OccurrencesCoverCaret() {
		t.Fatal("a shell without a symbol under its caret showed another shell's occurrences")
	}
	engine.mu.Lock()
	calls := append([]highlightConnectedCall(nil), engine.calls...)
	engine.mu.Unlock()
	asked := map[[2]int]bool{}
	for _, call := range calls {
		if call.text != highlightConnectedSource {
			t.Fatal("occurrences were asked for unsynchronized text", call)
		}
		asked[[2]int{call.line, call.col}] = true
	}
	if !asked[[2]int{4, 37}] || !asked[[2]int{0, 3}] {
		t.Fatal("occurrences were not asked at each shell's exact caret", calls)
	}
	if a.HasSelection() || a.CursorLine() != 4 || a.CursorCol() != 37 || a.Value() != highlightConnectedSource {
		t.Fatal("occurrences moved the caret, selected or edited")
	}

	// Moving within the marked symbol needs no new question; a caret where
	// there is nothing to mark asks once and then stays quiet.
	renameConnectedKey(t, one, tea.KeyPressMsg{Code: tea.KeyRight})
	highlightRest(t, one)
	if !a.OccurrencesCoverCaret() || engine.callCount() != settled {
		t.Fatal("moving inside the symbol dropped its marks or asked again", engine.callCount())
	}
	highlightRest(t, two)
	highlightRest(t, two)
	if engine.callCount() != settled {
		t.Fatal("a resting caret with nothing to mark kept asking", engine.callCount())
	}

	// An edit hides marks that describe the old text at once, before any
	// acknowledgement or new answer.
	one.app.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if a.OccurrencesCoverCaret() {
		t.Fatal("occurrences stayed on text that changed")
	}
	if one.app.Input.Value() != "private occurrence draft" {
		t.Fatal("occurrences consumed the composer draft")
	}
}
