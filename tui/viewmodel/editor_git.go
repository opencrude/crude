package viewmodel

import (
	"context"
	"errors"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/workspace"
)

type editorGitAnnotationMsg struct {
	generation uint64
	workspace  uint64
	path       string
	revision   uint64
	annotation workspace.GitFileAnnotation
	err        error
}

const (
	// editorGitIdleDelay is how long typing must pause before the gutter Git
	// projection is recomputed. Recomputing on every keystroke made the change
	// markers blink; recomputing on a fixed interval still churned them while
	// the user kept typing.
	editorGitIdleDelay = 400 * time.Millisecond
	// editorGitHoldLimit bounds how stale the projection may become during a
	// continuous typing burst, so a long paragraph still refreshes occasionally.
	editorGitHoldLimit = 2 * time.Second
)

// markEditorGitDirty records one text change and coalesces the recompute: the
// whole burst collapses into a single refresh once typing pauses (or once the
// burst has held the projection stale for editorGitHoldLimit).
func (m *App) markEditorGitDirty(now time.Time) {
	m.editorGitGen.Add(1)
	m.editorGitEditAt = now
	m.editorGitStale.Store(true)
	if m.editorGitSince.IsZero() {
		m.editorGitSince = now
	}
}

// markEditorGitStaleNow requests a recompute that must not wait for a typing
// pause: saves, renames, source-control operations, tab switches, and disk
// changes are facts the user did not type.
func (m *App) markEditorGitStaleNow() {
	m.editorGitStale.Store(true)
	m.editorGitEditAt = time.Time{}
	m.editorGitSince = time.Time{}
}

// editorGitDue reports whether the due-at-now projection refresh should start.
func (m *App) editorGitDue(now time.Time) bool {
	if !m.editorGitStale.Load() {
		return false
	}
	if m.editorGitEditAt.IsZero() {
		return true
	}
	if now.Sub(m.editorGitEditAt) >= editorGitIdleDelay {
		return true
	}
	return !m.editorGitSince.IsZero() && now.Sub(m.editorGitSince) >= editorGitHoldLimit
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
	revision := m.EditorState.Editor.Revision()
	gen := m.editorGitGen.Add(1)
	workspaceGen := m.workspaceSwitchGen.Load()
	service := m.Git
	if m.editorGitCancel != nil {
		m.editorGitCancel()
	}
	ctx, cancel := m.sourceControlContext()
	m.editorGitCancel = cancel
	m.editorGitStale.Store(false)
	m.editorGitEditAt = time.Time{}
	m.editorGitSince = time.Time{}
	return func() tea.Msg {
		defer cancel()
		ann, err := service.AnnotateFile(ctx, path, content)
		return editorGitAnnotationMsg{
			generation: gen,
			workspace:  workspaceGen,
			path:       path,
			revision:   revision,
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
	m.editorGitCancel = nil
	if msg.err != nil {
		// A transient failure (canceled or timed-out Git work) must not blank a
		// projection the user is looking at; the next refresh replaces it.
		// Deterministic failures — no repository, path outside it — genuinely
		// have no annotation, so they clear.
		if !errors.Is(msg.err, context.Canceled) && !errors.Is(msg.err, context.DeadlineExceeded) {
			m.EditorState.Editor.SetGitAnnotation(msg.revision, nil, nil, false)
		}
		return
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
	hunks := make([]editor.GitHunk, len(msg.annotation.Hunks))
	for i, hunk := range msg.annotation.Hunks {
		hunks[i] = editor.GitHunk{
			OldStart: hunk.OldStart,
			NewStart: hunk.NewStart,
			OldLines: append([]string(nil), hunk.OldLines...),
			NewLines: append([]string(nil), hunk.NewLines...),
		}
	}
	m.EditorState.Editor.SetGitAnnotation(msg.revision, hunks, blame, msg.annotation.Conflicted)
}
