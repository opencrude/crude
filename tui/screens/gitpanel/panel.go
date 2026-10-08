// Package gitpanel is the Git destination of the workspace bottom dock: a
// change list with one-click staging, a commit box, a live hunk preview of the
// selected file, history and branches. Repository work runs in the owner; the
// panel paints snapshots and emits typed intents.
package gitpanel

import (
	"fmt"
	"strings"
	"time"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/gitdiff"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID    ui.ComponentID = "gitpanel"
	TabsID      ui.ComponentID = "gitpanel/tabs"
	ListID      ui.ComponentID = "gitpanel/changes"
	MessageID   ui.ComponentID = "gitpanel/message"
	CommitID    ui.ComponentID = "gitpanel/commit"
	ReviewID    ui.ComponentID = "gitpanel/review-all"
	PreviewID   ui.ComponentID = "gitpanel/preview"
	HistoryID   ui.ComponentID = "gitpanel/history"
	BranchesID  ui.ComponentID = "gitpanel/branches"
	CommitDocID ui.ComponentID = "gitpanel/commit-preview"
	GenerateID  ui.ComponentID = "gitpanel/generate"
	DividerID   ui.ComponentID = "gitpanel/divider"
	WritingID   ui.ComponentID = "gitpanel/writing"
)

const (
	ActionRefresh  = "gitpanel.refresh"
	ActionStage    = "gitpanel.stage"   // payload []workspace.GitChange
	ActionUnstage  = "gitpanel.unstage" // payload []workspace.GitChange
	ActionDiscard  = "gitpanel.discard" // payload workspace.GitChange
	ActionOpenFile = "gitpanel.open-file"
	ActionReview   = "gitpanel.review" // payload string change key; "" = all
	ActionCommit   = "gitpanel.commit" // payload CommitRequest
	// ActionGenerate asks Aeron to draft the message (payload GenerateRequest);
	// ActionStopGenerate cancels a running draft.
	ActionGenerate     = "gitpanel.generate"
	ActionStopGenerate = "gitpanel.generate-stop"
	ActionInspect      = "gitpanel.inspect"
	ActionOpenCommit   = "gitpanel.open-commit"
	ActionCheckout     = "gitpanel.checkout" // payload workspace.GitBranchSummary
	ActionBranchMenu   = "gitpanel.branch-menu"

	actionSelect       = "gitpanel.select"
	actionBranchSelect = "gitpanel.branch-select"
)

// Mode is the panel tab.
type Mode string

const (
	ModeChanges  Mode = "changes"
	ModeHistory  Mode = "history"
	ModeBranches Mode = "branches"
)

// CommitRequest is the exact set the user saw when pressing Commit. StageAll
// commits every listed change; Paths is then the full listed set.
type CommitRequest struct {
	Message  string
	Paths    []string
	StageAll bool
	Changes  []workspace.GitChange
}

// wideWidth is the narrowest panel that shows the list and preview side by side.
const wideWidth = 96

// Panel is the Git dock destination.
type Panel struct {
	ui.Base
	th              *theme.Theme
	tokens          *ui.TokenSet
	tabs            *ui.Tabs
	list            *changeList
	message         *ui.TextArea
	messageBox      *messageBox
	commit          *ui.Button
	commitLabel     string
	reviewAll       *ui.Button
	generate        *ui.Button
	writingRule     *ui.ProgressRule
	writing         bool
	draftBackup     string
	split           *ui.SplitModel
	splitter        *ui.SplitDivider
	preview         *gitdiff.Document
	history         *ui.List
	historyBox      *selectList
	historyBar      *ui.ScrollbarControl
	commitDoc       *gitdiff.Document
	branches        *ui.List
	branchesBox     *selectList
	focus           func(ui.ComponentID) bool
	focusedID       func() ui.ComponentID
	mode            Mode
	snapshot        workspace.GitSnapshot
	haveSnapshot    bool
	files           map[string]gitdiff.File
	ordered         []gitdiff.File
	preparedOnce    bool
	evidence        workspace.GitCommitEvidence
	evidenceLoading string
	loading         bool
	busy            string
	errText         string
	dockHeaderInset int
	wide            bool
	statusRow       ui.Rect
	divider         int
}

