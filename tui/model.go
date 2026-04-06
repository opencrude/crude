package tui

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/charmbracelet/lipgloss"
	"github.com/mattn/go-runewidth"
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

	// Status line — compact activity indicator shown during agent work
	statusLine      string
	statusPhase     string
	tokenPrompt     int
	tokenCompletion int

	// Scroll: 0 = bottom, >0 = scrolled up by N lines
	chatOffset int

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

type slashCmd struct {
	name  string
	label string
	desc  string
	icon  string
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

var availableCmds = []slashCmd{
	{name: "/settings", label: "settings", desc: "Open general settings"},
	{name: "/provider", label: "provider", desc: "Select provider"},
	{name: "/model", label: "model", desc: "Select model"},
	{name: "/embed-model", label: "embed model", desc: "Select embedding model"},
	{name: "/api-key", label: "api key", desc: "Set API key"},
	{name: "/backend", label: "backend", desc: "Toggle local / ssh"},
	{name: "/help", label: "help", desc: "Show available commands"},
}

func (m *Model) updateSlashFilter() {
	m.slashFiltered = nil
	m.slashIdx = 0
	s := strings.ToLower(m.slashFilter)
	for _, c := range availableCmds {
		if strings.HasPrefix(c.name, s) || strings.Contains(strings.ToLower(c.desc), s) {
			m.slashFiltered = append(m.slashFiltered, c)
		}
	}
}

const maxHistorySize = 500

// browseHistoryUp navigates to older prompts (like ↑ in bash/zsh).
// Supports prefix filtering: if input is non-empty, only cycles through
// entries matching the current prefix.
func (m *Model) browseHistoryUp() {
	if len(m.history) == 0 {
		return
	}
	if m.historyIdx == -1 {
		// First time entering history — save draft and capture prefix
		m.historyDraft = string(m.inputBuf)
		m.historyPrefix = strings.TrimSpace(m.historyDraft)
		m.historyIdx = m.findHistoryIndex(-1)
		if m.historyIdx == -1 {
			return
		}
	} else {
		m.historyIdx = m.findHistoryIndex(m.historyIdx)
		if m.historyIdx == -1 {
			return
		}
	}
	m.setInput(m.history[m.historyIdx])
}

// browseHistoryDown navigates to newer prompts.
// When reaching the end, restores the original draft input.
func (m *Model) browseHistoryDown() {
	if m.historyIdx == -1 {
		return
	}
	next := m.findHistoryDown(m.historyIdx)
	if next == -1 {
		// No more entries — restore draft
		m.resetHistory()
		return
	}
	m.historyIdx = next
	m.setInput(m.history[m.historyIdx])
}

// findHistoryIndex returns the index of the next older history entry
// matching the current prefix. startIdx: -1 means start from newest,
// otherwise search older than startIdx.
func (m *Model) findHistoryIndex(startIdx int) int {
	start := len(m.history) - 1
	if startIdx != -1 {
		start = startIdx - 1
	}
	for i := start; i >= 0; i-- {
		if m.historyPrefix == "" || strings.HasPrefix(m.history[i], m.historyPrefix) {
			return i
		}
	}
	return -1
}

// findHistoryDown returns the index of the next newer history entry
// matching the current prefix. Returns -1 if no more entries (should restore draft).
func (m *Model) findHistoryDown(startIdx int) int {
	for i := startIdx + 1; i < len(m.history); i++ {
		if m.historyPrefix == "" || strings.HasPrefix(m.history[i], m.historyPrefix) {
			return i
		}
	}
	return -1
}

// resetHistory exits history browsing and restores the draft input.
func (m *Model) resetHistory() {
	if m.historyIdx != -1 {
		m.setInput(m.historyDraft)
		m.historyDraft = ""
		m.historyPrefix = ""
		m.historyIdx = -1
	}
}

// setInput sets the input buffer from a string and places cursor at end.
func (m *Model) setInput(s string) {
	m.inputBuf = []rune(s)
	m.cursorPos = len(m.inputBuf)
}

// appendHistory adds a prompt to history, deduplicating consecutive entries
// and trimming to max size.
func (m *Model) appendHistory(text string) {
	if text == "" {
		return
	}
	// Don't add duplicates of the most recent entry
	if len(m.history) > 0 && m.history[len(m.history)-1] == text {
		return
	}
	m.history = append(m.history, text)
	// Trim oldest entries if over capacity
	if len(m.history) > maxHistorySize {
		m.history = m.history[len(m.history)-maxHistorySize:]
	}
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

func (m *Model) executeSlashCommand(text string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(text)
	cmd := parts[0]

	m.inputBuf = nil
	m.cursorPos = 0
	m.messages = append(m.messages, ChatMessage{Role: RoleUser, Content: text, Time: time.Now()})
	m.chatOffset = 0

	switch cmd {
	case "/settings":
		m.settingsActive = true
	case "/provider":
		m.settingsActive = true
		m.inlineForm.focused = sfProvider
		m.inlineForm.isProviderSelect = true
		m.inlineForm.providerListIdx = 0
		for i, preset := range m.inlineForm.providerPresets {
			if preset.ID == m.inlineForm.providerID {
				m.inlineForm.providerListIdx = i
				break
			}
		}
	case "/model":
		m.settingsActive = true
		m.inlineForm.focused = sfModel
		if m.inlineForm.providerID == "ollama" || m.inlineForm.providerID == "custom" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("Type model name manually for %s.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else if string(m.inlineForm.fields[sfAPIKey].value) == "" && m.inlineForm.providerID != "openrouter" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("No API key provided for %s. Type model name manually.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else {
			m.inlineForm.isModelSelect = true
			if len(m.inlineForm.availableModels) == 0 && !m.inlineForm.fetchingModels {
				m.inlineForm.fetchError = ""
				m.inlineForm.fetchingModels = true
				current := fetchModelsMsg{
					ProviderID: m.inlineForm.providerID,
					BaseURL:    string(m.inlineForm.fields[sfBaseURL].value),
					APIKey:     string(m.inlineForm.fields[sfAPIKey].value),
				}
				return m, func() tea.Msg { return current }
			}
			m.inlineForm.updateModelFilter()
		}
	case "/embed-model":
		m.settingsActive = true
		m.inlineForm.focused = sfEmbedModel
		if m.inlineForm.providerID == "ollama" || m.inlineForm.providerID == "custom" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("Type embedding model name manually for %s.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else if string(m.inlineForm.fields[sfAPIKey].value) == "" && m.inlineForm.providerID != "openrouter" {
			m.inlineForm.editingField = true
			m.lastError = fmt.Sprintf("No API key provided for %s. Type embedding model name manually.", m.inlineForm.providerID)
			m.errorTime = time.Now()
		} else {
			m.inlineForm.isEmbedModelSelect = true
			if len(m.inlineForm.availableEmbedModels) == 0 && !m.inlineForm.fetchingEmbedModels {
				m.inlineForm.fetchEmbedError = ""
				m.inlineForm.fetchingEmbedModels = true
				current := fetchModelsMsg{
					ProviderID: m.inlineForm.providerID,
					BaseURL:    string(m.inlineForm.fields[sfBaseURL].value),
					APIKey:     string(m.inlineForm.fields[sfAPIKey].value),
					IsEmbed:    true,
				}
				return m, func() tea.Msg { return current }
			}
			m.inlineForm.updateEmbedModelFilter()
		}
	case "/api-key":
		m.settingsActive = true
		m.inlineForm.focused = sfAPIKey
		m.inlineForm.editingField = true
	case "/backend":
		m.settingsActive = true
		m.inlineForm.focused = sfBackend
		m.inlineForm.toggleBackend()
	case "/help":
		helpText := "Available commands:\n"
		for _, c := range availableCmds {
			helpText += fmt.Sprintf("  %-16s %s\n", c.name, c.desc)
		}
		m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Content: helpText, Time: time.Now()})
	default:
		m.lastError = fmt.Sprintf("Unknown command: %s (try /help)", cmd)
		m.errorTime = time.Now()
	}

	return m, nil
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Always handle resize and tick regardless of mode.
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tickMsg:
		m.spinFrame = (m.spinFrame + 1) % len(spinnerFrames)
		if m.lastError != "" && time.Since(m.errorTime) > 8*time.Second {
			m.lastError = ""
		}
		return m, tickCmd()
	}

	// ── Global cancel: Esc and Ctrl+C ────────────────────────
	if msg, ok := msg.(tea.KeyMsg); ok && (msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc) {
		// Cancel hierarchy: editing → model select → provider select → settings → double-press quit
		if m.settingsActive {
			if m.inlineForm.editingField {
				m.inlineForm.editingField = false
				return m, nil
			}
			if m.inlineForm.isModelSelect {
				m.inlineForm.isModelSelect = false
				m.inlineForm.modelSearch = ""
				return m, nil
			}
			if m.inlineForm.isEmbedModelSelect {
				m.inlineForm.isEmbedModelSelect = false
				m.inlineForm.embedModelSearch = ""
				return m, nil
			}
			if m.inlineForm.isProviderSelect {
				m.inlineForm.isProviderSelect = false
				return m, nil
			}
			m.settingsActive = false
			s := m.inlineForm.toSettings()
			m.lastSettings = s
			summary := formatSettingsSummary(s)
			m.messages = append(m.messages, ChatMessage{Role: RoleAssistant, Content: summary, Time: time.Now()})
			m.chatOffset = 0
			return m, nil
		}
		// Nothing to cancel — double-press to quit
		if time.Since(m.lastCancelTime) < 2*time.Second {
			m.quit = true
			return m, tea.Quit
		}
		m.lastCancelTime = time.Now()
		m.lastError = "press ctrl+c or esc again to exit"
		m.errorTime = time.Now()
		return m, nil
	}

	// Settings mode — handle key input and pass-through async fetch results.
	if m.settingsActive {
		switch msg := msg.(type) {
		case tea.KeyMsg:
			updated, cmd, saved, _ := m.inlineForm.Update(msg)
			m.inlineForm = updated
			if saved {
				s := m.inlineForm.toSettings()
				m.settingsActive = false
				m.modelName = s.Model
				if m.onSaveSettings != nil {
					go m.onSaveSettings(s)
				}
			}
			return m, cmd
		case ModelsListMsg:
			m.inlineForm.fetchingModels = false
			m.inlineForm.availableModels = []ModelEntry(msg)
			m.inlineForm.updateModelFilter()
			return m, nil
		case ModelsErrorMsg:
			m.inlineForm.fetchingModels = false
			m.inlineForm.fetchError = "Fetch failed (see bottom left). Press enter to type manually."
			m.lastError = string(msg)
			m.errorTime = time.Now()
			return m, nil
		case EmbedModelsListMsg:
			m.inlineForm.fetchingEmbedModels = false
			m.inlineForm.availableEmbedModels = []ModelEntry(msg)
			m.inlineForm.updateEmbedModelFilter()
			return m, nil
		case EmbedModelsErrorMsg:
			m.inlineForm.fetchingEmbedModels = false
			m.inlineForm.fetchEmbedError = "Fetch failed (see bottom left). Press enter to type manually."
			m.lastError = string(msg)
			m.errorTime = time.Now()
			return m, nil
		case errorMsg:
			m.lastError = string(msg)
			m.errorTime = time.Now()
			return m, nil
		case fetchModelsMsg:
			if m.fetchModelsFn != nil {
				info := msg
				if info.IsEmbed {
					m.inlineForm.fetchingEmbedModels = true
					m.inlineForm.fetchEmbedError = ""
				} else {
					m.inlineForm.fetchingModels = true
					m.inlineForm.fetchError = ""
				}
				return m, func() tea.Msg {
					cmd := m.fetchModelsFn(info.ProviderID, info.BaseURL, info.APIKey)
					if cmd == nil {
						return nil
					}
					res := cmd()
					if info.IsEmbed {
						switch r := res.(type) {
						case ModelsListMsg:
							return EmbedModelsListMsg(r)
						case ModelsErrorMsg:
							return EmbedModelsErrorMsg(r)
						}
					}
					return res
				}
			}
			return m, nil
		case openSettingsMsg:
			// No longer triggers a fetch; fetching is lazy (user opens model selector).
			return m, nil
		}
		return m, nil
	}

	// Normal chat mode.
	switch msg := msg.(type) {

	// ── Keyboard ──────────────────────────────────────────────
	case tea.KeyMsg:
		switch msg.Type {

		case tea.KeyCtrlS:
			m.settingsActive = true
			m.messages = append(m.messages, ChatMessage{Role: RoleUser, Content: "/settings", Time: time.Now()})
			m.chatOffset = 0
			return m, nil

		case tea.KeyEnter:
			// Shift+Enter inserts a newline for multi-line input.
			if msg.Alt {
				m.inputBuf = append(m.inputBuf, '\n')
				m.cursorPos = len(m.inputBuf)
				return m, nil
			}
			if m.slashActive && len(m.slashFiltered) > 0 {
				selected := m.slashFiltered[m.slashIdx]
				m.slashActive = false
				m.inputBuf = []rune(selected.name + " ")
				m.cursorPos = len(m.inputBuf)
				text := strings.TrimSpace(string(m.inputBuf))
				if text != "" && strings.HasPrefix(text, "/") {
					return m.executeSlashCommand(text)
				}
				return m, nil
			}
			if (!m.thinking || m.blocking) && len(m.inputBuf) > 0 {
				text := strings.TrimSpace(string(m.inputBuf))
				m.inputBuf = nil
				m.cursorPos = 0
				m.slashActive = false
				m.resetHistory()
				if text != "" {
					if strings.HasPrefix(text, "/") {
						return m.executeSlashCommand(text)
					}
					m.appendHistory(text)
					if OnHistorySave != nil {
						OnHistorySave(m.history)
					}
					m.messages = append(m.messages, ChatMessage{
						Role:    RoleUser,
						Content: text,
						Time:    time.Now(),
					})
					m.thinking = true
					m.chatOffset = 0
					ch := m.inputCh
					return m, func() tea.Msg {
						ch <- text
						return nil
					}
				}
			}

		case tea.KeyTab:
			if m.slashActive && len(m.slashFiltered) > 0 {
				selected := m.slashFiltered[m.slashIdx]
				m.inputBuf = []rune(selected.name + " ")
				m.cursorPos = len(m.inputBuf)
				m.slashActive = false
				return m, nil
			}

		case tea.KeyBackspace, tea.KeyDelete:
			if (!m.thinking || m.blocking) && len(m.inputBuf) > 0 {
				// If browsing history and user edits, update prefix
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(string(m.inputBuf))
					m.historyDraft = string(m.inputBuf)
					m.historyIdx = -1
				}
				m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
				if m.cursorPos > len(m.inputBuf) {
					m.cursorPos = len(m.inputBuf)
				}
				inputStr := string(m.inputBuf)
				if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") {
					m.slashFilter = inputStr
					m.updateSlashFilter()
					m.slashActive = len(m.slashFiltered) > 0
				} else {
					m.slashActive = false
				}
			}

		case tea.KeyUp:
			if m.slashActive && len(m.slashFiltered) > 0 {
				m.slashIdx--
				if m.slashIdx < 0 {
					m.slashIdx = len(m.slashFiltered) - 1
				}
				return m, nil
			}
			// Prompt history navigation
			if !m.thinking || m.blocking {
				m.browseHistoryUp()
			}

		case tea.KeyDown:
			if m.slashActive && len(m.slashFiltered) > 0 {
				m.slashIdx++
				if m.slashIdx >= len(m.slashFiltered) {
					m.slashIdx = 0
				}
				return m, nil
			}
			// Prompt history navigation
			if !m.thinking || m.blocking {
				m.browseHistoryDown()
			}

		case tea.KeyPgUp:
			m.chatOffset += m.chatAreaHeight()

		case tea.KeyPgDown:
			m.chatOffset -= m.chatAreaHeight()
			if m.chatOffset < 0 {
				m.chatOffset = 0
			}

		case tea.KeyHome:
			m.chatOffset = 1 << 30

		case tea.KeyEnd:
			m.chatOffset = 0

		default:
			if !m.thinking || m.blocking {
				// If browsing history and user types, update prefix and reset position
				if m.historyIdx != -1 {
					m.historyPrefix = strings.TrimSpace(string(m.inputBuf))
					m.historyDraft = string(m.inputBuf)
					m.historyIdx = -1
				}
				for _, r := range msg.Runes {
					if unicode.IsPrint(r) {
						m.inputBuf = append(m.inputBuf, r)
						m.cursorPos = len(m.inputBuf)
					}
				}
				inputStr := string(m.inputBuf)
				if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") {
					m.slashFilter = inputStr
					m.updateSlashFilter()
					m.slashActive = len(m.slashFiltered) > 0
				} else {
					m.slashActive = false
				}
			}
		}
	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			switch msg.Button {
			case tea.MouseButtonWheelUp:
				m.chatOffset++
			case tea.MouseButtonWheelDown:
				m.chatOffset--
				if m.chatOffset < 0 {
					m.chatOffset = 0
				}
			}
		}
	// ── Agent events ──────────────────────────────────────────
	case agentMsg:
		evt := AgentEventMsg(msg)
		switch evt.Type {
		case "phase":
			if s, ok := evt.Data.(string); ok && s != "" {
				m.statusPhase = s
				switch s {
				case "grounding":
					m.statusLine = "Gathering context"
				case "planning":
					m.statusLine = "Planning"
				case "executing":
					m.statusLine = "Executing"
				default:
					m.statusLine = s
				}
			}

		case "thinking":
			if s, ok := evt.Data.(string); ok && s != "" {
				m.statusLine = truncateLine(s, 80)
				m.messages = append(m.messages, ChatMessage{
					Role:           RoleThinking,
					Content:        s,
					Time:           time.Now(),
					ThinkingActive: true,
				})
			}

		case "done":
			m.thinking = false
			m.blocking = false
			m.statusLine = ""
			m.chatOffset = 0
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
				}
			}
			if content, ok := evt.Data.(string); ok && content != "" {
				m.messages = append(m.messages, ChatMessage{
					Role:    RoleAssistant,
					Content: content,
					Time:    time.Now(),
				})
			}

		case "tool_call":
			if d, ok := evt.Data.(map[string]string); ok {
				name := d["name"]
				args := d["args"]
				argsCompact := summarizeToolArgs(name, args)
				m.statusLine = toolLabel(name, argsCompact)
				idx := len(m.messages)
				m.messages = append(m.messages, ChatMessage{
					Role:       RoleTool,
					Content:    toolLabel(name, argsCompact),
					Time:       time.Now(),
					ToolName:   name,
					ToolArgs:   argsCompact,
					ToolStatus: "calling",
					ToolIndex:  idx,
				})
			}

		case "tool_result":
			if d, ok := evt.Data.(map[string]string); ok {
				name := d["name"]
				m.statusLine = name
				for i := len(m.messages) - 1; i >= 0; i-- {
					msg := &m.messages[i]
					if msg.Role == RoleTool && msg.ToolName == name && msg.ToolStatus == "calling" {
						msg.ToolStatus = "success"
						msg.Content = msg.ToolName + " " + msg.ToolArgs
						break
					}
				}
			}

		case "blocking":
			// Agent needs user input to continue — re-enable the input box.
			question := fmt.Sprintf("%v", evt.Data)
			m.thinking = false
			m.blocking = true
			m.statusLine = ""
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
				}
			}
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleAssistant,
				Content: "⚠  " + question,
				Time:    time.Now(),
			})

		case "error":
			m.thinking = false
			m.blocking = false
			m.statusLine = ""
			for i := range m.messages {
				if m.messages[i].ThinkingActive {
					m.messages[i].ThinkingActive = false
				}
			}
			errMsg := fmt.Sprintf("%v", evt.Data)
			m.lastError = errMsg
			m.errorTime = time.Now()
			m.messages = append(m.messages, ChatMessage{
				Role:    RoleError,
				Content: errMsg,
				Time:    time.Now(),
			})

		case "usage":
			if d, ok := evt.Data.(map[string]int); ok {
				m.tokenPrompt += d["prompt"]
				m.tokenCompletion += d["completion"]
			}
		}

	// ── External messages ──────────────────────────────────────
	case openSettingsMsg:
		m.settingsActive = true
		// Fetch is lazy — no auto-fetch here.

	case fetchModelsMsg:
		if m.fetchModelsFn != nil {
			info := msg
			if info.IsEmbed {
				m.inlineForm.fetchingEmbedModels = true
				m.inlineForm.fetchEmbedError = ""
			} else {
				m.inlineForm.fetchingModels = true
				m.inlineForm.fetchError = ""
			}
			return m, func() tea.Msg {
				cmd := m.fetchModelsFn(info.ProviderID, info.BaseURL, info.APIKey)
				if cmd == nil {
					return nil
				}
				res := cmd()
				if info.IsEmbed {
					switch r := res.(type) {
					case ModelsListMsg:
						return EmbedModelsListMsg(r)
					case ModelsErrorMsg:
						return EmbedModelsErrorMsg(r)
					}
				}
				return res
			}
		}

	case ModelsListMsg:
		m.inlineForm.fetchingModels = false
		m.inlineForm.availableModels = []ModelEntry(msg)
		m.inlineForm.updateModelFilter()

	case ModelsErrorMsg:
		m.inlineForm.fetchingModels = false
		m.inlineForm.fetchError = string(msg)

	case EmbedModelsListMsg:
		m.inlineForm.fetchingEmbedModels = false
		m.inlineForm.availableEmbedModels = []ModelEntry(msg)
		m.inlineForm.updateEmbedModelFilter()

	case EmbedModelsErrorMsg:
		m.inlineForm.fetchingEmbedModels = false
		m.inlineForm.fetchEmbedError = string(msg)

	case updateModelMsg:
		m.modelName = string(msg)

	case statusMsg:
		// reserved

	case errorMsg:
		m.lastError = string(msg)
		m.errorTime = time.Now()
	}

	return m, nil
}

