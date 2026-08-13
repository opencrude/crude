//go:build !cgo

package term

import (
	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/surface"
	"crude/tui/ui"
)

// Emulator is a CGO-disabled stub. NewEmulator returns ErrNoCGO.
type Emulator struct{}

func NewEmulator() (*Emulator, error) { return nil, ErrNoCGO }

func (e *Emulator) Close()                           {}
func (e *Emulator) SetTitleHandler(func(string))     {}
func (e *Emulator) SetReplyHandler(func([]byte))     {}
func (e *Emulator) SetBellHandler(func())            {}
func (e *Emulator) SetPwdHandler(func(string))       {}
func (e *Emulator) SetClipboardHandler(func(string)) {}
func (e *Emulator) Title() string                    { return "" }
func (e *Emulator) PWD() string                      { return "" }
func (e *Emulator) Cols() int                        { return 0 }
func (e *Emulator) Rows() int                        { return 0 }
func (e *Emulator) Cursor() (x, y int, visible bool) { return 0, 0, false }
func (e *Emulator) Resize(int, int)                  {}
func (e *Emulator) Feed([]byte)                      {}
func (e *Emulator) Paint(*surface.CellBuffer, int, int, cell.Color, cell.Color) {
}
func (e *Emulator) EncodeKey(tea.KeyPressMsg) []byte        { return nil }
func (e *Emulator) EncodePaste([]byte) []byte               { return nil }
func (e *Emulator) EncodeMouse(ui.Event, int, int) []byte   { return nil }
func (e *Emulator) EncodeFocus(bool) []byte                 { return nil }
func (e *Emulator) BracketedPaste() bool                    { return false }
func (e *Emulator) MouseTracking() bool                     { return false }
func (e *Emulator) AltScreen() bool                         { return false }
func (e *Emulator) FocusReporting() bool                    { return false }
func (e *Emulator) ViewportPinned() bool                    { return true }
func (e *Emulator) ScrollDelta(int)                         {}
func (e *Emulator) ScrollTop()                              {}
func (e *Emulator) ScrollBottom()                           {}
func (e *Emulator) Scrollbar() (offset, total, visible int) { return 0, 0, 0 }
func (e *Emulator) SetDefaultColors(cell.Color, cell.Color) {}
func (e *Emulator) CursorBlink() bool                       { return false }
func (e *Emulator) SetCursorState(bool, bool)               {}
func (e *Emulator) SelectionPress(int, int, uint64) bool    { return false }
func (e *Emulator) SelectionDrag(int, int) bool             { return false }
func (e *Emulator) SelectionRelease(int, int) bool          { return false }
func (e *Emulator) HasSelection() bool                      { return false }
func (e *Emulator) ClearSelection()                         {}
func (e *Emulator) SelectAll()                              {}
func (e *Emulator) SelectionText() string                   { return "" }
