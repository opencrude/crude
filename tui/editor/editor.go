package editor

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/fsnotify/fsnotify"

	"crude/tui/scrollbar"
	"crude/tui/theme"
)

type ActionType int

const (
	ActInsert ActionType = iota
	ActDelete
)

type Change struct {
	Type         ActionType
	Text         []byte
	StartPos     Position
	EndPos       Position
	CursorBefore Position
}

type Editor struct {
	th       *theme.Theme
	buf      *PieceTable
	filePath string
	title    string

	cursor  Position
	scrollX int
	scrollY int

	goalCol int
	goalSet bool

	viewW     int
	viewH     int
	scrollbar scrollbar.ScrollBar
	sbTrackH  int

	modified bool

	changes     []Change
	changeIndex int
	maxHistory  int

	open bool

	cursorVisible bool
	lastActivity  time.Time
	lastToggle    time.Time
	blinkActive   bool

	selecting bool
	selAnchor Position
	selEnd    Position

	chromaStyle *chroma.Style
	chromaLexer chroma.Lexer
	chromaCache map[string][]string

	lineRunesCacheValid bool
	lineRunesCacheLine  int
	lineRunesCache      []rune
	lineRunesCacheWidth int
	lineRenderCache     map[int]*lineRenderCache

	watcher      *fsnotify.Watcher
	watchDir     string
	watchChanged chan struct{}
	watchDone    chan struct{}

	// lastSaveTime records when the editor last wrote to disk. The watcher
	// suppresses fsnotify events that arrive within a short window of our own
	// save to avoid self-triggering a Reload + redundant LSP notification.
	lastSaveTime time.Time

	diagMu      sync.RWMutex
	diagnostics []LSPDiagnostic
	diagByLine  map[int][]diagSpan

	// onContentChanged is called after every edit so the App can sync LSP.
	onContentChanged func(filePath, text string)

	// hover state for diagnostic tooltip popup.
	hoverMessages []string
	hoverLine     int
	hoverCol      int
	hoverScreenX  int
	hoverScreenY  int
}

// HoverState is the current diagnostic hover state.
type HoverState struct {
	Messages []string
	Line     int
	Col      int
	ScreenX  int
	ScreenY  int
}

const (
	cursorBlinkInterval = 530 * time.Millisecond
	tickInterval        = 100 * time.Millisecond
	scrollMargin        = 3
	tabWidth            = 4
)

func New(th *theme.Theme) *Editor {
	e := &Editor{
		th:            th,
		buf:           NewPieceTable(),
		scrollbar:     scrollbar.New(0),
		maxHistory:    500,
		cursorVisible: true,
		diagnostics:   make([]LSPDiagnostic, 0),
		diagByLine:    make(map[int][]diagSpan),
	}
	e.ClearSelection()
	return e
}

func (e *Editor) Open(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	e.filePath = abs
	e.title = filepath.Base(abs)

	buf := NewPieceTable()
	err = buf.Load(abs)
	if err != nil {
		return err
	}
	e.buf = buf

	e.cursor = Position{Line: 0, Col: 0}
	e.scrollX = 0
	e.scrollY = 0
	e.modified = false
	e.changes = nil
	e.changeIndex = 0
	e.scrollbar = scrollbar.New(0)
	e.open = true
	e.cursorVisible = true
	e.blinkActive = false
	e.lastActivity = time.Now()
	e.ClearSelection()
	e.invalidateEditorCaches()
	e.initChroma()
	e.startWatching()

	return nil
}

func (e *Editor) Close() {
	e.stopWatching()
	e.open = false
	e.filePath = ""
	e.title = ""
	e.ClearSelection()
	e.diagMu.Lock()
	e.diagnostics = nil
	e.diagByLine = nil
	e.diagMu.Unlock()
}

func (e *Editor) Reload() error {
	if !e.open || e.filePath == "" {
		return nil
	}
	buf := NewPieceTable()
	err := buf.Load(e.filePath)
	if err != nil {
		return err
	}
	e.buf = buf
	e.modified = false
	e.chromaCache = nil
	e.invalidateEditorCaches()
	e.notifyContentChanged()
	return nil
}

func (e *Editor) invalidateEditorCaches() {
	e.lineRunesCacheValid = false
	e.lineRunesCacheLine = -1
	e.lineRunesCache = nil
	e.lineRunesCacheWidth = 0
	e.lineRenderCache = nil
}

func (e *Editor) IsOpen() bool     { return e.open }
func (e *Editor) FilePath() string { return e.filePath }
func (e *Editor) Title() string {
	if e.modified {
		return e.title + " ●"
	}
	return e.title
}
func (e *Editor) Modified() bool       { return e.modified }
func (e *Editor) Value() string        { return string(e.buf.Bytes()) }
func (e *Editor) CursorLine() int      { return e.cursor.Line }
func (e *Editor) CursorCol() int       { return e.cursor.Col }
func (e *Editor) LineCount() int       { return e.buf.LineCount() }
func (e *Editor) notifyContentChanged() {
	if e.onContentChanged != nil {
		e.onContentChanged(e.filePath, string(e.buf.Bytes()))
	}
}

// SetOnContentChanged registers a callback for text edits.
func (e *Editor) SetOnContentChanged(fn func(filePath, text string)) {
	e.onContentChanged = fn
}

// lspColToVisualCol converts a UTF-16 code unit offset from the start of a line
// (as reported by LSP in Position.Character) to a visual column position,
// accounting for tab expansion and wide characters.
func lspColToVisualCol(rawLine []byte, lspCol int) int {
	visCol := 0
	utf16Units := 0
	for _, r := range string(rawLine) {
		if utf16Units >= lspCol {
			break
		}
		// BMP (U+0000–U+FFFF) = 1 UTF-16 code unit,
		// supplementary (U+10000+) = 2 code units (surrogate pair).
		utf16Len := 1
		if r > 0xFFFF {
			utf16Len = 2
		}
		if utf16Units+utf16Len > lspCol {
			break
		}
		visCol += runeVisualWidth(r)
		utf16Units += utf16Len
	}
	return visCol
}

