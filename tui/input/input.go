package input

import (
	"strings"

	"crude/sessiondb"
	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
	"crude/tui/ui"
)

const slashPageSize = 8

type Event interface{ event() }

type SubmitEvent struct {
	Text         string
	ReferenceIDs []string
}

func (SubmitEvent) event() {}

type BtwSubmitEvent struct {
	Text         string
	ReferenceIDs []string
}

func (BtwSubmitEvent) event() {}

type SlashCommandEvent struct{ Command string }

func (SlashCommandEvent) event() {}

type SettingsOpenEvent struct{}

func (SettingsOpenEvent) event() {}

type HistorySaveEvent struct{ History []string }

func (HistorySaveEvent) event() {}

type AttachFilesEvent struct{}

func (AttachFilesEvent) event() {}

type CmdInfo struct {
	Name  string
	Label string
	Desc  string
	Icon  string
}

var availableCmds = []CmdInfo{
	{Name: "settings", Label: "settings", Desc: "Open general settings"},
	{Name: "undo", Label: "undo", Desc: "Undo last file operation"},
	{Name: "undo-n", Label: "undo N", Desc: "Undo last N file operations"},
	{Name: "undo-all", Label: "undo all", Desc: "Undo all file operations this session"},
	{Name: "yolo", Label: "yolo", Desc: "Disable ALL safety checks (DANGEROUS)"},
	{Name: "sessions", Label: "sessions", Desc: "Browse and resume past sessions"},
	{Name: "help", Label: "help", Desc: "Show available commands"},
}

func AvailableCmds() []CmdInfo {
	return availableCmds
}

func cmdsToItems(cmds []CmdInfo) []components.ListItem {
	items := make([]components.ListItem, len(cmds))
	for i, c := range cmds {
		items[i] = components.ListItem{
			ID:    c.Name,
			Label: c.Label,
		}
	}
	return items
}

type Model struct {
	theme                 *theme.Theme
	input                 *ui.TextArea
	history               []string
	histIdx               int
	histDraft, histPrefix string

	slashActive bool
	slashList   components.List

	showHint  bool
	width     int
	textWidth int
	thinking  bool
	blocking  bool
	spinFrame int
	refStore  ReferenceStore

	actionRegistry ActionRegistry
}

// ActionRegistry is the interface the input model needs to derive
// shortcut labels. Pointer so the value-type Model can carry it.
type ActionRegistry interface {
	HintFor(id string) string
	HintLabelFor(id string) string
}

// ActionRegistryAdapter bridges a concrete action registry (whose
// methods take a typed ActionID parameter) to the input model's
// string-based interface. Use it in the app layer where the concrete
// type is already known.
type ActionRegistryAdapter struct {
	HintForFn      func(id string) string
	HintLabelForFn func(id string) string
}

func (a ActionRegistryAdapter) HintFor(id string) string {
	if a.HintForFn != nil {
		return a.HintForFn(id)
	}
	return ""
}

func (a ActionRegistryAdapter) HintLabelFor(id string) string {
	if a.HintLabelForFn != nil {
		return a.HintLabelForFn(id)
	}
	return ""
}

// SetActionRegistry sets the action registry for shortcut derivation.
func (m *Model) SetActionRegistry(r ActionRegistry) {
	m.actionRegistry = r
}

func NewModel(t *theme.Theme) Model {
	slashList := components.NewList(t, slashPageSize)
	slashList.SetRenderMode(components.ListRenderPopup)
	slashList.SetItems(cmdsToItems(availableCmds))
	slashList.FilterMatchFn = func(item components.ListItem, filter string) bool {
		return strings.HasPrefix(strings.ToLower(item.ID), filter) || strings.Contains(strings.ToLower(item.Detail), filter)
	}

	tokens := ui.NewTokenSet(t)
	return Model{
		theme:     t,
		input:     ui.NewTextArea("input/chat", tokens, true),
		slashList: slashList,
		histIdx:   -1,
		showHint:  true,
	}
}

func (m *Model) ShowHint() bool {
	return m.showHint
}

// ContextHint returns composer guidance for the shared bottom status region.
// The input view itself only renders editable content and slash results.
func (m *Model) ContextHint() string {
	if !m.showHint || m.input.Value() != "" {
		return ""
	}
	if m.thinking && !m.blocking {
		submit := "enter send"
		if m.actionRegistry != nil {
			if hl := m.actionRegistry.HintLabelFor("input.submit"); hl != "" {
				submit = hl
			}
		}
		return "type to send a note to the agent · " + submit + " · esc cancel"
	}
	return m.buildChatHint()
}

