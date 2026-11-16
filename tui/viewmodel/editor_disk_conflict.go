package viewmodel

import (
	"crypto/sha256"
	"errors"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	"crude/shellport"
	"crude/textencoding"
	"crude/tui/editor"
	"crude/tui/ui"
	"crude/workspace"
)

const diskConflictOverlay ui.OverlayID = "disk-conflict"

// diskConflictPrompt is the one decision for a file that changed on disk while
// it had unsaved changes. Save, close-with-save and the save result all open
// it, so the choices never differ by entry point.
type diskConflictPrompt struct {
	editor     *editor.Editor
	owner      shellport.Workspace
	generation uint64
	closeAfter bool
}

// openDiskConflict asks Overwrite, Discard, Compare or Cancel. closeAfter
// closes the view once Overwrite or Discard succeeds.
func (m *App) openDiskConflict(ed *editor.Editor, closeAfter bool) {
	if ed == nil || !ed.IsOpen() || m.overlayHost == nil {
		return
	}
	ed = ed.Primary()
	if p := m.diskConflict; p != nil && p.editor == ed && m.overlayHost.Window(diskConflictOverlay) != nil {
		p.closeAfter = p.closeAfter || closeAfter
		return
	}
	m.overlayHost.Close(diskConflictOverlay)
	// The question replaces a waiting unsaved-changes prompt for this file. A
	// workspace switch waiting on it stops: the file needs a choice first.
	if intent := m.workspaceSwitchIntent; intent != nil && intent.prompt != nil && intent.prompt.Primary() == ed {
		m.cancelRemoteWorkspaceSwitch(false)
		m.Feedback.NotifyInfo("Workspace switch stopped. Choose which version of " + ed.BaseTitle() + " to keep.")
	}
	if m.DirtyPromptPath != "" {
		m.overlayHost.Close(ui.OverlayDirtyClose)
	}
	p := &diskConflictPrompt{editor: ed, owner: m.ws, generation: m.workspaceSwitchGen.Load(), closeAfter: closeAfter}
	m.diskConflict = p
	m.openConfirmModal(diskConflictOverlay, ui.BuildDiskConflictFrame(m.Theme, ed.BaseTitle(), ui.DiskConflictCancel),
		func(index int) tea.Cmd { return m.resolveDiskConflict(p, index) },
		func() {
			if m.diskConflict != p {
				return
			}
			// Esc or Cancel: nothing changes, and a waiting close stops.
			m.diskConflict = nil
			m.cancelEditorClose(p.editor)
		})
}

// resolveDiskConflict carries out the chosen button.
func (m *App) resolveDiskConflict(p *diskConflictPrompt, choice int) tea.Cmd {
	if choice < 0 || m.diskConflict != p {
		return nil
	}
	if choice == ui.DiskConflictCancel {
		m.overlayHost.Close(diskConflictOverlay)
		return nil
	}
	m.diskConflict = nil
	m.overlayHost.Close(diskConflictOverlay)
	ed := p.editor
	if p.owner != m.ws || p.generation != m.workspaceSwitchGen.Load() || !ed.IsOpen() {
		return nil
	}
	switch choice {
	case ui.DiskConflictOverwrite:
		if m.resolveLiveDisk(ed, &document.Resolution{}, 0, p.closeAfter) {
			return nil
		}
		return m.resolveLocalDisk(ed, true, p.closeAfter)
	case ui.DiskConflictDiscard:
		if m.discardLiveEditor(ed, p.closeAfter) {
			return nil
		}
		return m.resolveLocalDisk(ed, false, p.closeAfter)
	case ui.DiskConflictCompare:
		m.cancelEditorClose(ed)
		return m.compareEditorWithDisk(ed)
	}
	return nil
}

// discardEditorChanges returns the file to its saved text without closing it.
// The core change is undoable, so it needs no extra confirmation.
func (m *App) discardEditorChanges(ed *editor.Editor) tea.Cmd {
	if ed == nil || !ed.IsOpen() || ed.ReadOnly() || !ed.Modified() {
		return nil
	}
	if m.discardLiveEditor(ed, false) {
		m.Feedback.NotifyInfo("Changes discarded. Undo brings them back.")
		return nil
	}
	if ed.FilePath() == "" {
		return nil
	}
	return m.resolveLocalDisk(ed, false, false)
}

