package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/types"
)

const signatureHelpID = ui.OverlaySignatureHelp

// One invocation owns both pending work and its immutable presentation.
// Replacing it changes the pointer token even at the same cursor/source.
type signatureHelpState struct {
	editor               *editor.Editor
	path                 string
	revision, generation uint64
	source               types.InsightSource
	line, col            int
	deadline             time.Time
	cancel               context.CancelFunc
	result               *signatureHelpResultMsg
	shown                bool
	automatic            bool
	status               shellport.StatusService
	notice               string
}

type signatureHelpResultMsg struct {
	owner *signatureHelpState
	help  types.LSPSignatureHelp
	text  string
	hint  string
	err   error
}

func (m *App) closeSignatureHelp() {
	m.signatureHelpIntent = nil
	if state := m.signatureHelp; state != nil {
		m.signatureHelp = nil
		state.cancel()
		state.finishPendingNotice()
	}
	if m.overlayHost != nil {
		m.overlayHost.Close(signatureHelpID)
	}
}

func (s *signatureHelpState) finishPendingNotice() {
	if s.status != nil && s.notice != "" {
		s.status.Dismiss(s.notice)
		s.notice = ""
	}
}

func (m *App) requestSignatureHelpCmd() tea.Cmd {
	return m.requestSignatureHelp(false)
}

func (m *App) requestSignatureHelp(automatic bool) tea.Cmd {
	m.closeSignatureHelp()
	ed := m.EditorState.Editor
	service := m.ws.LSPMgr()
	if ed == nil || !ed.IsOpen() || service == nil || !service.ServerReady() {
		if !automatic {
			m.Feedback.NotifyInfo("Parameter information needs a connected language server")
		}
		return nil
	}
	source, current := ed.CurrentInsightSource()
	if !current {
		if !automatic {
			m.Feedback.NotifyInfo("Parameter information waits for acknowledged source text and resolved editor changes")
		}
		return nil
	}
	base, generation := m.lspRequestContext()
	ctx, cancel := context.WithTimeout(base, 5*time.Second)
	line, col := ed.LSPCursorPosition()
	state := &signatureHelpState{editor: ed, path: ed.FilePath(), revision: ed.Revision(), generation: generation, source: source,
		line: line, col: col, deadline: time.Now().Add(5 * time.Second), cancel: cancel, automatic: automatic}
	m.signatureHelp = state
	state.status = m.ws.Status()
	if state.status != nil && !automatic {
		state.notice = state.status.Notify(types.NotificationRecord{
			Owner:  types.ResourceRef{Kind: types.ResourceEditor, ID: state.path},
			Source: "parameter-information", Severity: types.SeverityInfo,
			Summary: "Requesting parameter information", Read: true, Transient: true,
		})
	}
	return func() tea.Msg {
		defer cancel()
		help, err := service.RequestSignatureHelp(ctx, state.path, line, col)
		return signatureHelpResultMsg{owner: state, help: help, text: signatureHelpText(help), hint: signatureHintText(help), err: err}
	}
}

func signatureHelpText(help types.LSPSignatureHelp) string {
	if len(help.Signatures) == 0 || help.ActiveSignature < 0 || help.ActiveSignature >= len(help.Signatures) {
		return ""
	}
	active := help.Signatures[help.ActiveSignature]
	var text strings.Builder
	fmt.Fprintf(&text, "Signature %d of %d\n%s\n", help.ActiveSignature+1, len(help.Signatures), active.Label)
	if active.ActiveParameter >= 0 && active.ActiveParameter < len(active.Parameters) {
		p := active.Parameters[active.ActiveParameter]
		fmt.Fprintf(&text, "\nActive parameter %d of %d: %s\n", active.ActiveParameter+1, len(active.Parameters), p.Label)
		if p.Documentation != "" {
			fmt.Fprintf(&text, "%s\n", p.Documentation)
		}
	} else {
		text.WriteString("\nNo parameters declared by this signature.\n")
	}
	if active.Documentation != "" {
		fmt.Fprintf(&text, "\n%s\n", active.Documentation)
	}
	if len(help.Signatures) > 1 {
		text.WriteString("\nOther signatures:\n")
		for i, signature := range help.Signatures {
			if i != help.ActiveSignature {
				fmt.Fprintf(&text, "%d. %s\n", i+1, signature.Label)
			}
		}
	}
	fmt.Fprintf(&text, "\nProvider: %s\nSource revision: %d; context: %d\n",
		help.Boundary.Producer.Provider, help.Boundary.Source.Revision, help.Boundary.Context.Generation)
	for _, unknown := range help.Boundary.Context.UnknownInputs {
		fmt.Fprintf(&text, "Unverified: %s\n", unknown)
	}
	return text.String()
}

