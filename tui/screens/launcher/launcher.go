// Package launcher provides the native welcome launcher surface.
package launcher

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID           = "launcher"
	HeroID             = "launcher/hero"
	NewWorkspaceID     = "launcher/new-workspace"
	ShortcutsID        = "launcher/shortcuts"
	DividerID          = "launcher/divider"
	WorkspacesHeaderID = "launcher/workspaces-header"
	SearchID           = "launcher/search"
	WorkspaceListID    = "launcher/workspace-list"
	ScrollbarID        = "launcher/scrollbar"
	EmptyID            = "launcher/empty"
)

// paneContentInset is the shared left rail for dense pane content. A pane has
// no matching right inset: content reaches the final column unless the
// scrollbar owns that column.
const paneContentInset = 1

// rowInset is the indent used for section body content (search field, list
// items, shortcut entries) so it sits one column past the section header text.
// The shortcuts header still hardcodes this offset directly because its
// shortcut list is hand-laid out without a pane; the workspaces section
// consumes the same offset via paneLayout.bodyRect.
const rowInset = paneContentInset + 1

// paneLayout is the single geometry contract for a dense pane. Header, search,
// empty state, and collection all consume Content; Scrollbar is either empty
// or owns the pane's final column without a spacer.
type paneLayout struct {
	Bounds    ui.Rect
	Content   ui.Rect
	Scrollbar ui.Rect
}

func newPaneLayout(bounds ui.Rect, hasScrollbar bool) paneLayout {
	p := paneLayout{Bounds: bounds}
	if bounds.Empty() {
		return p
	}
	inset := minInt(paneContentInset, bounds.W)
	barW := 0
	if hasScrollbar && bounds.W-inset > 0 {
		barW = minInt(ui.ScrollbarWidth, bounds.W-inset)
	}
	p.Content = ui.Rect{
		X: bounds.X + inset,
		Y: bounds.Y,
		W: maxInt(0, bounds.W-inset-barW),
		H: bounds.H,
	}
	if barW > 0 {
		p.Scrollbar = ui.Rect{X: bounds.X + bounds.W - barW, Y: bounds.Y, W: barW, H: bounds.H}
	}
	return p
}

func (p paneLayout) contentRect(y, h int) ui.Rect {
	return ui.Rect{X: p.Content.X, Y: y, W: p.Content.W, H: maxInt(0, h)}
}

// bodyRect returns the content rect indented one column inward, so body rows
// (search field, list items) sit one column past their section header.
func (p paneLayout) bodyRect(y, h int) ui.Rect {
	if p.Content.W <= 1 {
		return p.contentRect(y, h)
	}
	return ui.Rect{X: p.Content.X + 1, Y: y, W: p.Content.W - 1, H: maxInt(0, h)}
}

func (p paneLayout) scrollbarRect(y, h int) ui.Rect {
	if p.Scrollbar.Empty() {
		return ui.Rect{}
	}
	return ui.Rect{X: p.Scrollbar.X, Y: y, W: p.Scrollbar.W, H: maxInt(0, h)}
}

// ItemKind identifies a selectable launcher entry.
type ItemKind int

const (
	ItemNewSession ItemKind = iota
	ItemOpenFolder
	ItemWorkspace
)

// WorkspaceEntry is the display form of a known workspace.
type WorkspaceEntry struct {
	ID           string
	Name         string
	Path         string
	SessionCount int
}

// Item is one keyboard/mouse-selectable entry.
type Item struct {
	Kind      ItemKind
	Workspace *WorkspaceEntry
}

// ActivateMsg is emitted when the user activates a selected item.
type ActivateMsg struct {
	Screen        *Screen
	Kind          ItemKind
	WorkspacePath string
}

// DeleteMsg is emitted when the user deletes a workspace.
type DeleteMsg struct {
	Screen      *Screen
	Kind        ItemKind
	WorkspaceID string
}

// SearchDebouncedMsg is re-emitted by the search field; the launcher validates
// ownership and ignores it because filtering is synchronous.
type SearchDebouncedMsg struct {
	Screen *Screen
	Value  string
}

// Screen is the root native launcher component.
type Screen struct {
	ui.Base
	th         *theme.Theme
	tokens     *ui.TokenSet
	cwd        string
	workspaces []WorkspaceEntry
	shortcuts  [][2]string
	focus      func(ui.ComponentID) bool

	hero         *actionCard
	newWs        *actionCard
	shortcutsCmp *shortcutList
	divider      *ui.SplitDivider
	split        *ui.SplitModel
	header       *sectionHeader
	search       *searchWrapper
	list         *workspaceList
	scrollbar    *ui.ScrollbarControl
	empty        *workspaceEmpty

	filterOn bool
	selected int // flat index: 0 = hero, 1 = new workspace, 2.. = workspaces
	items    []Item
	filtered []int

	frame         int
	workspacePane paneLayout
}