func New(th *theme.Theme) *Panel {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	panel := &Panel{Base: ui.NewBase(ScreenID), th: th, tokens: tokens, mode: ModeChanges, files: make(map[string]gitdiff.File), divider: -1}
	panel.tabs = ui.NewTabs(TabsID, tokens)
	panel.tabs.SetChrome(ui.TabsShell)
	panel.tabs.SetRuleFullBleed(true)
	panel.list = newChangeList(panel)
	panel.message = ui.NewTextArea(MessageID, tokens, true)
	panel.message.SetPlaceholder("Commit message")
	panel.message.SetSubmitAction(ActionCommit)
	panel.messageBox = &messageBox{Base: ui.NewBase(MessageID), area: panel.message, owner: panel}
	panel.commit = ui.NewButton(CommitID, "Commit", ActionCommit)
	panel.commit.SetVariant(ui.VariantPrimary)
	panel.reviewAll = ui.NewButton(ReviewID, "Review all", ActionReview)
	panel.reviewAll.SetVariant(ui.VariantSecondary)
	panel.generate = ui.NewButton(GenerateID, "Generate", ActionGenerate)
	panel.generate.SetVariant(ui.VariantSecondary)
	panel.writingRule = ui.NewProgressRule(WritingID, tokens)
	panel.split = ui.NewSplitModel(ui.Horizontal)
	panel.split.SetMinSizes(34, 30)
	panel.split.SetFraction(0.36)
	panel.splitter = ui.NewSplitDivider(DividerID, panel.split)
	panel.splitter.SetColorRole(ui.ColorBorder)
	panel.splitter.SetFill(true)
	panel.preview = gitdiff.New(PreviewID, tokens)
	panel.preview.SetEmptyText("Select a change to preview it")
	panel.history = ui.NewList(HistoryID, tokens)
	panel.history.SetActivateAction(ActionOpenCommit)
	panel.history.SetDefaultRowBackground(ui.ColorBg)
	panel.historyBox = &selectList{list: panel.history, onMove: func() *ui.Action {
		if commit, ok := panel.SelectedCommit(); ok {
			return &ui.Action{ID: ActionInspect, Payload: commit}
		}
		return nil
	}}
	panel.historyBar = ui.NewScrollbarControl(ui.NewID(HistoryID, "scrollbar"), panel.history.Scroll(), tokens)
	panel.commitDoc = gitdiff.New(CommitDocID, tokens)
	panel.commitDoc.SetReadOnly(true)
	panel.commitDoc.SetEmptyText("Select a commit to see its changes")
	panel.branches = ui.NewList(BranchesID, tokens)
	panel.branches.SetActivateAction(ActionCheckout)
	panel.branches.SetDefaultRowBackground(ui.ColorBg)
	panel.branchesBox = &selectList{list: panel.branches, onMove: func() *ui.Action { return &ui.Action{ID: actionBranchSelect} }}
	panel.SetTokens(tokens)
	panel.sync()
	return panel
}

// SetFocusHandler installs the host's focus request and focus query.
func (panel *Panel) SetFocusHandler(handler func(ui.ComponentID) bool, focused func() ui.ComponentID) {
	panel.focus, panel.focusedID = handler, focused
}

func (panel *Panel) focusID(id ui.ComponentID) {
	if panel.focus != nil {
		panel.focus(id)
	}
}

func (panel *Panel) SetDockHeaderInset(width int) { panel.dockHeaderInset = max(0, width) }

func (panel *Panel) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	panel.tokens = tokens
	panel.tabs.SetTokens(tokens)
	panel.list.setTokens(tokens)
	panel.message.SetTokens(tokens)
	panel.message.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
	panel.commit.SetTokens(tokens)
	panel.commit.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	for _, button := range []*ui.Button{panel.reviewAll, panel.generate} {
		button.SetTokens(tokens)
		button.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	}
	panel.writingRule.SetTokens(tokens)
	panel.splitter.SetTokens(tokens)
	panel.preview.SetTokens(tokens)
	panel.history.SetTokens(tokens)
	panel.historyBar.SetTokens(tokens)
	panel.commitDoc.SetTokens(tokens)
	panel.branches.SetTokens(tokens)
}

