package widgets

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/widget"
)

func testTheme() *theme.Theme {
	return theme.DefaultDark()
}

func keyMsg(code rune) tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: code}
}

func keyStr(s string) tea.KeyPressMsg {
	return tea.KeyPressMsg{Text: s}
}

func TestConfirmDialog_New(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Dismiss()
	if d.focused != 0 {
		t.Errorf("expected focused=0 after Dismiss, got %d", d.focused)
	}
}

func TestConfirmDialog_Update_LeftRight(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")

	d, _, done := d.Update(keyMsg(tea.KeyRight))
	if done {
		t.Error("expected done=false after Right")
	}
	if d.focused != 1 {
		t.Errorf("expected focused=1, got %d", d.focused)
	}

	d, _, done = d.Update(keyMsg(tea.KeyRight))
	if d.focused != 2 {
		t.Errorf("expected focused=2, got %d", d.focused)
	}

	d, _, done = d.Update(keyMsg(tea.KeyRight))
	if d.focused != 2 {
		t.Errorf("expected focused=2 (clamped), got %d", d.focused)
	}

	d, _, done = d.Update(keyMsg(tea.KeyLeft))
	if d.focused != 1 {
		t.Errorf("expected focused=1, got %d", d.focused)
	}

	d, _, _ = d.Update(keyMsg(tea.KeyLeft))
	d, _, _ = d.Update(keyMsg(tea.KeyLeft))
	if d.focused != 0 {
		t.Errorf("expected focused=0, got %d", d.focused)
	}

	d, _, _ = d.Update(keyMsg(tea.KeyLeft))
	if d.focused != 0 {
		t.Errorf("expected focused=0 (clamped), got %d", d.focused)
	}
}

func TestConfirmDialog_Update_Tab(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")

	d, _, _ = d.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if d.focused != 1 {
		t.Errorf("expected focused=1 after Tab, got %d", d.focused)
	}

	d, _, _ = d.Update(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	if d.focused != 0 {
		t.Errorf("expected focused=0 after Shift+Tab, got %d", d.focused)
	}
}

func TestConfirmDialog_Update_EnterReturnsAllow(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")

	_, result, done := d.Update(keyMsg(tea.KeyEnter))
	if !done {
		t.Error("expected done=true after Enter")
	}
	if result.Choice != types.ConfirmAllow {
		t.Errorf("expected ConfirmAllow, got %q", result.Choice)
	}
	if result.Operation != "op" {
		t.Errorf("expected op, got %q", result.Operation)
	}
	if result.Tool != "tool" {
		t.Errorf("expected tool, got %q", result.Tool)
	}
	if result.Target != "target" {
		t.Errorf("expected target, got %q", result.Target)
	}
}

func TestConfirmDialog_Update_EnterReturnsAlways(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")
	d.focused = 1

	_, result, done := d.Update(keyMsg(tea.KeyEnter))
	if !done {
		t.Error("expected done=true after Enter")
	}
	if result.Choice != types.ConfirmAlways {
		t.Errorf("expected ConfirmAlways, got %q", result.Choice)
	}
}

func TestConfirmDialog_Update_EnterReturnsDeny(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")
	d.focused = 2

	_, result, done := d.Update(keyMsg(tea.KeyEnter))
	if !done {
		t.Error("expected done=true after Enter")
	}
	if result.Choice != types.ConfirmDeny {
		t.Errorf("expected ConfirmDeny, got %q", result.Choice)
	}
}

func TestConfirmDialog_Update_EscReturnsDeny(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")

	_, result, done := d.Update(keyMsg(tea.KeyEsc))
	if !done {
		t.Error("expected done=true after Esc")
	}
	if result.Choice != types.ConfirmDeny {
		t.Errorf("expected ConfirmDeny, got %q", result.Choice)
	}
}

func TestConfirmDialog_Update_ShortcutY(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")

	_, result, done := d.Update(keyStr("y"))
	if !done {
		t.Error("expected done=true after y")
	}
	if result.Choice != types.ConfirmAllow {
		t.Errorf("expected ConfirmAllow, got %q", result.Choice)
	}
}

func TestConfirmDialog_Update_ShortcutA(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")

	_, result, done := d.Update(keyStr("a"))
	if !done {
		t.Error("expected done=true after a")
	}
	if result.Choice != types.ConfirmAlways {
		t.Errorf("expected ConfirmAlways, got %q", result.Choice)
	}
}

func TestConfirmDialog_Update_ShortcutN(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")

	_, result, done := d.Update(keyStr("n"))
	if !done {
		t.Error("expected done=true after n")
	}
	if result.Choice != types.ConfirmDeny {
		t.Errorf("expected ConfirmDeny, got %q", result.Choice)
	}
}

func TestConfirmDialog_Update_NonKeyMsg(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)

	_, _, done := d.Update(tea.WindowSizeMsg{})
	if done {
		t.Error("expected done=false for non-key msg")
	}
}

