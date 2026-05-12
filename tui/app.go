package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"crude/fileio"
	"crude/tui/chat"
	"crude/tui/input"
	"crude/tui/settings"
	ttypes "crude/tui/types"
	"crude/tui/widget"
)

var OnHistorySave func([]string)

type mode int

const (
	modeChat mode = iota
	modeSettings
	modeConfirm
	modeYolo
)

type App struct {
	theme  *widget.Theme
	mode   mode
	width  int
	height int

	chat      chat.Model
	input     input.Model
	statusbar *StatusBar
	settings  settings.Model
	confirm   ConfirmDialog
	yoloDlg   YoloDialog

	thinking  bool
	blocking  bool
	streaming bool
	yolo      bool

	inputCh  chan<- string
	cancelCh chan<- struct{}

	OnQuit, OnExitMessage               func()
	OnSettingsSave                      func(Settings)
	OnConfirmResult                     func(ConfirmResult)
	OnYoloCmd                           func(bool)
	OnUndoCmd                           func(string)
	FetchModelsFn                       func(string, string, string) ([]ModelEntry, error)
	GetProviderConfigFn                 func(string) ProviderConfig
	ProviderPresets                     []ProviderPreset

	quit           bool
	lastCancelTime time.Time
	modelName      string
	username       string
	startTime      time.Time

	spinFrame int
	history   []string

	tokenPrompt, tokenCompletion, tokenCached         int
	sessionCost                                       float64
	contextWindow, contextUsed                        int

	lastSettings Settings
}

func NewApp(modelName string, username string, inputCh chan<- string, cancelCh chan<- struct{}, initial Settings, onSave func(Settings), fetchFn func(providerID, baseURL, apiKey string) tea.Cmd, getProviderConfigFn func(providerID string) ProviderConfig) *App {
	cm := chat.NewModel(appTheme)
	im := input.NewModel(appTheme)
	sb := NewStatusBar(appTheme)

	sm := settings.NewModel(appTheme, initial, nil, getProviderConfigFn, fetchFn)

	cd := NewConfirmDialog(appTheme)
	yd := NewYoloDialog(appTheme)

	return &App{
		theme:               appTheme,
		chat:                cm,
		input:               im,
		statusbar:           sb,
		settings:            sm,
		confirm:             cd,
		yoloDlg:             yd,
		inputCh:             inputCh,
		cancelCh:            cancelCh,
		modelName:           modelName,
		username:            username,
		startTime:           time.Now(),
		lastCancelTime:      time.Now(),
		lastSettings:        initial,
	}
}

func (m *App) SetProviderPresets(presets []ProviderPreset) {
	m.ProviderPresets = presets
	m.settings.SetPresets(presets)
}

func (m *App) SetHistory(history []string) {
	m.history = history
	m.input.SetHistory(history)
}

func (m *App) History() []string {
	return m.history
}

func (m *App) Init() tea.Cmd {
	return tea.Batch(tickCmd(), tea.EnterAltScreen)
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return ttypes.TickMsg(t)
	})
}

