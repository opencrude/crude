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

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/launcher"
	"crude/tui/screens/termdock"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
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
	snapshot   captypes.Snapshot
	err        error
}

func (app *App) capabilitySnapshotCmd() tea.Cmd {
	owner, generation := app.Workspace, app.workspaceSwitchGen.Load()
	if owner == nil {
		return nil
	}
	if app.capabilitySnapshotCancel != nil {
		app.capabilitySnapshotCancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	app.capabilitySnapshotCancel = cancel
	return func() tea.Msg {
		defer cancel()
		snapshot, err := owner.CapabilitySnapshot(ctx)
		return capabilitySnapshotMsg{generation: generation, snapshot: snapshot, err: err}
	}
}

func (app *App) applyCapabilitySnapshot(message capabilitySnapshotMsg) {
	if message.generation != app.workspaceSwitchGen.Load() {
		return
	}
	// An explicit refresh supersedes periodic observations already in flight.
	if app.launcherCapabilityCancel != nil {
		app.launcherCapabilityCancel()
		app.launcherCapabilityCancel = nil
	}
	app.launcherCapabilitySequence++
	app.applyObservedCapabilitySnapshot(message.snapshot, message.err, true)
}

func (app *App) applyObservedCapabilitySnapshot(snapshot captypes.Snapshot, err error, projectLauncher bool) {
	if err != nil {
		if snapshot.Generation == 0 {
			snapshot = app.capabilitySnapshot
		}
		snapshot.Error = err.Error()
	} else if snapshot.Generation == 0 {
		return
	}
	if snapshot.Generation < app.capabilitySnapshot.Generation {
		return
	}
	if reflect.DeepEqual(snapshot, app.capabilitySnapshot) {
		return
	}
	app.capabilitySnapshot = snapshot
	app.refreshActivityList()
	if app.activityBar != nil {
		for _, kind := range []captypes.Kind{captypes.KindSkill, captypes.KindMCP} {
			count := 0
			for _, entry := range app.capabilitySnapshot.Entries {
				if entry.Kind == kind && (entry.State == "failed" || entry.State == "unavailable" || entry.State == "needs sign-in") {
					count++
				}
			}
			view := activitySkills
			if kind == captypes.KindMCP {
				view = activityMCP
			}
			app.activityBar.SetBadge(view, count)
		}
	}
	if projectLauncher && app.launcherScreen != nil {
		app.projectLauncherCapabilities(app.capabilitySnapshot)
	}
	if window := app.overlayHost.Window(ui.OverlayWorkspaceSettings); window != nil {
		if body := app.settingsSurface(window); body != nil {
			body.Model().SetCapabilitySnapshot(app.capabilitySnapshot)
			if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
				frame.Host().MarkLayout()
			}
			window.MarkContentDirty()
		}
	}
}

func (app *App) projectLauncherCapabilities(snapshot captypes.Snapshot) {
	if app.launcherScreen == nil {
		return
	}
	bindings := make([]captypes.AgentBinding, 0, len(snapshot.Bindings))
	for _, binding := range snapshot.Bindings {
		if app.AgentTurnActive(binding.AgentID) {
			bindings = append(bindings, binding)
		}
	}
	snapshot.Bindings = bindings
	app.launcherScreen.SetCapabilities(snapshot)
	app.rootHost.MarkLayout()
}

type launcherCapabilitiesMsg struct {
	screen              *launcher.Screen
	workspace, sequence uint64
	snapshot            captypes.Snapshot
	err                 error
}

func (app *App) launcherCapabilitiesCmd(now time.Time) tea.Cmd {
	// Settings and capability sidebars also observe this workspace. An active
	// agent removes the launcher screen; only its optional projection depends
	// on that screen, never scheduling the shared runtime observation.
	if app.Workspace == nil || app.launcherCapabilityCancel != nil || now.Sub(app.launcherCapabilityAt) < 2*time.Second {
		return nil
	}
	app.launcherCapabilityAt = now
	app.launcherCapabilitySequence++
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	app.launcherCapabilityCancel = cancel
	owner, screen, generation, sequence := app.Workspace, app.launcherScreen, app.workspaceSwitchGen.Load(), app.launcherCapabilitySequence
	return func() tea.Msg {
		defer cancel()
		snapshot, err := owner.CapabilityRuntimeSnapshot(ctx)
		return launcherCapabilitiesMsg{screen: screen, workspace: generation, sequence: sequence, snapshot: snapshot, err: err}
	}
}

func (app *App) capabilityObservationVisible() bool {
	if app.launcherHostVisible() || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != nil {
		return true
	}
	return app.Tree != nil && app.Tree.Visible && (app.activityView == activitySkills || app.activityView == activityMCP)
}

