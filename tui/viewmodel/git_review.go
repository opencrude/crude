package viewmodel

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/screens/gitdiff"
	"crude/tui/screens/gitreview"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace"
)

// gitReviewView is one Git review tab. Working-change tabs (all changes, a
// folder, or one file) follow the shared prepared change set; commit tabs
// hold their own read-only evidence. Each tab owns a distinct screen so any
// number can be open, moved and split side by side.
type gitReviewView struct {
	tab       window.TabID
	returnTab window.TabID
	screen    *gitreview.Screen
	commit    string // full hash for commit tabs; "" for working changes
	reveal    string // change key to reveal once its diff is prepared
	// evidenceSequence identifies the commit read this tab is waiting for.
	evidenceSequence uint64
}

// gitReviewResource names a tab's identity: reopening the same scope or
// commit activates the existing tab instead of creating a duplicate.
func gitReviewResource(scope, commit string) window.Resource {
	if commit != "" {
		return window.Resource{Kind: window.ResourceGitReview, ID: "commit:" + commit}
	}
	return window.Resource{Kind: window.ResourceGitReview, ID: "changes:" + scope}
}

func (app *App) activeGitReview() *gitReviewView {
	if app.Win == nil {
		return nil
	}
	if tab := app.Win.ActiveTab(); tab != nil && tab.Resource.Kind == window.ResourceGitReview {
		return app.gitReviews[tab.ID]
	}
	return nil
}

func (app *App) isGitReviewActive() bool { return app.activeGitReview() != nil }

// gitReviewForScreen maps an action's screen back to its live tab.
func (app *App) gitReviewForScreen(screen *gitreview.Screen) *gitReviewView {
	for _, view := range app.gitReviews {
		if view.screen == screen && app.Win.IndexOf(view.tab) >= 0 {
			return view
		}
	}
	return nil
}

// openGitReviewTab activates (or creates) the tab for a resource and its screen.
func (app *App) openGitReviewTab(resource window.Resource, title string) *gitReviewView {
	returnTab := window.TabID(0)
	if active := app.Win.ActiveTab(); active != nil && active.Resource.Kind != window.ResourceGitReview {
		returnTab = active.ID
	}
	tab := app.Win.OpenResource(resource, title, true)
	if app.gitReviews == nil {
		app.gitReviews = make(map[window.TabID]*gitReviewView)
	}
	view := app.gitReviews[tab.ID]
	if view == nil {
		screen := gitreview.NewWithID(app.Theme, ui.ComponentID(fmt.Sprintf("%s/%d", gitreview.ScreenPrefix, tab.ID)))
		screen.SetFocusHandler(app.rootHost.Focus)
		view = &gitReviewView{tab: tab.ID, returnTab: returnTab, screen: screen}
		app.gitReviews[tab.ID] = view
	} else if returnTab != 0 {
		view.returnTab = returnTab
	}
	return view
}

// activateGitReviewTab focuses the review document, making room on short
// terminals where the dock would leave only a few readable rows.
func (app *App) activateGitReviewTab(view *gitReviewView) {
	app.FocusStack.Clear()
	if app.gitPanelOpen() && app.reviewRowsBesideDock() < gitReviewMinRows {
		// The review is what was asked for; the pill brings the dock back.
		app.hideWorkspacePanel()
	}
	app.setTermDockFocused(false)
	app.setSessionDeckFocused(false)
	app.handleTabSwitch()
	if app.rootHost != nil {
		app.rootHost.Focus(view.screen.DocumentID())
	}
}

// gitReviewMinRows is the shortest review body worth reading beside the dock.
const gitReviewMinRows = 14

// reviewRowsBesideDock estimates the main-area rows left while the dock is open.
func (app *App) reviewRowsBesideDock() int {
	return app.height - app.shellTopRow() - app.chromeBottomRows() - app.termDockHeight
}