// liveEditorNeedsDecision reports edits that did not reach the shared
// document, which only the changes-not-applied dialog settles.
func (m *App) liveEditorNeedsDecision(ed *editor.Editor) bool {
	state := m.liveEditors[ed.Primary()]
	return state != nil && (state.err != nil || state.uncertain != nil || ed.DocumentState().Conflict != nil)
}

// cancelEditorClose stops a close that waits on a save decision.
func (m *App) cancelEditorClose(ed *editor.Editor) {
	if ed == nil {
		return
	}
	if state := m.liveEditors[ed.Primary()]; state != nil {
		state.closeAfter, state.closeView = false, false
	}
	if state := m.editorSaves[ed.BufferID()]; state != nil {
		state.closeAfter = false
	}
	m.abortPendingTabClose()
}

// discardLiveEditor returns a connected document to its saved file: the core
// reloads it as one undoable change, which also settles a disk conflict.
// closeView then closes the view. It reports false for a local editor.
func (m *App) discardLiveEditor(ed *editor.Editor, closeView bool) bool {
	state := m.liveEditors[ed.Primary()]
	if state == nil {
		return false
	}
	state.reload, state.resolve, state.resolveRevision = true, nil, 0
	state.save, state.format, state.closeAfter = false, false, false
	state.closeView, state.due = closeView, time.Time{}
	if state.uncertain == nil {
		state.err = nil
	}
	return true
}

// resolveLiveDisk makes the current file the save baseline with the resolved
// text, then saves. revision, when nonzero, is the editor revision the
// resolution was made against; a newer edit refuses it.
func (m *App) resolveLiveDisk(ed *editor.Editor, resolve *document.Resolution, revision uint64, closeAfter bool) bool {
	state := m.liveEditors[ed.Primary()]
	if state == nil {
		return false
	}
	state.reload, state.resolve, state.resolveRevision = true, resolve, revision
	state.save, state.reason = true, workspace.DocumentSaveManual
	state.format = m.editorSettings.FormatOnSave
	state.closeAfter, state.closeView, state.due = closeAfter, false, time.Time{}
	if state.uncertain == nil {
		state.err = nil
	}
	return true
}

type localDiskResolvedMsg struct {
	ed                   *editor.Editor
	generation, revision uint64
	keep, closeAfter     bool
	data                 []byte
	encoding             textencoding.Encoding
	hash                 [32]byte
	exists               bool
	err                  error
}

// resolveLocalDisk settles a disk conflict for an editor without a core
// document: keep saves the current text over the file; otherwise the file
// text replaces it.
func (m *App) resolveLocalDisk(ed *editor.Editor, keep, closeAfter bool) tea.Cmd {
	path := ed.FilePath()
	msg := localDiskResolvedMsg{ed: ed, generation: m.workspaceSwitchGen.Load(), revision: ed.Revision(), keep: keep, closeAfter: closeAfter}
	return func() tea.Msg {
		raw, err := os.ReadFile(path)
		msg.exists = err == nil
		if errors.Is(err, os.ErrNotExist) {
			err = nil
		}
		msg.hash = sha256.Sum256(raw)
		if err == nil {
			msg.data, msg.encoding, err = textencoding.DecodeText(raw)
		}
		msg.err = err
		return msg
	}
}

func (m *App) applyLocalDiskResolved(msg localDiskResolvedMsg) tea.Cmd {
	ed := msg.ed
	if msg.generation != m.workspaceSwitchGen.Load() || !ed.IsOpen() {
		return nil
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Could not read the file. " + msg.err.Error())
		return nil
	}
	if ed.Revision() != msg.revision {
		m.Feedback.NotifyInfo("Your text changed. Save again to choose a version.")
		return nil
	}
	delete(m.editorSaves, ed.BufferID())
	if msg.keep {
		ed.AcceptDiskBaseline(msg.data, msg.hash, msg.exists)
		return m.requestEditorSave(ed, workspace.DocumentSaveManual, msg.closeAfter)
	}
	if !msg.exists {
		m.Feedback.NotifyError("The file no longer exists. Save it with a new name or restore it from Local History.")
		return nil
	}
	ed.ApplyDisk(msg.data, msg.encoding, msg.hash)
	if msg.closeAfter {
		m.closeEditorTabForce(editorDocumentKey(ed))
		m.continuePendingTabClose()
	}
	return nil
}
