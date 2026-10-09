package rendering

import (
	"crude/terminalvt"
	"crude/tui/cell"
)

// TerminalColor maps application-supplied terminal ink at the rendering
// boundary. Default colors remain theme roles supplied by the containing view.
func TerminalColor(value terminalvt.Color, fallback cell.Color) cell.Color {
	if value.Explicit {
		return cell.NewTrueColor(value.R, value.G, value.B)
	}
	return fallback
}
