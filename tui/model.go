package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
)

// Model is the Bubble Tea model for the chat TUI.
type Model struct {
	// Config
	modelName string

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

	quit bool
}

// NewModel creates a new TUI model.
func NewModel(modelName string, inputCh chan<- string, initial Settings, onSave func(Settings), fetchFn func(providerID, baseURL, apiKey string) tea.Cmd) *Model {
	return &Model{
		modelName:      modelName,
		inputCh:        inputCh,
		startTime:      time.Now(),
		width:          80,
		height:         24,
		inlineForm:     newInlineSettings(initial),
		onSaveSettings: onSave,
		fetchModelsFn:  fetchFn,
		historyIdx:     -1,
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