func (m *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.chat.SetSize(msg.Width, msg.Height)
		m.input.SetWidth(msg.Width)
		m.statusbar.SetWidth(msg.Width)
		return m, nil

	case ttypes.TickMsg:
		m.spinFrame++
		m.statusbar.SetSpinFrame(m.spinFrame)
		m.statusbar.ClearExpired()
		m.chat.InvalidateCache()
		return m, tickCmd()

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC || msg.Type == tea.KeyEsc {
			return m, m.handleCancel(msg)
		}
	}

	switch m.mode {
	case modeSettings:
		updated, cmd, evt := m.settings.Update(msg)
		m.settings = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		switch e := evt.(type) {
		case settings.SaveEvent:
			s := e.Settings
			m.modelName = s.Model
			m.lastSettings = s
			if m.OnSettingsSave != nil {
				m.OnSettingsSave(s)
			}
			m.chat.AppendMessage(chat.ChatMessage{
				Role:    chat.RoleAssistant,
				Content: formatSettingsSummary(s),
				Time:    time.Now(),
			})
			m.mode = modeChat
			m.chat.InvalidateCache()
		case settings.CancelEvent:
			m.settings.RestoreSnapshot()
			s := m.lastSettings
			m.chat.AppendMessage(chat.ChatMessage{
				Role:    chat.RoleAssistant,
				Content: formatSettingsSummary(s),
				Time:    time.Now(),
			})
			m.mode = modeChat
			m.chat.InvalidateCache()
		}

	case modeConfirm:
		m.statusbar.SetConfirming(true)
		updated, result, done := m.confirm.Update(msg)
		m.confirm = updated
		if done {
			m.statusbar.SetConfirming(false)
			m.blocking = false
			m.thinking = true
			if m.OnConfirmResult != nil {
				m.OnConfirmResult(result)
			}
			resultMsg := "✓ Allowed"
			switch result.Choice {
			case ConfirmAlways:
				resultMsg = "✓ Always allowed (session)"
			case ConfirmDeny:
				resultMsg = "✗ Denied"
			}
			m.chat.AppendMessage(chat.ChatMessage{
				Role:    chat.RoleAssistant,
				Content: resultMsg,
				Time:    time.Now(),
			})
			m.mode = modeChat
		}

	case modeYolo:
		m.statusbar.SetConfirming(true)
		updated, enabled, done := m.yoloDlg.Update(msg)
		m.yoloDlg = updated
		if done {
			m.statusbar.SetConfirming(false)
			m.yolo = enabled
			if enabled {
				m.chat.AppendMessage(chat.ChatMessage{
					Role:    chat.RoleAssistant,
					Content: "⚠ YOLO mode ACTIVE — all safety checks disabled. Use /yolo again to restore.",
					Time:    time.Now(),
				})
			} else {
				m.chat.AppendMessage(chat.ChatMessage{
					Role:    chat.RoleAssistant,
					Content: "YOLO mode cancelled. Safety checks remain active.",
					Time:    time.Now(),
				})
			}
			if m.OnYoloCmd != nil {
				m.OnYoloCmd(enabled)
			}
			m.mode = modeChat
		}

	case modeChat:
		updated, cmd, evt := m.input.Update(msg, m.thinking && !m.blocking, m.blocking)
		m.input = updated
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		switch e := evt.(type) {
		case input.SubmitEvent:
			cmds = append(cmds, m.submitInput(e.Text))
		case input.SlashCommandEvent:
			cmds = append(cmds, m.executeSlashCommand(e.Command))
		case input.SettingsOpenEvent:
			m.settings.SaveSnapshot()
			m.mode = modeSettings
		}

		updatedChat, chatCmd := m.chat.Update(msg)
		m.chat = updatedChat
		if chatCmd != nil {
			cmds = append(cmds, chatCmd)
		}
	}

	if msg, ok := msg.(ttypes.AgentMsg); ok {
		cmds = append(cmds, m.handleAgentEvent(ttypes.AgentEventMsg(msg)))
	}

	switch msg := msg.(type) {
	case ttypes.ErrorMsg:
		m.statusbar.SetError(string(msg))
	case ttypes.UpdateModelMsg:
		m.modelName = string(msg)
		m.chat.SetModelName(string(msg))
	case ttypes.OpenSettingsMsg:
		m.settings.SaveSnapshot()
		m.mode = modeSettings
	case ttypes.ModelsListMsg:
		m.settings.SetModels([]ttypes.ModelEntry(msg), false)
	case ttypes.OracleModelsListMsg:
		m.settings.SetModels([]ttypes.ModelEntry(msg), true)
	case ttypes.ModelsErrorMsg:
		m.settings.SetModelsError(string(msg))
	case ttypes.OracleModelsErrorMsg:
		m.settings.SetModelsError(string(msg))
	case ttypes.ClipboardCopiedMsg:
		m.statusbar.SetInfo("copied to clipboard")
	case ttypes.ClipboardErrorMsg:
		m.statusbar.SetError(string(msg))
	case ttypes.UndoResultMsg:
		if msg.Error != "" {
			m.statusbar.SetError(msg.Error)
		}
		if msg.Desc != "" {
			m.chat.AppendMessage(chat.ChatMessage{
				Role:    chat.RoleAssistant,
				Content: msg.Desc,
				Time:    time.Now(),
			})
		}
	}

	return m, tea.Batch(cmds...)
}

