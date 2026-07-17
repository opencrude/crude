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
