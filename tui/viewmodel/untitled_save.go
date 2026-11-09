package viewmodel

import (
	"context"
	"crude/textencoding"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/screens/filepicker"
	"crude/tui/ui"
	"crude/workspace"
)

type untitledSaveOwner struct {
	editor     *editor.Editor
	workspace  shellport.Workspace
	generation uint64
	closeAfter bool
	picker     *filepicker.Screen
	directory  string
	name       string
	encoding   textencoding.Encoding
	review     *untitledOverwriteState
}

func (m *App) openUntitledSave(ed *editor.Editor, closeAfter bool) {
	if ed == nil || !ed.IsOpen() || ed.FilePath() != "" || m.untitledSave != nil {
		return
	}
	if _, ok := m.ws.(shellport.UntitledDocumentSaver); !ok || m.liveEditors[ed.Primary()] == nil {
		m.Feedback.NotifyError("Cannot save this buffer in this workspace.")
		return
	}
	if state := m.liveEditors[ed.Primary()]; state.err != nil || state.uncertain != nil {
		m.Feedback.NotifyError("Resolve the shared draft before saving.")
		return
	}
	m.startUntitledSave(&untitledSaveOwner{editor: ed.Primary(), workspace: m.ws,
		generation: m.workspaceSwitchGen.Load(), closeAfter: closeAfter, directory: m.ws.RootPath(), encoding: ed.DocumentState().Encoding})
}

func (m *App) startUntitledSave(owner *untitledSaveOwner) {
	m.openPicker(owner.directory, filepicker.ModeSaveFile)
	owner.picker = m.FilePicker
	m.FilePicker.SetSaveOptions(owner.name, owner.encoding)
	m.untitledSave = owner
}

func (m *App) submitUntitledSave(msg filepicker.SelectMsg) {
	owner := m.untitledSave
	if owner == nil || owner.workspace != m.ws || owner.generation != m.workspaceSwitchGen.Load() ||
		owner.picker != msg.Target || !msg.ValidFor(owner.picker) || !owner.editor.IsOpen() || owner.editor.FilePath() != "" {
		return
	}
	state := m.liveEditors[owner.editor]
	if state == nil || state.uncertain != nil || state.err != nil {
		m.failUntitledSave(owner.editor, "Resolve the shared draft before saving.")
		return
	}
	state.save, state.reason, state.closeAfter = true, workspace.DocumentSaveManual, false
	state.format = false
	state.saveDirectory, state.saveName, state.saveEncoding = msg.Path, msg.Name, msg.Encoding
	owner.directory, owner.name, owner.encoding = msg.Path, msg.Name, msg.Encoding
	if intent := m.workspaceSwitchIntent; intent != nil && intent.prompt == owner.editor {
		intent.saving = true
	}
	owner.picker.SetSavePending(true, "Saving…")
	m.markFilePickerDirty()
}

type untitledOverwritePrepared struct {
	owner         *untitledSaveOwner
	localRevision uint64
	revision      uint64
	preview       workspace.ReviewedEditPreview
	port          shellport.ReviewedEdits
	err           error
}

// prepareUntitledOverwrite runs only after the create-only writer positively
// refused the destination. The retained review owns the exact disk preimage.
func (m *App) prepareUntitledOverwrite(ed *editor.Editor, revision uint64) bool {
	owner := m.untitledSave
	reviewer, ok := m.ws.(shellport.UntitledOverwriteReviewer)
	port, reviewed := m.ws.(shellport.ReviewedEdits)
	if owner == nil || owner.editor != ed.Primary() || owner.workspace != m.ws ||
		owner.generation != m.workspaceSwitchGen.Load() || owner.directory == "" ||
		!ok || !reviewed || owner.review != nil {
		return false
	}
	state := m.liveEditors[owner.editor]
	ds := owner.editor.DocumentState()
	if state == nil || state.sync || !state.topicReady || ds.Revision != revision || ds.PendingEdits != 0 {
		return false
	}
	msg := untitledOverwritePrepared{
		owner: owner, localRevision: owner.editor.Revision(), revision: revision, port: port,
	}
	id, directory, name, encoding := ds.DocumentID, owner.directory, owner.name, owner.encoding
	owner.picker.SetSavePending(true, "Checking the existing file…")
	m.markFilePickerDirty()
	m.editorCommands = append(m.editorCommands, m.clientIOCommand(context.Background(), func(parent context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 30*time.Second)
		defer cancel()
		msg.preview, msg.err = reviewer.PrepareUntitledOverwrite(ctx, id, revision, directory, name, encoding)
		return msg
	}))
	return true
}

func (m *App) applyUntitledOverwritePrepared(msg untitledOverwritePrepared) {
	owner := msg.owner
	valid := owner != nil && m.untitledSave == owner && owner.workspace == m.ws &&
		owner.generation == m.workspaceSwitchGen.Load() && owner.editor.IsOpen() &&
		owner.editor.FilePath() == "" && owner.editor.Revision() == msg.localRevision &&
		owner.editor.DocumentState().Revision == msg.revision &&
		owner.editor.DocumentState().PendingEdits == 0 && owner.review == nil
	if !valid || msg.err != nil || len(msg.preview.Files) != 1 {
		if msg.preview.ID != "" && msg.port != nil {
			m.runDetachedAsyncCmd(releaseReviewedEditPreview(reviewedEditDialogRequest{port: msg.port, preview: msg.preview}))
		}
		if owner != nil && m.untitledSave == owner {
			m.failUntitledSave(owner.editor, "Cannot replace this file safely. Choose a new name.")
		}
		return
	}
	owner.review = &untitledOverwriteState{owner: owner, preview: msg.preview, port: msg.port, revision: msg.localRevision}
	m.showUntitledOverwrite()
}

func (m *App) pendingUntitledSave(ed *editor.Editor) {
	if owner := m.untitledSave; owner != nil && owner.editor == ed.Primary() && owner.workspace == m.ws {
		owner.picker.SetSavePending(true, "Checking the save result…")
		m.markFilePickerDirty()
	}
}

func (m *App) uncertainUntitledSave(ed *editor.Editor) {
	if owner := m.untitledSave; owner != nil && owner.editor == ed.Primary() && owner.workspace == m.ws {
		owner.picker.SetSaveUncertain("Save result is unknown. Check the destination before trying again.")
		m.markFilePickerDirty()
	}
}

func (m *App) failUntitledSave(ed *editor.Editor, message string) {
	if state := m.liveEditors[ed.Primary()]; state != nil {
		state.closeAfter = false
	}
	if intent := m.workspaceSwitchIntent; intent != nil && intent.prompt == ed.Primary() {
		intent.saving = false
	}
	if owner := m.untitledSave; owner != nil && owner.editor == ed.Primary() && owner.workspace == m.ws {
		owner.picker.SetSavePending(false, message)
		m.markFilePickerDirty()
	}
}

func (m *App) finishUntitledSave(ed *editor.Editor) {
	state := m.liveEditors[ed.Primary()]
	if state != nil {
		state.firstSavePath = ""
	}
	if owner := m.untitledSave; owner != nil && owner.editor == ed.Primary() && owner.workspace == m.ws {
		// A switch waiting for Save must retain its accepted decision.
		m.untitledSave = nil
		m.overlayHost.Close(ui.OverlayFilePicker)
		if owner.closeAfter && state != nil {
			state.closeAfter = true
			if ed.Modified() {
				state.save = true
			} else {
				m.overlayHost.Close(ui.OverlayDirtyClose)
				m.closeEditorTabForce(editorDocumentKey(ed))
				m.continuePendingTabClose()
			}
		}
	}
}
