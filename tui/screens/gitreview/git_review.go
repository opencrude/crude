package gitreview

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/diffview"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID    ui.ComponentID = "gitreview"
	TabsID      ui.ComponentID = "gitreview/tabs"
	HeaderID    ui.ComponentID = "gitreview/header"
	ToolbarID   ui.ComponentID = "gitreview/toolbar"
	SearchID    ui.ComponentID = "gitreview/search"
	ListID      ui.ComponentID = "gitreview/list"
	ScrollbarID ui.ComponentID = "gitreview/scrollbar"
	DraftID     ui.ComponentID = "gitreview/draft"
	DetailID    ui.ComponentID = "gitreview/detail"

	ActionOpenFile      = "gitreview.open-file"
	ActionOpenChange    = "gitreview.open-change"
	ActionStage         = "gitreview.stage"
	ActionUnstage       = "gitreview.unstage"
	ActionReviewCommit  = "gitreview.review-commit"
	ActionCommit        = "gitreview.commit"
	ActionCheckout      = "gitreview.checkout"
	ActionRefresh       = "gitreview.refresh"
	ActionReturn        = "gitreview.return"
	ActionMode          = "gitreview.mode"
	ActionInspectCommit = "gitreview.inspect-commit"
)

type Mode string

const (
	ModeChanges  Mode = "changes"
	ModeHistory  Mode = "history"
	ModeBranches Mode = "branches"
)

type Presentation = diffview.Presentation

const (
	Unified    Presentation = diffview.Unified
	SideBySide Presentation = diffview.SideBySide
)

type ComparisonRequest struct {
	Comparison workspace.GitComparison
}

type CommitRequest struct {
	Review  workspace.GitCommitReview
	Message string
}

type CheckoutRequest struct {
	RepositoryRoot string
	Head           string
	Branch         workspace.GitBranchSummary
}

type ModeRequest struct {
	Mode Mode
}

type InspectCommitRequest struct {
	Commit workspace.GitCommitSummary
}

type Screen struct {
	ui.Base
	th              *theme.Theme
	tokens          *ui.TokenSet
	tabs            *ui.Tabs
	header          *ui.TextArea
	toolbar         *reviewToolbar
	search          *reviewSearch
	list            *reviewList
	scrollbar       *ui.ScrollbarControl
	viewer          *diffview.Screen
	draft           *commitDraft
	detail          *ui.TextArea
	focus           func(ui.ComponentID) bool
	snapshot        workspace.GitSnapshot
	comparison      workspace.GitComparison
	haveComparison  bool
	mode            Mode
	preferred       Presentation
	presented       Presentation
	navigator       bool
	sidebar         bool
	searchOpen      bool
	loading         bool
	errText         string
	commitPage      bool
	commitReview    workspace.GitCommitReview
	commitStale     bool
	stagedSignature string
	reviewed        map[string]bool
	evidence        workspace.GitCommitEvidence
	prepared        map[string]PreparedComparison
	body            ui.Rect
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	screen := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens, mode: ModeChanges, preferred: Unified, presented: Unified, sidebar: true, reviewed: make(map[string]bool)}
	screen.tabs = ui.NewTabs(TabsID, tokens)
	screen.tabs.SetChrome(ui.TabsShell)
	screen.syncTabs()
	screen.header = ui.NewTextArea(HeaderID, tokens, true)
	screen.header.SetReadOnly(true)
	screen.header.SetBackgroundColor(tokens.Color(ui.ColorBg))
	screen.detail = ui.NewTextArea(DetailID, tokens, true)
	screen.detail.SetReadOnly(true)
	screen.detail.SetBackgroundColor(tokens.Color(ui.ColorBg))
	screen.toolbar = newReviewToolbar(ToolbarID, screen)
	screen.search = &reviewSearch{Base: ui.NewBase(SearchID), field: ui.NewFilterField(SearchID, tokens), owner: screen}
	screen.search.field.SetPlaceholder("Find in comparison")
	screen.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	screen.search.field.SetPromptPlacement(ui.PromptEnd)
	screen.search.field.SetSubmitAction("")
	screen.search.field.OnChanged(func(query string) { screen.applySearch(query) })
	screen.list = newReviewList(screen)
	screen.scrollbar = ui.NewScrollbarControl(ScrollbarID, screen.list.list.Scroll(), tokens)
	screen.scrollbar.SetSurfaceBackground(ui.ColorBg)
	screen.viewer = diffview.NewWithID(th, ui.NewID(ScreenID, "comparison"))
	screen.viewer.SetPresentation(Unified)
	screen.draft = &commitDraft{Base: ui.NewBase(DraftID), area: ui.NewTextArea(DraftID, tokens, true), owner: screen}
	screen.draft.area.SetPlaceholder("Commit subject\n\nDescribe the staged changes")
	screen.draft.area.SetSubmitAction(ActionCommit)
	screen.syncHeader()
	screen.syncList()
	screen.syncToolbar()
	return screen
}

