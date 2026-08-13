package input

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/rendering"
	"crude/tui/ui"
)

const pasteInlineThreshold = 16 * 1024

func (m Model) Update(msg tea.Msg, thinking, blocking bool) (Model, tea.Cmd, Event) {
	m.thinking = thinking
	m.blocking = blocking

	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		return m.handleMouse(mouseMsg)
	}

	if m.slashActive {
		return m.updateSlash(msg)
	}

	switch msg := msg.(type) {
	case tea.PasteMsg:
		return m.handlePaste(msg.Content)

	case tea.KeyPressMsg:
		if m.showHint {
			m.showHint = false
		}

		if msg.String() == "ctrl+o" {
			return m, nil, AttachFilesEvent{}
		}

		// Alt+\ is a fallback newline shortcut that works on Windows Terminal,
		// where Alt+Enter is intercepted as "toggle fullscreen".
		if msg.Mod.Contains(tea.ModAlt) && msg.Code == '\\' {
			cmd := m.input.Update(msg, false)
			return m, cmd, nil
		}

		switch msg.Code {
		case tea.KeyEnter:
			if msg.Mod.Contains(tea.ModAlt) {
				cmd := m.input.Update(msg, false)
				return m, cmd, nil
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil, nil
			}
			refIDs := m.ReferencedIDs()
			m.appendHistory(text)
			m.input.Clear()
			m.slashActive = false
			m.histIdx = -1
			m.histDraft = ""
			m.histPrefix = ""
			if strings.HasPrefix(text, "/") {
				return m, nil, SlashCommandEvent{Command: text}
			}
			if m.thinking && !m.blocking {
				return m, nil, BtwSubmitEvent{Text: text, ReferenceIDs: refIDs}
			}
			return m, nil, SubmitEvent{Text: text, ReferenceIDs: refIDs}

		case tea.KeyUp:
			if m.input.CursorVisualLine() == 0 {
				m.browseHistoryUp()
				return m, nil, nil
			}
			cmd := m.input.Update(msg, false)
			return m, cmd, nil

		case tea.KeyDown:
			if m.input.CursorVisualLine() >= m.input.TotalVisualLines()-1 {
				m.browseHistoryDown()
				return m, nil, nil
			}
			cmd := m.input.Update(msg, false)
			return m, cmd, nil

		case tea.KeyBackspace, tea.KeyDelete:
			if id, deleted := m.deleteReferenceAtCursor(msg.Code == tea.KeyBackspace); deleted {
				m.syncSlashState()
				return m, nil, RefSelectEvent{ID: id}
			}
			if m.histIdx >= 0 {
				m.histPrefix = strings.TrimSpace(m.input.Value())
				m.histDraft = m.input.Value()
				m.histIdx = -1
			}
			cmd := m.input.Update(msg, false)
			m.syncSlashState()
			return m, cmd, nil

		default:
			if msg.Code == tea.KeySpace && msg.Text == " " {
				if id := m.ActiveReferenceID(); id != "" {
					return m, nil, RefOpenEvent{ID: id}
				}
			}
			if msg.Mod.Contains(tea.ModCtrl) && msg.Code == 's' {
				return m, nil, SettingsOpenEvent{}
			}
			if m.histIdx >= 0 {
				m.histPrefix = strings.TrimSpace(m.input.Value())
				m.histDraft = m.input.Value()
				m.histIdx = -1
			}
			cmd := m.input.Update(msg, false)
			m.applyReferenceRanges()
			m.syncSlashState()
			return m, cmd, nil
		}
	}

	return m, nil, nil
}

func (m Model) handlePaste(content string) (Model, tea.Cmd, Event) {
	content = rendering.SanitizeForTerminal(content)
	if content == "" {
		return m, nil, nil
	}

	if m.refStore == nil || (utf8.RuneCountInString(content) < 240 && !strings.Contains(content, "\n")) {
		m.input.InsertString(content)
		m.syncSlashState()
		return m, nil, nil
	}

	inline := len(content) <= pasteInlineThreshold
	kind := sessiondb.KindPastedText
	title := "Pasted text"
	if !inline {
		kind = sessiondb.KindPastedTextFile
		title = "Pasted text file"
	}
	ref, err := m.refStore.CreateReference(sessiondb.CreateRequest{
		Kind:    kind,
		Title:   title,
		Mime:    "text/plain; charset=utf-8",
		Content: content,
		Inline:  inline,
	})
	if err != nil {
		m.input.InsertString(content)
		m.syncSlashState()
		return m, nil, nil
	}
	m.InsertReference(ref)
	m.syncSlashState()
	return m, nil, RefSelectEvent{ID: ref.ID}
}

func (m *Model) deleteReferenceAtCursor(backspace bool) (string, bool) {
	pos := m.input.CursorFlat()
	for _, rr := range m.RefRanges() {
		deleteIt := false
		if backspace {
			deleteIt = pos > rr.Start && pos <= rr.End
		} else {
			deleteIt = pos >= rr.Start && pos < rr.End
		}
		if !deleteIt {
			continue
		}
		m.input.DeleteRange(rr.Start, rr.End)
		m.input.SetCursorFlat(rr.Start)
		if m.refStore != nil {
			_ = m.refStore.DeleteReference(rr.ID)
		}
		return rr.ID, true
	}
	return "", false
}

