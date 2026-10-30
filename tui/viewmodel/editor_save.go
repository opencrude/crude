package viewmodel

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

type editorSaveState struct {
	inFlight   bool
	pending    bool
	closeAfter bool
	reason     workspace.DocumentSaveReason
	cancel     context.CancelFunc
	discard    bool
	err        error
}

type editorSaveResultMsg struct {
	result              workspace.DocumentSaveResult
	workspaceGeneration uint64
	state               *editorSaveState
}

type editorFormatResultMsg struct {
	ed         *editor.Editor
	revision   uint64
	state      *editorSaveState
	generation uint64
	data       []byte
	err        error
}

func (m *App) handleEditorFormatResult(msg editorFormatResultMsg) tea.Cmd {
	ed := msg.ed
	if msg.generation != m.workspaceSwitchGen.Load() || !ed.IsOpen() || m.editorSaves[ed.BufferID()] != msg.state {
		return nil
	}
	state := msg.state
	state.inFlight = false
	if state.discard {
		delete(m.editorSaves, ed.BufferID())
		m.closeEditorTabForce(ed.FilePath())
		return nil
	}
	if msg.err == nil && ed.Revision() == msg.revision {
		ed.ApplyFormatted(string(msg.data))
	} else if m.Feedback != nil {
		m.Feedback.NotifyError("Formatting skipped; saving current text without formatting")
	}
	return m.requestEditorSaveWithFormat(ed, workspace.DocumentSaveManual, state.closeAfter, false)
}

type editorDiskResultMsg struct {
	ed             *editor.Editor
	revision       uint64
	generation     uint64
	diskGeneration uint64
	data           []byte
	hash           [32]byte
	err            error
	missing        bool
}

func (m *App) refreshEditorDiskCmd(ed *editor.Editor) tea.Cmd {
	path, revision, generation, diskGeneration := ed.FilePath(), ed.Revision(), m.workspaceSwitchGen.Load(), ed.DiskGeneration()
	return func() tea.Msg {
		data, err := os.ReadFile(path)
		missing := errors.Is(err, os.ErrNotExist)
		if missing {
			err = nil
		}
		return editorDiskResultMsg{ed: ed, revision: revision, generation: generation, diskGeneration: diskGeneration, data: data, hash: sha256.Sum256(data), err: err, missing: missing}
	}
}

func (m *App) applyEditorDisk(msg editorDiskResultMsg) {
	ed := msg.ed
	if msg.generation != m.workspaceSwitchGen.Load() || !ed.IsOpen() {
		return
	}
	if state := m.editorSaves[ed.BufferID()]; state != nil && state.inFlight {
		ed.SignalDiskChanged()
		return
	}
	if ed.Revision() != msg.revision || ed.DiskGeneration() != msg.diskGeneration {
		ed.SignalDiskChanged()
		return
	}
	if msg.missing {
		ed.NoteDiskMissing()
		m.markEditorDiskConflict(ed, "File was deleted on disk; local text retained. Save manually to recreate it.")
		return
	}
	if msg.err != nil {
		m.markEditorDiskConflict(ed, "Cannot read changed file: "+msg.err.Error())
		return
	}
	if ed.MatchesDisk(msg.hash) {
		return
	}
	if !ed.Modified() {
		ed.ApplyDisk(msg.data)
	} else {
		m.markEditorDiskConflict(ed, "File changed on disk; local edits retained. Compare before saving.")
	}
}

func (m *App) markEditorDiskConflict(ed *editor.Editor, message string) {
	if m.editorSaves == nil {
		m.editorSaves = make(map[string]*editorSaveState)
	}
	state := m.editorSaves[ed.BufferID()]
	if state != nil && state.err != nil {
		return
	}
	m.editorSaves[ed.BufferID()] = &editorSaveState{err: workspace.ErrDocumentConflict}
	if m.Feedback != nil {
		m.Feedback.NotifyError(message)
	}
}

