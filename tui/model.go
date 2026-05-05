package tui

import (
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// Model is the Bubble Tea model for the chat TUI.
type Model struct {
	// Config
	modelName string
	username  string

	// Chat state
	messages   []ChatMessage
	inputBuf   [][]rune
	cursorLine int
	cursorCol  int

	// Agent state
	thinking  bool
	blocking  bool // waiting for user to resolve a blocking node
	spinFrame int
	streaming bool // true while receiving stream tokens

	// Status line — compact activity indicator shown during agent work
	statusLine      string
	statusPhase     string
	tokenPrompt     int
	tokenCompletion int
	tokenCached     int
	sessionCost     float64

	// Scroll: 0 = bottom, >0 = scrolled up by N lines
	chatOffset     int
	userScrolled   bool      // true when user manually scrolls up during streaming
	scrollVelocity float64   // accumulated scroll momentum
	lastScrollTime time.Time // for velocity-based acceleration

	// Display
	width  int
	height int

	// Timing
	startTime      time.Time
	lastCancelTime time.Time
	lastInputTime  time.Time // last user input key press, for cursor solid-while-typing
	lastVisualCol  int // preserved visual column for vertical navigation in wrapped input

	// Error bar
	lastError string
	errorTime time.Time

	// Input channel — written via Cmd so it's safe.
	inputCh chan<- string

	// Cancel channel — signalled when user cancels the current agent operation.
	cancelCh chan<- struct{}

	// Inline settings
	settingsActive      bool
	inlineForm          inlineSettings
	onSaveSettings      func(Settings)
	fetchModelsFn       func(providerID, baseURL, apiKey string) tea.Cmd
	getProviderConfigFn func(providerID string) ProviderConfig

	// Confirmation form
	confirmForm     confirmForm
	OnConfirmResult func(ConfirmResult)

	// Undo / file change commands
	OnUndoCmd func(cmd string)

	// Slash command state
	slashActive      bool
	slashIdx         int
	slashListOffset  int
	slashFilter      string
	slashFiltered    []slashCmd

	// Settings state
	lastSettings Settings

	// Cached glamour renderer (recreated on width change)
	mdRenderer    *glamour.TermRenderer
	rendererWidth int
	cachedWidth   int

	// Prompt history
	history       []string // deduplicated, newest last
	historyIdx    int      // -1 = not browsing, 0 = newest, len-1 = oldest
	historyDraft  string   // preserves input when entering history
	historyPrefix string   // for prefix-filtered navigation (bash-style)

	// Hint bar
	showHint bool

	// Text selection in chat area
	selecting     bool
	selAnchorLine int
	selAnchorCol  int
	selEndLine    int
	selEndCol     int
	selMouseY     int
	selAutoScroll bool

	// Text selection in input area
	inputSelecting bool
	inputSelAnchor int
	inputSelEnd    int

	// Cached rendered lines (set during View, used by selection)
	cachedAllLines []string

	// Info message (gray, shown in status area)
	infoMsg     string
	infoMsgTime time.Time

	quit bool
}

// NewModel creates a new TUI model.
func NewModel(modelName string, username string, inputCh chan<- string, cancelCh chan<- struct{}, initial Settings, onSave func(Settings), fetchFn func(providerID, baseURL, apiKey string) tea.Cmd, getProviderConfigFn func(providerID string) ProviderConfig) *Model {
	return &Model{
		modelName:           modelName,
		username:            username,
		inputCh:             inputCh,
		cancelCh:            cancelCh,
		inputBuf:            [][]rune{{}},
		startTime:           time.Now(),
		lastVisualCol:       -1,
		width:               80,
		height:              24,
		inlineForm:          newInlineSettings(initial, getProviderConfigFn),
		confirmForm:         newConfirmForm(),
		onSaveSettings:      onSave,
		fetchModelsFn:       fetchFn,
		getProviderConfigFn: getProviderConfigFn,
		lastSettings:        initial,
		historyIdx:          -1,
		showHint:            true,
	}
}

// Md renderer — cached and reused; recreated only on width change.
func (m *Model) makeMdRenderer() *glamour.TermRenderer {
	w := m.width - padLR*2 - 16
	if w < 20 {
		w = 20
	}
	if m.mdRenderer != nil && m.rendererWidth == w {
		return m.mdRenderer
	}
	style := styles.DarkStyleConfig
	style.Document.Margin = uintPtr(0)
	style.CodeBlock.Margin = uintPtr(0)
	style.CodeBlock.Chroma = &ansi.Chroma{
		Text:                ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		Error:               ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		Comment:             ansi.StylePrimitive{Color: strPtr("#8b949e")},
		CommentPreproc:      ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		Keyword:             ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordReserved:     ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordNamespace:    ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		KeywordType:         ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		Operator:            ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		Punctuation:         ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		Name:                ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		NameBuiltin:         ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameTag:             ansi.StylePrimitive{Color: strPtr("#7ee787")},
		NameAttribute:       ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameClass:           ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameConstant:        ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		NameDecorator:       ansi.StylePrimitive{Color: strPtr("#d2a8ff")},
		NameFunction:        ansi.StylePrimitive{Color: strPtr("#d2a8ff")},
		LiteralNumber:       ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		LiteralString:       ansi.StylePrimitive{Color: strPtr("#a5d6ff")},
		LiteralStringEscape: ansi.StylePrimitive{Color: strPtr("#79c0ff")},
		GenericDeleted:      ansi.StylePrimitive{Color: strPtr("#ff7b72")},
		GenericInserted:     ansi.StylePrimitive{Color: strPtr("#7ee787")},
		GenericStrong:       ansi.StylePrimitive{Color: strPtr("#e6edf3")},
		GenericSubheading:   ansi.StylePrimitive{Color: strPtr("#e6edf3")},
	}
	style.CodeBlock.StylePrimitive.Color = strPtr("#e6edf3")
	style.CodeBlock.StylePrimitive.BackgroundColor = strPtr("#161b22")
	style.CodeBlock.BlockPrefix = ""

	style.Paragraph.Margin = uintPtr(0)
	style.Heading.Margin = uintPtr(0)
	style.List.Margin = uintPtr(0)
	style.Table.Margin = uintPtr(0)

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(w),
	)
	if err == nil {
		m.mdRenderer = r
		m.rendererWidth = w
		return r
	}
	return nil
}