const padLR = 2

func (m *Model) View() string {
	return m.chatView()
}

func (m *Model) chatView() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	pad := strings.Repeat(" ", padLR)
	innerW := m.width - padLR*2
	if innerW < 20 {
		innerW = 20
	}

	lines := make([]string, 0, m.height)
	add := func(s string) {
		if len(lines) < m.height {
			lines = append(lines, pad+s)
		}
	}
	addFull := func(s string) {
		if len(lines) < m.height {
			lines = append(lines, s)
		}
	}

	dim := lipgloss.NewStyle().Foreground(colorMuted)

	// top padding
	add("")

	// ── Header ────────────────────────────────────────────────
	uptime := time.Since(m.startTime).Truncate(time.Second)
	left := lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Render("crude") +
		" " + dim.Render(truncateLine(m.modelName, innerW/3))
	right := dim.Render(uptime.String())
	spacer := innerW - lipgloss.Width(left) - lipgloss.Width(right)
	if spacer < 0 {
		spacer = 0
	}
	add(left + strings.Repeat(" ", spacer) + right)
	sepStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	addFull(sepStyle.Render(strings.Repeat("─", m.width)))

	// ── Chat area ─────────────────────────────────────────────
	chatH := m.chatAreaHeight()
	allLines := m.renderAllMessages()

	total := len(allLines)
	maxOffset := total - chatH
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.chatOffset > maxOffset {
		m.chatOffset = maxOffset
	}
	if m.chatOffset < 0 {
		m.chatOffset = 0
	}

	endIdx := total - m.chatOffset
	startIdx := endIdx - chatH
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > total {
		endIdx = total
	}
	visible := allLines[startIdx:endIdx]
	// Pad empty lines at the TOP of chat area so messages stay anchored to bottom
	emptyTop := chatH - len(visible)
	for i := 0; i < emptyTop; i++ {
		add("")
	}
	for _, l := range visible {
		add(l)
	}

	// ── Input area (full width) ──────────────────────────────
	sepStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#333333"))
	addFull(sepStyle.Render(strings.Repeat("─", m.width)))

	if m.settingsActive {
		msg1 := pad + dim.Render("[ interactive settings active · arrows/tab: navigate · enter: edit · ctrl+s: save · esc: return ]")
		addFull(msg1)
	} else {
		var inputContent string
		if m.thinking && !m.blocking {
			inputContent = pad + dim.Italic(true).Render("> waiting for response...")
		} else {
			inputLines := strings.Split(string(m.inputBuf), "\n")
			for i, il := range inputLines {
				prefix := "  "
				if i == 0 {
					prefix = "> "
				}
				if i == len(inputLines)-1 {
					inputContent += pad + lipgloss.NewStyle().Foreground(colorMainText).Render(prefix+il) + dim.Render("_")
				} else {
					inputContent += pad + lipgloss.NewStyle().Foreground(colorMainText).Render(prefix+il) + "\n"
				}
			}
		}
		for _, il := range strings.Split(inputContent, "\n") {
			addFull(il)
		}

		inputStr := string(m.inputBuf)
		if m.slashActive && !m.thinking && !strings.Contains(inputStr, "\n") {
			accentStyle := lipgloss.NewStyle().Foreground(colorAccent)
			highlightStyle := lipgloss.NewStyle().Foreground(colorMainText).Background(lipgloss.Color("#1e2a3a"))

			for i, c := range m.slashFiltered {
				if i >= 8 {
					addFull(pad + dim.Render(fmt.Sprintf("  ... and %d more", len(m.slashFiltered)-8)))
					break
				}
				selected := i == m.slashIdx
				var line string
				if selected {
					line = accentStyle.Render(" ❯ ") + highlightStyle.Render(fmt.Sprintf("%-16s", c.label)) + " " + dim.Render(c.desc)
				} else {
					line = dim.Render("   ") + lipgloss.NewStyle().Foreground(colorFg).Render(fmt.Sprintf("%-16s", c.label)) + " " + dim.Render(c.desc)
				}
				addFull(pad + line)
			}
		}
	}

	// Bottom border below input
	addFull(sepStyle.Render(strings.Repeat("─", m.width)))

	// ── Status bar: errors left, status right ─────────────
	var errLeft string
	if m.lastError != "" {
		errLeft = pad + lipgloss.NewStyle().Foreground(colorError).Render(
			truncateLine(m.lastError, m.width/2))
	} else if m.thinking {
		errLeft = pad + workAnimation(m.spinFrame, m.statusPhase)
	}

	// Token usage display
	var usageStr string
	if m.tokenPrompt > 0 || m.tokenCompletion > 0 {
		usageStr = dim.Render(fmt.Sprintf(" %s in / %s out", formatTokens(m.tokenPrompt), formatTokens(m.tokenCompletion)))
	}

	var statusRight string
	if m.blocking {
		awaitStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ffaa00"))
		statusRight = awaitStyle.Render("⚠ awaiting input")
	} else if m.thinking {
		statusRight = dim.Render(m.statusPhase)
	} else {
		statusRight = dim.Render("ready")
	}
	statusRight = dim.Render(activeBackendLabel) + " " + statusRight + usageStr

	statusW := m.width - lipgloss.Width(errLeft) - lipgloss.Width(statusRight) - padLR
	if statusW < 1 {
		statusW = 1
	}
	statusBar := errLeft + strings.Repeat(" ", statusW) + statusRight + pad

	for len(lines) < m.height-2 {
		addFull("")
	}
	addFull(statusBar)
	addFull("") // bottom margin

	return strings.Join(lines, "\n")
}