func (panel *Panel) SetTheme(th *theme.Theme) {
	if th != nil {
		panel.th = th
		panel.SetTokens(ui.NewTokenSet(th))
	}
}

func (panel *Panel) Mode() Mode { return panel.mode }

func (panel *Panel) SetMode(mode Mode) {
	if mode == ModeChanges || mode == ModeHistory || mode == ModeBranches {
		panel.mode = mode
		panel.sync()
	}
}

// PreferredFocus is where keyboard focus lands when the dock opens the panel.
func (panel *Panel) PreferredFocus() ui.ComponentID {
	switch panel.mode {
	case ModeHistory:
		return HistoryID
	case ModeBranches:
		return BranchesID
	}
	return ListID
}

// OwnsFocus reports whether id belongs to this panel's tree.
func OwnsFocus(id ui.ComponentID) bool {
	return id == ScreenID || strings.HasPrefix(string(id), string(ScreenID)+"/")
}

func (panel *Panel) Snapshot() workspace.GitSnapshot { return panel.snapshot }

func (panel *Panel) SetSnapshot(snapshot workspace.GitSnapshot) {
	panel.snapshot, panel.haveSnapshot = snapshot, true
	panel.loading = false
	panel.errText = ""
	panel.sync()
}

func (panel *Panel) SetLoading(loading bool) {
	panel.loading = loading
	if loading {
		panel.errText = ""
	}
}

// SetBusy labels a running mutation; empty clears it.
func (panel *Panel) SetBusy(label string) {
	panel.busy = label
	panel.syncCommit()
}

func (panel *Panel) SetError(err error) {
	panel.loading, panel.busy = false, ""
	panel.errText = ""
	if err != nil {
		panel.errText = err.Error()
	}
	panel.syncCommit()
}

func (panel *Panel) Message() string { return panel.message.Value() }
func (panel *Panel) SetMessage(text string) {
	panel.message.SetValue(text)
	panel.syncCommit()
}

// CompleteCommit clears the draft only if it is still the committed text.
func (panel *Panel) CompleteCommit(request CommitRequest) {
	if strings.TrimSpace(panel.message.Value()) == strings.TrimSpace(request.Message) {
		panel.message.SetValue("")
	}
	panel.busy = ""
	panel.syncCommit()
}

// SetPrepared installs the prepared comparison of every listed change.
func (panel *Panel) SetPrepared(files []gitdiff.File) {
	panel.ordered = files
	panel.files = make(map[string]gitdiff.File, len(files))
	for _, file := range files {
		panel.files[file.Key] = file
	}
	panel.preparedOnce = files != nil
	panel.syncPreview()
}

// PreparedFiles returns the prepared set keyed by change identity.
func (panel *Panel) PreparedFiles() map[string]gitdiff.File { return panel.files }

// PreparedList returns the prepared set in display order.
func (panel *Panel) PreparedList() []gitdiff.File { return panel.ordered }

// SelectChange selects the change with this key, if it is listed.
func (panel *Panel) SelectChange(key string) bool {
	if panel.list.selectKey(key) {
		panel.syncPreview()
		return true
	}
	return false
}

func (panel *Panel) SelectedChange() (workspace.GitChange, bool) { return panel.list.selectedChange() }

// SetCommitEvidence installs a loaded commit for the History preview.
func (panel *Panel) SetCommitEvidence(evidence workspace.GitCommitEvidence, files []gitdiff.File) {
	if panel.evidenceLoading != "" && evidence.Commit.Hash != panel.evidenceLoading {
		return
	}
	panel.evidence, panel.evidenceLoading = evidence, ""
	panel.commitDoc.SetFiles(files)
}

// SelectedCommit is the highlighted History row.
func (panel *Panel) SelectedCommit() (workspace.GitCommitSummary, bool) {
	row, ok := panel.history.Selected()
	if !ok {
		return workspace.GitCommitSummary{}, false
	}
	commit, ok := row.Payload.(workspace.GitCommitSummary)
	return commit, ok
}

