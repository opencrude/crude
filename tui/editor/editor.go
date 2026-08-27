package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alecthomas/chroma/v2"
	"github.com/fsnotify/fsnotify"

	"crude/tui/theme"
	"crude/tui/ui"
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
	// Group links changes that undo/redo as one step. Zero means the
	// change stands alone; equal non-zero values form one atomic group.
	Group int
}

type Editor struct {
	th       *theme.Theme
	buf      *PieceTable
	filePath string
	title    string

	// buffer is the shared Buffer from BufferManager. When set, the Editor
	// delegates all text access (Value, LineCount, LineText, Save, etc.)
	// to the buffer instead of the local PieceTable. This enables buffer
	// sharing across multiple editor views (split view, diff view).
	// nil when the Editor was opened via the legacy Open() path.
	buffer *Buffer

	cursor  Position
	scrollX int
	scrollY int

	goalCol int
	goalSet bool

	viewW int
	viewH int
	// scrollModel + sbCtrl are the shared scrollbar chrome (paint + mouse drag).
	// Kept in sync with scrollY via syncScrollChrome.
	scrollModel *ui.ScrollModel
	sbCtrl      *ui.ScrollbarControl
	tokens      *ui.TokenSet
	// statusChrome, when true, reserves the bottom row for a document footline.
	// The main shell keeps this false — status lives in shellstatus.
	statusChrome bool

	modified bool

	changes     []Change
	changeIndex int
	maxHistory  int

	// undoGroupSeq/undoGroupDepth/undoGroupID implement undo grouping:
	// changes recorded between beginUndoGroup and endUndoGroup share one
	// group ID and undo/redo together as a single step.
	undoGroupSeq   int
	undoGroupDepth int
	undoGroupID    int

	open bool

	cursorVisible bool
	lastActivity  time.Time
	lastToggle    time.Time
	blinkActive   bool

	selecting     bool // keyboard or mouse selection anchor is active
	mouseDragging bool // true only while a mouse button drag is in progress
	selAnchor     Position
	selEnd        Position

	PadL int // left padding applied by the app layout

	// lineNumberOffset is added to rendered line numbers for read-only excerpt
	// editors such as Find in Files previews. Normal editors keep it at zero.
	lineNumberOffset int

	chromaStyle *chroma.Style
	chromaLexer chroma.Lexer
	// chromaLineCache is per-line colour maps from windowed (state-carrying)
	// tokenisation. Keyed by line index; invalidated from an edit line onward.
	chromaLineCache map[int]*chromaLineEntry
	// chromaDirtyFrom is the lowest line whose chroma cache may be stale
	// after an edit. -1 means nothing is dirty.
	chromaDirtyFrom int

	lineRunesCacheValid bool
	lineRunesCacheLine  int
	lineRunesCache      []rune
	lineRunesCacheWidth int
	lineGeomCache       map[int]*lineGeomCache

	watcher      *fsnotify.Watcher
	watchDir     string
	watchChanged chan struct{}
	watchDone    chan struct{}
	watchMu      sync.Mutex

	// lastSaveTime records when the editor last wrote to disk. The watcher
	// suppresses fsnotify events that arrive within a short window of our own
	// save to avoid self-triggering a Reload + redundant LSP notification.
	lastSaveTime time.Time

	diagMu      sync.RWMutex
	diagnostics []LSPDiagnostic
	diagByLine  map[int][]diagSpan

	// onContentChanged is called after every edit so the App can sync LSP.
	onContentChanged func(filePath, text string)

	// gitEdits/gitBlame are presentation copies of the workspace Git
	// annotation. Index 0 is editor line 0.
	gitEdits  []GitLineKind
	gitBlame  []GitBlameLine
	gitLoaded bool

	// formatOnSave, when true, runs the file's language-specific formatter
	// (gofmt/rustfmt/black) right after Save() succeeds. Set via
	// SetFormatOnSave(). Defaults to false to keep save behavior predictable
	// for users who don't want surprise reformatting.
	formatOnSave bool

	// hover state for diagnostic tooltip popup.
	hoverMessages []string
	hoverSeverity int
	hoverLine     int
	hoverCol      int
	hoverScreenX  int
	hoverScreenY  int

	// search is the per-editor find/replace state. Owned by the
	// Editor (not the tab wrapper) so the renderer can read it
	// directly when drawing highlights. Each tab has its own Editor
	// so this is automatically per-tab.
	search *SearchState
}