func (app *App) applyLauncherCapabilities(msg launcherCapabilitiesMsg) {
	if msg.workspace != app.workspaceSwitchGen.Load() || msg.sequence != app.launcherCapabilitySequence {
		return
	}
	app.launcherCapabilityCancel = nil
	if msg.snapshot.Generation == 0 && msg.err == nil {
		return
	}
	app.applyObservedCapabilitySnapshot(msg.snapshot, msg.err, msg.screen == app.launcherScreen)
}

func (app *App) capabilitySidebarItems(view activitybar.View) []ui.ListItem {
	kind := captypes.KindSkill
	if view == activityMCP {
		kind = captypes.KindMCP
	}
	items := []ui.ListItem{{ID: "capability.manage", Label: "Manage " + view.Label(), Command: true}}
	for _, entry := range app.capabilitySnapshot.Entries {
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

func (app *App) openCapabilityManager(view activitybar.View, query, id string) tea.Cmd {
	kind := captypes.KindSkill
	if view == activityMCP {
		kind = captypes.KindMCP
	}
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if window == nil {
		// Capabilities are application-owned by default: an install belongs to
		// the user, not to one project, so removing a workspace cannot destroy
		// it. The Application/Workspace footer toggle remains the explicit way
		// to bind an installation to the current workspace.
		app.openSettings(types.SettingsScopeApplication)
		window = app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	}
	body := app.settingsSurface(window)
	if body == nil {
		return nil
	}
	result := body.OpenCapabilities(kind, query, id)
	if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
		frame.Host().MarkLayout()
		frame.Host().Layout()
		body.FocusForm()
	}
	window.MarkContentDirty()
	return tea.Batch(result.Cmd, app.capabilitySnapshotCmd())
}

func (app *App) capabilityRequestCmd(message settingsui.CapabilityRequestMsg) tea.Cmd {
	generation := app.workspaceSwitchGen.Load()
	result := settingsui.CapabilityResultMsg{Target: message.Target, Action: message.Action, Snapshot: app.capabilitySnapshot, Sequence: message.Sequence, WorkspaceGeneration: generation}
	if message.Action.Operation == "cancel" {
		sequence, _ := strconv.ParseUint(message.Action.Arguments["sequence"], 10, 64)
		if operation, exists := app.capabilityOperations[sequence]; exists && operation.target == message.Target {
			operation.cancel()
			delete(app.capabilityOperations, sequence)
		}
		result.Content = "Cancellation requested for this operation; shared acquisitions may finish for other users."
		return func() tea.Msg { return result }
	}
	if message.Action.Operation == "use" {
		agent := app.AgentMgr.Get(app.activeAgentTabID())
		if agent == nil || !agent.IsOrchestrator() {
			result.Err = fmt.Errorf("select a root agent before staging a skill")
			return func() tea.Msg { return result }
		}
		app.Input.SetValuePreserveReferences("/skill " + message.Action.ID + " " + app.Input.Value())
		result.Content = "Skill invocation staged in the current composer; nothing was sent."
		return func() tea.Msg { return result }
	}
	if message.Action.Operation == "stage-context" {
		owner, draft := app.ActiveSession, app.Input.Value()
		agentID := app.activeAgentTabID()
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
	owner := app.Workspace
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	if app.capabilityOperations == nil {
		app.capabilityOperations = make(map[uint64]capabilityOperation)
	}
	app.capabilityOperations[message.Sequence] = capabilityOperation{target: message.Target, cancel: cancel}
	command := func() tea.Msg {
		defer cancel()
		snapshot, entries, content, err := owner.CapabilityAction(ctx, message.Action)
		return settingsui.CapabilityResultMsg{Target: message.Target, Action: message.Action, Snapshot: snapshot, Entries: entries, Content: content, Err: err, Sequence: message.Sequence, WorkspaceGeneration: generation}
	}
	if message.Action.Operation == "sign-in" || message.Action.Operation == "connect" {
		return tea.Batch(command, app.capabilityPollCmd(generation, message.Sequence))
	}
	return command
}

func (app *App) capabilityPollCmd(generation, sequence uint64) tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg { return capabilityPollMsg{generation: generation, sequence: sequence} })
}

func (app *App) cancelCapabilityOperations() {
	if app.launcherCapabilityCancel != nil {
		app.launcherCapabilityCancel()
		app.launcherCapabilityCancel = nil
	}
	app.launcherCapabilitySequence++
	app.launcherCapabilityAt = time.Time{}
	if app.capabilitySnapshotCancel != nil {
		app.capabilitySnapshotCancel()
		app.capabilitySnapshotCancel = nil
	}
	for sequence, operation := range app.capabilityOperations {
		operation.cancel()
		delete(app.capabilityOperations, sequence)
	}
}

