package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent/sessiondb"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/launcher"
	settingsui "crude/tui/screens/settingspages"
	"crude/tui/screens/shellfooter"
	"crude/tui/ui"
	captypes "crude/types/capability"
	"crude/types/setting"
	"crude/workspace"
)

type capabilityOperation struct {
	target *settingsui.Model
	cancel context.CancelFunc
}

type capabilityPollMsg struct {
	generation uint64
	sequence   uint64
}

type capabilityContextMsg struct {
	generation uint64
	owner      *workspace.Session
	agentID    string
	draft      string
	reference  sessiondb.Reference
	result     settingsui.CapabilityResultMsg
}

type capabilitySnapshotMsg struct {
	generation uint64
	sequence   uint64
	snapshot   captypes.Snapshot
	err        error
}

func (m *App) capabilitySnapshotCmd() tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	if owner == nil {
		return nil
	}
	if m.capabilitySnapshotCancel != nil {
		m.capabilitySnapshotCancel()
	}
	m.capabilitySnapshotSequence++
	sequence := m.capabilitySnapshotSequence
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	m.capabilitySnapshotCancel = cancel
	return func() tea.Msg {
		defer cancel()
		snapshot, err := owner.CapabilitySnapshot(ctx)
		return capabilitySnapshotMsg{generation: generation, sequence: sequence, snapshot: snapshot, err: err}
	}
}

func (m *App) applyCapabilitySnapshot(message capabilitySnapshotMsg) {
	if message.generation != m.workspaceSwitchGen.Load() || message.sequence != m.capabilitySnapshotSequence {
		return
	}
	m.capabilitySnapshotCancel = nil
	// An explicit refresh supersedes periodic observations already in flight.
	if m.launcherCapabilityCancel != nil {
		m.launcherCapabilityCancel()
		m.launcherCapabilityCancel = nil
	}
	m.launcherCapabilitySequence++
	m.applyObservedCapabilitySnapshot(message.snapshot, message.err, true)
}

func (m *App) applyObservedCapabilitySnapshot(snapshot captypes.Snapshot, err error, projectLauncher bool) {
	if err != nil {
		if snapshot.Generation == 0 {
			snapshot = m.capabilitySnapshot
		}
		snapshot.Error = err.Error()
	} else if snapshot.Generation == 0 {
		return
	}
	if snapshot.Generation < m.capabilitySnapshot.Generation {
		return
	}
	if reflect.DeepEqual(snapshot, m.capabilitySnapshot) {
		// A new home screen still needs the retained snapshot even when shared
		// runtime state did not change while the previous home was unmounted.
		if projectLauncher {
			m.projectLauncherCapabilities(snapshot)
		}
		return
	}
	m.capabilitySnapshot = snapshot
	if m.capabilityPicker != nil {
		m.capabilityPicker.screen.SetSnapshot(snapshot)
		m.invalidateCapabilityPicker()
	}
	m.refreshActivityList()
	if m.activityBar != nil {
		for _, kind := range []captypes.Kind{captypes.KindSkill, captypes.KindMCP} {
			count := 0
			for _, entry := range m.capabilitySnapshot.Entries {
				if entry.Kind == kind && (entry.State == "failed" || entry.State == "unavailable" || entry.State == "needs sign-in") {
					count++
				}
			}
			view := activitySkills
			if kind == captypes.KindMCP {
				view = activityMCP
			}
			m.activityBar.SetBadge(view, count)
		}
	}
	if projectLauncher && m.launcherScreen != nil {
		m.projectLauncherCapabilities(m.capabilitySnapshot)
	}
	if window := m.overlayHost.Window(ui.OverlayWorkspaceSettings); window != nil {
		if body := m.settingsSurface(window); body != nil {
			body.Model().SetCapabilitySnapshot(m.capabilitySnapshot)
			if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
				frame.Host().MarkLayout()
			}
			window.MarkContentDirty()
		}
	}
}

func (m *App) projectLauncherCapabilities(snapshot captypes.Snapshot) {
	if m.launcherScreen == nil {
		return
	}
	bindings := make([]captypes.AgentBinding, 0, len(snapshot.Bindings))
	for _, binding := range snapshot.Bindings {
		if m.ws.AgentTurnActive(binding.AgentID) {
			bindings = append(bindings, binding)
		}
	}
	snapshot.Bindings = bindings
	m.launcherScreen.SetCapabilities(snapshot)
	m.rootHost.MarkLayout()
}

