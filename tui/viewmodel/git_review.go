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

func (m *App) activeGitReview() *gitReviewView {
	if m.Win == nil {
		return nil
	}
	if tab := m.Win.ActiveTab(); tab != nil && tab.Resource.Kind == window.ResourceGitReview {
		return m.gitReviews[tab.ID]
	}
	return nil
}

func (m *App) isGitReviewActive() bool { return m.activeGitReview() != nil }

// gitReviewForScreen maps an action's screen back to its live tab.
func (m *App) gitReviewForScreen(screen *gitreview.Screen) *gitReviewView {
	for _, view := range m.gitReviews {
		if view.screen == screen && m.Win.IndexOf(view.tab) >= 0 {
			return view
		}
	}
	return nil
}

// openGitReviewTab activates (or creates) the tab for a resource and its screen.
func (m *App) openGitReviewTab(resource window.Resource, title string) *gitReviewView {
	returnTab := window.TabID(0)
	if active := m.Win.ActiveTab(); active != nil && active.Resource.Kind != window.ResourceGitReview {
		returnTab = active.ID
	}
	tab := m.Win.OpenResource(resource, title, true)
	if m.gitReviews == nil {
		m.gitReviews = make(map[window.TabID]*gitReviewView)
	}
	view := m.gitReviews[tab.ID]
	if view == nil {
		screen := gitreview.NewWithID(m.Theme, ui.ComponentID(fmt.Sprintf("%s/%d", gitreview.ScreenPrefix, tab.ID)))
		screen.SetFocusHandler(m.rootHost.Focus)
		view = &gitReviewView{tab: tab.ID, returnTab: returnTab, screen: screen}
		m.gitReviews[tab.ID] = view
	} else if returnTab != 0 {
		view.returnTab = returnTab
	}
	return view
}

// activateGitReviewTab focuses the review document, making room on short
// terminals where the dock would leave only a few readable rows.
func (m *App) activateGitReviewTab(view *gitReviewView) {
	m.FocusStack.Clear()
	if m.gitPanelOpen() && m.reviewRowsBesideDock() < gitReviewMinRows {
		// The review is what was asked for; the pill brings the dock back.
		m.hideWorkspacePanel()
	}
	m.setTermDockFocused(false)
	m.setSessionDeckFocused(false)
	m.handleTabSwitch()
	if m.rootHost != nil {
		m.rootHost.Focus(view.screen.DocumentID())
	}
}

// gitReviewMinRows is the shortest review body worth reading beside the dock.
const gitReviewMinRows = 14

// reviewRowsBesideDock estimates the main-area rows left while the dock is open.
func (m *App) reviewRowsBesideDock() int {
	return m.height - m.shellTopRow() - m.chromeBottomRows() - m.termDockHeight
}

// openGitReview opens the working-change review for scope ("" = every
// change, a folder, or one file) in its own tab, revealing a change key.
func (m *App) openGitReview(scope, reveal string) tea.Cmd {
	scope = strings.Trim(scope, "/")
	title := "Changes"
	if scope != "" {
		name, _ := gitdiff.SplitPath(scope)
		title = "Changes · " + name
	}
	view := m.openGitReviewTab(gitReviewResource(scope, ""), title)
	var files []gitdiff.File
	branch := ""
	if m.gitPanel != nil {
		files = m.gitPanel.PreparedList()
		branch = m.gitPanel.Snapshot().Branch
	}
	view.screen.SetScope(scope)
	view.screen.ShowChanges(files, branch)
	view.reveal = reveal
	if reveal != "" && view.screen.Reveal(reveal) {
		view.reveal = ""
	}
	if m.gitPanel != nil {
		view.screen.SetLoading(m.gitPanel.PreparationPending())
		if err := m.gitPanel.PreparationError(); err != "" {
			view.screen.SetError(fmt.Errorf("%s", err))
		}
	}
	m.activateGitReviewTab(view)
	return m.refreshSourceControl()
}

// openGitFileReview opens one changed file in its own tab.
func (m *App) openGitFileReview(change workspace.GitChange) tea.Cmd {
	return m.openGitReview(change.Path, gitdiff.ChangeKey(change))
}

// openGitCommitReview opens one commit, read-only, in its own tab.
func (m *App) openGitCommitReview(commit workspace.GitCommitSummary) tea.Cmd {
	view := m.openGitReviewTab(gitReviewResource("", commit.Hash), "Commit "+commit.ShortHash)
	view.commit = commit.Hash
	view.screen.ShowCommit(workspace.GitCommitEvidence{Commit: commit}, nil)
	view.screen.SetLoading(true)
	m.activateGitReviewTab(view)
	return m.inspectGitCommitInto(commit, view)
}

// showPreparedGitChanges hands the shared prepared set to every open
// working-change tab; commit tabs keep their own evidence.
func (m *App) showPreparedGitChanges(files []gitdiff.File, branch string) {
	for _, view := range m.gitReviews {
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
func (m *App) gitChangeReviewOpen() bool {
	if m.Win == nil {
		return false
	}
	for _, view := range m.gitReviews {
		if view.commit == "" && m.Win.IndexOf(view.tab) >= 0 {
			return true
		}
	}
	return false
}

// closeGitReview closes one review tab and returns to the tab it came from.
func (m *App) closeGitReview(id window.TabID) {
	m.removeGitReview(id, true)
}

func (m *App) removeGitReview(id window.TabID, returnToSource bool) {
	if cancel := m.gitEvidenceCancel[id]; cancel != nil {
		cancel()
		delete(m.gitEvidenceCancel, id)
	}
	view := m.gitReviews[id]
	if view != nil {
		view.screen.Document().StopSearch()
	}
	wasActive := m.Win.ActiveTab() != nil && m.Win.ActiveTab().ID == id
	delete(m.gitReviews, id)
	if view != nil {
		m.cancelOrphanedComparisonGeometry(view.screen.Document().SourceGeometry())
	}
	m.Win.CloseTabByID(id)
	if returnToSource && wasActive && view != nil {
		if index := m.Win.IndexOf(view.returnTab); index >= 0 {
			m.Win.SwitchTo(index)
		}
	}
	m.handleTabSwitch()
	if returnToSource {
		m.FocusStack.Clear()
	}
}

// returnFromGitReview leaves the review for the tab it was opened from,
// keeping the review tab open.
func (m *App) returnFromGitReview(view *gitReviewView) {
	if m.Win == nil || view == nil {
		return
	}
	if index := m.Win.IndexOf(view.returnTab); index >= 0 {
		m.Win.SwitchTo(index)
	} else {
		m.Win.Prev()
	}
	m.handleTabSwitch()
	m.FocusStack.Clear()
}

// handleGitReviewScreenAction routes the actions a review screen emits with
// itself as payload.
func (m *App) handleGitReviewScreenAction(action ui.Action) (tea.Cmd, bool) {
	screen, ok := action.Payload.(*gitreview.Screen)
	if !ok {
		return nil, false
	}
	view := m.gitReviewForScreen(screen)
	if view == nil {
		return nil, true
	}
	switch action.ID {
	case gitreview.ActionReturn:
		m.returnFromGitReview(view)
	case gitreview.ActionOpenAll:
		return m.openGitReview("", ""), true
	case gitreview.ActionRefresh:
		if view.commit != "" {
			return m.inspectGitCommitInto(workspace.GitCommitSummary{Hash: view.commit, ShortHash: shortHash(view.commit)}, view), true
		}
		return m.refreshSourceControl(), true
	}
	return nil, true
}

func shortHash(hash string) string {
	if len(hash) > 7 {
		return hash[:7]
	}
	return hash
}