// New creates a launcher screen. Use Refresh or SetData to populate it.
func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:   ui.NewBase(ScreenID),
		th:     th,
		tokens: tokens,
	}

	s.hero = &actionCard{
		Base:    ui.NewBase(HeroID),
		owner:   s,
		id:      HeroID,
		kind:    ItemNewSession,
		title:   " New Session",
		tagline: "",
		hint:    "enter ↵",
		sparkle: true,
	}
	s.newWs = &actionCard{
		Base:    ui.NewBase(NewWorkspaceID),
		owner:   s,
		id:      NewWorkspaceID,
		kind:    ItemOpenFolder,
		title:   "📁 New Workspace",
		tagline: "Open a folder as a workspace",
		hint:    "enter ↵",
	}

	s.shortcutsCmp = &shortcutList{Base: ui.NewBase(ShortcutsID), owner: s}
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetDividerSize(3)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetTokens(tokens)
	s.divider.SetColorRole(ui.ColorMuted)
	s.divider.SetFill(true)
	s.divider.SetTransparent(true)
	s.header = &sectionHeader{Base: ui.NewBase(WorkspacesHeaderID), owner: s}
	s.empty = &workspaceEmpty{Base: ui.NewBase(EmptyID), owner: s}

	searchField := ui.NewSearchField(SearchID, tokens)
	searchField.SetPrompt(ui.SearchGlyphEnd(tokens))
	searchField.SetPromptPlacement(ui.PromptEnd)
	searchField.SetPlaceholder("Search workspaces")
	searchField.SetSubmitAction("")
	s.search = &searchWrapper{Base: ui.NewBase(SearchID), owner: s, field: searchField}

	virt := ui.NewVirtualList(WorkspaceListID, tokens)
	virt.SetActivateAction("launcher.activate")
	// Wheel scrolling must survive per-frame relayout: every frame re-runs
	// Layout, and selection-anchored scroll would yank the viewport back to
	// the selected row, undoing wheel/scrollbar gestures within one tick.
	// The launcher owns reveal explicitly — keyboard-driven selection moves
	// call EnsureSelectedVisible; hover and wheel never re-anchor.
	virt.SetSelectionAnchorsScroll(false)
	s.list = &workspaceList{Base: ui.NewBase(WorkspaceListID), owner: s, list: virt}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, virt.Scroll(), tokens)

	s.hero.SetTokens(tokens)
	s.newWs.SetTokens(tokens)
	s.search.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)

	return s
}

// SetTokens updates the runtime theme tokens.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.hero.SetTokens(tokens)
	s.newWs.SetTokens(tokens)
	s.search.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	if s.divider != nil {
		s.divider.SetTokens(tokens)
		s.divider.SetColorRole(ui.ColorMuted)
	}
	if s.search != nil && s.search.field != nil {
		s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.search.field.SetPromptPlacement(ui.PromptEnd)
	}
}

// SetFocusHandler sets the callback used to move keyboard focus.
func (s *Screen) SetFocusHandler(fn func(ui.ComponentID) bool) {
	s.focus = fn
	s.syncFocus()
}

// SyncFocus reapplies the screen's logical selection after a host layout has
// registered all child component IDs.
func (s *Screen) SyncFocus() { s.syncFocus() }

func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

// SetShortcuts replaces the shortcut rows shown in the left column.
func (s *Screen) SetShortcuts(shortcuts [][2]string) {
	s.shortcuts = shortcuts
}

// SetAnimation updates the sparkle frame.
func (s *Screen) SetAnimation(frame int, _ float64) {
	s.frame = frame
}

// Refresh reloads workspaces and resets the launcher for the given project.
func (s *Screen) Refresh(cwd string) {
	s.loadData(cwd)
	s.filterOn = false
	s.selected = 0
	s.search.field.SetValue("")
	s.rebuildItems()
	// Fresh project listing starts at the top; without selection-anchored
	// scroll the offset would otherwise persist across project switches.
	s.list.list.Scroll().ScrollTo(0)
	s.syncFocus()
}

// SetData initializes the launcher with externally-provided workspace data.
func (s *Screen) SetData(cwd string, workspaces []workspace.WorkspaceInfo) {
	s.workspaces = make([]WorkspaceEntry, 0, len(workspaces))
	cwdAbs, _ := filepath.Abs(cwd)
	if cwdAbs == "" {
		cwdAbs = cwd
	}
	for _, w := range workspaces {
		name := workspace.WorkspaceDisplayName(w)
		s.workspaces = append(s.workspaces, WorkspaceEntry{
			ID:           w.ID,
			Name:         name,
			Path:         w.ProjectDir,
			SessionCount: w.SessionCount,
		})
	}
	s.cwd = cwdAbs
	s.rebuildItems()
}

func (s *Screen) loadData(cwd string) {
	infos, _ := workspace.ListWorkspaceInfos()
	s.SetData(cwd, infos)
}

// FilterActive reports whether the workspace filter is active.
func (s *Screen) FilterActive() bool { return s.filterOn }

// Query returns the current workspace filter text.
func (s *Screen) Query() string {
	if s == nil || s.search == nil || s.search.field == nil {
		return ""
	}
	return s.search.field.Value()
}

// SelectedItem returns the currently selected item, or nil.
func (s *Screen) SelectedItem() *Item {
	if s.selected < 0 || s.selected >= len(s.items) {
		return nil
	}
	it := s.items[s.selected]
	return &it
}

// TotalItems returns the count of selectable items.
func (s *Screen) TotalItems() int { return len(s.items) }

// SelectedWorkspacePath returns the selected workspace directory, if any.
func (s *Screen) SelectedWorkspacePath() string {
	it := s.SelectedItem()
	if it == nil || it.Kind != ItemWorkspace || it.Workspace == nil {
		return ""
	}
	return it.Workspace.Path
}

// HandleSearchDebounced validates a debounce notification from the search field.
// Launcher filtering is synchronous, so accepted notifications are no-ops.
func (s *Screen) HandleSearchDebounced(msg ui.SearchDebouncedMsg) tea.Cmd {
	if msg.Field != s.search.field || msg.Value != s.search.field.Value() {
		return nil
	}
	return nil
}

// ListScrollOffset exposes the workspace viewport offset for integration tests
// and viewmodel geometry assertions.
func (s *Screen) ListScrollOffset() int {
	if s == nil || s.list == nil || s.list.list == nil {
		return 0
	}
	return s.list.list.Scroll().Offset()
}

// SetListScroll sets the workspace viewport offset, clamped by the shared
// ScrollModel.
func (s *Screen) SetListScroll(offset int) {
	if s == nil || s.list == nil || s.list.list == nil {
		return
	}
	s.list.list.Scroll().ScrollTo(offset)
}

