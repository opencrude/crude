package input

import (
	"strings"
	"testing"

	"crude/tui/theme"
)

func TestComposerHintIsStatusOnly(t *testing.T) {
	m := NewModel(theme.DefaultDark())
	if got := m.ContextHint(); !strings.Contains(got, "enter send") {
		t.Fatalf("ContextHint()=%q", got)
	}
	if got := m.View(80, 20, false, false, 0); strings.Contains(got, "enter send") {
		t.Fatalf("input view should not render status guidance: %q", got)
	}
	if got := m.FirstLineY(20, true, false, 0); got != 24 {
		t.Fatalf("status guidance must not shift mouse geometry: FirstLineY=%d", got)
	}
}

func TestComposerThinkingHintIsStatusOnly(t *testing.T) {
	m := NewModel(theme.DefaultDark())
	m.SetState(true, false)
	if got := m.ContextHint(); !strings.Contains(got, "send a note") {
		t.Fatalf("thinking ContextHint()=%q", got)
	}
	m.SetInput("hello")
	if got := m.ContextHint(); got != "" {
		t.Fatalf("typing should hide composer context, got %q", got)
	}
}
