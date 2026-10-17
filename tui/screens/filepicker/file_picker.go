// Package filepicker provides the native file and workspace folder picker body.
package filepicker

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/filetree"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID     ui.ComponentID = "filepicker"
	FilterID     ui.ComponentID = "filepicker/filter"
	BreadcrumbID ui.ComponentID = "filepicker/breadcrumb"
	// ListID is retained as the public focus target name for callers and tests;
	// its implementation is now the native file tree.
	ListID      ui.ComponentID = "filepicker/list"
	ScrollbarID ui.ComponentID = "filepicker/scrollbar"
	ActionsID   ui.ComponentID = "filepicker/actions"
)

type Mode uint8

const (
	ModeFile Mode = iota
	ModeFolder
	// ModeOpenFile selects one document directly, without attach selections.
	ModeOpenFile
)

// Entry is the small neutral adapter accepted by the existing workspace
// browser lifecycle. The picker immediately projects it into filetree nodes.
type Entry struct {
	Name      string
	Path      string
	IsDir     bool
	IsSymlink bool
}

// IntentToken identifies one user intent within one open picker lifecycle.
// Epoch prevents close/reopen ABA; Sequence prevents older intents from
// overtaking newer intents from the same open picker.
type IntentToken struct {
	Epoch    uint64
	Sequence uint64
}

type NavigateMsg struct {
	Target   *Screen
	Path     string
	Epoch    uint64
	Sequence uint64
}

// LoadMsg requests an asynchronous child directory listing without replacing
// the current browser root. NavigateMsg is reserved for breadcrumb/path-root
// navigation.
type LoadMsg struct {
	Target   *Screen
	Path     string
	Epoch    uint64
	Sequence uint64
}

// SearchMsg starts bounded recursive path discovery for the current picker
// root. It carries no file contents.
type SearchMsg struct {
	Target   *Screen
	Query    string
	Epoch    uint64
	Sequence uint64
}

// ToggleMsg is emitted when a file is activated in Attach Files mode. It does
// not close the picker; confirmation is a separate SelectMsg.
type ToggleMsg struct {
	Target   *Screen
	Path     string
	Epoch    uint64
	Sequence uint64
}

type SelectMsg struct {
	Target   *Screen
	Path     string
	Paths    []string
	Folder   bool
	Epoch    uint64
	Sequence uint64
}

type CancelMsg struct {
	Target   *Screen
	Epoch    uint64
	Sequence uint64
}

func (m NavigateMsg) Intent() IntentToken {
	return IntentToken{Epoch: m.Epoch, Sequence: m.Sequence}
}

func (m NavigateMsg) ValidFor(target *Screen) bool {
	return m.Target == target && target != nil && target.IsCurrentIntent(m.Epoch, m.Sequence)
}

func (m LoadMsg) Intent() IntentToken {
	return IntentToken{Epoch: m.Epoch, Sequence: m.Sequence}
}

func (m LoadMsg) ValidFor(target *Screen) bool {
	return m.Target == target && target != nil && target.IsCurrentIntent(m.Epoch, m.Sequence)
}

func (m SearchMsg) Intent() IntentToken {
	return IntentToken{Epoch: m.Epoch, Sequence: m.Sequence}
}

func (m SearchMsg) ValidFor(target *Screen) bool {
	return m.Target == target && target != nil && target.IsCurrentIntent(m.Epoch, m.Sequence)
}

func (m ToggleMsg) Intent() IntentToken {
	return IntentToken{Epoch: m.Epoch, Sequence: m.Sequence}
}

func (m ToggleMsg) ValidFor(target *Screen) bool {
	return m.Target == target && target != nil && target.IsCurrentIntent(m.Epoch, m.Sequence)
}

func (m SelectMsg) Intent() IntentToken {
	return IntentToken{Epoch: m.Epoch, Sequence: m.Sequence}
}

func (m SelectMsg) ValidFor(target *Screen) bool {
	return m.Target == target && target != nil && target.IsCurrentIntent(m.Epoch, m.Sequence)
}

func (m CancelMsg) Intent() IntentToken {
	return IntentToken{Epoch: m.Epoch, Sequence: m.Sequence}
}

func (m CancelMsg) ValidFor(target *Screen) bool {
	return m.Target == target && target != nil && target.IsCurrentIntent(m.Epoch, m.Sequence)
}

type Screen struct {
	ui.Base
	th      *theme.Theme
	tokens  *ui.TokenSet
	mode    Mode
	root    string
	current string
	// entries is a compatibility snapshot for callers that seed a root listing;
	// rows are rendered exclusively from model and tree.
	entries   []Entry
	active    bool
	errText   string
	openEpoch uint64
	intentSeq uint64
	asyncLoad *asyncDirectoryLoad

	model        *filetree.FileTreeModel
	browseModel  *filetree.FileTreeModel
	searchActive bool
	searchQuery  string
	showHidden   bool
	filter       *pickerFilter
	breadcrumb   *pickerBreadcrumb
	tree         *pickerTree
	scrollbar    *ui.ScrollbarControl
	actions      *ui.ActionBar
	focus        func(ui.ComponentID)
	selected     map[string]bool
}

type pickerFilter struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

type pickerTree struct {
	ui.Base
	owner         *Screen
	tree          *ui.Tree
	lastClickPath string
	lastClickAt   time.Time
}

