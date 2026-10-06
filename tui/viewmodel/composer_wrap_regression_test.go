package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
)

func TestComposerTypingSpaceInWrappedGapKeepsDraftAndPaintedCursor(t *testing.T) {
	for _, width := range []int{80, 160} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			app := newFullTestApp(t)
			app.Logger = stubLogger{}
			app.width, app.height = width, 40
			app.AddOrchestrator("owner")
			app.maybeHydrateComposerDraft()
			app.paintShellFrame()
			pad := app.Theme.Spacing.PadLR
			textWidth := app.inputRect.W - 2*pad
			original := strings.Repeat("a", textWidth) + " x"
			app.Input.SetInput(original)
			app.Input.SetCursorFlat(textWidth)
			app.paintShellFrame()

			// The user's exact path: App.Update -> dispatchChatKey ->
			// input.Model.Update -> TextArea.Update -> EnsureCursorVisible.
			_, _ = app.Update(tea.KeyPressMsg{Code: ' ', Text: " "})
			app.paintShellFrame()
			if app.Input.Value() != strings.Repeat("a", textWidth)+"  x" || app.Input.CursorFlat() != textWidth+1 {
				t.Fatalf("typing changed the draft/cursor: %q at %d", app.Input.Value(), app.Input.CursorFlat())
			}
			cursor := app.mainSurf.Buffer().Get(app.inputRect.X+pad, app.inputRect.Y+2)
			if cursor.Char != 'x' || !cursor.Attr.Has(cell.AttrReverse) {
				t.Fatalf("wrapped gap lost its visible reverse-block cursor: %+v", cursor)
			}
			_, _ = app.Update(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl})
			app.paintShellFrame()
			if app.Input.Value() != original || app.Input.CursorFlat() != textWidth {
				t.Fatalf("typing fix broke composer undo: %q at %d", app.Input.Value(), app.Input.CursorFlat())
			}
		})
	}
}
