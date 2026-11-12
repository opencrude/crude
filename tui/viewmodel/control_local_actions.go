package viewmodel

import (
	"strings"

	"crude/tui/screens/codeactions"
	"crude/tui/screens/completion"
	"crude/tui/screens/inlay"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/screens/navigation"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"crude/types/control"
)

// describeLocalControl binds action metadata to a live product owner, not a
// label or an action prefix alone. Async reads/mutations still need the shell's
// final receipt bridge; Write describes local navigation, not completion.
func (m *App) describeLocalControl(owner shellControlHost, localID ui.ComponentID, action string, op ui.ControlOperation) (control.Effect, bool, error) {
	if state := m.settingsPrompt; state != nil && state.request.Kind == settingsui.PromptRemoveCodeTools &&
		owner.window != nil && owner.window == m.overlayHost.Window(ui.OverlaySettingsPrompt) {
		return control.Delete, true, nil
	}
	if request := m.projectTools.request; m.projectToolsCurrent(request) && owner.window != nil &&
		owner.window == m.overlayHost.Window(projectToolsOverlay) {
		// Selection and confirmation belong to the human who reviewed this exact
		// project preview. Generic control or key injection cannot grant consent.
		return control.Write, true, nil
	}
	if s := m.completion; m.completionCurrent(s) && owner.window == m.overlayHost.Window(ui.OverlayCompletion) {
		if localID == completion.ListID {
			switch action {
			case "select":
				return control.Read, false, nil
			case "activate", "scroll", "set_offset":
				return control.Write, false, nil
			}
		}
		if localID == completion.TextID {
			switch op.Verb {
			case "select_range", "scroll", "set_offset":
				return control.Write, false, nil
			}
		}
	}
	if s := m.inlayInspector; m.inlayInspectorCurrent(s) && owner.window == m.overlayHost.Window(ui.OverlayInlayHints) &&
		strings.HasPrefix(string(localID), string(inlay.ScreenID)+"/") {
		switch action {
		case inlay.ActionInspect, inlay.ActionResolve:
			return control.Read, false, nil
		case inlay.ActionBack, inlay.ActionClose:
			return control.Write, false, nil
		}
	}
	if s := m.navigation; m.navigationCurrent(s) && owner.window == m.overlayHost.Window(navigationOverlay) &&
		strings.HasPrefix(string(localID), string(navigation.ScreenID)+"/") {
		switch action {
		case navigation.ActionOpen, navigation.ActionClose:
			// Open includes a selected source read and private tab/caret change.
			return control.Write, false, nil
		}
	}
	if s := m.codeActions; m.codeActionsVisible(s) && owner.window == m.overlayHost.Window(codeActionsOverlay) &&
		strings.HasPrefix(string(localID), string(codeactions.ScreenID)+"/") {
		switch action {
		case codeactions.ActionPrepare:
			return control.Read, false, nil
		case codeactions.ActionBack, codeactions.ActionClose:
			return control.Write, false, nil
		}
	}
	if v := m.historySafety; v != nil && m.overlayHost != nil {
		if v.confirm != nil && owner.window != nil && owner.window == m.overlayHost.Window(historySafetyConfirmOverlay) && op.Verb == "activate" {
			// No generic key/button route may grant this human consent.
			return control.Write, true, nil
		}
		if m.historySafetyVisible(v) && owner.window == m.overlayHost.Window(ui.OverlayLocalHistory) &&
			strings.HasPrefix(string(localID), string(historyscreen.SafetyScreenID)+"/") {
			switch action {
			case historyscreen.ActionSafetyRelease, historyscreen.ActionSafetyReprotect:
				return control.Write, true, nil
			case historyscreen.ActionSafetyInspect, historyscreen.ActionSafetyNext, historyscreen.ActionSafetyRefresh, historyscreen.ActionSafetyCheck:
				return control.Read, false, nil
			case historyscreen.ActionSafetyBack, historyscreen.ActionSafetyRestore:
				return control.Write, false, nil
			}
		}
	}
	if p := m.LSPPopup.EditPreview; p != nil && owner.window != nil && owner.window == m.overlayHost.Window(reviewedEditOverlay) && op.Verb == "activate" {
		if frame, ok := owner.window.Content.(*ui.WindowFrameWidget); ok && frame.Body() == p &&
			strings.HasPrefix(string(localID), string(p.bar.ID())+"/") {
			switch action {
			case "apply":
				return control.Write, false, nil
			case "inspect":
				return control.Read, false, nil
			case "cancel":
				return control.Write, false, nil
			}
		}
	}
	if state := m.editorSnippet; state != nil && m.ownsSnippetChoice(state.choice) {
		p := state.choice
		if owner.window == p.window && localID == p.list.ID() && action == "snippet.choose" && op.Verb == "activate" {
			if frame, ok := p.window.Content.(*ui.WindowFrameWidget); ok && frame.Body() == p {
				return control.Write, false, nil
			}
		}
	}
	if owner.window != nil && m.capabilityPicker != nil {
		if frame, ok := owner.window.Content.(*ui.WindowFrameWidget); ok && frame.Body() == m.capabilityPicker.screen && strings.HasPrefix(string(localID), "capability-picker/") {
			// A card or detail switch commits the session gate; it completes
			// with the session settings receipt.
			if strings.HasSuffix(string(localID), "/switch") && (action == "activate" || action == "set_checked") {
				return control.Write, true, nil
			}
			switch action {
			case "activate", "set_checked", "scroll", "set_offset", "tabs.activate", "tabs.enter", "capability-picker.focus-list", "capability-picker.close", "capability-picker.manage":
				return control.Write, false, nil
			case "capability-picker.toggle":
				return control.Write, true, nil
			}
		}
	}
	if owner.window == nil && owner.prefix == "" && m.memoryBrowserVisible() && strings.HasPrefix(string(localID), "memorybrowser/") {
		var err error
		action, err = m.memoryBrowser.ResolveControlAction(localID, action, op)
		if err != nil {
			return "", false, err
		}
		switch action {
		case "memorybrowser.activate", "memorybrowser.inspect", "memorybrowser.retry", "memorybrowser.reload":
			return control.Read, false, nil
		case "set_value", "replace", "undo", "redo", "select_range", "tabs.activate", "tabs.enter", "memorybrowser.hide", "memorybrowser.back", "form.cancel", "memorybrowser.menu", "memorybrowser.browse-actions", "memorybrowser.details", "memorybrowser.delete", "form.changed":
			return control.Write, false, nil
		case "memorybrowser.pin", "memorybrowser.save":
			return control.Write, false, nil
		case "memorybrowser.confirm-delete":
			return control.Delete, false, nil
		case "memorybrowser.copy":
			return "", false, &control.Fault{Code: control.Unavailable, Message: "clipboard export has no semantic receipt"}
		}
	}
	if owner.window == nil && owner.prefix == "" && m.boardBrowser != nil && m.termDock != nil && m.termDock.IsOpen() && m.termDock.Panel(m.termDock.ActivePanel()) == m.boardBrowser && strings.HasPrefix(string(localID), "boardbrowser/") {
		var err error
		action, err = m.boardBrowser.ResolveControlAction(localID, action, op)
		if err != nil {
			return "", false, err
		}
		switch action {
		case "board.inspect", "board.refresh", "board.search", "board.next":
			return control.Read, false, nil
		case "tabs.activate", "tabs.enter", "board.menu", "board.close-menu", "form.changed", "board.back", "board.mark-read":
			return control.Write, false, nil
		}
	}
	return "", false, nil
}