func (m *App) submitInput(text string) tea.Cmd {
	m.chat.AppendMessage(chat.ChatMessage{
		Role:    chat.RoleUser,
		Content: text,
		Time:    time.Now(),
	})
	m.history = append(m.history, text)
	if len(m.history) > 500 {
		m.history = m.history[len(m.history)-500:]
	}
	if OnHistorySave != nil {
		OnHistorySave(m.history)
	}
	m.thinking = true
	m.streaming = false
	m.chat.ScrollToBottom()
	ch := m.inputCh
	return func() tea.Msg {
		select {
		case ch <- text:
			return nil
		default:
			return ttypes.ErrorMsg("input channel blocked - please try again")
		}
	}
}

func (m *App) executeSlashCommand(cmd string) tea.Cmd {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	cmdName := parts[0]
	m.chat.AppendMessage(chat.ChatMessage{
		Role:    chat.RoleUser,
		Content: cmd,
		Time:    time.Now(),
	})

	switch cmdName {
	case "/settings":
		m.settings.SaveSnapshot()
		m.mode = modeSettings
	case "/provider":
		m.settings.SaveSnapshot()
		m.mode = modeSettings
		m.settings.FocusField(0)
	case "/model":
		m.settings.SaveSnapshot()
		m.mode = modeSettings
		m.settings.FocusField(3)
	case "/oracle-model":
		m.settings.SaveSnapshot()
		m.mode = modeSettings
		m.settings.FocusField(4)
	case "/api-key":
		m.settings.SaveSnapshot()
		m.mode = modeSettings
		m.settings.FocusField(2)
	case "/effort":
		efforts := []string{"none", "low", "medium", "high", "xhigh"}
		idx := 0
		for i, v := range efforts {
			if v == m.lastSettings.ThinkingEffort {
				idx = i
				break
			}
		}
		idx = (idx + 1) % len(efforts)
		m.lastSettings.ThinkingEffort = efforts[idx]
		if m.OnSettingsSave != nil {
			go m.OnSettingsSave(m.lastSettings)
		}
		m.chat.AppendMessage(chat.ChatMessage{
			Role:    chat.RoleAssistant,
			Content: fmt.Sprintf("Thinking effort: %s", efforts[idx]),
			Time:    time.Now(),
		})
	case "/help":
		var b strings.Builder
		b.WriteString("Available commands:\n  /settings     Open general settings\n  /provider     Select provider\n  /model        Select model\n  /oracle-model Select oracle model\n  /api-key      Set API key\n  /effort       Cycle thinking effort\n  /undo         Undo last file operation\n  /undo-n       Undo last N operations\n  /undo-all     Undo all operations\n  /changes      Show changed files\n  /yolo         Toggle YOLO mode\n  /help         Show this help")
		m.chat.AppendMessage(chat.ChatMessage{Role: chat.RoleAssistant, Content: b.String(), Time: time.Now()})
	case "/yolo":
		if m.yolo {
			m.yolo = false
			m.chat.AppendMessage(chat.ChatMessage{
				Role:    chat.RoleAssistant,
				Content: "⚠ YOLO mode disabled. Safety checks restored.",
				Time:    time.Now(),
			})
			if m.OnYoloCmd != nil {
				go m.OnYoloCmd(false)
			}
		} else {
			m.yoloDlg.Reset()
			m.mode = modeYolo
		}
	case "/undo", "/undo-n", "/undo-all", "/changes":
		if m.OnUndoCmd != nil {
			go m.OnUndoCmd(cmd)
		}
	default:
		m.statusbar.SetError(fmt.Sprintf("Unknown command: %s (try /help)", cmdName))
	}

	return nil
}

