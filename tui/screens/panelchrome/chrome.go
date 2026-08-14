// Package panelchrome provides the shared chrome for the sidebar panels
// mounted beside the activity rail (Files, Agents, Terminals, Git).
//
// Every panel paints the same header (icon immediately followed by the
// search/commit field), keeps the same body inset, and is separated from
// its neighbours by the same vertical rule, so switching activities never
// shifts the content grid under the pointer.
package panelchrome

import (
	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	// PadLeft is the inset every panel body row shares so labels never
	// touch the activity rail's border rule.
	PadLeft = 1

	// SearchRows is the height of the filter/commit field.
	SearchRows = 1

	// HeaderRows is the chrome above a panel's scrollable body: icon+search
	// on one row, then the body immediately below.
	HeaderRows = SearchRows
)

// BadgePart is one trailing header chip, painted like the Agents count on
// the search row. Empty FG uses the muted token.
type BadgePart struct {
	Text string
	FG   cell.Color
}

// Title describes the shared panel header row: icon, then the interactive
// field, then an optional right-aligned badge. The panel name is not painted;
// the activity-rail icon already identifies the view.
type Title struct {
	// Icon is the panel glyph, painted in the accent color.
	Icon string
	// Badge is an optional right-aligned count or status word.
	Badge string
	// BadgeParts, when non-empty, replace Badge so a row can carry more than
	// one colored chip (explorer git + diagnostics).
	BadgeParts []BadgePart
}

// BodyWidth is the row width left of the scrollbar track (gap column +
// thumb). Header fields must use this so they share a right edge with
// list/tree rows instead of running into the scrollbar column.
func BodyWidth(innerW int) int {
	return filetree.SidebarContentWidth(innerW)
}

// FieldRect is the search/commit field on the header row, immediately after
// the icon and before any right-aligned badge. contentW is BodyWidth: the
// same right edge as the panel's scrollable rows.
func (t Title) FieldRect(contentW int) ui.Rect {
	start := PadLeft + t.iconCols()
	endReserve := 0
	if b := t.badgeCols(); b > 0 {
		endReserve = b
	}
	return ui.Rect{
		X: start,
		Y: 0,
		W: max(1, contentW-start-endReserve),
		H: SearchRows,
	}
}

func (t Title) iconCols() int {
	if t.Icon == "" {
		return 0
	}
	return rendering.VisibleWidth(t.Icon) + 1
}

func (t Title) badgeCols() int {
	w := t.badgeWidth()
	if w == 0 {
		return 0
	}
	return w + 1
}

func (t Title) badgeWidth() int {
	if len(t.BadgeParts) > 0 {
		n := 0
		for _, p := range t.BadgeParts {
			if p.Text == "" {
				continue
			}
			if n > 0 {
				n++
			}
			n += rendering.VisibleWidth(p.Text)
		}
		return n
	}
	if t.Badge == "" {
		return 0
	}
	return rendering.VisibleWidth(t.Badge)
}

// PaintTitle draws the header icon (and optional badge) on row 0 of r.
// The search/commit field is a child laid out with FieldRect and paints
// the span between them.
//
// The row is filled with the panel background first so a previous activity's
// longer title cannot bleed through when the panel swaps.
func PaintTitle(buf *surface.CellBuffer, ox, oy int, r ui.Rect, tokens *ui.TokenSet, t Title) {
	if buf == nil || tokens == nil || r.W <= 0 || r.H <= 0 {
		return
	}
	bg := tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, ui.Rect{X: r.X, Y: r.Y, W: r.W, H: 1}, cell.BlankCell(bg))

	x := ox + r.X
	y := oy + r.Y
	end := x + r.W

	x += PadLeft
	if t.Icon != "" && x < end {
		x = buf.WriteString(x, y, t.Icon, tokens.Color(ui.ColorAccent), bg, 0)
		x = buf.WriteString(x, y, " ", tokens.Color(ui.ColorFg), bg, 0)
	}

	muted := tokens.Color(ui.ColorMuted)
	badgeW := t.badgeCols()
	if badgeW >= end-x {
		badgeW = 0
	}
	if badgeW > 0 {
		bx := end - badgeW + 1
		if len(t.BadgeParts) > 0 {
			written := false
			for _, p := range t.BadgeParts {
				if p.Text == "" {
					continue
				}
				if written {
					bx = buf.WriteString(bx, y, " ", muted, bg, 0)
				}
				fg := p.FG
				if fg.IsDefault() {
					fg = muted
				}
				bx = buf.WriteString(bx, y, p.Text, fg, bg, 0)
				written = true
			}
		} else {
			buf.WriteString(bx, y, t.Badge, muted, bg, 0)
		}
	}
	_ = x
}

// PaintVRule fills one column of height h with the panel border rule. This is
// the separator between the activity rail and the panel body; it uses the tree
// border color so the rail rule and the sidebar's split divider match.
func PaintVRule(buf *surface.CellBuffer, x, y, h int, tokens *ui.TokenSet) {
	if buf == nil || tokens == nil || h <= 0 {
		return
	}
	glyph := ui.FirstRune(tokens.Glyphs().BorderV, '│')
	fg := tokens.TreeBorderFg()
	bg := tokens.Color(ui.ColorBg)
	for i := 0; i < h; i++ {
		buf.Set(x, y+i, cell.NewCell(glyph, fg, bg, 0))
	}
}

// NerdFont resolves the theme's nerd-font preference: an explicit setting
// wins, otherwise the terminal is probed. Every panel resolves it the same
// way so rail icons, panel title icons, and tree icons come from one font set.
func NerdFont(th *theme.Theme) bool {
	if th != nil && th.Tree.Config.UseNerdFont != nil {
		return *th.Tree.Config.UseNerdFont
	}
	return filetree.DetectNerdFont()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
