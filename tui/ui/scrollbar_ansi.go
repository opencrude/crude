package ui

import (
	"charm.land/lipgloss/v2"

	"crude/tui/theme"
)

// ScrollbarCellANSI paints one track cell for a vertical scrollbar using
// ScrollModel.ThumbBounds. This is the single ANSI scrollbar glyph path for
// legacy View() consumers (launcher, Find in Files, etc.).
//
// row is the 0-based row within the visible track (0..viewport-1).
func ScrollbarCellANSI(th *theme.Theme, scroll *ScrollModel, row int) string {
	if th == nil {
		th = theme.DefaultDark()
	}
	if scroll == nil {
		return " "
	}
	trackH := scroll.Viewport()
	if trackH <= 0 || scroll.Content() <= trackH {
		return " "
	}
	top, thH := scroll.ThumbBounds(trackH)
	if row >= top && row < top+thH {
		return lipgloss.NewStyle().Foreground(th.Colors.Accent).Render("█")
	}
	return lipgloss.NewStyle().Foreground(th.Colors.Border).Render("░")
}

// SyncScrollModel configures a ScrollModel for a 1-line-per-item list viewport.
func SyncScrollModel(s *ScrollModel, content, viewport, offset int) *ScrollModel {
	if s == nil {
		s = NewScrollModel()
	}
	s.SetContent(content)
	s.SetViewport(viewport)
	s.ScrollTo(offset)
	return s
}
