package gitreview

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

type reviewToolbar struct {
	ui.Base
	owner   *Screen
	bar     *ui.ActionBar
	buttons []*reviewButton
	widths  []int
}

func newReviewToolbar(id ui.ComponentID, owner *Screen) *reviewToolbar {
	bar := ui.NewActionBar(id, owner.tokens)
	bar.SetAlignEnd(false)
	bar.SetSurfaceBackground(ui.ColorBg)
	bar.SetExitActions("gitreview.focus-next", "gitreview.focus-previous")
	return &reviewToolbar{Base: ui.NewBase(id), owner: owner, bar: bar}
}

func (toolbar *reviewToolbar) setTokens(tokens *ui.TokenSet) {
	toolbar.bar.SetTokens(tokens)
	for _, button := range toolbar.buttons {
		button.SetTokens(tokens)
		button.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	}
}

func (toolbar *reviewToolbar) setButtons(specs []ui.ActionBarButton) {
	existing := make(map[ui.ComponentID]*reviewButton, len(toolbar.buttons))
	for _, button := range toolbar.buttons {
		existing[button.ID()] = button
	}
	toolbar.buttons = toolbar.buttons[:0]
	toolbar.widths = toolbar.widths[:0]
	for _, spec := range specs {
		id := ui.NewID(toolbar.ID(), spec.Action)
		button := existing[id]
		if button == nil {
			button = &reviewButton{Button: ui.NewButton(id, spec.Label, spec.Action), toolbar: toolbar}
		}
		button.SetTokens(toolbar.owner.tokens)
		button.SetSurfaceBackground(toolbar.owner.tokens.Color(ui.ColorBg))
		button.SetDisabled(!spec.Enabled)
		button.SetVariant(spec.Variant)
		button.SetLabel(spec.Label)
		toolbar.buttons = append(toolbar.buttons, button)
		toolbar.widths = append(toolbar.widths, rendering.VisibleWidth(spec.Label)+4)
	}
	toolbar.bar.SetButtons(specs...)
}

func (toolbar *reviewToolbar) rows(width int) int {
	rows, used := 1, 0
	for _, buttonW := range toolbar.widths {
		if used > 0 && used+2+buttonW > width {
			rows++
			used = 0
		}
		if used > 0 {
			used += 2
		}
		used += buttonW
	}
	return rows
}

func (toolbar *reviewToolbar) Children() []ui.Component {
	if toolbar.rows(toolbar.Bounds().W) <= 1 {
		return nil
	}
	children := make([]ui.Component, len(toolbar.buttons))
	for position, button := range toolbar.buttons {
		children[position] = button
	}
	return children
}

func (toolbar *reviewToolbar) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	toolbar.SetBounds(bounds)
	toolbar.bar.Layout(ctx, ui.Rect{W: bounds.W, H: bounds.H})
	column, row := 0, 0
	for position, button := range toolbar.buttons {
		width := min(toolbar.widths[position], bounds.W)
		if column > 0 && column+2+width > bounds.W {
			row++
			column = 0
		}
		if column > 0 {
			column += 2
		}
		height := 1
		if row >= bounds.H {
			height = 0
		}
		button.Layout(ctx, ui.Rect{X: column, Y: row, W: max(0, min(width, bounds.W-column)), H: height})
		column += width
	}
}

func (toolbar *reviewToolbar) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	if toolbar.rows(toolbar.Bounds().W) <= 1 && toolbar.Bounds().H > 0 {
		toolbar.bar.Paint(buf, originX+toolbar.Bounds().X, originY+toolbar.Bounds().Y, ctx)
	}
}

func (toolbar *reviewToolbar) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if toolbar.rows(toolbar.Bounds().W) <= 1 {
		return toolbar.bar.Handle(ctx, evt)
	}
	if evt.IsKey() {
		if evt.Key.Code == tea.KeyDown || evt.Key.Code == tea.KeyTab || evt.Key.Code == tea.KeyEnter {
			if len(toolbar.buttons) > 0 {
				toolbar.owner.focusID(toolbar.buttons[0].ID())
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return ui.EventResult{}
}

type reviewButton struct {
	*ui.Button
	toolbar *reviewToolbar
}

func (button *reviewButton) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		direction := 0
		switch evt.Key.Code {
		case tea.KeyLeft, tea.KeyUp:
			direction = -1
		case tea.KeyRight, tea.KeyDown, tea.KeyTab:
			direction = 1
			if evt.Key.Mod.Contains(tea.ModShift) {
				direction = -1
			}
		}
		if direction != 0 {
			buttons := button.toolbar.buttons
			for position, candidate := range buttons {
				if candidate != button {
					continue
				}
				next := position + direction
				id := button.toolbar.owner.documentID()
				if next >= 0 && next < len(buttons) {
					id = buttons[next].ID()
				}
				button.toolbar.owner.focusID(id)
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
			}
		}
	}
	return button.Button.Handle(ctx, evt)
}

func (toolbar *reviewToolbar) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Git review actions"}
}

