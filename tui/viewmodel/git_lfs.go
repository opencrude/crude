package viewmodel

import (
	"context"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/workspace"
)

func (m *App) gitLargeFiles(operation string, paths ...string) tea.Cmd {
	label := "Set up Git LFS"
	switch operation {
	case "pull":
		return m.runGitRemote("Downloading large files", "Downloaded large files", func(ctx context.Context, git shellport.GitService) (workspace.GitSyncResult, error) {
			return workspace.GitSyncResult{}, git.LFS(ctx, operation)
		})
	case "track":
		label = "Track file with Git LFS"
	}
	return m.sourceControlMutation(label, nil, func(ctx context.Context, git shellport.GitService) error {
		return git.LFS(ctx, operation, paths...)
	})
}
