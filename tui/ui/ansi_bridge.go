package ui

import (
	"crude/tui/rendering"
	"crude/tui/surface"
)

// renderANSIToSurface is the controlled bridge from ANSI strings into cells.
// Only adapters may call this.
func renderANSIToSurface(surf *surface.Surface, ansi string) {
	if surf == nil {
		return
	}
	rendering.RenderToSurface(surf, ansi)
}
