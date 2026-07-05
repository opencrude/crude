package viewmodel

import (
	"fmt"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/widgets"
	"crude/tui/wm"
)

// pathUnderMouse resolves a file tree path from a screen Y.
func (m *App) pathUnderMouse(mouse tea.Mouse) string {
	if m.Tree.Model == nil {
		return ""
	}
	visible := m.Tree.Model.VisibleRows()
	if visible == nil {
		return ""
	}
	row := mouse.Y - m.Tree.YOffset
	if row < 0 || row >= len(visible) {
		return ""
	}
	return visible[row]
}

// openFileFromTree opens a file in the editor when triggered from
// the file tree context menu.
func (m *App) openFileFromTree(path string) (tea.Cmd, error) {
	return m.openEditorTab(path)
}

// startNewFileInTree starts a small prompt for naming a new file
// inside the given directory. Opens a focused text-input popup
// above the active editor. On Enter, the file is created (empty)
// and the tree is updated.
func (m *App) startNewFileInTree(dir string) {
	m.openTreeTextPrompt(widgets.TextPromptNewFile, "New file",
		"in "+dir, dir, "", func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			full := filepath.Join(dir, value)
			if _, err := os.Stat(full); err == nil {
				return fmt.Errorf("already exists: %s", value)
			}
			f, err := os.Create(full)
			if err != nil {
				return err
			}
			_ = f.Close()
			m.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileCreated, Path: full,
			})
			m.StatusBar.SetInfo("Created " + value)
			// Open the new file in the editor so the user can
			// start typing immediately.
			if _, err := m.openEditorTab(full); err != nil {
				m.StatusBar.SetError("open " + value + ": " + err.Error())
			}
			return nil
		})
}

// startNewFolderInTree starts a small prompt for naming a new
// folder inside the given directory. On Enter, the folder is
// created and the tree is updated.
func (m *App) startNewFolderInTree(dir string) {
	m.openTreeTextPrompt(widgets.TextPromptNewFolder, "New folder",
		"in "+dir, dir, "", func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			full := filepath.Join(dir, value)
			if _, err := os.Stat(full); err == nil {
				return fmt.Errorf("already exists: %s", value)
			}
			if err := os.MkdirAll(full, 0o755); err != nil {
				return err
			}
			m.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileCreated, Path: full,
			})
			// Expand the parent so the new folder is visible.
			m.Tree.Model.ExpandToPath(full)
			m.StatusBar.SetInfo("Created folder " + value)
			return nil
		})
}

// startRenameInTree starts a rename prompt prefilled with the
// current name of the path. On Enter, the file or folder is
// renamed; if it was open in an editor, the tab key is updated
// to follow the new path.
func (m *App) startRenameInTree(path string) {
	if path == "" {
		return
	}
	base := filepath.Base(path)
	m.openTreeTextPrompt(widgets.TextPromptRename, "Rename",
		path, "", base, func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			if value == base {
				return fmt.Errorf("name unchanged")
			}
			dir := filepath.Dir(path)
			newPath := filepath.Join(dir, value)
			if _, err := os.Stat(newPath); err == nil {
				return fmt.Errorf("target already exists: %s", value)
			}
			if err := os.Rename(path, newPath); err != nil {
				return err
			}
			m.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{
				Type: filetree.FileRenamed, OldPath: path, Path: newPath,
			})
			// If the file was open in an editor tab, retarget
			// the tab to the new path so the user keeps their
			// cursor/scroll/undo.
			if tab, ok := m.EditorState.Tabs[path]; ok {
				delete(m.EditorState.Tabs, path)
				tab.Path = newPath
				m.EditorState.Tabs[newPath] = tab
			}
			// Update the active editor pointer if the renamed
			// file is currently active.
			if m.EditorState.ActiveView == path {
				m.EditorState.ActiveView = newPath
			}
			m.StatusBar.SetInfo(fmt.Sprintf("Renamed to %s", value))
			return nil
		})
}

// startDeleteInTree starts a destructive-action confirmation. For
// directories the warning is more emphatic; for files it's brief.
// On Yes, the file or directory is removed, the tree updates, and
// any open editor tab for the file is closed.
func (m *App) startDeleteInTree(path string) {
	if path == "" {
		return
	}
	isDir := m.Tree.Model.IsDir(path)
	base := filepath.Base(path)

	var question, detail string
	if isDir {
		question = fmt.Sprintf("Delete folder %q and all its contents?", base)
		detail = path
	} else {
		question = fmt.Sprintf("Delete %q?", base)
		detail = path
	}
	lines := widgets.ConfirmPrompt(m.Theme,
		"Delete",
		question,
		detail,
		widgets.ConfirmPromptDanger,
		false,
		60,
	)
	pop := widgets.NewPopupContent(lines)
	fw := wm.NewFloatingWindow("tree-delete-confirm", "", pop,
		60, len(lines),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
		m.windowManager.AddWindow(fw)
	}
	m.TreeDeletePending = path
}

