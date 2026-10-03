package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/sessiondb"
	"crude/tui/screens/capabilitypicker"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

type capabilityPickerOwner struct {
	screen     *capabilitypicker.Screen
	session    *workspace.Session
	agentID    string
	draft      string
	generation uint64
	sequence   uint64
	cancel     context.CancelFunc
}

type capabilityPickerResultMsg struct {
	owner    *capabilityPickerOwner
	sequence uint64
	content  string
	err      error
}

func (app *App) openCapabilityPicker() {
	if app.capabilityPicker != nil {
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
	}
	owner := &capabilityPickerOwner{screen: capabilitypicker.New(app.Theme, app.capabilitySnapshot), session: app.ActiveSession, agentID: app.activeAgentTabID(), draft: app.Input.Value(), generation: app.workspaceSwitchGen.Load()}
	app.capabilityPicker = owner
	width, height := fitFloatingSize(app.width, app.height, 90, 24, 44, 14)
	frame := ui.NewWindowFrameWidget(app.Theme, "capability-picker/frame", ui.FrameWindow, "Capabilities · stage in composer", owner.screen, nil, true, width, height)
	owner.screen.SetFocusHandler(frame.Focus)
	window := wm.NewFloatingWindow(string(ui.OverlayCapabilityPicker), "Capabilities", frame, width, height, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithClosable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(window, app.width, app.height)
	app.overlayHost.Open(ui.OverlayCapabilityPicker, window, ui.ModalDialogPolicy(), func() {
		if owner.cancel != nil {
			owner.cancel()
		}
		if app.capabilityPicker == owner {
			app.capabilityPicker = nil
		}
	})
	frame.Host().MarkLayout()
	frame.Host().Resize(width, height)
	frame.Host().Layout()
	owner.screen.FocusSearch()
}

func (app *App) capabilityPickerOwnsDraft(owner *capabilityPickerOwner) bool {
	return owner != nil && owner == app.capabilityPicker && owner.generation == app.workspaceSwitchGen.Load() && owner.session != nil && owner.session == app.ActiveSession && owner.agentID != "" && owner.agentID == app.activeAgentTabID() && owner.draft == app.Input.Value()
}

func (app *App) handleCapabilityPickerAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "capability-picker.") || app.capabilityPicker == nil {
		return nil, false
	}
	owner := app.capabilityPicker
	switch action.ID {
	case "capability-picker.close":
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
	case "capability-picker.insert":
		selection, ok := action.Payload.(capabilitypicker.Selection)
		if !ok || !app.capabilityPickerOwnsDraft(owner) {
			app.Feedback.NotifyError("Select an owning agent; capability selection did not change the composer")
			return nil, true
		}
		if selection.Kind == "skills" {
			app.Input.SetValuePreserveReferences("/skill " + selection.ID + " " + owner.draft)
			app.overlayHost.Close(ui.OverlayCapabilityPicker)
			return nil, true
		}
		if owner.session.RefStore == nil {
			return nil, true
		}
		request := sessiondb.CreateRequest{Kind: sessiondb.KindMCPTool, Title: selection.ID, Mime: "text/plain", Inline: true, Content: fmt.Sprintf("Selected tool: %s\nConnection: %s\nObserved schema: %s\nThis is user selection, not execution approval. Current effect/scope grants and role ceilings still apply.", selection.ID, selection.Connection, selection.Source)}
		generation, draft, agentID := owner.generation, owner.draft, owner.agentID
		store := owner.session.RefStore
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
		return func() tea.Msg {
			reference, err := store.CreateReference(request)
			return capabilityContextMsg{generation: generation, owner: owner.session, agentID: agentID, draft: draft, reference: reference, result: settingsui.CapabilityResultMsg{Err: err, WorkspaceGeneration: generation}}
		}, true
	case "capability-picker.fetch":
		action, valid := owner.screen.FetchAction()
		if !valid {
			return nil, true
		}
		if owner.cancel != nil {
			owner.cancel()
		}
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		owner.cancel = cancel
		owner.sequence++
		sequence, workspaceOwner := owner.sequence, app.Workspace
		return func() tea.Msg {
			defer cancel()
			_, _, content, err := workspaceOwner.CapabilityAction(ctx, action)
			return capabilityPickerResultMsg{owner: owner, sequence: sequence, content: content, err: err}
		}, true
	case "capability-picker.stage":
		if !app.capabilityPickerOwnsDraft(owner) {
			app.Feedback.NotifyError("Composer owner or draft changed; context was not attached")
			return nil, true
		}
		action := owner.screen.StageAction()
		var response struct{ IsError, Unknown bool }
		if action.Content == "" || json.Unmarshal([]byte(action.Content), &response) != nil || response.IsError || response.Unknown {
			return nil, true
		}
		command := app.capabilityRequestCmd(settingsui.CapabilityRequestMsg{Action: action})
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
		return command, true
	}
	return nil, true
}

func (app *App) applyCapabilityPickerResult(message capabilityPickerResultMsg) {
	owner := app.capabilityPicker
	if owner == nil || owner != message.owner || owner.sequence != message.sequence || owner.generation != app.workspaceSwitchGen.Load() {
		return
	}
	owner.screen.Apply(message.content, message.err)
	if window := app.overlayHost.Window(ui.OverlayCapabilityPicker); window != nil {
		window.Content.(*ui.WindowFrameWidget).Host().MarkLayout()
		window.MarkContentDirty()
	}
}
