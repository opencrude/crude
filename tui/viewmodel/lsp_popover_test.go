package viewmodel

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/tui/ui"
)

func TestConnectedEditorPopoversAnchorResizeAndDismissOnOutsideClick(t *testing.T) {
	for _, tc := range []struct {
		name string
		open func(*testing.T) (*App, ui.OverlayID)
	}{
		{"rename", func(t *testing.T) (*App, ui.OverlayID) {
			f := newRenameConnectedFixture(t, renameConnectedRaw, false)
			app := f.shells[0].app
			renameConnectedKey(t, f.shells[0], tea.KeyPressMsg{Code: tea.KeyF2})
			return app, ui.OverlayTextPrompt
		}},
		{"code actions", func(t *testing.T) (*App, ui.OverlayID) {
			f, _ := newActionConnectedFixture(t, actionConnectedRaw)
			actionConnectedOpen(t, f)
			return f.shells[0].app, codeActionsOverlay
		}},
		{"references", func(t *testing.T) (*App, ui.OverlayID) {
			f := newNavigationConnectedFixture(t, navigationConnectedMultiple, "\t😀name\r\n")
			f.open(t, tea.KeyPressMsg{Code: ';', Mod: tea.ModAlt})
			return f.shells[0].app, ui.OverlayNavigation
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app, id := tc.open(t)
			ed := app.EditorState.Editor
			source, draft := ed.Value(), app.Input.Value()
			for _, size := range [][2]int{{160, 40}, {80, 24}} {
				app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
				app.View()
				window := app.overlayHost.Window(id)
				if window == nil {
					t.Fatal("editor command did not keep its popup after resize")
				}
				frame := window.Content.(interface{ Root() ui.Component }).Root().(*ui.WindowFrame)
				if !frame.CloseBounds().Empty() {
					t.Fatal("transient editor popup exposes a window close control")
				}
				rect, mounted := app.screenRectOf(app.editorScreen.ID())
				x, y := ed.CursorScreenPosition(rect.X, rect.Y)
				px, py := window.Position()
				w, h := window.Size()
				if !mounted || px > x || px+w <= x || (py != y+1 && py+h != y) {
					t.Fatalf("popup left its source: source=%d:%d popup=%d:%d %dx%d", x, y, px, py, w, h)
				}
			}
			rect, _ := app.screenRectOf(app.editorScreen.ID())
			x, y := ed.CursorScreenPosition(rect.X, rect.Y)
			for _, msg := range []tea.Msg{
				tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft},
				tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft},
			} {
				_, cmd := app.Update(msg)
				runAppCommand(t, app, cmd)
			}
			if app.overlayHost.IsOpen(id) || app.focusedPopup() != nil || app.focusTarget() != FocusEditor {
				t.Fatal("outside click did not dismiss the popup and return to the clicked editor")
			}
			if ed.Value() != source || app.Input.Value() != draft {
				t.Fatal("dismissing the popup changed source or the private draft")
			}
			if tc.name == "rename" && app.TextPrompt != nil || tc.name == "code actions" && app.codeActions != nil {
				t.Fatal("dismissal retained the cancelled operation")
			}
		})
	}
}

func TestConnectedRenamePopoverValidationAndPasteKeepTheSourceUnchanged(t *testing.T) {
	f := newRenameConnectedFixture(t, renameConnectedRaw, false)
	shell := f.shells[0]
	app := shell.app
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyF2})
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyEnter})
	if paint := ansi.Strip(app.View().RenderContent()); !strings.Contains(paint, "Name cannot be empty") {
		t.Fatalf("rename validation is not visible beside its field:\n%s", paint)
	}
	_, cmd := app.Update(tea.PasteMsg{Content: "another"})
	runAppCommand(t, app, cmd)
	if app.TextPrompt == nil || app.TextPrompt.ValueText() != "another" {
		t.Fatal("the anchored rename field did not receive paste")
	}
	renameConnectedKey(t, shell, tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.TextPrompt != nil || app.LSPPopup.EditPreview != nil {
		t.Fatal("Escape did not cancel rename before preparing changes")
	}
	f.assertBytes(t, renameConnectedMain, renameConnectedPeer)
}
