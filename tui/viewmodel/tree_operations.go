package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
	"crude/tui/ui"
	"crude/tui/wm"
)

// pathUnderMouse resolves a file tree path from a screen Y.
func (m *App) pathUnderMouse(mouse tea.Mouse) string {
	if m.Tree == nil || m.Tree.Model == nil || m.sidebarScreen == nil || m.rootHost == nil {
		return ""
	}
	rect, ok := m.rootHost.ScreenRect(m.sidebarScreen.ID())
	if !ok || !rect.Contains(mouse.X, mouse.Y) {
		return ""
	}
	return m.sidebarScreen.PathAtLocalY(mouse.Y - rect.Y)
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
	m.openTreeTextPrompt(ui.TextPromptNewFile, "New file",
		"in "+dir, dir, "", func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			full := filepath.Join(dir, value)
			msg := m.CreateFile(full)
			if !msg.Succeeded() {
				return msg.Err
			}
			m.Tree.Model.ApplyLocalMutation(filetree.FileChangeEvent{
				Type: filetree.FileCreated, Path: full,
			})
			m.Feedback.NotifyInfo("Created " + value)
			// Open the new file in the editor so the user can
			// start typing immediately.
			if _, err := m.openEditorTab(full); err != nil {
				m.Feedback.NotifyError("open " + value + ": " + err.Error())
			}
			return nil
		})
}

// startNewFolderInTree starts a small prompt for naming a new
// folder inside the given directory. On Enter, the folder is
// created and the tree is updated.
func (m *App) startNewFolderInTree(dir string) {
	m.openTreeTextPrompt(ui.TextPromptNewFolder, "New folder",
		"in "+dir, dir, "", func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			full := filepath.Join(dir, value)
			msg := m.CreateFolder(full)
			if !msg.Succeeded() {
				return msg.Err
			}
			m.Tree.Model.ApplyLocalMutation(filetree.FileChangeEvent{
				Type: filetree.FileCreated, Path: full,
			})
			// Expand the parent so the new folder is visible.
			m.Tree.Model.ExpandToPath(full)
			m.Feedback.NotifyInfo("Created folder " + value)
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
	m.openTreeTextPrompt(ui.TextPromptRename, "Rename",
		path, "", base, func(value string) error {
			if value == "" {
				return fmt.Errorf("name cannot be empty")
			}
			if value == base {
				return fmt.Errorf("name unchanged")
			}
			dir := filepath.Dir(path)
			newPath := filepath.Join(dir, value)
			msg := m.RenamePath(path, newPath)
			if !msg.Succeeded() {
				return msg.Err
			}
			m.Tree.Model.ApplyLocalMutation(filetree.FileChangeEvent{
				Type: filetree.FileRenamed, OldPath: path, Path: newPath,
			})
			type editorRetarget struct {
				oldPath, newPath string
				tab              *EditorTab
			}
			var retargets []editorRetarget
			for tabPath, tab := range m.EditorState.Tabs {
				target := ""
				if tabPath == path {
					target = newPath
				}
				if msg.IsDir {
					rel, err := filepath.Rel(path, tabPath)
					if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
						target = filepath.Join(newPath, rel)
					}
				}
				if target != "" {
					retargets = append(retargets, editorRetarget{tabPath, target, tab})
				}
			}
			for _, item := range retargets {
				if m.Documents != nil {
					m.Documents.Cancel(item.tab.Editor.BufferID())
				}
				if m.BufferMgr != nil {
					if err := m.BufferMgr.Retarget(item.oldPath, item.newPath); err != nil {
						return err
					}
				}
				if err := item.tab.Editor.Retarget(item.newPath); err != nil {
					return err
				}
				delete(m.EditorState.Tabs, item.oldPath)
				item.tab.Path = item.newPath
				m.EditorState.Tabs[item.newPath] = item.tab
				if m.EditorState.ActiveView == item.oldPath {
					m.EditorState.ActiveView = item.newPath
				}
				m.retitleEditorWindowTab(item.oldPath, item.newPath, item.tab.Editor)
				if m.LSPMgr != nil {
					lsp, oldFile, newFile, text := m.LSPMgr, item.oldPath, item.newPath, item.tab.Editor.Value()
					m.startSourceControlMutation(func() tea.Msg {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						lsp.CloseFile(ctx, oldFile)
						lsp.OpenFile(ctx, newFile, text)
						return nil
					})
				}
			}
			if len(retargets) > 0 {
				m.markEditorGitStaleNow()
			}
			m.Feedback.NotifyInfo(fmt.Sprintf("Renamed to %s", value))
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
	for tabPath, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil || !tab.Editor.Modified() {
			continue
		}
		rel, err := filepath.Rel(path, tabPath)
		if tabPath == path || isDir && err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			detail += "\nUnsaved editor changes will be discarded."
			break
		}
	}
	// Default focus No (safe) so accidental Enter does not delete.
	m.GitDiscardPending = nil
	m.TreeDeleteChoice = 1
	frame := ui.BuildTreeDeleteFrame(m.Theme, question, detail, m.TreeDeleteChoice)
	w, h := frame.Measure(80, 40)
	fw := wm.NewFloatingWindow(string(ui.OverlayTreeDelete), "", frame,
		w, h,
		wm.WithDecoration(false),
		wm.WithMovable(true), // drag by frame title row
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayTreeDelete, fw, ui.ModalDialogPolicy(), func() {
		m.TreeDeletePending = ""
	})
	m.TreeDeletePending = path
}

