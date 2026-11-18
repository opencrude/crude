package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/shellfooter"
	"crude/tui/ui"
	"crude/types"
)

// Aeron's editor suggestions. The core owns text, context, the model call and
// the ledger. This shell owner holds one typing intent, one request and the
// shown change of the active editor; every async result carries the editor,
// its revision, the workspace generation and the request sequence.
const (
	suggestionGhostOwner = "aeron"
	suggestionViewID     = "editor-suggestion"
	suggestionMinPause   = 0
	suggestionMaxPause   = 150 * time.Millisecond
	suggestionStartPause = 50 * time.Millisecond
	suggestionFooter     = "aeron-suggestions"
)

type suggestionState struct {
	mode     types.EditorPredictionMode
	limit    int64
	status   types.EditorPredictionStatus
	sequence uint64
	intent   *suggestionIntent
	request  *suggestionRequest
	// jump is a change at the next place, shown after the first Tab.
	jump *suggestionJump
	// dismissed remembers the change the user dismissed with Esc, so the same
	// change does not come back until the text near the caret changes.
	dismissed string
	// pause adapts to how many automatic requests a newer key replaced.
	pause              time.Duration
	replaced, answered int
	problem            string
}

type suggestionIntent struct {
	editor     *editor.Editor
	revision   uint64
	generation uint64
	due        time.Time
	deadline   time.Time
	automatic  bool
	cancel     context.CancelFunc
}

type suggestionRequest struct {
	workspace  shellport.Workspace
	editor     *editor.Editor
	source     types.InsightSource
	revision   uint64
	generation uint64
	sequence   uint64
	caret      int
	automatic  bool
	cancel     context.CancelFunc
}

type suggestionJump struct {
	editor   *editor.Editor
	revision uint64
	start    int
	end      int
	text     string
}

type suggestionWakeMsg struct{}

type suggestionResultMsg struct {
	owner  *suggestionRequest
	result types.EditorPredictionResult
	err    error
}

type suggestionStatusMsg struct {
	workspace shellport.Workspace
	status    types.EditorPredictionStatus
	err       error
}

// applySuggestionSettings follows the effective workspace choice.
func (m *App) applySuggestionSettings(settings types.WorkspacePredictionSettings) {
	if m.suggest.mode != settings.Mode {
		m.cancelSuggestion()
		m.suggest.problem = ""
	}
	m.suggest.mode, m.suggest.limit = settings.Mode, settings.HourlyTokenLimit
	if m.suggest.pause == 0 {
		m.suggest.pause = suggestionStartPause
	}
}

func (m *App) suggestionPort() (shellport.Suggestions, bool) {
	port, ok := m.ws.(shellport.Suggestions)
	return port, ok
}

// cancelSuggestion drops the intent, the running request and Aeron's shown
// change. It never touches another producer's inline text.
func (m *App) cancelSuggestion() {
	if intent := m.suggest.intent; intent != nil {
		intent.cancel()
		m.suggest.intent = nil
	}
	if request := m.suggest.request; request != nil {
		request.cancel()
		m.suggest.request = nil
	}
	m.suggest.jump = nil
	if m.EditorState != nil && m.EditorState.Editor != nil && m.EditorState.Editor.GhostOwner() == suggestionGhostOwner {
		m.EditorState.Editor.DismissGhostText()
	}
}

// onSuggestionTyping schedules an automatic request after a short pause.
func (m *App) onSuggestionTyping(ed *editor.Editor, key tea.KeyPressMsg) {
	if m.suggest.request != nil && m.suggest.request.automatic {
		m.suggest.replaced++
		m.adaptSuggestionPause()
	}
	if request := m.suggest.request; request != nil {
		request.cancel()
		m.suggest.request = nil
	}
	m.suggest.jump = nil
	if m.suggest.mode != types.PredictionType || !editorTypingKey(key) && key.Code != tea.KeyEnter {
		return
	}
	m.scheduleSuggestion(ed, m.suggest.pause, true)
}

func (m *App) scheduleSuggestion(ed *editor.Editor, pause time.Duration, automatic bool) {
	if intent := m.suggest.intent; intent != nil {
		intent.cancel()
	}
	base, generation := m.lspRequestContext()
	ctx, cancel := context.WithCancel(base)
	due := time.Now().Add(pause)
	m.suggest.intent = &suggestionIntent{editor: ed, revision: ed.Revision(), generation: generation,
		due: due, deadline: due.Add(5 * time.Second), automatic: automatic, cancel: cancel}
	m.editorCommands = append(m.editorCommands, func() tea.Msg {
		timer := time.NewTimer(max(time.Millisecond, time.Until(due)))
		defer timer.Stop()
		select {
		case <-timer.C:
			return suggestionWakeMsg{}
		case <-ctx.Done():
			return nil
		}
	})
}