func (m *App) handleCancel(msg tea.KeyMsg) tea.Cmd {
	now := time.Now()

	if m.mode == modeSettings {
		m.mode = modeChat
		m.settings.RestoreSnapshot()
		m.chat.AppendMessage(chat.ChatMessage{
			Role:    chat.RoleAssistant,
			Content: formatSettingsSummary(m.lastSettings),
			Time:    time.Now(),
		})
		return nil
	}

	if m.mode == modeYolo {
		m.yoloDlg.Dismiss()
		m.mode = modeChat
		return nil
	}

	if m.mode == modeConfirm {
		m.confirm.Dismiss()
		m.blocking = false
		m.mode = modeChat
		if m.OnConfirmResult != nil {
			m.OnConfirmResult(ConfirmResult{Choice: ConfirmDeny})
		}
		return nil
	}

	if m.thinking {
		select {
		case m.cancelCh <- struct{}{}:
		default:
		}
		m.statusbar.SetInfo("cancelling...")
		return nil
	}

	if now.Sub(m.lastCancelTime) < 2*time.Second {
		m.quit = true
		return tea.Quit
	}
	m.lastCancelTime = now
	m.statusbar.SetError("press ctrl+c or esc again to exit")
	return nil
}

func (m *App) handleAgentEvent(evt ttypes.AgentEventMsg) tea.Cmd {
	switch evt.Type {
	case "phase":
		if phase, ok := evt.Data.(string); ok {
			m.statusbar.SetPhase(phase)
			m.statusbar.statusLine = phase
		}

	case "thinking":
		if s, ok := evt.Data.(string); ok && s != "" {
			m.chat.AppendThinking(s)
			m.thinking = true
		}

	case "stream_token":
		if s, ok := evt.Data.(string); ok && s != "" {
			m.chat.AppendToken(s)
			m.streaming = true
		}

	case "tool_call_streaming":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name == "" || name == "reply" {
				break
			}
			path, _ := data["path"].(string)
			command, _ := data["command"].(string)
			urlStr, _ := data["url"].(string)
			query, _ := data["query"].(string)
			source, _ := data["source"].(string)
			dest, _ := data["destination"].(string)
			pattern, _ := data["pattern"].(string)

			var target string
			target = path
			if target == "" && command != "" {
				target = command
			}
			if target == "" && source != "" && dest != "" {
				target = source + " → " + dest
			} else if target == "" && source != "" {
				target = source
			}
			if target == "" && urlStr != "" {
				target = urlStr
			}
			if target == "" && query != "" {
				target = query
			}
			if target == "" && pattern != "" {
				target = pattern
			}

			contentLines, _ := data["content_lines"].(float64)
			oldLines, _ := data["old_lines"].(float64)
			newLines, _ := data["new_lines"].(float64)

			if name == "read_file" && target != "" {
				offset, _ := data["offset"].(float64)
				limit, _ := data["limit"].(float64)
				if offset > 0 && limit > 0 {
					target = fmt.Sprintf("%s line %d-%d", target, int(offset), int(offset+limit-1))
				} else if offset > 0 {
					target = fmt.Sprintf("%s line %d+", target, int(offset))
				} else if limit > 0 {
					target = fmt.Sprintf("%s (first %d lines)", target, int(limit))
				}
			}

			data["target"] = target
			data["content_lines_int"] = int(contentLines)
			data["old_lines_int"] = int(oldLines)
			data["new_lines_int"] = int(newLines)

			m.chat.UpdateToolStreaming(toolID, name, data)
		}

	case "tool_call":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name == "" || name == "reply" {
				break
			}
			args, _ := data["args"].(string)
			argsCompact := summarizeToolArgs(name, args)
			m.statusbar.statusLine = toolLabel(name, argsCompact)
			m.chat.UpdateToolCall(toolID, name, argsCompact)
		}

	case "tool_progress":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name == "" || name == "reply" {
				break
			}
			phase, _ := data["phase"].(string)
			message, _ := data["message"].(string)
			path, _ := data["path"].(string)
			linesAdded, _ := data["lines_added"].(float64)
			linesRemoved, _ := data["lines_removed"].(float64)

			if path != "" {
				data["target"] = path
			}
			if linesAdded > 0 {
				data["content_lines_int"] = int(linesAdded)
			}
			if linesRemoved > 0 {
				data["old_lines_int"] = int(linesRemoved)
			}

			m.chat.UpdateToolProgress(toolID, name, phase, message)
			if phase == "done" {
				m.statusbar.statusLine = name
			} else if phase != "" && message != "" {
				m.statusbar.statusLine = message
			}
		}

	case "tool_result":
		if data, ok := evt.Data.(map[string]any); ok {
			toolID, _ := data["tool_id"].(string)
			name, _ := data["name"].(string)
			if name == "" || name == "reply" {
				break
			}
			result, _ := data["result"].(string)
			errFlag, _ := data["error"].(string)
			isError := errFlag == "true"
			m.statusbar.statusLine = name
			m.chat.UpdateToolResult(toolID, name, result, isError)
		}

	case "blocking":
		var data map[string]string
		if d, ok := evt.Data.(map[string]any); ok {
			data = make(map[string]string, len(d))
			for k, v := range d {
				data[k] = fmt.Sprintf("%v", v)
			}
		} else if d, ok := evt.Data.(map[string]string); ok {
			data = d
		} else {
			question := fmt.Sprintf("%v", evt.Data)
			data = map[string]string{"question": question}
		}
		question := data["question"]
		operation := data["operation"]
		tool := data["tool"]
		target := data["target"]
		risk := data["risk"]
		reason := data["reason"]
		suggestion := data["suggestion"]
		m.thinking = false
		m.blocking = true
		m.chat.MarkAllThinkingDone()
		if risk != "" {
			m.confirm.ResetWithDetails(question, operation, tool, target, risk, reason, suggestion)
		} else {
			m.confirm.Reset(question, operation, tool, target)
		}
		m.chat.AppendMessage(chat.ChatMessage{
			Role:    chat.RoleAssistant,
			Content: "⚠  " + question,
			Time:    time.Now(),
		})
		m.mode = modeConfirm

	case "blocking_complete":
		m.blocking = false
		m.confirm.Dismiss()
		m.statusbar.SetConfirming(false)
		m.mode = modeChat

	case "done":
		m.chat.MarkAllThinkingDone()
		m.chat.ClearStreamingTools()
		m.thinking = false
		m.streaming = false

		var content, reason string
		switch d := evt.Data.(type) {
		case string:
			content = d
		case map[string]any:
			content, _ = d["content"].(string)
			reason, _ = d["reason"].(string)
		}

		if reason == "completed" {
			m.statusbar.SetInfo("✓ done")
		} else if reason != "" {
			m.statusbar.SetError("⚠ " + reason)
		}

		if content != "" {
			m.chat.AppendMessage(chat.ChatMessage{
				Role:    chat.RoleAssistant,
				Content: content,
				Time:    time.Now(),
			})
		}
		m.chat.ScrollToBottom()

	case "error":
		m.chat.MarkAllThinkingDone()
		m.chat.ClearStreamingTools()
		errMsg := fmt.Sprintf("%v", evt.Data)
		m.chat.AppendMessage(chat.ChatMessage{Role: chat.RoleError, Content: errMsg, Time: time.Now()})
		m.statusbar.SetError(errMsg)
		m.thinking = false
		m.streaming = false
		m.chat.ScrollToBottom()

	case "usage":
		switch d := evt.Data.(type) {
		case map[string]any:
			prompt, _ := d["prompt"].(float64)
			completion, _ := d["completion"].(float64)
			cached, _ := d["cached"].(float64)
			cost, _ := d["cost"].(float64)
			ctxWindow, _ := d["context_window"].(float64)
			ctxUsed, _ := d["context_used"].(float64)
			m.tokenPrompt += int(prompt)
			m.tokenCompletion += int(completion)
			m.tokenCached += int(cached)
			m.sessionCost += cost
			if int(ctxWindow) > 0 {
				m.contextWindow = int(ctxWindow)
				m.contextUsed = int(ctxUsed)
			}
			m.statusbar.UpdateUsage(m.tokenPrompt, m.tokenCompletion, m.tokenCached, m.sessionCost, m.contextWindow, m.contextUsed)
		case map[string]int:
			m.tokenCached += d["prompt_cached"]
			m.tokenCompletion += d["completion"]
			m.tokenPrompt += d["prompt"]
			if cw := d["context_window"]; cw > 0 {
				m.contextWindow = cw
				m.contextUsed = d["prompt"]
			}
			m.statusbar.UpdateUsage(m.tokenPrompt, m.tokenCompletion, m.tokenCached, m.sessionCost, m.contextWindow, m.contextUsed)
		}

	case "retry":
		switch d := evt.Data.(type) {
		case map[string]any:
			attempt := 0
			switch a := d["attempt"].(type) {
			case float64:
				attempt = int(a)
			case int:
				attempt = a
			}
			backoff, _ := d["backoff"].(float64)
			m.statusbar.SetRetry(fmt.Sprintf("⚠ retrying in %.0fs... (attempt %d)", backoff, attempt))
		case string:
			m.statusbar.SetRetry(d)
		}

	case "async_command_status":
		if cmds, ok := evt.Data.([]fileio.AsyncCmdInfo); ok {
			m.statusbar.SetAsyncCommands(cmds)
		}
	}

	return nil
}