type breadcrumbPart struct {
	path  string
	label string
	x     int
	w     int
}

type pickerBreadcrumb struct {
	ui.Base
	owner  *Screen
	tokens *ui.TokenSet
	icons  *filetree.IconResolver
	path   string
	status string
	parts  []breadcrumbPart
	hover  int
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:     ui.NewBase(ScreenID),
		th:       th,
		tokens:   tokens,
		selected: make(map[string]bool),
	}
	s.filter = &pickerFilter{
		Base:  ui.NewBase(FilterID),
		field: ui.NewFilterField(FilterID, tokens),
		owner: s,
	}
	s.filter.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.filter.field.SetPromptPlacement(ui.PromptEnd)
	s.filter.field.SetSubmitAction("")
	s.filter.field.OnChanged(func(value string) {
		if s.model != nil && !s.searchActive {
			s.model.SetFilter(value)
		}
		s.sync()
	})
	s.breadcrumb = &pickerBreadcrumb{
		Base:   ui.NewBase(BreadcrumbID),
		owner:  s,
		tokens: tokens,
		icons:  filetree.NewIconResolver(),
		hover:  -1,
	}
	s.breadcrumb.icons.ApplyTheme(th)
	s.actions = ui.NewActionBar(ActionsID, tokens)
	s.rebuildTree()
	s.rebuildActions()
	s.sync()
	return s
}

func (s *Screen) Open(root string, mode Mode, entries []Entry) {
	if s == nil {
		return
	}
	s.discardAsyncDirectoryLoad()
	s.openEpoch++
	s.mode = mode
	s.root = cleanAbs(root)
	s.current = s.root
	s.active = true
	s.errText = ""
	s.selected = make(map[string]bool)
	s.searchActive = false
	s.searchQuery = ""
	s.showHidden = false
	s.entries = append(s.entries[:0], entries...)
	if mode == ModeFolder {
		s.filter.field.SetPlaceholder("Search folders")
	} else {
		s.filter.field.SetPlaceholder("Search files and folders")
	}
	s.filter.field.SetValue("")
	s.installModel(filetree.NewPickerFileTreeModel(s.current, mode == ModeFolder))
	if entries != nil {
		s.model.SetDirectoryEntries(s.current, toTreeEntries(entries))
	}
	s.rebuildActions()
	s.sync()
}

// SetDirectory applies the successful result of a root navigation request.
func (s *Screen) SetDirectory(path string, entries []Entry) {
	if s == nil {
		return
	}
	s.discardAsyncDirectoryLoad()
	s.current = cleanAbs(path)
	s.entries = append(s.entries[:0], entries...)
	s.errText = ""
	s.filter.field.SetValue("")
	s.searchActive = false
	s.searchQuery = ""
	s.installModel(filetree.NewPickerFileTreeModel(s.current, s.mode == ModeFolder))
	s.model.SetDirectoryEntries(s.current, toTreeEntries(entries))
	s.rebuildActions()
	s.sync()
}

// SetDirectoryChildren applies an asynchronous expansion result while keeping
// the current root and all other loaded branches intact.
func (s *Screen) SetDirectoryChildren(path string, entries []Entry) bool {
	if s == nil || s.model == nil {
		return false
	}
	s.entries = append(s.entries[:0], entries...)
	ok := s.model.SetDirectoryEntries(cleanAbs(path), toTreeEntries(entries))
	if ok {
		s.errText = ""
		s.rebuildActions()
		s.sync()
	}
	return ok
}

func (s *Screen) SetError(err error) {
	if s == nil {
		return
	}
	if err == nil {
		s.errText = ""
	} else {
		s.errText = err.Error()
	}
	if s.model != nil && s.asyncLoad != nil && s.asyncLoad.expand {
		s.model.SetDirectoryLoadError(s.asyncLoad.path)
	}
	s.rebuildActions()
	s.sync()
}

func (s *Screen) Close() {
	if s == nil {
		return
	}
	s.discardAsyncDirectoryLoad()
	s.active = false
	s.errText = ""
	s.openEpoch++
	s.selected = make(map[string]bool)
	s.entries = nil
	s.searchActive = false
	s.searchQuery = ""
	s.model = nil
	s.browseModel = nil
	s.rebuildTree()
	s.rebuildActions()
	s.sync()
}

func (s *Screen) IsOpen() bool { return s != nil && s.active }
func (s *Screen) Mode() Mode {
	if s == nil {
		return ModeFile
	}
	return s.mode
}
func (s *Screen) Current() string {
	if s == nil {
		return ""
	}
	return s.current
}
func (s *Screen) VisibleCount() int {
	if s == nil || s.model == nil {
		return 0
	}
	return len(s.model.VisibleNodes())
}

// VisiblePaths returns the current tree projection in paint order. It is used
// by parent policies and regression tests; callers must not mutate the result.
func (s *Screen) VisiblePaths() []string {
	if s == nil || s.model == nil {
		return nil
	}
	return s.model.VisibleRows()
}
func (s *Screen) OpenEpoch() uint64 {
	if s == nil {
		return 0
	}
	return s.openEpoch
}
func (s *Screen) IntentSequence() uint64 {
	if s == nil {
		return 0
	}
	return s.intentSeq
}