func (s *Screen) rebuildItems() {
	items := make([]Item, 0, 2+len(s.workspaces))
	items = append(items,
		Item{Kind: ItemNewSession},
		Item{Kind: ItemOpenFolder},
	)
	for i := range s.workspaces {
		w := s.workspaces[i]
		wc := w
		items = append(items, Item{Kind: ItemWorkspace, Workspace: &wc})
	}
	s.items = items

	ft := ""
	if s.filterOn {
		ft = s.search.field.Value()
	}
	s.filtered = nil
	for i, it := range s.items {
		if it.Kind == ItemWorkspace && it.Workspace != nil && s.workspaceMatches(*it.Workspace, ft) {
			s.filtered = append(s.filtered, i)
		}
	}

	s.selected = clampInt(s.selected, 0, len(s.items)-1)
	if s.filterOn && !s.listItemVisible(s.selected) {
		if len(s.filtered) > 0 {
			s.selected = s.filtered[0]
		}
	}
	s.syncList()
	s.ensureSelectedVisible()
}

func (s *Screen) listItemVisible(itemIdx int) bool {
	for _, i := range s.filtered {
		if i == itemIdx {
			return true
		}
	}
	return false
}

func (s *Screen) selectableIndices() []int {
	if s.filterOn {
		return append([]int(nil), s.filtered...)
	}
	indices := make([]int, len(s.items))
	for i := range s.items {
		indices[i] = i
	}
	return indices
}

func (s *Screen) syncList() {
	var items []ui.VirtItem
	if s.filterOn {
		items = make([]ui.VirtItem, 0, len(s.filtered))
		for _, idx := range s.filtered {
			it := s.items[idx]
			items = append(items, ui.VirtItem{
				ID:      fmt.Sprintf("row-%d", idx),
				Height:  1,
				Label:   s.itemListLabel(it),
				Payload: idx,
			})
		}
	} else {
		items = make([]ui.VirtItem, 0, maxInt(0, len(s.items)-2))
		for i := 2; i < len(s.items); i++ {
			it := s.items[i]
			items = append(items, ui.VirtItem{
				ID:      fmt.Sprintf("row-%d", i),
				Height:  1,
				Label:   s.itemListLabel(it),
				Payload: i,
			})
		}
	}
	s.list.list.SetItems(items)
	cursor := s.listCursorFromSelected()
	if cursor >= 0 {
		s.list.list.SelectIndex(cursor)
	} else {
		s.list.list.SelectIndex(0)
	}
}

func (s *Screen) itemListLabel(it Item) string {
	if it.Kind == ItemWorkspace && it.Workspace != nil {
		return it.Workspace.Name
	}
	return ""
}

func (s *Screen) listCursorFromSelected() int {
	if s.selected < 2 {
		return -1
	}
	if s.filterOn {
		for pos, idx := range s.filtered {
			if idx == s.selected {
				return pos
			}
		}
		return -1
	}
	return s.selected - 2
}

func (s *Screen) ensureSelectedVisible() {
	cursor := s.listCursorFromSelected()
	if cursor >= 0 {
		s.list.list.SelectIndex(cursor)
	}
}

func (s *Screen) syncFocus() {
	if s.filterOn {
		s.focusID(SearchID)
		return
	}
	switch s.selected {
	case 0:
		s.focusID(HeroID)
	case 1:
		s.focusID(NewWorkspaceID)
	default:
		s.focusID(WorkspaceListID)
	}
}

func (s *Screen) selectFlat(idx int) {
	if idx < 0 || idx >= len(s.items) {
		return
	}
	if s.selected == idx {
		return
	}
	s.selected = idx
	s.syncList()
	s.syncFocus()
}

func (s *Screen) moveSelection(delta int) {
	indices := s.selectableIndices()
	if len(indices) == 0 {
		return
	}
	pos := 0
	for i, idx := range indices {
		if idx == s.selected {
			pos = i
			break
		}
	}
	pos = clampInt(pos+delta, 0, len(indices)-1)
	s.selected = indices[pos]
	s.syncList()
	// Keyboard navigation reveals the selection; with selection-anchored
	// scroll disabled nothing else scrolls the new row into view.
	s.list.list.EnsureSelectedVisible()
	s.syncFocus()
}

func (s *Screen) moveToFirst() {
	indices := s.selectableIndices()
	if len(indices) == 0 {
		return
	}
	s.selected = indices[0]
	s.syncList()
	s.list.list.EnsureSelectedVisible()
	s.syncFocus()
}

func (s *Screen) moveToLast() {
	indices := s.selectableIndices()
	if len(indices) == 0 {
		return
	}
	s.selected = indices[len(indices)-1]
	s.syncList()
	s.list.list.EnsureSelectedVisible()
	s.syncFocus()
}

func (s *Screen) activateFilter() {
	s.filterOn = true
	s.search.field.SetValue("")
	s.rebuildItems()
	if len(s.filtered) > 0 {
		s.selected = s.filtered[0]
	}
	s.syncList()
	// Reveal the first filtered row so Enter acts on a visible item.
	s.list.list.EnsureSelectedVisible()
	s.syncFocus()
}

func (s *Screen) deactivateFilter() {
	if !s.filterOn {
		return
	}
	s.filterOn = false
	s.search.field.SetValue("")
	s.rebuildItems()
	s.syncFocus()
}

func (s *Screen) applyFilter() {
	if !s.filterOn {
		return
	}
	s.rebuildItems()
	s.list.list.Scroll().ScrollTo(0)
}