// adaptSuggestionPause waits longer when typing keeps replacing requests and
// shorter when answers arrive in use. It never depends on connection speed.
func (m *App) adaptSuggestionPause() {
	total := m.suggest.replaced + m.suggest.answered
	if total < 8 {
		return
	}
	share := float64(m.suggest.replaced) / float64(total)
	switch {
	case share > 0.6:
		m.suggest.pause = min(suggestionMaxPause, m.suggest.pause+10*time.Millisecond)
	case share < 0.3:
		m.suggest.pause = max(suggestionMinPause, m.suggest.pause-10*time.Millisecond)
	}
	m.suggest.replaced, m.suggest.answered = m.suggest.replaced/2, m.suggest.answered/2
}

// suggestionEditorOwnsInput reports whether ed may show a suggestion now.
func (m *App) suggestionEditorOwnsInput(ed *editor.Editor) bool {
	return ed != nil && ed.IsOpen() && !ed.ReadOnly() && m.EditorState != nil && m.EditorState.Editor == ed &&
		m.focusTarget() == FocusEditor && m.focusedPopup() == nil && !m.termDockOwnsKeys() &&
		!m.sessionDeckCapturesInput() && !ed.HasMultipleCarets() && !ed.HasSelection()
}

// autoSuggestionCmd drains a due intent on ordinary Update messages, so a
// wake that arrives before the edit's acknowledgement does not lose it.
func (m *App) autoSuggestionCmd(now time.Time) tea.Cmd {
	m.showPendingJump()
	intent := m.suggest.intent
	if intent == nil {
		return nil
	}
	ed := intent.editor
	if !m.suggestionEditorOwnsInput(ed) || ed.Revision() != intent.revision ||
		!m.acceptsLSPGeneration(intent.generation) || now.After(intent.deadline) {
		m.cancelSuggestionIntent()
		return nil
	}
	if now.Before(intent.due) {
		return nil
	}
	if _, current := ed.CurrentInsightSource(); !current {
		return nil // wait for the core to acknowledge the edit
	}
	automatic := intent.automatic
	m.cancelSuggestionIntent()
	return m.startSuggestion(ed, automatic)
}

func (m *App) cancelSuggestionIntent() {
	if intent := m.suggest.intent; intent != nil {
		intent.cancel()
		m.suggest.intent = nil
	}
}

// requestSuggestionNow is "Ask Aeron for a suggestion" (Alt+\).
func (m *App) requestSuggestionNow() tea.Cmd {
	ed := m.EditorState.Editor
	switch {
	case m.suggest.mode == types.PredictionOff:
		m.Feedback.NotifyInfo("Aeron suggestions are off. Turn them on from Aeron in the footer.")
		return nil
	case !m.suggestionEditorOwnsInput(ed):
		m.Feedback.NotifyInfo("Put one caret in the code, without a selection, to ask for a suggestion.")
		return nil
	}
	m.suggest.dismissed = ""
	if _, current := ed.CurrentInsightSource(); !current {
		m.scheduleSuggestion(ed, 0, false)
		return nil
	}
	return m.startSuggestion(ed, false)
}

func (m *App) startSuggestion(ed *editor.Editor, automatic bool) tea.Cmd {
	port, connected := m.suggestionPort()
	if !connected || !m.suggestionEditorOwnsInput(ed) {
		return nil
	}
	if owner := ed.GhostOwner(); owner != "" && owner != suggestionGhostOwner {
		return nil // another producer's inline text holds Tab
	}
	source, current := ed.CurrentInsightSource()
	start, end, ok := ed.SourceByteRange()
	if !current || !ok || start != end || source.Kind != "canonical" {
		return nil
	}
	if request := m.suggest.request; request != nil {
		request.cancel()
	}
	base, generation := m.lspRequestContext()
	ctx, cancel := context.WithCancel(base)
	m.suggest.sequence++
	request := &suggestionRequest{workspace: m.ws, editor: ed, source: source, revision: ed.Revision(),
		generation: generation, sequence: m.suggest.sequence, caret: start, automatic: automatic, cancel: cancel}
	m.suggest.request = request
	wire := types.EditorPredictionRequest{Source: source, ViewID: suggestionViewID, Sequence: request.sequence,
		Caret: start, Automatic: automatic, Completions: m.visibleCompletionLabels(ed)}
	return m.clientIOCommand(ctx, func(parent context.Context) tea.Msg {
		call, stop := context.WithTimeout(parent, 70*time.Second)
		defer stop()
		result, err := port.RequestEditorSuggestion(call, wire)
		if cancelled := call.Err(); cancelled != nil {
			err = errors.Join(err, cancelled)
		}
		return suggestionResultMsg{owner: request, result: result, err: err}
	})
}

