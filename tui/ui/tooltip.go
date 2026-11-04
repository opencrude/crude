package ui

import (
	"strings"
	"time"

	"crude/tui/rendering"
)

// TooltipSource is implemented by a control that can explain the item under
// the pointer, such as the full path behind a shortened tab label. The control
// only answers what to show and where; the shell owns the one tooltip window,
// its delay and its dismissal (see docs/reference/control-usage.md).
type TooltipSource interface {
	// Tooltip returns the text for the hovered item and the item's
	// component-local rectangle. ok is false when nothing is hovered or the
	// item has nothing to explain.
	Tooltip() (text string, anchor Rect, ok bool)
}

// TooltipDelay is how long the pointer rests on an item before its tooltip
// opens. Moving to another item while a tooltip is open shows the new one
// at once.
const TooltipDelay = 500 * time.Millisecond

// TooltipChrome is the width that the popover frame adds to its text: two
// borders and two columns of padding on each side.
const TooltipChrome = 6

// TooltipFrameWidth is the popover width for text: its widest line plus the
// frame, limited to maxW. Longer lines wrap in the frame's text area.
func TooltipFrameWidth(text string, maxW int) int {
	w := 0
	for _, line := range strings.Split(text, "\n") {
		w = max(w, rendering.VisibleWidth(line))
	}
	return min(max(1, maxW), w+TooltipChrome)
}