// openTreeTextPrompt opens the focused text-input popup used by
// new-file / new-folder / rename. Centralizes the popup creation
// so the three callers stay small. The submit callback returns a
// non-nil error to surface validation issues; the popup stays open
// in that case so the user can fix the name.
func (m *App) openTreeTextPrompt(kind widgets.TextPromptKind, title, prompt, dir, initial string, onSubmit func(value string) error) {
	if onSubmit == nil {
		return
	}
	st := &widgets.TextPromptState{
		Title:        title,
		Prompt:       prompt,
		Kind:         kind,
		InitialValue: initial,
		Value:        initial,
		Cursor:       len(initial),
		Focused:      true,
		OnSubmit:     onSubmit,
		OnCancel:     func() {},
	}
	lines := widgets.TextPromptLines(m.Theme, st)
	pop := widgets.NewPopupContent(lines)
	// TextPromptLines now self-renders the unified popup chrome.
	fw := wm.NewFloatingWindow("tree-prompt", "", pop,
		widgets.TextPromptPopupWidth(st),
		widgets.TextPromptPopupHeight(st),
		wm.WithDecoration(false),
		wm.WithMovable(false),
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-prompt")
		m.windowManager.AddWindow(fw)
	}
	m.TreePrompt = st
}

func (m *App) handleTreePromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.TreePrompt == nil || m.windowManager == nil {
		return nil
	}
	st := m.TreePrompt
	switch msg.Code {
	case tea.KeyEsc:
		if st.OnCancel != nil {
			st.OnCancel()
		}
		m.closeTreePrompt()
		return nil
	case tea.KeyEnter:
		if st.OnSubmit != nil {
			if err := st.OnSubmit(st.Value); err != nil {
				m.StatusBar.SetError(err.Error())
				return nil
			}
		}
		m.closeTreePrompt()
		return nil
	case tea.KeyBackspace:
		if st.Cursor > 0 && len(st.Value) > 0 {
			st.Value = st.Value[:st.Cursor-1] + st.Value[st.Cursor:]
			st.Cursor--
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyDelete:
		if st.Cursor < len(st.Value) {
			st.Value = st.Value[:st.Cursor] + st.Value[st.Cursor+1:]
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyLeft:
		if st.Cursor > 0 {
			st.Cursor--
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyRight:
		if st.Cursor < len(st.Value) {
			st.Cursor++
			m.refreshTreePrompt()
		}
		return nil
	case tea.KeyHome:
		st.Cursor = 0
		m.refreshTreePrompt()
		return nil
	case tea.KeyEnd:
		st.Cursor = len(st.Value)
		m.refreshTreePrompt()
		return nil
	}
	if msg.Text != "" {
		st.Value = st.Value[:st.Cursor] + msg.Text + st.Value[st.Cursor:]
		st.Cursor += len(msg.Text)
		m.refreshTreePrompt()
	}
	return nil
}

func (m *App) closeTreePrompt() {
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-prompt")
	}
	m.TreePrompt = nil
}

func (m *App) refreshTreePrompt() {
	if m.windowManager == nil || m.TreePrompt == nil {
		return
	}
	fw := m.windowManager.Window("tree-prompt")
	if fw == nil {
		return
	}
	lines := widgets.TextPromptLines(m.Theme, m.TreePrompt)
	pop := widgets.NewPopupContent(lines)
	fw.SetContent(pop)
	fw.MarkContentDirty()
}

// handleTreeDeleteConfirmKey is the keyboard handler for the
// destructive confirm popup (delete file/folder). ←/↑ = Yes,
// →/↓ = No, Enter = activate focused, Esc = cancel.
func (m *App) handleTreeDeleteConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window("tree-delete-confirm")
	if fw == nil {
		return nil
	}
	path := m.TreeDeletePending
	switch msg.Code {
	case tea.KeyEsc:
		m.cancelTreeDelete()
		return nil
	case tea.KeyEnter:
		m.confirmTreeDelete()
		return nil
	case tea.KeyLeft, tea.KeyUp:
		m.StatusBar.SetInfo("press enter to confirm delete of " + filepath.Base(path))
		return nil
	case tea.KeyRight, tea.KeyDown:
		m.cancelTreeDelete()
		return nil
	}
	return nil
}

func (m *App) confirmTreeDelete() {
	path := m.TreeDeletePending
	m.TreeDeletePending = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
	}
	if path == "" {
		return
	}
	isDir := m.Tree.Model.IsDir(path)
	var err error
	if isDir {
		err = os.RemoveAll(path)
	} else {
		err = os.Remove(path)
	}
	if err != nil {
		m.StatusBar.SetError("delete " + filepath.Base(path) + ": " + err.Error())
		return
	}
	m.Tree.Model.ProcessWatcherEvent(filetree.FileChangeEvent{
		Type: filetree.FileDeleted, Path: path,
	})
	// If the deleted file was open in an editor, close the tab.
	if tab, ok := m.EditorState.Tabs[path]; ok {
		m.closeEditorTabForce(tab.Path)
	}
	m.StatusBar.SetInfo("Deleted " + filepath.Base(path))
}

func (m *App) cancelTreeDelete() {
	m.TreeDeletePending = ""
	if m.windowManager != nil {
		m.windowManager.RemoveWindow("tree-delete-confirm")
	}
}