func (screen *Screen) syncToolbar() {
	specs := make([]ui.ActionBarButton, 0, 10)
	add := func(label, action string, enabled bool) {
		specs = append(specs, ui.ActionBarButton{Label: label, Action: action, Enabled: enabled, Variant: ui.VariantSecondary})
	}
	ready := screen.haveComparison && !screen.loading && screen.errText == ""
	switch screen.mode {
	case ModeChanges:
		if screen.commitPage {
			add("Commit staged", ActionCommit, screen.canCommit())
			add("Review again", ActionReviewCommit, !screen.loading)
		} else {
			if screen.comparison.Area == workspace.GitAreaStaged {
				add("Unstage file", ActionUnstage, ready && !screen.comparison.Historical && screen.comparison.Fingerprint != "" && screen.comparison.OriginalPath == "")
			} else {
				add("Stage file", ActionStage, ready && !screen.comparison.Historical && screen.comparison.Area != workspace.GitAreaConflict && screen.comparison.Fingerprint != "" && screen.comparison.OriginalPath == "")
			}
			add("Review commit", ActionReviewCommit, !screen.loading)
		}
		add("Open file", ActionOpenFile, ready && !screen.comparison.Deleted)
		add("Files", "gitreview.navigator", !screen.commitPage)
	case ModeBranches:
		branch := screen.selectedBranch()
		add("Checkout", ActionCheckout, branch.Name != "" && !branch.Current && !screen.loading)
	default:
		add("Inspect", ActionInspectCommit, !screen.loading)
	}
	if screen.mode != ModeBranches {
		label := "Side by side"
		if screen.preferred == SideBySide {
			label = "Unified"
		}
		add(label, "gitreview.presentation", ready && !screen.comparison.Binary)
		add("Find", "gitreview.search", ready && !screen.comparison.Binary)
		add("Previous", "gitreview.previous-hunk", screen.viewer.HunkCount() > 0)
		add("Next", "gitreview.next-hunk", screen.viewer.HunkCount() > 0)
		add("Copy path", "gitreview.copy-path", screen.haveComparison)
	}
	add("Refresh", ActionRefresh, !screen.loading)
	add("Return", ActionReturn, true)
	screen.toolbar.setButtons(specs)
}

type reviewSearch struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

func (search *reviewSearch) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	search.SetBounds(bounds)
	search.field.Layout(ctx, ui.Rect{W: bounds.W, H: bounds.H})
}

func (search *reviewSearch) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	search.field.Paint(buf, originX+search.Bounds().X, originY+search.Bounds().Y, ctx)
}

func (search *reviewSearch) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyEnter:
			search.owner.nextMatch(evt.Key.Mod.Contains(tea.ModShift))
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case tea.KeyTab:
			search.owner.focusID(search.owner.documentID())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return search.field.Handle(ctx, evt)
}

func (search *reviewSearch) TextEditor() ui.TextClipboardTarget { return search.field.TextEditor() }
func (search *reviewSearch) Semantics() ui.Semantics            { return search.field.Semantics() }

type commitDraft struct {
	ui.Base
	area  *ui.TextArea
	owner *Screen
}

func (draft *commitDraft) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	draft.SetBounds(bounds)
	draft.area.Layout(ctx, ui.Rect{W: bounds.W, H: bounds.H})
}

func (draft *commitDraft) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	draft.area.Paint(buf, originX+draft.Bounds().X, originY+draft.Bounds().Y, ctx)
}

func (draft *commitDraft) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		draft.owner.focusID(ToolbarID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	result := draft.area.Handle(ctx, evt)
	if draft.owner.errText == "Enter a commit message; the draft remains editable" && strings.TrimSpace(draft.area.Value()) != "" {
		draft.owner.errText = ""
	}
	draft.owner.syncToolbar()
	return result
}

func (draft *commitDraft) TextEditor() ui.TextClipboardTarget { return draft.area }
func (draft *commitDraft) CancelPointerCapture()              { draft.area.CancelPointerCapture() }
func (draft *commitDraft) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleTextInput, Label: "Commit message", Value: draft.area.Value()}
}

type reviewList struct {
	ui.Base
	list      *ui.List
	owner     *Screen
	lastClick string
	lastTime  time.Time
}

func newReviewList(owner *Screen) *reviewList {
	list := ui.NewList(ListID, owner.tokens)
	list.SetActivateAction("gitreview.activate")
	list.SetDefaultRowBackground(ui.ColorBg)
	return &reviewList{Base: ui.NewBase(ListID), list: list, owner: owner}
}

func (list *reviewList) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	list.SetBounds(bounds)
	list.list.Layout(ctx, ui.Rect{W: bounds.W, H: bounds.H})
}

func (list *reviewList) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	list.list.Paint(buf, originX+list.Bounds().X, originY+list.Bounds().Y, ctx)
}