type launcherCapabilitiesMsg struct {
	screen              *launcher.Screen
	workspace, sequence uint64
	snapshot            captypes.Snapshot
	err                 error
}

func (m *App) launcherCapabilitiesCmd(now time.Time) tea.Cmd {
	// Settings and capability sidebars also observe this workspace. An active
	// agent removes the launcher screen; only its optional projection depends
	// on that screen, never scheduling the shared runtime observation.
	if m.ws == nil || m.launcherCapabilityCancel != nil || now.Sub(m.launcherCapabilityAt) < 2*time.Second {
		return nil
	}
	m.launcherCapabilityAt = now
	m.launcherCapabilitySequence++
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	m.launcherCapabilityCancel = cancel
	owner, screen, generation, sequence := m.ws, m.launcherScreen, m.workspaceSwitchGen.Load(), m.launcherCapabilitySequence
	return func() tea.Msg {
		defer cancel()
		snapshot, err := owner.CapabilityRuntimeSnapshot(ctx)
		return launcherCapabilitiesMsg{screen: screen, workspace: generation, sequence: sequence, snapshot: snapshot, err: err}
	}
}

func (m *App) capabilityObservationVisible() bool {
	if m.launcherHostVisible() || m.overlayHost.Window(ui.OverlayWorkspaceSettings) != nil || m.capabilityPicker != nil {
		return true
	}
	return m.Tree != nil && m.Tree.Visible && (m.activityView == activitySkills || m.activityView == activityMCP)
}

func (m *App) applyLauncherCapabilities(msg launcherCapabilitiesMsg) {
	if msg.workspace != m.workspaceSwitchGen.Load() || msg.sequence != m.launcherCapabilitySequence {
		return
	}
	m.launcherCapabilityCancel = nil
	if msg.snapshot.Generation == 0 && msg.err == nil {
		return
	}
	m.applyObservedCapabilitySnapshot(msg.snapshot, msg.err, msg.screen == m.launcherScreen)
}

func (m *App) capabilitySidebarItems(view activitybar.View) []ui.ListItem {
	kind := captypes.KindSkill
	if view == activityMCP {
		kind = captypes.KindMCP
	}
	items := []ui.ListItem{{ID: "capability.manage", Label: "Manage " + view.Label(), Command: true}}
	for _, entry := range m.capabilitySnapshot.Entries {
		if entry.Kind != kind {
			continue
		}
		items = append(items, ui.ListItem{ID: "capability:" + entry.ID, Label: entry.Name, Detail: entry.State, Payload: entry})
	}
	if len(items) == 1 {
		items = append(items, ui.ListItem{ID: "none", Label: "No configured " + view.Label(), Disabled: true})
	}
	return items
}

func (m *App) openCapabilityManager(view activitybar.View, query, id string) tea.Cmd {
	kind := captypes.KindSkill
	if view == activityMCP {
		kind = captypes.KindMCP
	}
	return m.openCapabilitySettings(func(body *settingsui.WorkspaceSettingsSurface) ui.EventResult {
		return body.OpenCapabilities(kind, query, id)
	})
}

func (m *App) openCapabilitySettings(open func(*settingsui.WorkspaceSettingsSurface) ui.EventResult) tea.Cmd {
	window := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if window == nil {
		// Capabilities are application-owned by default: an install belongs to
		// the user, not to one project, so removing a workspace cannot destroy
		// it. The Application/Workspace footer toggle remains the explicit way
		// to bind an installation to the current workspace.
		m.openSettings(setting.ScopeApplication)
		window = m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	}
	body := m.settingsSurface(window)
	if body == nil {
		return nil
	}
	result := open(body)
	if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
		frame.Host().MarkLayout()
		frame.Host().Layout()
		body.FocusForm()
	}
	window.MarkContentDirty()
	return tea.Batch(result.Cmd, m.capabilitySnapshotCmd())
}