func (s *Screen) activateSelected() tea.Cmd {
	it := s.SelectedItem()
	if it == nil {
		return nil
	}
	if s.filterOn && !s.listItemVisible(s.selected) {
		return nil
	}
	msg := ActivateMsg{Screen: s, Kind: it.Kind}
	switch it.Kind {
	case ItemOpenFolder:
		// no extra fields
	case ItemWorkspace:
		if it.Workspace != nil {
			msg.WorkspacePath = it.Workspace.Path
		}
	}
	return func() tea.Msg { return msg }
}

func (s *Screen) deleteSelected() (DeleteMsg, bool) {
	it := s.SelectedItem()
	if it == nil {
		return DeleteMsg{}, false
	}
	msg := DeleteMsg{Screen: s, Kind: it.Kind}
	switch it.Kind {
	case ItemWorkspace:
		if it.Workspace == nil {
			return DeleteMsg{}, false
		}
		msg.WorkspaceID = it.Workspace.ID
		s.RemoveWorkspace(it.Workspace.ID, it.Workspace.Path)
	default:
		return DeleteMsg{}, false
	}
	return msg, true
}

// RemoveWorkspace drops the workspace entry matching the given ID (path is
// the fallback match for legacy entries without an ID) from the launcher's
// in-memory list and rebuilds the rows. The caller persists the removal via
// workspace.DeleteWorkspace.
func (s *Screen) RemoveWorkspace(id, path string) bool {
	for i := range s.workspaces {
		w := s.workspaces[i]
		if w.ID == id || (id == "" && w.Path == path) {
			s.workspaces = append(s.workspaces[:i], s.workspaces[i+1:]...)
			s.rebuildItems()
			return true
		}
	}
	return false
}

// WorkspaceItemAt maps a workspace-list viewport row (0 = first visible row)
// to the entry rendered there, or nil for empty space below the last row.
// Coordinates are list-local: callers translate screen-absolute mouse
// positions through Host.ScreenRect(WorkspaceListID). Used by the
// right-click context menu to target the row under the cursor.
func (s *Screen) WorkspaceItemAt(viewportRow int) *WorkspaceEntry {
	if s == nil || s.list == nil || s.list.list == nil {
		return nil
	}
	idx := s.list.rowAt(viewportRow)
	if idx < 0 {
		return nil
	}
	real := s.list.realIndex(idx)
	if real < 0 || real >= len(s.items) {
		return nil
	}
	it := s.items[real]
	if it.Kind != ItemWorkspace || it.Workspace == nil {
		return nil
	}
	return it.Workspace
}

func (s *Screen) workspaceMatches(ws WorkspaceEntry, query string) bool {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return true
	}
	return strings.Contains(strings.ToLower(ws.Name), query) ||
		strings.Contains(strings.ToLower(ws.Path), query)
}

func (s *Screen) cardIndex(kind ItemKind) int {
	switch kind {
	case ItemNewSession:
		return 0
	case ItemOpenFolder:
		return 1
	}
	return -1
}

func (s *Screen) isCardSelected(kind ItemKind) bool {
	return s.selected == s.cardIndex(kind)
}

// handleKey is the screen-level key dispatcher used by child wrappers.
func (s *Screen) handleKey(msg tea.KeyPressMsg) ui.EventResult {
	if msg.String() == "ctrl+c" {
		return ui.EventResult{Handled: true}
	}
	if msg.Code == tea.KeyEsc {
		if s.filterOn {
			s.deactivateFilter()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	}

	if s.filterOn {
		// In filter mode, only navigation and activation are handled here;
		// text editing stays in the search field.
		switch msg.Code {
		case tea.KeyUp:
			s.moveSelection(-1)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyDown:
			s.moveSelection(1)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyEnter:
			cmd := s.activateSelected()
			return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{}
	}

	switch msg.String() {
	case "up", "k":
		s.moveSelection(-1)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case "down", "j":
		s.moveSelection(1)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case "enter":
		cmd := s.activateSelected()
		return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
	case "/":
		s.activateFilter()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case "d":
		msg, ok := s.deleteSelected()
		if !ok {
			return ui.EventResult{Handled: true}
		}
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return msg }, Invalidate: ui.InvalidatePaint}
	case "pgup":
		s.moveSelection(-5)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case "pgdown":
		s.moveSelection(5)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case "home":
		s.moveToFirst()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case "end":
		s.moveToLast()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	}
	return ui.EventResult{}
}

func (s *Screen) forwardFilter(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if !s.filterOn {
		return ui.EventResult{}
	}
	s.focusID(SearchID)
	ctx.Focused = true
	result := s.search.Handle(ctx, evt)
	result.Invalidate |= ui.InvalidateFocus | ui.InvalidatePaint
	return result
}

// Children returns the component tree.
func (s *Screen) Children() []ui.Component {
	children := []ui.Component{s.hero, s.newWs, s.shortcutsCmp, s.divider, s.header, s.search}
	if len(s.list.list.Items()) == 0 {
		children = append(children, s.empty)
	} else {
		children = append(children, s.list, s.scrollbar)
	}
	return children
}

