package viewmodel

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/filetree"
	"crude/tui/screens/shelltree"
	"crude/tui/ui"
	"crude/workspace"
)

// File-tree file actions: drag and drop, cut/copy/paste, duplicate and undo
// of the last move. Every pointer, keyboard and menu entry ends here, and
// every mutation goes through the workspace file owner (RenamePath,
// CopyPath), which refuses an existing target and leaves nothing behind on
// failure.

// treeClipboard is a cut or copied tree entry, owned by one workspace.
type treeClipboard struct {
	owner shellport.Workspace
	path  string
	cut   bool
}

// treeMove is the last move, kept so Ctrl+Z in the tree can put it back.
type treeMove struct {
	owner    shellport.Workspace
	from, to string
}

// treeMoveRequest is a dropped move awaiting confirmation.
type treeMoveRequest struct {
	owner       shellport.Workspace
	generation  uint64
	source, dir string
}

type treeCopyResultMsg struct {
	owner      shellport.Workspace
	generation uint64
	result     workspace.FileOperationResultMsg
	verb       string
}

const overlayTreeMove ui.OverlayID = "tree-move-confirm"

func (m *App) handleTreeAction(a *ui.Action) (tea.Cmd, bool) {
	if a == nil || !strings.HasPrefix(a.ID, "tree.") {
		return nil, false
	}
	if a.ID == shelltree.ActionDrag || a.ID == shelltree.ActionDrop {
		drop, ok := a.Payload.(shelltree.TreeDrop)
		switch {
		case !ok:
		case a.ID == shelltree.ActionDrag:
			m.trackTreeDrag(drop)
		default:
			return m.dropTreeEntry(drop), true
		}
		return nil, true
	}
	path, _ := a.Payload.(string)
	switch a.ID {
	case shelltree.ActionRename:
		m.startRenameInTree(path)
	case shelltree.ActionDelete:
		m.startDeleteInTree(path)
	case shelltree.ActionCut, shelltree.ActionCopy:
		m.setTreeClipboard(path, a.ID == shelltree.ActionCut)
	case shelltree.ActionPaste:
		return m.pasteTreeEntry(m.treeTargetDir(path)), true
	case shelltree.ActionDuplicate:
		return m.copyTreeEntry(path, filepath.Dir(path), "Duplicated"), true
	case shelltree.ActionUndo:
		m.undoTreeMove()
	default:
		return nil, false
	}
	return nil, true
}

// treeTargetDir is the folder that receives a paste at path: the folder
// itself, the folder of a file, or the workspace root.
func (m *App) treeTargetDir(path string) string {
	switch {
	case path == "":
		return m.ws.RootPath()
	case m.Tree.Model.IsDir(path):
		return path
	default:
		return filepath.Dir(path)
	}
}

func (m *App) treeRel(path string) string {
	rel, err := filepath.Rel(m.ws.RootPath(), path)
	if err != nil || rel == "." {
		return filepath.Base(m.ws.RootPath())
	}
	return filepath.ToSlash(rel)
}

func (m *App) dropTreeEntry(drop shelltree.TreeDrop) tea.Cmd {
	proposed := m.treeFileDrop(drop.Source)
	if m.mainPanes != nil {
		m.mainPanes.drop = nil
	}
	if !drop.InTree {
		// A file released over a pane opens exactly where the preview showed.
		if drop.IsDir || proposed == nil {
			return nil
		}
		confirmed := m.fileDropAt(drop.Source, drop.X, drop.Y)
		if confirmed == nil || confirmed.group != proposed.group || confirmed.edge != proposed.edge || confirmed.rect != proposed.rect {
			return nil
		}
		return m.openFileDrop(confirmed)
	}
	if drop.TargetDir == "" {
		return nil
	}
	m.confirmTreeMove(drop.Source, drop.TargetDir)
	return nil
}

// confirmTreeMove asks before a pointer move: a drop is easy to make by
// accident, a paste is not.
func (m *App) confirmTreeMove(source, dir string) {
	m.treeMovePending = &treeMoveRequest{owner: m.ws, generation: m.workspaceSwitchGen.Load(), source: source, dir: dir}
	name := filepath.Base(source)
	frame := ui.BuildConfirmFrame(m.Theme, ui.ConfirmFrameSpec{
		Title:    "Move",
		Question: fmt.Sprintf("Move %s to %s?", name, m.treeRel(dir)),
		Detail:   "From " + m.treeRel(source) + "\nTo " + m.treeRel(filepath.Join(dir, name)) + "\nPress Ctrl+Z in the file tree to move it back.",
		Buttons:  []ui.ConfirmButtonSpec{{Label: "Move", ActionID: ui.ConfirmActionYes}, {Label: "Cancel", ActionID: ui.ConfirmActionNo}},
		Width:    64, FocusedIndex: 0,
	})
	m.openConfirmModal(overlayTreeMove, frame, m.activateTreeMove, func() { m.treeMovePending = nil })
}

