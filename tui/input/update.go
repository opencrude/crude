package input

import (
	"fmt"
	"strings"
	"unicode"

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
			if event, ok := m.commitCommandLine(text); ok {
				return m, nil, event
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
	content = rendering.SanitizeForTerminal(ui.PastedLineBreaks(content))
	if content == "" {
		return m, nil, nil
	}
	if fragment, ok := m.clipboard.match(content); ok {
		// Text this shell copied from a composer: its attachments come too.
		return m, nil, m.beginFragmentPaste(fragment)
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

// slashToken is a command name being typed at the caret: "/" at the start of
// the draft or after whitespace, through the caret, with whitespace or the end
// of the draft after it. Offsets are flat rune offsets of the draft.
type slashToken struct {
	start, end int
	whole      bool
}

// slashTokenAtCursor finds the command typed at the caret. A command can be
// started anywhere in a prompt, so the user does not have to delete a draft
// to open settings, compact or switch a session.
func (m *Model) slashTokenAtCursor() (slashToken, string, bool) {
	if m.input.HasActiveSelection() {
		return slashToken{}, "", false
	}
	text := []rune(m.input.Value())
	pos := m.input.CursorFlat()
	if pos < 0 || pos > len(text) || pos < len(text) && !unicode.IsSpace(text[pos]) {
		return slashToken{}, "", false
	}
	start := pos
	for start > 0 && !unicode.IsSpace(text[start-1]) {
		start--
	}
	if start == pos || text[start] != '/' {
		return slashToken{}, "", false
	}
	for _, ref := range m.RefRanges() {
		if ref.Start < pos && ref.End > start {
			return slashToken{}, "", false
		}
	}
	token := slashToken{start: start, end: pos}
	token.whole = start == 0 && strings.TrimSpace(string(text[pos:])) == ""
	return token, string(text[start+1 : pos]), true
}

func (m *Model) syncSlashState() {
	token, filter, ok := m.slashTokenAtCursor()
	if !ok {
		m.slashActive = false
		return
	}
	m.slash = token
	m.slashActive = true
	m.setSlashFilter(filter)
	// Inside a prompt only a command name that starts with the typed text
	// opens the list, so a path such as /usr/bin stays plain prose.
	if !token.whole && !m.slashNameMatches(filter) {
		m.slashActive = false
	}
}

// slashNameMatches reports a command whose name starts with filter.
func (m *Model) slashNameMatches(filter string) bool {
	filter = strings.ToLower(filter)
	for _, c := range availableCmds {
		if strings.HasPrefix(strings.ToLower(c.Name), filter) {
			return true
		}
	}
	return false
}

func (m *Model) setSlashFilter(filter string) {
	m.slashFilter = filter
	filter = strings.ToLower(filter)
	// Commands whose name starts with the filter come first, so Tab and Enter
	// complete the command being typed; description matches follow.
	var named, described []ui.ListItem
	for _, c := range availableCmds {
		item := ui.ListItem{ID: c.Name, Label: c.Label, Detail: c.Desc, Icon: c.Icon}
		switch {
		case filter == "" || strings.HasPrefix(strings.ToLower(c.Name), filter):
			named = append(named, item)
		case strings.Contains(strings.ToLower(c.Desc), filter):
			described = append(described, item)
		}
	}
	items := append(named, described...)
	prevID := ""
	if item, ok := m.slashList.Selected(); ok {
		prevID = item.ID
	}
	m.slashList.SetItems(items)
	if prevID != "" {
		for i, it := range items {
			// A retained description match must not override an exact name
			// prefix as the query changes. Keep selection only within the
			// highest available relevance tier.
			if it.ID == prevID && (len(named) == 0 || i < len(named)) {
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
	if !m.slash.whole {
		return m.commitSlashToken(item.ID), true
	}
	return m.commitSlashCommand("/" + item.ID), true
}

// commitSlashToken runs a command chosen inside a prompt. Only the typed
// command name leaves the draft; the prompt, its attachments, undo history
// and caret stay, and the command gets none of the draft's attachments.
func (m *Model) commitSlashToken(name string) SlashCommandEvent {
	text := []rune(m.input.Value())
	start, end := m.slash.start, m.slash.end
	if start < 0 || end > len(text) || start > end {
		m.slashActive = false
		return SlashCommandEvent{Command: "/" + name}
	}
	switch {
	case end < len(text) && text[end] == ' ':
		end++
	case end == len(text) && start > 0 && text[start-1] == ' ':
		start--
	}
	m.input.DeleteRange(start, end)
	m.input.SetCursorFlat(start)
	m.applyReferenceRanges()
	m.slashActive = false
	return SlashCommandEvent{Command: "/" + name}
}

// commitCommandLine runs a draft whose first word names a command. A command
// with arguments takes the rest of the draft, as if picked from the list. Any
// other command runs alone, and the prompt after it stays in the composer. A
// first word that names no command leaves the draft a normal prompt, so a
// prompt that starts with a path is sent, never lost.
func (m *Model) commitCommandLine(text string) (Event, bool) {
	if !strings.HasPrefix(text, "/") {
		return nil, false
	}
	fields := strings.Fields(text)
	command, known := slashCommand(fields[0])
	if !known {
		if len(fields) == 1 && !strings.ContainsAny(strings.TrimPrefix(fields[0], "/"), "/.") {
			// A mistyped command alone: say so and keep it for correction.
			return UnknownCommandEvent{Name: fields[0]}, true
		}
		return nil, false
	}
	if command.Args || len(fields) == 1 {
		return m.commitSlashCommand(text), true
	}
	runes := []rune(m.input.Value())
	start := len([]rune(text)) - len([]rune(strings.TrimLeft(text, " \t\r\n")))
	m.slash = slashToken{start: start, end: start + len([]rune(fields[0]))}
	if m.slash.end > len(runes) {
		return nil, false
	}
	return m.commitSlashToken(command.Name), true
}

func (m *Model) commitSlashCommand(command string) SlashCommandEvent {
	event := SlashCommandEvent{Command: command, ReferenceIDs: m.ReferencedIDs()}
	fields := strings.Fields(command)
	if len(fields) > 0 && len(event.ReferenceIDs) > 0 &&
		(strings.EqualFold(fields[0], "/goal") || strings.EqualFold(fields[0], "/loop")) {
		// These session commands store plain text only. Let the shell explain
		// the refusal while the original draft and attachment identities stay.
		m.slashActive = false
		return event
	}
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
		item, ok := m.slashList.Selected()
		if ok && !m.slash.whole {
			// Inside a prompt there are no arguments to complete: Tab runs
			// the chosen command like Enter.
			return m, nil, m.commitSlashToken(item.ID)
		}
		if ok {
			m.invalidatePendingPastes()
			m.input.SetValue("/" + item.ID + " ")
		}
		m.slashActive = false
		return m, nil, nil

	case tea.KeyEnter:
		if ui.IsNewlineChord(keyMsg) && !m.slash.whole {
			m.slashActive = false
			return m, m.input.Update(msg, false), nil
		}
		res := m.slashList.Handle(ui.EventContext{}, ui.KeyEvent(keyMsg))
		if event, ok := m.activateSlashSelection(res.Action); ok {
			return m, res.Cmd, event
		}
		m.slashActive = false
		if m.slash.whole {
			if event, ok := m.commitCommandLine(m.input.Value()); ok {
				return m, nil, event
			}
		}
		return m, nil, nil

	case tea.KeyUp:
		res := m.slashList.Handle(ui.EventContext{}, ui.KeyEvent(keyMsg))
		return m, res.Cmd, nil

	case tea.KeyDown:
		res := m.slashList.Handle(ui.EventContext{}, ui.KeyEvent(keyMsg))
		return m, res.Cmd, nil

	default:
		// Every other key edits the draft through the ordinary composer
		// path (references, history, Left/Right, Backspace, typing); the
		// command list then follows the token at the caret.
		m.slashActive = false
		updated, cmd, event := m.update(msg, m.thinking, m.blocking)
		return updated, cmd, event
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
	if chord := ui.ClipboardChordFor(key); chord == ui.ClipboardChordCopy || chord == ui.ClipboardChordCut {
		m.rememberClipboardChord(chord)
	}
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
	m.ClearDraft()
	return m, nil, true
}
