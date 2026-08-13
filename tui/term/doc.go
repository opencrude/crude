package term

import "errors"

// CellPixelWidth and CellPixelHeight are the logical cell pixel dimensions
// passed to libghostty-vt resize. Size reports (CSI 14/16/18 t) use
// cols*width and rows*height so full-screen programs get consistent geometry
// in Crude's cell TUI.
const (
	CellPixelWidth  = 8
	CellPixelHeight = 16
)

const defaultScrollbackLines = 10000

// ErrNoCGO is returned by NewEmulator when the binary was built with CGO disabled.
var ErrNoCGO = errors.New("libghostty-vt requires cgo; rebuild with CGO_ENABLED=1")

// ErrUnavailable is returned when libghostty-vt cannot construct a terminal.
var ErrUnavailable = errors.New("libghostty-vt is unavailable")