func (m *App) onEditorContentChanged(ed *editor.Editor, filePath, text string) {
	m.projectTreeDiagnostic(filePath, m.lspFacts[filePath])
	if m.FindReplace != nil && m.EditorState != nil && m.EditorState.Editor == ed {
		state := ed.Search()
		m.FindReplace.MatchCount, m.FindReplace.CurrentIdx, m.FindReplace.Error = len(state.Matches), state.CurrentIdx, state.Error
		m.refreshFindReplacePopup()
	}
	if state := m.liveEditors[ed]; state != nil {
		state.due = time.Now().Add(time.Duration(m.editorSettings.AutoSaveDelay) * time.Millisecond)
		m.markEditorGitDirty(time.Now())
		return
	}
	if m.editorChanges != nil && m.editorChanges.source == ed {
		m.overlayHost.Close(editorChangesOverlay)
	}
	if m.ws.LSPMgr() != nil {
		if m.ws.LSPMgr().Settings().AutoComplete && m.LSPPopup != nil {
			m.LSPPopup.CompletionDue = time.Now().Add(350 * time.Millisecond)
		}
	}
	m.markEditorGitDirty(time.Now())
	if m.ws == nil || m.ws.Documents() == nil || ed == nil {
		return
	}
	if state := m.editorSaves[ed.BufferID()]; state != nil && (state.err != nil || state.discard || state.closeAfter) {
		return
	}
	if !ed.Modified() {
		m.ws.Documents().Cancel(ed.BufferID())
		return
	}
	if m.editorSettings.AutoSave == types.AutoSaveAfterDelay && ed.Modified() && m.DirtyPromptPath != filePath {
		delay := time.Duration(m.editorSettings.AutoSaveDelay) * time.Millisecond
		m.ws.Documents().Schedule(ed.BufferID(), filePath, ed.Revision(), delay)
	}
}

func (m *App) requestActiveEditorSave(reason workspace.DocumentSaveReason) tea.Cmd {
	if m.EditorState == nil || m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		if m.Feedback != nil && reason == workspace.DocumentSaveManual {
			m.Feedback.NotifyInfo("No file open to save")
		}
		return nil
	}
	return m.requestEditorSave(m.EditorState.Editor, reason, false)
}

func (m *App) requestEditorSave(ed *editor.Editor, reason workspace.DocumentSaveReason, closeAfter bool) tea.Cmd {
	return m.requestEditorSaveWithFormat(ed, reason, closeAfter, true)
}

func (m *App) requestEditorSaveWithFormat(ed *editor.Editor, reason workspace.DocumentSaveReason, closeAfter, format bool) tea.Cmd {
	if state := m.liveEditors[ed]; state != nil {
		state.save, state.reason, state.closeAfter = true, reason, closeAfter
		state.format = format && reason == workspace.DocumentSaveManual && m.editorSettings.FormatOnSave
		if state.uncertain == nil {
			state.err = nil
		}
		return nil
	}
	if m.ws == nil || ed == nil || ed.ReadOnly() {
		return nil
	}
	m.ws.EnsureDocuments(m.eventBus)
	if m.editorSaves == nil {
		m.editorSaves = make(map[string]*editorSaveState)
	}
	snapshot := ed.BufferSnapshot()
	if snapshot.ID == "" || snapshot.Path == "" {
		if m.Feedback != nil && reason == workspace.DocumentSaveManual {
			m.Feedback.NotifyError("Save failed: file has no path")
		}
		return nil
	}
	m.ws.Documents().Cancel(snapshot.ID)
	state := m.editorSaves[snapshot.ID]
	if state == nil {
		state = &editorSaveState{}
		m.editorSaves[snapshot.ID] = state
	}
	if state.inFlight {
		state.pending = true
		state.closeAfter = state.closeAfter || closeAfter
		state.reason = reason
		return nil
	}
	if state.err != nil && reason != workspace.DocumentSaveManual {
		return nil
	}
	shouldFormat := format && reason == workspace.DocumentSaveManual && m.editorSettings.FormatOnSave
	if !ed.Modified() && !shouldFormat {
		if closeAfter {
			m.overlayHost.Close(ui.OverlayDirtyClose)
			m.closeEditorTabForce(snapshot.Path)
			m.continuePendingTabClose()
		}
		return nil
	}
	state.inFlight = true
	state.closeAfter = closeAfter
	state.reason = reason
	state.err = nil
	ctx, cancel := context.WithCancel(context.Background())
	state.cancel = cancel
	if shouldFormat {
		generation := m.workspaceSwitchGen.Load()
		return func() tea.Msg {
			defer cancel()
			formatCtx, stop := context.WithTimeout(ctx, 10*time.Second)
			defer stop()
			data, err := workspace.FormatDocument(formatCtx, snapshot.Path, snapshot.Data)
			return editorFormatResultMsg{ed: ed, revision: snapshot.Revision, state: state, generation: generation, data: data, err: err}
		}
	}
	request := workspace.DocumentSaveRequest{
		BufferID: snapshot.ID, Path: snapshot.Path, Data: snapshot.Data, Revision: snapshot.Revision,
		ExpectedHash: snapshot.ExpectedHash, ExpectedExist: snapshot.ExpectedExists, Reason: reason,
	}
	service := m.ws.Documents()
	generation := m.workspaceSwitchGen.Load()
	return func() tea.Msg {
		defer cancel()
		result := service.Save(ctx, request)
		return editorSaveResultMsg{result: result, workspaceGeneration: generation, state: state}
	}
}