type skillInvocationMsg struct {
	workspace     uint64
	agentID       string
	draft         string
	draftRevision uint64
	sequence      uint64
	input         provider.UserInput
	err           error
}

func (app *App) invokeSkill(remainder, draft string) tea.Cmd {
	return app.invokeSkillWithRefs(remainder, draft, nil)
}

func (app *App) invokeSkillWithRefs(remainder, draft string, refIDs []string) tea.Cmd {
	id, arguments := remainder, ""
	if split := strings.IndexAny(remainder, " \t\r\n"); split >= 0 {
		id = remainder[:split]
		arguments = remainder[split+1:]
	} else {
		arguments = ""
	}
	agentID := app.activeAgentTabID()
	if id == "" || agentID == "" {
		app.Input.SetValuePreserveReferences(draft)
		app.Feedback.NotifyError("Select an active agent and a source-qualified skill identity")
		return nil
	}
	owner, generation := app.Workspace, app.workspaceSwitchGen.Load()
	app.skillInvocationSequence++
	sequence := app.skillInvocationSequence
	draftRevision := app.Input.DraftRevision()
	references := append([]string(nil), refIDs...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		input, err := owner.PrepareSkillInput(ctx, agentID, id, arguments)
		input.ReferenceIDs = references
		return skillInvocationMsg{workspace: generation, agentID: agentID, draft: draft, draftRevision: draftRevision, sequence: sequence, input: input, err: err}
	}
}

func (app *App) applyCapabilityResult(message settingsui.CapabilityResultMsg) tea.Cmd {
	if operation, exists := app.capabilityOperations[message.Sequence]; exists && operation.target == message.Target {
		operation.cancel()
		delete(app.capabilityOperations, message.Sequence)
	}
	if message.WorkspaceGeneration != app.workspaceSwitchGen.Load() {
		return nil
	}
	if message.Snapshot.Generation != 0 && message.Snapshot.Generation >= app.capabilitySnapshot.Generation {
		app.applyCapabilitySnapshot(capabilitySnapshotMsg{generation: message.WorkspaceGeneration, snapshot: message.Snapshot})
	}
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if window != nil {
		if body := app.settingsSurface(window); body != nil && body.Model() == message.Target {
			_, command := body.Model().Update(message)
			body.SetModel(body.Model())
			if frame, ok := window.Content.(*ui.WindowFrameWidget); ok {
				frame.Host().MarkLayout()
			}
			window.MarkContentDirty()
			app.refreshActivityList()
			if message.Err == nil && message.Action.Operation == "open-file" {
				open, err := app.openEditorTab(message.Content)
				if err != nil {
					app.Feedback.NotifyError(err.Error())
				}
				return tea.Batch(command, open)
			}
			return command
		}
	}
	app.refreshActivityList()
	return nil
}

func (app *App) openNavigationMenu() {
	items := make([]ui.ContextMenuItem, 0, 5)
	for _, view := range activitybar.Views() {
		selected := view
		items = append(items, ui.ContextMenuItem{Label: view.Label(), Action: func() { app.startSourceControlMutation(app.toggleActivity(selected)) }})
	}
	app.openContextMenuState(tea.Mouse{X: app.frameLeftCol() + activitybar.Width, Y: app.shellTopRow()}, &ui.ContextMenuState{Title: "Navigation", Sections: []ui.ContextMenuSection{{Items: items}}})
}

func (app *App) openFooterMenu() {
	items := []ui.ContextMenuItem{
		{Label: "Problems", Action: func() { app.toggleWorkspacePanel(termdock.PanelProblems) }},
		{Label: "Notices", Action: func() { app.toggleWorkspacePanel(termdock.PanelNotices) }},
		{Label: "Terminals", Action: func() { app.toggleWorkspacePanel(termdock.PanelTerminal) }},
		{Label: "Board", Action: func() { app.startSourceControlMutation(app.toggleBoardBrowser()) }},
		{Label: "Memory", Action: func() { _, command := app.toggleMemoryBrowser(); app.startSourceControlMutation(command) }},
		{Label: "Workspace", Action: func() { app.toggleWorkspacePanel(termdock.PanelWorkspace) }},
	}
	app.openContextMenuState(tea.Mouse{X: max(0, app.width-20), Y: max(0, app.height-app.chromeBottomRows()-len(items)-2)}, &ui.ContextMenuState{Title: "Workspace views", Sections: []ui.ContextMenuSection{{Items: items}}})
}
