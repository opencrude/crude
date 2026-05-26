// Package wm implements the Window Manager layer (Layer 3) of the compositing engine.
//
// It provides floating windows with decorations (borders, title bars, close buttons),
// drag and resize interaction, z-ordering, and focus management.
//
// The Window Manager depends only on the surface, widget, cell, and theme packages.
// It does NOT depend on the compositor package — integration with the compositor
// happens at the application layer.
package wm

// WindowState represents the current state of a floating window.
type WindowState int

const (
	// WindowNormal is the default window state.
	WindowNormal WindowState = iota
	// WindowMinimized means the window is collapsed to its title bar.
	WindowMinimized
	// WindowMaximized means the window fills the screen (except for any reserved areas).
	WindowMaximized
	// WindowFullscreen means the window covers the entire screen.
	WindowFullscreen
)

// Edge identifies a border edge or corner of a window, used for resize hit-testing.
type Edge int

const (
	EdgeNone       Edge = iota
	EdgeTop             // Top border edge
	EdgeBottom          // Bottom border edge
	EdgeLeft            // Left border edge
	EdgeRight           // Right border edge
	EdgeTopLeft         // Top-left corner
	EdgeTopRight        // Top-right corner
	EdgeBottomLeft      // Bottom-left corner
	EdgeBottomRight     // Bottom-right corner
)

// BorderStyle defines the visual style of window borders.
type BorderStyle int

const (
	BorderNone    BorderStyle = iota // No border
	BorderSingle                     // ┌─┐│└─┘
	BorderDouble                     // ╔═╗║╚═╝
	BorderRounded                    // ╭─╮│╰─╯
	BorderThick                      // ┏━┓┃┗━┛
	BorderASCII                      // +-+|
	BorderLShape                     // Left+bottom border only, like ⌐ shape
)

// TextAlign controls horizontal alignment of text within a region.
type TextAlign int

const (
	AlignLeft TextAlign = iota
	AlignCenter
	AlignRight
)

// BorderChars holds the rune characters for drawing a border of a given style.
type BorderChars struct {
	TopLeft, TopRight, BottomLeft, BottomRight rune
	Horizontal, Vertical                       rune
	LeftT, RightT, TopT, BottomT               rune // T-junctions for future split panes
}

// BorderCharsFor returns the character set for a given border style.
func BorderCharsFor(style BorderStyle) BorderChars {
	switch style {
	case BorderSingle:
		return BorderChars{
			TopLeft: '┌', TopRight: '┐', BottomLeft: '└', BottomRight: '┘',
			Horizontal: '─', Vertical: '│',
			LeftT: '├', RightT: '┤', TopT: '┬', BottomT: '┴',
		}
	case BorderDouble:
		return BorderChars{
			TopLeft: '╔', TopRight: '╗', BottomLeft: '╚', BottomRight: '╝',
			Horizontal: '═', Vertical: '║',
			LeftT: '╠', RightT: '╣', TopT: '╦', BottomT: '╩',
		}
	case BorderRounded:
		return BorderChars{
			TopLeft: '╭', TopRight: '╮', BottomLeft: '╰', BottomRight: '╯',
			Horizontal: '─', Vertical: '│',
			LeftT: '├', RightT: '┤', TopT: '┬', BottomT: '┴',
		}
	case BorderThick:
		return BorderChars{
			TopLeft: '┏', TopRight: '┓', BottomLeft: '┗', BottomRight: '┛',
			Horizontal: '━', Vertical: '┃',
			LeftT: '┣', RightT: '┫', TopT: '┳', BottomT: '┻',
		}
	case BorderASCII:
		return BorderChars{
			TopLeft: '+', TopRight: '+', BottomLeft: '+', BottomRight: '+',
			Horizontal: '-', Vertical: '|',
			LeftT: '+', RightT: '+', TopT: '+', BottomT: '+',
		}
	case BorderLShape:
		return BorderChars{
			TopLeft: ' ', TopRight: ' ', BottomLeft: '└', BottomRight: '─',
			Horizontal: '─', Vertical: '│',
			LeftT: '├', RightT: '┤', TopT: '┬', BottomT: '┴',
		}
	default:
		// BorderNone: all spaces
		return BorderChars{
			TopLeft: ' ', TopRight: ' ', BottomLeft: ' ', BottomRight: ' ',
			Horizontal: ' ', Vertical: ' ',
			LeftT: ' ', RightT: ' ', TopT: ' ', BottomT: ' ',
		}
	}
}

// WindowOption configures a FloatingWindow during creation.
type WindowOption func(*FloatingWindow)

// WithDecoration controls whether the window has a border and title bar.
func WithDecoration(decorated bool) WindowOption {
	return func(fw *FloatingWindow) { fw.decorated = decorated }
}

// WithMovable controls whether the window can be dragged by its title bar.
func WithMovable(movable bool) WindowOption {
	return func(fw *FloatingWindow) { fw.movable = movable }
}

// WithResizable controls whether the window can be resized by dragging its edges.
func WithResizable(resizable bool) WindowOption {
	return func(fw *FloatingWindow) { fw.resizable = resizable }
}

// WithClosable controls whether the window has a close button in the title bar.
func WithClosable(closable bool) WindowOption {
	return func(fw *FloatingWindow) { fw.closable = closable }
}

// WithModal controls whether the window blocks input to windows below it.
func WithModal(modal bool) WindowOption {
	return func(fw *FloatingWindow) { fw.modal = modal }
}

// WithBorderStyle sets the border drawing style.
func WithBorderStyle(style BorderStyle) WindowOption {
	return func(fw *FloatingWindow) { fw.borderStyle = style }
}

// WithMinSize sets the minimum window size (including decorations).
func WithMinSize(w, h int) WindowOption {
	return func(fw *FloatingWindow) { fw.minW, fw.minH = w, h }
}

// WithMaxSize sets the maximum window size (including decorations).
// A value of 0 means unlimited on that axis.
func WithMaxSize(w, h int) WindowOption {
	return func(fw *FloatingWindow) { fw.maxW, fw.maxH = w, h }
}

// WithTitleAlign sets the alignment of the title text in the title bar.
func WithTitleAlign(align TextAlign) WindowOption {
	return func(fw *FloatingWindow) { fw.titleAlign = align }
}