func (s *Screen) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)

	width := maxInt(1, r.W)
	height := maxInt(1, r.H)

	heroH := minInt(4, height)
	blankH := minInt(1, maxInt(0, height-heroH))
	bodyH := height - heroH - blankH

	gap := minInt(3, maxInt(0, width-1))
	leftW := width * 40 / 100
	if width >= 40 {
		leftW = clampInt(leftW, 24, 40)
	} else {
		leftW = clampInt(leftW, 1, maxInt(1, width-gap-1))
	}
	if s.split != nil {
		minLeft := 1
		if width >= 40 {
			minLeft = 24
		}
		s.split.SetDividerSize(gap)
		s.split.SetMinSizes(minLeft, 1)
		s.split.SetMaxFirst(maxInt(minLeft, width-gap-1))
		if s.split.HasFirstSize() {
			leftW, gap, _ = s.split.Resolve(width)
		}
	}
	rightW := width - leftW - gap
	if rightW < 1 {
		rightW = 1
		leftW = maxInt(1, width-rightW-gap)
	}

	if heroH > 0 {
		s.hero.Layout(ctx, ui.Rect{X: 0, Y: 0, W: width, H: heroH})
	}
	newWsH := minInt(4, maxInt(0, bodyH))
	s.newWs.Layout(ctx, ui.Rect{X: 0, Y: heroH + blankH, W: leftW, H: newWsH})

	shortcutsY := heroH + blankH + 4 + 1
	shortcutsH := maxInt(0, bodyH-4-1)
	s.shortcutsCmp.Layout(ctx, ui.Rect{X: 0, Y: shortcutsY, W: leftW, H: shortcutsH})

	bodyY := heroH + blankH
	listY := bodyY + 2
	listH := maxInt(0, bodyH-2)
	rightX := leftW + gap
	showScrollbar := listH > 0 && len(s.list.list.Items()) > listH
	s.workspacePane = newPaneLayout(ui.Rect{X: rightX, Y: bodyY, W: rightW, H: bodyH}, showScrollbar)
	pane := s.workspacePane

	count := len(s.filtered)
	nWorkspaces := 0
	for _, idx := range s.filtered {
		if idx < 0 || idx >= len(s.items) {
			continue
		}
		if s.items[idx].Kind == ItemWorkspace {
			nWorkspaces++
		}
	}
	countLabel := fmt.Sprintf("%d items", count)
	switch {
	case nWorkspaces == 1:
		countLabel = "1 workspace"
	case nWorkspaces > 1:
		countLabel = fmt.Sprintf("%d workspaces", nWorkspaces)
	}
	s.header.Set("Recent", countLabel)
	if s.divider != nil {
		s.divider.SetCurrentFirst(leftW)
		s.divider.Layout(ctx, ui.Rect{X: leftW, Y: bodyY, W: gap, H: bodyH})
	}
	s.header.Layout(ctx, pane.contentRect(bodyY, 1))
	s.search.Layout(ctx, pane.bodyRect(bodyY+1, 1))
	s.list.Layout(ctx, pane.bodyRect(listY, listH))
	s.scrollbar.Layout(ctx, pane.scrollbarRect(listY, listH))
	s.empty.Layout(ctx, pane.bodyRect(listY, listH))
}

func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	// Launcher lives on the main content layer; transparent background lets the
	// terminal/theme default show through, matching the legacy welcome view.
}

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		return s.handleKey(evt.Key)
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(ctx ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "launcher.activate":
		return ui.EventResult{Handled: true, Cmd: s.activateSelected(), Invalidate: ui.InvalidatePaint}
	case "launcher.filter-on":
		s.activateFilter()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case ui.ActionSplitResize:
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Launcher"}
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)

// actionCard is a bordered action tile (hero or New Workspace).
type actionCard struct {
	ui.Base
	owner   *Screen
	id      ui.ComponentID
	kind    ItemKind
	title   string
	tagline string
	hint    string
	sparkle bool
	tokens  *ui.TokenSet
}

func (c *actionCard) SetTokens(tokens *ui.TokenSet) { c.tokens = tokens }

func (c *actionCard) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: 4})
}

func (c *actionCard) Layout(ctx ui.LayoutContext, r ui.Rect) {
	c.SetBounds(r)
}

func (c *actionCard) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	r := c.Bounds()
	if r.Empty() || r.H < 4 || r.W < 8 {
		return
	}
	selected := c.owner.isCardSelected(c.kind)
	borderFG := c.tokens.Color(ui.ColorBorder)
	titleFG := c.tokens.Color(ui.ColorFg)
	if selected {
		borderFG = c.tokens.Color(ui.ColorAccent)
		titleFG = c.tokens.Color(ui.ColorAccent)
	} else if c.kind == ItemNewSession {
		borderFG = c.tokens.Color(ui.ColorMuted)
	}

	ox, oy := originX+r.X, originY+r.Y
	innerW := r.W - 4 // 2 border + 2 padding
	g := c.tokens.Glyphs()
	cornerTL := string(ui.FirstRune(g.CornerTL, '╭'))
	cornerTR := string(ui.FirstRune(g.CornerTR, '╮'))
	cornerBL := string(ui.FirstRune(g.CornerBL, '╰'))
	cornerBR := string(ui.FirstRune(g.CornerBR, '╯'))
	hBorder := string(ui.FirstRune(g.BorderH, '─'))
	vBorder := string(ui.FirstRune(g.BorderV, '│'))

	// Top border
	buf.WriteString(ox, oy, cornerTL+strings.Repeat(hBorder, r.W-2)+cornerTR, borderFG, cell.NoColor, 0)

	// Title row
	title := c.title
	if c.sparkle {
		title = sparkleFrame(c.owner.frame) + title
	}
	title = rendering.TruncateLine(title, innerW)
	buf.WriteString(ox, oy+1, vBorder, borderFG, cell.NoColor, 0)
	buf.WriteString(ox+1, oy+1, " ", titleFG, cell.NoColor, 0)
	titleW := rendering.VisibleWidth(title)
	buf.WriteString(ox+2, oy+1, title, titleFG, cell.NoColor, cell.AttrBold)
	buf.WriteString(ox+2+titleW, oy+1, strings.Repeat(" ", innerW-titleW), titleFG, cell.NoColor, 0)
	buf.WriteString(ox+r.W-2, oy+1, " ", titleFG, cell.NoColor, 0)
	buf.WriteString(ox+r.W-1, oy+1, vBorder, borderFG, cell.NoColor, 0)

	// Middle row
	tag := c.tagline
	if c.kind == ItemNewSession {
		tag = c.owner.cwd
	}
	hint := c.hint
	muted := c.tokens.Color(ui.ColorMuted)
	hint = rendering.TruncateLine(hint, innerW)
	hintW := rendering.VisibleWidth(hint)
	maxTagW := maxInt(0, innerW-hintW-1)
	tag = rendering.TruncateLine(tag, maxTagW)
	tagW := rendering.VisibleWidth(tag)
	spacerW := innerW - tagW - hintW
	if spacerW < 0 {
		spacerW = 0
	}
	middle := tag + strings.Repeat(" ", spacerW) + hint
	buf.WriteString(ox, oy+2, vBorder, borderFG, cell.NoColor, 0)
	buf.WriteString(ox+1, oy+2, " ", muted, cell.NoColor, 0)
	buf.WriteString(ox+2, oy+2, middle, muted, cell.NoColor, 0)
	buf.WriteString(ox+r.W-2, oy+2, " ", muted, cell.NoColor, 0)
	buf.WriteString(ox+r.W-1, oy+2, vBorder, borderFG, cell.NoColor, 0)

	// Bottom border
	buf.WriteString(ox, oy+3, cornerBL+strings.Repeat(hBorder, r.W-2)+cornerBR, borderFG, cell.NoColor, 0)
}