// openGitReview opens the working-change review for scope ("" = every
// change, a folder, or one file) in its own tab, revealing a change key.
func (app *App) openGitReview(scope, reveal string) tea.Cmd {
	scope = strings.Trim(scope, "/")
	title := "Changes"
	if scope != "" {
		name, _ := gitdiff.SplitPath(scope)
		title = "Changes · " + name
	}
	view := app.openGitReviewTab(gitReviewResource(scope, ""), title)
	var files []gitdiff.File
	branch := ""
	if app.gitPanel != nil {
		files = app.gitPanel.PreparedList()
		branch = app.gitPanel.Snapshot().Branch
	}
	view.screen.SetScope(scope)
	view.screen.ShowChanges(files, branch)
	view.reveal = reveal
	if reveal != "" && view.screen.Reveal(reveal) {
		view.reveal = ""
	}
	if app.gitPanel != nil {
		view.screen.SetLoading(app.gitPanel.PreparationPending())
		if err := app.gitPanel.PreparationError(); err != "" {
			view.screen.SetError(fmt.Errorf("%s", err))
		}
	}
	app.activateGitReviewTab(view)
	return app.refreshSourceControl()
}

// openGitFileReview opens one changed file in its own tab.
func (app *App) openGitFileReview(change workspace.GitChange) tea.Cmd {
	return app.openGitReview(change.Path, gitdiff.ChangeKey(change))
}

// openGitCommitReview opens one commit, read-only, in its own tab.
func (app *App) openGitCommitReview(commit workspace.GitCommitSummary) tea.Cmd {
	view := app.openGitReviewTab(gitReviewResource("", commit.Hash), "Commit "+commit.ShortHash)
	view.commit = commit.Hash
	view.screen.ShowCommit(workspace.GitCommitEvidence{Commit: commit}, nil)
	view.screen.SetLoading(true)
	app.activateGitReviewTab(view)
	return app.inspectGitCommitInto(commit, view)
}

// showPreparedGitChanges hands the shared prepared set to every open
// working-change tab; commit tabs keep their own evidence.
func (app *App) showPreparedGitChanges(files []gitdiff.File, branch string) {
	for _, view := range app.gitReviews {
		if view.commit != "" {
			continue
		}
		view.screen.ShowChanges(files, branch)
		if view.reveal != "" && view.screen.Reveal(view.reveal) {
			view.reveal = ""
		}
	}
}

// gitChangeReviewOpen reports whether any tab shows working changes.
func (app *App) gitChangeReviewOpen() bool {
	if app.Win == nil {
		return false
	}
	for _, view := range app.gitReviews {
		if view.commit == "" && app.Win.IndexOf(view.tab) >= 0 {
			return true
		}
	}
	return false
}

// closeGitReview closes one review tab and returns to the tab it came from.
func (app *App) closeGitReview(id window.TabID) {
	if cancel := app.gitEvidenceCancel[id]; cancel != nil {
		cancel()
		delete(app.gitEvidenceCancel, id)
	}
	view := app.gitReviews[id]
	if view != nil {
		view.screen.Document().StopSearch()
	}
	wasActive := app.Win.ActiveTab() != nil && app.Win.ActiveTab().ID == id
	delete(app.gitReviews, id)
	if view != nil {
		app.cancelOrphanedComparisonGeometry(view.screen.Document().SourceGeometry())
	}
	app.Win.CloseTabByID(id)
	if wasActive && view != nil {
		if index := app.Win.IndexOf(view.returnTab); index >= 0 {
			app.Win.SwitchTo(index)
		}
	}
	app.handleTabSwitch()
	app.FocusStack.Clear()
}

// returnFromGitReview leaves the review for the tab it was opened from,
// keeping the review tab open.
func (app *App) returnFromGitReview(view *gitReviewView) {
	if app.Win == nil || view == nil {
		return
	}
	if index := app.Win.IndexOf(view.returnTab); index >= 0 {
		app.Win.SwitchTo(index)
	} else {
		app.Win.Prev()
	}
	app.handleTabSwitch()
	app.FocusStack.Clear()
}

// handleGitReviewScreenAction routes the actions a review screen emits with
// itself as payload.
func (app *App) handleGitReviewScreenAction(action ui.Action) (tea.Cmd, bool) {
	screen, ok := action.Payload.(*gitreview.Screen)
	if !ok {
		return nil, false
	}
	view := app.gitReviewForScreen(screen)
	if view == nil {
		return nil, true
	}
	switch action.ID {
	case gitreview.ActionReturn:
		app.returnFromGitReview(view)
	case gitreview.ActionOpenAll:
		return app.openGitReview("", ""), true
	case gitreview.ActionRefresh:
		if view.commit != "" {
			return app.inspectGitCommitInto(workspace.GitCommitSummary{Hash: view.commit, ShortHash: shortHash(view.commit)}, view), true
		}
		return app.refreshSourceControl(), true
	}
	return nil, true
}

func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}