// RequestEvidence marks hash as the History preview being loaded.
func (panel *Panel) RequestEvidence(hash string) { panel.evidenceLoading = hash }

func (panel *Panel) stagedCount() int { return len(panel.snapshot.StagedPaths()) }

func (panel *Panel) changeCount() int {
	paths := make(map[string]bool)
	for _, change := range panel.snapshot.Changes {
		paths[change.Path] = true
	}
	return len(paths)
}

func (panel *Panel) sync() {
	changes := panel.changeCount()
	label := "Changes"
	if changes > 0 {
		label = fmt.Sprintf("Changes %d", changes)
	}
	panel.tabs.SetItems([]ui.TabItem{
		{ID: string(ModeChanges), Label: label},
		{ID: string(ModeHistory), Label: "History"},
		{ID: string(ModeBranches), Label: "Branches"},
	}, string(panel.mode))
	panel.list.setChanges(panel.snapshot.Changes)
	panel.syncHistory()
	panel.syncBranches()
	panel.syncCommit()
	panel.syncPreview()
}

func (panel *Panel) syncHistory() {
	rows := make([]ui.ListItem, 0, len(panel.snapshot.RecentCommits))
	for _, commit := range panel.snapshot.RecentCommits {
		rows = append(rows, ui.ListItem{ID: commit.Hash, Label: commit.Subject, Detail: commit.ShortHash + "  " + relativeTime(commit.When), Payload: commit})
	}
	if len(rows) == 0 {
		rows = append(rows, ui.ListItem{ID: "empty", Label: "No commits yet", Disabled: true})
	}
	panel.history.SetItemsStable(rows)
}

func (panel *Panel) syncBranches() {
	var local, remote []ui.ListItem
	for _, branch := range panel.snapshot.Branches {
		item := ui.ListItem{ID: "branch:" + branch.Name, Label: "  " + branch.Name, Payload: branch}
		if branch.Current {
			item.Label = "● " + branch.Name
			item.Detail = "current"
		}
		if branch.Remote {
			remote = append(remote, item)
		} else {
			local = append(local, item)
		}
	}
	rows := make([]ui.ListItem, 0, len(local)+len(remote)+2)
	if len(local) > 0 {
		rows = append(rows, ui.ListItem{ID: "local", Label: " Local", Header: true})
		rows = append(rows, local...)
	}
	if len(remote) > 0 {
		rows = append(rows, ui.ListItem{ID: "remote", Label: " Remote", Header: true})
		rows = append(rows, remote...)
	}
	if len(rows) == 0 {
		rows = append(rows, ui.ListItem{ID: "empty", Label: "No branches", Disabled: true})
	}
	panel.branches.SetItemsStable(rows)
}

func (panel *Panel) syncCommit() {
	staged, changes := panel.stagedCount(), panel.changeCount()
	label := "Commit"
	switch {
	case staged > 0:
		label = fmt.Sprintf("✓ Commit %d", staged)
	case changes > 0:
		label = fmt.Sprintf("✓ Commit all %d", changes)
	}
	panel.commit.SetLabel(label)
	panel.commitLabel = label
	panel.commit.SetDisabled(!panel.canCommit())
	if panel.writing {
		panel.generate.SetLabel("Stop")
		panel.generate.SetActionID(ActionStopGenerate)
		panel.generate.SetDisabled(false)
	} else {
		panel.generate.SetLabel("Generate")
		panel.generate.SetActionID(ActionGenerate)
		panel.generate.SetDisabled(changes == 0 || panel.busy != "" || panel.hasConflicts())
	}
	placeholder := "Commit message"
	if staged == 0 && changes > 0 {
		placeholder = "Message · nothing staged, so Commit stages all"
	}
	panel.message.SetPlaceholder(placeholder)
}

func (panel *Panel) canCommit() bool {
	return panel.busy == "" && !panel.writing && strings.TrimSpace(panel.message.Value()) != "" && panel.changeCount() > 0 && !panel.hasConflicts()
}

func (panel *Panel) hasConflicts() bool {
	for _, change := range panel.snapshot.Changes {
		if change.Area == workspace.GitAreaConflict {
			return true
		}
	}
	return false
}