func (s *Screen) Selected() (Entry, bool) {
	if s == nil || s.model == nil {
		return Entry{}, false
	}
	node := s.model.SelectedNode()
	if node == nil {
		return Entry{}, false
	}
	return Entry{Name: node.Name, Path: node.Path, IsDir: node.IsDir, IsSymlink: node.IsSymlink}, true
}

// SelectedPaths returns attach selections in deterministic insertion order.
func (s *Screen) SelectedPaths() []string {
	if s == nil || len(s.selected) == 0 {
		return nil
	}
	paths := make([]string, 0, len(s.selected))
	for path, selected := range s.selected {
		if selected {
			paths = append(paths, path)
		}
	}
	sort.Strings(paths)
	return paths
}

// TogglePath changes one attach selection. Folder-mode rows and unknown paths
// are intentionally ignored.
func (s *Screen) TogglePath(path string) bool {
	if s == nil || s.mode != ModeFile || s.model == nil {
		return false
	}
	node := s.model.NodeByPath(cleanAbs(path))
	if node == nil || node.IsDir {
		return false
	}
	path = cleanAbs(path)
	s.selected[path] = !s.selected[path]
	if !s.selected[path] {
		delete(s.selected, path)
	}
	s.rebuildActions()
	s.sync()
	return true
}

func (s *Screen) ShowHidden() bool {
	return s != nil && s.showHidden
}

// ToggleHidden changes the in-memory picker projection. A caller with an
// active recursive search should issue a new SearchMsg using the new policy.
func (s *Screen) ToggleHidden() bool {
	if s == nil {
		return false
	}
	s.showHidden = !s.showHidden
	query := ""
	if s.filter != nil && s.filter.field != nil {
		query = s.filter.field.Value()
	}
	if s.searchActive {
		s.ClearPathSearch()
		if query != "" {
			s.filter.field.SetValue(query)
		}
	}
	if s.model != nil {
		s.model.SetShowHidden(s.showHidden)
	}
	s.sync()
	return s.showHidden
}

// BeginPathSearch marks the picker as showing a recursive path result stream.
// The current browse model remains retained so clearing the query restores its
// expansion and cursor state.
func (s *Screen) BeginPathSearch(query string) bool {
	if s == nil || !s.active {
		return false
	}
	s.searchActive = strings.TrimSpace(query) != ""
	s.searchQuery = query
	if !s.searchActive {
		return s.ClearPathSearch()
	}
	s.errText = "Searching…"
	s.sync()
	return true
}

// ApplyPathSearch replaces the displayed projection with a tree-shaped path
// result model. The browse model remains untouched for restoration.
func (s *Screen) ApplyPathSearch(query string, matches []filetree.PathMatch) bool {
	if s == nil || !s.active || strings.TrimSpace(query) == "" {
		return false
	}
	s.searchActive = true
	s.searchQuery = query
	s.errText = ""
	s.model = filetree.NewPickerSearchModel(s.current, s.mode == ModeFolder, matches)
	s.model.SetShowHidden(s.showHidden)
	s.configureModel()
	s.rebuildTree()
	s.rebuildActions()
	s.sync()
	return true
}

// ClearPathSearch restores the retained browse model and its local expansion
// state without touching the filesystem.
func (s *Screen) ClearPathSearch() bool {
	if s == nil {
		return false
	}
	s.searchActive = false
	s.searchQuery = ""
	s.errText = ""
	if s.browseModel != nil {
		s.browseModel.ClearFilter()
		s.model = s.browseModel
		s.configureModel()
	}
	if s.filter != nil && s.filter.field != nil {
		s.filter.field.SetValue("")
	}
	s.rebuildTree()
	s.rebuildActions()
	s.sync()
	return true
}

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *Screen) FocusFilter()                            { s.focusID(FilterID) }
func (s *Screen) FocusTree()                              { s.focusID(ListID) }

