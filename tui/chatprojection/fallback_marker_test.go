package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestBlankNativeVsANSIFallbackMarker(t *testing.T) {
	blank := buildPlainBodyLine("")
	if blank.PreferANSI() {
		t.Fatal("blank native spacer must not PreferANSI")
	}
	if blank.IsZero() {
		t.Fatal("blank native spacer must not be IsZero (empty-text span)")
	}

	fb := ansiFallbackLine("**rich**")
	if !fb.PreferANSI() {
		t.Fatal("ansiFallbackLine must PreferANSI")
	}
	if fb.IsZero() {
		t.Fatal("ANSI fallback sentinel must not look like an absent header (IsZero)")
	}

	th := theme.DefaultDark()
	ctx := renderContext{
		th:           th,
		contentWidth: 40,
		innerW:       60,
		width:        80,
		height:       40,
		modelName:    "test",
		username:     "user",
	}

	// Plain assistant body with an embedded blank line stays native (mdRenderer nil).
	plain := renderAssistantMessage(types.ChatMessage{
		Role:    types.RoleAssistant,
		Content: "alpha\n\nomega",
		Time:    time.Now(),
	}, ctx, true)
	foundBlank := false
	for _, pl := range plain.native {
		if pl.PreferANSI() {
			t.Fatalf("plain body unexpectedly PreferANSI: %#v", pl)
		}
		if pl.PlainText() == "" && !pl.PreferANSI() {
			foundBlank = true
		}
	}
	if !foundBlank {
		t.Fatalf("expected native blank among %#v / lines %#v", plain.native, plain.lines)
	}

	// Markdown/rich rows must route to ANSI fallback.
	md := rendering.MakeMdRenderer(80)
	if md == nil {
		t.Fatal("MakeMdRenderer returned nil")
	}
	ctx.mdRenderer = md
	rich := renderAssistantMessage(types.ChatMessage{
		Role:    types.RoleAssistant,
		Content: "# Heading\n\nparagraph",
		Time:    time.Now(),
	}, ctx, true)
	foundFallback := false
	for _, pl := range rich.native {
		if pl.PreferANSI() {
			foundFallback = true
			break
		}
	}
	if !foundFallback {
		t.Fatalf("expected PreferANSI body rows, got %#v lines=%#v", rich.native, rich.lines)
	}

	// Paint proof: blank native writes cells without needing the ANSI string path.
	tokens := ui.NewTokenSet(th)
	buf := surface.NewCellBuffer(20, 1)
	PaintLineAt(buf, 0, 0, 20, blank, tokens, 0, 0, false)
	for x := 0; x < 20; x++ {
		c := buf.Get(x, 0)
		if strings.ContainsRune(string(c.Char), '\x1b') {
			t.Fatalf("blank native painted escape at col %d", x)
		}
	}
}
