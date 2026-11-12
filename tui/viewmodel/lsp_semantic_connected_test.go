package viewmodel

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/types"
	"crude/workspace"
)

const semanticConnectedSource = "package journey\n\nvar value = 1\n"

type semanticConnectedCall struct{ mode, text, previous string }
type semanticConnectedRuntime struct {
	renameConnectedRuntime
	calls []semanticConnectedCall
}
type semanticConnectedClient struct {
	*renameConnectedClient
	engine *semanticConnectedRuntime
}

func (r *semanticConnectedRuntime) NewClient(spec types.LSPServerSpec, root string, admit func() error) workspace.LSPClient {
	return &semanticConnectedClient{r.renameConnectedRuntime.NewClient(spec, root, admit).(*renameConnectedClient), r}
}
func (c *semanticConnectedClient) RequestSemanticTokens(_ context.Context, path, mode, previous string, _ types.LSPSemanticRange) (types.LSPSemanticResponse, error) {
	r := c.engine
	r.mu.Lock()
	defer r.mu.Unlock()
	d := c.docs[path]
	r.calls = append(r.calls, semanticConnectedCall{mode, d.text, previous})
	out := types.LSPSemanticResponse{WireVersion: d.version, Support: types.LSPSemanticSupport{Types: []string{"function"}, Full: true, Delta: true}}
	switch mode {
	case "full":
		out.JSON = []byte(`{"resultId":"stable","data":[2,4,5,0,0]}`)
	case "full/delta":
		out.JSON = []byte(`{"resultId":"stable","edits":[]}`)
	}
	return out, nil
}
func TestConnectedSemanticColorsSurviveThemeSharedEditsAndPeerFocus(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	for name, text := range map[string]string{path: semanticConnectedSource, filepath.Join(root, "go.mod"): "module journey\n\ngo 1.22\n"} {
		if err := os.WriteFile(name, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	executable, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	engine := &semanticConnectedRuntime{}
	var owned *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		w.LSPMgr.SetRuntime(engine)
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect = false
		settings.LSP.AutoComplete = false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {Enabled: true, Command: executable, RootMarkers: []string{"go.mod"}}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shells := []remoteJourneyShell{newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)}
	eds := []*editor.Editor{liveTestOpen(t, shells[0].app, path), liveTestOpen(t, shells[1].app, path)}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, _, err := owned.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 1, Column: 1}); err != nil {
		t.Fatal(err)
	}
	ink := func(i int) cell.Color {
		app, ed := shells[i].app, eds[i]
		app.View()
		r := app.editorRect
		got := app.mainSurf.Buffer().Get(r.X+ed.PadL+ed.GutterWidth()+4, r.Y+2)
		if got.Char != 'v' {
			t.Fatalf("not painting the source token: %+v", got)
		}
		return got.FG
	}
	for i, shell := range shells {
		shell.app.setFocusEditor()
		shell.app.Input.SetInput(fmt.Sprintf("private %d", i))
		eds[i].SetCursorPosition(2, 9)
		shell.app.View()
		_, cmd := shell.app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
		runAppCommand(t, shell.app, cmd)
	}
	remoteJourneyUntil(t, shells, func() bool {
		for i, shell := range shells {
			v := shell.app.semanticViews[eds[i]]
			if v == nil || v.request == nil || !v.request.installed {
				return false
			}
			want := theme.SyntaxPalette(shell.app.Theme, shell.app.editorSettings.SyntaxTheme).Colors.Code.Function
			if !ink(i).Equal(rendering.ThemeColorToCell(want)) {
				return false
			}
		}
		return true
	})
	if eds[0].Value() != semanticConnectedSource || eds[1].Value() != semanticConnectedSource || shells[0].app.Input.Value() != "private 0" {
		t.Fatal("semantic colors changed source or draft")
	}
	engine.mu.Lock()
	before := len(engine.calls)
	engine.mu.Unlock()
	eds[0].SetTheme(theme.DefaultLight())
	shells[0].app.View()
	engine.mu.Lock()
	after := len(engine.calls)
	engine.mu.Unlock()
	if before != after {
		t.Fatal("theme change requested new tokens")
	}
	// Author typing changes the one shared source; both views must only display
	// evidence for the new acknowledged source, and author Undo remains usable.
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, shells, func() bool {
		if !strings.Contains(eds[0].Value(), "valueX") || eds[1].Value() != eds[0].Value() {
			return false
		}
		for i, shell := range shells {
			v := shell.app.semanticViews[eds[i]]
			if v == nil || v.request == nil || !v.request.installed || v.request.source.Revision != eds[i].DocumentState().Revision {
				return false
			}
		}
		want := theme.SyntaxPalette(shells[1].app.Theme, shells[1].app.editorSettings.SyntaxTheme).Colors.Code.Function
		return ink(1).Equal(rendering.ThemeColorToCell(want))
	})
	engine.mu.Lock()
	calls := append([]semanticConnectedCall(nil), engine.calls...)
	engine.mu.Unlock()
	delta := false
	for _, call := range calls {
		delta = delta || call.mode == "full/delta" && strings.Contains(call.text, "valueX") && call.previous == "stable"
	}
	if !delta {
		t.Fatalf("no source-qualified delta after editing: %+v", calls)
	}
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool {
		return eds[0].Value() == semanticConnectedSource && eds[1].Value() == semanticConnectedSource
	})
	if data, err := os.ReadFile(path); err != nil || string(data) != semanticConnectedSource {
		t.Fatal("semantic coloring wrote disk", err)
	}
	// A second shell can focus a file with no language server. That changes
	// the workspace's active-file status, not this visible file's producer.
	remoteJourneyUntil(t, shells, func() bool {
		v := shells[0].app.semanticViews[eds[0]]
		return v != nil && v.request != nil && v.request.installed && v.request.source.Revision == eds[0].DocumentState().Revision
	})
	originalInk := ink(0)
	plain := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(plain, []byte("no provider here\n"), 0600); err != nil {
		t.Fatal(err)
	}
	liveTestOpen(t, shells[1].app, plain)
	remoteJourneyUntil(t, shells, func() bool {
		lsp := shells[0].app.ws.LSPMgr()
		return !lsp.ServerReady() && lsp.AssistanceCapabilities(path).Producer.InstanceID != ""
	})
	remoteJourneyUntil(t, shells, func() bool {
		v := shells[0].app.semanticViews[eds[0]]
		return v != nil && v.request != nil && v.request.installed &&
			v.request.source.Revision == eds[0].DocumentState().Revision && ink(0).Equal(originalInk)
	})
	eds[0].SetCursorPosition(2, 9)
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'X', Text: "X"})
	remoteJourneyUntil(t, shells, func() bool {
		v := shells[0].app.semanticViews[eds[0]]
		return !shells[0].app.ws.LSPMgr().ServerReady() && strings.Contains(eds[0].Value(), "valueX") && eds[1].Value() == eds[0].Value() && v != nil && v.request != nil && v.request.installed && v.request.source.Revision == eds[0].DocumentState().Revision && ink(0).Equal(originalInk)
	})
	renameConnectedKey(t, shells[0], tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
	remoteJourneyUntil(t, shells, func() bool {
		v := shells[0].app.semanticViews[eds[0]]
		return !shells[0].app.ws.LSPMgr().ServerReady() && eds[0].Value() == semanticConnectedSource && eds[1].Value() == semanticConnectedSource && v != nil && v.request != nil && v.request.installed && v.request.source.Revision == eds[0].DocumentState().Revision && ink(0).Equal(originalInk)
	})
	if shells[0].app.Input.Value() != "private 0" || shells[1].app.EditorState.Editor.Value() != "no provider here\n" {
		t.Fatal("another shell's focus changed the source or private draft")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != semanticConnectedSource {
		t.Fatal("peer focus journey wrote disk", err)
	}
}
