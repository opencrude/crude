package viewmodel

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/tui/screens/capabilitypicker"
	"crude/tui/ui"
	"crude/tui/wm"
	captypes "crude/types/capability"
	"crude/workspace"
)

// capabilityPickerOwner is one open Capabilities modal. It is bound to the
// session selected when it opened: its switches change only that session's
// capability gate (shared by the session's root and child agents).
type capabilityPickerOwner struct {
	screen     *capabilitypicker.Screen
	session    *workspace.Session
	runtime    shellport.Workspace
	generation uint64
	sequence   uint64
	cancel     context.CancelFunc
}

// capabilityToggleOutcome is one committed (or failed) session switch change.
type capabilityToggleOutcome struct {
	id     string
	result workspace.SessionSettingsResult
}

type capabilityPickerResultMsg struct {
	owner    *capabilityPickerOwner
	sequence uint64
	snapshot captypes.Snapshot
	err      error
	toggle   *capabilityToggleOutcome
}

func (m *App) openCapabilityPicker() tea.Cmd {
	return m.openCapabilityPickerTab(captypes.KindSkill, "")
}

func (m *App) openCapabilityPickerTab(kind captypes.Kind, query string) tea.Cmd {
	if m.capabilityPicker != nil {
		m.overlayHost.Close(ui.OverlayCapabilityPicker)
	}
	owner := &capabilityPickerOwner{screen: capabilitypicker.New(m.Theme, m.capabilitySnapshot), session: m.ActiveSession, runtime: m.ws, generation: m.workspaceSwitchGen.Load()}
	owner.screen.SetCategory(kind)
	owner.screen.SetQuery(query)
	m.syncCapabilityPickerSession(owner)
	m.capabilityPicker = owner
	width, height := fitFloatingSize(m.width, m.height, 84, 34, 40, 14)
	// The body paints its own heading ("Skills · <session>"); the border
	// carries no title so it does not crowd the top edge.
	owner.screen.SetSessionLabel(m.capabilityPickerSessionLabel(owner.session))
	title := "Skills"
	if kind == captypes.KindMCP {
		title = "MCP servers"
	}
	frame := ui.NewWindowFrameWidget(m.Theme, "capability-picker/frame", ui.FrameWindow, "", owner.screen, nil, true, width, height)
	owner.screen.SetFocusHandler(frame.Focus)
	window := wm.NewFloatingWindow(string(ui.OverlayCapabilityPicker), title, frame, width, height, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithClosable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(window, m.width, m.height)
	m.overlayHost.Open(ui.OverlayCapabilityPicker, window, ui.ModalDialogPolicy(), func() {
		if owner.cancel != nil {
			owner.cancel()
		}
		if m.capabilityPicker == owner {
			m.capabilityPicker = nil
		}
	})
	frame.Host().MarkLayout()
	frame.Host().Resize(width, height)
	frame.Host().Layout()
	owner.screen.FocusSearch()
	return m.capabilityPickerCmd(owner)
}

// capabilityPickerSessionLabel names the owning session by its root agent.
func (m *App) capabilityPickerSessionLabel(session *workspace.Session) string {
	if session == nil || m.ws == nil || m.ws.AgentMgr() == nil {
		return ""
	}
	if node := m.ws.AgentMgr().Get(session.AgentID); node != nil {
		return strings.TrimSpace(node.Label)
	}
	return ""
}

// syncCapabilityPickerSession installs the owning session's current gate.
func (m *App) syncCapabilityPickerSession(owner *capabilityPickerOwner) {
	if owner == nil {
		return
	}
	session := owner.session
	live := session != nil && m.ws != nil && m.ws.SessionsByID()[session.SessionID] == session
	var disabled []string
	if live && session.Settings != nil {
		disabled = session.Settings.Effective.SessionDisabledCapabilities
	}
	owner.screen.SetSession(live, disabled)
}

// refreshCapabilityPickerSession follows a settings change of the session
// that owns the open modal (another shell, the root agent, or a reset).
func (m *App) refreshCapabilityPickerSession(session *workspace.Session) {
	if owner := m.capabilityPicker; owner != nil && owner.session == session {
		m.syncCapabilityPickerSession(owner)
		m.invalidateCapabilityPicker()
	}
}

func (m *App) capabilityPickerCmd(owner *capabilityPickerOwner) tea.Cmd {
	if owner.cancel != nil {
		owner.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	owner.cancel = cancel
	owner.sequence++
	sequence, workspaceOwner := owner.sequence, m.ws
	return func() tea.Msg {
		defer cancel()
		snapshot, err := workspaceOwner.CapabilitySnapshot(ctx)
		return capabilityPickerResultMsg{owner: owner, sequence: sequence, snapshot: snapshot, err: err}
	}
}

// capabilityToggleCmd commits one switch to the owning session. Toggles are
// idempotent sets, never cancelled by a later refresh or another toggle.
func (m *App) capabilityToggleCmd(owner *capabilityPickerOwner, id string, enabled bool) tea.Cmd {
	fail := func(err error) tea.Cmd {
		return func() tea.Msg {
			return capabilityPickerResultMsg{owner: owner, sequence: owner.sequence, err: err, toggle: &capabilityToggleOutcome{id: id, result: workspace.SessionSettingsResult{Err: err}}}
		}
	}
	if owner.session == nil || owner.runtime == nil {
		return fail(errors.New("no session owns this window"))
	}
	operation, err := owner.runtime.PrepareSessionCapabilityToggle(owner.session.SessionID, id, enabled)
	if err != nil {
		return fail(err)
	}
	sequence := owner.sequence
	return func() tea.Msg {
		result := operation.Run()
		return capabilityPickerResultMsg{owner: owner, sequence: sequence, err: result.Err, toggle: &capabilityToggleOutcome{id: id, result: result}}
	}
}

func (m *App) handleCapabilityPickerAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "capability-picker.") || m.capabilityPicker == nil {
		return nil, false
	}
	owner := m.capabilityPicker
	switch action.ID {
	case "capability-picker.close":
		m.overlayHost.Close(ui.OverlayCapabilityPicker)
	case "capability-picker.manage":
		target, _ := action.Payload.(capabilitypicker.ManageTarget)
		view := activitySkills
		if target.Kind == captypes.KindMCP {
			view = activityMCP
		}
		m.overlayHost.Close(ui.OverlayCapabilityPicker)
		return m.openCapabilityManager(view, "", target.ID), true
	case "capability-picker.toggle":
		if owner.generation != m.workspaceSwitchGen.Load() {
			return nil, true
		}
		id, enabled, ok := owner.screen.BeginToggle(action.Payload)
		if ok {
			m.invalidateCapabilityPicker()
			return m.capabilityToggleCmd(owner, id, enabled), true
		}
	}
	return nil, true
}

