package viewmodel

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/capabilitypicker"
	"crude/tui/ui"
	"crude/tui/wm"
	captypes "crude/types/capability"
)

type capabilityPickerOwner struct {
	screen     *capabilitypicker.Screen
	generation uint64
	sequence   uint64
	cancel     context.CancelFunc
}

type capabilityPickerResultMsg struct {
	owner    *capabilityPickerOwner
	sequence uint64
	snapshot captypes.Snapshot
	err      error
}

func (app *App) openCapabilityPicker() tea.Cmd {
	return app.openCapabilityPickerTab(captypes.KindSkill, "")
}

func (app *App) openCapabilityPickerTab(kind captypes.Kind, query string) tea.Cmd {
	if app.capabilityPicker != nil {
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
	}
	owner := &capabilityPickerOwner{screen: capabilitypicker.New(app.Theme, app.capabilitySnapshot), generation: app.workspaceSwitchGen.Load()}
	owner.screen.SetCategory(kind)
	owner.screen.SetQuery(query)
	app.capabilityPicker = owner
	width, height := fitFloatingSize(app.width, app.height, 84, 28, 36, 12)
	frame := ui.NewWindowFrameWidget(app.Theme, "capability-picker/frame", ui.FrameWindow, "Capabilities", owner.screen, nil, true, width, height)
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
	return app.capabilityPickerCmd(owner, nil)
}

func (app *App) capabilityPickerCmd(owner *capabilityPickerOwner, action *captypes.Action) tea.Cmd {
	if owner.cancel != nil {
		owner.cancel()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	owner.cancel = cancel
	owner.sequence++
	sequence, workspaceOwner := owner.sequence, app.ws
	return func() tea.Msg {
		defer cancel()
		var snapshot captypes.Snapshot
		var err error
		if action == nil {
			snapshot, err = workspaceOwner.CapabilitySnapshot(ctx)
		} else {
			snapshot, _, _, err = workspaceOwner.CapabilityAction(ctx, *action)
		}
		return capabilityPickerResultMsg{owner: owner, sequence: sequence, snapshot: snapshot, err: err}
	}
}

func (app *App) handleCapabilityPickerAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "capability-picker.") || app.capabilityPicker == nil {
		return nil, false
	}
	owner := app.capabilityPicker
	switch action.ID {
	case "capability-picker.close":
		app.overlayHost.Close(ui.OverlayCapabilityPicker)
	case "capability-picker.toggle":
		if owner.generation != app.workspaceSwitchGen.Load() {
			return nil, true
		}
		operation, ok := owner.screen.BeginAction()
		if ok {
			app.invalidateCapabilityPicker()
			return app.capabilityPickerCmd(owner, &operation), true
		}
	}
	return nil, true
}

func (app *App) invalidateCapabilityPicker() {
	if window := app.overlayHost.Window(ui.OverlayCapabilityPicker); window != nil {
		window.Content.(*ui.WindowFrameWidget).Host().MarkLayout()
		window.MarkContentDirty()
	}
}

func (app *App) applyCapabilityPickerResult(message capabilityPickerResultMsg) {
	owner := app.capabilityPicker
	if owner == nil || owner != message.owner || owner.sequence != message.sequence || owner.generation != app.workspaceSwitchGen.Load() {
		return
	}
	owner.cancel = nil
	app.applyObservedCapabilitySnapshot(message.snapshot, message.err, true)
	owner.screen.Apply(app.capabilitySnapshot, message.err)
	app.invalidateCapabilityPicker()
}