func (s *Screen) focusID(id ui.ComponentID) {
	if s != nil && s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) IsCurrentIntent(epoch, sequence uint64) bool {
	return s != nil && s.active && sequence != 0 && epoch == s.openEpoch && sequence == s.intentSeq
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.filter, s.breadcrumb, s.tree, s.scrollbar, s.actions}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	if s == nil || tokens == nil {
		return
	}
	s.tokens = tokens
	if th := tokens.Theme(); th != nil {
		s.th = th
	}
	s.filter.SetTokens(tokens)
	s.breadcrumb.SetTokens(tokens)
	s.tree.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.actions.SetTokens(tokens)
	s.filter.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.filter.field.SetPromptPlacement(ui.PromptEnd)
	s.rebuildActions()
	s.sync()
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	const headerRows = 3
	filterY, breadcrumbY, treeY := headerRows, headerRows+2, headerRows+4
	s.filter.Layout(ctx, ui.Rect{X: 1, Y: filterY, W: maxInt(1, r.W-2), H: 1})
	s.breadcrumb.Layout(ctx, ui.Rect{X: 1, Y: breadcrumbY, W: maxInt(1, r.W-2), H: 1})
	treeH := maxInt(1, r.H-treeY-2)
	if s.model != nil {
		s.model.Update(filetree.TreeHeightMsg{Height: treeH})
	}
	if s.tree != nil {
		s.tree.sync()
	}
	barW := 0
	if s.scrollbar.NeededForViewport(treeH) {
		barW = minInt(ui.ScrollbarWidth, maxInt(0, r.W-1))
	}
	s.tree.Layout(ctx, ui.Rect{X: 1, Y: treeY, W: maxInt(1, r.W-1-barW), H: treeH})
	s.scrollbar.Layout(ctx, ui.Rect{X: maxInt(0, r.W-barW), Y: treeY, W: barW, H: treeH})
	s.actions.Layout(ctx, ui.Rect{Y: maxInt(0, r.H-1), W: r.W, H: 1})
	s.sync()
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	if s == nil || buf == nil || s.tokens == nil {
		return
	}
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorPopupBg)
	fg := s.tokens.Color(ui.ColorFg)
	accent := s.tokens.Color(ui.ColorAccent)
	muted := s.tokens.Color(ui.ColorMuted)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	x := ox + r.X + 1
	y := oy + r.Y
	x = buf.WriteString(x, y, "◆ crude", accent, bg, cell.AttrBold)
	label := "ATTACH FILES"
	subtitle := "Choose files to add to this conversation · fuzzy-search as you type"
	switch s.mode {
	case ModeFolder:
		label = "OPEN WORKSPACE"
		subtitle = "Choose a project folder · fuzzy-search as you type"
	case ModeOpenFile:
		label = "OPEN FILE"
		subtitle = "Choose a file to open in this pane · fuzzy-search as you type"
	}
	if x < ox+r.X+r.W-1 {
		buf.WriteString(x, y, "  "+label, fg, bg, cell.AttrBold)
	}
	if r.H > 1 {
		buf.WriteString(ox+r.X+1, y+1, rendering.TruncateLine(subtitle, maxInt(1, r.W-2)), muted, bg, 0)
	}
	if r.H > 2 {
		line := strings.Repeat(string(ui.FirstRune(s.tokens.Glyphs().BorderH, '─')), maxInt(0, r.W))
		buf.WriteString(ox+r.X, y+2, line, s.tokens.Color(ui.ColorPopupDivider), bg, 0)
	}
}

func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "filepicker.entry.activate":
		if s.AsyncDirectoryLoading() {
			return ui.EventResult{Handled: true}
		}
		item, ok := action.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		entry, ok := item.Payload.(Entry)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		if entry.IsDir {
			return s.navigate(entry.Path)
		}
		return ui.EventResult{Handled: true, Cmd: s.requestFileActivation(entry.Path), Invalidate: ui.InvalidatePaint}
	case "filepicker.breadcrumb.navigate":
		path, ok := action.Payload.(string)
		if !ok || path == "" {
			return ui.EventResult{Handled: true}
		}
		return s.navigate(path)
	case "filepicker.select":
		return s.confirm()
	case "filepicker.cancel":
		return s.cancel()
	case "actionbar.exit.backward":
		if s.VisibleCount() > 0 {
			s.FocusTree()
		} else {
			s.FocusFilter()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.forward":
		s.FocusFilter()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	if s.mode == ModeOpenFile {
		return ui.Semantics{Role: ui.RoleDialog, Label: "Open File", Value: s.current}
	}
	return ui.Semantics{Role: ui.RoleDialog, Label: "File picker", Value: s.current}
}

func (s *Screen) rebuildActions() {
	if s.actions == nil {
		return
	}
	primary := "Attach Files"
	switch s.mode {
	case ModeFolder:
		primary = "Open Workspace"
	case ModeOpenFile:
		primary = "Open File"
	}
	if !s.actions.SetLabel("filepicker.select", primary) {
		s.actions = ui.NewActionBar(ActionsID, s.tokens,
			ui.ActionBarButton{ID: ui.NewID(ActionsID, "select"), Label: primary, Action: "filepicker.select", Variant: ui.VariantPrimary, Enabled: false},
			ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "filepicker.cancel", Variant: ui.VariantSecondary, Enabled: true},
		)
		s.actions.SetCancelAction("filepicker.cancel")
	}
	enabled := s.mode == ModeFolder || len(s.SelectedPaths()) > 0
	if s.mode == ModeOpenFile {
		entry, ok := s.Selected()
		enabled = ok && !entry.IsDir
	}
	s.actions.SetEnabled("filepicker.select", s.active && !s.AsyncDirectoryLoading() && enabled)
}

func (s *Screen) sync() {
	if s == nil {
		return
	}
	if s.tree != nil {
		s.tree.sync()
	}
	if s.breadcrumb != nil {
		s.breadcrumb.SetPath(s.current)
		status := s.statusText()
		s.breadcrumb.SetStatus(status)
	}
	if s.actions != nil {
		s.rebuildActions()
	}
}

func (s *Screen) statusText() string {
	if s.errText != "" {
		return s.errText
	}
	if s.AsyncDirectoryLoading() {
		if s.asyncLoad != nil && s.asyncLoad.expand {
			return "Loading folder…"
		}
		return "Loading…"
	}
	count := s.VisibleCount()
	if s.mode == ModeFolder {
		return plural(count, "folder", "folders")
	}
	selected := len(s.SelectedPaths())
	if selected > 0 {
		return plural(count, "item", "items") + " · " + plural(selected, "selected", "selected")
	}
	return plural(count, "item", "items")
}

func (s *Screen) installModel(model *filetree.FileTreeModel) {
	s.model = model
	s.browseModel = model
	if s.tree != nil {
		s.tree.lastClickPath = ""
		s.tree.lastClickAt = time.Time{}
	}
	if model == nil {
		s.rebuildTree()
		return
	}
	s.configureModel()
	s.rebuildTree()
}