func uintPtr(u uint) *uint {
	return &u
}

func strPtr(s string) *string {
	return &s
}

// inputText returns the full input as a flattened string with \n separators.
func (m *Model) inputText() string {
	var parts []string
	for _, line := range m.inputBuf {
		parts = append(parts, string(line))
	}
	return strings.Join(parts, "\n")
}

// inputLen returns the number of runes in the virtual flat buffer (including \n separators).
func (m *Model) inputLen() int {
	n := 0
	for _, line := range m.inputBuf {
		n += len(line)
	}
	if len(m.inputBuf) > 0 {
		n += len(m.inputBuf) - 1
	}
	return n
}

// setInput sets the input buffer from a string and places cursor at end.
func (m *Model) setInput(s string) {
	lines := strings.Split(s, "\n")
	m.inputBuf = make([][]rune, len(lines))
	for i, l := range lines {
		m.inputBuf[i] = []rune(l)
	}
	m.cursorLine = len(m.inputBuf) - 1
	if m.cursorLine < 0 {
		m.cursorLine = 0
		m.inputBuf = [][]rune{{}}
	}
	m.cursorCol = len(m.inputBuf[m.cursorLine])
}

// clearInput resets the input buffer.
func (m *Model) clearInput() {
	m.inputBuf = [][]rune{{}}
	m.cursorLine = 0
	m.cursorCol = 0
}

// inputLines returns the lines of the input buffer.
func (m *Model) inputLines() [][]rune {
	if len(m.inputBuf) == 0 {
		return [][]rune{{}}
	}
	return m.inputBuf
}

// cursorLineCol returns the line index and column of the cursor position.
func (m *Model) cursorLineCol() (line, col int) {
	return m.cursorLine, m.cursorCol
}