func (c *actionCard) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		result := c.owner.handleKey(evt.Key)
		if result.Handled || !c.owner.filterOn {
			return result
		}
		return c.owner.forwardFilter(ctx, evt)
	}
	if c.owner.filterOn {
		if evt.Kind == ui.EventCustom {
			if _, ok := evt.Custom.(tea.PasteMsg); ok {
				return c.owner.forwardFilter(ctx, evt)
			}
		}
	}
	switch evt.Kind {
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		c.owner.selectFlat(c.owner.cardIndex(c.kind))
		return ui.EventResult{Handled: true, Cmd: c.owner.activateSelected(), Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case ui.EventMouseMotion:
		c.owner.selectFlat(c.owner.cardIndex(c.kind))
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (c *actionCard) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleButton, Label: c.title, Selected: c.owner.isCardSelected(c.kind)}
}

// shortcutList renders the Shortcuts header and key/label rows in the left column.
type shortcutList struct {
	ui.Base
	owner *Screen
}

func (sl *shortcutList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (sl *shortcutList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	sl.SetBounds(r)
}

func (sl *shortcutList) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	r := sl.Bounds()
	if r.Empty() {
		return
	}
	ox, oy := originX+r.X, originY+r.Y
	tokens := sl.owner.tokens
	if tokens == nil {
		return
	}
	accent := tokens.Color(ui.ColorAccent)
	muted := tokens.Color(ui.ColorMuted)
	fg := tokens.Color(ui.ColorFg)

	// Header
	header := strings.Repeat(" ", paneContentInset) + "SHORTCUTS "
	headerW := r.W
	contentW := rendering.VisibleWidth(header)
	remain := headerW - contentW
	if remain < 0 {
		remain = 0
	}
	line := header + strings.Repeat(string(ui.FirstRune(tokens.Glyphs().BorderH, '─')), remain)
	line = rendering.TruncateLine(line, r.W)
	buf.WriteString(ox, oy, line, accent, cell.NoColor, cell.AttrBold)

	// Rows
	for i, sc := range sl.owner.shortcuts {
		y := oy + 1 + i
		if y >= oy+r.H {
			break
		}
		key := sc[0]
		label := sc[1]
		keyX := ox + rowInset
		buf.WriteString(ox, y, strings.Repeat(" ", rowInset), fg, cell.NoColor, 0)
		available := maxInt(0, r.W-rowInset)
		key = rendering.TruncateLine(key, available)
		keyW := rendering.VisibleWidth(key)
		gap := 14 - keyW
		if gap < 2 {
			gap = 2
		}
		gap = minInt(gap, maxInt(0, available-keyW))
		labelW := maxInt(0, available-keyW-gap)
		label = rendering.TruncateLine(label, labelW)
		buf.WriteString(keyX, y, key, fg, cell.NoColor, 0)
		buf.WriteString(keyX+keyW, y, strings.Repeat(" ", gap), fg, cell.NoColor, 0)
		buf.WriteString(keyX+keyW+gap, y, label, muted, cell.NoColor, 0)
		// fill any trailing whitespace
		fill := ox + r.W - (keyX + keyW + gap + rendering.VisibleWidth(label))
		if fill > 0 {
			buf.WriteString(keyX+keyW+gap+rendering.VisibleWidth(label), y, strings.Repeat(" ", fill), fg, cell.NoColor, 0)
		}
	}
	// Fill remaining rows
	for y := oy + 1 + len(sl.owner.shortcuts); y < oy+r.H; y++ {
		buf.WriteString(ox, y, strings.Repeat(" ", r.W), fg, cell.NoColor, 0)
	}
}

func (sl *shortcutList) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Shortcuts"}
}

// sectionHeader renders "LABEL ────" plus optional trailing muted text.
type sectionHeader struct {
	ui.Base
	owner    *Screen
	label    string
	trailing string
}

func (sh *sectionHeader) Set(label, trailing string) {
	sh.label = label
	sh.trailing = trailing
}

func (sh *sectionHeader) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: 1})
}

func (sh *sectionHeader) Layout(ctx ui.LayoutContext, r ui.Rect) {
	sh.SetBounds(r)
}

