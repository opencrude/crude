package viewmodel

import (
	"fmt"
	"path/filepath"
	"strings"

	"crude/tui/screens/gitdiff"
	"crude/tui/ui"
	"crude/workspace"
)

// gitChangesUnder returns the repository-relative scope for an absolute
// workspace path and every listed change at or below it. A path outside the
// repository has no scope.
func (m *App) gitChangesUnder(path string) (string, []workspace.GitChange, bool) {
	if m.gitPanel == nil {
		return "", nil, false
	}
	snapshot := m.gitPanel.Snapshot()
	if snapshot.RepositoryRoot == "" {
		return "", nil, false
	}
	if path == "" {
		path = m.RootPath
	}
	rel, err := filepath.Rel(snapshot.RepositoryRoot, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", nil, false
	}
	rel = filepath.ToSlash(rel)
	if rel == "." {
		rel = ""
	}
	var changes []workspace.GitChange
	for _, change := range snapshot.Changes {
		if rel == "" || change.Path == rel || strings.HasPrefix(change.Path, rel+"/") {
			changes = append(changes, change)
		}
	}
	return rel, changes, true
}

// explorerGitSection is the Explorer context menu's Git group: review the
// changes of a file or folder, and stage, unstage or discard them in place.
func (m *App) explorerGitSection(path string, isDir bool) (ui.ContextMenuSection, bool) {
	scope, changes, ok := m.gitChangesUnder(path)
	if !ok {
		return ui.ContextMenuSection{}, false
	}
	files := make(map[string]bool)
	var unstaged, staged, discardable []workspace.GitChange
	for _, change := range changes {
		files[change.Path] = true
		switch change.Area {
		case workspace.GitAreaStaged:
			staged = append(staged, change)
		case workspace.GitAreaWorking:
			unstaged = append(unstaged, change)
			discardable = append(discardable, change)
		default:
			unstaged = append(unstaged, change)
		}
	}
	viewDetail := "no changes"
	if len(files) > 0 {
		viewDetail = gitCountNoun(len(files), "file")
	}
	reveal := ""
	if !isDir && len(changes) > 0 {
		reveal = gitdiff.ChangeKey(changes[0])
	}
	section := ui.ContextMenuSection{SectionLabel: "Git", Items: []ui.ContextMenuItem{
		{Label: "View Changes", Detail: viewDetail, Disabled: len(changes) == 0, Action: func() {
			m.startSourceControlMutation(m.openGitReview(scope, reveal))
		}},
	}}
	noun := "Changes"
	if isDir || path == "" {
		noun = fmt.Sprintf("%d Changes", len(unstaged))
	}
	section.Items = append(section.Items,
		ui.ContextMenuItem{Label: "Stage " + noun, Disabled: len(unstaged) == 0, Action: func() {
			m.startSourceControlMutation(m.stageGitChanges(unstaged))
		}},
		ui.ContextMenuItem{Label: "Unstage", Disabled: len(staged) == 0, Action: func() {
			m.startSourceControlMutation(m.unstageGitChanges(staged))
		}},
	)
	if !isDir && path != "" {
		section.Items = append(section.Items, ui.ContextMenuItem{Label: "Discard Changes…", Disabled: len(discardable) == 0, Action: func() {
			if len(discardable) > 0 {
				m.startDiscardGitChange(discardable[0])
			}
		}})
	}
	return section, true
}
