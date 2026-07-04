package viewmodel

import (
	"fmt"

	"charm.land/bubbletea/v2"

	"crude/tui/widgets"
	"crude/tui/wm"
)

func (m *App) showDirtyClosePrompt(path string) {
	if m.windowManager == nil {
		// No floating-window support? Just close and lose the data.
		m.closeEditorTabForce(path)
		return
	}
	tab, ok := m.EditorState.Tabs[path]
	if !ok {
		return
	}
	filename := tab.Editor.Title()
	detail := fmt.Sprintf("'%s' has unsaved changes.", filename)
	// Default focus is "Cancel" (index 2) so an accidental Enter keeps
	// the user's work safe.
	lines := widgets.ConfirmPrompt3Button(m.Theme,
		"Unsaved changes",
		"What do you want to do with the unsaved changes?",
		detail,
		widgets.ConfirmPromptWarning,
		m.DirtyPromptChoice,
		62,
	)

	const id = "dirty-close-prompt"
	m.windowManager.RemoveWindow(id)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow(id, "", pop, 62, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.windowManager.AddWindow(fw)
	m.DirtyPromptPath = path
	// Reset choice to Cancel (safe default) on open.
	m.DirtyPromptChoice = 2
}

// dirtyPromptSave is called when the user picks "Save" in the
// dirty-close prompt. Saves the file (if it has a path) and then
// closes the tab. If save fails, the popup stays open so the user
// can pick "Don't Save" or "Cancel".
func (m *App) dirtyPromptSave() {
	path := m.DirtyPromptPath
	if path == "" {
		return
	}
	tab, ok := m.EditorState.Tabs[path]
	if !ok {
		// Tab was already closed somehow; just dismiss.
		m.DirtyPromptPath = ""
		if m.windowManager != nil {
			m.windowManager.RemoveWindow("dirty-close-prompt")
		}
		return
	}
	// Save (synchronous). The editor's Save() returns an error; on
	// failure, surface it in the status bar and keep the popup open.
	if err := tab.Editor.Save(); err != nil {
		m.StatusBar.SetError(fmt.Sprintf("Save failed: %v", err))
		return
	}
	// Saved successfully — close the tab.
	m.DirtyPromptPath = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("dirty-close-prompt")
	}
	m.closeEditorTabForce(path)
}

// dirtyPromptDiscard is called when the user picks "Don't Save" in
// the dirty-close prompt. Closes the tab without saving.
func (m *App) dirtyPromptDiscard() {
	path := m.DirtyPromptPath
	m.DirtyPromptPath = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("dirty-close-prompt")
	}
	if path != "" {
		m.closeEditorTabForce(path)
	}
}

// dirtyPromptCancel is called when the user picks "Cancel" in the
// dirty-close prompt. Dismisses the popup and keeps the tab open.
func (m *App) dirtyPromptCancel() {
	m.DirtyPromptPath = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("dirty-close-prompt")
	}
}

// handleDirtyPromptKey routes keypresses when the dirty-close dialog
// is open. Cycles focus between Save / Don't Save / Cancel and
// dispatches the chosen action.
func (m *App) handleDirtyPromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.DirtyPromptPath == "" {
		return nil
	}
	switch msg.Code {
	case tea.KeyLeft, tea.KeyUp:
		m.DirtyPromptChoice = (m.DirtyPromptChoice + 2) % 3 // -1 mod 3
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyRight, tea.KeyDown:
		m.DirtyPromptChoice = (m.DirtyPromptChoice + 1) % 3
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyTab:
		// Shift+Tab moves focus left, plain Tab moves focus right.
		if msg.Mod.Contains(tea.ModShift) {
			m.DirtyPromptChoice = (m.DirtyPromptChoice + 2) % 3
		} else {
			m.DirtyPromptChoice = (m.DirtyPromptChoice + 1) % 3
		}
		m.refreshDirtyPrompt()
		return nil
	case tea.KeyEnter:
		switch m.DirtyPromptChoice {
		case 0:
			m.dirtyPromptSave()
		case 1:
			m.dirtyPromptDiscard()
		default:
			m.dirtyPromptCancel()
		}
		return nil
	case tea.KeyEsc:
		m.dirtyPromptCancel()
		return nil
	}
	switch msg.String() {
	case "s", "S":
		m.DirtyPromptChoice = 0
		m.dirtyPromptSave()
		return nil
	case "d", "D":
		m.DirtyPromptChoice = 1
		m.dirtyPromptDiscard()
		return nil
	case "c", "C":
		m.DirtyPromptChoice = 2
		m.dirtyPromptCancel()
		return nil
	}
	return nil
}

func (m *App) refreshDirtyPrompt() {
	if m.windowManager == nil {
		return
	}
	fw := m.windowManager.Window("dirty-close-prompt")
	if fw == nil {
		return
	}
	tab, ok := m.EditorState.Tabs[m.DirtyPromptPath]
	if !ok {
		return
	}
	filename := tab.Editor.Title()
	detail := fmt.Sprintf("'%s' has unsaved changes.", filename)
	lines := widgets.ConfirmPrompt3Button(m.Theme,
		"Unsaved changes",
		"What do you want to do with the unsaved changes?",
		detail,
		widgets.ConfirmPromptWarning,
		m.DirtyPromptChoice,
		62,
	)
	pop := widgets.NewPopupContent(lines)
	fw.SetSize(62, len(lines))
	fw.SetContent(pop)
	fw.MarkContentDirty()
}
