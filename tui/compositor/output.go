package compositor

import (
	"crude/tui/cell"
	"crude/tui/surface"
	"fmt"
	"strings"
)

// cellStyleState tracks the current terminal style state for optimization.
// By tracking what style the terminal is currently in, we can skip redundant
// SGR (Select Graphic Rendition) escape sequences when consecutive cells
// share the same style.
type cellStyleState struct {
	fg   cell.Color
	bg   cell.Color
	attr cell.Attrs
}

// GenerateANSI converts a cell buffer into a full ANSI escape sequence string.
// This is the primary output path for Bubble Tea integration.
//
// Algorithm:
//  1. Start each row from the leftmost column
//  2. Track "current style" (fg, bg, attr)
//  3. When a cell's style differs from current, emit SGR sequence to update
//  4. Write the cell's character
//  5. Separate rows with \n
//  6. Reset style at end of each row if non-default (ensures clean row starts)
//  7. Final reset at end of frame if needed
//
// Optimization:
//   - Uses strings.Builder (optimized for string building, not bytes.Buffer)
//   - Only emits SGR when style actually changes from the previous cell
//   - Skips continuation cells (Width==0) entirely (the primary wide char
//     already wrote them)
//   - Uses reset+set SGR strategy (simpler than incremental, avoids subtle
//     bugs with partial SGR updates)
//
// Performance: this function is called every frame (potentially at 60fps).
// The inner loop is kept tight — style changes use fmt.Fprintf which is
// only invoked when the style actually differs from the previous cell.
func GenerateANSI(buf *surface.CellBuffer) string {
	w, h := buf.Width(), buf.Height()
	if w == 0 || h == 0 {
		return ""
	}

	var sb strings.Builder
	// Pre-allocate: each cell is roughly 4 bytes (char) on average.
	// SGR sequences add more bytes but only on style changes.
	sb.Grow(w * h * 4)

	cur := cellStyleState{} // default state: default fg, default bg, no attrs

	for y := 0; y < h; y++ {
		if y > 0 {
			sb.WriteByte('\n')
		}
		for x := 0; x < w; x++ {
			c := buf.Get(x, y)

			// Skip continuation cells — they're rendered as part of their
			// primary wide cell. Writing them would produce duplicate output.
			if c.Width == 0 {
				continue
			}

			// Determine target style
			targetFG := c.FG
			targetBG := c.BG
			targetAttr := c.Attr

			// Emit style change if needed
			setStyle(&sb, targetFG, targetBG, targetAttr, &cur)

			// Write the character
			if c.Char == 0 {
				sb.WriteByte(' ')
			} else {
				sb.WriteRune(c.Char)
			}

			// For wide chars, the continuation cell position is implicitly
			// filled by the terminal (the wide char occupies 2 columns).
		}

		// Reset style at end of row if non-default.
		// This ensures each row starts clean, which is important for
		// terminals that wrap lines — a style leak from one row would
		// bleed into the next.
		if !cur.fg.IsDefault() || !cur.bg.IsDefault() || cur.attr != 0 {
			sb.WriteString("\x1b[0m")
			cur = cellStyleState{}
		}
	}

	// Final reset if needed (belt-and-suspenders for robustness)
	if !cur.fg.IsDefault() || !cur.bg.IsDefault() || cur.attr != 0 {
		sb.WriteString("\x1b[0m")
	}

	return sb.String()
}

// GenerateDiffANSI converts only the changed cells into ANSI output.
// Uses cursor positioning (CSI H) to jump to changed cells.
//
// For V1 with Bubble Tea, this is not used (full-frame is the default).
// Included for future direct-to-terminal rendering where diff-based output
// provides significant savings when only a small portion of the screen changed.
func GenerateDiffANSI(w, h int, diff *DiffResult) string {
	if diff == nil || len(diff.Cells) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.Grow(len(diff.Cells) * 24) // conservative estimate

	cur := cellStyleState{}

	for _, change := range diff.Cells {
		// Skip continuation cells
		if change.Cell.Width == 0 {
			continue
		}

		// Cursor positioning: \x1b[row;colH (1-based coordinates)
		fmt.Fprintf(&sb, "\x1b[%d;%dH", change.Y+1, change.X+1)

		// Set style
		setStyle(&sb, change.Cell.FG, change.Cell.BG, change.Cell.Attr, &cur)

		// Write character
		if change.Cell.Char == 0 {
			sb.WriteByte(' ')
		} else {
			sb.WriteRune(change.Cell.Char)
		}
	}

	// Final reset if needed
	if !cur.fg.IsDefault() || !cur.bg.IsDefault() || cur.attr != 0 {
		sb.WriteString("\x1b[0m")
	}

	return sb.String()
}

// setStyle emits the minimal ANSI SGR sequence to transition from
// the current style state to the target style.
//
// Strategy: emit a full reset+set sequence whenever the style changes.
// While this is slightly more bytes than incremental updates, it's much
// simpler and avoids subtle bugs with partial SGR updates (e.g., turning
// off bold without resetting italic).
//
// The SGR sequence is: \x1b[0;1;3;38;5;196;48;2;255;0;0m
// Where 0=reset, then each attribute/color code is appended with ";" separator.
//
// Returns the new current state (updated in-place via cur pointer).
func setStyle(sb *strings.Builder, targetFG, targetBG cell.Color, targetAttr cell.Attrs, cur *cellStyleState) {
	if targetFG == cur.fg && targetBG == cur.bg && targetAttr == cur.attr {
		return // No change needed
	}

	// Build complete SGR sequence: \x1b[0;...m
	// Start with reset (0) to clear all previous attributes
	sb.WriteString("\x1b[0")

	// Attributes (using standard SGR codes)
	if targetAttr&cell.AttrBold != 0 {
		sb.WriteString(";1")
	}
	if targetAttr&cell.AttrDim != 0 {
		sb.WriteString(";2")
	}
	if targetAttr&cell.AttrItalic != 0 {
		sb.WriteString(";3")
	}
	if targetAttr&cell.AttrUnderline != 0 {
		sb.WriteString(";4")
	}
	if targetAttr&cell.AttrBlink != 0 {
		sb.WriteString(";5")
	}
	if targetAttr&cell.AttrReverse != 0 {
		sb.WriteString(";7")
	}
	if targetAttr&cell.AttrStrikethrough != 0 {
		sb.WriteString(";9")
	}

	// Foreground color
	if !targetFG.IsDefault() {
		switch targetFG.Type {
		case cell.ColorPalette:
			fmt.Fprintf(sb, ";38;5;%d", targetFG.Palette)
		case cell.ColorTrueColor:
			fmt.Fprintf(sb, ";38;2;%d;%d;%d", targetFG.R, targetFG.G, targetFG.B)
		}
	}

	// Background color
	if !targetBG.IsDefault() {
		switch targetBG.Type {
		case cell.ColorPalette:
			fmt.Fprintf(sb, ";48;5;%d", targetBG.Palette)
		case cell.ColorTrueColor:
			fmt.Fprintf(sb, ";48;2;%d;%d;%d", targetBG.R, targetBG.G, targetBG.B)
		}
	}

	sb.WriteString("m")

	*cur = cellStyleState{fg: targetFG, bg: targetBG, attr: targetAttr}
}