func (e *Editor) SetPadding(padL int) {
	e.PadL = padL
}

// HoverState is the current diagnostic hover state.
type HoverState struct {
	Messages []string
	Severity int
	Line     int
	Col      int
	ScreenX  int
	ScreenY  int
}

const (
	cursorBlinkInterval = 530 * time.Millisecond
	tickInterval        = 100 * time.Millisecond
	scrollMargin        = 3
	tabWidth            = DefaultTabWidth
)

func New(th *theme.Theme) *Editor {
	e := &Editor{
		th:            th,
		buf:           NewPieceTable(),
		scrollModel:   ui.NewScrollModel(),
		tokens:        ui.NewTokenSet(th),
		maxHistory:    500,
		cursorVisible: true,
		diagnostics:   make([]LSPDiagnostic, 0),
		diagByLine:    make(map[int][]diagSpan),
		search:        NewSearchState(),
	}
	e.sbCtrl = ui.NewScrollbarControl("editor/scrollbar", e.scrollModel, e.tokens)
	e.ClearSelection()
	return e
}

// NewPreview creates a read-only, watcher-free editor over an in-memory
// excerpt. firstLine is the 1-based source line represented by text line 1.
func NewPreview(th *theme.Theme, path, text string, firstLine int) *Editor {
	e := New(th)
	e.filePath = path
	e.title = filepath.Base(path)
	e.buf.ReplaceAll(text)
	e.open = true
	e.cursorVisible = false
	if firstLine > 1 {
		e.lineNumberOffset = firstLine - 1
	}
	e.initChroma()
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
	e.buffer = nil

	e.cursor = Position{Line: 0, Col: 0}
	e.scrollX = 0
	e.scrollY = 0
	e.modified = false
	e.changes = nil
	e.changeIndex = 0
	e.scrollModel = ui.NewScrollModel()
	e.sbCtrl = ui.NewScrollbarControl("editor/scrollbar", e.scrollModel, e.tokens)
	e.open = true
	e.cursorVisible = true
	e.blinkActive = false
	e.lastActivity = time.Now()
	e.ClearSelection()
	e.ClearHover()
	e.invalidateEditorCaches()
	e.initChroma()
	e.startWatching()

	return nil
}

// OpenBuffer opens the editor on a shared Buffer, bypassing direct file
// loading. The Editor delegates all text access to the buffer. Call this
// instead of Open() when using the BufferManager.
func (e *Editor) OpenBuffer(buf *Buffer) {
	e.buffer = buf
	e.filePath = buf.Path
	e.title = buf.Title
	e.buf = buf.buf

	e.cursor = Position{Line: 0, Col: 0}
	e.scrollX = 0
	e.scrollY = 0
	e.modified = false
	e.changes = nil
	e.changeIndex = 0
	e.scrollModel = ui.NewScrollModel()
	e.sbCtrl = ui.NewScrollbarControl("editor/scrollbar", e.scrollModel, e.tokens)
	e.open = true
	e.cursorVisible = true
	e.blinkActive = false
	e.lastActivity = time.Now()
	e.ClearSelection()
	e.ClearHover()
	e.invalidateEditorCaches()
	e.initChroma()
	if e.filePath != "" {
		e.startWatching()
	}
}

// ReplaceDocument replaces the whole buffer text (shared Buffer or local
// PieceTable), invalidates caches, and notifies listeners. Cursor is clamped.
func (e *Editor) ReplaceDocument(text string) {
	if e.buffer != nil {
		e.buffer.ReplaceContents(text)
	} else if e.buf != nil {
		e.buf.ReplaceAll(text)
		e.modified = true
	}
	e.invalidateEditorCaches()
	if e.cursor.Line >= e.LineCount() {
		e.cursor.Line = max(0, e.LineCount()-1)
	}
	e.notifyContentChanged()
}

