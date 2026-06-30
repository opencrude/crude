package viewmodel

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"crude/tui/input"
	"crude/tui/rendering"
	"crude/tui/scrollbar"
	"crude/tui/theme"
	"crude/tui/widget"
	ttypes "crude/tui/types"
	"crude/tui/wm"
)

type cmdCategory string

const (
	catFile    cmdCategory = "file"
	catTools   cmdCategory = "tools"
	catSystem  cmdCategory = "system"
	catHelp    cmdCategory = "help"
	catAgent   cmdCategory = "agent"
)

type cmdEntry struct {
	ID       string
	Label    string
	Detail   string
	Category cmdCategory
	Icon     string
}

var paletteCommands = []cmdEntry{
	{ID: "composer", Label: "Composer", Detail: "compose a detailed prompt", Category: catTools, Icon: "\u270f"},
	{ID: "settings", Label: "Settings", Detail: "configure provider, model, theme", Category: catTools, Icon: "\u2699"},
	{ID: "attach-file", Label: "Attach File", Detail: "add a file reference", Category: catFile, Icon: "\u29c9"},
	{ID: "orchestrator-settings", Label: "Orchestrator Settings", Detail: "override model and provider", Category: catAgent, Icon: "\u2699"},
	{ID: "reset-orchestrator", Label: "Reset Overrides", Detail: "clear orchestrator overrides", Category: catAgent, Icon: "\u21ba"},
	{ID: "toggle-multiagent", Label: "Toggle Multi-Agent", Detail: "enable or disable sub-agents", Category: catAgent, Icon: "\u2606"},
	{ID: "yolo", Label: "YOLO Mode", Detail: "toggle safety checks", Category: catTools, Icon: "\u2620"},
	{ID: "undo", Label: "Undo", Detail: "revert last file change", Category: catSystem, Icon: "\u21a9"},
	{ID: "help", Label: "Help", Detail: "show commands and shortcuts", Category: catHelp, Icon: "?"},
	{ID: "quit", Label: "Quit", Detail: "exit the application", Category: catSystem, Icon: "\u2716"},
}

type commandPaletteModel struct {
	th       *theme.Theme
	items    []cmdEntry
	filtered []cmdEntry
	groups   []cmdGroup
	filter   string
	idx      int
	scroll   int
	sb       scrollbar.ScrollBar
	callback func(cmdID string) tea.Cmd
	width    int
	height   int
	screenX  int
	screenY  int

	Hover widget.Hover

	// lineToItem maps rendered content line indices to item indices.
	// -1 means the line is a section header or divider.
	lineToItem []int
}

type cmdGroup struct {
	Category cmdCategory
	Label    string
	Start    int
	Count    int
}

func categoryLabel(c cmdCategory) string {
	switch c {
	case catFile:
		return "File"
	case catTools:
		return "Tools"
	case catAgent:
		return "Agent"
	case catSystem:
		return "System"
	case catHelp:
		return "Help"
	}
	return string(c)
}

func newCommandPaletteModel(th *theme.Theme, callback func(cmdID string) tea.Cmd) *commandPaletteModel {
	m := &commandPaletteModel{
		th:       th,
		items:    paletteCommands,
		callback: callback,
		sb:       scrollbar.New(0),
		Hover:    widget.NewHover(),
	}
	m.applyFilter()
	return m
}

func (m *commandPaletteModel) applyFilter() {
	if m.filter == "" {
		m.filtered = append([]cmdEntry(nil), m.items...)
	} else {
		m.filtered = nil
		lower := strings.ToLower(m.filter)
		for _, item := range m.items {
			if fuzzyMatch(item, lower) {
				m.filtered = append(m.filtered, item)
			}
		}
	}
	m.buildGroups()
	if m.idx >= len(m.filtered) {
		m.idx = 0
	}
	m.clampScroll()
}

func fuzzyMatch(item cmdEntry, filter string) bool {
	return fuzzyContains(item.Label, filter) ||
		fuzzyContains(item.Detail, filter) ||
		fuzzyContains(item.ID, filter)
}

func fuzzyContains(text, filter string) bool {
	if filter == "" {
		return true
	}
	lower := strings.ToLower(text)
	fi := 0
	for _, r := range lower {
		if fi < len(filter) && byte(r) == filter[fi] {
			fi++
		}
	}
	return fi == len(filter)
}