func (m *App) handleSignatureHelpResult(msg signatureHelpResultMsg) {
	m.syncSignatureHelp()
	if m.signatureHelp != msg.owner {
		return
	}
	if msg.err != nil {
		m.closeSignatureHelp()
		if !msg.owner.automatic {
			m.Feedback.NotifyInfo("Parameter information: " + msg.err.Error())
		}
		return
	}
	m.signatureHelp.result = &msg
	m.syncSignatureHelp()
}

// Only Update invokes this lifecycle reconciliation. A remote result may lead
// its replica's context publication; retain one bounded result until that exact
// cursor arrives, never paint it against an older or replacement context.
func (m *App) syncSignatureHelp() {
	state := m.signatureHelp
	if state == nil {
		return
	}
	valid := m.acceptsLSPDocument(state.generation, state.path, state.editor, state.revision) &&
		state.editor.MatchesInsightSource(state.source)
	line, col := state.editor.LSPCursorPosition()
	ownWindow := m.overlayHost != nil && m.overlayHost.IsOpen(signatureHelpID)
	ownsInput := m.focusTarget() == FocusEditor && !m.termDockOwnsKeys() && !m.sessionDeckCapturesInput()
	if ownWindow && !state.automatic {
		ownsInput = m.focusedPopup() == m.overlayHost.Window(signatureHelpID)
	} else {
		ownsInput = ownsInput && m.focusedPopup() == nil
	}
	if state.automatic {
		ownsInput = ownsInput && !state.editor.HasSelection()
	}
	if !valid || line != state.line || col != state.col || m.ws.LSPMgr() == nil || !m.ws.LSPMgr().ServerReady() ||
		m.sessionDeckModal() || !ownsInput {
		m.closeSignatureHelp()
		return
	}
	if !state.shown && time.Now().After(state.deadline) {
		m.closeSignatureHelp()
		if !state.automatic {
			m.Feedback.NotifyInfo("Parameter information timed out or its context could not be observed")
		}
		return
	}
	if state.result == nil {
		return
	}
	owner, contextGeneration := m.ws.LSPMgr().ContextCursor()
	boundary := state.result.help.Boundary
	if owner == "" || owner != boundary.Source.WorkspaceGeneration ||
		contextGeneration > boundary.Context.Generation || boundary.Source != state.source {
		m.closeSignatureHelp()
		return
	}
	if contextGeneration < boundary.Context.Generation {
		return
	}
	if state.shown {
		return
	}
	state.finishPendingNotice()
	if state.result.text == "" {
		m.closeSignatureHelp()
		if !state.automatic {
			m.Feedback.NotifyInfo("No parameter information at this position")
		}
		return
	}
	area := ui.NewTextArea("lsp-signature/text", ui.NewTokenSet(m.Theme), true)
	area.SetReadOnly(true)
	text := state.result.text
	if state.automatic {
		text = state.result.hint
	}
	area.SetValue(text)
	area.SetCursorFlat(0)
	w, h := min(90, max(1, m.width-4)), min(18, max(1, m.height-4))
	if state.automatic {
		h = min(h, 10)
	}
	bounds, visible := m.editorHelpBounds(state.editor, w, h)
	if !visible {
		m.closeSignatureHelp()
		return
	}
	w, h = bounds.W, bounds.H
	title := "Parameter information"
	if state.automatic {
		title += " · Alt+I details"
	}
	widget := ui.NewWindowFrameWidget(m.Theme, "lsp-signature/frame", ui.FramePopover, title, area, nil, true, w, h)
	if !state.automatic {
		widget.Focus(area.ID())
	}
	fw := wm.NewFloatingWindow(string(signatureHelpID), "", widget, w, h, wm.WithDecoration(true),
		wm.WithMovable(!state.automatic), wm.WithResizable(!state.automatic), wm.WithClosable(true), wm.WithFocusable(!state.automatic),
		wm.WithClickOutside(wm.ClickOutsideCloseRoute))
	fw.SetPosition(bounds.X, bounds.Y)
	policy := ui.PopoverPolicy(true)
	policy.Passive, policy.Focusable = state.automatic, !state.automatic
	m.overlayHost.Open(signatureHelpID, fw, policy, func() {
		if m.signatureHelp == state {
			m.signatureHelp = nil
			state.cancel()
			state.finishPendingNotice()
		}
	})
	state.shown = true
}
