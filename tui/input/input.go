package input

import (
	"strings"

	"crude/promptref"
	"crude/tui/components"
	"crude/tui/rendering"
	"crude/tui/theme"
)

const slashPageSize = 8

type Event interface{ event() }

type SubmitEvent struct{ Text string }

func (SubmitEvent) event() {}

type BtwSubmitEvent struct{ Text string }

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
	theme    *theme.Theme
	input    components.TextInput
	history  []string
	histIdx  int
	histDraft, histPrefix string

	slashActive bool
	slashList   components.List

	showHint        bool
	width           int
	textWidth       int
	thinking        bool
	blocking        bool
	spinFrame       int
	refStore        ReferenceStore
}

func NewModel(t *theme.Theme) Model {
	slashList := components.NewList(t, slashPageSize)
	slashList.SetRenderMode(components.ListRenderPopup)
	slashList.SetItems(cmdsToItems(availableCmds))
	slashList.FilterMatchFn = func(item components.ListItem, filter string) bool {
		return strings.HasPrefix(strings.ToLower(item.ID), filter) || strings.Contains(strings.ToLower(item.Detail), filter)
	}

	return Model{
		theme:     t,
		input:     components.NewTextInput(t),
		slashList: slashList,
		histIdx:   -1,
		showHint:  true,
	}
}

func (m *Model) ShowHint() bool {
	return m.showHint
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
	textW := m.textWidth
	if textW < 1 {
		textW = m.width - m.theme.Spacing.PadLR*2 - 2
		if textW < 1 {
			textW = 1
		}
	}
	return m.input.Height(textW)
}

func (m Model) InputTextWidth() int {
	w := m.width - m.theme.Spacing.PadLR*2 - 2
	if w < 1 {
		w = 1
	}
	return w
}

func (m Model) FirstLineY(chatAreaHeight int, showHint, thinking bool, inputLen int) int {
	y := chatAreaHeight + 4
	if showHint && inputLen == 0 {
		y++
	}
	return y
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
				segW := rendering.StringWidth(seg)
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

func (m Model) PromptReferences() []promptref.Reference {
	if m.refStore == nil {
		return nil
	}
	ids := m.ReferencedIDs()
	refs := make([]promptref.Reference, 0, len(ids))
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