func (sh *sectionHeader) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	r := sh.Bounds()
	if r.Empty() {
		return
	}
	tokens := ui.NewTokenSet(ctx.Theme)
	accent := tokens.Color(ui.ColorAccent)
	muted := tokens.Color(ui.ColorMuted)

	ox, oy := originX+r.X, originY+r.Y
	trailing := rendering.TruncateLine(sh.trailing, r.W)
	trailingW := rendering.VisibleWidth(trailing)
	separatorW := 0
	if trailingW > 0 && trailingW < r.W {
		separatorW = 1
	}
	labelW := maxInt(0, r.W-trailingW-separatorW)
	label := ""
	if labelW > 0 {
		label = rendering.TruncateLine(strings.ToUpper(sh.label), labelW)
	}
	labelWUsed := rendering.VisibleWidth(label)
	buf.WriteString(ox, oy, label, accent, cell.NoColor, cell.AttrBold)
	fillW := maxInt(0, labelW-labelWUsed)
	buf.WriteString(ox+labelWUsed, oy, strings.Repeat(string(ui.FirstRune(tokens.Glyphs().BorderH, '─')), fillW), accent, cell.NoColor, cell.AttrBold)
	if separatorW > 0 {
		buf.WriteString(ox+labelW, oy, " ", muted, cell.NoColor, 0)
	}
	if trailingW > 0 {
		buf.WriteString(ox+labelW+separatorW, oy, trailing, muted, cell.NoColor, 0)
	}
}

func (sh *sectionHeader) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventMouseWheel && sh.owner != nil {
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			sh.owner.list.list.Scroll().ScrollBy(-3)
		case tea.MouseWheelDown:
			sh.owner.list.list.Scroll().ScrollBy(3)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (sh *sectionHeader) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleLabel, Label: sh.label}
}

// workspaceEmpty renders the quiet empty/no-results state for the collection.
type workspaceEmpty struct {
	ui.Base
	owner *Screen
}

func (e *workspaceEmpty) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (e *workspaceEmpty) Layout(_ ui.LayoutContext, r ui.Rect) { e.SetBounds(r) }

func (e *workspaceEmpty) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	if e.owner == nil || len(e.owner.list.list.Items()) > 0 {
		return
	}
	r := e.Bounds()
	if r.Empty() {
		return
	}
	text := "No recent workspaces"
	query := strings.TrimSpace(e.owner.search.field.Value())
	if e.owner.filterOn && query != "" {
		text = fmt.Sprintf("No workspaces match %q", query)
	}
	text = rendering.TruncateLine(text, maxInt(0, r.W))
	tokens := e.owner.tokens
	buf.WriteString(originX+r.X, originY+r.Y, text, tokens.Color(ui.ColorMuted), cell.NoColor, 0)
}

func (e *workspaceEmpty) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	// The empty-state component occupies the same viewport as the collection
	// so it can paint an empty result without a second geometry system. Forward
	// events whenever rows exist to keep hit testing deterministic.
	if e.owner != nil && len(e.owner.list.list.Items()) > 0 {
		return e.owner.list.Handle(ctx, evt)
	}
	return ui.EventResult{}
}

func (e *workspaceEmpty) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleLabel, Label: "Workspace results"}
}

// searchWrapper wraps the shared SearchField and adapts it to the launcher.
type searchWrapper struct {
	ui.Base
	owner *Screen
	field *ui.SearchField
}

func (sw *searchWrapper) SetTokens(tokens *ui.TokenSet) {
	sw.field.SetTokens(tokens)
	if tokens != nil {
		sw.field.SetBackgroundColor(tokens.Color(ui.ColorSubPaneBg))
		sw.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	}
}

func (sw *searchWrapper) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return sw.field.Measure(ctx, cs)
}

func (sw *searchWrapper) Layout(ctx ui.LayoutContext, r ui.Rect) {
	sw.SetBounds(r)
	sw.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}

func (sw *searchWrapper) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	sw.field.Paint(buf, originX+sw.Bounds().X, originY+sw.Bounds().Y, ctx)
}

func (sw *searchWrapper) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if !sw.owner.filterOn {
		// When not filtering, the search row only reacts to activation/clicks.
		switch evt.Kind {
		case ui.EventMouseClick:
			if evt.MouseClick.Button == tea.MouseLeft {
				sw.owner.activateFilter()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
			}
		case ui.EventMouseWheel:
			return sw.forwardWheel(evt.MouseWheel)
		}
		return ui.EventResult{}
	}

	if evt.IsKey() {
		key := evt.Key
		switch key.Code {
		case tea.KeyUp:
			sw.owner.moveSelection(-1)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyDown:
			sw.owner.moveSelection(1)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyEnter:
			cmd := sw.owner.activateSelected()
			return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
		case tea.KeyEsc:
			sw.owner.deactivateFilter()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyBackspace:
			if sw.field.Value() == "" {
				sw.owner.deactivateFilter()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
			}
		}
	}

	switch evt.Kind {
	case ui.EventMouseWheel:
		return sw.forwardWheel(evt.MouseWheel)
	case ui.EventMouseClick:
		if evt.MouseClick.Button == tea.MouseLeft {
			// Click keeps the filter active; Host has already moved focus here.
			sw.owner.filterOn = true
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}

	before := sw.field.Value()
	res := sw.field.Handle(ctx, evt)
	if sw.field.Value() != before {
		sw.owner.applyFilter()
		res.Invalidate |= ui.InvalidatePaint | ui.InvalidateLayout
	}
	return res
}

func (sw *searchWrapper) forwardWheel(m tea.MouseWheelMsg) ui.EventResult {
	switch m.Button {
	case tea.MouseWheelUp:
		sw.owner.list.list.Scroll().ScrollBy(-3)
	case tea.MouseWheelDown:
		sw.owner.list.list.Scroll().ScrollBy(3)
	}
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
}

func (sw *searchWrapper) Semantics() ui.Semantics { return sw.field.Semantics() }

// workspaceList wraps the VirtualList and forwards launcher-level keys.
type workspaceList struct {
	ui.Base
	owner *Screen
	list  *ui.VirtualList
}

func (wl *workspaceList) SetTokens(tokens *ui.TokenSet) {
	wl.list.SetTokens(tokens)
}