func (screen *Screen) SetFocusHandler(handler func(ui.ComponentID) bool) {
	screen.focus = handler
	screen.viewer.SetFocusHandler(handler)
}

func (screen *Screen) focusID(id ui.ComponentID) {
	if screen.focus != nil {
		screen.focus(id)
	}
}

func (screen *Screen) Mode() Mode { return screen.mode }

func (screen *Screen) SetMode(mode Mode) {
	if mode != ModeChanges && mode != ModeHistory && mode != ModeBranches {
		return
	}
	screen.mode = mode
	screen.syncTabs()
	screen.syncList()
	screen.syncHeader()
	screen.syncToolbar()
}

func (screen *Screen) syncTabs() {
	screen.tabs.SetItems([]ui.TabItem{{ID: string(ModeChanges), Label: "Changes"}, {ID: string(ModeHistory), Label: "History"}, {ID: string(ModeBranches), Label: "Branches"}}, string(screen.mode))
}

func (screen *Screen) SetPresentation(presentation Presentation) {
	if presentation == Unified || presentation == SideBySide {
		screen.preferred = presentation
		screen.viewer.SetPresentation(presentation)
		screen.syncToolbar()
	}
}

func (screen *Screen) PreferredPresentation() Presentation { return screen.preferred }
func (screen *Screen) Presentation() Presentation          { return screen.presented }
func (screen *Screen) Comparison() workspace.GitComparison { return screen.comparison }

func (screen *Screen) SetComparison(prepared PreparedComparison) {
	screen.InstallComparisons([]PreparedComparison{prepared})
	screen.commitPage = false
	screen.SetMode(ModeChanges)
	screen.setComparison(prepared.Comparison)
}

func (screen *Screen) setComparison(comparison workspace.GitComparison) {
	changed := comparison != screen.comparison || !screen.haveComparison
	screen.comparison, screen.haveComparison = comparison, true
	screen.loading, screen.errText = false, ""
	if changed {
		if comparison.Binary {
			screen.viewer.SetPrepared(diffview.Prepared{})
			screen.syncHeader()
			screen.syncToolbar()
			return
		}
		prepared, exists := screen.prepared[comparison.Path+"\x00"+comparison.Fingerprint]
		if !exists {
			screen.SetError(errors.New("comparison presentation is unavailable; refresh the review"))
			return
		}
		screen.viewer.SetPrepared(prepared.View)
		screen.applySearch(screen.search.field.Value())
	}
	screen.syncHeader()
	screen.syncToolbar()
}

func (screen *Screen) SetSnapshot(snapshot workspace.GitSnapshot) {
	snapshot.Changes = append([]workspace.GitChange(nil), snapshot.Changes...)
	snapshot.RecentCommits = append([]workspace.GitCommitSummary(nil), snapshot.RecentCommits...)
	snapshot.Branches = append([]workspace.GitBranchSummary(nil), snapshot.Branches...)
	signature := stagedSnapshotSignature(snapshot)
	if screen.commitReview.Fingerprint != "" && screen.stagedSignature != "" && signature != screen.stagedSignature {
		screen.commitStale = true
	}
	screen.snapshot = snapshot
	screen.stagedSignature = signature
	screen.syncList()
	screen.syncHeader()
	screen.syncToolbar()
}