// flatToLineCol converts a flat buffer position to (line, col).
func (m *Model) flatToLineCol(pos int) (line, col int) {
	if pos <= 0 {
		return 0, 0
	}
	remaining := pos
	for li, lineRunes := range m.inputBuf {
		if remaining <= len(lineRunes) {
			return li, remaining
		}
		remaining -= len(lineRunes) + 1
	}
	if len(m.inputBuf) == 0 {
		return 0, 0
	}
	last := len(m.inputBuf) - 1
	return last, len(m.inputBuf[last])
}

// lineColToFlat converts (line, col) to a flat buffer position.
func (m *Model) lineColToFlat(line, col int) int {
	if line < 0 {
		line = 0
	}
	if line >= len(m.inputBuf) {
		line = len(m.inputBuf) - 1
	}
	pos := 0
	for i := 0; i < line; i++ {
		pos += len(m.inputBuf[i]) + 1
	}
	if col > len(m.inputBuf[line]) {
		col = len(m.inputBuf[line])
	}
	return pos + col
}

// cursorFlat returns the flat position of the cursor.
func (m *Model) cursorFlat() int {
	return m.lineColToFlat(m.cursorLine, m.cursorCol)
}

// setCursorFlat sets cursorLine/cursorCol from a flat position.
func (m *Model) setCursorFlat(pos int) {
	m.cursorLine, m.cursorCol = m.flatToLineCol(pos)
}

// cursorLineStart returns the flat position of the start of the current line.
func (m *Model) cursorLineStart() int {
	return m.lineColToFlat(m.cursorLine, 0)
}

// cursorLineEnd returns the flat position of the end of the current line
// (just after the last character, before any newline).
func (m *Model) cursorLineEnd() int {
	return m.lineColToFlat(m.cursorLine, len(m.inputBuf[m.cursorLine]))
}

// cursorPrevWord returns the position of the previous word boundary.
func (m *Model) cursorPrevWord() int {
	line, col := m.cursorLine, m.cursorCol
	if col == 0 {
		if line == 0 {
			return 0
		}
		line--
		col = len(m.inputBuf[line])
	}
	pos := m.lineColToFlat(line, col)
	lineRunes := m.inputBuf[line]
	for col > 0 && unicode.IsSpace(lineRunes[col-1]) {
		col--
		pos--
	}
	for col > 0 && !unicode.IsSpace(lineRunes[col-1]) {
		col--
		pos--
	}
	return pos
}

// cursorNextWord returns the position of the next word boundary.
func (m *Model) cursorNextWord() int {
	line, col := m.cursorLine, m.cursorCol
	lineLen := len(m.inputBuf[line])
	flatEnd := m.lineColToFlat(len(m.inputBuf)-1, len(m.inputBuf[len(m.inputBuf)-1]))
	pos := m.lineColToFlat(line, col)
	lineRunes := m.inputBuf[line]
	for col < lineLen && !unicode.IsSpace(lineRunes[col]) {
		col++
		pos++
	}
	for col < lineLen && unicode.IsSpace(lineRunes[col]) {
		col++
		pos++
	}
	if pos > flatEnd {
		pos = flatEnd
	}
	return pos
}

// splitLineAt splits the line at the cursor position into two lines.
func (m *Model) splitLineAt(line, col int) {
	if line < 0 || line >= len(m.inputBuf) {
		return
	}
	runes := m.inputBuf[line]
	left := make([]rune, col)
	copy(left, runes[:col])
	right := make([]rune, len(runes)-col)
	copy(right, runes[col:])

	newLines := make([][]rune, len(m.inputBuf)+1)
	copy(newLines, m.inputBuf[:line])
	newLines[line] = left
	newLines[line+1] = right
	copy(newLines[line+2:], m.inputBuf[line+1:])
	m.inputBuf = newLines
}

// insertRuneAt inserts a rune at the given flat position.
func (m *Model) insertRuneAt(pos int, r rune) {
	if len(m.inputBuf) == 0 {
		m.inputBuf = [][]rune{{r}}
		return
	}
	line, col := m.flatToLineCol(pos)
	if line >= len(m.inputBuf) {
		line = len(m.inputBuf) - 1
	}
	newLine := make([]rune, len(m.inputBuf[line])+1)
	copy(newLine, m.inputBuf[line][:col])
	newLine[col] = r
	copy(newLine[col+1:], m.inputBuf[line][col:])
	m.inputBuf[line] = newLine
}