func (s *Screen) configureModel() {
	if s == nil || s.model == nil {
		return
	}
	model := s.model
	model.SetDirectoryLoader(func(path string) tea.Cmd { return s.requestChildLoad(path) })
	model.SetFileSelectedHandler(func(path string) tea.Cmd { return s.requestFileActivation(path) })
}

func (s *Screen) rebuildTree() {
	if s.tree == nil {
		t := ui.NewTree(ListID, s.tokens)
		s.tree = &pickerTree{Base: ui.NewBase(ListID), owner: s, tree: t}
	} else {
		s.tree.owner = s
	}
	cfg := filetree.PaintConfigFromTheme(nil)
	cfg.ShowGitStatus = false
	cfg.ShowDiagnostics = false
	cfg.Background = s.tokens.Color(ui.ColorPopupBg)
	cfg.BackgroundSet = true
	var icons *filetree.IconResolver
	if s.model != nil {
		icons = s.model.IconResolver()
	}
	if icons == nil {
		icons = filetree.NewIconResolver()
	}
	if s.th != nil {
		icons.ApplyTheme(s.th)
	}
	s.tree.tree.SetTokens(s.tokens)
	s.tree.tree.SetSurfaceBackground(ui.ColorPopupBg)
	s.tree.tree.SetRowPainter(filetree.NewTreeRowPainter(s.model, icons, cfg))
	if s.scrollbar == nil {
		s.scrollbar = ui.NewScrollbarControl(ScrollbarID, s.tree.tree.Scroll(), s.tokens)
		s.scrollbar.SetSurfaceBackground(ui.ColorPopupBg)
		s.scrollbar.OnScroll(func(_, _ int) {
			if s.model != nil {
				filetree.ApplyScrollToModel(s.model, s.tree.tree.Scroll())
			}
		})
	} else {
		s.scrollbar.SetScroll(s.tree.tree.Scroll())
	}
}

func (s *Screen) requestChildLoad(path string) tea.Cmd {
	intent, ok := s.nextIntent()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		return LoadMsg{Target: s, Path: cleanAbs(path), Epoch: intent.Epoch, Sequence: intent.Sequence}
	}
}

func (s *Screen) requestFileActivation(path string) tea.Cmd {
	if s == nil || s.model == nil {
		return nil
	}
	if s.mode == ModeOpenFile {
		if s.AsyncDirectoryLoading() {
			return nil
		}
		node := s.model.NodeByPath(cleanAbs(path))
		if node == nil || node.IsDir {
			return nil
		}
		return s.selectPath(cleanAbs(path), nil, false).Cmd
	}
	if s.mode != ModeFile {
		return nil
	}
	intent, ok := s.nextIntent()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		return ToggleMsg{Target: s, Path: cleanAbs(path), Epoch: intent.Epoch, Sequence: intent.Sequence}
	}
}

func (s *Screen) requestSearch(query string) tea.Cmd {
	intent, ok := s.nextIntent()
	if !ok {
		return nil
	}
	return func() tea.Msg {
		return SearchMsg{Target: s, Query: query, Epoch: intent.Epoch, Sequence: intent.Sequence}
	}
}

func (s *Screen) navigate(path string) ui.EventResult {
	if s == nil || !s.active {
		return ui.EventResult{Handled: true}
	}
	if s.searchActive {
		s.ClearPathSearch()
	}
	intent, ok := s.nextIntent()
	if !ok {
		return ui.EventResult{Handled: true}
	}
	message := NavigateMsg{Target: s, Path: cleanAbs(path), Epoch: intent.Epoch, Sequence: intent.Sequence}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return message }, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) confirm() ui.EventResult {
	if s == nil || !s.active || s.AsyncDirectoryLoading() {
		return ui.EventResult{Handled: true}
	}
	if s.mode == ModeFolder {
		return s.selectPath(s.current, nil, true)
	}
	if s.mode == ModeOpenFile {
		entry, ok := s.Selected()
		if !ok || entry.IsDir {
			return ui.EventResult{Handled: true}
		}
		return s.selectPath(entry.Path, nil, false)
	}
	paths := s.SelectedPaths()
	if len(paths) == 0 {
		return ui.EventResult{Handled: true}
	}
	return s.selectPath(paths[0], paths, false)
}

func (s *Screen) selectPath(path string, paths []string, folder bool) ui.EventResult {
	intent, ok := s.nextIntent()
	if !ok {
		return ui.EventResult{Handled: true}
	}
	message := SelectMsg{Target: s, Path: path, Paths: append([]string(nil), paths...), Folder: folder, Epoch: intent.Epoch, Sequence: intent.Sequence}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return message }, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) cancel() ui.EventResult {
	if s == nil || !s.active {
		return ui.EventResult{Handled: true}
	}
	if s.filter != nil && s.filter.field != nil && s.filter.field.Value() != "" {
		s.filter.field.SetValue("")
		if s.searchActive {
			s.ClearPathSearch()
		}
		s.sync()
		return ui.EventResult{Handled: true, Cmd: s.requestSearch(""), Invalidate: ui.InvalidatePaint}
	}
	intent, ok := s.nextIntent()
	if !ok {
		return ui.EventResult{Handled: true}
	}
	message := CancelMsg{Target: s, Epoch: intent.Epoch, Sequence: intent.Sequence}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return message }, Invalidate: ui.InvalidatePaint}
}