func (list *reviewList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		id := list.owner.documentID()
		if list.owner.commitPage && list.owner.mode == ModeChanges {
			id = DraftID
		}
		list.owner.focusID(id)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	result := list.list.Handle(ctx, evt)
	if row, ok := list.list.Selected(); ok {
		list.owner.inspectRow(row)
		if evt.Kind == ui.EventMouseClick && result.Action != nil {
			now := time.Now()
			double := list.lastClick == row.ID && now.Sub(list.lastTime) <= 400*time.Millisecond
			list.lastClick, list.lastTime = row.ID, now
			if !double {
				result.Action = nil
			}
		}
	}
	return result
}

func (list *reviewList) Semantics() ui.Semantics { return list.list.Semantics() }

func (screen *Screen) syncList() {
	rows := make([]ui.ListItem, 0)
	switch screen.mode {
	case ModeChanges:
		if screen.commitPage {
			for _, comparison := range screen.commitReview.Comparisons {
				rows = append(rows, ui.ListItem{ID: "staged:" + comparison.Path, Label: comparison.Path, Payload: comparison})
			}
		} else {
			for _, change := range screen.snapshot.Changes {
				rows = append(rows, ui.ListItem{ID: fmt.Sprintf("change:%d:%s", change.Area, change.Path), Label: change.Path, Detail: change.Code, Payload: change})
			}
		}
	case ModeHistory:
		for _, commit := range screen.snapshot.RecentCommits {
			rows = append(rows, ui.ListItem{ID: "commit:" + commit.Hash, Label: commit.Subject, Detail: commit.ShortHash, Payload: commit})
		}
		if screen.evidence.Commit.Hash != "" {
			rows = append(rows, ui.ListItem{ID: "history-files", Label: "Commit changes", Header: true})
			for _, comparison := range screen.evidence.Comparisons {
				rows = append(rows, ui.ListItem{ID: "evidence:" + comparison.Path, Label: comparison.Path, Payload: comparison})
			}
		}
	case ModeBranches:
		for _, branch := range screen.snapshot.Branches {
			detail := "local"
			if branch.Remote {
				detail = "remote"
			} else if branch.Current {
				detail = "current"
			}
			rows = append(rows, ui.ListItem{ID: "branch:" + branch.Name, Label: branch.Name, Detail: detail, Payload: branch})
		}
	}
	if len(rows) == 0 {
		label := "No changes"
		if screen.mode == ModeHistory {
			label = "No commits"
		} else if screen.mode == ModeBranches {
			label = "No branches"
		} else if screen.commitPage {
			label = "No staged files; stage a reviewed file first"
		}
		rows = append(rows, ui.ListItem{ID: "empty", Label: label, Disabled: true})
	}
	screen.list.list.SetItemsStable(rows)
	if row, ok := screen.list.list.Selected(); ok {
		screen.inspectRow(row)
	}
}

func (screen *Screen) inspectRow(row ui.ListItem) {
	switch value := row.Payload.(type) {
	case workspace.GitBranchSummary:
		kind := "Local branch"
		if value.Remote {
			kind = "Remote-tracking branch"
		}
		text := value.Name + "\n\n" + kind
		if value.Current {
			text += " · current branch"
		}
		text += "\nSelecting this branch does not check it out."
		if screen.detail.Value() != text {
			screen.detail.SetValue(text)
			screen.detail.SetCursorFlat(0)
		}
	case workspace.GitCommitSummary:
		if screen.evidence.Commit.Hash == "" {
			screen.detail.SetValue(value.Hash + "\n" + value.Author + " · " + value.When.Format("2006-01-02 15:04") + "\n\n" + value.Subject)
			screen.detail.SetCursorFlat(0)
		}
	}
	screen.syncToolbar()
}

func (screen *Screen) activateRow(row ui.ListItem, result *ui.EventResult) {
	switch value := row.Payload.(type) {
	case workspace.GitChange:
		result.Action = &ui.Action{ID: ActionOpenChange, Payload: value}
	case workspace.GitComparison:
		if screen.commitPage {
			screen.reviewed[value.Path] = true
		}
		screen.setComparison(value)
	case workspace.GitCommitSummary:
		result.Action = &ui.Action{ID: ActionInspectCommit, Payload: InspectCommitRequest{Commit: value}}
	case workspace.GitBranchSummary:
		screen.inspectRow(row)
	}
}

func (screen *Screen) selectedBranch() workspace.GitBranchSummary {
	row, ok := screen.list.list.Selected()
	if !ok {
		return workspace.GitBranchSummary{}
	}
	branch, _ := row.Payload.(workspace.GitBranchSummary)
	return branch
}

func (screen *Screen) navigateFile(direction int, result *ui.EventResult) {
	rows := screen.list.list.Items()
	if len(rows) == 0 {
		return
	}
	index := screen.list.list.SelectedIndex()
	for count := 0; count < len(rows); count++ {
		index = (index + direction + len(rows)) % len(rows)
		if rows[index].Selectable() {
			screen.list.list.SelectIndex(index)
			screen.activateRow(rows[index], result)
			return
		}
	}
}