// openTreeTextPrompt opens the focused text-input popup used by
// new-file / new-folder / rename. Centralizes the popup creation
// so the three callers stay small. The submit callback returns a
// non-nil error to surface validation issues; the popup stays open
// in that case so the user can fix the name.
func (m *App) openTreeTextPrompt(kind ui.TextPromptKind, title, prompt, dir, initial string, onSubmit func(value string) error) {
	if onSubmit == nil {
		return
	}
	st := ui.NewTextPromptState(kind, title, prompt, initial)
	st.OnSubmit = onSubmit
	st.OnCancel = func() {}
	// First product surface on unified WindowFrame chrome.
	frame := ui.BuildTextPromptFrame(m.Theme, st)
	fw := wm.NewFloatingWindow(string(ui.OverlayTreePrompt), "", frame,
		ui.TextPromptPopupWidth(st),
		ui.TextPromptPopupHeight(st),
		wm.WithDecoration(false),
		wm.WithMovable(true), // drag by frame title row
		wm.WithResizable(false),
		wm.WithClosable(false),
		wm.WithModal(true),
		wm.WithFocusable(true),
		wm.WithClickOutside(wm.ClickOutsideIgnore),
	)
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(ui.OverlayTreePrompt, fw, ui.ModalDialogPolicy(), func() {
		m.TreePrompt = nil
	})
	m.TreePrompt = st
}

func (m *App) handleTreePromptKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.TreePrompt == nil || m.windowManager == nil {
		return nil
	}
	st := m.TreePrompt
	if st.Edit == nil {
		// Legacy safety: construct engine from Value/Cursor fields.
		st.Edit = ui.NewTextEdit()
		st.Edit.SetValue(st.Value)
		st.Edit.SetCursor(st.Cursor, false)
	}
	ed := st.Edit

	switch msg.Code {
	case tea.KeyEsc:
		if st.OnCancel != nil {
			st.OnCancel()
		}
		m.closeTreePrompt()
		return nil
	case tea.KeyEnter:
		st.SyncFromEdit()
		if st.OnSubmit != nil {
			if err := st.OnSubmit(st.ValueText()); err != nil {
				m.Feedback.NotifyError(err.Error())
				return nil
			}
		}
		m.closeTreePrompt()
		return nil
	default:
		if !ui.HandleTextEditKey(ed, msg) {
			return nil
		}
	}
	st.SyncFromEdit()
	m.refreshTreePrompt()
	return nil
}

