package viewmodel

import (
	"testing"

	"crude/tui/editor"
	"crude/types"
	"crude/workspace"
)

func TestPathlessEditorNeverSchedulesOrRequestsAutomaticSave(t *testing.T) {
	for _, mode := range []types.AutoSaveMode{types.AutoSaveAfterDelay, types.AutoSaveEditorFocusChange, types.AutoSaveTerminalFocusLoss} {
		for _, connected := range []bool{false, true} {
			t.Run(string(mode)+"/connected="+map[bool]string{false: "false", true: "true"}[connected], func(t *testing.T) {
				app := newAutosaveTestApp(t)
				ed := editor.New(app.Theme)
				ed.OpenBuffer(editor.NewBufferManager().CreateScratch())
				t.Cleanup(ed.Close)
				ed.InsertRune('x')
				app.EditorState.Tabs["untitled-test"] = NewEditorTab("untitled-test", ed)
				var state *liveEditorState
				if connected {
					state = &liveEditorState{}
					app.liveEditors = map[*editor.Editor]*liveEditorState{ed: state}
				}
				app.applyEditorSettings(types.EditorSettings{AutoSave: mode, AutoSaveDelay: 1})
				app.onEditorContentChanged(ed, "", "")
				for _, reason := range []workspace.DocumentSaveReason{workspace.DocumentSaveAfterDelay, workspace.DocumentSaveFocusOut} {
					if cmd := app.requestEditorSave(ed, reason, false); cmd != nil {
						t.Fatalf("automatic save returned a command for pathless text: %s", reason)
					}
				}
				if wsOf(app).Documents.PendingDeadlines() != 0 || state != nil && (state.save || !state.due.IsZero()) {
					t.Fatal("pathless text acquired an automatic-save deadline or request")
				}
				if !ed.Modified() || ed.Value() != "x" {
					t.Fatal("automatic save changed or cleared the unnamed draft")
				}
			})
		}
	}
}
