package viewmodel

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/shellport"
	remotesscreen "crude/tui/screens/remotes"
	"crude/tui/ui"
	"crude/tui/wm"
)

const remoteInstallOverlay ui.OverlayID = "remote-install-consent"

// The offer is pinned to the exact failed request and immutable form preimage.
// Approval authorizes installation only, never save, pairing or attachment.
type remoteInstallConsent struct {
	state      *remoteManagerState
	owner      shellport.Workspace
	generation uint64
	editor     *remotesscreen.ConnectionEditor
	id, action string
	sequence   uint64
	draft      shellport.RemoteConnectionDraft
	offer      shellport.RemoteInstallOffer
}

func remoteSafeError(err error, draft shellport.RemoteConnectionDraft) string {
	text := err.Error()
	if draft.PairCode != "" {
		text = strings.ReplaceAll(text, draft.PairCode, "[redacted]")
	}
	return text
}

func (m *App) offerRemoteInstall(r remoteManagerResult) bool {
	var offer shellport.RemoteInstallOffer
	if r.action == "install" || r.err == nil || !errors.As(r.err, &offer) || m.overlayHost == nil {
		return false
	}
	s := m.remotes
	p := r.editor
	if p == nil {
		c, ok := m.remoteConnection(r.id)
		if !ok {
			return false
		}
		m.openRemoteEditor(c, false, "Edit connection · "+c.Name, "")
		p = s.editor
		r.draft = shellport.RemoteConnectionDraft{Connection: c}
	}
	if p == nil || s.editor != p {
		return true
	}
	draft, err := p.Draft()
	if err != nil || draft != r.draft {
		p.SetBusy(false)
		p.SetStatus("Connection changed; test the current draft again.")
		m.dirtyRemoteEditor()
		return true
	}
	p.SetBusy(true)
	p.SetStatus("Connection not saved. Review the matching-build installation request.")
	m.dirtyRemoteEditor()
	consent := &remoteInstallConsent{state: s, owner: m.ws, generation: m.workspaceSwitchGen.Load(), editor: p, id: r.id, action: r.action, sequence: r.sequence, draft: draft, offer: offer}
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title: "Install matching remote core", Question: "Install the matching build on this host?",
		Detail:   offer.RemoteInstallSummary() + "\nAfter installation only the connection is tested. Save and Open remain separate decisions.",
		Severity: ui.ConfirmPromptQuestion, Width: min(76, max(36, m.width-4)), FocusedIndex: 1,
		Buttons: []ui.ConfirmButtonSpec{{Label: "Install matching build", ActionID: ui.ConfirmActionYes}, {Label: "Cancel", ActionID: ui.ConfirmActionCancel}},
	})
	w, h := frame.Measure(m.width, m.height)
	w, h = min(w, max(1, m.width)), min(h, max(1, m.height))
	fw := wm.NewFloatingWindow(string(remoteInstallOverlay), "", frame, w, h,
		wm.WithMovable(true), wm.WithResizable(false), wm.WithClosable(false), wm.WithModal(true), wm.WithFocusable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	s.install = consent
	m.overlayHost.RegisterContentHandler(remoteInstallOverlay, func(_ *ui.OverlayHost, win *wm.FloatingWindow, x, y int, _ tea.Msg) tea.Cmd {
		if choice := confirmFrameButtonAt(win, x, y); choice >= 0 {
			return m.resolveRemoteInstall(choice == 0)
		}
		return nil
	})
	m.overlayHost.RegisterHoverHandler(remoteInstallOverlay, func(_ *ui.OverlayHost, win *wm.FloatingWindow, x, y int, _ tea.MouseButton) {
		if cw, ok := win.Content.(*ui.ComponentWidget); ok {
			ui.SetConfirmHover(cw, confirmFrameButtonAt(win, x, y))
		}
	})
	policy := ui.ModalDialogPolicy()
	policy.Movable = true
	m.overlayHost.Open(remoteInstallOverlay, fw, policy, func() {
		if s.install == consent {
			s.install = nil
			p.SetBusy(false)
			m.dirtyRemoteEditor()
		}
	})
	m.forceFrame()
	return true
}

func (m *App) resolveRemoteInstall(approve bool) tea.Cmd {
	s := m.remotes
	if s == nil || s.install == nil || m.overlayHost == nil {
		return nil
	}
	c := s.install
	draft, err := c.editor.Draft()
	top := m.overlayHost.Top()
	valid := c.state == s && !s.stopped && c.owner == m.ws && c.generation == m.workspaceSwitchGen.Load() && s.editor == c.editor && s.editorID == c.id && err == nil && draft == c.draft && s.latest[c.action+":"+c.id] == c.sequence && top != nil && top.ID == remoteInstallOverlay
	m.overlayHost.Close(remoteInstallOverlay)
	if !approve || !valid || m.asyncIOStopped.Load() {
		return nil
	}
	c.editor.SetBusy(true)
	c.editor.SetStatus("Installing the approved matching build…")
	m.dirtyRemoteEditor()
	return m.remoteJob("install", c.id, c.editor, func(ctx context.Context, r *remoteManagerResult) {
		r.draft = c.draft
		r.err = c.offer.InstallRemote(ctx)
	})
}

func (m *App) finishRemoteInstall(r remoteManagerResult) tea.Cmd {
	p := r.editor
	if p == nil || m.remotes.editor != p {
		return nil
	}
	draft, err := p.Draft()
	if err != nil || draft != r.draft {
		p.SetBusy(false)
		p.SetStatus("Connection changed; test the current draft again.")
		m.dirtyRemoteEditor()
		return nil
	}
	if r.err != nil {
		p.SetBusy(false)
		p.SetStatus("Installation failed. " + remoteSafeError(r.err, r.draft))
		m.dirtyRemoteEditor()
		return nil
	}
	manager := m.remotes.manager
	p.SetStatus("Matching build installed. Testing connection; nothing has been saved or opened.")
	m.dirtyRemoteEditor()
	return m.remoteJob("test", r.id, p, func(ctx context.Context, result *remoteManagerResult) {
		result.draft = r.draft
		result.status, result.err = manager.Test(ctx, r.draft.Connection)
	})
}

func (m *App) handleRemoteInstallInput(msg tea.Msg) (bool, tea.Cmd) {
	if m.remotes == nil || m.remotes.install == nil || m.overlayHost == nil {
		return false, nil
	}
	top := m.overlayHost.Top()
	if top == nil || top.ID != remoteInstallOverlay {
		return false, nil
	}
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return false, nil
	}
	m.forceFrame()
	cw := top.Window.Content.(*ui.ComponentWidget)
	switch key.Code {
	case tea.KeyEsc:
		return true, m.resolveRemoteInstall(false)
	case tea.KeyEnter:
		return true, m.resolveRemoteInstall(ui.ConfirmButtonIndexFromID(cw.Host().Focused()) == 0)
	case tea.KeyLeft, tea.KeyUp:
		ui.MoveConfirmFocus(cw, -1, 2)
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		delta := 1
		if key.Code == tea.KeyTab && key.Mod.Contains(tea.ModShift) {
			delta = -1
		}
		ui.MoveConfirmFocus(cw, delta, 2)
	}
	return true, nil
}
