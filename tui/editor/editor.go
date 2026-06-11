package editor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

	lsp          *LSPIntegration
	lspCtx       context.Context
	lspCancel    context.CancelFunc
	lspConnected bool
	lspStatus    LSPStatus
	lspError     string

	diagMu      sync.RWMutex
	diagnostics []LSPDiagnostic
	diagByLine  map[int][]diagSpan
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
	e.startLSP()

	return nil
}

func (e *Editor) Close() {
	e.stopLSP()
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
	e.syncLSP()
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
func (e *Editor) Modified() bool     { return e.modified }
func (e *Editor) Value() string      { return string(e.buf.Bytes()) }
func (e *Editor) CursorLine() int    { return e.cursor.Line }
func (e *Editor) CursorCol() int     { return e.cursor.Col }
func (e *Editor) LineCount() int     { return e.buf.LineCount() }
func (e *Editor) LSPConnected() bool { return e.lspConnected }
func (e *Editor) LSPStatus() LSPStatus { return e.lspStatus }
func (e *Editor) LSPError() string     { return e.lspError }
func (e *Editor) resetLSPStatus() {
	e.lspStatus = LSPIdle
	e.lspError = ""
}

func (e *Editor) startLSP() {
	e.lspConnected = false
	e.resetLSPStatus()
	ext := strings.ToLower(filepath.Ext(e.filePath))
	cfg, ok := lspServers[ext]
	if !ok {
		e.lspError = "no LSP server configured for " + ext
		return
	}
	e.lspStatus = LSPStarting
	if _, err := exec.LookPath(cfg.cmd); err != nil {
		e.lspStatus = LSPFailed
		e.lspError = cfg.cmd + " not found in PATH"
		return
	}
	e.lspCtx, e.lspCancel = context.WithCancel(context.Background())
	rootURI := e.lspRootURI()
	e.lsp = NewLSPIntegration(cfg.cmd, cfg.args, rootURI)
	e.lsp.onDiagnostics = e.setDiagnostics
	if err := e.lsp.Start(e.lspCtx); err != nil {
		e.lsp = nil
		e.lspCancel()
		e.lspStatus = LSPFailed
		e.lspError = cfg.cmd + " failed to start: " + err.Error()
		return
	}
	e.lspStatus = LSPConnected
	e.lspConnected = true
	langID := e.lspLanguageID(ext)
	e.lsp.OpenFile(e.lspCtx, e.filePath, langID, string(e.buf.Bytes()))
	e.syncLSP()
}

func (e *Editor) stopLSP() {
	if e.lsp == nil {
		return
	}
	e.lsp.CloseFile(e.lspCtx)
	e.lsp.Stop()
	e.lspCancel()
	e.lsp = nil
}

func (e *Editor) syncLSP() {
	if e.lsp == nil {
		return
	}
	e.lsp.UpdateFile(e.lspCtx, string(e.buf.Bytes()))
}

func (e *Editor) setDiagnostics(diags []LSPDiagnostic) {
	e.diagMu.Lock()
	e.diagnostics = diags
	byLine := make(map[int][]diagSpan, len(diags))
	for _, d := range diags {
		byLine[d.Line] = append(byLine[d.Line], diagSpan{
			startCol: d.Col,
			endCol:   d.EndCol,
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

func (e *Editor) lspRootURI() string {
	dir := filepath.Dir(e.filePath)
	ext := strings.ToLower(filepath.Ext(e.filePath))
	markers := []string{"go.mod", "package.json", "Cargo.toml", "pyproject.toml", "composer.json"}
	preferred := map[string]string{
		".go": "go.mod",
		".rs": "Cargo.toml",
		".py": "pyproject.toml",
	}
	if p, ok := preferred[ext]; ok {
		markers = []string{p}
	}
	d := dir
	for {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(d, m)); err == nil {
				return "file://" + d
			}
		}
		parent := filepath.Dir(d)
		if parent == d {
			return "file://" + dir
		}
		d = parent
	}
}

func (e *Editor) lspLanguageID(ext string) string {
	switch ext {
	case ".go":
		return "go"
	case ".rs":
		return "rust"
	case ".py":
		return "python"
	case ".ts", ".tsx":
		return "typescript"
	case ".js", ".jsx":
		return "javascript"
	case ".css":
		return "css"
	case ".json":
		return "json"
	default:
		return ""
	}
}
