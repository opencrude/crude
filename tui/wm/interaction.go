package wm

// ClampPosition ensures a window stays within screen bounds.
// At least 3 rows and 10 columns of the window must remain visible
// on screen. If the window is partially off-screen, it is moved back
// so the minimum visible area is preserved.
func ClampPosition(fw *FloatingWindow, screenW, screenH int) {
	if screenW <= 0 || screenH <= 0 {
		return
	}

	// Minimum visible width and height (in cells).
	minVisibleW := 10
	minVisibleH := 3

	// Ensure at least minVisibleW columns are visible from the left.
	maxX := screenW - minVisibleW
	if maxX < -fw.w+minVisibleW {
		maxX = -fw.w + minVisibleW
	}
	if fw.x > maxX {
		fw.x = maxX
	}

	// Ensure at least minVisibleH rows are visible from the top.
	maxY := screenH - minVisibleH
	if maxY < -fw.h+minVisibleH {
		maxY = -fw.h + minVisibleH
	}
	if fw.y > maxY {
		fw.y = maxY
	}

	// Don't let the window go too far off the left/top.
	// The left edge can go off screen, but at least minVisibleW cells
	// of the right side must be visible.
	if fw.x < -fw.w+minVisibleW {
		fw.x = -fw.w + minVisibleW
	}
	if fw.y < -fw.h+minVisibleH {
		fw.y = -fw.h + minVisibleH
	}
}

// ClampSize ensures a window size is within min/max bounds.
//
// For decorated windows, the minimum size is at least 5×3
// (2 border columns + 3 content columns, 2 border rows + 1 content row).
// For undecorated windows, the minimum is 1×1.
func ClampSize(fw *FloatingWindow) {
	// Enforce minimums.
	minW := fw.minW
	minH := fw.minH
	if fw.decorated {
		if minW < 5 {
			minW = 5
		}
		if minH < 3 {
			minH = 3
		}
	} else {
		if minW < 1 {
			minW = 1
		}
		if minH < 1 {
			minH = 1
		}
	}
	if fw.w < minW {
		fw.w = minW
	}
	if fw.h < minH {
		fw.h = minH
	}

	// Enforce maximums (0 = unlimited).
	if fw.maxW > 0 && fw.w > fw.maxW {
		fw.w = fw.maxW
	}
	if fw.maxH > 0 && fw.h > fw.maxH {
		fw.h = fw.maxH
	}
}

// CenterOnScreen centers a window on the screen.
func CenterOnScreen(fw *FloatingWindow, screenW, screenH int) {
	fw.x = (screenW - fw.w) / 2
	fw.y = (screenH - fw.h) / 2
}

// CascadePositions arranges multiple windows in a cascade pattern,
// each offset by (2, 1) from the previous, to avoid full overlap.
// The first window is positioned near the top-left of the screen
// with some padding. If there are many windows, the cascade wraps
// back toward the top to avoid going off-screen.
func CascadePositions(windows []*FloatingWindow, screenW, screenH int) {
	if len(windows) == 0 {
		return
	}

	offsetX := 2
	offsetY := 1
	startX := 4
	startY := 2

	// Calculate how many offsets fit before we go off-screen.
	// Reset the cascade position when windows would go off the bottom.
	maxY := screenH - 5  // leave some margin at the bottom
	maxX := screenW - 10 // leave some margin at the right

	x := startX
	y := startY

	for _, fw := range windows {
		fw.x = x
		fw.y = y

		x += offsetX
		y += offsetY

		// Wrap if we'd go off-screen.
		if y > maxY || x > maxX {
			x = startX + 4
			y = startY
		}
	}
}