func (m *Model) syncSlashState() {
	inputStr := m.input.Value()
	if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") && !strings.Contains(inputStr, "\n") {
		m.slashActive = true
		m.setSlashFilter(strings.TrimPrefix(inputStr, "/"))
	} else {
		m.slashActive = false
	}
}

func (m *Model) setSlashFilter(filter string) {
	m.slashFilter = filter
	filter = strings.ToLower(filter)
	var items []ui.ListItem
	for _, c := range availableCmds {
		id := strings.ToLower(c.Name)
		desc := strings.ToLower(c.Desc)
		if filter == "" || strings.HasPrefix(id, filter) || strings.Contains(desc, filter) {
			items = append(items, ui.ListItem{
				ID:     c.Name,
				Label:  c.Label,
				Detail: c.Desc,
				Icon:   c.Icon,
			})
		}
	}
	prevID := ""
	if item, ok := m.slashList.Selected(); ok {
		prevID = item.ID
	}
	m.slashList.SetItems(items)
	if prevID != "" {
		for i, it := range items {
			if it.ID == prevID {
				m.slashList.SelectIndex(i)
				break
			}
		}
	}
}

func (m Model) updateSlash(msg tea.Msg) (Model, tea.Cmd, Event) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil, nil
	}

	switch keyMsg.Code {
	case tea.KeyEsc:
		m.slashActive = false
		return m, nil, nil

	case tea.KeyTab:
		if item, ok := m.slashList.Selected(); ok {
			m.input.SetValue("/" + item.ID + " ")
		}
		m.slashActive = false
		return m, nil, nil

	case tea.KeyEnter:
		res := m.slashList.Handle(ui.EventContext{}, ui.KeyEvent(keyMsg))
		if res.Action != nil && res.Action.ID == "slash.select" {
			if item, ok := m.slashList.Selected(); ok {
				cmd := "/" + item.ID
				m.appendHistory(cmd)
				m.input.Clear()
				m.slashActive = false
				m.histIdx = -1
				m.histDraft = ""
				m.histPrefix = ""
				return m, nil, SlashCommandEvent{Command: cmd}
			}
		}
		text := strings.TrimSpace(m.input.Value())
		if text != "" && strings.HasPrefix(text, "/") {
			m.appendHistory(text)
			m.input.Clear()
			m.slashActive = false
			m.histIdx = -1
			m.histDraft = ""
			m.histPrefix = ""
			return m, nil, SlashCommandEvent{Command: text}
		}
		m.slashActive = false
		return m, nil, nil

	case tea.KeyUp:
		res := m.slashList.Handle(ui.EventContext{}, ui.KeyEvent(keyMsg))
		return m, res.Cmd, nil

	case tea.KeyDown:
		res := m.slashList.Handle(ui.EventContext{}, ui.KeyEvent(keyMsg))
		return m, res.Cmd, nil

	case tea.KeyBackspace, tea.KeyDelete:
		cmd := m.input.Update(msg, false)
		inputStr := m.input.Value()
		if inputStr == "" || !strings.HasPrefix(inputStr, "/") || strings.Contains(inputStr, " ") || strings.Contains(inputStr, "\n") {
			m.slashActive = false
		} else {
			m.setSlashFilter(strings.TrimPrefix(inputStr, "/"))
		}
		return m, cmd, nil

	default:
		cmd := m.input.Update(msg, false)
		inputStr := m.input.Value()
		if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") && !strings.Contains(inputStr, "\n") {
			m.setSlashFilter(strings.TrimPrefix(inputStr, "/"))
		} else {
			m.slashActive = false
		}
		return m, cmd, nil
	}
}

func (m *Model) appendHistory(text string) {
	if len(m.history) > 0 && m.history[len(m.history)-1] == text {
		return
	}
	m.history = append(m.history, text)
	if len(m.history) > 500 {
		m.history = m.history[len(m.history)-500:]
	}
}

func (m Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd, Event) {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		switch msg.Button {
		case tea.MouseLeft:
			pos, ok := m.MouseToInputPos(msg.X, msg.Y)
			if ok {
				m.input.SetSelection(pos, pos)
				m.input.SetCursorFlat(pos)
			} else {
				m.input.ClearSelection()
			}
		case tea.MouseWheelUp, tea.MouseWheelDown:
			if msg.Button == tea.MouseWheelUp {
				m.browseHistoryUp()
			} else {
				m.browseHistoryDown()
			}
		}

	case tea.MouseMotionMsg:
		if m.input.IsSelecting() {
			pos, ok := m.MouseToInputPos(msg.X, msg.Y)
			if ok {
				m.input.UpdateSelectionEnd(pos)
			} else if msg.Y < m.textAreaY() {
				m.input.UpdateSelectionEnd(0)
			} else {
				m.input.UpdateSelectionEnd(m.input.Len())
			}
		}

	case tea.MouseReleaseMsg:
		if m.input.IsSelecting() {
			// Keep the selection range so Ctrl+C can copy it; only
			// clear the drag flag.
			m.input.SetSelecting(false)
		}
	}

	return m, nil, nil
}