func (m *commandPaletteModel) buildGroups() {
	m.groups = nil
	var current cmdCategory
	var start int
	for i, item := range m.filtered {
		if item.Category != current {
			if i > start {
				m.groups = append(m.groups, cmdGroup{Category: current, Start: start, Count: i - start})
			}
			current = item.Category
			start = i
		}
	}
	if len(m.filtered) > start {
		m.groups = append(m.groups, cmdGroup{Category: current, Start: start, Count: len(m.filtered) - start})
	}
}

func (m *commandPaletteModel) clampScroll() {
	if m.idx < 0 {
		m.idx = 0
	}
	if m.idx >= len(m.filtered) {
		m.idx = len(m.filtered) - 1
	}
}

func (m *commandPaletteModel) visibleHeight() int {
	if m.height < 3 {
		return 3
	}
	vh := m.height - 2
	if vh < 1 {
		vh = 1
	}
	return vh
}

// buildContentLines renders all items with category headers into flat line slices
// and returns a parallel slice mapping each line to its item index (-1 for non-item lines).
func (m *commandPaletteModel) buildContentLines(width int, dim, accent, highlight lipgloss.Style) ([]string, []int) {
	var lines []string
	var mapIdx []int

	for gi, g := range m.groups {
		if g.Count <= 0 {
			continue
		}
		if gi > 0 {
			lines = append(lines, dim.Render(strings.Repeat("\u2500", width)))
			mapIdx = append(mapIdx, -1)
		}
		lines = append(lines, dim.Render(fmt.Sprintf("  %s", categoryLabel(g.Category))))
		mapIdx = append(mapIdx, -1)

		for i := g.Start; i < g.Start+g.Count; i++ {
			lines = append(lines, m.renderItem(m.filtered[i], i == m.idx, width, dim, accent, highlight))
			mapIdx = append(mapIdx, i)
		}
	}
	return lines, mapIdx
}

func (m *commandPaletteModel) View(width int) string {
	m.width = width
	m.lineToItem = nil
	m.clampScroll()
	vh := m.visibleHeight()

	dim := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)
	accent := m.th.Styles.InputPrompt
	highlight := m.th.Styles.Highlight
	fg := m.th.Colors.Fg
	bgPad := lipgloss.NewStyle().Background(m.th.Colors.SubPaneBg)

	padLine := func(s string) string {
		visW := rendering.VisibleWidth(s)
		if visW < width {
			return s + bgPad.Render(strings.Repeat(" ", width-visW))
		}
		return s
	}

	var out []string

	promptStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Muted)
	textStyle := lipgloss.NewStyle().Foreground(fg)
	cursorStyle := lipgloss.NewStyle().Foreground(m.th.Colors.Accent)
	out = append(out, padLine(promptStyle.Render("> ")+textStyle.Render(m.filter)+cursorStyle.Render("\u2588")))
	out = append(out, padLine(dim.Render(strings.Repeat("\u2500", width))))

	if len(m.filtered) == 0 {
		m.lineToItem = nil
		out = append(out, padLine(dim.Render("  no matching commands")))
		for len(out) < vh+2 {
			out = append(out, padLine(""))
		}
		return strings.Join(out, "\n")
	}

	contentLines, mapIdx := m.buildContentLines(width, dim, accent, highlight)

	selLine := -1
	for cl, itemIdx := range mapIdx {
		if itemIdx == m.idx {
			selLine = cl
			break
		}
	}

	if vh > 0 {
		if selLine < m.scroll {
			m.scroll = selLine
		}
		if selLine >= m.scroll+vh {
			m.scroll = selLine - vh + 1
		}
	}
	if m.scroll > len(contentLines)-vh {
		m.scroll = len(contentLines) - vh
	}
	if m.scroll < 0 {
		m.scroll = 0
	}

	sbW := 0
	var sbLines []string
	if len(contentLines) > vh && vh > 0 {
		m.sb.SetTrackHeight(vh)
		m.sb.SetScrollInfo(len(contentLines), vh, m.scroll)
		if sbv := m.sb.View(); sbv != "" {
			sbLines = strings.Split(sbv, "\n")
			sbW = scrollbar.ScrollbarWidth
		}
	}

	renderW := width - sbW
	if renderW < 10 {
		renderW = 10
	}

	contentLines, m.lineToItem = m.buildContentLines(renderW, dim, accent, highlight)

	end := m.scroll + vh
	if end > len(contentLines) {
		end = len(contentLines)
	}
	visible := contentLines[m.scroll:end]

	for li, line := range visible {
		lw := rendering.VisibleWidth(line)
		if lw < renderW {
			line += bgPad.Render(strings.Repeat(" ", renderW-lw))
		}
		if sbW > 0 && li < len(sbLines) && sbLines[li] != "" {
			line += sbLines[li]
		}
		out = append(out, padLine(line))
	}

	for len(out) < vh+2 {
		out = append(out, padLine(""))
	}

	return strings.Join(out, "\n")
}

