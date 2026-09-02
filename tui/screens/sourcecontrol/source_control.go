// Package sourcecontrol provides the native Git activity sidebar.
package sourcecontrol

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID    ui.ComponentID = "sourcecontrol"
	CommitID    ui.ComponentID = "sourcecontrol/commit"
	ListID      ui.ComponentID = "sourcecontrol/list"
	ScrollbarID ui.ComponentID = "sourcecontrol/scrollbar"
	DividerID   ui.ComponentID = "sourcecontrol/divider"

	ActionOpenChange     = "sourcecontrol.open-change"
	ActionCheckoutBranch = "sourcecontrol.checkout-branch"
	ActionCommit         = "sourcecontrol.commit"
	ActionRefresh        = "sourcecontrol.refresh"
)

const (
	summaryRow = panelchrome.HeaderRows
	listTop    = panelchrome.HeaderRows + 1
)

type ItemKind uint8

const (
	ItemChange ItemKind = iota
	ItemCommit
	ItemBranch
)

// Item is the typed payload for one selectable source-control row.
type Item struct {
	Kind   ItemKind
	Change workspace.GitChange
	Commit workspace.GitCommitSummary
	Branch workspace.GitBranchSummary
}

// Screen renders branch state, commit input, grouped changes, recent commits,
// and local branches in one keyboard/mouse navigable activity panel.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	snapshot  workspace.GitSnapshot
	commit    *commitField
	list      *ui.List
	scrollbar *ui.ScrollbarControl
	split     *ui.SplitModel
	divider   *ui.SplitDivider
	icons     *filetree.IconResolver
	loading   bool
	errText   string
	focus     func(ui.ComponentID) bool
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:   ui.NewBase(ScreenID),
		th:     th,
		tokens: tokens,
		icons:  filetree.NewIconResolver(),
	}
	s.icons.ApplyTheme(&th.Colors.Tree)
	field := ui.NewTextField(CommitID, tokens)
	field.SetPrompt("› ")
	field.SetPlaceholder("Commit message (Ctrl+Enter)")
	field.SetSubmitAction("")
	s.commit = &commitField{Base: ui.NewBase(CommitID), field: field, owner: s}
	s.list = ui.NewList(ListID, tokens)
	s.list.SetActivateAction("sourcecontrol.activate")
	s.list.SetDefaultRowBackground(ui.ColorBg)
	s.list.SetSelectionMarker(false)
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, s.list.Scroll(), tokens)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetMinSizes(12, 0)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetTokens(tokens)
	s.rebuild()
	return s
}

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID) bool) { s.focus = fn }

func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) SetSnapshot(snapshot workspace.GitSnapshot) {
	selected := ""
	if item, ok := s.list.Selected(); ok {
		selected = item.ID
	}
	offset := s.list.Scroll().Offset()
	s.snapshot = snapshot
	s.loading = false
	s.errText = ""
	s.rebuild()
	if selected != "" {
		for i, item := range s.list.Items() {
			if item.ID == selected {
				s.list.SelectIndex(i)
				break
			}
		}
	}
	s.list.Scroll().ScrollTo(offset)
}

func (s *Screen) Snapshot() workspace.GitSnapshot { return s.snapshot }

func (s *Screen) SetLoading(loading bool) {
	s.loading = loading
	if loading {
		s.errText = ""
	}
}

func (s *Screen) SetError(err error) {
	s.loading = false
	if err == nil {
		s.errText = ""
	} else {
		s.errText = err.Error()
	}
	s.rebuild()
}

func (s *Screen) CommitMessage() string {
	if s.commit == nil || s.commit.field == nil {
		return ""
	}
	return s.commit.field.Value()
}

func (s *Screen) ClearCommitMessage() {
	if s.commit != nil && s.commit.field != nil {
		s.commit.field.SetValue("")
	}
}

func (s *Screen) SetSplitMaxFirst(n int) {
	if s.split != nil {
		s.split.SetMaxFirst(n)
	}
}

