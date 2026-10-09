package viewmodel

import (
	"context"
	"sort"

	tea "charm.land/bubbletea/v2"

	"crude/shellport"
	"crude/tui/screens/gitpanel"
	"crude/tui/screens/shellheader"
	"crude/tui/ui"
	"crude/workspace"
)

// openBranchSwitcher opens the header's branch dropdown: local branches,
// remote-tracking branches, and a fallback into the Git dock panel.
// Left-click on the header Git segment lands here; right-click keeps the
// shell region menu (dispatchMouseRightClick).
func (m *App) openBranchSwitcher() tea.Cmd {
	if m.gitPanel == nil {
		return nil
	}
	snapshot := m.gitPanel.Snapshot()
	var local, remote []workspace.GitBranchSummary
	for _, branch := range snapshot.Branches {
		if branch.Remote {
			remote = append(remote, branch)
		} else {
			local = append(local, branch)
		}
	}
	st := &ui.ContextMenuState{Title: "⑂ " + snapshot.Branch}
	if snapshot.Branch == "" {
		st.Title = "⑂ Branches"
	}
	if len(local) > 0 {
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Local",
			Items:        m.branchMenuItems(local, snapshot.Branch),
		})
	}
	if len(remote) > 0 {
		st.Sections = append(st.Sections, ui.ContextMenuSection{
			SectionLabel: "Remote",
			Items:        m.branchMenuItems(remote, snapshot.Branch),
		})
	}
	st.Sections = append(st.Sections, ui.ContextMenuSection{
		SectionLabel: "Repository",
		Items: []ui.ContextMenuItem{
			{Label: "Refresh", Detail: "Ctrl+R", Action: func() {
				m.startSourceControlMutation(m.refreshSourceControl())
			}},
			{Label: "Open Git panel", Detail: "Ctrl+Alt+G", Action: func() {
				m.startSourceControlMutation(m.openGitPanel(gitpanel.ModeChanges))
			}},
			{Label: "Review all changes", Action: func() {
				m.startSourceControlMutation(m.openGitReview("", ""))
			}},
		},
	})
	if len(local)+len(remote) == 0 {
		// No branches listed: fall back to the panel instead of a dead menu.
		return m.openGitPanel(gitpanel.ModeChanges)
	}
	return m.openBranchSwitcherAtHeader(st)
}

// branchMenuItems renders one branch group, deduplicated and sorted. The
// current branch shows a "current" detail and is disabled so Enter never
// re-checkouts it.
func (m *App) branchMenuItems(branches []workspace.GitBranchSummary, current string) []ui.ContextMenuItem {
	seen := make(map[string]bool, len(branches))
	names := make([]string, 0, len(branches))
	for _, branch := range branches {
		if branch.Name == "" || seen[branch.Name] {
			continue
		}
		seen[branch.Name] = true
		names = append(names, branch.Name)
	}
	sort.Strings(names)
	items := make([]ui.ContextMenuItem, 0, len(names))
	for _, name := range names {
		name := name
		detail := ""
		disabled := name == current
		if disabled {
			detail = "current"
		}
		items = append(items, ui.ContextMenuItem{
			Label: name, Detail: detail, Disabled: disabled,
			Action: func() {
				m.startSourceControlMutation(m.sourceControlMutation(
					"Switched to "+name, nil,
					func(ctx context.Context, service shellport.GitService) error {
						return service.Checkout(ctx, name)
					},
				))
			},
		})
	}
	return items
}

// openBranchSwitcherAtHeader anchors the dropdown under the header's Git
// segment: the header slot spans the full content width at the frame top, so
// the menu opens at (frameLeft, frameTop+2) — just below the two-row header
// chrome. openContextMenuState clamps the popup inside the shell frame.
func (m *App) openBranchSwitcherAtHeader(st *ui.ContextMenuState) tea.Cmd {
	x, y := 0, 2
	if m.rootHost != nil {
		if rect, ok := m.rootHost.ScreenRect(shellheader.ScreenID); ok && !rect.Empty() {
			x, y = rect.X, rect.Y+rect.H
		}
	}
	return m.openContextMenuState(tea.Mouse{X: x, Y: y}, st)
}
