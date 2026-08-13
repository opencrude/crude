// Package panelchrome provides the shared chrome for the sidebar panels
// mounted beside the activity rail (Files, Agents, Terminals, Git).
//
// Every panel paints the same title row, keeps the same body inset, and is
// separated from its neighbours by the same vertical rule, so switching
// activities never shifts the content grid under the pointer.
package panelchrome

import (
	"strings"

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

	// TitleRows is the height of the panel title row.
	TitleRows = 1

	// SearchRows is the height of the always-visible filter row.
	SearchRows = 1

	// HeaderRows is the total chrome above a panel's scrollable body.
	HeaderRows = TitleRows + SearchRows
)

// Title describes one panel title row.
type Title struct {
	// Icon is the panel glyph, painted in the accent color.
	Icon string
	// Label is the panel name. It is painted upper-case.
	Label string
	// Badge is an optional right-aligned count or status word.
	Badge string
}

// PaintTitle draws one panel title row inside r:
//
//	ICON  LABEL ················· badge
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

	badge := ""
	badgeW := 0
	if t.Badge != "" {
		badge = t.Badge
		badgeW = rendering.VisibleWidth(badge) + 1
		if badgeW >= end-x {
			badge, badgeW = "", 0
		}
	}

	label := rendering.TruncateVisual(strings.ToUpper(t.Label), max(0, end-x-badgeW), "…")
	x = buf.WriteString(x, y, label, tokens.Color(ui.ColorFg), bg, cell.AttrBold)

	if badgeW > 0 {
		buf.WriteString(end-badgeW+1, y, badge, tokens.Color(ui.ColorMuted), bg, 0)
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
