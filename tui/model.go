package tui

import (
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
	messages  []ChatMessage
	inputBuf  []rune
	cursorPos int

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

	// Error bar
	lastError string
	errorTime time.Time

	// Input channel — written via Cmd so it's safe.
	inputCh chan<- string

	// Inline settings
	settingsActive bool
	inlineForm     inlineSettings
	onSaveSettings func(Settings)
	fetchModelsFn  func(providerID, baseURL, apiKey string) tea.Cmd

	// Confirmation form
	confirmForm     confirmForm
	OnConfirmResult func(ConfirmResult)

	// Undo / file change commands
	OnUndoCmd func(cmd string)

	// Slash command state
	slashActive   bool
	slashIdx      int
	slashFilter   string
	slashFiltered []slashCmd

	// Settings state
	lastSettings Settings

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
func NewModel(modelName string, username string, inputCh chan<- string, initial Settings, onSave func(Settings), fetchFn func(providerID, baseURL, apiKey string) tea.Cmd) *Model {
	return &Model{
		modelName:      modelName,
		username:       username,
		inputCh:        inputCh,
		startTime:      time.Now(),
		width:          80,
		height:         24,
		inlineForm:     newInlineSettings(initial),
		confirmForm:    newConfirmForm(),
		onSaveSettings: onSave,
		fetchModelsFn:  fetchFn,
		historyIdx:     -1,
		showHint:       true,
	}
}

// Md renderer — created dynamically per render with correct width.
func (m *Model) makeMdRenderer() *glamour.TermRenderer {
	w := m.width - padLR*2 - 16
	if w < 20 {
		w = 20
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

	r, _ := glamour.NewTermRenderer(
		glamour.WithStyles(style),
		glamour.WithWordWrap(w),
	)
	return r
}

func uintPtr(u uint) *uint {
	return &u
}

func strPtr(s string) *string {
	return &s
}

// setInput sets the input buffer from a string and places cursor at end.
func (m *Model) setInput(s string) {
	m.inputBuf = []rune(s)
	m.cursorPos = len(m.inputBuf)
}

// inputLines splits the input buffer into lines (without newline characters).
func (m *Model) inputLines() [][]rune {
	var lines [][]rune
	var current []rune
	for _, r := range m.inputBuf {
		if r == '\n' {
			lines = append(lines, current)
			current = nil
		} else {
			current = append(current, r)
		}
	}
	lines = append(lines, current)
	return lines
}

// cursorLineCol returns the line index and column of the cursor position.
func (m *Model) cursorLineCol() (line, col int) {
	for i := 0; i < m.cursorPos && i < len(m.inputBuf); i++ {
		if m.inputBuf[i] == '\n' {
			line++
			col = 0
		} else {
			col++
		}
	}
	return
}

// cursorLineStart returns the absolute position of the start of the current line.
func (m *Model) cursorLineStart() int {
	for i := m.cursorPos - 1; i >= 0; i-- {
		if m.inputBuf[i] == '\n' {
			return i + 1
		}
	}
	return 0
}

// cursorLineEnd returns the absolute position of the end of the current line
// (just after the last character, before any newline).
func (m *Model) cursorLineEnd() int {
	for i := m.cursorPos; i < len(m.inputBuf); i++ {
		if m.inputBuf[i] == '\n' {
			return i
		}
	}
	return len(m.inputBuf)
}

// cursorPrevWord returns the position of the previous word boundary.
func (m *Model) cursorPrevWord() int {
	pos := m.cursorPos
	for pos > 0 && unicode.IsSpace(m.inputBuf[pos-1]) {
		pos--
	}
	for pos > 0 && !unicode.IsSpace(m.inputBuf[pos-1]) {
		pos--
	}
	return pos
}

// cursorNextWord returns the position of the next word boundary.
func (m *Model) cursorNextWord() int {
	pos := m.cursorPos
	for pos < len(m.inputBuf) && !unicode.IsSpace(m.inputBuf[pos]) {
		pos++
	}
	for pos < len(m.inputBuf) && unicode.IsSpace(m.inputBuf[pos]) {
		pos++
	}
	return pos
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