func stagedSnapshotSignature(snapshot workspace.GitSnapshot) string {
	var identity strings.Builder
	fmt.Fprintf(&identity, "%s\x00%s", snapshot.RepositoryRoot, snapshot.Head)
	for _, change := range snapshot.Changes {
		if change.Area == workspace.GitAreaStaged || change.Area == workspace.GitAreaConflict {
			fmt.Fprintf(&identity, "\x00%d:%s:%s", change.Area, change.Path, change.Code)
		}
	}
	return fmt.Sprintf("%x", sha256.Sum256([]byte(identity.String())))
}

func (screen *Screen) SetSidebarAvailable(available bool) {
	screen.sidebar = available
	screen.syncList()
	screen.syncToolbar()
}

func (screen *Screen) SetNavigatorVisible(visible bool) {
	screen.navigator = visible
	screen.syncList()
	screen.syncToolbar()
}

func (screen *Screen) SetLoading(loading bool) {
	screen.loading = loading
	if loading {
		screen.errText = ""
	}
	screen.syncToolbar()
}

func (screen *Screen) SetError(err error) {
	screen.loading = false
	if err == nil {
		screen.errText = ""
	} else {
		screen.errText = err.Error()
		if errors.Is(err, workspace.ErrGitReviewStale) {
			screen.commitStale = true
		}
	}
	screen.syncToolbar()
}

func (screen *Screen) SetCommitReview(review workspace.GitCommitReview) {
	review.Comparisons = append([]workspace.GitComparison(nil), review.Comparisons...)
	screen.commitReview = review
	screen.commitPage, screen.commitStale, screen.loading, screen.errText = true, false, false, ""
	screen.reviewed = make(map[string]bool)
	screen.SetMode(ModeChanges)
	screen.syncList()
	if len(review.Comparisons) > 0 {
		screen.reviewed[review.Comparisons[0].Path] = true
		screen.setComparison(review.Comparisons[0])
	}
	screen.syncHeader()
	screen.syncToolbar()
}

func (screen *Screen) CommitMessage() string { return screen.draft.area.Value() }
func (screen *Screen) SetCommitMessage(message string) {
	screen.draft.area.SetValue(message)
	screen.syncToolbar()
}

func (screen *Screen) ClearCommitMessage() {
	screen.draft.area.SetValue("")
	screen.syncToolbar()
}

func (screen *Screen) CompleteCommit(request CommitRequest) {
	if request.Message == screen.CommitMessage() {
		screen.ClearCommitMessage()
	}
	if request.Review.Fingerprint == screen.commitReview.Fingerprint {
		screen.commitReview = workspace.GitCommitReview{}
		screen.commitPage = false
		screen.commitStale = false
	}
	screen.syncList()
	screen.syncHeader()
	screen.syncToolbar()
}

func (screen *Screen) SetCommitEvidence(evidence workspace.GitCommitEvidence) {
	evidence.Comparisons = append([]workspace.GitComparison(nil), evidence.Comparisons...)
	screen.evidence = evidence
	screen.loading, screen.errText = false, ""
	screen.SetMode(ModeHistory)
	screen.detail.SetValue(evidence.Commit.Hash + "\n" + evidence.Commit.Author + " · " + evidence.Commit.When.Format("2006-01-02 15:04") + "\n\n" + evidence.Message)
	if len(evidence.Comparisons) > 0 {
		screen.setComparison(evidence.Comparisons[0])
	}
	screen.syncList()
	screen.syncToolbar()
}