func (m *App) handleEditorSaveResult(msg editorSaveResultMsg) tea.Cmd {
	result := msg.result
	id := result.Request.BufferID
	if msg.workspaceGeneration != m.workspaceSwitchGen.Load() {
		return nil
	}
	state := m.editorSaves[id]
	if state == nil || state != msg.state {
		return nil
	}
	state.inFlight = false
	// Resolve the target by buffer lifetime, not by the path captured when the
	// save was requested: a rename during the write must not drop this
	// acknowledgement, which would leave the buffer dirty with a stale disk
	// baseline and turn the next save into a false conflict.
	tab, tabPath := m.editorTabByBufferID(id, result.Request.Path)
	if tab == nil {
		delete(m.editorSaves, id)
		return nil
	}
	if state.discard {
		if result.Committed && m.Feedback != nil {
			m.Feedback.NotifyInfo("Save completed before discard; the saved text remains on disk")
		}
		delete(m.editorSaves, id)
		m.closeEditorTabForce(tabPath)
		m.continuePendingTabClose()
		return nil
	}
	if result.Committed || result.Unchanged {
		tab.Editor.AcceptSave(result.Request.Revision, result.DiskHash, result.Request.Data)
	}
	if result.Err != nil {
		state.err = result.Err
		if errors.Is(result.Err, context.Canceled) {
			delete(m.editorSaves, id)
			return nil
		}
		if m.Feedback != nil {
			if errors.Is(result.Err, workspace.ErrDocumentConflict) {
				m.Feedback.NotifyError("Save blocked: file changed on disk")
			} else if result.Committed {
				m.Feedback.NotifyError(fmt.Sprintf("Saved, but durability could not be confirmed: %v", result.Err))
			} else {
				m.Feedback.NotifyError(fmt.Sprintf("Save failed: %v", result.Err))
			}
		}
		return nil
	}
	if state != nil && state.closeAfter && !tab.Editor.Modified() {
		m.overlayHost.Close(ui.OverlayDirtyClose)
		m.closeEditorTabForce(tabPath)
		delete(m.editorSaves, id)
		m.continuePendingTabClose()
		return nil
	}
	if result.Request.Reason == workspace.DocumentSaveManual && m.Feedback != nil {
		m.Feedback.NotifyInfo("Saved")
	}
	if state.pending {
		reason := state.reason
		state.pending = false
		return m.requestEditorSave(tab.Editor, reason, state.closeAfter)
	}
	delete(m.editorSaves, id)
	return nil
}

func (m *App) handleAutosaveDue(msg workspace.DocumentAutosaveDueMsg) tea.Cmd {
	if m.editorSettings.AutoSave != types.AutoSaveAfterDelay || m.ws.Documents() == nil || !m.ws.Documents().AcceptDue(msg) {
		return nil
	}
	// The deadline carries the buffer lifetime; a rename between scheduling and
	// firing must not strand the dirty buffer with no further deadline.
	tab, _ := m.editorTabByBufferID(msg.BufferID, msg.Path)
	if tab == nil || tab.Editor.Revision() != msg.Revision || !tab.Editor.Modified() {
		return nil
	}
	return m.requestEditorSave(tab.Editor, workspace.DocumentSaveAfterDelay, false)
}

func (m *App) applyEditorSettings(settings types.EditorSettings) {
	settings = types.NormalizeEditorSettings(settings)
	m.editorSettings = settings
	if m.EditorState == nil || m.ws.Documents() == nil {
		return
	}
	for _, tab := range m.EditorState.Tabs {
		if tab == nil || tab.Editor == nil {
			continue
		}
		ed := tab.Editor
		m.ws.Documents().Cancel(ed.BufferID())
		if state := m.editorSaves[ed.BufferID()]; state != nil && state.inFlight && state.reason != workspace.DocumentSaveManual {
			state.pending = false
			state.cancel()
		}
		if settings.AutoSave == types.AutoSaveAfterDelay && ed.Modified() {
			m.ws.Documents().Schedule(ed.BufferID(), ed.FilePath(), ed.Revision(), time.Duration(settings.AutoSaveDelay)*time.Millisecond)
		}
	}
}

func (m *App) logicalEditorFocus() *editor.Editor {
	if m.EditorState == nil || m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() || m.EditorState.ActiveView == "" {
		return nil
	}
	if m.windowManager != nil {
		if fw := m.focusedPopup(); fw != nil {
			switch fw.ID {
			case string(ui.OverlayFindReplace), "completion-popup", "diag-hover", "editor-changes", "context-menu", "dirty-close-prompt":
				return m.EditorState.Editor
			default:
				return nil
			}
		}
	}
	if m.focusTarget() == FocusEditor {
		return m.EditorState.Editor
	}
	return nil
}