func (m *App) closeTreePrompt() {
	m.overlayHost.Close(ui.OverlayTreePrompt)
}

func (m *App) refreshTreePrompt() {
	if m.windowManager == nil || m.TreePrompt == nil {
		return
	}
	fw := m.windowManager.Window("tree-prompt")
	if fw == nil {
		return
	}
	// Mutate the retained TextField in the existing frame instead of
	// rebuilding the WindowFrame on each keystroke. The frame is built
	// once on open; keystrokes push st.Edit into the framed field.
	if m.TreePrompt.Edit != nil {
		if cw, ok := fw.Content.(*ui.ComponentWidget); ok && cw != nil {
			ui.ApplyTextPromptEdit(cw, m.TreePrompt)
			fw.MarkContentDirty()
			return
		}
	}
	// Fallback: rebuild frame from current edit state.
	frame := ui.BuildTextPromptFrame(m.Theme, m.TreePrompt)
	fw.SetContent(frame)
	fw.MarkContentDirty()
}

// handleTreeDeleteConfirmKey is the keyboard handler for the
// destructive confirm popup (delete file/folder). Arrows/Tab move focus
// in place via the shared confirm helpers (no tree rebuild); Enter
// activates the focused button; y/n are direct shortcuts; Esc cancels.
// Default focus is No.
func (m *App) handleTreeDeleteConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.windowManager == nil {
		return nil
	}
	fw := m.windowManager.Window("tree-delete-confirm")
	if fw == nil {
		return nil
	}
	cw, ok := fw.Content.(*ui.ComponentWidget)
	if !ok {
		return nil
	}
	switch msg.Code {
	case tea.KeyEsc:
		m.cancelTreeDelete()
		return nil
	case tea.KeyEnter:
		if m.TreeDeleteChoice == 0 {
			m.confirmTreeDelete()
		} else {
			m.cancelTreeDelete()
		}
		return nil
	case tea.KeyLeft, tea.KeyUp:
		if n := ui.MoveConfirmFocus(cw, -1, 2); n >= 0 {
			m.TreeDeleteChoice = n
		}
		return nil
	case tea.KeyRight, tea.KeyDown, tea.KeyTab:
		delta := 1
		if msg.Code == tea.KeyTab && msg.Mod.Contains(tea.ModShift) {
			delta = -1
		}
		if n := ui.MoveConfirmFocus(cw, delta, 2); n >= 0 {
			m.TreeDeleteChoice = n
		}
		return nil
	}
	switch msg.String() {
	case "y", "Y":
		m.TreeDeleteChoice = 0
		m.confirmTreeDelete()
		return nil
	case "n", "N":
		m.cancelTreeDelete()
		return nil
	}
	return nil
}

func (m *App) confirmTreeDelete() {
	if m.GitDiscardPending != nil {
		change := *m.GitDiscardPending
		m.overlayHost.Close(ui.OverlayTreeDelete)
		m.startSourceControlMutation(m.discardGitChange(change))
		return
	}
	path := m.TreeDeletePending
	m.overlayHost.Close(ui.OverlayTreeDelete)
	if path == "" {
		return
	}
	msg := m.DeletePath(path)
	if !msg.Succeeded() {
		m.Feedback.NotifyError("delete " + filepath.Base(path) + ": " + msg.Err.Error())
		return
	}
	m.Tree.Model.ApplyLocalMutation(filetree.FileChangeEvent{
		Type: filetree.FileDeleted, Path: path,
	})
	// If the deleted file was open in an editor, close the tab.
	if tab, ok := m.EditorState.Tabs[path]; ok {
		m.closeEditorTabForce(tab.Path)
	}
	m.Feedback.NotifyInfo("Deleted " + filepath.Base(path))
}

func (m *App) cancelTreeDelete() {
	m.overlayHost.Close(ui.OverlayTreeDelete)
}