// SetDiagnostics replaces the current diagnostics for the editor.
func (e *Editor) SetDiagnostics(diags []LSPDiagnostic) {
	e.diagMu.Lock()

	// Convert LSP column offsets (UTF-16 code units from line start) to
	// visual column offsets (tab-expanded, wide-char-aware). Must make a
	// copy so we don't mutate the caller's slice (e.g. LSPManager cache).
	converted := make([]LSPDiagnostic, len(diags))
	copy(converted, diags)
	for i, d := range converted {
		rawLine, err := e.buf.Line(d.Line)
		if err == nil {
			converted[i].Col = lspColToVisualCol(rawLine, d.Col)
			converted[i].EndCol = lspColToVisualCol(rawLine, d.EndCol)
		}
	}
	e.diagnostics = converted

	byLine := make(map[int][]diagSpan, len(converted))
	for _, d := range converted {
		startCol := d.Col
		endCol := d.EndCol
		if endCol <= startCol {
			// LSP sometimes sends EndCol=0 for whole-line diagnostics.
			// Fall back to the visual width of the line.
			lw := e.lineVisualWidth(d.Line)
			if lw > startCol {
				endCol = lw
			} else {
				endCol = startCol + 1
			}
		}
		byLine[d.Line] = append(byLine[d.Line], diagSpan{
			startCol: startCol,
			endCol:   endCol,
			severity: d.Severity,
		})
	}
	e.diagByLine = byLine
	e.diagMu.Unlock()
}

func (e *Editor) DiagnosticCounts() (errors, warnings, hints int) {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	for _, d := range e.diagnostics {
		switch d.Severity {
		case DiagError:
			errors++
		case DiagWarning:
			warnings++
		default:
			hints++
		}
	}
	return
}

func (e *Editor) lineDiagnostics(lineIdx int) []diagSpan {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	if e.diagByLine == nil {
		return nil
	}
	return e.diagByLine[lineIdx]
}

// diagnosticsAt returns all LSPDiagnostics covering the given position.
func (e *Editor) diagnosticsAt(line, col int) []LSPDiagnostic {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	var result []LSPDiagnostic
	lineEnd := -1
	for _, d := range e.diagnostics {
		if d.Line != line {
			continue
		}
		endCol := d.EndCol
		if endCol <= d.Col {
			if lineEnd < 0 {
				lineEnd = e.lineVisualWidth(line)
			}
			if lineEnd <= d.Col {
				lineEnd = d.Col + 1
			}
			endCol = lineEnd
		}
		if col >= d.Col && col < endCol {
			result = append(result, d)
		}
	}
	return result
}

// SetHover sets the hover diagnostic state for a tooltip popup.
func (e *Editor) SetHover(screenX, screenY, line, col int, msgs []string) {
	e.hoverScreenX = screenX
	e.hoverScreenY = screenY
	e.hoverLine = line
	e.hoverCol = col
	e.hoverMessages = msgs
}

// ClearHover clears the hover diagnostic state.
func (e *Editor) ClearHover() {
	e.hoverMessages = nil
}

// HoverState returns the current hover diagnostics state.
func (e *Editor) HoverState() HoverState {
	return HoverState{
		Messages: e.hoverMessages,
		Line:     e.hoverLine,
		Col:      e.hoverCol,
		ScreenX:  e.hoverScreenX,
		ScreenY:  e.hoverScreenY,
	}
}

// refreshMouseHover updates hover diagnostics for a screen-positioned mouse.
func (e *Editor) refreshMouseHover(screenX, screenY int) {
	pos, ok := e.mouseToPosition(screenX, screenY)
	if !ok {
		e.ClearHover()
		return
	}
	diags := e.diagnosticsAt(pos.Line, pos.Col)
	if len(diags) == 0 {
		e.ClearHover()
		return
	}
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		msgs = append(msgs, d.Message)
	}
	e.SetHover(screenX, screenY, pos.Line, pos.Col, msgs)
}

// refreshCursorHover updates hover diagnostics for the keyboard cursor.
func (e *Editor) refreshCursorHover(sidebarW, chatAreaTop int) {
	diags := e.diagnosticsAt(e.cursor.Line, e.cursor.Col)
	if len(diags) == 0 {
		e.ClearHover()
		return
	}
	msgs := make([]string, 0, len(diags))
	for _, d := range diags {
		msgs = append(msgs, d.Message)
	}
	screenX, screenY := e.CursorScreenPosition(sidebarW, chatAreaTop)
	e.SetHover(screenX, screenY, e.cursor.Line, e.cursor.Col, msgs)
}

// CursorScreenPosition returns the approximate absolute screen cell of the cursor.
func (e *Editor) CursorScreenPosition(sidebarW, chatAreaTop int) (int, int) {
	bodyH := e.viewH
	if bodyH < 1 {
		bodyH = 1
	}
	lineOff := e.cursor.Line - e.scrollY
	if lineOff < 0 {
		lineOff = 0
	}
	if lineOff >= bodyH {
		lineOff = bodyH - 1
	}
	colOff := e.cursor.Col - e.scrollX
	if colOff < 0 {
		colOff = 0
	}
	return sidebarW + e.gutterWidth() + colOff, chatAreaTop + lineOff
}

func (e *Editor) severityColor(severity int) string {
	switch severity {
	case DiagError:
		return "#ff7b72"
	case DiagWarning:
		return "#d29922"
	default:
		return "#8b949e"
	}
}


