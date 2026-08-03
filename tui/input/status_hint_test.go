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
	// The guidance lives in the status bar, so the input's mouse geometry is
	// independent of it: MouseToInputPos maps the input's own first text row
	// (textAreaY, one separator row below the shell top) to rune index 0 and
	// treats the row above the text area as outside the input.
	if pos, ok := m.MouseToInputPos(0, m.textAreaY()); !ok || pos != 0 {
		t.Fatalf("first text row must map to rune 0: pos=%d ok=%v", pos, ok)
	}
	if _, ok := m.MouseToInputPos(0, m.textAreaY()-1); ok {
		t.Fatalf("row above the text area must be outside: ok=%v", ok)
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