func (s *Screen) navigateParent() ui.EventResult {
	if s == nil || !s.active || s.AsyncDirectoryLoading() {
		return ui.EventResult{Handled: true}
	}
	parent := filepath.Dir(s.current)
	if parent == s.current {
		return ui.EventResult{Handled: true}
	}
	return s.navigate(parent)
}

func (s *Screen) nextIntent() (IntentToken, bool) {
	if s == nil || !s.active {
		return IntentToken{}, false
	}
	s.intentSeq++
	return IntentToken{Epoch: s.openEpoch, Sequence: s.intentSeq}, true
}

func (s *Screen) FocusedPath() string {
	if s == nil || s.model == nil {
		return ""
	}
	if node := s.model.SelectedNode(); node != nil {
		return node.Path
	}
	return s.current
}

// pickerFilter is a retained TextField wrapper. Search remains the first
// focus target; tree keys are routed here when the tree is focused.
func (p *pickerFilter) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return p.field.Measure(ctx, cs)
}

func (p *pickerFilter) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}

func (p *pickerFilter) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	p.field.Paint(buf, ox+p.Bounds().X, oy+p.Bounds().Y, ctx)
}

func (p *pickerFilter) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if p.owner == nil {
		return p.field.Handle(ctx, evt)
	}
	if evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			return p.owner.cancel()
		case key.String() == "ctrl+." && !p.owner.AsyncDirectoryLoading():
			query := p.field.Value()
			p.owner.ToggleHidden()
			if query != "" {
				return ui.EventResult{Handled: true, Cmd: p.owner.requestSearch(query), Invalidate: ui.InvalidatePaint}
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case key.String() == "ctrl+r" && !p.owner.AsyncDirectoryLoading():
			return p.owner.navigate(p.owner.current)
		case p.owner.AsyncDirectoryLoading():
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		case key.Code == tea.KeyEnter:
			return p.owner.confirmFiltered()
		case key.Code == tea.KeyBackspace && p.field.Edit().Len() == 0:
			return p.owner.navigateParent()
		case key.Code == tea.KeyUp || key.Code == tea.KeyDown || key.Code == tea.KeyPgUp || key.Code == tea.KeyPgDown || key.Code == tea.KeyHome || key.Code == tea.KeyEnd || key.Code == tea.KeyRight || key.Code == tea.KeyLeft:
			p.owner.FocusTree()
			return p.owner.tree.Handle(ui.EventContext{Focused: true}, evt)
		case key.Code == tea.KeyTab:
			if !key.Mod.Contains(tea.ModShift) {
				p.owner.FocusTree()
			} else {
				p.owner.focusID(ActionsID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	before := p.field.Value()
	result := p.field.Handle(ctx, evt)
	if p.owner != nil && before != p.field.Value() {
		result.Cmd = p.owner.requestSearch(p.field.Value())
	}
	return result
}

func (p *pickerFilter) Semantics() ui.Semantics       { return p.field.Semantics() }
func (p *pickerFilter) SetTokens(tokens *ui.TokenSet) { p.field.SetTokens(tokens) }

func (s *Screen) confirmFiltered() ui.EventResult {
	if s == nil || s.model == nil || s.AsyncDirectoryLoading() {
		return ui.EventResult{Handled: true}
	}
	if node := s.model.SelectedNode(); node != nil {
		if s.mode == ModeOpenFile {
			cmd := s.model.ToggleExpandOrSelect()
			s.sync()
			return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
		}
		if node.IsDir && s.mode == ModeFolder {
			return s.selectPath(node.Path, nil, true)
		}
		if !node.IsDir && s.mode == ModeFile {
			s.TogglePath(node.Path)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	return s.confirm()
}

func (p *pickerTree) SetTokens(tokens *ui.TokenSet) {
	if p == nil || p.tree == nil {
		return
	}
	p.tree.SetTokens(tokens)
}

func (p *pickerTree) sync() {
	if p == nil || p.owner == nil || p.tree == nil {
		return
	}
	model := p.owner.model
	if model == nil {
		p.tree.SetItems(nil)
		return
	}
	model.SetSelectionMarks(p.owner.selected)
	p.tree.SetItems(filetree.TreeItems(model))
	p.tree.SelectIndex(model.CursorIndex())
	p.tree.SyncHover(model.Hover.Index)
	filetree.SyncScrollModel(model, p.tree.Scroll(), p.Bounds().H)
	if p.owner.scrollbar != nil {
		p.owner.scrollbar.SetScroll(p.tree.Scroll())
	}
}

func (p *pickerTree) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return p.tree.Measure(ctx, cs)
}

func (p *pickerTree) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.tree.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}

func (p *pickerTree) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	p.tree.Paint(buf, ox+p.Bounds().X, oy+p.Bounds().Y, ctx)
}

func (p *pickerTree) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if p.owner == nil || p.owner.model == nil {
		return ui.EventResult{}
	}
	if p.owner.mode == ModeOpenFile && p.owner.AsyncDirectoryLoading() && (!evt.IsKey() || evt.Key.Code != tea.KeyEsc) {
		return ui.EventResult{Handled: true}
	}
	if evt.IsKey() {
		key := evt.Key
		if key.Code == tea.KeyEsc {
			return p.owner.cancel()
		}
		if key.Code == tea.KeyTab {
			if key.Mod.Contains(tea.ModShift) {
				p.owner.FocusFilter()
			} else {
				p.owner.focusID(ActionsID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		if key.String() == "ctrl+." {
			query := p.owner.filter.field.Value()
			p.owner.ToggleHidden()
			if query != "" {
				return ui.EventResult{Handled: true, Cmd: p.owner.requestSearch(query), Invalidate: ui.InvalidatePaint}
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		if key.String() == "ctrl+r" {
			return p.owner.navigate(p.owner.current)
		}
		if key.Code == ' ' && key.Mod == 0 {
			if node := p.owner.model.SelectedNode(); node != nil {
				if p.owner.mode == ModeFolder && node.IsDir {
					return p.owner.selectPath(node.Path, nil, true)
				}
				if p.owner.mode == ModeFile && !node.IsDir {
					return ui.EventResult{Handled: true, Cmd: p.owner.requestFileActivation(node.Path), Invalidate: ui.InvalidatePaint}
				}
			}
			return ui.EventResult{Handled: true}
		}
		if ui.IsCommitChord(key) {
			return p.owner.confirm()
		}
		if key.Text != "" && !key.Mod.Contains(tea.ModCtrl) && !key.Mod.Contains(tea.ModAlt) && !key.Mod.Contains(tea.ModSuper) {
			p.owner.FocusFilter()
			return p.owner.filter.Handle(ui.EventContext{Focused: true}, evt)
		}
		cmd := p.owner.model.Update(filetree.TreeKeyPressMsg{Key: key})
		p.owner.sync()
		return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
	}
	if evt.Kind == ui.EventCustom {
		if _, ok := evt.Custom.(tea.PasteMsg); ok {
			p.owner.FocusFilter()
			return p.owner.filter.Handle(ui.EventContext{Focused: true}, evt)
		}
	}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		node := p.nodeAt(ctx.LocalY)
		now := time.Now()
		doubleClick := node != nil && node.Path == p.lastClickPath && now.Sub(p.lastClickAt) <= 500*time.Millisecond
		if node != nil && (node.IsDir || p.owner.mode == ModeOpenFile) {
			p.lastClickPath = node.Path
			p.lastClickAt = now
		} else {
			p.lastClickPath = ""
			p.lastClickAt = time.Time{}
		}
		if doubleClick {
			p.lastClickPath = ""
			p.lastClickAt = time.Time{}
			if node.IsDir {
				return p.owner.navigate(node.Path)
			}
			return ui.EventResult{Handled: true, Cmd: p.owner.requestFileActivation(node.Path), Invalidate: ui.InvalidatePaint}
		}
		if node != nil && !node.IsDir && p.owner.mode == ModeOpenFile {
			p.owner.model.Select(node.Path)
			p.owner.sync()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	localY := ctx.LocalY
	cmd := p.owner.model.Update(filetree.TreeMouseMsg{Mouse: pickerMouseMessage(evt, localY)})
	p.owner.sync()
	return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidatePaint}
}

func (p *pickerTree) nodeAt(localY int) *filetree.TreeNode {
	if p == nil || p.owner == nil || p.owner.model == nil {
		return nil
	}
	idx := p.owner.model.ScrollOffset() + localY
	nodes := p.owner.model.VisibleNodes()
	if localY < 0 || localY >= p.Bounds().H || idx < 0 || idx >= len(nodes) {
		return nil
	}
	return nodes[idx]
}

func (p *pickerTree) Semantics() ui.Semantics {
	if p == nil || p.tree == nil {
		return ui.Semantics{Role: ui.RoleTree, Label: "File tree"}
	}
	return p.tree.Semantics()
}

func pickerMouseMessage(evt ui.Event, y int) tea.Msg {
	switch evt.Kind {
	case ui.EventMouseClick:
		m := evt.MouseClick
		m.Y = y
		return m
	case ui.EventMouseMotion:
		m := evt.MouseMotion
		m.Y = y
		return m
	case ui.EventMouseRelease:
		m := evt.MouseRelease
		m.Y = y
		return m
	case ui.EventMouseWheel:
		m := evt.MouseWheel
		m.Y = y
		return m
	default:
		return evt.Custom
	}
}

func (b *pickerBreadcrumb) SetTokens(tokens *ui.TokenSet) {
	b.tokens = tokens
	if b.icons != nil && tokens != nil {
		if th := tokens.Theme(); th != nil {
			b.icons.ApplyTheme(th)
		}
	}
}

func (b *pickerBreadcrumb) SetPath(path string) {
	if b != nil {
		b.path = cleanAbs(path)
	}
}

func (b *pickerBreadcrumb) SetStatus(status string) {
	if b != nil {
		b.status = status
	}
}

func (b *pickerBreadcrumb) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: 1})
}

func (b *pickerBreadcrumb) Layout(_ ui.LayoutContext, r ui.Rect) {
	b.SetBounds(r)
	b.parts = b.visibleParts(r.W)
}

func (b *pickerBreadcrumb) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	if b == nil || buf == nil || b.tokens == nil {
		return
	}
	r := b.Bounds()
	bg := b.tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	sep := " " + string(ui.FirstRune(b.tokens.Glyphs().BreadcrumbSep, '›')) + " "
	for i, part := range b.parts {
		if i > 0 {
			sepX := part.x - rendering.VisibleWidth(sep)
			buf.WriteString(ox+r.X+sepX, oy+r.Y, sep, b.tokens.Color(ui.ColorMuted), bg, 0)
		}
		fg := b.tokens.Color(ui.ColorMuted)
		attr := cell.Attrs(0)
		if i == len(b.parts)-1 {
			fg = b.tokens.Color(ui.ColorFg)
			attr = cell.AttrBold
		}
		if i == b.hover {
			fg = b.tokens.Color(ui.ColorAccent)
		}
		buf.WriteString(ox+r.X+part.x, oy+r.Y, part.label, fg, bg, attr)
	}
	if b.status != "" {
		statusW := rendering.VisibleWidth(b.status)
		x := ox + r.X + maxInt(0, r.W-statusW)
		buf.WriteString(x, oy+r.Y, rendering.TruncateLine(b.status, maxInt(1, statusW)), b.tokens.Color(ui.ColorMuted), bg, 0)
	}
}

func (b *pickerBreadcrumb) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if b == nil || b.owner == nil {
		return ui.EventResult{}
	}
	if evt.Kind == ui.EventMouseMotion {
		idx := b.partAt(ctx.LocalX)
		if idx != b.hover {
			b.hover = idx
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		idx := b.partAt(ctx.LocalX)
		if idx >= 0 && idx < len(b.parts) {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "filepicker.breadcrumb.navigate", Payload: b.parts[idx].path}, Invalidate: ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{}
}

func (b *pickerBreadcrumb) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Path", Value: b.path}
}

func (b *pickerBreadcrumb) visibleParts(width int) []breadcrumbPart {
	if b == nil || b.path == "" || width <= 0 {
		return nil
	}
	paths, labels := breadcrumbPathParts(b.path, b.icons)
	if len(paths) == 0 {
		return nil
	}
	sep := " " + string(ui.FirstRune(b.tokens.Glyphs().BreadcrumbSep, '›')) + " "
	statusW := rendering.VisibleWidth(b.status)
	available := width - statusW - 2
	if available < 1 {
		available = width
	}
	parts := make([]breadcrumbPart, 0, len(paths))
	for i := range paths {
		parts = append(parts, breadcrumbPart{path: paths[i], label: labels[i]})
	}
	measure := func(items []breadcrumbPart) int {
		n := 0
		for i, part := range items {
			if i > 0 {
				n += rendering.VisibleWidth(sep)
			}
			n += rendering.VisibleWidth(part.label)
		}
		return n
	}
	if measure(parts) > available && len(parts) > 2 {
		collapsed := make([]breadcrumbPart, 0, len(parts))
		collapsed = append(collapsed, parts[0])
		lastStart := len(parts) - 3
		if lastStart < 1 {
			lastStart = 1
		}
		collapsed = append(collapsed, breadcrumbPart{path: parts[lastStart].path, label: "…"})
		collapsed = append(collapsed, parts[lastStart+1:]...)
		parts = collapsed
		for measure(parts) > available && len(parts) > 3 {
			parts = append(parts[:2], parts[len(parts)-1:]...)
		}
	}
	x := 0
	for i := range parts {
		parts[i].x = x
		parts[i].w = rendering.VisibleWidth(parts[i].label)
		x += parts[i].w
		if i < len(parts)-1 {
			x += rendering.VisibleWidth(sep)
		}
	}
	return parts
}

func (b *pickerBreadcrumb) partAt(x int) int {
	for i, part := range b.parts {
		if x >= part.x && x < part.x+part.w {
			return i
		}
	}
	return -1
}

func breadcrumbPathParts(path string, icons *filetree.IconResolver) ([]string, []string) {
	clean := filepath.Clean(path)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	segments := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	rootPath := volume + string(filepath.Separator)
	rootLabel := "⌂ " + rootPath
	if volume == "" {
		rootPath = string(filepath.Separator)
	}
	paths := []string{rootPath}
	labels := []string{rootLabel}
	current := rootPath
	for _, segment := range segments {
		current = filepath.Join(current, segment)
		label := segment
		if icons != nil {
			if icon := icons.ResolveIcon(segment, true, false, true).Icon; icon != "" {
				label = icon + " " + segment
			}
		}
		paths = append(paths, current)
		labels = append(labels, label)
	}
	return paths, labels
}

func toTreeEntries(entries []Entry) []filetree.DirectoryEntry {
	result := make([]filetree.DirectoryEntry, 0, len(entries))
	for _, entry := range entries {
		if entry.Name == ".." || entry.Name == "" {
			continue
		}
		result = append(result, filetree.DirectoryEntry{
			Name: entry.Name, Path: entry.Path, IsDir: entry.IsDir, IsSymlink: entry.IsSymlink,
		})
	}
	return result
}

func cleanAbs(path string) string {
	if path == "" {
		path = "."
	}
	abs, err := filepath.Abs(path)
	if err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}

func plural(count int, singular, multiple string) string {
	word := multiple
	if count == 1 {
		word = singular
	}
	return strings.Join([]string{strconv.Itoa(count), word}, " ")
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
var _ ui.Component = (*pickerTree)(nil)
var _ ui.Component = (*pickerBreadcrumb)(nil)
