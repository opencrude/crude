//go:build cgo

package viewmodel

import (
	"bytes"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"
	uv "github.com/charmbracelet/ultraviolet"

	"crude/internal/terminalvt"
)

// Check the actual escape stream, not the renderer's idea of the terminal.
// A duplicate in the document and an old reverse-video cell are different bugs.
func TestEditorAndFindPresentEachWideCharacterOnce(t *testing.T) {
	for _, target := range []string{"editor", "find field"} {
		t.Run(target, func(t *testing.T) {
			const width, height = 110, 28
			app, _ := newInputContractEditorApp(t, "notes.txt")
			app.Tree.Visible = false
			app.Update(tea.WindowSizeMsg{Width: width, Height: height})
			ed := app.EditorState.Editor
			ed.SetCursorPosition(0, 0)
			original := ed.Value()
			app.View()
			if target == "find field" {
				app.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
				if app.FindReplace == nil {
					t.Fatal("Ctrl+F did not open the find field")
				}
			}

			terminal, err := terminalvt.New(terminalvt.Options{Cols: width, Rows: height})
			if err != nil {
				t.Fatal(err)
			}
			defer terminal.Close()
			var wire bytes.Buffer
			display := uv.NewTerminalRenderer(&wire, []string{"TERM="})
			display.SetColorProfile(colorprofile.TrueColor)
			display.SetRelativeCursor(false)
			display.SetFullscreen(true)
			display.SetTabStops(-1)
			screen := uv.NewScreenBuffer(width, height)
			present := func(want string) {
				t.Helper()
				app.presentImmediateFrame()
				view := app.View()
				if view.Cursor != nil {
					t.Fatal("shell enabled a second, hardware caret")
				}
				grid := uv.NewScreenBuffer(width, height)
				view.Surface.Draw(grid, grid.Bounds())
				screen.Replace(grid.Buffer)
				display.Render(screen.RenderBuffer)
				if err := display.Flush(); err != nil {
					t.Fatal(err)
				}
				terminal.Feed(wire.Bytes())
				wire.Reset()
				if want == "" {
					return
				}
				rows, err := terminal.Rows(terminalvt.RowRequest{Start: -1})
				if err != nil {
					t.Fatal(err)
				}
				for y, cells := range rows.Cells {
					var intended strings.Builder
					for x := range cells {
						intended.WriteString(grid.CellAt(x, y).Content)
					}
					if !strings.Contains(intended.String(), want) {
						continue
					}
					for x, actual := range cells {
						expected := grid.CellAt(x, y)
						reverse := expected.Style.Attrs&uv.AttrReverse != 0
						if strings.TrimSpace(actual.Text) != strings.TrimSpace(expected.Content) || actual.Style.Inverse != reverse {
							t.Fatalf("%q at (%d,%d): terminal %q reverse=%v; control %q reverse=%v",
								want, x, y, actual.Text, actual.Style.Inverse, expected.Content, reverse)
						}
					}
					return
				}
				t.Fatalf("the control did not paint %q", want)
			}
			present("")
			var want string
			for _, r := range "test😂😂a" {
				key := tea.KeyPressMsg{Code: r, Text: string(r)}
				app.Update(key)
				app.Update(tea.KeyReleaseMsg(key))
				want += string(r)
				if target == "editor" {
					if ed.Value() != want+original {
						t.Fatalf("editor contains %q, want %q", ed.Value(), want+original)
					}
				} else if app.FindReplace.Query() != want || ed.Value() != original {
					t.Fatalf("find field contains %q, document contains %q", app.FindReplace.Query(), ed.Value())
				}
				present(want)
			}
		})
	}
}