func (e *Editor) Close() {
	e.stopWatching()
	e.open = false
	e.filePath = ""
	e.title = ""
	e.buffer = nil
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
	e.chromaLineCache = nil
	e.chromaDirtyFrom = 0
	e.invalidateEditorCaches()
	e.ClearHover()
	e.notifyContentChanged()
	return nil
}

func (e *Editor) invalidateEditorCaches() {
	e.lineRunesCacheValid = false
	e.lineRunesCacheLine = -1
	e.lineRunesCache = nil
	e.lineRunesCacheWidth = 0
	e.lineGeomCache = nil
	e.chromaLineCache = nil
	e.chromaDirtyFrom = 0
}

// InvalidateWidthProfile clears all geometry derived from terminal cell widths.
// Buffer contents, cursor position, undo state, and syntax data are preserved.
func (e *Editor) InvalidateWidthProfile() {
	e.lineRunesCacheValid = false
	e.lineRunesCacheLine = -1
	e.lineRunesCache = nil
	e.lineRunesCacheWidth = 0
	e.lineGeomCache = nil
}

func (e *Editor) IsOpen() bool     { return e.open }
func (e *Editor) FilePath() string { return e.filePath }

// ScrollY returns the first visible document line index.
func (e *Editor) ScrollY() int { return e.scrollY }

// CursorVisible returns true when the cursor should be drawn as visible
// (solid or blinking-visible phase). Used by the viewmodel to avoid
// re-rendering on blink ticks that don't change the cursor state.
func (e *Editor) CursorVisible() bool { return e.cursorVisible }

func (e *Editor) Title() string {
	if e.Modified() {
		return e.title + " ●"
	}
	return e.title
}
func (e *Editor) Modified() bool {
	if e.buffer != nil {
		return e.buffer.IsDirty()
	}
	return e.modified
}
func (e *Editor) Value() string {
	if e.buffer != nil {
		return string(e.buffer.Bytes())
	}
	return string(e.buf.Bytes())
}
func (e *Editor) CursorLine() int { return e.cursor.Line }
func (e *Editor) CursorCol() int  { return e.cursor.Col }
func (e *Editor) LineCount() int {
	if e.buffer != nil {
		return e.buffer.LineCount()
	}
	return e.buf.LineCount()
}
func (e *Editor) LineText(line int) string {
	if e.buffer != nil {
		return e.buffer.Line(line)
	}
	b, err := e.buf.Line(line)
	if err != nil {
		return ""
	}
	return strings.TrimRight(string(b), "\n")
}
func (e *Editor) notifyContentChanged() {
	if e.onContentChanged != nil {
		e.onContentChanged(e.filePath, string(e.buf.Bytes()))
	}
}

// SetOnContentChanged registers a callback for text edits.
func (e *Editor) SetOnContentChanged(fn func(filePath, text string)) {
	e.onContentChanged = fn
}

// SetFormatOnSave controls whether the editor runs the file's
// language-specific formatter (gofmt, rustfmt, black) after Save().
func (e *Editor) SetFormatOnSave(v bool) {
	e.formatOnSave = v
}