func (screen *Screen) canCommit() bool {
	if screen.loading || screen.commitStale || screen.errText != "" || screen.commitReview.Fingerprint == "" || strings.TrimSpace(screen.CommitMessage()) == "" || len(screen.commitReview.Comparisons) == 0 {
		return false
	}
	for _, comparison := range screen.commitReview.Comparisons {
		if !screen.reviewed[comparison.Path] {
			return false
		}
	}
	return true
}

func (screen *Screen) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	screen.tokens = tokens
	screen.tabs.SetTokens(tokens)
	screen.header.SetTokens(tokens)
	screen.header.SetBackgroundColor(tokens.Color(ui.ColorBg))
	screen.detail.SetTokens(tokens)
	screen.detail.SetBackgroundColor(tokens.Color(ui.ColorBg))
	screen.search.field.SetTokens(tokens)
	screen.list.list.SetTokens(tokens)
	screen.scrollbar.SetTokens(tokens)
	screen.draft.area.SetTokens(tokens)
	screen.viewer.SetTokens(tokens)
	screen.toolbar.setTokens(tokens)
}

func (screen *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	screen.th = th
	screen.viewer.SetTheme(th)
	screen.SetTokens(ui.NewTokenSet(th))
}

func (screen *Screen) Children() []ui.Component {
	children := []ui.Component{screen.header, screen.tabs, screen.toolbar, screen.search}
	if screen.showsList() {
		children = append(children, screen.list, screen.scrollbar)
	}
	if screen.commitPage && screen.mode == ModeChanges {
		children = append(children, screen.draft)
	}
	if screen.mode == ModeBranches || screen.mode == ModeHistory && screen.evidence.Commit.Hash == "" {
		return append(children, screen.detail)
	}
	if screen.mode == ModeHistory && screen.evidence.Commit.Hash != "" {
		children = append(children, screen.detail)
	}
	if screen.haveComparison && !screen.comparison.Binary {
		if screen.comparisonState() == "" {
			children = append(children, screen.viewer)
		}
	}
	return children
}

func (screen *Screen) showsList() bool {
	return screen.mode != ModeChanges || screen.commitPage || screen.navigator || !screen.sidebar
}

func (screen *Screen) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	screen.SetBounds(bounds)
	screen.presented = Unified
	if screen.preferred == SideBySide && bounds.W >= 100 {
		screen.presented = SideBySide
	}
	pad := min(screen.tokens.PadLR(), bounds.W/2)
	innerW := max(0, bounds.W-2*pad)
	headerH := min(2, bounds.H)
	screen.header.Layout(ctx, ui.Rect{X: pad, W: innerW, H: headerH})
	tabH := min(ui.StripHeight, max(0, bounds.H-headerH))
	screen.tabs.Layout(ctx, ui.Rect{Y: headerH, W: bounds.W, H: tabH})
	y := headerH + tabH
	toolbarH := min(screen.toolbar.rows(innerW), max(0, bounds.H-y-5))
	screen.toolbar.Layout(ctx, ui.Rect{X: pad, Y: y, W: innerW, H: toolbarH})
	y += toolbarH
	if screen.searchOpen {
		screen.search.Layout(ctx, ui.Rect{X: pad, Y: y, W: innerW, H: min(1, max(0, bounds.H-y))})
		y += screen.search.Bounds().H
	} else {
		screen.search.Layout(ctx, ui.Rect{})
	}
	y++
	body := ui.Rect{X: pad, Y: min(y, bounds.H), W: innerW, H: max(0, bounds.H-y)}
	if screen.commitPage && screen.mode == ModeChanges {
		commitH := min(5, max(0, body.H-4))
		if innerW >= 80 {
			listW := innerW / 3
			screen.layoutList(ctx, ui.Rect{X: body.X, Y: body.Y, W: listW, H: commitH})
			screen.draft.Layout(ctx, ui.Rect{X: body.X + listW + 1, Y: body.Y, W: max(0, innerW-listW-1), H: commitH})
			body.Y += commitH
			body.H -= commitH
		} else {
			listH := min(3, max(0, body.H-7))
			screen.layoutList(ctx, ui.Rect{X: body.X, Y: body.Y, W: innerW, H: listH})
			draftH := min(3, max(0, body.H-listH-4))
			screen.draft.Layout(ctx, ui.Rect{X: body.X, Y: body.Y + listH, W: innerW, H: draftH})
			body.Y += listH + draftH
			body.H -= listH + draftH
		}
	} else if screen.showsList() {
		if innerW >= 90 {
			listW := min(36, innerW/3)
			screen.layoutList(ctx, ui.Rect{X: body.X, Y: body.Y, W: listW, H: body.H})
			body.X += listW + 1
			body.W -= listW + 1
		} else {
			listH := min(4, max(0, body.H-4))
			screen.layoutList(ctx, ui.Rect{X: body.X, Y: body.Y, W: innerW, H: listH})
			body.Y += listH
			body.H -= listH
		}
	}
	if screen.mode == ModeBranches || screen.mode == ModeHistory && screen.evidence.Commit.Hash == "" {
		screen.detail.Layout(ctx, body)
		return
	}
	if screen.mode == ModeHistory {
		detailH := min(3, max(0, body.H-4))
		screen.detail.Layout(ctx, ui.Rect{X: body.X, Y: body.Y, W: body.W, H: detailH})
		body.Y += detailH
		body.H -= detailH
	}
	screen.body = body
	screen.viewer.Layout(ctx, body)
	screen.presented = screen.viewer.Presentation()
}

