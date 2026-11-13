package cell

import (
	"testing"
	"unsafe"
)

func TestHyperlinkRejectsTerminalControlsAndExecutableTargets(t *testing.T) {
	for _, target := range []string{"javascript:alert(1)", "file:///etc/passwd", "data:text/html,test", "https://", "https://example.test/\x1b]52;c;bad\a", "https://example.test/\u009c", "https://example.test/%zz"} {
		if NewLink(target, "") != nil {
			t.Fatalf("unsafe destination accepted: %q", target)
		}
	}
	for _, target := range []string{"https://example.test/a?x=1#anchor", "http://example.test/a", "mailto:reader@example.test"} {
		got := NewLink(target, "id=example")
		if got == nil || got.Target() != target || got.Params() != "id=example" {
			t.Fatalf("safe destination changed: %q", target)
		}
	}
	if NewLink("https://example.test/", "id=x\a") != nil {
		t.Fatal("terminal control in hyperlink parameters accepted")
	}
}

func TestHyperlinkAddsAtMostOneSharedPointerPerCell(t *testing.T) {
	// Links are sparse shared objects. Every unlinked screen cell must retain
	// a pointer-sized overhead, not add a URL-sized allocation per cell.
	type previousCell struct {
		Char        rune
		Content     string
		Width       uint8
		FG, BG      Color
		Attr        Attrs
		Transparent bool
	}
	if got, want := unsafe.Sizeof(Cell{}), unsafe.Sizeof(previousCell{}); got > want+unsafe.Sizeof((*Link)(nil)) {
		t.Fatalf("hyperlink exceeds the pointer-sized cell budget: old=%d new=%d", want, got)
	}
}
