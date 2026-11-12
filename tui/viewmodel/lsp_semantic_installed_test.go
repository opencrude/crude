package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/core"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/types"
	"crude/workspace"
)

const installedSemanticSource = "package journey\n\nfunc greet(name string) string { return name }\n"

type installedSemanticFixture struct {
	shell                  remoteJourneyShell
	editor                 *editor.Editor
	runtime                *core.Runtime
	retained               *workspace.Workspace
	path, root, executable string
}

// Use the generic configured provider and an isolated framed core, not a test
// response or a running user core. Its own process is joined by the fixture.
func newInstalledSemanticFixture(t *testing.T) installedSemanticFixture {
	t.Helper()
	executable := os.Getenv("CRUDE_SEMANTIC_GOPLS")
	if executable == "" {
		t.Skip("set CRUDE_SEMANTIC_GOPLS to an installed server")
	}
	executable, err := filepath.EvalSymlinks(executable)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	for name, text := range map[string]string{path: installedSemanticSource, filepath.Join(root, "go.mod"): "module journey\n\ngo 1.22\n"} {
		if err := os.WriteFile(name, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var owned *workspace.Workspace
	runtime := remoteJourneyRuntime(t, "http://unused.invalid", func(w *workspace.Workspace) {
		owned = w
		settings := w.AppDefaults()
		settings.LSP = types.DefaultLSPSettings()
		settings.LSP.AutoDetect = false
		settings.LSP.AutoComplete = false
		settings.LSP.Languages = map[string]types.LSPLanguageSettings{"go": {Enabled: true, Command: executable, Args: []string{"serve"}, RootMarkers: []string{"go.mod"}, Environment: map[string]string{"GOPROXY": "off", "GOSUMDB": "off"}}}
		w.SetAppDefaults(settings)
		grantSemanticFixture(t, w)
	})
	shell := newRemoteJourneyShell(t, runtime, root)
	ed := liveTestOpen(t, shell.app, path)
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	if _, _, err := owned.LSPMgr.ProbeHover(ctx, path, types.LSPSourcePosition{Line: 3, Column: 6}); err != nil {
		t.Fatal(err)
	}
	return installedSemanticFixture{shell, ed, runtime, owned, path, root, executable}
}

// This exercises actual provider tokens through the connected editor path.
func TestInstalledSemanticProviderPaintsActualTokens(t *testing.T) {
	f := newInstalledSemanticFixture(t)
	shell, ed, path, source := f.shell, f.editor, f.path, installedSemanticSource
	shell.app.setFocusEditor()
	shell.app.Input.SetInput("private semantic draft")
	ed.SetCursorPosition(2, 0)
	shell.app.View()
	_, cmd := shell.app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	runAppCommand(t, shell.app, cmd)
	t.Cleanup(func() {
		if t.Failed() {
			if v := shell.app.semanticViews[ed]; v != nil && v.request != nil {
				t.Logf("semantic request: started=%v installed=%v err=%v result=%+v", v.request.started, v.request.installed, v.request.err, v.request.result)
			}
		}
	})
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		v := shell.app.semanticViews[ed]
		if v == nil || v.request == nil || !v.request.installed || v.request.result == nil {
			return false
		}
		set := v.request.result
		target := strings.Index(source, "name string")
		found := false
		for _, span := range set.Spans {
			if span.Start == target && span.End == target+4 && span.Type < len(set.Types) && set.Types[span.Type] == "parameter" {
				found = true
			}
		}
		if !found {
			return false
		}
		shell.app.View()
		r := shell.app.editorRect
		x := r.X + ed.PadL + ed.GutterWidth() + 11
		pixel := shell.app.mainSurf.Buffer().Get(x, r.Y+2)
		want := theme.SyntaxPalette(shell.app.Theme, shell.app.editorSettings.SyntaxTheme).Colors.Code.Property
		return pixel.Char == 'n' && pixel.FG.Equal(rendering.ThemeColorToCell(want))
	})
	if ed.Value() != source || ed.Modified() || shell.app.Input.Value() != "private semantic draft" {
		t.Fatal("semantic provider altered source or private draft")
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != source {
		t.Fatal("semantic provider wrote disk", err)
	}
}