func (wl *workspaceList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return wl.list.Measure(ctx, cs)
}

func (wl *workspaceList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	wl.SetBounds(r)
	wl.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
	wl.list.SetRowPainter(wl.rowPainter)
}

func (wl *workspaceList) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	wl.list.Paint(buf, originX+wl.Bounds().X, originY+wl.Bounds().Y, ctx)
}

func (wl *workspaceList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		result := wl.owner.handleKey(evt.Key)
		if result.Handled || !wl.owner.filterOn {
			return result
		}
		return wl.owner.forwardFilter(ctx, evt)
	}
	if wl.owner.filterOn {
		if evt.Kind == ui.EventCustom {
			if _, ok := evt.Custom.(tea.PasteMsg); ok {
				return wl.owner.forwardFilter(ctx, evt)
			}
		}
	}
	switch evt.Kind {
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		idx := wl.rowAt(ctx.LocalY)
		if idx < 0 {
			return ui.EventResult{}
		}
		realIdx := wl.realIndex(idx)
		wl.owner.selectFlat(realIdx)
		return ui.EventResult{Handled: true, Cmd: wl.owner.activateSelected(), Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	case ui.EventMouseMotion:
		idx := wl.rowAt(ctx.LocalY)
		if idx < 0 {
			return ui.EventResult{Handled: true}
		}
		realIdx := wl.realIndex(idx)
		wl.owner.selectFlat(realIdx)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case ui.EventMouseWheel:
		return wl.list.Handle(ctx, evt)
	}
	return wl.list.Handle(ctx, evt)
}

func (wl *workspaceList) Semantics() ui.Semantics { return wl.list.Semantics() }

func (wl *workspaceList) rowAt(localY int) int {
	idx := wl.list.Scroll().Offset() + localY
	if idx < 0 || idx >= len(wl.list.Items()) {
		return -1
	}
	return idx
}

func (wl *workspaceList) realIndex(listIdx int) int {
	items := wl.list.Items()
	if listIdx < 0 || listIdx >= len(items) {
		return -1
	}
	if payload, ok := items[listIdx].Payload.(int); ok {
		return payload
	}
	return listIdx + 2
}

func (wl *workspaceList) rowPainter(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, localRow int, state ui.ControlState, ctx ui.PaintContext, tokens *ui.TokenSet) {
	if localRow != 0 {
		return
	}
	selected := false
	if payload, ok := item.Payload.(int); ok && payload == wl.owner.selected {
		selected = true
	}

	bg := cell.NoColor
	if selected || state == ui.StateHovered {
		bg = tokens.Color(ui.ColorHover)
		ui.FillRect(buf, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	}

	if payload, ok := item.Payload.(int); ok && payload >= 0 && payload < len(wl.owner.items) {
		it := wl.owner.items[payload]
		if it.Kind != ItemWorkspace {
			return
		}
	}

	ws := wl.workspaceFor(item)
	if ws == nil {
		return
	}
	name := ws.Name
	if name == "" {
		name = filepath.Base(ws.Path)
	}
	if name == "" || name == "." {
		name = "untitled"
	}
	meta := sessionCountLabel(ws.SessionCount)

	icons := filetree.NewIconResolver()
	if th := tokens.Theme(); th != nil {
		icons.ApplyTheme(&th.Colors.Tree)
	}
	icon := icons.ResolveIcon(filepath.Base(ws.Path), true, false, false)

	fixedW := rendering.VisibleWidth(icon.Icon) + 1 + 1 + 2 + rendering.VisibleWidth(meta)
	available := width - fixedW
	if available < 2 {
		available = 2
	}
	nameW := rendering.VisibleWidth(name)
	maxNameW := maxInt(8, width/4)
	if nameW > maxNameW {
		nameW = maxNameW
	}
	if nameW > available {
		nameW = available
	}
	displayName := rendering.TruncateLine(name, nameW)
	pathW := available - rendering.VisibleWidth(displayName) - 3
	path := ""
	if pathW > 4 {
		path = " · " + rendering.TruncateLine(ws.Path, pathW)
	}

	iconFG := rendering.ThemeColorToCell(icon.Color)
	if iconFG.IsDefault() {
		iconFG = tokens.Color(ui.ColorFg)
	}
	nameFG := tokens.Color(ui.ColorFg)
	pathFG := tokens.Color(ui.ColorMuted)
	metaFG := tokens.Color(ui.ColorMuted)

	xpos := x
	xpos = buf.WriteString(xpos, y, icon.Icon, iconFG, bg, 0)
	xpos = buf.WriteString(xpos, y, " ", nameFG, bg, 0)
	xpos = buf.WriteString(xpos, y, displayName, nameFG, bg, cell.AttrBold)
	xpos = buf.WriteString(xpos, y, path, pathFG, bg, 0)

	leftW := xpos - x
	gap := ui.TrailingGap(width, leftW, rendering.VisibleWidth(meta))
	xpos = buf.WriteString(xpos, y, strings.Repeat(" ", gap), nameFG, bg, 0)
	buf.WriteString(xpos, y, meta, metaFG, bg, 0)
}

func (wl *workspaceList) workspaceFor(item ui.VirtItem) *WorkspaceEntry {
	if payload, ok := item.Payload.(int); ok && payload >= 0 && payload < len(wl.owner.items) {
		return wl.owner.items[payload].Workspace
	}
	return nil
}

// utility helpers

func sparkleFrame(frame int) string {
	frames := []string{"✨", "🌟", "✨", "💫", "✨", "⭐"}
	return frames[(frame/10000)%len(frames)]
}

func sessionCountLabel(n int) string {
	if n == 1 {
		return "1 session"
	}
	return fmt.Sprintf("%d sessions", n)
}

func clampInt(v, low, high int) int {
	if v < low {
		return low
	}
	if v > high {
		return high
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