func TestConfirmDialog_View_NotEmpty(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("proceed?", "read", "cat", "/tmp/x")

	v := d.View(0)
	if v == "" {
		t.Fatal("expected non-empty view")
	}
	if !strings.Contains(v, "proceed") {
		t.Error("expected question in view")
	}
}

func TestConfirmDialog_View_RendersRiskLabels(t *testing.T) {
	th := testTheme()
	risks := []struct {
		key   string
		label string
	}{
		{"blocked", "BLOCKED"},
		{"destructive", "DANGEROUS"},
		{"network", "NETWORK"},
		{"project-write", "WRITE"},
		{"unknown", "UNKNOWN"},
	}

	for _, r := range risks {
		d := NewConfirmDialog(th)
		d.ResetWithDetails("?", "op", "tool", "target", r.key, "reason", "suggestion")
		v := d.View(0)
		if !strings.Contains(v, r.label) {
			t.Errorf("expected risk label %q for key %q in view", r.label, r.key)
		}
	}
}

func TestConfirmDialog_Reset(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("q?", "op", "tool", "tgt")

	if d.question != "q?" {
		t.Errorf("expected question q?, got %q", d.question)
	}
	if d.operation != "op" {
		t.Errorf("expected operation op, got %q", d.operation)
	}
	if d.tool != "tool" {
		t.Errorf("expected tool tool, got %q", d.tool)
	}
	if d.target != "tgt" {
		t.Errorf("expected target tgt, got %q", d.target)
	}
	if d.risk != "" {
		t.Errorf("expected empty risk after Reset, got %q", d.risk)
	}
	if d.focused != 0 {
		t.Errorf("expected focused=0 after Reset, got %d", d.focused)
	}
}

func TestConfirmDialog_ResetWithDetails(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.ResetWithDetails("q?", "op", "tool", "tgt", "blocked", "bad", "try this")

	if d.risk != "blocked" {
		t.Errorf("expected risk blocked, got %q", d.risk)
	}
	if d.reason != "bad" {
		t.Errorf("expected reason bad, got %q", d.reason)
	}
	if d.suggestion != "try this" {
		t.Errorf("expected suggestion try this, got %q", d.suggestion)
	}
}

func TestConfirmDialog_Dismiss(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.ResetWithDetails("?", "op", "tool", "tgt", "blocked", "reason", "suggestion")
	d.focused = 2
	d.Dismiss()

	if d.risk != "" {
		t.Error("expected empty risk after Dismiss")
	}
	if d.reason != "" {
		t.Error("expected empty reason after Dismiss")
	}
	if d.suggestion != "" {
		t.Error("expected empty suggestion after Dismiss")
	}
	if d.focused != 0 {
		t.Errorf("expected focused=0 after Dismiss, got %d", d.focused)
	}
}

func TestConfirmDialog_SetWidth(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.SetWidth(80)
	if d.width != 80 {
		t.Errorf("expected width=80, got %d", d.width)
	}
}

func TestConfirmDialog_Render(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	d.Reset("test?", "op", "tool", "target")

	surf := surface.NewSurface("test", 80, 24, cell.BlankCell(rendering.ThemeColorToCell(th.Colors.Bg)))
	ctx := widget.NewRenderContext(th)
	d.Render(surf, ctx)

	buf := surf.Buffer()
	if buf == nil {
		t.Fatal("expected non-nil buffer")
	}
}

func TestConfirmDialog_Measure(t *testing.T) {
	th := testTheme()
	d := NewConfirmDialog(th)
	w, h := d.Measure(120, 40)
	if w > 120 {
		t.Errorf("expected width <= 120, got %d", w)
	}
	if h > 40 {
		t.Errorf("expected height <= 40, got %d", h)
	}
	if w != 80 {
		t.Errorf("expected preferred width 80, got %d", w)
	}
	if h != 16 {
		t.Errorf("expected preferred height 16, got %d", h)
	}
}