// deleteRuneAt removes the rune BEFORE the given flat position (backspace).
func (m *Model) deleteRuneAt(pos int) {
	if pos <= 0 {
		return
	}
	line, col := m.flatToLineCol(pos)
	if col > 0 {
		newLine := make([]rune, len(m.inputBuf[line])-1)
		copy(newLine, m.inputBuf[line][:col-1])
		copy(newLine[col-1:], m.inputBuf[line][col:])
		m.inputBuf[line] = newLine
	} else if line > 0 {
		prevLen := len(m.inputBuf[line-1])
		merged := make([]rune, prevLen+len(m.inputBuf[line]))
		copy(merged, m.inputBuf[line-1])
		copy(merged[prevLen:], m.inputBuf[line])
		m.inputBuf = append(m.inputBuf[:line-1], m.inputBuf[line:]...)
		m.inputBuf[line-1] = merged
	}
}

// deleteForwardAt removes the rune AT the given flat position (delete key).
func (m *Model) deleteForwardAt(pos int) {
	line, col := m.flatToLineCol(pos)
	if line >= len(m.inputBuf) {
		return
	}
	lineLen := len(m.inputBuf[line])
	if col < lineLen {
		newLine := make([]rune, lineLen-1)
		copy(newLine, m.inputBuf[line][:col])
		copy(newLine[col:], m.inputBuf[line][col+1:])
		m.inputBuf[line] = newLine
	} else if line+1 < len(m.inputBuf) {
		merged := make([]rune, lineLen+len(m.inputBuf[line+1]))
		copy(merged, m.inputBuf[line])
		copy(merged[lineLen:], m.inputBuf[line+1])
		m.inputBuf = append(m.inputBuf[:line], m.inputBuf[line+1:]...)
		m.inputBuf[line] = merged
	}
}

// deleteRange removes runes from start to end (flat positions).
func (m *Model) deleteRange(start, end int) {
	if start >= end {
		return
	}
	startLine, startCol := m.flatToLineCol(start)
	endLine, endCol := m.flatToLineCol(end)

	if startLine == endLine {
		newLine := make([]rune, len(m.inputBuf[startLine])-(endCol-startCol))
		copy(newLine, m.inputBuf[startLine][:startCol])
		copy(newLine[startCol:], m.inputBuf[startLine][endCol:])
		m.inputBuf[startLine] = newLine
		return
	}

	var result [][]rune
	result = append(result, m.inputBuf[:startLine]...)
	merged := make([]rune, startCol+len(m.inputBuf[endLine])-endCol)
	copy(merged, m.inputBuf[startLine][:startCol])
	copy(merged[startCol:], m.inputBuf[endLine][endCol:])
	result = append(result, merged)
	result = append(result, m.inputBuf[endLine+1:]...)
	m.inputBuf = result
}

// subRunes returns the rune slice for a flat range [start, end).
func (m *Model) subRunes(start, end int) []rune {
	if start >= end {
		return nil
	}
	startLine, startCol := m.flatToLineCol(start)
	endLine, endCol := m.flatToLineCol(end)

	if startLine == endLine {
		r := make([]rune, endCol-startCol)
		copy(r, m.inputBuf[startLine][startCol:endCol])
		return r
	}

	var result []rune
	result = append(result, m.inputBuf[startLine][startCol:]...)
	for li := startLine + 1; li < endLine; li++ {
		result = append(result, '\n')
		result = append(result, m.inputBuf[li]...)
	}
	result = append(result, '\n')
	result = append(result, m.inputBuf[endLine][:endCol]...)
	return result
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.EnterAltScreen)
}

// SetProviderPresets configures the available provider presets for the settings panel.
func (m *Model) SetProviderPresets(presets []ProviderPreset) {
	m.inlineForm.providerPresets = presets
}

// SetHistory restores a previously persisted prompt history.
func (m *Model) SetHistory(history []string) {
	m.history = history
}

// History returns the current prompt history for persistence.
func (m *Model) History() []string {
	return m.history
}

// OnHistorySave is called when the model wants to persist history.
var OnHistorySave func([]string)

// IsQuitting returns true after the user has requested quit.
func (m *Model) IsQuitting() bool { return m.quit }