// visibleCompletionLabels steers the model with the completion list the user
// sees, the chosen row first. It is a hint, not a source of truth.
func (m *App) visibleCompletionLabels(ed *editor.Editor) []string {
	s := m.completion
	if s == nil || s.editor != ed || s.set == nil || !s.shown || s.stale {
		return nil
	}
	rows := rankCompletions(s.set.Entries, s.typed)
	labels := make([]string, 0, min(8, len(rows)))
	if s.screen != nil {
		if id := s.screen.SelectedID(); id != "" {
			for _, entry := range s.set.Entries {
				if entry.ID == id {
					labels = append(labels, entry.Insertion.Label)
				}
			}
		}
	}
	for _, row := range rows {
		if len(labels) == 8 {
			break
		}
		if len(labels) > 0 && labels[0] == row.Insertion.Label {
			continue
		}
		labels = append(labels, row.Insertion.Label)
	}
	return labels
}

// applySuggestionResult shows an answer when it still belongs to the exact
// editor, revision and caret that asked.
func (m *App) applySuggestionResult(msg suggestionResultMsg) {
	request := msg.owner
	if request == nil || m.suggest.request != request {
		return
	}
	m.suggest.request = nil
	request.cancel()
	if request.workspace != m.ws || !m.acceptsLSPGeneration(request.generation) {
		return
	}
	m.suggest.answered++
	if msg.err != nil {
		if errors.Is(msg.err, context.Canceled) || errors.Is(msg.err, context.DeadlineExceeded) ||
			strings.Contains(msg.err.Error(), "newer suggestion request") {
			return
		}
		m.suggest.problem = suggestionProblemText(msg.err)
		if !request.automatic {
			m.Feedback.NotifyInfo(m.suggest.problem)
		}
		return
	}
	m.suggest.problem = ""
	m.suggest.status = msg.result.Status
	ed := request.editor
	start, end, ok := ed.SourceByteRange()
	if !m.suggestionEditorOwnsInput(ed) || ed.Revision() != request.revision || !ok || start != end ||
		start != request.caret || !ed.MatchesInsightSource(msg.result.Source) {
		return
	}
	result := msg.result
	if result.Empty() {
		if !request.automatic {
			m.Feedback.NotifyInfo("Aeron has no suggestion here.")
		}
		return
	}
	key := suggestionKey(ed, result)
	if key == m.suggest.dismissed && request.automatic {
		return
	}
	var err error
	switch result.Kind {
	case types.PredictionInsert, types.PredictionEdit:
		err = ed.SetSuggestion(suggestionGhostOwner, request.revision, editor.Suggestion{Start: result.Start, End: result.End, Text: result.Text})
	case types.PredictionJump:
		line, _, _ := ed.SourceLineBounds(result.Start)
		_, _, lineEnd := ed.SourceLineBounds(request.caret)
		line++
		err = ed.SetSuggestion(suggestionGhostOwner, request.revision, editor.Suggestion{Start: lineEnd, End: lineEnd,
			Marker: fmt.Sprintf("  ⇥ line %d", line), JumpTo: result.Start})
		if err == nil {
			m.suggest.jump = &suggestionJump{editor: ed, revision: request.revision, start: result.Start, end: result.End, text: result.Text}
		}
	}
	if err != nil {
		m.suggest.jump = nil
	}
	m.forceFrame()
}

// showPendingJump shows the change at the next place once the first Tab has
// moved the caret there.
func (m *App) showPendingJump() {
	jump := m.suggest.jump
	if jump == nil {
		return
	}
	ed := jump.editor
	if ed != m.EditorState.Editor || ed.Revision() != jump.revision {
		m.suggest.jump = nil
		return
	}
	if _, shown := ed.GhostSuggestion(); shown {
		return // the marker is still on screen
	}
	start, end, ok := ed.SourceByteRange()
	m.suggest.jump = nil
	if !ok || start != end || start != jump.start || !m.suggestionEditorOwnsInput(ed) {
		return
	}
	_ = ed.SetSuggestion(suggestionGhostOwner, jump.revision, editor.Suggestion{Start: jump.start, End: jump.end, Text: jump.text})
	m.forceFrame()
}

