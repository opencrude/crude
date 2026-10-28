package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/coreclient"
	"crude/projectpath"
	"crude/shellport"
	"crude/tui/screens/filepicker"
)

type pickerCreateFolderResultMsg struct {
	target     *filepicker.Screen
	intent     filepicker.IntentToken
	owner      shellport.Workspace
	generation uint64
	path       string
	name       string
	err        error
}

func (m *App) beginPickerCreateFolder(msg filepicker.CreateFolderMsg) tea.Cmd {
	if !msg.ValidFor(m.FilePicker) {
		return nil
	}
	m.invalidatePathSearch(msg.Target)
	m.invalidateDirectoryAsyncLoad(msg.Target)
	m.markFilePickerDirty()
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	return m.clientIOCommand(context.Background(), func(parent context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(parent, 20*time.Second)
		defer cancel()
		var path string
		var err error
		if creator, ok := owner.(shellport.WorkspaceFolderCreator); ok {
			path, err = creator.CreateWorkspaceFolder(ctx, msg.Parent, msg.Name)
		} else if _, connected := owner.(shellport.ClientFiles); connected {
			err = errors.New("connected core does not support creating workspace folders")
		} else {
			path, err = projectpath.CreateFolder(ctx, msg.Parent, msg.Name)
		}
		var uncertain *coreclient.UncertainError
		if err != nil && (errors.As(err, &uncertain) || ctx.Err() != nil || errors.Is(err, projectpath.ErrParentMovedAfterCreate)) {
			err = fmt.Errorf("%w; cancel and check the parent folder before retrying: %v", filepicker.ErrFolderCreationUnknown, err)
		}
		return pickerCreateFolderResultMsg{target: msg.Target, intent: msg.Intent(), owner: owner, generation: generation, path: path, name: msg.Name, err: err}
	})
}

func (m *App) applyPickerCreateFolderResult(result pickerCreateFolderResultMsg) tea.Cmd {
	if result.target == nil || result.target != m.FilePicker || result.owner != m.ws || result.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if result.err == nil && result.path == "" {
		result.err = fmt.Errorf("%w; cancel and check the parent folder before retrying", filepicker.ErrFolderCreationUnknown)
	}
	if !result.target.CompleteCreateFolderFor(result.intent, result.err) {
		return nil
	}
	m.markFilePickerDirty()
	if result.err != nil {
		return nil
	}
	if m.Feedback != nil {
		m.Feedback.NotifyInfo("Created folder: " + result.name)
	}
	return m.beginDirectoryAsyncLoadFor(result.target, result.path, result.intent)
}
