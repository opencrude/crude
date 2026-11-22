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
