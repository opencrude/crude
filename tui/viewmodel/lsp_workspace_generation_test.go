package viewmodel

import (
	"testing"

	"crude/types"
	"crude/workspace"
)

func TestLSPResultsFromOldWorkspaceGenerationAreIgnored(t *testing.T) {
	f := newNavigationConnectedFixture(t, navigationConnectedPoint, "\t😀name\r\n")
	app, current := f.shells[0].app, f.main
	result := f.captureResult(t)
	oldGeneration := app.workspaceSwitchGen.Load()
	app.workspaceSwitchGen.Add(1)
	app.resetLSPRequestContext()

	_, cmd := app.Update(result)
	runAppCommand(t, app, cmd)
	if app.EditorState.Editor != current || app.EditorState.ActiveView != f.engine.main ||
		app.EditorState.Tabs[f.engine.target] != nil || app.navigation != nil {
		t.Fatal("qualified stale result opened an old-workspace file or revived its reader")
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