func (m *App) capabilityRequestCmd(message settingsui.CapabilityRequestMsg) tea.Cmd {
	generation := m.workspaceSwitchGen.Load()
	result := settingsui.CapabilityResultMsg{Target: message.Target, Action: message.Action, Snapshot: m.capabilitySnapshot, Sequence: message.Sequence, WorkspaceGeneration: generation}
	if operation, exists := m.capabilityOperations[message.Supersedes]; message.Supersedes != 0 && exists && operation.target == message.Target {
		operation.cancel()
		delete(m.capabilityOperations, message.Supersedes)
	}
	if message.Action.Operation == "cancel" {
		sequence, _ := strconv.ParseUint(message.Action.Arguments["sequence"], 10, 64)
		if operation, exists := m.capabilityOperations[sequence]; exists && operation.target == message.Target {
			operation.cancel()
			delete(m.capabilityOperations, sequence)
		}
		result.Content = "Cancellation requested for this operation; shared acquisitions may finish for other users."
		return func() tea.Msg { return result }
	}
	if message.Action.Operation == "stage-context" {
		owner, draft := m.ActiveSession, m.Input.Value()
		agentID := m.activeAgentTabID()
		var response captypes.Result
		if owner == nil || owner.RefStore == nil || agentID == "" || json.Unmarshal([]byte(message.Action.Content), &response) != nil || response.IsError || response.Unknown || len(message.Action.Content) > 1<<20 {
			result.Err = fmt.Errorf("select an owning agent and fetch a successful bounded preview first")
			return func() tea.Msg { return result }
		}
		var text strings.Builder
		fmt.Fprintf(&text, "Connection: %s\nSource: %s\nFetched preview: %s\n\n", message.Action.ID, message.Action.Arguments["source"], time.Now().UTC().Format(time.RFC3339))
		for _, item := range response.Content {
			if item.Text != "" {
				text.WriteString(item.Text + "\n")
			} else if item.Data != "" {
				result.Err = fmt.Errorf("binary MCP context needs an explicit media attachment; this preview is text-only")
				return func() tea.Msg { return result }
			}
		}
		if len(response.Structured) > 0 {
			text.Write(response.Structured)
		}
		request := sessiondb.CreateRequest{Kind: sessiondb.KindMCPContext, Title: message.Action.ID + " · " + message.Action.Arguments["source"], Mime: "text/plain", Content: text.String(), Inline: true}
		return func() tea.Msg {
			reference, err := owner.RefStore.CreateReference(request)
			result.Err = err
			result.Content = "MCP context staged as a source-attributed attachment; nothing was sent."
			return capabilityContextMsg{generation: generation, owner: owner, agentID: agentID, draft: draft, reference: reference, result: result}
		}
	}
	owner := m.ws
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	if m.capabilityOperations == nil {
		m.capabilityOperations = make(map[uint64]capabilityOperation)
	}
	m.capabilityOperations[message.Sequence] = capabilityOperation{target: message.Target, cancel: cancel}
	command := func() tea.Msg {
		defer cancel()
		snapshot, entries, content, err := owner.CapabilityAction(ctx, message.Action)
		return settingsui.CapabilityResultMsg{Target: message.Target, Action: message.Action, Snapshot: snapshot, Entries: entries, Content: content, Err: err, Sequence: message.Sequence, WorkspaceGeneration: generation}
	}
	if message.Action.Operation == "sign-in" || message.Action.Operation == "connect" {
		return tea.Batch(command, m.capabilityPollCmd(generation, message.Sequence))
	}
	return command
}

func (m *App) capabilityPollCmd(generation, sequence uint64) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return capabilityPollMsg{generation: generation, sequence: sequence} })
}

func (m *App) cancelCapabilityOperations() {
	if m.launcherCapabilityCancel != nil {
		m.launcherCapabilityCancel()
		m.launcherCapabilityCancel = nil
	}
	m.launcherCapabilitySequence++
	m.launcherCapabilityAt = time.Time{}
	if m.capabilitySnapshotCancel != nil {
		m.capabilitySnapshotCancel()
		m.capabilitySnapshotCancel = nil
	}
	m.capabilitySnapshotSequence++
	for sequence, operation := range m.capabilityOperations {
		operation.cancel()
		delete(m.capabilityOperations, sequence)
	}
}

