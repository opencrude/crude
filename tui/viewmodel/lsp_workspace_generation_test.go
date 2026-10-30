package viewmodel

import (
	"path/filepath"
	"testing"

	"crude/tui/editor"
	"crude/types"
	"crude/workspace"
)

func TestLSPResultsFromOldWorkspaceGenerationAreIgnored(t *testing.T) {
	app := newTestApp(t)
	current := editor.NewPreview(app.Theme, "current.go", "", 1)
	app.EditorState.Editor = current
	app.EditorState.ActiveView = "current.go"
	app.EditorState.Tabs["current.go"] = NewEditorTab("current.go", current)

	oldGeneration := app.workspaceSwitchGen.Load()
	app.workspaceSwitchGen.Add(1)
	app.resetLSPRequestContext()

	stalePath := filepath.Join(t.TempDir(), "stale.go")
	_, _ = app.Update(lspDefinitionResultMsg{
		generation: oldGeneration,
		locations:  []editor.LSPLocation{{Path: stalePath}},
	})
	if app.EditorState.Editor != current || app.EditorState.ActiveView != "current.go" {
		t.Fatal("stale definition result opened an old-workspace file")
	}

	_, _ = app.Update(workspace.LSPDiagnosticsMsg{
		Generation: oldGeneration,
		FilePath:   current.FilePath(),
		Facts: types.InsightDiagnostics{ID: "stale-publication", Diagnostics: []workspace.LSPDiagnostic{{
			Severity: workspace.DiagError,
			Message:  "stale",
		}}},
	})
	if got := current.DiagnosticsAt(0, 0); len(got) != 0 {
		t.Fatalf("stale diagnostics reached replacement editor: %+v", got)
	}
}

func TestWorkspaceGenerationCancelsOutstandingLSPContext(t *testing.T) {
	app := newTestApp(t)
	ctx, generation := app.lspRequestContext()
	if generation != app.workspaceSwitchGen.Load() {
		t.Fatalf("request generation=%d current=%d", generation, app.workspaceSwitchGen.Load())
	}

	state := app.beginWorkspaceSwitch()
	defer state.restore(app)
	select {
	case <-ctx.Done():
	default:
		t.Fatal("workspace switch did not cancel the old LSP request context")
	}
}
