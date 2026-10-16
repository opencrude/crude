// Package wm implements the Window Manager layer (Layer 3) of the compositing engine.
//
// It provides floating-window placement, drag and resize interaction, z-ordering,
// focus management, and WindowFrame-aligned hit testing. Visual chrome lives in
// tui/ui.WindowFrame.
//
// The Window Manager depends only on the surface, widget, cell, and theme packages.
// It does NOT depend on the compositor package; integration with the compositor
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
	EdgeNone        Edge = iota
	EdgeTop              // Top border edge
	EdgeBottom           // Bottom border edge
	EdgeLeft             // Left border edge
	EdgeRight            // Right border edge
	EdgeTopLeft          // Top-left corner
	EdgeTopRight         // Top-right corner
	EdgeBottomLeft       // Bottom-left corner
	EdgeBottomRight      // Bottom-right corner
)

// ClickOutsideBehavior defines what happens when the user clicks outside
// this window. This is a critical UX distinction: context menus close,
// find/replace stays open but routes the click, modals ignore outside clicks.
type ClickOutsideBehavior int

const (
	// ClickOutsideCloseRoute closes the window and routes the click
	// to the underlying widget. Used for context menus, hover popups.
	ClickOutsideCloseRoute ClickOutsideBehavior = iota
	// ClickOutsideKeepRoute keeps the window open and routes the click
	// to the underlying widget. Used for find/replace, file picker.
	ClickOutsideKeepRoute
	// ClickOutsideIgnore does nothing; the window stays focused and
	// the click has no effect. Used for modal dialogs (save confirm,
	// text prompt, tree delete confirm).
	ClickOutsideIgnore
)

// WithClickOutside sets the click-outside behavior for this window.
func WithClickOutside(b ClickOutsideBehavior) WindowOption {
	return func(fw *FloatingWindow) { fw.clickOutside = b }
}

// WindowOption configures a FloatingWindow during creation.
type WindowOption func(*FloatingWindow)

// WithDecoration controls whether standard WindowFrame interaction regions
// (title drag, close, resize edges) are active. It does not paint visual chrome.
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

// WithFocusable controls whether the window participates in focus and mouse hit-testing.
// Non-focusable windows are still rendered, but mouse and keyboard input passes through.
func WithFocusable(focusable bool) WindowOption {
	return func(fw *FloatingWindow) { fw.focusable = focusable }
}

// WithMinSize sets the minimum complete WindowFrame size.
func WithMinSize(w, h int) WindowOption {
	return func(fw *FloatingWindow) { fw.minW, fw.minH = w, h }
}

// WithMaxSize sets the maximum complete WindowFrame size.
// A value of 0 means unlimited on that axis.
func WithMaxSize(w, h int) WindowOption {
	return func(fw *FloatingWindow) { fw.maxW, fw.maxH = w, h }
}
