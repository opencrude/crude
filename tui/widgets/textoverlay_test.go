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

func TestTextOverlay_New(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	if o.IsOpen() {
		t.Error("expected closed initially")
	}
	if o.RefID() != "" {
		t.Errorf("expected empty refID, got %q", o.RefID())
	}
}

func TestTextOverlay_Open(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("ref-1", "Edit file", "hello world")

	if !o.IsOpen() {
		t.Error("expected open after Open()")
	}
	if o.RefID() != "ref-1" {
		t.Errorf("expected refID ref-1, got %q", o.RefID())
	}
	if o.Value() != "hello world" {
		t.Errorf("expected value 'hello world', got %q", o.Value())
	}
}

func TestTextOverlay_Value(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "title", "line1\nline2")

	v := o.Value()
	if v != "line1\nline2" {
		t.Errorf("expected 'line1\\nline2', got %q", v)
	}
}

func TestTextOverlay_IsOpen(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)

	if o.IsOpen() {
		t.Error("expected false initially")
	}

	o.Open("id", "title", "content")
	if !o.IsOpen() {
		t.Error("expected true after Open")
	}

	o.Close()
	if o.IsOpen() {
		t.Error("expected false after Close")
	}
}

func TestTextOverlay_Close(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "title", "content")
	o.Close()

	if o.RefID() != "" {
		t.Errorf("expected empty refID after Close, got %q", o.RefID())
	}
	if o.Value() != "" {
		t.Errorf("expected empty value after Close, got %q", o.Value())
	}
	if o.IsOpen() {
		t.Error("expected IsOpen=false after Close")
	}
}

func TestTextOverlay_Update_CtrlS_Save(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "title", "text")

	_, _, action := o.Update(tea.KeyPressMsg{Text: "ctrl+s"})
	if action != TextOverlaySave {
		t.Errorf("expected TextOverlaySave, got %v", action)
	}
}

func TestTextOverlay_Update_CtrlC_Cancel(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "title", "text")

	_, _, action := o.Update(tea.KeyPressMsg{Text: "ctrl+c"})
	if action != TextOverlayCancel {
		t.Errorf("expected TextOverlayCancel, got %v", action)
	}
}

func TestTextOverlay_Update_Esc_Cancel(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "title", "text")

	_, _, action := o.Update(keyMsg(tea.KeyEsc))
	if action != TextOverlayCancel {
		t.Errorf("expected TextOverlayCancel, got %v", action)
	}
}

func TestTextOverlay_Update_OtherKeysUpdateText(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "title", "")

	_, _, action := o.Update(keyStr("a"))
	if action != TextOverlayNone {
		t.Errorf("expected TextOverlayNone, got %v", action)
	}
	if o.Value() != "a" {
		t.Errorf("expected value 'a', got %q", o.Value())
	}
}

func TestTextOverlay_ViewInner_ReturnsContent(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "Title", "hello")

	lines := o.ViewInner(80, 10, 0)
	if len(lines) == 0 {
		t.Fatal("expected non-empty ViewInner result")
	}

	joined := strings.Join(lines, "\n")
	if !strings.Contains(rendering.StripAnsi(joined), "hello") {
		t.Logf("ViewInner output: %q", joined)
		t.Error("expected content 'hello' in ViewInner")
	}
}

func TestTextOverlay_ViewInner_SmallDimensions(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "T", "c")

	lines := o.ViewInner(5, 3, 0)
	if lines != nil {
		t.Log("ViewInner returned nil for small dims")
	}
}

func TestTextOverlay_Render(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	o.Open("id", "Title", "content")

	surf := surface.NewSurface("test", 80, 24, cell.BlankCell(rendering.ThemeColorToCell(th.Colors.Bg)))
	ctx := widget.NewRenderContext(th)
	o.Render(surf, ctx)

	buf := surf.Buffer()
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
}

func TestTextOverlay_Measure(t *testing.T) {
	th := testTheme()
	o := NewTextOverlay(th)
	w, h := o.Measure(100, 50)
	if w != 100 {
		t.Errorf("expected width=100, got %d", w)
	}
	if h != 50 {
		t.Errorf("expected height=50, got %d", h)
	}
}
