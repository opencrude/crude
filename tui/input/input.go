package input

import (
	"strings"

	"crude/tui/components"
	"crude/tui/theme"
)

const slashPageSize = 8

type Event interface{ event() }

type SubmitEvent struct{ Text string }

func (SubmitEvent) event() {}

type SlashCommandEvent struct{ Command string }

func (SlashCommandEvent) event() {}

type SettingsOpenEvent struct{}

func (SettingsOpenEvent) event() {}

type HistorySaveEvent struct{ History []string }

func (HistorySaveEvent) event() {}

type CmdInfo struct {
	Name  string
	Label string
	Desc  string
	Icon  string
}

var availableCmds = []CmdInfo{
	{Name: "settings", Label: "settings", Desc: "Open general settings"},
	{Name: "provider", Label: "provider", Desc: "Select provider"},
	{Name: "model", Label: "model", Desc: "Select model"},
	{Name: "oracle-model", Label: "oracle model", Desc: "Select oracle (reasoning) model"},
	{Name: "api-key", Label: "api key", Desc: "Set API key"},
	{Name: "effort", Label: "effort", Desc: "Cycle thinking effort (none/low/medium/high/xhigh)"},
	{Name: "undo", Label: "undo", Desc: "Undo last file operation"},
	{Name: "undo-n", Label: "undo N", Desc: "Undo last N file operations"},
	{Name: "undo-all", Label: "undo all", Desc: "Undo all file operations this session"},
	{Name: "changes", Label: "changes", Desc: "Show files changed this session"},
	{Name: "yolo", Label: "yolo", Desc: "Disable ALL safety checks (DANGEROUS)"},
	{Name: "help", Label: "help", Desc: "Show available commands"},
}

func AvailableCmds() []CmdInfo {
	return availableCmds
}

func cmdsToItems(cmds []CmdInfo) []components.ListItem {
	items := make([]components.ListItem, len(cmds))
	for i, c := range cmds {
		items[i] = components.ListItem{
			ID:     c.Name,
			Label:  c.Label,
			Detail: c.Desc,
			Icon:   c.Icon,
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

	showHint  bool
	width     int
	textWidth int
	thinking  bool
	blocking  bool
	spinFrame int
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
	m.textWidth = w - 5
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
	m.input.SetValue(s)
}

func (m *Model) ClearInput() {
	m.input.Clear()
	m.histIdx = -1
	m.histDraft = ""
	m.histPrefix = ""
	m.slashActive = false
}

func (m Model) Height() int {
	textW := m.textWidth
	if textW < 1 {
		textW = m.width - 5
		if textW < 1 {
			textW = 1
		}
	}
	return m.input.Height(textW)
}

func (m Model) InputTextWidth() int {
	w := m.width - 5
	if w < 1 {
		w = 1
	}
	return w
}

func (m Model) FirstLineY(chatAreaHeight int, showHint, thinking bool, inputLen int) int {
	return 0
}

func (m Model) MouseToInputPos(mouseX, mouseY, chatAreaHeight int, showHint bool) (runeIdx int, ok bool) {
	return 0, false
}

func (m Model) Value() string {
	return m.input.Value()
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
