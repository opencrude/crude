package viewmodel

import (
	"context"
	"errors"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitreview"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

type gitReviewResultMsg struct {
	workspace  uint64
	sequence   uint64
	target     *gitreview.Screen
	operation  string
	comparison workspace.GitComparison
	review     workspace.GitCommitReview
	evidence   workspace.GitCommitEvidence
	commit     gitreview.CommitRequest
	prepared   []gitreview.PreparedComparison
	err        error
}

func (app *App) isGitReviewActive() bool {
	if app.Win == nil {
		return false
	}
	tab := app.Win.ActiveTab()
	return tab != nil && tab.Resource.Kind == window.ResourceGitReview && app.gitReview != nil
}

func (app *App) openGitReview(mode gitreview.Mode, change *workspace.GitChange) tea.Cmd {
	if app.gitReview == nil {
		app.gitReview = gitreview.New(app.Theme)
		app.gitReview.SetFocusHandler(app.rootHost.Focus)
	}
	if active := app.Win.ActiveTab(); active != nil && active.Resource.Kind != window.ResourceGitReview {
		app.gitReviewReturn = active.ID
	}
	app.Win.OpenResource(window.Resource{Kind: window.ResourceGitReview, ID: app.RootPath}, "Git Changes", true)
	app.gitReview.SetMode(mode)
	if app.sourceControl != nil {
		app.gitReview.SetSnapshot(app.sourceControl.Snapshot())
	}
	app.FocusStack.Clear()
	app.setTermDockFocused(false)
	app.setSessionDeckFocused(false)
	app.handleTabSwitch()
	if change != nil {
		return app.gitReviewCommand("compare", *change, gitreview.CommitRequest{}, "")
	}
	return app.refreshSourceControl()
}

func (app *App) gitReviewCommand(operation string, change workspace.GitChange, commit gitreview.CommitRequest, hash string) tea.Cmd {
	if app.gitReview == nil {
		return nil
	}
	service := app.Git
	if service == nil {
		service = workspace.NewGitService(app.RootPath)
		app.Git = service
	}
	app.gitReviewSequence++
	sequence, generation, target := app.gitReviewSequence, app.workspaceSwitchGen.Load(), app.gitReview
	comparison := target.Comparison()
	th := app.Theme
	target.SetLoading(true)
	return func() tea.Msg {
		ctx, cancel := app.sourceControlContext()
		defer cancel()
		result := gitReviewResultMsg{workspace: generation, sequence: sequence, target: target, operation: operation, commit: commit}
		switch operation {
		case "compare":
			result.comparison, result.err = service.Compare(ctx, change)
		case "review-commit":
			result.review, result.err = service.ReviewCommit(ctx)
		case "commit":
			result.err = service.CommitReviewed(ctx, commit.Review, commit.Message)
		case "stage":
			result.err = service.StageReviewed(ctx, comparison)
		case "unstage":
			result.err = service.UnstageReviewed(ctx, comparison)
		case "inspect-commit":
			result.evidence, result.err = service.InspectCommit(ctx, hash)
		}
		if result.err == nil {
			comparisons := result.review.Comparisons
			if operation == "compare" {
				comparisons = []workspace.GitComparison{result.comparison}
			}
			if operation == "inspect-commit" {
				comparisons = result.evidence.Comparisons
			}
			for _, item := range comparisons {
				if err := ctx.Err(); err != nil {
					result.err = err
					break
				}
				result.prepared = append(result.prepared, gitreview.PrepareComparison(th, item))
			}
		}
		return result
	}
}

func (app *App) applyGitReviewResult(message gitReviewResultMsg) tea.Cmd {
	if message.workspace != app.workspaceSwitchGen.Load() || message.target != app.gitReview || message.sequence != app.gitReviewSequence {
		return nil
	}
	if message.err != nil {
		app.gitReview.SetError(message.err)
		return nil
	}
	app.gitReview.SetLoading(false)
	app.gitReview.InstallComparisons(message.prepared)
	switch message.operation {
	case "compare":
		if len(message.prepared) > 0 {
			app.gitReview.SetComparison(message.prepared[0])
		}
	case "review-commit":
		app.gitReview.SetCommitReview(message.review)
	case "inspect-commit":
		app.gitReview.SetCommitEvidence(message.evidence)
	case "commit":
		app.gitReview.CompleteCommit(message.commit)
		return app.refreshSourceControl()
	case "stage", "unstage":
		return app.refreshSourceControl()
	}
	return nil
}

func (app *App) returnFromGitReview() {
	if app.Win == nil {
		return
	}
	if index := app.Win.IndexOf(app.gitReviewReturn); index >= 0 {
		app.Win.SwitchTo(index)
	} else {
		app.Win.Prev()
	}
	app.handleTabSwitch()
	app.FocusStack.Clear()
}

func (app *App) handleGitReviewAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "gitreview.") || app.gitReview == nil {
		return nil, false
	}
	switch action.ID {
	case gitreview.ActionOpenChange:
		change, ok := action.Payload.(workspace.GitChange)
		if ok {
			return app.gitReviewCommand("compare", change, gitreview.CommitRequest{}, ""), true
		}
	case gitreview.ActionReviewCommit:
		return app.gitReviewCommand("review-commit", workspace.GitChange{}, gitreview.CommitRequest{}, ""), true
	case gitreview.ActionCommit:
		request, ok := action.Payload.(gitreview.CommitRequest)
		if ok {
			return app.gitReviewCommand("commit", workspace.GitChange{}, request, ""), true
		}
	case gitreview.ActionStage, gitreview.ActionUnstage:
		request, ok := action.Payload.(gitreview.ComparisonRequest)
		if !ok {
			return nil, true
		}
		if tab := app.EditorState.Tabs[request.Comparison.AbsolutePath]; tab != nil && tab.Editor != nil && tab.Editor.Modified() {
			app.gitReview.SetError(errors.New("this document has unsaved changes; save explicitly, refresh, and review the disk bytes before staging"))
			return nil, true
		}
		return app.gitReviewCommand(strings.TrimPrefix(action.ID, "gitreview."), workspace.GitChange{}, gitreview.CommitRequest{}, ""), true
	case gitreview.ActionOpenFile:
		request, ok := action.Payload.(gitreview.ComparisonRequest)
		if ok {
			command, err := app.openEditorTab(request.Comparison.AbsolutePath)
			if err != nil {
				app.gitReview.SetError(err)
			}
			return command, true
		}
	case gitreview.ActionInspectCommit:
		request, ok := action.Payload.(gitreview.InspectCommitRequest)
		if ok {
			return app.gitReviewCommand("inspect-commit", workspace.GitChange{}, gitreview.CommitRequest{}, request.Commit.Hash), true
		}
	case gitreview.ActionReturn:
		app.returnFromGitReview()
	case gitreview.ActionRefresh, gitreview.ActionMode:
		return app.refreshSourceControl(), true
	case gitreview.ActionCheckout:
		request, ok := action.Payload.(gitreview.CheckoutRequest)
		if ok {
			app.startGitCheckout(request)
		}
	}
	return nil, true
}

func (app *App) startGitCheckout(request gitreview.CheckoutRequest) {
	state := &ui.ContextMenuState{Title: "Checkout " + request.Branch.Name, Sections: []ui.ContextMenuSection{{Items: []ui.ContextMenuItem{{Label: "Cancel"}, {Label: "Checkout branch", Action: func() {
		app.startSourceControlMutation(app.sourceControlMutation("Switched to "+request.Branch.Name, false, func(ctx context.Context, service *workspace.GitService) error {
			snapshot, err := service.Snapshot(ctx)
			if err != nil {
				return err
			}
			if snapshot.Head != request.Head || snapshot.RepositoryRoot != request.RepositoryRoot {
				return workspace.ErrGitReviewStale
			}
			return service.Checkout(ctx, request.Branch.Name)
		}))
	}}}}}}
	app.openContextMenuState(tea.Mouse{X: max(0, app.width/2-15), Y: max(app.shellTopRow(), app.height/2)}, state)
}