func (s *Screen) SplitFirst() int {
	if s.split == nil {
		return 0
	}
	return s.split.FirstSize()
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.commit != nil {
		s.commit.SetTokens(tokens)
	}
	if s.list != nil {
		s.list.SetTokens(tokens)
		s.list.SetDefaultRowBackground(ui.ColorBg)
	}
	if s.scrollbar != nil {
		s.scrollbar.SetTokens(tokens)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	if s.icons != nil {
		s.icons.ApplyTheme(&th.Colors.Tree)
	}
	s.SetTokens(ui.NewTokenSet(th))
	s.rebuild()
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.commit, s.list, s.scrollbar, s.divider}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	contentW := r.W
	if contentW > 1 {
		contentW--
	}
	s.commit.Layout(ctx, s.headerTitle().FieldRect(panelchrome.BodyWidth(contentW)))
	listH := max(1, r.H-listTop)
	barW := 0
	if s.scrollbar.NeededForViewport(listH) {
		barW = min(ui.ScrollbarWidth, contentW)
	}
	bodyW := panelchrome.BodyWidth(contentW)
	s.list.Layout(ctx, ui.Rect{
		X: panelchrome.PadLeft, Y: listTop,
		W: max(1, bodyW-panelchrome.PadLeft), H: listH,
	})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, contentW-barW), Y: listTop, W: barW, H: listH})
	if s.split != nil && !s.split.Dragging() {
		s.split.SetFirstSize(max(0, r.W-1))
	}
	s.divider.SetCurrentFirst(max(0, r.W-1))
	s.divider.Layout(ctx, ui.Rect{X: max(0, r.W-1), W: 1, H: r.H})
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	if buf == nil || s.tokens == nil {
		return
	}
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	contentW := r.W
	if contentW > 1 {
		contentW--
	}
	panelchrome.PaintTitle(buf, ox, oy, ui.Rect{X: r.X, Y: r.Y, W: panelchrome.BodyWidth(contentW), H: r.H}, s.tokens, s.headerTitle())
	s.paintSummary(buf, ox+r.X+panelchrome.PadLeft, oy+r.Y+summaryRow, max(1, r.W-3))
}

func (s *Screen) paintSummary(buf *surface.CellBuffer, x, y, width int) {
	text := s.summary()
	fg := s.tokens.Color(ui.ColorMuted)
	if s.errText != "" {
		text = s.errText
		fg = s.tokens.Color(ui.ColorError)
	} else if s.loading {
		text = "Refreshing repository…"
		fg = s.tokens.Color(ui.ColorAccent)
	}
	buf.WriteString(x, y, rendering.TruncateLine(text, width), fg, s.tokens.Color(ui.ColorBg), 0)
}

func (s *Screen) summary() string {
	counts := map[workspace.GitChangeArea]int{}
	for _, change := range s.snapshot.Changes {
		counts[change.Area]++
	}
	if len(s.snapshot.Changes) == 0 {
		return "✓ Working tree clean"
	}
	var parts []string
	if n := counts[workspace.GitAreaConflict]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d conflicts", n))
	}
	if n := counts[workspace.GitAreaStaged]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d staged", n))
	}
	if n := counts[workspace.GitAreaWorking]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d changed", n))
	}
	if n := counts[workspace.GitAreaUntracked]; n > 0 {
		parts = append(parts, fmt.Sprintf("%d untracked", n))
	}
	return strings.Join(parts, " · ")
}

func (s *Screen) headerTitle() panelchrome.Title {
	return panelchrome.Title{
		Icon:  sourceControlIcon(panelchrome.NerdFont(s.th)),
		Badge: s.branchBadge(),
	}
}

func (s *Screen) branchBadge() string {
	branch := s.snapshot.Branch
	if branch == "" {
		return ""
	}
	if s.snapshot.Ahead > 0 || s.snapshot.Behind > 0 {
		branch += fmt.Sprintf(" ↑%d ↓%d", s.snapshot.Ahead, s.snapshot.Behind)
	}
	return branch
}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.String() == "ctrl+r" {
		return ui.EventResult{
			Handled:    true,
			Action:     &ui.Action{ID: ActionRefresh},
			Invalidate: ui.InvalidatePaint,
		}
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if action.ID != "sourcecontrol.activate" {
		return ui.EventResult{Action: &action}
	}
	row, ok := action.Payload.(ui.ListItem)
	if !ok {
		return ui.EventResult{Handled: true}
	}
	item, ok := row.Payload.(Item)
	if !ok {
		return ui.EventResult{Handled: true}
	}
	switch item.Kind {
	case ItemChange:
		return ui.EventResult{
			Handled: true,
			Action:  &ui.Action{ID: ActionOpenChange, Payload: item.Change},
		}
	case ItemBranch:
		if item.Branch.Current {
			return ui.EventResult{Handled: true}
		}
		return ui.EventResult{
			Handled: true,
			Action:  &ui.Action{ID: ActionCheckoutBranch, Payload: item.Branch},
		}
	default:
		return ui.EventResult{Handled: true}
	}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Source Control", Value: s.snapshot.Branch}
}

func (s *Screen) ItemAtLocalY(localY int) (Item, bool) {
	row := localY - listTop
	if row < 0 || row >= s.list.Scroll().Viewport() {
		return Item{}, false
	}
	idx := s.list.Scroll().Offset() + row
	items := s.list.Items()
	if idx < 0 || idx >= len(items) || !items[idx].Selectable() {
		return Item{}, false
	}
	item, ok := items[idx].Payload.(Item)
	return item, ok
}

