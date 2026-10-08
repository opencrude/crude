package input

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/ui"
)

func (m Model) Update(msg tea.Msg, thinking, blocking bool) (Model, tea.Cmd, Event) {
	before := m.draftStateKey()
	updated, cmd, event := m.update(msg, thinking, blocking)
	if updated.draftStateKey() != before {
		updated.draftRevision++
	}
	switch value := event.(type) {
	case SubmitEvent:
		value.DraftRevision = updated.draftRevision
		event = value
	case SteeringSubmitEvent:
		value.DraftRevision = updated.draftRevision
		event = value
	}
	return updated, cmd, event
}

func (m Model) update(msg tea.Msg, thinking, blocking bool) (Model, tea.Cmd, Event) {
	m.thinking = thinking
	m.blocking = blocking
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyEnter && m.historyLoading {
		return m, nil, nil
	}
	switch msg.(type) {
	case tea.KeyPressMsg, tea.PasteMsg:
		m.historyGeneration++
		m.historyLoading = false
	}

	if mouseMsg, ok := msg.(tea.MouseMsg); ok {
		return m.handleMouse(mouseMsg)
	}

	// Shared text-control chords run before every other key layer so a live
	// selection behaves identically while the slash popup is open (copy, cut,
	// Esc drops the selection first).
	if keyMsg, ok := msg.(tea.KeyPressMsg); ok {
		if updated, cmd, handled := m.applyClipboardChord(keyMsg); handled {
			return updated, cmd, nil
		}
	}
	if paste, ok := msg.(tea.PasteMsg); ok {
		return m.handlePaste(paste.Content)
	}
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyEnter && !key.Mod.Contains(tea.ModAlt) && len(m.pendingPastes) > 0 {
		return m, nil, PastePendingEvent{}
	}

	if m.slashActive {
		return m.updateSlash(msg)
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
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
			text := m.input.Value()
			if strings.TrimSpace(text) == "" {
				return m, nil, nil
			}
			refIDs := m.ReferencedIDs()
			if strings.HasPrefix(text, "/") {
				return m, nil, SlashCommandEvent{Command: text, ReferenceIDs: refIDs}
			}
			if m.thinking && !m.blocking {
				return m, nil, SteeringSubmitEvent{Text: text, ReferenceIDs: refIDs, DraftRevision: m.draftRevision}
			}
			return m, nil, SubmitEvent{Text: text, ReferenceIDs: refIDs, DraftRevision: m.draftRevision}

		case tea.KeyUp:
			if m.input.CursorVisualLine() == 0 {
				event := m.browseHistoryUp()
				return m, nil, event
			}
			cmd := m.input.Update(msg, false)
			return m, cmd, nil

		case tea.KeyDown:
			if m.input.CursorVisualLine() >= m.input.TotalVisualLines()-1 {
				event := m.browseHistoryDown()
				return m, nil, event
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

func (m Model) draftStateKey() string {
	return fmt.Sprintf("%v\x00%d\x00%v", m.input.EditState(), m.draftEpoch, m.SnapshotReferenceIDs())
}

func (m Model) handlePaste(content string) (Model, tea.Cmd, Event) {
	content = rendering.SanitizeForTerminal(content)
	if content == "" {
		return m, nil, nil
	}

	if !m.promotePastedText(content) {
		m.input.InsertString(content)
		m.syncSlashState()
		return m, nil, nil
	}

	m.pasteSequence++
	if m.pendingPastes == nil {
		m.pendingPastes = make(map[uint64]bool)
	}
	m.pendingPastes[m.pasteSequence] = true
	return m, nil, PasteAttachmentEvent{ID: m.pasteSequence, Epoch: m.draftEpoch, Content: content}
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
			m.invalidatePendingPastes()
			m.input.SetValue("/" + item.ID + " ")
		}
		m.slashActive = false
		return m, nil, nil

	case tea.KeyEnter:
		refIDs := m.ReferencedIDs()
		res := m.slashList.Handle(ui.EventContext{}, ui.KeyEvent(keyMsg))
		if res.Action != nil && res.Action.ID == "slash.select" {
			if item, ok := m.slashList.Selected(); ok {
				cmd := "/" + item.ID
				m.input.Clear()
				m.invalidatePendingPastes()
				m.slashActive = false
				m.histIdx = -1
				m.histDraft = ""
				m.histPrefix = ""
				return m, nil, SlashCommandEvent{Command: cmd, ReferenceIDs: refIDs}
			}
		}
		text := strings.TrimSpace(m.input.Value())
		if text != "" && strings.HasPrefix(text, "/") {
			m.input.Clear()
			m.invalidatePendingPastes()
			m.slashActive = false
			m.histIdx = -1
			m.histDraft = ""
			m.histPrefix = ""
			return m, nil, SlashCommandEvent{Command: text, ReferenceIDs: refIDs}
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
				return m, nil, m.browseHistoryUp()
			}
			return m, nil, m.browseHistoryDown()
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
		// Copy-on-release, matching the chat message area and docked
		// terminals: a drag writes the selection to the clipboard and drops
		// it, so the drag never leaves stale selection state behind.
		return m, m.input.EndMouseSelection(), nil
	}

	return m, nil, nil
}

// applyClipboardChord applies the shared text-control clipboard chords
// (Ctrl+C / Ctrl+Shift+C copy, Ctrl+X cuts, Esc drops a selection) and the
// composer's own fallback on top of them: an unhandled plain Ctrl+C clears the
// draft, so the key copies when there is a selection and otherwise gives a
// fast "start over". The explicit Ctrl+Shift+C copy never clears anything.
// It reports whether the key was consumed.
func (m Model) applyClipboardChord(key tea.KeyPressMsg) (Model, tea.Cmd, bool) {
	if res, handled := m.input.HandleClipboardKey(key); handled {
		if ui.ClipboardChordFor(key) == ui.ClipboardChordCut && m.input.Value() == "" {
			m.invalidatePendingPastes()
		}
		// Cut and clear can rewrite the draft, so chips and the slash popup
		// are resynced exactly like every other key path.
		m.applyReferenceRanges()
		m.syncSlashState()
		return m, res.Cmd, true
	}
	if ui.ClipboardChordFor(key) != ui.ClipboardChordCopy {
		return m, nil, false
	}
	if m.input.Value() == "" {
		return m, nil, true
	}
	m.ClearInput()
	return m, nil, true
}