func (screen *Screen) layoutList(ctx ui.LayoutContext, bounds ui.Rect) {
	barW := 0
	if screen.scrollbar.NeededForViewport(bounds.H) {
		barW = min(ui.ScrollbarWidth, bounds.W)
	}
	screen.list.Layout(ctx, ui.Rect{X: bounds.X, Y: bounds.Y, W: max(0, bounds.W-barW), H: bounds.H})
	screen.scrollbar.Layout(ctx, ui.Rect{X: bounds.X + bounds.W - barW, Y: bounds.Y, W: barW, H: bounds.H})
}

func (screen *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	if buf == nil || screen.tokens == nil {
		return
	}
	bounds := screen.Bounds()
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(screen.tokens.Color(ui.ColorBg)))
	statusY := screen.toolbar.Bounds().Y + screen.toolbar.Bounds().H
	if screen.searchOpen {
		statusY++
	}
	if statusY < bounds.H {
		text, role := screen.status()
		buf.WriteString(originX+bounds.X+screen.header.Bounds().X, originY+bounds.Y+statusY, rendering.TruncateLine(text, screen.header.Bounds().W), screen.tokens.Color(role), screen.tokens.Color(ui.ColorBg), 0)
	}
	if screen.mode == ModeBranches || screen.mode == ModeHistory && screen.evidence.Commit.Hash == "" {
		return
	}
	if text := screen.comparisonState(); text != "" && !screen.body.Empty() {
		for row, line := range rendering.WrapText(text, screen.body.W) {
			if row >= screen.body.H {
				break
			}
			buf.WriteString(originX+bounds.X+screen.body.X, originY+bounds.Y+screen.body.Y+row, line, screen.tokens.Color(ui.ColorMuted), screen.tokens.Color(ui.ColorBg), 0)
		}
	}
}

func (screen *Screen) syncHeader() {
	root := screen.snapshot.RepositoryRoot
	if root == "" {
		root = screen.comparison.RepositoryRoot
	}
	identity := filepath.Base(root)
	if identity == "." || identity == "" {
		identity = "Git review"
	}
	if screen.snapshot.Branch != "" {
		identity += " / " + screen.snapshot.Branch
	}
	if screen.haveComparison && screen.mode != ModeBranches {
		identity += "\n" + screen.comparison.Path
		if screen.comparison.OriginalPath != "" {
			identity += " ← " + screen.comparison.OriginalPath
		}
	} else {
		identity += "\n" + string(screen.mode)
	}
	if screen.header.Value() != identity {
		screen.header.SetValue(identity)
		screen.header.SetCursorFlat(0)
	}
}