func (s *Screen) rebuild() {
	var rows []ui.ListItem
	if s.errText != "" {
		rows = append(rows, ui.ListItem{ID: "git-error", Label: "Repository unavailable", Detail: "!", Disabled: true})
	} else {
		rows = append(rows, s.changeRows()...)
		rows = append(rows, s.historyRows()...)
		rows = append(rows, s.branchRows()...)
	}
	s.list.SetItems(rows)
}

func (s *Screen) changeRows() []ui.ListItem {
	var rows []ui.ListItem
	groups := []struct {
		area  workspace.GitChangeArea
		label string
	}{
		{workspace.GitAreaConflict, "MERGE CHANGES"},
		{workspace.GitAreaStaged, "STAGED CHANGES"},
		{workspace.GitAreaWorking, "CHANGES"},
		{workspace.GitAreaUntracked, "UNTRACKED"},
	}
	for _, group := range groups {
		var changes []workspace.GitChange
		for _, change := range s.snapshot.Changes {
			if change.Area == group.area {
				changes = append(changes, change)
			}
		}
		if len(changes) == 0 {
			continue
		}
		rows = append(rows, ui.ListItem{
			ID: "header:" + group.label, Label: group.label,
			Detail: strconv.Itoa(len(changes)), Header: true,
		})
		for _, change := range changes {
			name := filepath.Base(change.Path)
			parent := filepath.Dir(change.Path)
			if parent == "." {
				parent = ""
			}
			icon := ""
			if s.icons != nil {
				icon = s.icons.ResolveIcon(name, false, false, false).Icon
			}
			label := name
			if parent != "" {
				label += "  " + parent
			}
			rows = append(rows, ui.ListItem{
				ID:    "change:" + strconv.Itoa(int(change.Area)) + ":" + change.Path,
				Label: label, Detail: change.Code, Icon: icon,
				Payload: Item{Kind: ItemChange, Change: change},
			})
		}
	}
	if len(rows) == 0 {
		rows = append(rows, ui.ListItem{ID: "clean", Label: "Working tree clean", Icon: "✓", Disabled: true})
	}
	return rows
}

func (s *Screen) historyRows() []ui.ListItem {
	if len(s.snapshot.RecentCommits) == 0 {
		return nil
	}
	rows := []ui.ListItem{{
		ID: "header:history", Label: "RECENT COMMITS",
		Detail: strconv.Itoa(len(s.snapshot.RecentCommits)), Header: true,
	}}
	for _, commit := range s.snapshot.RecentCommits {
		rows = append(rows, ui.ListItem{
			ID: "commit:" + commit.Hash, Label: commit.Subject,
			Detail: commit.ShortHash, Icon: "●",
			Payload: Item{Kind: ItemCommit, Commit: commit},
		})
	}
	return rows
}

func (s *Screen) branchRows() []ui.ListItem {
	if len(s.snapshot.Branches) == 0 {
		return nil
	}
	rows := []ui.ListItem{{
		ID: "header:branches", Label: "LOCAL BRANCHES",
		Detail: strconv.Itoa(len(s.snapshot.Branches)), Header: true,
	}}
	for _, branch := range s.snapshot.Branches {
		icon := "⑂"
		detail := ""
		if branch.Current {
			icon = "●"
			detail = "current"
		}
		rows = append(rows, ui.ListItem{
			ID: "branch:" + branch.Name, Label: branch.Name,
			Detail: detail, Icon: icon,
			Payload: Item{Kind: ItemBranch, Branch: branch},
		})
	}
	return rows
}

type commitField struct {
	ui.Base
	field *ui.TextField
	owner *Screen
}

func (c *commitField) SetTokens(tokens *ui.TokenSet) { c.field.SetTokens(tokens) }
func (c *commitField) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return c.field.Measure(ctx, cs)
}
func (c *commitField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	c.SetBounds(r)
	c.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (c *commitField) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	c.field.SetBackgroundColor(c.owner.tokens.Color(ui.ColorSubPaneBg))
	c.field.SetPromptColor(c.owner.tokens.Color(ui.ColorAccent))
	c.field.Paint(buf, ox+c.Bounds().X, oy+c.Bounds().Y, ctx)
}
func (c *commitField) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch {
		case evt.Key.String() == "ctrl+enter":
			return ui.EventResult{
				Handled:    true,
				Action:     &ui.Action{ID: ActionCommit, Payload: strings.TrimSpace(c.field.Value())},
				Invalidate: ui.InvalidatePaint,
			}
		case evt.Key.Code == tea.KeyDown || evt.Key.Code == tea.KeyTab:
			c.owner.focusID(ListID)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return c.field.Handle(ctx, evt)
}
func (c *commitField) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleTextInput, Label: "Commit message", Value: c.field.Value()}
}

func sourceControlIcon(nerd bool) string {
	if nerd {
		return "\uf418"
	}
	return "⑂"
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
