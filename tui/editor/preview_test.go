package editor

import (
	"strings"
	"testing"

	"crude/tui/rendering"
	"crude/tui/theme"
)

func TestPreviewUsesSourceLineNumbersAndChroma(t *testing.T) {
	ed := NewPreview(theme.DefaultDark(), "/tmp/main.go", "func main() {\n\treturn\n}", 40)
	ed.SetSelection(0, 0, 0, 4)
	lines := ed.View(60, 4, 0, 0)
	if len(lines) != 4 {
		t.Fatalf("preview rendered %d lines", len(lines))
	}
	plain := rendering.StripAnsi(lines[0])
	if !strings.Contains(plain, "40") || !strings.Contains(plain, "func main") {
		t.Fatalf("preview first line=%q", plain)
	}
	if lines[0] == plain {
		t.Fatal("preview should contain Chroma/selection ANSI styling")
	}
}

func TestViewWideGlyphsKeepScrollbarColumn(t *testing.T) {
	const width = 60
	ed := NewPreview(
		theme.DefaultDark(),
		"/tmp/width.md",
		"single: ☰\nwide: 😀\ntwo wide: 😀 / ⚡\ncjk: 中文\nplain",
		1,
	)

	for i, line := range ed.View(width, 4, 0, 0) {
		if got := rendering.VisibleWidth(line); got != width {
			t.Errorf("line %d rendered width = %d, want %d", i, got, width)
		}
		plain := rendering.StripAnsi(line)
		scrollbarAt := strings.LastIndexAny(plain, "░█")
		if scrollbarAt < 0 {
			t.Fatalf("line %d has no scrollbar: %q", i, plain)
		}
		if got := rendering.PlainWidth(plain[:scrollbarAt]); got != width-1 {
			t.Errorf("line %d scrollbar column = %d, want %d", i, got, width-1)
		}
	}
}