func (m *commandPaletteModel) renderItem(item cmdEntry, selected bool, width int, dim, accent, highlight lipgloss.Style) string {
	icon := " "
	if item.Icon != "" {
		icon = item.Icon
	}
	detailStr := item.Detail
	gap := 3

	iconW := rendering.VisibleWidth(icon)
	detailW := rendering.VisibleWidth(detailStr)
	availForLabel := width - 2 - iconW - 1 - gap - detailW
	if availForLabel < 6 {
		availForLabel = width - 2 - iconW - 1
		detailStr = ""
	}
	label := rendering.TruncateLine(item.Label, availForLabel)

	rest := icon + " " + label
	if detailStr != "" {
		paddedW := width - 2 - iconW - 1 - gap - rendering.VisibleWidth(label)
		if paddedW < 0 {
			paddedW = 0
		}
		rest += strings.Repeat(" ", gap) + rendering.TruncateLine(detailStr, paddedW)
	}

	if selected {
		return accent.Render("\u276f") + " " + highlight.Render(rest)
	}
	return dim.Render("  " + rest)
}

func (m *commandPaletteModel) handleMouseMotion(msg tea.MouseMotionMsg) {
	mouse := msg.Mouse()
	localX := mouse.X - m.screenX
	localY := mouse.Y - m.screenY

	if localX < 0 || localX >= m.width || localY < 0 || localY >= m.height {
		return
	}

	// Content area starts at contentY=1 (BorderLShape decoration).
	// First 2 content lines are the prompt row and divider row.
	visLine := localY - 1 - 2
	if visLine < 0 || len(m.lineToItem) == 0 {
		return
	}

	contentLine := m.scroll + visLine
	if contentLine < 0 || contentLine >= len(m.lineToItem) {
		return
	}

	if itemIdx := m.lineToItem[contentLine]; itemIdx >= 0 {
		m.idx = itemIdx
	}
}

func (m *commandPaletteModel) Update(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.updateKey(msg)

	case tea.MouseMotionMsg:
		m.handleMouseMotion(msg)
		updated, cmd := m.sb.Update(msg)
		m.sb = updated
		if m.sb.ConsumeScrollChanged() {
			m.scroll = m.sb.GetScrollY()
			m.clampScroll()
		}
		return cmd

	case tea.MouseClickMsg, tea.MouseReleaseMsg:
		updated, cmd := m.sb.Update(msg)
		m.sb = updated
		if m.sb.ConsumeScrollChanged() {
			m.scroll = m.sb.GetScrollY()
			m.clampScroll()
		}
		return cmd
	}
	return nil
}

func (m *commandPaletteModel) updateKey(keyMsg tea.KeyPressMsg) tea.Cmd {
	switch keyMsg.Code {
	case tea.KeyEsc:
		return nil

	case tea.KeyEnter:
		if len(m.filtered) > 0 && m.idx >= 0 && m.idx < len(m.filtered) {
			item := m.filtered[m.idx]
			if m.callback != nil {
				return m.callback(item.ID)
			}
		}
		return nil

	case tea.KeyUp:
		if m.idx > 0 {
			m.idx--
			m.clampScroll()
		}

	case tea.KeyDown:
		if m.idx < len(m.filtered)-1 {
			m.idx++
			m.clampScroll()
		}

	case tea.KeyPgUp:
		vh := m.visibleHeight()
		m.idx -= vh
		if m.idx < 0 {
			m.idx = 0
		}
		m.clampScroll()

	case tea.KeyPgDown:
		vh := m.visibleHeight()
		m.idx += vh
		if m.idx >= len(m.filtered) {
			m.idx = len(m.filtered) - 1
		}
		m.clampScroll()

	case tea.KeyBackspace:
		if len(m.filter) > 0 {
			runes := []rune(m.filter)
			m.filter = string(runes[:len(runes)-1])
			m.applyFilter()
		}

	default:
		for _, r := range keyMsg.Text {
			if unicode.IsPrint(r) {
				m.filter += string(r)
				m.applyFilter()
			}
		}
	}

	return nil
}