func (m *Model) buildChatHint() string {
	var parts []string
	if m.actionRegistry != nil {
		for _, id := range []string{"input.submit", "input.newline", "file.attach"} {
			if hl := m.actionRegistry.HintLabelFor(id); hl != "" {
				parts = append(parts, hl)
			}
		}
	} else {
		parts = append(parts, "enter send", "alt+enter new line", "ctrl+o attach file")
	}
	parts = append(parts, "↑↓ history", "esc exit")
	return strings.Join(parts, " · ")
}

func (m *Model) DismissHint() {
	m.showHint = false
}

func (m *Model) SetState(thinking, blocking bool) {
	m.thinking = thinking
	m.blocking = blocking
}

func (m *Model) SetWidth(w int) {
	m.width = w
	m.textWidth = w - m.theme.Spacing.PadLR*2 - 2
	if m.textWidth < 1 {
		m.textWidth = 1
	}
	m.input.SetTextWidth(m.textWidth)
}

func (m *Model) SetSpinFrame(frame int) {
	m.spinFrame = frame
}

func (m *Model) SetHistory(h []string) {
	m.history = h
}

func (m Model) History() []string {
	return m.history
}

func (m *Model) SetInput(s string) {
	m.SetValuePreserveReferences(s)
}

func (m *Model) ClearInput() {
	m.input.Clear()
	m.histIdx = -1
	m.histDraft = ""
	m.histPrefix = ""
	m.slashActive = false
}

func (m *Model) SetPrompt(p string) {
	m.input.SetPrompt(p)
}

func (m *Model) ResetPrompt() {
	m.input.SetPrompt("")
}

func (m Model) Height() int {
	return m.input.Height()
}

func (m Model) InputTextWidth() int {
	w := m.width - m.theme.Spacing.PadLR*2 - 2
	if w < 1 {
		w = 1
	}
	return w
}

func (m Model) FirstLineY(chatAreaHeight int, showHint, thinking bool, inputLen int) int {
	_ = showHint
	_ = thinking
	_ = inputLen
	return chatAreaHeight + 4
}

func (m Model) MouseToInputPos(mouseX, mouseY, chatAreaHeight int, showHint bool) (runeIdx int, ok bool) {
	firstY := m.FirstLineY(chatAreaHeight, showHint, m.thinking, m.input.Len())
	relY := mouseY - firstY
	if relY < 0 {
		return -1, false
	}

	textW := m.textWidth
	if textW < 1 {
		textW = m.width - 5
		if textW < 1 {
			textW = 1
		}
	}

	lines := m.input.Lines()
	visIdx := 0
	for li, logLine := range lines {
		segments := rendering.WordWrap(string(logLine), textW)
		for si, seg := range segments {
			if visIdx == relY {
				col := mouseX - m.theme.Spacing.PadLR - 2
				if col < 0 {
					col = 0
				}
				segW := rendering.VisibleWidth(seg)
				if col > segW {
					col = segW
				}

				pos := 0
				for i := 0; i < li; i++ {
					pos += len(lines[i]) + 1
				}
				segRunes := []rune(seg)
				segRuneIdx := rendering.ColToRuneIdx(segRunes, col)
				pos += rendering.FindSegmentStart(logLine, textW, si) + segRuneIdx
				return pos, true
			}
			visIdx++
		}
	}
	return -1, false
}

func (m Model) InputSelecting() bool { return m.input.HasActiveSelection() || m.input.IsSelecting() }

func (m Model) SelectedText() string { return m.input.SelectedText() }

func (m Model) InputSelectionRange() (start, end int) {
	return m.input.SelectionRange()
}

func (m *Model) ClearInputSelection() {
	m.input.ClearSelection()
}

func (m Model) Value() string {
	return m.input.Value()
}

func (m Model) DisplayValue() string {
	if m.refStore == nil {
		return m.input.Value()
	}
	return DisplayReferences(m.input.Value(), m.refStore.ListReferences())
}

func (m Model) ExpandedValue() string {
	if m.refStore == nil {
		return m.input.Value()
	}
	return ExpandReferences(m.input.Value(), m.refStore.ListReferences())
}

func (m Model) PromptReferences() []sessiondb.Reference {
	if m.refStore == nil {
		return nil
	}
	ids := m.ReferencedIDs()
	refs := make([]sessiondb.Reference, 0, len(ids))
	for _, id := range ids {
		if ref, ok := m.refStore.GetReference(id); ok && ref.Active() {
			refs = append(refs, ref)
		}
	}
	return refs
}

func (m Model) SlashActive() bool {
	return m.slashActive
}

func (m Model) SlashFiltered() []CmdInfo {
	items := m.slashList.Items()
	cmds := make([]CmdInfo, len(items))
	for i, item := range items {
		cmds[i] = CmdInfo{
			Name:  item.ID,
			Label: item.Label,
			Desc:  item.Detail,
			Icon:  item.Icon,
		}
	}
	return cmds
}