func (m *App) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	pad := strings.Repeat(" ", appTheme.Spacing.PadLR)
	innerW := m.width - appTheme.Spacing.PadLR*2
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

	dim := lipgloss.NewStyle().Foreground(appTheme.Colors.Muted)

	add("")

	uptime := time.Since(m.startTime).Truncate(time.Second)
	left := appTheme.Styles.Header.Render("crude") +
		" " + dim.Render(widget.TruncateLine(m.modelName, innerW/3))

	var middle string
	if m.yolo {
		yoloStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("#ff0000")).Bold(true)
		middle = " " + yoloStyle.Render("⚠ YOLO")
	}

	var rightStr string
	if middle != "" {
		rightStr = middle + " " + dim.Render(uptime.String())
	} else {
		rightStr = dim.Render(uptime.String())
	}
	spacer := innerW - lipgloss.Width(left) - lipgloss.Width(rightStr)
	if spacer < 0 {
		spacer = 0
	}
	add(left + strings.Repeat(" ", spacer) + rightStr)
	addFull(appTheme.Styles.Separator.Render(strings.Repeat("─", m.width)))

	chatH := m.chatAreaHeight()
	chatLines := m.renderChatArea()
	total := len(chatLines)

	chatOffset := m.chat.ChatOffset()
	if chatOffset > total-chatH {
		chatOffset = total - chatH
	}
	if chatOffset < 0 {
		chatOffset = 0
	}

	endIdx := total - chatOffset
	startIdx := endIdx - chatH
	if startIdx < 0 {
		startIdx = 0
	}
	if endIdx > total {
		endIdx = total
	}
	visible := chatLines[startIdx:endIdx]

	emptyTop := chatH - len(visible)
	for i := 0; i < emptyTop; i++ {
		add("")
	}
	for _, line := range visible {
		addFull(line)
	}

	asyncLines := m.statusbar.RenderAsyncBar()
	if len(asyncLines) > 0 {
		add("")
		addFull(dim.Render(strings.Repeat("─", m.width)))
	}
	for _, al := range asyncLines {
		addFull(al)
	}

	inputView := m.input.View(m.width, m.height, m.mode == modeSettings, m.mode == modeConfirm)

	addFull(appTheme.Styles.Separator.Render(strings.Repeat("─", m.width)))

	if m.mode == modeSettings {
		addFull("  " + dim.Render("[ interactive settings active · arrows/tab: navigate · enter: edit · ctrl+s: save · esc: return ]"))
	} else if m.mode == modeConfirm {
		addFull("  " + dim.Render("[ confirmation required · ← →: choose · enter: select · y/a/n: quick select · esc: deny ]"))
	} else {
		for _, il := range strings.Split(inputView, "\n") {
			addFull(il)
		}
	}

	addFull(appTheme.Styles.Separator.Render(strings.Repeat("─", m.width)))

	statusView := m.statusbar.View()
	addFull(statusView)

	add("")

	return strings.Join(lines, "\n")
}

func (m *App) chatAreaHeight() int {
	if m.mode == modeSettings || m.mode == modeConfirm || m.mode == modeYolo {
		h := m.height - 7
		if h < 1 {
			h = 1
		}
		return h
	}
	inputLineCount := m.input.Height()
	reserved := 2 + 1 + inputLineCount + 1 + 1 + 1
	h := m.height - reserved
	if h < 1 {
		h = 1
	}
	return h
}

func (m *App) renderChatArea() []string {
	return chat.RenderAllMessages(m.chat.Messages(), appTheme, m.width, m.spinFrame, m.chat.MdRenderer(), m.modelName, m.streaming, m.height, m.username)
}