func (panel *Panel) commitRequest() (CommitRequest, bool) {
	if !panel.canCommit() {
		return CommitRequest{}, false
	}
	request := CommitRequest{Message: panel.message.Value(), Paths: panel.snapshot.StagedPaths()}
	if len(request.Paths) == 0 {
		request.StageAll = true
		request.Changes = append([]workspace.GitChange(nil), panel.snapshot.Changes...)
		seen := make(map[string]bool)
		for _, change := range panel.snapshot.Changes {
			if !seen[change.Path] {
				seen[change.Path] = true
				request.Paths = append(request.Paths, change.Path)
			}
		}
	}
	return request, true
}

func (panel *Panel) syncPreview() {
	change, ok := panel.list.selectedChange()
	if !ok {
		panel.preview.SetFiles(nil)
		return
	}
	file, ok := panel.files[gitdiff.ChangeKey(change)]
	if !ok {
		panel.preview.SetEmptyText("Preparing diff…")
		if panel.preparedOnce {
			panel.preview.SetEmptyText("Diff unavailable · refresh with Ctrl+R")
		}
		panel.preview.SetFiles(nil)
		return
	}
	panel.preview.SetFiles([]gitdiff.File{file})
}

func (panel *Panel) emptyChangesText() (string, ui.SemanticColor) {
	switch {
	case panel.errText != "" && !panel.haveSnapshot:
		return panel.errText, ui.ColorError
	case !panel.haveSnapshot:
		return "Reading repository…", ui.ColorMuted
	case panel.snapshot.RepositoryRoot == "":
		return "This workspace is not a Git repository.", ui.ColorMuted
	}
	return "✓ Working tree clean", ui.ColorSuccess
}

func (panel *Panel) Children() []ui.Component {
	if panel.Bounds().Empty() {
		return nil
	}
	children := []ui.Component{panel.tabs}
	add := func(candidates ...ui.Component) {
		for _, child := range candidates {
			if !child.Bounds().Empty() {
				children = append(children, child)
			}
		}
	}
	switch panel.mode {
	case ModeChanges:
		add(panel.messageBox, panel.writingRule, panel.commit, panel.generate, panel.reviewAll, panel.list)
		if panel.wide {
			add(panel.splitter, panel.preview)
		}
	case ModeHistory:
		add(panel.historyBox, panel.historyBar)
		if panel.wide {
			add(panel.splitter, panel.commitDoc)
		}
	case ModeBranches:
		add(panel.branchesBox)
	}
	return children
}

func (panel *Panel) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

