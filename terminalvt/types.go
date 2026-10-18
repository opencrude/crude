// Package terminalvt owns nonvisual libghostty-vt parsing and protocol encoding.
// Engine methods require serialization; returned projections own their memory.
package terminalvt

import (
	"errors"
	"time"
)

var (
	ErrNoCGO       = errors.New("libghostty-vt requires cgo; rebuild with CGO_ENABLED=1")
	ErrUnavailable = errors.New("libghostty-vt is unavailable")
	ErrClosed      = errors.New("terminal engine closed")
	ErrBounds      = errors.New("terminal projection bounds exceeded")
)

const (
	CellPixelWidth     = 8
	CellPixelHeight    = 16
	MaxCols            = 1000
	MaxRows            = 500
	MaxSnapshotRows    = 512
	MaxScrollbackLines = 10000
	// Backing history is additionally byte-bounded (Ghostty page granularity).
	MaxScrollbackBytes = 32 << 20
	// A broken child cannot indefinitely retain an uncommitted visual update.
	SynchronizedOutputTimeout = time.Second
)

type Options struct{ Cols, Rows, ScrollbackLines int }

func validateOptions(o *Options) error {
	if o.Cols == 0 {
		o.Cols = 80
	}
	if o.Rows == 0 {
		o.Rows = 24
	}
	if o.ScrollbackLines == 0 {
		o.ScrollbackLines = MaxScrollbackLines
	}
	if o.Cols < 8 || o.Rows < 2 || o.Cols > MaxCols || o.Rows > MaxRows || o.ScrollbackLines < 0 || o.ScrollbackLines > MaxScrollbackLines {
		return ErrBounds
	}
	return nil
}

type Effects struct {
	Replies                        [][]byte
	Title, CWD                     string
	TitleChanged, CWDChanged, Bell bool
	// Clipboard is offered decoded bytes only; no host clipboard access occurs.
	Clipboard [][]byte
}
type Color struct {
	Explicit bool
	R, G, B  uint8
}
type Style struct{ Bold, Faint, Italic, Underline, Strikethrough, Inverse bool }
type Cell struct {
	Text   string
	Width  uint8
	FG, BG Color
	Style  Style
}
type Cursor struct {
	X, Y           int
	Visible, Blink bool
}
type Modes struct{ BracketedPaste, MouseTracking, AltScreen, FocusReporting bool }

// RowRequest binds relative history coordinates to the publication that supplied
// them. Count zero requests a complete viewport at the current grid height;
// Start -1 explicitly follows the live active grid without an historical anchor.
type RowRequest struct {
	Start, Count int
	// ViewportStart is the recovery viewport if a selection page's epoch became
	// incompatible; -1 follows the active grid. It shares the same history clock.
	ViewportStart int
	HistoryEpoch  uint64
	HistoryBase   uint64
}

type Projection struct {
	Cols, GridRows int
	// Start and Total address current screen history from its oldest retained row.
	Start, Total int
	Cells        [][]Cell
	// Wrapped marks rows that continue onto the next physical row.
	Wrapped []bool
	// HistoryEpoch changes on incompatible reset, screen switch or reflow.
	// HistoryBase advances by exact oldest-row eviction within the same epoch.
	HistoryEpoch uint64
	HistoryBase  uint64
	Cursor       Cursor
	Modes        Modes
	// Synchronized publishes the completed active grid during DEC 2026. New
	// historical pages wait until commit; previously detached history stays valid
	// only while HistoryEpoch is unchanged.
	Synchronized bool
	// ReadSequence orders detached pages even when a timeout publishes new
	// visual state without any additional PTY ingress generation.
	ReadSequence uint64
}
type Modifiers uint8

const (
	ModShift Modifiers = 1 << iota
	ModCtrl
	ModAlt
	ModSuper
)

func (m Modifiers) Contains(n Modifiers) bool { return m&n != 0 }

type Key struct {
	Code rune
	Text string
	Mod  Modifiers
}

// Special key values are neutral identities, not framework key codes.
const (
	KeyEnter rune = 0x110000 + iota
	KeyTab
	KeyBackspace
	KeyEscape
	KeyUp
	KeyDown
	KeyLeft
	KeyRight
	KeyHome
	KeyEnd
	KeyPgUp
	KeyPgDown
	KeyInsert
	KeyDelete
	KeySpace
	KeyF1
	KeyF2
	KeyF3
	KeyF4
	KeyF5
	KeyF6
	KeyF7
	KeyF8
	KeyF9
	KeyF10
	KeyF11
	KeyF12
)

type MouseAction uint8

const (
	MousePress MouseAction = iota
	MouseRelease
	MouseMotion
)

type MouseButton uint8

const (
	MouseNone MouseButton = iota
	MouseLeft
	MouseMiddle
	MouseRight
	MouseWheelUp
	MouseWheelDown
)

type Mouse struct {
	Action MouseAction
	Button MouseButton
	X, Y   int
	Mod    Modifiers
}