func (m *App) toggleCommandPalette() (tea.Model, tea.Cmd) {
	if m.windowManager.Window("command-palette") != nil {
		m.windowManager.RemoveWindow("command-palette")
		m.commandPaletteModel = nil
		return m, nil
	}

	model := newCommandPaletteModel(m.theme, func(cmdID string) tea.Cmd {
		m.windowManager.RemoveWindow("command-palette")
		m.commandPaletteModel = nil
		return m.executePaletteCommand(cmdID)
	})

	palW := m.width * 50 / 100
	if palW < 50 {
		palW = 50
	}
	if palW > 80 {
		palW = 80
	}

	palH := 14
	if palH > m.height-8 {
		palH = m.height - 8
	}
	if palH < 6 {
		palH = 6
	}

	viewFn := func() string {
		if fw := m.windowManager.Window("command-palette"); fw != nil {
			b := fw.ContentBounds()
			model.width = b.W
			model.height = b.H
			sx, sy := fw.Position()
			model.screenX = sx
			model.screenY = sy
		}
		return model.View(model.width)
	}
	ansiWidget := rendering.NewANSIAdapter(viewFn)

	fw := wm.NewFloatingWindow("command-palette", "", ansiWidget,
		palW, palH,
		wm.WithDecoration(true),
		wm.WithResizable(false),
		wm.WithClosable(true),
		wm.WithClickOutside(wm.ClickOutsideKeepRoute),
	)
	fw.SetPosition((m.width-palW)/2, 2)
	m.windowManager.AddWindow(fw)

	m.commandPaletteModel = model
	return m, nil
}

func (m *App) executePaletteCommand(cmdID string) tea.Cmd {
	var cmds []tea.Cmd

	switch cmdID {
	case "composer":
		m.toggleComposer()
		return nil

	case "settings":
		m.settings.SaveSnapshot()
		m.setMode(ModeSettings)

	case "orchestrator-settings":
		m.openOrchestratorSettings()

	case "reset-orchestrator":
		m.resetOrchestratorOverrides()

	case "sessions":
		if m.anyActive() {
			m.statusbar.SetError("Cannot switch sessions while agent is running")
			return nil
		}
		if m.onListSessions != nil {
			sessions := m.onListSessions()
			if len(sessions) > 0 {
				m.launcher = NewLauncherState(sessions)
				m.lastInteractionTime = time.Now()
			} else {
				m.statusbar.SetInfo("No past sessions found")
			}
		}

	case "help":
		var b strings.Builder
		b.WriteString("Available commands:\n")
		for _, c := range input.AvailableCmds() {
			b.WriteString("  " + "/" + c.Label + " - " + c.Desc + "\n")
		}
		b.WriteString("\nKeyboard Shortcuts:\n")
		b.WriteString("  Ctrl+Shift+P  - Command Palette\n")
		b.WriteString("  Ctrl+P        - Command Palette (fallback)\n")
		b.WriteString("  Alt+C         - Open Composer\n")
		b.WriteString("  Ctrl+B        - Toggle Sidebar\n")
		b.WriteString("  Ctrl+O        - Attach File\n")
		b.WriteString("  Ctrl+S        - Open Settings\n")
		b.WriteString("  Ctrl+Q        - Quit\n")
		b.WriteString("  Esc           - Cancel\n")
		if ml := m.activeMsgList(); ml != nil {
			ml.AppendMessage(ttypes.ChatMessage{Role: ttypes.RoleAssistant, Content: b.String(), Time: time.Now()})
		}

	case "undo":
		if m.OnUndoCmd != nil {
			go m.OnUndoCmd("/undo")
		}

	case "yolo":
		if m.yolo {
			m.yolo = false
			m.statusbar.SetInfo("YOLO mode disabled")
			if m.OnYoloCmd != nil {
				go m.OnYoloCmd(false)
			}
		} else {
			m.yoloDlg.Reset()
			m.setMode(ModeYolo)
		}

	case "toggle-sidebar":
		m.ToggleSidebar()

	case "attach-file":
		m.openFilePicker()

	case "toggle-multiagent":
		if m.OnSettingsSave != nil {
			m.settings.SaveSnapshot()
			s := m.settings.ToSettings()
			s.Agents.Enabled = !s.Agents.Enabled
			m.statusbar.SetInfo("Toggling multi-agent mode...")
			onSave := m.OnSettingsSave
			cmds = append(cmds, func() tea.Msg {
				err := onSave(s)
				return ttypes.SettingsSaveResultMsg{Settings: s, Err: err}
			})
		}

	case "quit":
		m.Quit = true
		return tea.Quit
	}

	if len(cmds) > 0 {
		return tea.Batch(cmds...)
	}
	return nil
}
