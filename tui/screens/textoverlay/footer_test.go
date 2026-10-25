package textoverlay

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
)

func TestTextOverlayFramedFooterReservesRuleAndDecisions(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	screen.Open("ref", "Pasted Text1", strings.Repeat("body text\n", 20))
	frame := ui.NewWindowFrameWidget(th, "textoverlay/frame", ui.FrameWindow, screen.Title(), screen, nil, true, 40, 8)
	screen.SetFocusHandler(frame.Focus)
	screen.FocusInput()
	surf := surface.NewSurface("window", 40, 8, cell.TransparentBlank())
	frame.Render(surf, &wm.RenderContext{Theme: th, Focused: true})
	buf := surf.Buffer()
	for x := 1; x < 39; x++ {
		if got := buf.Get(x, 5).Char; got != '─' {
			t.Fatalf("footer rule missing at %d,5 (got %q):\n%s", x, got, bufferText(buf))
		}
	}
	rows := strings.Split(bufferText(buf), "\n")
	if !strings.HasSuffix(rows[6], "[ Save (Ctrl+S) ]  [ Cancel ]│") {
		t.Fatalf("decisions must follow the rule, primary first and dismissal rightmost:\n%s", bufferText(buf))
	}
	input, _ := frame.Host().ScreenRect(InputID)
	actions, _ := frame.Host().ScreenRect(ActionsID)
	if input.Y+input.H != 5 || actions != (ui.Rect{X: 1, Y: 5, W: 38, H: 2}) {
		t.Fatalf("body/footer geometry input=%+v actions=%+v", input, actions)
	}
	if got := frame.Host().HitTest(38, 6); got != ActionsID {
		t.Fatalf("painted Cancel hit=%q, want action bar", got)
	}
	_ = commandMessage[CancelMsg](t, frame.Dispatch(tea.MouseClickMsg{X: 38, Y: 6, Button: tea.MouseLeft}))
}

func TestTextOverlayShortFrameNeverOverlapsBodyAndFooter(t *testing.T) {
	th := theme.DefaultDark()
	for height := 1; height <= 6; height++ {
		screen := New(th)
		screen.Open("ref", "Pasted Text1", "body text")
		frame := ui.NewWindowFrameWidget(th, "textoverlay/frame", ui.FrameWindow, screen.Title(), screen, nil, true, 20, height)
		input, _ := frame.Host().ScreenRect(InputID)
		actions, _ := frame.Host().ScreenRect(ActionsID)
		if input.Y+input.H > actions.Y || input.H+actions.H > max(0, height-2) {
			t.Errorf("height %d: body/footer overlap or exceed frame: input=%+v actions=%+v", height, input, actions)
		}
	}
}

// Mount the same frame below an offset parent, move it, resize it, and paint
// into a larger sentinel-filled surface. This exercises Host's actual paint and
// pointer coordinate transforms rather than a screen-only geometry proxy.
func TestTextOverlayFooterNestedMovedResizedAndConstrained(t *testing.T) {
	for _, th := range []*theme.Theme{theme.DefaultDark(), theme.DefaultLight()} {
		for _, readOnly := range []bool{false, true} {
			screen := New(th)
			if readOnly {
				screen.OpenReadOnly("ref", "Preview", strings.Repeat("body text\n", 30))
			} else {
				screen.Open("ref", "Edit", strings.Repeat("body text\n", 30))
			}
			frame := ui.NewWindowFrame("frame", ui.FrameWindow, ui.NewTokenSet(th))
			frame.SetContent(screen)
			mount := &offsetFrame{Base: ui.NewBase("mount"), frame: frame}
			host := ui.NewHost(th, mount)
			host.Resize(110, 20)
			for _, width := range []int{0, 1, 2, 3, 7, 18, 32, 40, 96} {
				for height := 0; height <= 12; height++ {
					mount.rect = ui.Rect{X: 3 + height%2, Y: 2, W: width, H: height}
					host.MarkLayout()
					buf := surface.NewCellBuffer(110, 20)
					buf.Fill(cell.NewCell('#', cell.NoColor, cell.NoColor, 0))
					host.Paint(buf)
					input, _ := host.ScreenRect(InputID)
					actions, _ := host.ScreenRect(ActionsID)
					if input.Y+input.H > actions.Y || input.H+actions.H != max(0, height-2) {
						t.Fatalf("%dx%d: overlapping body/footer: %+v %+v", width, height, input, actions)
					}
					for y := 0; y < buf.Height(); y++ {
						for x := 0; x < buf.Width(); x++ {
							c := buf.Get(x, y)
							if !mount.rect.Contains(x, y) && c.Char != '#' {
								t.Fatalf("%dx%d: escaped moved frame at %d,%d: %q", width, height, x, y, c.Char)
							}
							if mount.rect.Contains(x, y) && (c.Transparent || c.BG.IsDefault()) {
								t.Fatalf("%dx%d: nonopaque frame at %d,%d", width, height, x, y)
							}
						}
					}
					for x := actions.X; x < actions.X+actions.W && actions.H > 0; x++ {
						if buf.Get(x, actions.Y).Char != '─' || host.HitTest(x, actions.Y) != ActionsID {
							t.Fatalf("%dx%d: missing full-width rule/hit at %d,%d", width, height, x, actions.Y)
						}
					}
					for _, action := range []string{"textoverlay.save", "textoverlay.cancel"} {
						button, exists := screen.actions.ButtonBounds(action)
						if !exists || button.Empty() {
							continue
						}
						x, y := actions.X+button.X, actions.Y+button.Y
						if !actions.Contains(x, y) || !actions.Contains(x+button.W-1, y) || host.HitTest(x, y) != ActionsID {
							t.Fatalf("%dx%d: escaped/missing decision hit: %+v", width, height, button)
						}
						result := host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
						if action == "textoverlay.save" {
							_ = commandMessage[SaveMsg](t, result)
						} else {
							_ = commandMessage[CancelMsg](t, result)
						}
						host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
					}
				}
			}
		}
	}
}

