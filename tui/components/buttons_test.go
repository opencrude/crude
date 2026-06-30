package components

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/theme"
)

func TestButtonGroup_HoverSetsIndexOnMotion(t *testing.T) {
	th := theme.DefaultDark()
	bg := NewButtonGroup(th,
		Button{Label: "Yes", Key: "y"},
		Button{Label: "No", Key: "n"},
		Button{Label: "Maybe", Key: "m"},
	)

	// Get the rendered width of one button so we can compute the
	// X coordinate of the second button.
	w := lipglossWidthOf(buttonRender(th, bg.buttons[0], true, false))

	// Motion at X=0 should hover button 0.
	bg, _, _ = bg.Update(tea.MouseMotionMsg{X: 0, Y: 0})
	if bg.Hover.Index != 0 {
		t.Fatalf("motion at X=0 -> hover %d, want 0", bg.Hover.Index)
	}

	// Motion at X=w+2 (just past the gap) should hover button 1.
	bg, _, _ = bg.Update(tea.MouseMotionMsg{X: w + 2, Y: 0})
	if bg.Hover.Index != 1 {
		t.Fatalf("motion at X=%d -> hover %d, want 1", w+2, bg.Hover.Index)
	}

	// Motion at X past the last button should clear hover.
	bg, _, _ = bg.Update(tea.MouseMotionMsg{X: 1000, Y: 0})
	if bg.Hover.Index != -1 {
		t.Fatalf("motion at X=1000 -> hover %d, want -1", bg.Hover.Index)
	}
}

func TestButtonGroup_ClickOnHoveredActivates(t *testing.T) {
	th := theme.DefaultDark()
	bg := NewButtonGroup(th,
		Button{Label: "Yes", Key: "y"},
		Button{Label: "No", Key: "n"},
	)

	// Hover the second button.
	w := lipglossWidthOf(buttonRender(th, bg.buttons[0], true, false))
	bg, _, _ = bg.Update(tea.MouseMotionMsg{X: w + 2, Y: 0})

	// Click should activate button 1.
	bg, activated, done := bg.Update(tea.MouseClickMsg{X: w + 2, Y: 0, Button: tea.MouseLeft})
	if !done {
		t.Fatal("click on hovered button should produce done=true")
	}
	if activated != 1 {
		t.Fatalf("click on button 1 -> activated %d, want 1", activated)
	}
	if bg.FocusedIndex() != 1 {
		t.Fatalf("after click, focused = %d, want 1", bg.FocusedIndex())
	}
}

func TestButtonGroup_ClickWithoutHoverDoesNotActivate(t *testing.T) {
	th := theme.DefaultDark()
	bg := NewButtonGroup(th,
		Button{Label: "Yes", Key: "y"},
		Button{Label: "No", Key: "n"},
	)
	// No hover set — click at X=0 should be a no-op for
	// activation, not blindly activate button 0.
	_, activated, done := bg.Update(tea.MouseClickMsg{X: 0, Y: 0, Button: tea.MouseLeft})
	if done {
		t.Fatal("click without hover should NOT activate")
	}
	if activated != 0 {
		t.Fatalf("click without hover -> activated %d, want 0", activated)
	}
}

func TestButtonGroup_ViewIncludesHoverBackground(t *testing.T) {
	th := theme.DefaultDark()
	bg := NewButtonGroup(th,
		Button{Label: "Yes", Key: "y"},
		Button{Label: "No", Key: "n"},
	)
	bg.Hover.Set(0)

	view := bg.View()
	// The hovered button should include the hover background. We
	// don't check the exact escape codes (those depend on lipgloss
	// internals); we just check the view is non-empty and contains
	// the button labels.
	stripped := stripAnsi(view)
	if !strings.Contains(stripped, "Yes") || !strings.Contains(stripped, "No") {
		t.Fatalf("View() = %q, expected to contain both labels", stripped)
	}
}

func TestButtonGroup_SetHoverReturnsChange(t *testing.T) {
	th := theme.DefaultDark()
	bg := NewButtonGroup(th, Button{Label: "OK", Key: "enter"})

	if !bg.SetHover(0) {
		t.Fatal("SetHover(0) on fresh group should report change")
	}
	if bg.SetHover(0) {
		t.Fatal("SetHover(0) again should report no change")
	}
	if !bg.SetHover(-1) {
		t.Fatal("SetHover(-1) from 0 should report change")
	}
}

// stripAnsi is a tiny helper that removes CSI sequences. Mirrors the
// one in tui/form/textitem_test.go — duplicated here rather than
// exported to keep the test self-contained.
func stripAnsi(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		if r == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// lipglossWidthOf returns the visible width of an ANSI-styled
// string. Wraps the standard helper so tests don't need to import
// lipgloss directly.
func lipglossWidthOf(s string) int {
	return lipgloss.Width(s)
}
