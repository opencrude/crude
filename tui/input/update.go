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
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyEnter && !ui.IsNewlineChord(key) && len(m.pendingPastes) > 0 {
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

		// The newline chord (Shift+Enter, Alt+Enter or Ctrl+J; see
		// ui.NewlineChord) breaks the line; every other Enter sends.
		if ui.IsNewlineChord(msg) {
			cmd := m.input.Update(msg, false)
			return m, cmd, nil
		}

		switch msg.Code {
		case tea.KeyEnter:
			text := m.input.Value()
			if strings.TrimSpace(text) == "" {
				return m, nil, nil
			}
			refIDs := m.ReferencedIDs()
			if strings.HasPrefix(text, "/") {
				return m, nil, SlashCommandEvent{Command: text, ReferenceIDs: refIDs}
			}
			// A pending question may leave the composer available for drafting,
			// but its answer must settle before a prompt can be sent or steered.
			if m.blocking {
				return m, nil, nil
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

// activateSlashSelection consumes the List's exact activation payload, so
// keyboard and pointer selection share command construction and draft cleanup.
func (m *Model) activateSlashSelection(action *ui.Action) (SlashCommandEvent, bool) {
	if action == nil || action.ID != "slash.select" {
		return SlashCommandEvent{}, false
	}
	item, ok := action.Payload.(ui.ListItem)
	if !ok || !item.Selectable() || item.ID == "" {
		return SlashCommandEvent{}, false
	}
	return m.commitSlashCommand("/" + item.ID), true
}

func (m *Model) commitSlashCommand(command string) SlashCommandEvent {
	event := SlashCommandEvent{Command: command, ReferenceIDs: m.ReferencedIDs()}
	m.ClearInput()
	return event
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
		res := m.slashList.Handle(ui.EventContext{}, ui.KeyEvent(keyMsg))
		if event, ok := m.activateSlashSelection(res.Action); ok {
			return m, res.Cmd, event
		}
		text := strings.TrimSpace(m.input.Value())
		if text != "" && strings.HasPrefix(text, "/") {
			event := m.commitSlashCommand(text)
			return m, nil, event
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
	// Older console transports encode wheel as MouseClickMsg. Keep that event's
	// history contract; actual wheel messages over the text body scroll its viewport.
	if click, ok := msg.(tea.MouseClickMsg); ok {
		if click.Button == tea.MouseWheelUp {
			return m, nil, m.browseHistoryUp()
		}
		if click.Button == tea.MouseWheelDown {
			return m, nil, m.browseHistoryDown()
		}
	}
	// Both the bridge and the mounted shell delegate gestures to TextArea. A
	// second press/motion implementation lost Shift, multi-click and unit bounds.
	mouse := msg.Mouse()
	pad, _ := m.inputGeometry(m.width)
	result := m.input.Handle(ui.EventContext{LocalX: mouse.X - pad, LocalY: mouse.Y - m.textAreaY()}, ui.NormalizeEvent(msg))
	return m, result.Cmd, nil
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