// chatAreaHeight returns the available lines for the chat area,
// dynamically accounting for multi-line input.
func (m *Model) chatAreaHeight() int {
	if m.settingsActive {
		h := m.height - 7
		if h < 1 {
			h = 1
		}
		return h
	}
	inputLineCount := 1 + strings.Count(string(m.inputBuf), "\n")
	slashLines := 0
	inputStr := string(m.inputBuf)
	if m.slashActive && !m.thinking && !strings.Contains(inputStr, "\n") {
		slashLines = len(m.slashFiltered)
		if slashLines > 8 {
			slashLines = 9
		}
	}
	reserved := 2 + 1 + inputLineCount + slashLines + 1 + 1 + 1
	h := m.height - reserved
	if h < 1 {
		h = 1
	}
	return h
}

// renderAllMessages converts all ChatMessages into display lines.
func (m *Model) renderAllMessages() []string {
	var all []string
	dim := lipgloss.NewStyle().Foreground(colorMuted)
	contentWidth := m.width - padLR*2 - 16 // leave room for timestamp
	if contentWidth < 10 {
		contentWidth = 10
	}
	innerW := m.width - padLR*2
	if innerW < 20 {
		innerW = 20
	}

	for _, msg := range m.messages {
		timeLine1, timeLine2 := formatTimeLabel(msg.Time)
		timeW := max(len(timeLine1), len(timeLine2))

		// Breathing room between messages
		all = append(all, "")

		switch msg.Role {
		case RoleUser:
			// ❯ prompt with gray background
			userStyle := lipgloss.NewStyle().
				Foreground(colorMainText).
				Background(lipgloss.Color("#2a2a3a"))
			lines := wrapText(msg.Content, contentWidth)
			for i, line := range lines {
				prefix := "  "
				if i == 0 {
					prefix = "❯ "
				}
				styled := userStyle.Render(prefix + line)
				if i == 0 {
					// Right-align time on first line
					sp := innerW - lipgloss.Width(styled) - timeW
					if sp < 1 {
						sp = 1
					}
					all = append(all, styled+strings.Repeat(" ", sp)+dim.Render(timeLine1))
				} else if i == 1 {
					sp := innerW - lipgloss.Width(styled) - timeW
					if sp < 1 {
						sp = 1
					}
					all = append(all, styled+strings.Repeat(" ", sp)+dim.Render(timeLine2))
				} else {
					all = append(all, styled)
				}
			}
			// If only one line, still show timeLine2
			if len(lines) == 1 {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				all = append(all, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}

		case RoleAssistant:
			// Try rendering as Markdown
			var rendered string
			isMarkdown := false
			md := m.makeMdRenderer()
			if md != nil {
				r, err := md.Render(msg.Content)
				if err == nil {
					rendered = strings.TrimRight(r, "\n")
					isMarkdown = true
				}
			}
			var asstLines []string
			if !isMarkdown {
				asstLines = wrapText(msg.Content, contentWidth)
			} else {
				asstLines = strings.Split(rendered, "\n")
			}

			// First line: background label + content
			labelStyle := lipgloss.NewStyle().Foreground(colorAccent2).Bold(true).Background(lipgloss.Color("#2a2a3a"))
			if len(asstLines) > 0 {
				label := labelStyle.Render("❯ assistant ")
				asstLines[0] = label + asstLines[0]
			}

			// Remaining lines: just indented, no background

			// Add timestamps
			for i, line := range asstLines {
				var timePart string
				if i == 0 {
					timePart = dim.Render(timeLine1)
				} else if i == 1 {
					timePart = dim.Render(timeLine2)
				}
				if timePart != "" {
					lineW := lipgloss.Width(line)
					sp := innerW - lineW - timeW
					if sp < 1 {
						sp = 1
					}
					asstLines[i] = line + strings.Repeat(" ", sp) + timePart
				}
			}

			all = append(all, asstLines...)
			// If only one line, show date on the next line
			if len(asstLines) == 1 {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				all = append(all, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}

		case RoleThinking:
			lines := wrapText(msg.Content, contentWidth)
			borderPrefix := dim.Render("  │ ")
			for i, line := range lines {
				if i == 0 {
					if msg.ThinkingActive {
						all = append(all, borderPrefix+dim.Render(spinnerFrames[m.spinFrame]+" ")+dim.Italic(true).Render(line))
					} else {
						all = append(all, borderPrefix+dim.Render("● ")+dim.Italic(true).Render(line))
					}
				} else {
					all = append(all, borderPrefix+"  "+dim.Italic(true).Render(line))
				}
			}

		case RoleTool:
			isCalling := msg.ToolStatus == "calling"

			spinColor := spinnerColorDefault
			if m.statusPhase == "grounding" {
				spinColor = spinnerColorGrounding
			} else if m.statusPhase == "planning" {
				spinColor = spinnerColorPlanning
			} else if m.statusPhase == "executing" {
				spinColor = spinnerColorExecuting
			}
			if msg.ToolStatus == "success" {
				spinColor = spinnerColorDone
			} else if msg.ToolStatus == "error" {
				spinColor = spinnerColorError
			}

			spinStyle := lipgloss.NewStyle().Foreground(spinColor)
			label := pascalCaseTool(msg.ToolName) + " " + msg.ToolArgs
			nameStyle := lipgloss.NewStyle().Foreground(colorMuted)

			var line string
			if isCalling {
				line = dim.Render("  │ ") + spinStyle.Render(spinnerFrames[m.spinFrame]+" ") + nameStyle.Render(label)
			} else if msg.ToolStatus == "success" {
				line = dim.Render("  │ ") + spinStyle.Render("● ") + nameStyle.Render(label)
			} else if msg.ToolStatus == "error" {
				line = dim.Render("  │ ") + spinStyle.Render("● ") + nameStyle.Render(label)
			} else {
				line = dim.Render("  │ ") + spinStyle.Render("○ ") + nameStyle.Render(label)
			}

			all = append(all, line)

		case RoleError:
			lines := wrapText(msg.Content, contentWidth)
			for i, line := range lines {
				styled := lipgloss.NewStyle().Foreground(colorError).Render("  " + line)
				if i == 0 {
					label := lipgloss.NewStyle().Foreground(colorError).Render("error")
					styled = label + " " + lipgloss.NewStyle().Foreground(colorError).Render(line)
					sp := innerW - lipgloss.Width(styled) - timeW
					if sp < 1 {
						sp = 1
					}
					all = append(all, styled+strings.Repeat(" ", sp)+dim.Render(timeLine1))
				} else if i == 1 {
					sp := innerW - lipgloss.Width(styled) - timeW
					if sp < 1 {
						sp = 1
					}
					all = append(all, styled+strings.Repeat(" ", sp)+dim.Render(timeLine2))
				} else {
					all = append(all, styled)
				}
			}
			if len(lines) == 1 {
				sp := innerW - timeW
				if sp < 0 {
					sp = 0
				}
				all = append(all, strings.Repeat(" ", sp)+dim.Render(timeLine2))
			}
		}
	}

	// If settings is active, render the form at the bottom of messages.
	if m.settingsActive {
		for _, sl := range strings.Split(m.inlineForm.View(), "\n") {
			all = append(all, sl)
		}
	}

	return all
}

// formatTimeLabel returns two right-aligned strings: "HH:MM" and "today"/"yesterday"/"MM:DD".
func formatTimeLabel(t time.Time) (string, string) {
	line1 := t.Format("15:04")
	now := time.Now()
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if t.After(todayStart) {
		return line1, "today"
	}
	yesterdayStart := todayStart.Add(-24 * time.Hour)
	if t.After(yesterdayStart) {
		return line1, "yesterday"
	}
	return line1, t.Format("01/02")
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// activeBackendLabel is set by main to display the current backend in the footer.
var activeBackendLabel = "local"

// wrapText breaks text into lines of at most width display cells.
func wrapText(text string, width int) []string {
	if width <= 0 {
		return []string{text}
	}
	var result []string
	for _, para := range strings.Split(text, "\n") {
		if runewidth.StringWidth(para) <= width {
			result = append(result, para)
			continue
		}
		words := strings.Fields(para)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		current := words[0]
		for _, w := range words[1:] {
			if runewidth.StringWidth(current)+1+runewidth.StringWidth(w) <= width {
				current += " " + w
			} else {
				result = append(result, current)
				current = w
			}
		}
		result = append(result, current)
	}
	if len(result) == 0 {
		result = []string{""}
	}
	return result
}

func truncateLine(s string, max int) string {
	if runewidth.StringWidth(s) <= max {
		return s
	}
	return runewidth.Truncate(s, max, "…")
}

// IsQuitting returns true after the user has requested quit.
func (m *Model) IsQuitting() bool { return m.quit }

func formatSettingsSummary(s Settings) string {
	lines := []string{"Current Settings:"}
	if s.ProviderID != "" {
		lines = append(lines, fmt.Sprintf("  provider: %s", s.ProviderID))
	}
	if s.Model != "" {
		lines = append(lines, fmt.Sprintf("  model: %s", s.Model))
	}
	if s.EmbedModel != "" {
		lines = append(lines, fmt.Sprintf("  embed model: %s", s.EmbedModel))
	}
	if s.Backend != "" {
		lines = append(lines, fmt.Sprintf("  backend: %s", s.Backend))
	}
	if s.SSHHost != "" {
		lines = append(lines, fmt.Sprintf("  ssh: %s@%s", s.SSHUser, s.SSHHost))
	}
	return strings.Join(lines, "\n")
}

// formatTokens formats a token count into a compact human-readable string.
func formatTokens(n int) string {
	switch {
	case n >= 1000000:
		return fmt.Sprintf("%.1fM", float64(n)/1000000)
	case n >= 1000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%d", n)
	}
}

// summarizeToolArgs extracts a compact, user-friendly summary from tool args JSON.
func summarizeToolArgs(tool, argsJSON string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &m); err != nil {
		return truncateLine(argsJSON, 60)
	}

	switch tool {
	case "read_file", "write_file", "delete_file":
		if p, ok := m["path"].(string); ok {
			return p
		}
	case "edit_file":
		p, _ := m["path"].(string)
		return p
	case "list_directory":
		if p, ok := m["path"].(string); ok {
			return p
		}
		return "."
	case "glob_files":
		if p, ok := m["pattern"].(string); ok {
			return p
		}
	case "grep_files":
		pat, _ := m["pattern"].(string)
		path, _ := m["path"].(string)
		if path != "" {
			return fmt.Sprintf("%s in %s", pat, path)
		}
		return pat
	case "execute_command":
		if cmd, ok := m["command"].(string); ok {
			return truncateLine(cmd, 60)
		}
	case "web_search":
		if q, ok := m["query"].(string); ok {
			return truncateLine(q, 60)
		}
	case "web_fetch":
		if u, ok := m["url"].(string); ok {
			return truncateLine(u, 60)
		}
	case "arxiv_search":
		if q, ok := m["query"].(string); ok {
			return truncateLine(q, 60)
		}
	case "run_javascript":
		if code, ok := m["code"].(string); ok {
			lines := strings.Split(code, "\n")
			if len(lines) > 1 {
				return truncateLine(lines[0], 60)
			}
			return truncateLine(code, 60)
		}
	case "reply", "blocking":
		return ""
	}

	// Fallback: show first string value found
	for _, v := range m {
		if s, ok := v.(string); ok {
			return truncateLine(s, 60)
		}
	}
	return ""
}

// toolLabel returns a compact display string for a tool invocation.
func toolLabel(tool, argsSummary string) string {
	if argsSummary != "" {
		return fmt.Sprintf("%s %s", tool, argsSummary)
	}
	return tool
}

// pascalCaseTool converts a snake_case tool name to PascalCase for display.
func pascalCaseTool(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, " ")
}

// workAnimation returns a fun animated string for the status bar when agent is working.
func workAnimation(frame int, phase string) string {
	phaseLabel := phase
	switch phase {
	case "grounding":
		phaseLabel = "gathering context"
	case "planning":
		phaseLabel = "planning"
	case "executing":
		phaseLabel = "executing"
	default:
		phaseLabel = "thinking"
	}

	spinColor := spinnerColorDefault
	switch phase {
	case "grounding":
		spinColor = spinnerColorGrounding
	case "planning":
		spinColor = spinnerColorPlanning
	case "executing":
		spinColor = spinnerColorExecuting
	}

	spinStyle := lipgloss.NewStyle().Foreground(spinColor)
	dots := strings.Repeat(".", frame%4)
	return spinStyle.Render(spinnerFrames[frame%len(spinnerFrames)]+" ") +
		lipgloss.NewStyle().Foreground(colorMainText).Render(phaseLabel) +
		lipgloss.NewStyle().Foreground(colorMuted).Render(dots)
}
