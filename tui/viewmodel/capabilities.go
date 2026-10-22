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

	"crude/sessiondb"
	"crude/tui/screens/activitybar"
	"crude/tui/screens/launcher"
	"crude/tui/screens/shellfooter"
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
	sequence   uint64
	snapshot   captypes.Snapshot
	err        error
}

func (app *App) capabilitySnapshotCmd() tea.Cmd {
	owner, generation := app.ws, app.workspaceSwitchGen.Load()
	if owner == nil {
		return nil
	}
	if app.capabilitySnapshotCancel != nil {
		app.capabilitySnapshotCancel()
	}
	app.capabilitySnapshotSequence++
	sequence := app.capabilitySnapshotSequence
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	app.capabilitySnapshotCancel = cancel
	return func() tea.Msg {
		defer cancel()
		snapshot, err := owner.CapabilitySnapshot(ctx)
		return capabilitySnapshotMsg{generation: generation, sequence: sequence, snapshot: snapshot, err: err}
	}
}

func (app *App) applyCapabilitySnapshot(message capabilitySnapshotMsg) {
	if message.generation != app.workspaceSwitchGen.Load() || message.sequence != app.capabilitySnapshotSequence {
		return
	}
	app.capabilitySnapshotCancel = nil
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
		// A new home screen still needs the retained snapshot even when shared
		// runtime state did not change while the previous home was unmounted.
		if projectLauncher {
			app.projectLauncherCapabilities(snapshot)
		}
		return
	}
	app.capabilitySnapshot = snapshot
	if app.capabilityPicker != nil {
		app.capabilityPicker.screen.SetSnapshot(snapshot)
		app.invalidateCapabilityPicker()
	}
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
		if app.ws.AgentTurnActive(binding.AgentID) {
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
	if app.ws == nil || app.launcherCapabilityCancel != nil || now.Sub(app.launcherCapabilityAt) < 2*time.Second {
		return nil
	}
	app.launcherCapabilityAt = now
	app.launcherCapabilitySequence++
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	app.launcherCapabilityCancel = cancel
	owner, screen, generation, sequence := app.ws, app.launcherScreen, app.workspaceSwitchGen.Load(), app.launcherCapabilitySequence
	return func() tea.Msg {
		defer cancel()
		snapshot, err := owner.CapabilityRuntimeSnapshot(ctx)
		return launcherCapabilitiesMsg{screen: screen, workspace: generation, sequence: sequence, snapshot: snapshot, err: err}
	}
}

func (app *App) capabilityObservationVisible() bool {
	if app.launcherHostVisible() || app.overlayHost.Window(ui.OverlayWorkspaceSettings) != nil || app.capabilityPicker != nil {
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
	if operation, exists := app.capabilityOperations[message.Supersedes]; message.Supersedes != 0 && exists && operation.target == message.Target {
		operation.cancel()
		delete(app.capabilityOperations, message.Supersedes)
	}
	if message.Action.Operation == "cancel" {
		sequence, _ := strconv.ParseUint(message.Action.Arguments["sequence"], 10, 64)
		if operation, exists := app.capabilityOperations[sequence]; exists && operation.target == message.Target {
			operation.cancel()
			delete(app.capabilityOperations, sequence)
		}
		result.Content = "Cancellation requested for this operation; shared acquisitions may finish for other users."
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
	owner := app.ws
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
	app.capabilitySnapshotSequence++
	for sequence, operation := range app.capabilityOperations {
		operation.cancel()
		delete(app.capabilityOperations, sequence)
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
		// An accepted operation supersedes older explicit reads as well as polls.
		if app.capabilitySnapshotCancel != nil {
			app.capabilitySnapshotCancel()
		}
		app.capabilitySnapshotSequence++
		app.applyCapabilitySnapshot(capabilitySnapshotMsg{generation: message.WorkspaceGeneration, sequence: app.capabilitySnapshotSequence, snapshot: message.Snapshot})
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

// applyCapabilitySearchTick lets the exact settings model that armed a live
// search decide whether its quiet period still names the current query.
func (app *App) applyCapabilitySearchTick(message settingsui.CapabilitySearchTickMsg) tea.Cmd {
	window := app.overlayHost.Window(ui.OverlayWorkspaceSettings)
	if window == nil {
		return nil
	}
	body := app.settingsSurface(window)
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

func (app *App) openNavigationMenu() {
	items := make([]ui.ContextMenuItem, 0, 5)
	for _, view := range activitybar.Views() {
		selected := view
		items = append(items, ui.ContextMenuItem{Label: view.Label(), Action: func() { app.startSourceControlMutation(app.toggleActivity(selected)) }})
	}
	app.openContextMenuState(tea.Mouse{X: app.frameLeftCol() + activitybar.Width, Y: app.shellTopRow()}, &ui.ContextMenuState{Title: "Navigation", Sections: []ui.ContextMenuSection{{Items: items}}})
}

func (app *App) openFooterMenu() {
	segments := app.footerStatusSegments()
	items := make([]ui.ContextMenuItem, 0, len(segments))
	for _, segment := range segments {
		if segment.Action == "" {
			continue
		}
		// Use the footer's projection and dispatch path so overflow cannot
		// lose a destination or acquire different toggle/refresh behavior.
		message := shellfooter.ActionMsg{Action: segment.Action, TargetID: segment.TargetID}
		items = append(items, ui.ContextMenuItem{Label: segment.Text, Action: func() {
			app.runDetachedAsyncCmd(func() tea.Msg { return message })
		}})
	}
	app.openContextMenuState(tea.Mouse{X: max(0, app.width-20), Y: max(0, app.height-app.chromeBottomRows()-len(items)-2)}, &ui.ContextMenuState{Title: "Views", Sections: []ui.ContextMenuSection{{Items: items}}})
}
