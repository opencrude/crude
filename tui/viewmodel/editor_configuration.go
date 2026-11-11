package viewmodel

import (
	tea "charm.land/bubbletea/v2"
	"context"
	"crude/indentation"
	"crude/shellport"
	"crude/tui/editor"
	"time"
)

type editorConfigRequest struct {
	owner    shellport.Workspace
	id, path string
	epoch    uint64
	cancel   context.CancelFunc
	loading  bool
	invalid  bool
	err      error
}
type editorConfigResult struct {
	editor  *editor.Editor
	request *editorConfigRequest
	path    string
	choices indentation.Choices
	err     error
}

func (m *App) editorConfigurationCommand(ed *editor.Editor) tea.Cmd {
	port, ok := m.ws.(shellport.EditorConfiguration)
	if !ok || ed == nil || !ed.IsOpen() || ed.ReadOnly() {
		return nil
	}
	ed = ed.Primary()
	id, path := ed.DocumentState().DocumentID, ed.FilePath()
	if id == "" {
		return nil
	}
	old := m.editorConfigRequests[ed]
	epoch := m.workspaceSwitchGen.Load()
	if old != nil {
		if old.owner != m.ws || old.id != id || old.path != path || old.epoch != epoch {
			old.invalid = true
			old.cancel()
		}
		if !old.invalid || old.loading {
			return nil
		}
		delete(m.editorConfigRequests, ed)
	}
	running := 0
	for _, r := range m.editorConfigRequests {
		if r.loading {
			running++
		}
	}
	if running >= 4 {
		return nil
	}

	if old != nil && old.path != path {
		_ = ed.SetProjectIndentation(indentation.Choices{})
	}
	ctx, cancel := context.WithCancel(context.Background())
	request := &editorConfigRequest{owner: m.ws, id: id, path: path, epoch: epoch, cancel: cancel, loading: true}
	if m.editorConfigRequests == nil {
		m.editorConfigRequests = make(map[*editor.Editor]*editorConfigRequest)
	}
	m.editorConfigRequests[ed] = request
	run := m.clientIOCommand(ctx, func(parent context.Context) tea.Msg {
		defer cancel()
		ctx, stop := context.WithTimeout(parent, 10*time.Second)
		defer stop()
		path, choices, err := port.ReadEditorConfig(ctx, id)
		return editorConfigResult{ed, request, path, choices, err}
	})
	// Cancellation before command admission still retires this read's slot.
	return func() tea.Msg {
		if result := run(); result != nil {
			return result
		}
		return editorConfigResult{editor: ed, request: request, err: context.Canceled}
	}
}
func (m *App) applyEditorConfiguration(msg editorConfigResult) {
	ed, r := msg.editor, msg.request
	if r == nil || m.editorConfigRequests[ed] != r {
		return
	}
	r.loading = false
	if r.invalid || r.owner != m.ws || r.epoch != m.workspaceSwitchGen.Load() || !ed.IsOpen() || r.path != ed.FilePath() || r.id != ed.DocumentState().DocumentID {
		delete(m.editorConfigRequests, ed)
		return
	}
	r.err = msg.err
	if msg.err != nil {
		m.Feedback.NotifyError("Could not read EditorConfig. Check the file, then reload it from the indentation menu. " + msg.err.Error())
		return
	}
	if msg.path != r.path {
		return
	}
	if err := ed.SetProjectIndentation(msg.choices); err != nil {
		r.err = err
		m.Feedback.NotifyError(err.Error())
	}
	m.invalidatePreparedEditor(ed)
}
func (m *App) invalidateEditorConfiguration() {
	m.editorConfigGeneration++
	for _, r := range m.editorConfigRequests {
		r.invalid = true
		r.cancel()
	}
}
func (m *App) cancelEditorConfiguration() {
	m.invalidateEditorConfiguration()
	clear(m.editorConfigRequests)
}

// A newly mounted document already has its project policy before its first key.
// A newer watcher event or an existing live policy wins over this prepared read.
func (m *App) installPreparedEditorConfiguration(ed *editor.Editor, file paneFilePrepared, generation uint64) {
	if !file.configRead || generation != m.editorConfigGeneration || ed.FilePath() != file.configPath {
		return
	}
	ed = ed.Primary()
	if m.editorConfigRequests[ed] != nil {
		return
	}
	request := &editorConfigRequest{owner: m.ws, id: ed.DocumentState().DocumentID, path: ed.FilePath(), epoch: m.workspaceSwitchGen.Load(), cancel: func() {}}
	if m.editorConfigRequests == nil {
		m.editorConfigRequests = make(map[*editor.Editor]*editorConfigRequest)
	}
	m.editorConfigRequests[ed] = request
	m.applyEditorConfiguration(editorConfigResult{editor: ed, request: request, path: file.configPath, choices: file.config, err: file.configErr})
}
