package ui

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"

	"crude/tui/rendering"
	"crude/tui/theme"
)

// FilterPaintOptions configures ANSI paint for a single-line filter/query field.
// Used by legacy View() paths until full TextField surface paint is universal.
type FilterPaintOptions struct {
	Prompt      string // e.g. ">" or "/ "
	Placeholder string // shown when value empty
	Width       int    // total visible width budget for the painted line
	Focused     bool   // when true, show inverse/block cursor
	BG          color.Color
}

// RenderFilterLine paints a single-line filter/query from TextEdit into ANSI.
// Cursor is inverse on the grapheme under the cursor, or a block at EOL.
// Horizontal viewport follows the cursor for long values.
func RenderFilterLine(th *theme.Theme, ed *TextEdit, opt FilterPaintOptions) string {
	if th == nil {
		th = theme.DefaultDark()
	}
	if ed == nil {
		ed = NewTextEdit()
	}
	if opt.Width < 4 {
		opt.Width = 4
	}
	bg := opt.BG
	if bg == nil {
		bg = th.Colors.Bg
	}
	muted := lipgloss.NewStyle().Foreground(th.Colors.Muted).Background(bg)
	textStyle := lipgloss.NewStyle().Foreground(th.Colors.Fg).Background(bg)
	// Inverse cursor: fg on accent bg
	cursorStyle := lipgloss.NewStyle().Foreground(th.Colors.Bg).Background(th.Colors.Accent).Bold(true)
	blockStyle := lipgloss.NewStyle().Foreground(th.Colors.Accent).Background(bg).Bold(true)

	prompt := opt.Prompt
	if prompt == "" {
		prompt = "> "
	}
	// Ensure trailing space after short prompts like ">"
	if prompt == ">" {
		prompt = "> "
	}

	value := ed.Value()
	if value == "" {
		ph := opt.Placeholder
		line := muted.Render(prompt)
		if ph != "" {
			line += muted.Render(ph)
		}
		if opt.Focused {
			line += blockStyle.Render("█")
		}
		return line
	}

	runes := []rune(value)
	cursor := ed.Cursor()
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(runes) {
		cursor = len(runes)
	}

	promptW := rendering.VisibleWidth(prompt)
	available := opt.Width - promptW
	if available < 2 {
		available = 2
	}
	cursorW := 1
	if cursor < len(runes) {
		cursorW = max(1, rendering.VisibleWidth(string(runes[cursor])))
	}

	// Scroll start so cursor stays visible.
	start := 0
	for start < cursor && rendering.VisibleWidth(string(runes[start:cursor])) > available-cursorW {
		start++
	}

	var b strings.Builder
	b.WriteString(muted.Render(prompt))
	used := 0
	if start > 0 {
		b.WriteString(muted.Render("…"))
		used++
	}
	for i := start; i < len(runes); i++ {
		w := max(1, rendering.VisibleWidth(string(runes[i])))
		if used+w > available {
			break
		}
		ch := string(runes[i])
		if opt.Focused && i == cursor {
			b.WriteString(cursorStyle.Render(ch))
		} else {
			b.WriteString(textStyle.Render(ch))
		}
		used += w
	}
	if opt.Focused && cursor >= len(runes) && used < available {
		b.WriteString(blockStyle.Render("█"))
	}
	return b.String()
}
