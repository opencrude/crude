package widgets

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/widget"
)

func TestYoloDialog_New(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)
	if d.cursor != 0 {
		t.Errorf("expected cursor=0, got %d", d.cursor)
	}
}

func TestYoloDialog_Update_LeftRight(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	d, _, _ = d.Update(keyMsg(tea.KeyRight))
	if d.cursor != 1 {
		t.Errorf("expected cursor=1 after Right, got %d", d.cursor)
	}

	d, _, _ = d.Update(keyMsg(tea.KeyRight))
	if d.cursor != 1 {
		t.Errorf("expected cursor=1 (clamped), got %d", d.cursor)
	}

	d, _, _ = d.Update(keyMsg(tea.KeyLeft))
	if d.cursor != 0 {
		t.Errorf("expected cursor=0 after Left, got %d", d.cursor)
	}

	d, _, _ = d.Update(keyMsg(tea.KeyLeft))
	if d.cursor != 0 {
		t.Errorf("expected cursor=0 (clamped), got %d", d.cursor)
	}
}

func TestYoloDialog_Update_Tab(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	d, _, _ = d.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if d.cursor != 1 {
		t.Errorf("expected cursor=1 after Tab, got %d", d.cursor)
	}

	d, _, _ = d.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if d.cursor != 0 {
		t.Errorf("expected cursor=0 after Shift+Tab, got %d", d.cursor)
	}
}

func TestYoloDialog_Update_EnterCursor0(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	_, confirmed, done := d.Update(keyMsg(tea.KeyEnter))
	if !done {
		t.Error("expected done=true")
	}
	if !confirmed {
		t.Error("expected confirmed=true with cursor=0")
	}
}

func TestYoloDialog_Update_EnterCursor1(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)
	d.cursor = 1

	_, confirmed, done := d.Update(keyMsg(tea.KeyEnter))
	if !done {
		t.Error("expected done=true")
	}
	if confirmed {
		t.Error("expected confirmed=false with cursor=1")
	}
}

func TestYoloDialog_Update_Esc(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	_, confirmed, done := d.Update(keyMsg(tea.KeyEsc))
	if !done {
		t.Error("expected done=true")
	}
	if confirmed {
		t.Error("expected confirmed=false on Esc")
	}
}

func TestYoloDialog_Update_ShortcutY(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	_, confirmed, done := d.Update(keyStr("y"))
	if !done {
		t.Error("expected done=true after y")
	}
	if !confirmed {
		t.Error("expected confirmed=true after y")
	}
}

func TestYoloDialog_Update_ShortcutN(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	_, confirmed, done := d.Update(keyStr("n"))
	if !done {
		t.Error("expected done=true after n")
	}
	if confirmed {
		t.Error("expected confirmed=false after n")
	}
}

func TestYoloDialog_Update_NonKeyMsg(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	_, _, done := d.Update(tea.WindowSizeMsg{})
	if done {
		t.Error("expected done=false for non-key msg")
	}
}

func TestYoloDialog_View_NotEmpty(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	v := d.View()
	if v == "" {
		t.Fatal("expected non-empty view")
	}
	if !strings.Contains(v, "YOLO") {
		t.Error("expected YOLO in view")
	}
}

func TestYoloDialog_Reset(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)
	d.cursor = 1
	d.Reset()
	if d.cursor != 0 {
		t.Errorf("expected cursor=0 after Reset, got %d", d.cursor)
	}
}

func TestYoloDialog_Dismiss(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)
	d.cursor = 1
	d.Dismiss()
	if d.cursor != 0 {
		t.Errorf("expected cursor=0 after Dismiss, got %d", d.cursor)
	}
}

func TestYoloDialog_Render(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)

	surf := surface.NewSurface("test", 80, 24, cell.BlankCell(rendering.ThemeColorToCell(th.Colors.Bg)))
	ctx := widget.NewRenderContext(th)
	d.Render(surf, ctx)

	buf := surf.Buffer()
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
}

func TestYoloDialog_Measure(t *testing.T) {
	th := testTheme()
	d := NewYoloDialog(th)
	w, h := d.Measure(100, 50)
	if w != 100 {
		t.Errorf("expected width=100, got %d", w)
	}
	if h != 50 {
		t.Errorf("expected height=50, got %d", h)
	}
}