// SetDiagnostics replaces the current diagnostics for the editor.
func (e *Editor) SetDiagnostics(diags []LSPDiagnostic) {
	e.diagMu.Lock()

	// Convert LSP column offsets (UTF-16 code units from line start) to
	// visual column offsets (tab-expanded, grapheme-aware). Must make a
	// copy so we don't mutate the caller's slice (e.g. LSPManager cache).
	converted := make([]LSPDiagnostic, len(diags))
	copy(converted, diags)
	for i, d := range converted {
		rawLine, err := e.buf.Line(d.Line)
		if err == nil {
			converted[i].Col = UTF16ColToVisualCol(string(rawLine), d.Col, tabWidth)
			converted[i].EndCol = UTF16ColToVisualCol(string(rawLine), d.EndCol, tabWidth)
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

	if e.buffer != nil {
		e.buffer.SetDiagnostics(diags)
	}
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

// DiagnosticsAt returns all LSPDiagnostics covering the given
// position. Public version of diagnosticsAt for use by external
// callers (e.g. the right-click context menu).
func (e *Editor) DiagnosticsAt(line, col int) []LSPDiagnostic {
	return e.diagnosticsAt(line, col)
}

// Diagnostics returns a snapshot of every LSP diagnostic for the
// current file, sorted by line then column. Used by navigation
// features (F8/Shift+F8, status-bar "Goto first error") that need
// to walk the diagnostic list in order. The returned slice is a
// copy — modifying it does not affect the editor.
func (e *Editor) Diagnostics() []LSPDiagnostic {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	out := make([]LSPDiagnostic, len(e.diagnostics))
	copy(out, e.diagnostics)
	return out
}

// SetSelection programmatically creates a selection from
// (startLine, startCol) to (endLine, endCol). Use this to highlight
// a diagnostic range after navigating to it (e.g. "Goto first
// error"). Coordinates are clamped to non-negative values; a
// backwards single-line range is normalized. Setting a selection is
// purely visual — it does not move the cursor.
func (e *Editor) SetSelection(startLine, startCol, endLine, endCol int) {
	if startLine < 0 {
		startLine = 0
	}
	if startCol < 0 {
		startCol = 0
	}
	if endLine < startLine {
		endLine = startLine
	}
	if endCol < 0 {
		endCol = 0
	}
	start := Position{Line: startLine, Col: startCol}
	end := Position{Line: endLine, Col: endCol}
	if end.Col < start.Col && end.Line == start.Line {
		// Normalize a backwards single-line range.
		start, end = end, start
	}
	e.selAnchor = start
	e.selEnd = end
	e.selecting = true
}

// NextError returns the first diagnostic in the current file with
// line strictly greater than fromLine (or with line == fromLine and
// col > fromCol). Returns nil if there is no such error. The
// returned diagnostic is a pointer into the editor's diagnostic
// slice — do not retain across edits.
func (e *Editor) NextError(fromLine, fromCol int) *LSPDiagnostic {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	for i := range e.diagnostics {
		d := &e.diagnostics[i]
		if d.Severity != DiagError {
			continue
		}
		if d.Line > fromLine || (d.Line == fromLine && d.Col > fromCol) {
			return d
		}
	}
	return nil
}

// PrevError returns the last diagnostic in the current file with
// line strictly less than fromLine (or with line == fromLine and
// col < fromCol). Returns nil if there is no such error.
func (e *Editor) PrevError(fromLine, fromCol int) *LSPDiagnostic {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	for i := len(e.diagnostics) - 1; i >= 0; i-- {
		d := &e.diagnostics[i]
		if d.Severity != DiagError {
			continue
		}
		if d.Line < fromLine || (d.Line == fromLine && d.Col < fromCol) {
			return d
		}
	}
	return nil
}

// FirstError returns the first error diagnostic in the current
// file (sorted by line/col), or nil if there are none. Used by
// project-wide "Goto first error" when the file is already open.
func (e *Editor) FirstError() *LSPDiagnostic {
	e.diagMu.RLock()
	defer e.diagMu.RUnlock()
	for i := range e.diagnostics {
		d := &e.diagnostics[i]
		if d.Severity == DiagError {
			return d
		}
	}
	return nil
}

// DeleteSelection removes the current selection (if any) and
// returns true if anything was deleted. The deletion is recorded as
// a single undo step and the cursor moves to the selection start.
func (e *Editor) DeleteSelection() bool {
	if !e.HasSelection() {
		return false
	}
	sLine, sCol, eLine, eCol := e.SelectionRange()
	start := Position{Line: sLine, Col: sCol}
	end := Position{Line: eLine, Col: eCol}
	cursorBefore := e.cursor
	text := e.buf.GetText(start, end)
	_, _ = e.buf.Delete(start, end)
	e.recordChange(Change{
		Type:         ActDelete,
		Text:         []byte(text),
		StartPos:     start,
		EndPos:       end,
		CursorBefore: cursorBefore,
	})
	e.cursor = start
	e.modified = true
	if e.buffer != nil {
		e.buffer.MarkDirty()
	}
	e.invalidateEditorCaches()
	e.ClearSelection()
	e.notifyContentChanged()
	return true
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
func (e *Editor) SetHover(screenX, screenY, line, col int, severity int, msgs []string) {
	e.hoverScreenX = screenX
	e.hoverScreenY = screenY
	e.hoverLine = line
	e.hoverCol = col
	e.hoverSeverity = severity
	e.hoverMessages = msgs
}

// ClearHover clears the hover diagnostic state.
func (e *Editor) ClearHover() {
	e.hoverMessages = nil
	e.hoverSeverity = 0
}

// HoverState returns the current hover diagnostics state.
func (e *Editor) HoverState() HoverState {
	return HoverState{
		Messages: e.hoverMessages,
		Severity: e.hoverSeverity,
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
	highestSev := 0
	for _, d := range diags {
		msgs = append(msgs, d.Message)
		if d.Severity > highestSev {
			highestSev = d.Severity
		}
	}
	e.SetHover(screenX, screenY, pos.Line, pos.Col, highestSev, msgs)
}

// refreshCursorHover updates hover diagnostics for the keyboard cursor.
func (e *Editor) RefreshCursorHover(sidebarW, chatAreaTop int) {
	diags := e.diagnosticsAt(e.cursor.Line, e.cursor.Col)
	if len(diags) == 0 {
		e.ClearHover()
		return
	}
	msgs := make([]string, 0, len(diags))
	highestSev := 0
	for _, d := range diags {
		msgs = append(msgs, d.Message)
		if d.Severity > highestSev {
			highestSev = d.Severity
		}
	}
	screenX, screenY := e.CursorScreenPosition(sidebarW, chatAreaTop)
	e.SetHover(screenX, screenY, e.cursor.Line, e.cursor.Col, highestSev, msgs)
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
	return sidebarW + e.PadL + e.gutterWidth() + colOff, chatAreaTop + lineOff
}

// SetCursorPosition jumps the cursor to (line, col) clamping to the buffer's
// valid range. Use this from LSP navigations ("go to definition", "go to
// reference", "find symbol") so the cursor lands precisely where the server
// pointed.
func (e *Editor) SetCursorPosition(line, col int) {
	lc := e.buf.LineCount()
	if lc <= 0 {
		e.cursor.Line = 0
		e.cursor.Col = 0
		return
	}
	if line < 0 {
		line = 0
	}
	if line >= lc {
		line = lc - 1
	}
	e.cursor.Line = line
	lineBytes, _ := e.buf.Line(line)
	maxCol := LineVisualWidth(string(lineBytes), tabWidth)
	if col < 0 {
		col = 0
	}
	if col > maxCol {
		col = maxCol
	}
	e.cursor.Col = col
	e.invalidateEditorCaches()
}

// ScrollToCursor adjusts the scroll offsets so the cursor is visible. After
// a SetCursorPosition this brings the line into view.
func (e *Editor) ScrollToCursor() {
	e.ensureCursorVisible()
	e.invalidateEditorCaches()
}

// LineTextFromPath returns the trimmed content of the file at the given path.
// Used by the references popup to show a preview of the matching line. The
// path is read fresh from disk; for the active editor use LineText() instead.
func (e *Editor) LineTextFromPath(filePath string, lineIdx int) (string, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	if lineIdx < 0 || lineIdx >= len(lines) {
		return "", fmt.Errorf("line %d out of range", lineIdx)
	}
	return strings.TrimRight(lines[lineIdx], "\r"), nil
}