// suggestionKey identifies a change for dismissal memory: the change and the
// text of its lines, so it comes back only after the code near it changes.
func suggestionKey(ed *editor.Editor, result types.EditorPredictionResult) string {
	_, start, _ := ed.SourceLineBounds(result.Start)
	_, _, end := ed.SourceLineBounds(result.End)
	return string(result.Kind) + "\x00" + result.Text + "\x00" + ed.SourceSlice(start, end)
}

// noteSuggestionDismissed remembers the change the user dismissed with Esc.
func (m *App) noteSuggestionDismissed(ed *editor.Editor) {
	if ed == nil || ed.GhostOwner() != suggestionGhostOwner {
		return
	}
	if s, ok := ed.GhostSuggestion(); ok {
		kind := types.PredictionInsert
		if s.Start != s.End {
			kind = types.PredictionEdit
		}
		if s.Marker != "" {
			jump := m.suggest.jump
			if jump != nil {
				m.suggest.dismissed = suggestionKey(ed, types.EditorPredictionResult{Kind: types.PredictionJump, Start: jump.start, End: jump.end, Text: jump.text})
			}
			m.suggest.jump = nil
			return
		}
		m.suggest.dismissed = suggestionKey(ed, types.EditorPredictionResult{Kind: kind, Start: s.Start, End: s.End, Text: s.Text})
	}
}

func suggestionProblemText(err error) string {
	text := err.Error()
	if i := strings.LastIndex(text, ": "); i >= 0 && strings.Contains(text[:i], "suggestion_failed") {
		text = text[i+2:]
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return "Aeron could not make a suggestion."
	}
	return strings.ToUpper(text[:1]) + strings.TrimSuffix(text[1:], ".") + "."
}

// refreshSuggestionStatus reads the core's status and hourly meter.
func (m *App) refreshSuggestionStatus() tea.Cmd {
	port, connected := m.suggestionPort()
	if !connected {
		return nil
	}
	owner := m.ws
	base, _ := m.lspRequestContext()
	return m.clientIOCommand(base, func(parent context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 10*time.Second)
		defer cancel()
		status, err := port.EditorSuggestionStatus(ctx)
		return suggestionStatusMsg{workspace: owner, status: status, err: err}
	})
}

func (m *App) applySuggestionStatus(msg suggestionStatusMsg) {
	if msg.workspace != m.ws || msg.err != nil {
		return
	}
	m.suggest.status = msg.status
	if msg.status.Mode != types.PredictionOff && !msg.status.Ready {
		m.suggest.problem = msg.status.Reason
	} else if msg.status.Ready {
		m.suggest.problem = ""
	}
	m.forceFrame()
}

// suggestionFooterSegment is the quiet Aeron item. It shows one state.
func (m *App) suggestionFooterSegment() (shellfooter.Segment, bool) {
	mode := m.suggest.mode
	if mode != types.PredictionAsk && mode != types.PredictionType {
		mode = types.PredictionOff
	}
	if m.EditorState == nil || m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		if mode == types.PredictionOff {
			return shellfooter.Segment{}, false
		}
	}
	segment := shellfooter.Segment{Text: "Aeron", FillRole: ui.ColorAccent2, Pill: true, Action: suggestionFooter, Priority: 0}
	switch {
	case mode == types.PredictionOff:
		segment.Text, segment.FillRole = "Aeron off", ui.ColorMuted
	case m.suggest.problem != "":
		segment.Text, segment.FillRole, segment.Attention = "Aeron !", ui.ColorWarning, true
	case m.suggest.request != nil:
		segment.Text, segment.Attention = "Aeron …", true
	}
	return segment, true
}

