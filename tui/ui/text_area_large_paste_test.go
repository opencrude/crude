package ui

import (
	"runtime"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
)

func measurePaste(fn func()) (time.Duration, uint64) {
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	start := time.Now()
	fn()
	elapsed := time.Since(start)
	runtime.ReadMemStats(&after)
	return elapsed, after.TotalAlloc - before.TotalAlloc
}

// A paste into a text area used to insert one rune at a time, rescanning every
// preceding line and reallocating the target line per rune. A large system
// prompt therefore blocked Update for seconds to minutes while the composer,
// which promotes large pastes off the UI, stayed responsive.
func TestTextAreaLargePasteIsOneLinearEdit(t *testing.T) {
	const lines = 20000
	line := strings.Repeat("word ", 10)
	content := strings.Repeat(line+"\n", lines-1) + line

	ta := NewTextArea("paste", NewTokenSet(theme.DefaultDark()), true)
	ta.Layout(LayoutContext{}, Rect{W: 80, H: 20})

	elapsed, allocated := measurePaste(func() {
		ta.Handle(EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: content}))
	})
	t.Logf("1 MiB multi-line paste: %s, %d bytes allocated (%.1fx payload)",
		elapsed, allocated, float64(allocated)/float64(len(content)))

	// The old rune-by-rune path took seconds for this payload and allocated
	// quadratically (one fresh copy of the growing line per rune plus a full
	// buffer-slice copy per newline). The linear splice allocates a small
	// multiple of the payload, so this bound does not depend on machine load.
	if allocated > 40*uint64(len(content)) {
		t.Fatalf("1 MiB paste allocated %d bytes (%.1fx payload); insertion must be linear",
			allocated, float64(allocated)/float64(len(content)))
	}
	if elapsed > 2*time.Second {
		t.Fatalf("1 MiB paste took %s; pasting must not freeze Update", elapsed)
	}
	if got := ta.Value(); got != content {
		t.Fatalf("paste lost content: got %d bytes, want %d", len(got), len(content))
	}
	if ta.CurLine() != lines-1 || ta.CurCol() != len([]rune(line)) {
		t.Fatalf("cursor = (%d,%d), want (%d,%d)", ta.CurLine(), ta.CurCol(), lines-1, len([]rune(line)))
	}

	ta.Handle(EventContext{Focused: true}, KeyEvent(tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}))
	if got := ta.Value(); got != "" {
		t.Fatalf("one paste must be one undo step, value after undo = %q", got)
	}
}

// A single logical line produces one visual row per wrap segment. Cursor
// projection used to re-wrap the entire line for every one of those rows, which
// made long unbroken prompts quadratic outside the insertion path itself.
func TestTextAreaLongSingleLinePasteStaysResponsive(t *testing.T) {
	content := strings.Repeat("word ", 200000) // 1 MiB, one logical line

	ta := NewTextArea("paste", NewTokenSet(theme.DefaultDark()), true)
	ta.Layout(LayoutContext{}, Rect{W: 80, H: 20})

	elapsed, allocated := measurePaste(func() {
		ta.Handle(EventContext{Focused: true}, CustomEvent(tea.PasteMsg{Content: content}))
	})
	t.Logf("1 MiB single-row paste: %s, %d bytes allocated (%.1fx payload)",
		elapsed, allocated, float64(allocated)/float64(len(content)))

	// Per-segment start lookups re-parsed the 1 MiB line for every visual row,
	// so the old path needed minutes and allocated hundreds of times the
	// payload before the cursor could be placed.
	if allocated > 400*uint64(len(content)) {
		t.Fatalf("single-row paste allocated %d bytes (%.1fx payload); segment offsets must be linear",
			allocated, float64(allocated)/float64(len(content)))
	}
	if elapsed > 5*time.Second {
		t.Fatalf("1 MiB single-row paste took %s; segment offsets must be linear", elapsed)
	}
	if got := ta.CurCol(); got != len([]rune(content)) {
		t.Fatalf("cursor column = %d, want %d", got, len([]rune(content)))
	}
	if ta.CursorVisualLine() < 1000 {
		t.Fatal("fixture did not wrap the long line into many visual rows")
	}
}