func (screen *Screen) status() (string, ui.SemanticColor) {
	if screen.errText != "" {
		return screen.errText, ui.ColorError
	}
	if screen.loading {
		return "Loading Git evidence…", ui.ColorMuted
	}
	if screen.commitPage && screen.mode == ModeChanges {
		if screen.commitStale {
			return "Staged set changed · Review commit again; your message is preserved", ui.ColorWarning
		}
		remaining := 0
		for _, comparison := range screen.commitReview.Comparisons {
			if !screen.reviewed[comparison.Path] {
				remaining++
			}
		}
		if remaining > 0 {
			return fmt.Sprintf("Exact staged set · review %d remaining files before committing", remaining), ui.ColorMuted
		}
		return fmt.Sprintf("Exact staged set · %d files · commit includes index bytes only", len(screen.commitReview.Comparisons)), ui.ColorMuted
	}
	if screen.mode == ModeBranches {
		return "Select to inspect · Checkout is an explicit action", ui.ColorMuted
	}
	if screen.mode == ModeHistory && screen.evidence.Commit.Hash == "" {
		return "Select a commit, then Inspect to load its complete changes", ui.ColorMuted
	}
	if screen.haveComparison {
		if screen.searchOpen && screen.viewer.SearchStatus() != "" {
			return screen.viewer.SearchStatus(), ui.ColorMuted
		}
		return screen.comparison.BaseLabel + " → " + screen.comparison.TargetLabel + " · " + string(screen.presented), ui.ColorMuted
	}
	return "Select a change in Source Control to review it", ui.ColorMuted
}

func (screen *Screen) comparisonState() string {
	if screen.loading {
		return "Loading comparison; previous evidence is not a current mutation preview."
	}
	if screen.errText != "" {
		return "Comparison unavailable. Refresh to retry; no Git mutation was inferred from missing evidence."
	}
	if !screen.haveComparison {
		return "No comparison selected."
	}
	comparison := screen.comparison
	if comparison.Binary {
		return fmt.Sprintf("Binary file · %s → %s\n%d bytes → %d bytes. Text comparison is unavailable; Open file uses the working-tree version.", comparison.BaseLabel, comparison.TargetLabel, len(comparison.Before), len(comparison.After))
	}
	if comparison.Before == comparison.After {
		switch {
		case comparison.Deleted:
			return "Deleted empty file."
		case comparison.Untracked:
			return "Untracked empty file · not yet in the index."
		case comparison.BeforeMode != comparison.AfterMode:
			return fmt.Sprintf("File mode changed: %06o → %06o; text is unchanged.", comparison.BeforeMode, comparison.AfterMode)
		default:
			return "No textual differences between the compared versions."
		}
	}
	if screen.body.W < 10 || screen.body.H < 4 {
		return "Increase the review area to inspect this comparison."
	}
	return ""
}

func (screen *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Git review", Value: screen.comparison.Path, Description: screen.comparison.BaseLabel + " → " + screen.comparison.TargetLabel}
}