// openSuggestionMenu is the Aeron item's menu: the mode, Suggest now, the
// measured speed, the hourly meter and Settings.
func (m *App) openSuggestionMenu() tea.Cmd {
	mode := m.suggest.mode
	choice := func(label string, value types.EditorPredictionMode, detail string) ui.ContextMenuItem {
		mark := "  "
		if mode == value {
			mark = "• "
		}
		return ui.ContextMenuItem{Label: mark + label, Detail: detail, Action: func() { m.chooseSuggestionMode(value) }}
	}
	modes := ui.ContextMenuSection{SectionLabel: "Suggestions", Items: []ui.ContextMenuItem{
		choice("Off", types.PredictionOff, ""),
		choice("When I ask", types.PredictionAsk, "Alt+\\"),
		choice("As I type", types.PredictionType, ""),
	}}
	var info []ui.ContextMenuItem
	if m.suggest.problem != "" {
		info = append(info, ui.ContextMenuItem{Label: m.suggest.problem, Disabled: true})
	}
	status := m.suggest.status
	if status.MeasuredLatencyMS > 0 {
		info = append(info, ui.ContextMenuItem{Label: fmt.Sprintf("Speed: about %d ms", status.MeasuredLatencyMS), Disabled: true})
	}
	if status.LimitTokens > 0 {
		used := status.ActiveTokens + status.ChargedTokens
		info = append(info, ui.ContextMenuItem{Label: fmt.Sprintf("This hour: %s of %s tokens", groupDigits(used), groupDigits(status.LimitTokens)), Disabled: true})
	}
	actions := ui.ContextMenuSection{Items: []ui.ContextMenuItem{
		{Label: "Suggest now", Detail: "Alt+\\", Disabled: mode == types.PredictionOff, Action: func() {
			m.runDetachedAsyncCmd(m.requestSuggestionNow())
		}},
		{Label: "Suggestion settings", Action: func() { m.runDetachedAsyncCmd(m.openApplicationAgent("role/aeron")) }},
	}}
	sections := []ui.ContextMenuSection{modes}
	if len(info) > 0 {
		sections = append(sections, ui.ContextMenuSection{Items: info})
	}
	sections = append(sections, actions)
	menu := &ui.ContextMenuState{Title: "Aeron", Sections: sections}
	if anchor, ok := m.footerSegmentRect(suggestionFooter); ok {
		return tea.Batch(m.openMenuFor(anchor, menu), m.refreshSuggestionStatus())
	}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width-30), Y: max(0, m.height-m.chromeBottomRows()-10)}, menu)
	return m.refreshSuggestionStatus()
}

func groupDigits(n int64) string {
	s := fmt.Sprint(n)
	var out []byte
	for i := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, s[i])
	}
	return string(out)
}

// chooseSuggestionMode turns suggestions on after asking once, the first time
// in a workspace, where the code goes. Off needs no question.
func (m *App) chooseSuggestionMode(mode types.EditorPredictionMode) {
	if mode == m.suggest.mode {
		return
	}
	edits := []types.SettingEdit{{Path: []string{"prediction", "mode"}, After: []byte(`"` + string(mode) + `"`)}}
	if mode == types.PredictionOff {
		m.runDetachedAsyncCmd(m.saveSettingsShortcut(types.SettingsScopeWorkspace, edits, "Aeron suggestions are off.", nil, 0))
		return
	}
	limit := m.suggest.limit
	if limit <= 0 {
		limit = types.DefaultPredictionHourlyTokens
	}
	edits = append(edits, types.SettingEdit{Path: []string{"prediction", "hourly_token_limit"}, After: []byte(fmt.Sprint(limit))})
	label := "Aeron suggests when you press Alt+\\."
	if mode == types.PredictionType {
		label = "Aeron suggests as you type."
	}
	save := func() {
		m.runDetachedAsyncCmd(tea.Batch(m.saveSettingsShortcut(types.SettingsScopeWorkspace, edits, label, nil, 0), m.refreshSuggestionStatus()))
	}
	if m.suggest.mode != types.PredictionOff {
		save()
		return
	}
	destination := m.suggestionDestination()
	question := &ui.ContextMenuState{Title: "Turn on Aeron suggestions in this workspace?", Sections: []ui.ContextMenuSection{{Items: []ui.ContextMenuItem{
		{Label: "Cancel"},
		{Label: "Turn on", Detail: "Aeron sends nearby code, recent edits and related code to " + destination + ".", Action: save},
	}}}}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width/2-30), Y: max(m.shellTopRow(), m.height/2)}, question)
}

// suggestionDestination names the account that receives the code.
func (m *App) suggestionDestination() string {
	settings := m.ws.LoadAppSettings()
	id := settings.Agents.Suggestions.AccountID
	if id == "" {
		id = settings.Agents.Categories["aeron"].AccountID
	}
	if id == "" {
		id = settings.Agents.Categories["default"].AccountID
	}
	if account, ok := settings.Accounts[id]; ok {
		switch {
		case account.Label != "":
			return "the account " + account.Label
		case account.Connection.Name != "":
			return account.Connection.Name
		}
	}
	return "Aeron's account"
}
