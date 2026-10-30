package app

import (
	"context"
	"errors"
	"fmt"

	"crude/coreprotocol"
	"crude/shellport/remote"
	startuptui "crude/tui/startup"
	"crude/types"
)

// attachStartupSelection is the single startup chooser/attachment lifecycle.
// Private handovers never enter the ordinary repair chooser, including failures
// after a successful initial inspection. Callbacks are the existing I/O owners.
func attachStartupSelection(ctx context.Context, root string, exact bool, selection coreprotocol.StartupSnapshot,
	choose func(context.Context, coreprotocol.StartupSnapshot) (startuptui.Selection, error),
	attach func(context.Context, string) (*remote.Workspace, error),
) (string, *remote.Workspace, []types.OperationIssue, error) {
	if exact && (selection.Root != root || selection.FolderKind != "" || selection.PathError != "" || selection.ShowIntroduction) {
		return "", nil, nil, errors.New("handover destination is not ready; no alternative workspace or onboarding choice was selected")
	}
	issues := append([]types.OperationIssue(nil), selection.Issues...)
	for {
		if selection.ShowIntroduction || selection.FolderKind != "" || selection.PathError != "" {
			selected, err := choose(ctx, selection)
			if err != nil {
				return "", nil, nil, fmt.Errorf("select project: %w", err)
			}
			if selected.Root == "" {
				return "", nil, nil, nil
			}
			root, issues = selected.Root, selected.Issues
		} else {
			root = selection.Root
		}
		ws, err := attach(ctx, root)
		if err == nil {
			return root, ws, issues, nil
		}
		if exact {
			return "", nil, nil, fmt.Errorf("attach exact handover destination: %w", err)
		}
		// Ordinary launch keeps its repair navigation after disappearance or
		// initialization failure. It never discards an old attachment/draft.
		selection.Root, selection.PathError, selection.ShowIntroduction, selection.Issues = root, err.Error(), false, issues
	}
}