func (panel *Panel) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	panel.SetBounds(bounds)
	width, height := bounds.W, bounds.H
	hide := func(components ...ui.Component) {
		for _, component := range components {
			component.Layout(ctx, ui.Rect{})
		}
	}
	hide(panel.messageBox, panel.writingRule, panel.commit, panel.generate, panel.reviewAll, panel.list, panel.splitter, panel.preview, panel.historyBox, panel.historyBar, panel.commitDoc, panel.branchesBox)
	panel.tabs.SetRuleBleed(0, width)
	panel.tabs.Layout(ctx, ui.Rect{W: max(0, width-panel.dockHeaderInset), H: min(ui.StripHeight, height)})
	top := min(ui.StripHeight, height)
	bodyH := max(0, height-top)
	panel.wide = width >= wideWidth && bodyH >= 4
	leftW := width
	panel.divider = -1
	if panel.wide && panel.mode != ModeBranches {
		first, gap, _ := panel.split.Resolve(width)
		leftW = max(34, min(first, width-30))
		panel.divider = leftW
		panel.splitter.SetCurrentFirst(leftW)
		panel.splitter.Layout(ctx, ui.Rect{X: leftW, Y: top, W: gap, H: bodyH})
	}
	right := ui.Rect{X: leftW + 1, Y: top, W: max(0, width-leftW-1), H: bodyH}
	// Row one of the body is the repository status line in every mode.
	panel.statusRow = ui.Rect{X: 1, Y: top, W: max(0, leftW-2), H: min(1, bodyH)}
	y := top + panel.statusRow.H
	switch panel.mode {
	case ModeChanges:
		inner := ui.Rect{X: 1, W: max(0, leftW-2)}
		messageH := 1
		switch {
		case bodyH >= 16:
			messageH = 3
		case bodyH >= 11:
			messageH = 2
		}
		if bodyH < 6 {
			messageH = 0
		}
		if messageH > 0 {
			textH := messageH
			if panel.writing && messageH >= 2 {
				textH-- // indeterminate ink motion under the draft while Aeron writes
				panel.writingRule.Layout(ctx, ui.Rect{X: inner.X, Y: y + textH, W: inner.W, H: 1})
			}
			panel.messageBox.Layout(ctx, ui.Rect{X: inner.X, Y: y, W: inner.W, H: textH})
			y += messageH
			x := inner.X
			for _, button := range []*ui.Button{panel.commit, panel.generate, panel.reviewAll} {
				if button == panel.reviewAll && len(panel.snapshot.Changes) == 0 {
					continue
				}
				w := button.Measure(ctx, ui.Loose(40, 1)).W
				if x+w > inner.X+inner.W {
					break
				}
				button.Layout(ctx, ui.Rect{X: x, Y: y, W: w, H: 1})
				x += w + 1
			}
			y++
		}
		panel.list.Layout(ctx, ui.Rect{X: 0, Y: y, W: leftW, H: max(0, height-y)})
		if panel.wide {
			panel.preview.Layout(ctx, right)
		}
	case ModeHistory:
		listH := max(0, height-y)
		barW := 0
		if panel.historyBar.NeededForViewport(listH) {
			barW = ui.ScrollbarWidth
		}
		panel.historyBox.Layout(ctx, ui.Rect{X: 0, Y: y, W: max(0, leftW-barW), H: listH})
		panel.historyBar.Layout(ctx, ui.Rect{X: leftW - barW, Y: y, W: barW, H: listH})
		if panel.wide {
			docY := right.Y + 2
			panel.commitDoc.Layout(ctx, ui.Rect{X: right.X, Y: docY, W: right.W, H: max(0, height-docY)})
		}
	case ModeBranches:
		panel.divider = -1
		panel.branchesBox.Layout(ctx, ui.Rect{X: 0, Y: y, W: width, H: max(0, height-y)})
	}
}

func (panel *Panel) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	bounds := panel.Bounds()
	tokens := panel.tokens
	bg := tokens.Color(ui.ColorBg)
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(bg))
	x, y := originX+bounds.X, originY+bounds.Y
	panel.writingRule.SetFrame(ctx.Frame)
	panel.paintStatus(buf, x, y)
	if panel.mode == ModeHistory && panel.wide {
		panel.paintCommitHeader(buf, x+panel.divider+1, y+min(ui.StripHeight, bounds.H), max(0, bounds.W-panel.divider-1))
	}
}

// paintStatus draws branch identity and tracking, or the running operation.
func (panel *Panel) paintStatus(buf *surface.CellBuffer, x, y int) {
	row := panel.statusRow
	if row.Empty() {
		return
	}
	tokens := panel.tokens
	bg := tokens.Color(ui.ColorBg)
	col := x + row.X
	end := col + row.W
	write := func(text string, role ui.SemanticColor, attr cell.Attrs) {
		if col >= end || text == "" {
			return
		}
		text = rendering.TruncateVisual(text, end-col, "…")
		buf.WriteString(col, y+row.Y, text, tokens.Color(role), bg, attr)
		col += rendering.VisibleWidth(text)
	}
	if panel.errText != "" {
		write("✕ "+panel.errText, ui.ColorError, 0)
		return
	}
	branch := panel.snapshot.Branch
	if branch == "" {
		branch = "—"
	}
	write(gitdiff.BranchIcon(panelchrome.NerdFont(panel.th))+" ", ui.ColorAccent, 0)
	write(branch, ui.ColorFg, cell.AttrBold)
	if panel.snapshot.Ahead > 0 {
		write(fmt.Sprintf("  ↑%d", panel.snapshot.Ahead), ui.ColorSuccess, 0)
	}
	if panel.snapshot.Behind > 0 {
		write(fmt.Sprintf("  ↓%d", panel.snapshot.Behind), ui.ColorWarning, 0)
	}
	switch {
	case panel.writing:
		write("  · Aeron is writing…", ui.ColorAccent, 0)
	case panel.busy != "":
		write("  · "+panel.busy+"…", ui.ColorAccent, 0)
	case panel.loading:
		write("  · refreshing…", ui.ColorMuted, 0)
	case panel.snapshot.Upstream != "":
		write("  → "+panel.snapshot.Upstream, ui.ColorMuted, 0)
	}
}

