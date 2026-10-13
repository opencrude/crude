package chatprojection

import (
	"strings"
	"testing"
	"time"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestBlankNativeSpacerIsPaintable(t *testing.T) {
	blank := buildPlainBodyLine("")
	if blank.IsZero() {
		t.Fatal("blank native spacer must not be IsZero (empty-text span)")
	}

	th := theme.DefaultDark()
	ctx := renderContext{
		th:           th,
		contentWidth: 40,
		innerW:       60,
		width:        80,
		height:       40,
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
		if pl.PlainText() == "" {
			foundBlank = true
		}
	}
	if !foundBlank {
		t.Fatalf("expected native blank among %#v / lines %#v", plain.native, plain.lines)
	}

	// Markdown/rich rows are now bridged to native PaintLines (no ANSI fallback).
	// The bridge reuses the SGR parser and carries explicit colour overrides for
	// Chroma syntax colours; known theme colours map back to roles.
	md := rendering.MakeMdRenderer(80, th)
	if md == nil {
		t.Fatal("MakeMdRenderer returned nil")
	}
	ctx.mdRenderer = md
	ctx.tokens = ui.NewTokenSet(th)
	rich := renderAssistantMessage(types.ChatMessage{
		Role:    types.RoleAssistant,
		Content: "# Heading\n\nparagraph",
		Time:    time.Now(),
	}, ctx, true)
	// Theme-bound Markdown may map every colour back to a semantic role; rich
	// emphasis must still survive the native bridge without ANSI fallback.
	foundStyle := false
	for _, pl := range rich.native {
		for _, s := range pl.Spans {
			if s.FGSet || s.Attr != 0 || s.Role != ui.ColorFg {
				foundStyle = true
				break
			}
		}
		if foundStyle {
			break
		}
	}
	if !foundStyle {
		t.Fatalf("expected styled native Markdown spans, got %#v", rich.native)
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
