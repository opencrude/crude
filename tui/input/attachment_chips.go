package input

import (
	"fmt"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
)

type ReferenceChip struct {
	ID, Label    string
	Body, Remove ui.Rect
	Next         int
}

// ReferenceChips is the single geometry source for paint, clicks and menus.
// Close has a distinct three-cell target and never shares an opening hit area.
func (m Model) ReferenceChips(width int) []ReferenceChip {
	refs := m.activePromptRefs()
	if len(refs) == 0 {
		return nil
	}
	x, end := m.theme.Spacing.PadLR, width-m.theme.Spacing.PadLR
	start := min(m.attachmentOffset, len(refs)-1)
	chips := make([]ReferenceChip, 0, min(len(refs), 8))
	for i := start; i < len(refs); i++ {
		ref := refs[i]
		label := strings.Join(strings.Fields(ref.Title), " ")
		if label == "" {
			label = ref.DisplayName()
		}
		reserve := 0
		if i+1 < len(refs) || start > 0 {
			reserve = 7
		}
		available := end - x - reserve
		bodyW := min(34, rendering.VisibleWidth(label)+2)
		if len(chips) == 0 {
			bodyW = min(bodyW, available-3)
		}
		if bodyW < 3 || bodyW+3 > available {
			break
		}
		chips = append(chips, ReferenceChip{ID: ref.ID, Label: label,
			Body: ui.Rect{X: x, Y: 1, W: bodyW, H: 1}, Remove: ui.Rect{X: x + bodyW, Y: 1, W: 3, H: 1}})
		x += bodyW + 4
	}
	if next := start + len(chips); next < len(refs) || start > 0 {
		label := fmt.Sprintf("+%d", len(refs)-next)
		if next >= len(refs) {
			next, label = 0, "First"
		}
		chips = append(chips, ReferenceChip{Label: label, Body: ui.Rect{X: max(x, end-6), Y: 1, W: max(0, end-max(x, end-6)), H: 1}, Next: next})
	}
	return chips
}

func (m Model) paintRefChips(buf *surface.CellBuffer, ox, oy, width int) {
	for _, chip := range m.ReferenceChips(width) {
		bg := m.tokens.Color(ui.ColorSelection)
		text := rendering.TruncateVisual(chip.Label, max(0, chip.Body.W-2), "…")
		if chip.Body.W < 2 {
			continue
		}
		text = " " + text + strings.Repeat(" ", max(0, chip.Body.W-2-rendering.VisibleWidth(text))) + " "
		buf.WriteString(ox+chip.Body.X, oy+chip.Body.Y, text, m.tokens.Color(ui.ColorAccent2), bg, cell.AttrUnderline)
		if chip.ID != "" {
			buf.WriteString(ox+chip.Remove.X, oy+chip.Remove.Y, " × ", m.tokens.Color(ui.ColorError), m.tokens.Color(ui.ColorHover), 0)
		}
	}
}