func (m *App) invalidateCapabilityPicker() {
	if window := m.overlayHost.Window(ui.OverlayCapabilityPicker); window != nil {
		window.Content.(*ui.WindowFrameWidget).Host().MarkLayout()
		window.MarkContentDirty()
	}
}

func (m *App) applyCapabilityPickerResult(message capabilityPickerResultMsg) {
	owner := message.owner
	if outcome := message.toggle; outcome != nil {
		// The session commit is authoritative whether or not this modal is
		// still open; installing it is revision-fenced.
		if owner.runtime == m.ws && owner.generation == m.workspaceSwitchGen.Load() && outcome.result.Err == nil && outcome.result.ResultOwner() != nil {
			if m.ws.ApplySessionSettingsResult(outcome.result) {
				m.refreshSessionSettingsProjection(outcome.result.ResultOwner())
			}
		}
		if m.capabilityPicker != owner {
			return
		}
		m.syncCapabilityPickerSession(owner)
		owner.screen.FinishToggle(outcome.id, outcome.result.Err)
		m.invalidateCapabilityPicker()
		return
	}
	if m.capabilityPicker == nil || m.capabilityPicker != owner || owner.sequence != message.sequence || owner.generation != m.workspaceSwitchGen.Load() {
		return
	}
	owner.cancel = nil
	m.applyObservedCapabilitySnapshot(message.snapshot, message.err, true)
	owner.screen.SetSnapshot(m.capabilitySnapshot)
	m.invalidateCapabilityPicker()
}