func TestTextOverlayFooterModeAndSavingTransitions(t *testing.T) {
	th := theme.DefaultDark()
	screen := New(th)
	frame := ui.NewWindowFrameWidget(th, "frame", ui.FrameWindow, "Pasted Text1", screen, nil, true, 40, 8)
	screen.SetFocusHandler(frame.Focus)
	bar := screen.actions
	paint := func() string {
		frame.Host().MarkLayout()
		surf := surface.NewSurface("window", 40, 8, cell.TransparentBlank())
		frame.Render(surf, &wm.RenderContext{Theme: th})
		return bufferText(surf.Buffer())
	}
	for _, content := range []string{"Loading attachment…", "Attachment could not be opened.\npermission denied", "Preview text"} {
		screen.OpenReadOnly("ref", "Preview", content)
		painted := paint()
		if !strings.Contains(painted, "[ Close ]") || strings.Contains(painted, "Save") || strings.Contains(painted, "Cancel") {
			t.Fatalf("preview/loading/error decisions:\n%s", painted)
		}
		screen.FocusInput()
		result := frame.Dispatch(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl})
		if result.Cmd != nil {
			t.Fatal("preview saved through stale Ctrl+S")
		}
	}
	screen.Open("ref", "Edit", "draft")
	paint()
	screen.FocusInput()
	frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyTab})
	if frame.Host().Focused() != ActionsID {
		t.Fatal("Tab did not reach decisions")
	}
	_ = commandMessage[SaveMsg](t, frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyEnter}))
	screen.SetBusy(true)
	paint()
	if screen.actions != bar || screen.actions.ActionEnabled("textoverlay.save") {
		t.Fatal("saving replaced the retained bar or left Save enabled")
	}
	for _, key := range []tea.KeyPressMsg{{Code: tea.KeyEnter}, {Code: 's', Mod: tea.ModCtrl}} {
		if result := frame.Dispatch(key); result.Cmd != nil {
			t.Fatal("busy Save emitted another command")
		}
	}
	screen.FocusInput()
	frame.Dispatch(tea.KeyPressMsg{Code: 'x', Text: "x"})
	frame.Dispatch(tea.PasteMsg{Content: "paste"})
	if screen.Value() != "draft" {
		t.Fatal("saving changed the draft")
	}
	screen.SetBusy(false) // Save error: retain draft and allow retry.
	if painted := paint(); !strings.Contains(painted, "[ Save (Ctrl+S) ]  [ Cancel ]") || screen.Value() != "draft" {
		t.Fatalf("save failure did not retain editable draft/footer:\n%s", painted)
	}
	screen.FocusInput()
	_ = commandMessage[SaveMsg](t, frame.Dispatch(tea.KeyPressMsg{Code: 's', Mod: tea.ModCtrl}))
	frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyTab})
	frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyRight})
	_ = commandMessage[CancelMsg](t, frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyEnter}))
	frame.Dispatch(tea.KeyPressMsg{Code: tea.KeyUp})
	if frame.Host().Focused() != InputID {
		t.Fatal("Up did not return to editor")
	}
}

type offsetFrame struct {
	ui.Base
	frame *ui.WindowFrame
	rect  ui.Rect
}

func (m *offsetFrame) Children() []ui.Component { return []ui.Component{m.frame} }
func (m *offsetFrame) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (m *offsetFrame) Layout(ctx ui.LayoutContext, r ui.Rect) {
	m.SetBounds(r)
	m.frame.Layout(ctx, m.rect)
}
func (m *offsetFrame) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}
func (m *offsetFrame) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult {
	return ui.EventResult{}
}