func (panel *Panel) paintCommitHeader(buf *surface.CellBuffer, x, y, width int) {
	tokens := panel.tokens
	bg := tokens.Color(ui.ColorBg)
	commit, ok := panel.SelectedCommit()
	if !ok || width <= 2 {
		return
	}
	line := commit.ShortHash + "  " + commit.Author + "  " + commit.When.Format("2006-01-02 15:04")
	buf.WriteString(x+1, y, rendering.TruncateVisual(line, width-2, "…"), tokens.Color(ui.ColorMuted), bg, 0)
	subject := commit.Subject
	if panel.evidenceLoading == commit.Hash {
		subject += "  · loading…"
	}
	buf.WriteString(x+1, y+1, rendering.TruncateVisual(subject, width-2, "…"), tokens.Color(ui.ColorFg), bg, cell.AttrBold)
}

func (panel *Panel) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.String() {
	case "ctrl+r":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionRefresh}}
	case "tab":
		panel.cycleFocus(1)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "shift+tab":
		panel.cycleFocus(-1)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "c":
		if panel.mode == ModeChanges && !panel.messageBox.Bounds().Empty() {
			panel.focusID(MessageID)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	case "1", "2", "3":
		panel.SetMode([]Mode{ModeChanges, ModeHistory, ModeBranches}[evt.Key.String()[0]-'1'])
		panel.focusID(panel.PreferredFocus())
		return ui.EventResult{Handled: true, Action: panel.modeAction(), Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (panel *Panel) modeAction() *ui.Action {
	if panel.mode == ModeHistory {
		if commit, ok := panel.SelectedCommit(); ok && panel.evidence.Commit.Hash != commit.Hash {
			return &ui.Action{ID: ActionInspect, Payload: commit}
		}
	}
	return nil
}

// cycleFocus walks the panel's interactive regions in reading order.
func (panel *Panel) cycleFocus(direction int) {
	var order []ui.ComponentID
	switch panel.mode {
	case ModeChanges:
		if !panel.messageBox.Bounds().Empty() {
			order = append(order, MessageID, CommitID)
		}
		order = append(order, ListID)
		if panel.wide {
			order = append(order, PreviewID)
		}
	case ModeHistory:
		order = append(order, HistoryID)
		if panel.wide {
			order = append(order, CommitDocID)
		}
	default:
		order = append(order, BranchesID)
	}
	order = append(order, TabsID)
	current := panel.focusedIndex(order)
	next := (current + direction + len(order)) % len(order)
	panel.focusID(order[next])
}

func (panel *Panel) focusedIndex(order []ui.ComponentID) int {
	if panel.focusedID == nil {
		return -1
	}
	focused := panel.focusedID()
	for index, id := range order {
		if id == focused {
			return index
		}
	}
	return -1
}

func (panel *Panel) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch action.ID {
	case "tabs.activate":
		mode, _ := action.Payload.(string)
		panel.SetMode(Mode(mode))
		result.Action = panel.modeAction()
		return result
	case "tabs.enter":
		panel.focusID(panel.PreferredFocus())
		result.Invalidate |= ui.InvalidateFocus
		return result
	case actionSelect:
		panel.syncPreview()
		return result
	case actionBranchSelect:
		return result
	case ui.ActionSplitResize:
		if resize, ok := action.Payload.(ui.SplitResize); ok && resize.ID == DividerID {
			return result
		}
		return ui.EventResult{Action: &action}
	case ActionGenerate:
		if panel.writing || panel.changeCount() == 0 {
			return result
		}
		result.Action = &ui.Action{ID: ActionGenerate, Payload: GenerateRequest{StageAll: panel.stagedCount() == 0}}
		return result
	case ActionStopGenerate:
		result.Action = &ui.Action{ID: ActionStopGenerate}
		return result
	case ActionCommit:
		request, ok := panel.commitRequest()
		if !ok {
			if strings.TrimSpace(panel.message.Value()) == "" {
				panel.focusID(MessageID)
				result.Invalidate |= ui.InvalidateFocus
			}
			return result
		}
		panel.busy = "Committing"
		panel.syncCommit()
		result.Action = &ui.Action{ID: ActionCommit, Payload: request}
		return result
	case ActionReview:
		key, _ := action.Payload.(string)
		result.Action = &ui.Action{ID: ActionReview, Payload: key}
		return result
	}
	if action.ID == ActionOpenCommit {
		if row, ok := panel.history.Selected(); ok {
			if commit, ok := row.Payload.(workspace.GitCommitSummary); ok {
				result.Action = &ui.Action{ID: ActionOpenCommit, Payload: commit}
			}
		}
		return result
	}
	if action.ID == ActionCheckout {
		if row, ok := panel.branches.Selected(); ok {
			if branch, ok := row.Payload.(workspace.GitBranchSummary); ok && !branch.Current {
				result.Action = &ui.Action{ID: ActionCheckout, Payload: branch}
			}
		}
		return result
	}
	if strings.HasPrefix(action.ID, "gitpanel.") || action.ID == gitdiff.ActionFile {
		result.Action = &action
		return result
	}
	return ui.EventResult{Action: &action}
}

func (panel *Panel) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Git", Value: panel.snapshot.Branch, Description: fmt.Sprintf("%d changed files, %d staged", panel.changeCount(), panel.stagedCount())}
}