func (m *App) applyCapabilityResult(message settingsui.CapabilityResultMsg) tea.Cmd {
	if operation, exists := m.capabilityOperations[message.Sequence]; exists && operation.target == message.Target {
		operation.cancel()
		delete(m.capabilityOperations, message.Sequence)
	}
	if message.WorkspaceGeneration != m.workspaceSwitchGen.Load() {
		return nil
	}
	if message.Snapshot.Generation != 0 && message.Snapshot.Generation >= m.capabilitySnapshot.Generation {
		// An accepted operation supersedes older explicit reads as well as polls.
		if m.capabilitySnapshotCancel != nil {
			m.capabilitySnapshotCancel()
		}
		m.capabilitySnapshotSequence++
		m.applyCapabilitySnapshot(capabilitySnapshotMsg{generation: message.WorkspaceGeneration, sequence: m.capabilitySnapshotSequence, snapshot: message.Snapshot})
	}
	window := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if window != nil {
		if body := m.settingsSurface(window); body != nil && body.Model() == message.Target {
			_, command := body.Model().Update(message)
			body.SetModel(body.Model())
			if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
				frame.Host().MarkLayout()
			}
			window.MarkContentDirty()
			m.refreshActivityList()
			if message.Err == nil && message.Action.Operation == "open-file" {
				open, err := m.openEditorTab(message.Content)
				if err != nil {
					m.Feedback.NotifyError(err.Error())
				}
				return tea.Batch(command, open)
			}
			return command
		}
	}
	m.refreshActivityList()
	return nil
}

// applyCapabilitySearchTick lets the exact settings model that armed a live
// search decide whether its quiet period still names the current query.
func (m *App) applyCapabilitySearchTick(message settingsui.CapabilitySearchTickMsg) tea.Cmd {
	window := m.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if window == nil {
		return nil
	}
	body := m.settingsSurface(window)
	if body == nil || body.Model() != message.Target {
		return nil
	}
	command := body.CapabilitySearchTick(message)
	if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
		frame.Host().MarkLayout()
	}
	window.MarkContentDirty()
	return command
}

func (m *App) openNavigationMenu() {
	items := make([]ui.ContextMenuItem, 0, 5)
	for _, view := range activitybar.Views() {
		selected := view
		items = append(items, ui.ContextMenuItem{Label: view.Label(), Action: func() { m.startSourceControlMutation(m.toggleActivity(selected)) }})
	}
	m.openContextMenuState(tea.Mouse{X: m.frameLeftCol() + activitybar.Width, Y: m.shellTopRow()}, &ui.ContextMenuState{Title: "Navigation", Sections: []ui.ContextMenuSection{{Items: items}}})
}

func (m *App) openFooterMenu() {
	segments := m.footerStatusSegments()
	items := make([]ui.ContextMenuItem, 0, len(segments))
	for _, segment := range segments {
		if segment.Action == "" {
			continue
		}
		// Use the footer's projection and dispatch path so overflow cannot
		// lose a destination or acquire different toggle/refresh behavior.
		message := shellfooter.ActionMsg{Action: segment.Action, TargetID: segment.TargetID}
		items = append(items, ui.ContextMenuItem{Label: segment.Text, Action: func() {
			m.runDetachedAsyncCmd(func() tea.Msg { return message })
		}})
	}
	menu := &ui.ContextMenuState{Title: "Views", Sections: []ui.ContextMenuSection{{Items: items}}}
	if anchor, ok := m.footerSegmentRect("footer-more"); ok {
		m.openMenuFor(anchor, menu)
		return
	}
	m.openContextMenuState(tea.Mouse{X: max(0, m.width-20), Y: max(0, m.height-m.chromeBottomRows()-len(items)-2)}, menu)
}

// footerSegmentRect is the screen rect of the footer item for action.
func (m *App) footerSegmentRect(action string) (ui.Rect, bool) {
	if m.rootHost == nil || m.footerScreen == nil {
		return ui.Rect{}, false
	}
	footer, ok := m.rootHost.ScreenRect(shellfooter.ScreenID)
	if !ok || footer.Empty() {
		return ui.Rect{}, false
	}
	segment, ok := m.footerScreen.SegmentBounds(action)
	if !ok {
		return ui.Rect{}, false
	}
	segment.X += footer.X
	segment.Y += footer.Y
	return segment, true
}
