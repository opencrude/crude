package input

import (
	"strings"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/promptref"
	"crude/tui/components"
	"crude/tui/rendering"
)

const pasteInlineThreshold = 16 * 1024

func isBase64Image(s string) bool {
	return strings.HasPrefix(s, "data:image/") || (len(s) > 100 && strings.HasPrefix(s, "/9j/"))
}

func detectImageMime(s string) string {
	if strings.HasPrefix(s, "data:image/png;") {
		return "image/png"
	}
	if strings.HasPrefix(s, "data:image/jpeg;") || strings.HasPrefix(s, "data:image/jpg;") {
		return "image/jpeg"
	}
	if strings.HasPrefix(s, "data:image/gif;") {
		return "image/gif"
	}
	if strings.HasPrefix(s, "data:image/webp;") {
		return "image/webp"
	}
	if strings.HasPrefix(s, "/9j/") {
		return "image/jpeg"
	}
	return "image/png"
}

func extractBase64Data(s string) string {
	if idx := strings.Index(s, ";base64,"); idx >= 0 {
		return s[idx+8:]
	}
	return s
}

func (m Model) Update(msg tea.Msg, thinking, blocking bool, chatAreaHeight int, showHint bool) (Model, tea.Cmd, Event) {
	m.thinking = thinking
	m.blocking = blocking

	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		return m.handleMouse(mouseMsg, chatAreaHeight, showHint)
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
		if msg.Mod.Contains(tea.ModAlt) && len(msg.Text) == 1 && msg.Text[0] == '\\' {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
			return m, cmd, nil
		}

		switch msg.Code {
		case tea.KeyEnter:
			if msg.Mod.Contains(tea.ModAlt) {
				var cmd tea.Cmd
				m.input, cmd = m.input.Update(msg, false)
				return m, cmd, nil
			}
			text := strings.TrimSpace(m.input.Value())
			if text == "" {
				return m, nil, nil
			}
			expandedText := strings.TrimSpace(m.ExpandedValue())
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
				return m, nil, BtwSubmitEvent{Text: expandedText}
			}
			return m, nil, SubmitEvent{Text: expandedText}

		case tea.KeyUp:
			if m.input.CursorVisualLine() == 0 {
				m.browseHistoryUp()
				return m, nil, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
			return m, cmd, nil

		case tea.KeyDown:
			if m.input.CursorVisualLine() >= m.input.TotalVisualLines()-1 {
				m.browseHistoryDown()
				return m, nil, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
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
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
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
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg, false)
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

	if isBase64Image(content) {
		return m.handleImagePaste(content)
	}

	if m.refStore == nil || (utf8.RuneCountInString(content) < 240 && !strings.Contains(content, "\n")) {
		m.input.InsertString(content)
		m.syncSlashState()
		return m, nil, nil
	}

	inline := len(content) <= pasteInlineThreshold
	kind := promptref.KindPastedText
	title := "Pasted text"
	if !inline {
		kind = promptref.KindPastedTextFile
		title = "Pasted text file"
	}
	ref, err := m.refStore.CreateReference(promptref.CreateRequest{
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
	if inline {
		m.InsertReference(ref)
	}
	m.syncSlashState()
	return m, nil, RefSelectEvent{ID: ref.ID}
}

func (m Model) handleImagePaste(content string) (Model, tea.Cmd, Event) {
	if m.refStore == nil {
		return m, nil, nil
	}
	mime := detectImageMime(content)
	b64data := extractBase64Data(content)
	ref, err := m.refStore.CreateReference(promptref.CreateRequest{
		Kind:    promptref.KindPastedImage,
		Title:   "Pasted image",
		Mime:    mime,
		Content: b64data,
		Inline:  false,
	})
	if err != nil {
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
		m.slashList.SetFilter(strings.TrimPrefix(inputStr, "/"))
	} else {
		m.slashActive = false
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
		m.slashList, _ = m.slashList.Update(msg)
		if m.slashList.Action() == components.ListActionSelect {
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
		var cmd tea.Cmd
		m.slashList, cmd = m.slashList.Update(msg)
		return m, cmd, nil

	case tea.KeyDown:
		var cmd tea.Cmd
		m.slashList, cmd = m.slashList.Update(msg)
		return m, cmd, nil

	case tea.KeyBackspace, tea.KeyDelete:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg, false)
		inputStr := m.input.Value()
		if inputStr == "" || !strings.HasPrefix(inputStr, "/") || strings.Contains(inputStr, " ") || strings.Contains(inputStr, "\n") {
			m.slashActive = false
		} else {
			m.slashList.SetFilter(strings.TrimPrefix(inputStr, "/"))
		}
		return m, cmd, nil

	default:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg, false)
		inputStr := m.input.Value()
		if strings.HasPrefix(inputStr, "/") && !strings.Contains(inputStr, " ") && !strings.Contains(inputStr, "\n") {
			m.slashList.SetFilter(strings.TrimPrefix(inputStr, "/"))
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

func (m Model) handleMouse(msg tea.MouseMsg, chatAreaHeight int, showHint bool) (Model, tea.Cmd, Event) {
	switch msg := msg.(type) {
	case tea.MouseClickMsg:
		if msg.Button == tea.MouseLeft {
			pos, ok := m.MouseToInputPos(msg.X, msg.Y, chatAreaHeight, showHint)
			if ok {
				m.input.SetSelection(pos, pos)
				m.input.SetCursorFlat(pos)
			} else {
				m.input.ClearSelection()
			}
		} else if msg.Button == tea.MouseWheelUp || msg.Button == tea.MouseWheelDown {
			if msg.Button == tea.MouseWheelUp {
				m.browseHistoryUp()
			} else {
				m.browseHistoryDown()
			}
		}

	case tea.MouseMotionMsg:
		if m.input.IsSelecting() {
			pos, ok := m.MouseToInputPos(msg.X, msg.Y, chatAreaHeight, showHint)
			if ok {
				m.input.UpdateSelectionEnd(pos)
			} else {
				firstY := m.FirstLineY(chatAreaHeight, showHint, m.thinking, m.input.Len())
				if msg.Y < firstY {
					m.input.UpdateSelectionEnd(0)
				} else {
					m.input.UpdateSelectionEnd(m.input.Len())
				}
			}
		}

	case tea.MouseReleaseMsg:
		if m.input.IsSelecting() {
			selected := m.input.SelectedText()
			m.input.ClearSelection()
			if selected != "" {
				return m, rendering.CopyToClipboardCmd(selected), nil
			}
		}
	}

	return m, nil, nil
}
