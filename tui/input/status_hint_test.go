package input

import (
	"strings"
	"testing"

	"crude/tui/theme"
)

func TestComposerHintIsStatusOnly(t *testing.T) {
	m := NewModel(theme.DefaultDark())
	if got := m.ContextHint(); !strings.Contains(got, "Enter send") {
		t.Fatalf("ContextHint()=%q", got)
	}
	// The guidance must NOT consume a shell row: ShellHeight (which the
	// viewmodel uses for the input slot height) must be identical whether
	// the hint is shown or dismissed. A hint row here would shrink the
	// chat area exactly like the legacy inline guidance row.
	shown := m.ShellHeight(80)
	m.DismissHint()
	dismissed := m.ShellHeight(80)
	if shown != dismissed {
		t.Fatalf("guidance must not reserve a shell row: shown=%d dismissed=%d", shown, dismissed)
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
