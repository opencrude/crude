package settings

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// Pasting a hand-tuned system prompt into Settings -> Agents -> Instructions
// must not block the shell: the field inserts the whole payload, unlike the
// composer which promotes large pastes into an off-UI attachment.
func TestAgentInstructionsLargePasteStaysResponsive(t *testing.T) {
	const lines = 20000
	line := strings.Repeat("word ", 10)
	content := strings.Repeat(line+"\n", lines-1) + line

	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	host := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(130, 36)
	host.Layout()
	c := s.agents
	c.begin("role/default", false)
	host.MarkLayout()
	host.Layout()
	host.Focus(c.tabs.ID())
	host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
	if !c.form.FocusField("agent-instructions") {
		t.Fatal("instructions field is not editable")
	}
	host.Focus(AgentSettingsFormID)

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	host.Handle(tea.PasteMsg{Content: content})
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	allocated := after.TotalAlloc - before.TotalAlloc
	t.Logf("1 MiB instructions paste: %s, %d bytes allocated (%.1fx payload)",
		elapsed, allocated, float64(allocated)/float64(len(content)))

	// A rune-by-rune insertion took seconds here and allocated quadratically;
	// the linear splice is independent of machine load.
	if allocated > 120*uint64(len(content)) {
		t.Fatalf("instructions paste allocated %d bytes (%.1fx payload); insertion must be linear",
			allocated, float64(allocated)/float64(len(content)))
	}
	if elapsed > 3*time.Second {
		t.Fatalf("1 MiB instructions paste took %s; the shell must stay responsive", elapsed)
	}
	if got := c.draft.Field("agent-instructions").Value.String(); got != content {
		t.Fatalf("instructions paste lost content: got %d bytes, want %d", len(got), len(content))
	}

	// The next frame paints the pasted text without re-deriving the whole
	// document, so the editor is usable immediately after the paste.
	buf := surface.NewCellBuffer(130, 36)
	start = time.Now()
	host.MarkLayout()
	host.Layout()
	host.Paint(buf)
	if painted := time.Since(start); painted > time.Second {
		t.Fatalf("painting the pasted instructions took %s", painted)
	}
}