// activateTreeMove serves both the mouse and Enter. Only the exact pending
// request of this workspace generation moves anything.
func (m *App) activateTreeMove(choice int) tea.Cmd {
	request := m.treeMovePending
	m.overlayHost.Close(overlayTreeMove)
	if choice == 0 && request != nil && request.owner == m.ws && request.generation == m.workspaceSwitchGen.Load() {
		m.moveTreeEntry(request.source, request.dir)
	}
	return nil
}

func (m *App) treeMoveConfirmKey(msg tea.KeyPressMsg) tea.Cmd {
	_, cmd := m.confirmModalInput(overlayTreeMove, msg, false, m.activateTreeMove)
	return cmd
}

func (m *App) moveTreeEntry(source, dir string) bool {
	target := filepath.Join(dir, filepath.Base(source))
	if err := m.relocateTreePath(source, target); err != nil {
		m.Feedback.NotifyError("Cannot move " + filepath.Base(source) + ": " + err.Error())
		return false
	}
	m.treeLastMove = &treeMove{owner: m.ws, from: target, to: source}
	m.Tree.Model.Select(target)
	m.Feedback.NotifyInfo("Moved " + filepath.Base(source) + " to " + m.treeRel(dir) + ". Press Ctrl+Z in the file tree to undo.")
	return true
}

func (m *App) undoTreeMove() {
	move := m.treeLastMove
	if move == nil || move.owner != m.ws {
		m.Feedback.NotifyInfo("There is no move to undo.")
		return
	}
	m.treeLastMove = nil
	if err := m.relocateTreePath(move.from, move.to); err != nil {
		m.Feedback.NotifyError("Cannot move " + filepath.Base(move.from) + " back: " + err.Error())
		return
	}
	m.Tree.Model.Select(move.to)
	m.Feedback.NotifyInfo("Moved " + filepath.Base(move.to) + " back to " + m.treeRel(filepath.Dir(move.to)) + ".")
}

func (m *App) setTreeClipboard(path string, cut bool) {
	if path == "" {
		return
	}
	m.treeClip = &treeClipboard{owner: m.ws, path: path, cut: cut}
	verb := "Copied"
	if cut {
		verb = "Cut"
	}
	m.Feedback.NotifyInfo(verb + " " + filepath.Base(path) + ". Select a folder and press Ctrl+V to paste.")
}

func (m *App) treeClipboardReady() bool {
	return m.treeClip != nil && m.treeClip.owner == m.ws
}

func (m *App) pasteTreeEntry(dir string) tea.Cmd {
	if !m.treeClipboardReady() {
		m.Feedback.NotifyInfo("Nothing to paste. Cut or copy a file first.")
		return nil
	}
	clip := *m.treeClip
	if clip.cut {
		if filepath.Dir(clip.path) == dir {
			return nil
		}
		if !shelltree.ValidMoveTarget(clip.path, dir) {
			m.Feedback.NotifyError("Cannot move " + filepath.Base(clip.path) + " into itself.")
			return nil
		}
		if m.moveTreeEntry(clip.path, dir) {
			m.treeClip = nil
		}
		return nil
	}
	return m.copyTreeEntry(clip.path, dir, "Pasted")
}

// copyTreeEntry copies off the update goroutine: a folder copy can be large.
func (m *App) copyTreeEntry(source, dir, verb string) tea.Cmd {
	if source == "" {
		return nil
	}
	target := m.freeTreeName(dir, filepath.Base(source))
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		return treeCopyResultMsg{owner: owner, generation: generation, result: owner.CopyPath(source, target), verb: verb}
	}
}

func (m *App) applyTreeCopyResult(msg treeCopyResultMsg) tea.Cmd {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	name := filepath.Base(msg.result.OldPath)
	if !msg.result.Succeeded() {
		m.Feedback.NotifyError("Cannot copy " + name + ": " + msg.result.Err.Error())
		return nil
	}
	m.Tree.Model.ApplyLocalMutation(filetree.FileChangeEvent{Type: filetree.FileCreated, Path: msg.result.NewPath})
	m.Tree.Model.Select(msg.result.NewPath)
	m.Feedback.NotifyInfo(msg.verb + " " + filepath.Base(msg.result.NewPath) + " in " + m.treeRel(filepath.Dir(msg.result.NewPath)) + ".")
	return nil
}

// freeTreeName picks the first unused "name copy", "name copy 2" … in dir,
// from the tree's own inventory. The workspace refuses a name that is taken
// on disk anyway, so this never overwrites.
func (m *App) freeTreeName(dir, name string) string {
	candidate := filepath.Join(dir, name)
	if m.Tree.Model.NodeByPath(candidate) == nil {
		return candidate
	}
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	if strings.HasPrefix(name, ".") && stem == "" {
		stem, ext = name, ""
	}
	for index := 1; ; index++ {
		suffix := " copy"
		if index > 1 {
			suffix = fmt.Sprintf(" copy %d", index)
		}
		candidate = filepath.Join(dir, stem+suffix+ext)
		if m.Tree.Model.NodeByPath(candidate) == nil {
			return candidate
		}
	}
}