func (screen *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.String() {
	case "esc":
		if screen.searchOpen {
			screen.searchOpen = false
			screen.focusID(screen.documentID())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
		}
		if screen.commitPage {
			screen.commitPage = false
			screen.syncList()
			screen.syncToolbar()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionReturn}}
	case "ctrl+f":
		screen.searchOpen = true
		screen.focusID(SearchID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	case "ctrl+r":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionRefresh}}
	case "alt+down":
		screen.NavigateHunk(1)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "alt+up":
		screen.NavigateHunk(-1)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "alt+right", "alt+left":
		result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		direction := 1
		if evt.Key.String() == "alt+left" {
			direction = -1
		}
		screen.navigateFile(direction, &result)
		return result
	case "f3":
		screen.nextMatch(false)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "shift+f3":
		screen.nextMatch(true)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case "tab":
		screen.focusID(TabsID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (screen *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch action.ID {
	case "tabs.activate":
		mode, _ := action.Payload.(string)
		screen.SetMode(Mode(mode))
		result.Action = &ui.Action{ID: ActionMode, Payload: ModeRequest{Mode: screen.mode}}
	case "tabs.enter":
		screen.focusID(screen.documentID())
		if screen.showsList() {
			screen.focusID(ListID)
		}
	case "gitreview.activate":
		row, ok := action.Payload.(ui.ListItem)
		if !ok {
			return result
		}
		screen.activateRow(row, &result)
	case "gitreview.presentation":
		if screen.preferred == Unified {
			screen.SetPresentation(SideBySide)
		} else {
			screen.SetPresentation(Unified)
		}
	case "gitreview.navigator":
		screen.SetNavigatorVisible(!screen.navigator)
	case "gitreview.search":
		screen.searchOpen = !screen.searchOpen
		if screen.searchOpen {
			screen.focusID(SearchID)
		}
	case "gitreview.previous-hunk":
		screen.NavigateHunk(-1)
	case "gitreview.next-hunk":
		screen.NavigateHunk(1)
	case "gitreview.previous-file":
		screen.navigateFile(-1, &result)
	case "gitreview.next-file":
		screen.navigateFile(1, &result)
	case "gitreview.copy-path":
		result.Cmd = rendering.CopyToClipboardCmd(screen.comparison.Path)
	case ActionReviewCommit:
		screen.commitPage = true
		screen.SetMode(ModeChanges)
		result.Action = &ui.Action{ID: ActionReviewCommit}
	case ActionStage, ActionUnstage, ActionOpenFile:
		if screen.toolbar.bar.ActionEnabled(action.ID) {
			result.Action = &ui.Action{ID: action.ID, Payload: ComparisonRequest{Comparison: screen.comparison}}
		}
	case ActionCommit:
		if screen.canCommit() {
			result.Action = &ui.Action{ID: ActionCommit, Payload: CommitRequest{Review: screen.commitReview, Message: screen.CommitMessage()}}
		} else {
			if strings.TrimSpace(screen.CommitMessage()) == "" {
				screen.errText = "Enter a commit message; the draft remains editable"
			}
		}
	case ActionCheckout:
		if row, ok := screen.list.list.Selected(); ok {
			if branch, ok := row.Payload.(workspace.GitBranchSummary); ok && !branch.Current && !screen.loading {
				result.Action = &ui.Action{ID: ActionCheckout, Payload: CheckoutRequest{RepositoryRoot: screen.snapshot.RepositoryRoot, Head: screen.snapshot.Head, Branch: branch}}
			}
		}
	case ActionInspectCommit:
		if row, ok := screen.list.list.Selected(); ok {
			if commit, ok := row.Payload.(workspace.GitCommitSummary); ok {
				result.Action = &ui.Action{ID: ActionInspectCommit, Payload: InspectCommitRequest{Commit: commit}}
			}
		}
	case "gitreview.focus-next", "gitreview.focus-previous":
		screen.focusID(screen.documentID())
	case ActionReturn, ActionRefresh:
		result.Action = &ui.Action{ID: action.ID}
	default:
		return ui.EventResult{Action: &action}
	}
	screen.syncToolbar()
	return result
}

func (screen *Screen) documentID() ui.ComponentID {
	if screen.mode == ModeBranches {
		return DetailID
	}
	return screen.viewer.DocumentID()
}

func (screen *Screen) DocumentID() ui.ComponentID      { return screen.documentID() }
func (screen *Screen) InvalidateWidthProfile()         { screen.viewer.InvalidateWidthProfile() }
func (screen *Screen) NavigateHunk(direction int) bool { return screen.viewer.NavigateHunk(direction) }
func (screen *Screen) applySearch(query string)        { screen.viewer.SetQuery(query) }
func (screen *Screen) nextMatch(previous bool)         { screen.viewer.NextMatch(previous) }

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