func relativeTime(when time.Time) string {
	if when.IsZero() {
		return ""
	}
	elapsed := time.Since(when)
	switch {
	case elapsed < time.Minute:
		return "now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh", int(elapsed.Hours()))
	case elapsed < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(elapsed.Hours()/24))
	}
	return when.Format("Jan 2006")
}

// compile-time checks
var _ ui.Component = (*Panel)(nil)
var _ ui.Parent = (*Panel)(nil)
var _ ui.ActionHandler = (*Panel)(nil)

// ChangeListOffset is the change list's first painted row.
func (panel *Panel) ChangeListOffset() int { return panel.list.scroll.Offset() }

// GenerateRequest asks for a message describing what Commit would record:
// the staged set, or every listed change when nothing is staged.
type GenerateRequest struct {
	StageAll bool
}

// Writing reports whether Aeron is drafting the message.
func (panel *Panel) Writing() bool { return panel.writing }

// NeedsFrame keeps the writing indicator animated.
func (panel *Panel) NeedsFrame() bool { return panel.writing }

// BeginWriting clears the draft for streamed text, keeping the user's own
// draft to restore if the request fails or is stopped.
func (panel *Panel) BeginWriting() {
	panel.draftBackup = panel.message.Value()
	panel.writing = true
	panel.errText = ""
	panel.message.SetValue("")
	panel.message.SetReadOnly(true)
	panel.writingRule.SetActive(true)
	panel.syncCommit()
}

// AppendWriting shows streamed text as it arrives.
func (panel *Panel) AppendWriting(text string) {
	if !panel.writing {
		return
	}
	panel.message.SetValue(panel.message.Value() + text)
	panel.message.SetCursorFlat(len([]rune(panel.message.Value())))
}

// FinishWriting installs the final message, or restores the previous draft
// and reports why when err is set (a stop passes context.Canceled).
func (panel *Panel) FinishWriting(message string, err error) {
	if !panel.writing {
		return
	}
	panel.writing = false
	panel.message.SetReadOnly(false)
	panel.writingRule.SetActive(false)
	if err != nil {
		panel.message.SetValue(panel.draftBackup)
		panel.errText = err.Error()
	} else {
		panel.message.SetValue(message)
		panel.message.SetCursorFlat(len([]rune(message)))
	}
	panel.draftBackup = ""
	panel.syncCommit()
}
