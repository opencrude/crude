package viewmodel

import (
	"strings"

	"crude/tui/screens/codeactions"
	historyscreen "crude/tui/screens/localhistory"
	"crude/tui/ui"
	"crude/types/control"
)

// describeLocalControl binds action metadata to a live product owner, not a
// label or an action prefix alone. Async reads/mutations still need the shell's
// final receipt bridge; Write describes local navigation, not completion.
func (m *App) describeLocalControl(owner shellControlHost, localID ui.ComponentID, action string, op ui.ControlOperation) (control.Effect, bool, error) {
	if s := m.codeActions; m.codeActionsVisible(s) && owner.window == m.overlayHost.Window(codeActionsOverlay) &&
		strings.HasPrefix(string(localID), string(codeactions.ScreenID)+"/") {
		switch action {
		case codeactions.ActionInspect, codeactions.ActionPrepare:
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
		case "set_value", "replace", "undo", "redo", "select_range", "tabs.activate", "tabs.enter", "memorybrowser.hide", "memorybrowser.back", "form.cancel", "memorybrowser.menu", "memorybrowser.browse-actions", "memorybrowser.correct", "memorybrowser.retire", "form.changed":
			return control.Write, false, nil
		case "memorybrowser.pin", "memorybrowser.save":
			return control.Write, false, nil
		case "memorybrowser.confirm-retire":
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
