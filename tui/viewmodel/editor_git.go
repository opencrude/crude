package viewmodel

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/workspace"
)

type editorGitAnnotationMsg struct {
	generation uint64
	workspace  uint64
	path       string
	annotation workspace.GitFileAnnotation
	err        error
}

func (m *App) refreshEditorGitCmd() tea.Cmd {
	if m == nil || m.EditorState == nil || m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return nil
	}
	path := m.EditorState.Editor.FilePath()
	if path == "" {
		return nil
	}
	if m.Git == nil {
		m.Git = workspace.NewGitService(m.RootPath)
	}
	content := m.EditorState.Editor.Value()
	gen := m.editorGitGen.Add(1)
	workspaceGen := m.workspaceSwitchGen.Load()
	service := m.Git
	m.editorGitAt = time.Now()
	m.editorGitStale.Store(false)
	return func() tea.Msg {
		ctx, cancel := m.sourceControlContext()
		defer cancel()
		ann, err := service.AnnotateFile(ctx, path, content)
		return editorGitAnnotationMsg{
			generation: gen,
			workspace:  workspaceGen,
			path:       path,
			annotation: ann,
			err:        err,
		}
	}
}

func (m *App) applyEditorGitAnnotation(msg editorGitAnnotationMsg) {
	if m == nil || msg.generation != m.editorGitGen.Load() {
		return
	}
	if msg.workspace != m.workspaceSwitchGen.Load() {
		return
	}
	if m.EditorState == nil || m.EditorState.Editor == nil || !m.EditorState.Editor.IsOpen() {
		return
	}
	if m.EditorState.Editor.FilePath() != msg.path {
		return
	}
	if msg.err != nil {
		m.EditorState.Editor.SetGitAnnotation(nil, nil)
		return
	}
	edits := make([]editor.GitLineKind, len(msg.annotation.Edits))
	for i, kind := range msg.annotation.Edits {
		edits[i] = editor.GitLineKind(kind)
	}
	blame := make([]editor.GitBlameLine, len(msg.annotation.Blame))
	for i, line := range msg.annotation.Blame {
		blame[i] = editor.GitBlameLine{
			Line:        line.Line,
			Hash:        line.Hash,
			ShortHash:   line.ShortHash,
			Author:      line.Author,
			When:        line.When,
			Subject:     line.Subject,
			Uncommitted: line.Uncommitted,
		}
	}
	m.EditorState.Editor.SetGitAnnotation(edits, blame)
}
