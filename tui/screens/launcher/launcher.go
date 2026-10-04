// Package launcher provides the workspace home surface.
package launcher

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID           = "launcher"
	NewSessionID       = "launcher/new-session"
	ActionErrorID      = "launcher/action-error"
	NewWorkspaceID     = "launcher/open-workspace"
	WorkspacesHeaderID = "launcher/workspaces-header"
	SearchID           = "launcher/search"
	WorkspaceListID    = "launcher/workspaces"
	ScrollbarID        = "launcher/scrollbar"
	EmptyID            = "launcher/empty"
	HintsID            = "launcher/hints"
	RetryID            = "launcher/retry"
	SkillsID           = "launcher/skills"
	MCPID              = "launcher/mcp"
	DividerID          = "launcher/divider"
)

type ItemKind int

const (
	ItemNewSession ItemKind = iota
	ItemOpenFolder
	ItemWorkspace
)

type WorkspaceEntry struct {
	ID, Name, Path string
	SessionCount   int
}
type Item struct {
	Kind      ItemKind
	Workspace *WorkspaceEntry
}
type ActivateMsg struct {
	Screen        *Screen
	Kind          ItemKind
	WorkspacePath string
}
type CommandMsg struct {
	Screen           *Screen
	Action, TargetID string
}

// Screen receives snapshots and emits owner-tagged intents; it never performs
// filesystem, provider, or persistence work.
type Screen struct {
	ui.Base
	tokens                    *ui.TokenSet
	registry                  *ui.ActionRegistry
	cwd, home                 string
	workspaces                []WorkspaceEntry
	filtered                  []int
	selected                  int // 0: new session, 1: open workspace, 2+: workspace index + 2
	filterOn                  bool
	focused                   ui.ComponentID
	focus                     func(ui.ComponentID) bool
	loading                   bool
	loadError                 string
	frame                     int
	newSession, openWorkspace *launcherAction
	actionError               *ui.Label
	divider                   *ui.SplitDivider
	split                     *ui.SplitModel
	skills, mcp               *capabilityPanel
	header                    *sectionHeader
	rule                      *ui.ProgressRule
	search                    *searchWrapper
	list                      *workspaceList
	scrollbar                 *ui.ScrollbarControl
	empty                     *workspaceEmpty
	retry                     *ui.ActionLink
	hints                     *ui.Hint
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: ui.NewTokenSet(th), registry: ui.DefaultRegistry()}
	s.home, _ = os.UserHomeDir()
	s.newSession = newLauncherAction(s, NewSessionID, ItemNewSession, " New Session", "")
	s.actionError = ui.NewLabel(ActionErrorID, "", th)
	s.actionError.SetColorRole(ui.ColorError)
	s.openWorkspace = newLauncherAction(s, NewWorkspaceID, ItemOpenFolder, "📁 New Workspace", "Open a folder as a workspace")
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetDividerSize(3)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetFill(true)
	s.divider.SetTransparent(true)
	s.divider.SetColorRole(ui.ColorMuted)
	s.header = &sectionHeader{Base: ui.NewBase(WorkspacesHeaderID), owner: s}
	s.rule = ui.NewProgressRule("launcher/rule", s.tokens)
	s.empty = &workspaceEmpty{Base: ui.NewBase(EmptyID), owner: s}
	field := ui.NewFilterField(SearchID, s.tokens)
	field.SetPrompt(ui.SearchGlyphEnd(s.tokens))
	field.SetPromptPlacement(ui.PromptEnd)
	field.SetPlaceholder("Search workspaces")
	field.SetSubmitAction("")
	s.search = &searchWrapper{Base: ui.NewBase(SearchID), owner: s, field: field}
	list := ui.NewVirtualList(WorkspaceListID, s.tokens)
	list.SetSelectionAnchorsScroll(false)
	list.SetOpaqueRowFill(false)
	s.list = &workspaceList{Base: ui.NewBase(WorkspaceListID), owner: s, list: list, hovered: -1}
	list.SetRowPainter(s.list.rowPainter)
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), s.tokens)
	s.retry = ui.NewActionLink(RetryID, "Retry", "launcher.retry")
	s.hints = ui.NewHint(HintsID, s.registry, s.tokens)
	s.skills = newCapabilityPanel(s, SkillsID, "Skills", "skills.manage")
	s.mcp = newCapabilityPanel(s, MCPID, "MCP", "mcp.manage")
	s.SetTokens(s.tokens)
	return s
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.newSession.SetTokens(tokens)
	s.actionError.SetTokens(tokens)
	s.openWorkspace.SetTokens(tokens)
	s.search.SetTokens(tokens)
	s.list.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.rule.SetTokens(tokens)
	s.retry.SetTokens(tokens)
	s.hints.SetTokens(tokens)
	s.skills.SetTokens(tokens)
	s.mcp.SetTokens(tokens)
	s.divider.SetTokens(tokens)
}
func (s *Screen) SetRegistry(registry *ui.ActionRegistry) {
	if registry == nil || s.registry == registry {
		return
	}
	s.registry = registry
	s.hints.SetRegistry(registry)
}
func (s *Screen) SetFocusHandler(fn func(ui.ComponentID) bool) { s.focus = fn; s.syncFocus() }

// SetActionError keeps an explicit startup failure beside the initiating action.
// The owning app retains the complete error in Notices for inspection and copy.
func (s *Screen) SetActionError(message string) { s.actionError.SetText(message) }

func (s *Screen) focusID(id ui.ComponentID) {
	s.focused = id
	if s.focus != nil {
		s.focus(id)
	}
}
func (s *Screen) SyncFocus() {
	if s.focused == "" {
		s.syncFocus()
	} else {
		s.focusID(s.focused)
	}
}
func (s *Screen) OwnsFocus(id ui.ComponentID) bool {
	return id == ScreenID || strings.HasPrefix(string(id), ScreenID+"/")
}
func (s *Screen) SetAnimation(frame int, _ float64) { s.frame = frame; s.rule.SetFrame(frame) }
func (s *Screen) Loading() bool                     { return s.loading }
func (s *Screen) BeginLoad(cwd string) {
	if !sameWorkspacePath(cwd, s.cwd) {
		s.filterOn = false
		s.search.field.SetValue("")
		s.selected = 0
		s.focused = NewSessionID
		s.SetData(cwd, nil)
		s.skills.reset()
		s.mcp.reset()
		s.list.list.Scroll().ScrollTo(0)
	}
	s.loading, s.loadError = true, ""
	s.rule.SetActive(true)
	if s.focused == RetryID {
		s.focusID(SearchID)
		s.filterOn = true
	}
}
func (s *Screen) FinishLoad(cwd string, infos []workspace.WorkspaceInfo, err error) {
	s.loading = false
	s.rule.SetActive(false)
	if err != nil {
		s.loadError = err.Error()
		return
	}
	s.loadError = ""
	s.SetData(cwd, infos)
}
func (s *Screen) SetData(cwd string, infos []workspace.WorkspaceInfo) {
	selectedPath := s.SelectedWorkspacePath()
	abs, err := filepath.Abs(cwd)
	if err != nil {
		abs = cwd
	}
	s.cwd = abs
	s.workspaces = nil
	for _, w := range infos {
		if w.ID == workspace.WorkspaceID(abs) || sameWorkspacePath(w.ProjectDir, abs) {
			continue
		}
		s.workspaces = append(s.workspaces, WorkspaceEntry{ID: w.ID, Name: workspace.WorkspaceDisplayName(w), Path: w.ProjectDir, SessionCount: w.SessionCount})
	}
	for i, w := range s.workspaces {
		if selectedPath != "" && sameWorkspacePath(w.Path, selectedPath) {
			s.selected = i + 2
			break
		}
	}
	s.rebuildItems()
}
func sameWorkspacePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	aa, ea := filepath.Abs(a)
	bb, eb := filepath.Abs(b)
	if ea != nil || eb != nil {
		return filepath.Clean(a) == filepath.Clean(b)
	}
	return filepath.Clean(aa) == filepath.Clean(bb)
}
func (s *Screen) FilterActive() bool { return s.filterOn }
func (s *Screen) Query() string      { return s.search.field.Value() }
func (s *Screen) TotalItems() int    { return 2 + len(s.workspaces) }
func (s *Screen) SelectedItem() *Item {
	if s.selected == 0 {
		return &Item{Kind: ItemNewSession}
	}
	if s.selected == 1 {
		return &Item{Kind: ItemOpenFolder}
	}
	index := s.selected - 2
	if index < 0 || index >= len(s.workspaces) {
		return nil
	}
	entry := s.workspaces[index]
	return &Item{Kind: ItemWorkspace, Workspace: &entry}
}
func (s *Screen) SelectedWorkspacePath() string {
	if item := s.SelectedItem(); item != nil && item.Workspace != nil {
		return item.Workspace.Path
	}
	return ""
}
func (s *Screen) ListScrollOffset() int    { return s.list.list.Scroll().Offset() }
func (s *Screen) SetListScroll(offset int) { s.list.list.Scroll().ScrollTo(offset) }
func (s *Screen) RemoveWorkspace(id string) {
	for i, w := range s.workspaces {
		if w.ID == id {
			s.workspaces = append(s.workspaces[:i], s.workspaces[i+1:]...)
			s.rebuildItems()
			return
		}
	}
}
func (s *Screen) rebuildItems() {
	query := strings.ToLower(strings.TrimSpace(s.Query()))
	s.filtered = nil
	for i, w := range s.workspaces {
		if query == "" || strings.Contains(strings.ToLower(w.Name), query) || strings.Contains(strings.ToLower(w.Path), query) {
			s.filtered = append(s.filtered, i)
		}
	}
	s.selected = max(0, min(s.selected, s.TotalItems()-1))
	if s.filterOn && s.selectedPosition() < 0 && len(s.filtered) > 0 {
		s.selected = s.filtered[0] + 2
	}
	s.syncList()
}
func (s *Screen) selectedPosition() int {
	for pos, index := range s.filtered {
		if index+2 == s.selected {
			return pos
		}
	}
	return -1
}
func (s *Screen) syncList() {
	rows := make([]ui.VirtItem, 0, len(s.filtered))
	for pos, index := range s.filtered {
		height := workspaceRowStride
		if pos == len(s.filtered)-1 {
			height = workspaceContentHeight
		}
		rows = append(rows, ui.VirtItem{ID: s.workspaces[index].Path, Height: height, Payload: index})
	}
	s.list.list.SetItems(rows)
	s.syncSelection()
}
func (s *Screen) syncSelection() {
	if pos := s.selectedPosition(); pos >= 0 {
		s.list.list.SelectIndex(pos)
	} else {
		s.list.list.Selection().ClearSelection()
	}
}
func (s *Screen) selectFlat(index int) {
	if index >= 0 && index < s.TotalItems() {
		s.selected = index
		s.syncSelection()
	}
}
func (s *Screen) syncFocus() {
	if s.filterOn {
		s.focusID(SearchID)
		return
	}
	switch s.selected {
	case 0:
		s.focusID(NewSessionID)
	case 1:
		s.focusID(NewWorkspaceID)
	default:
		s.focusID(WorkspaceListID)
	}
}
func (s *Screen) moveSelection(delta int) {
	if len(s.filtered) == 0 {
		if !s.filterOn {
			s.selected = max(0, min(1, s.selected+delta))
			s.syncFocus()
		}
		return
	}
	pos := s.selectedPosition()
	if s.selected < 2 && !s.filterOn {
		if delta < 0 {
			s.selected = max(0, s.selected-1)
		} else if s.selected == 0 {
			s.selected = 1
		} else {
			s.selected = s.filtered[0] + 2
		}
	} else {
		next := pos + delta
		if next < 0 && !s.filterOn {
			s.selected = 1
		} else {
			s.selected = s.filtered[max(0, min(len(s.filtered)-1, next))] + 2
		}
	}
	s.syncSelection()
	s.list.list.EnsureSelectedVisible()
	s.syncFocus()
}
func (s *Screen) activateFilter() {
	s.filterOn = true
	if len(s.filtered) > 0 && s.selectedPosition() < 0 {
		s.selected = s.filtered[0] + 2
	}
	s.syncSelection()
	s.list.list.EnsureSelectedVisible()
	s.focusID(SearchID)
}
func (s *Screen) deactivateFilter() {
	s.filterOn = false
	s.search.field.SetValue("")
	s.rebuildItems()
	s.syncFocus()
}
func (s *Screen) applyFilter() { s.rebuildItems(); s.list.list.Scroll().ScrollTo(0) }
func (s *Screen) activateSelected() tea.Cmd {
	item := s.SelectedItem()
	if item == nil || s.filterOn && s.selectedPosition() < 0 {
		return nil
	}
	msg := ActivateMsg{Screen: s, Kind: item.Kind}
	if item.Workspace != nil {
		msg.WorkspacePath = item.Workspace.Path
	}
	return func() tea.Msg { return msg }
}
func (s *Screen) command(action, id string) ui.EventResult {
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CommandMsg{Screen: s, Action: action, TargetID: id} }}
}
func (s *Screen) WorkspaceItemAt(x, y int) *WorkspaceEntry {
	index := s.list.indexAt(x, y)
	if index < 0 {
		return nil
	}
	entry := s.workspaces[index]
	return &entry
}
func (s *Screen) cycleFocus(backward bool) {
	var ids []ui.ComponentID
	for _, component := range []ui.Component{s.newSession, s.openWorkspace, s.skills, s.mcp, s.search} {
		if !component.Bounds().Empty() {
			ids = append(ids, component.ID())
		}
	}
	if len(s.filtered) > 0 {
		ids = append(ids, WorkspaceListID)
	}
	if s.loadError != "" {
		ids = append(ids, RetryID)
	}
	if len(ids) == 0 {
		return
	}
	current := 0
	for i, id := range ids {
		if id == s.focused {
			current = i
			break
		}
	}
	delta := 1
	if backward {
		delta = -1
	}
	next := ids[(current+delta+len(ids))%len(ids)]
	s.focusID(next)
	s.filterOn = next == SearchID || s.Query() != ""
	if next == NewSessionID {
		s.selected = 0
	}
	if next == NewWorkspaceID {
		s.selected = 1
	}
	if next == WorkspaceListID && s.selectedPosition() < 0 {
		s.selected = s.filtered[0] + 2
		s.syncSelection()
	}
}
func (s *Screen) handleKey(msg tea.KeyPressMsg) ui.EventResult {
	invalid := ui.InvalidatePaint | ui.InvalidateFocus
	switch msg.Code {
	case tea.KeyTab:
		s.cycleFocus(msg.Mod.Contains(tea.ModShift))
	case tea.KeyEsc:
		if s.filterOn {
			s.deactivateFilter()
			invalid |= ui.InvalidateLayout
		}
	case tea.KeyUp:
		s.moveSelection(-1)
	case tea.KeyDown:
		s.moveSelection(1)
	case tea.KeyPgUp:
		if !s.filterOn && s.selectedPosition() < max(1, s.list.Bounds().H/workspaceRowStride) {
			s.selected = 0
			s.syncSelection()
			s.list.list.Scroll().ScrollTo(0)
			s.syncFocus()
		} else {
			s.moveSelection(-max(1, s.list.Bounds().H/workspaceRowStride))
		}
	case tea.KeyPgDown:
		if s.selected < 2 && len(s.filtered) > 0 {
			s.selected = s.filtered[0] + 2
		}
		s.moveSelection(max(1, s.list.Bounds().H/workspaceRowStride))
	case tea.KeyHome:
		if s.filterOn && s.focused == SearchID {
			return ui.EventResult{}
		}
		s.selected = 0
		s.list.list.Scroll().ScrollTo(0)
		s.syncSelection()
		s.syncFocus()
	case tea.KeyEnd:
		if s.filterOn && s.focused == SearchID {
			return ui.EventResult{}
		}
		if len(s.filtered) > 0 {
			s.selected = s.filtered[len(s.filtered)-1] + 2
			s.syncSelection()
			s.list.list.EnsureSelectedVisible()
			s.syncFocus()
		}
	case tea.KeyEnter:
		return ui.EventResult{Handled: true, Cmd: s.activateSelected()}
	default:
		if s.filterOn {
			return ui.EventResult{}
		}
		switch msg.String() {
		case "/":
			s.activateFilter()
			invalid |= ui.InvalidateLayout
		case "j":
			s.moveSelection(1)
		case "k":
			s.moveSelection(-1)
		case "ctrl+c":
		default:
			return ui.EventResult{}
		}
	}
	return ui.EventResult{Handled: true, Invalidate: invalid}
}
func (s *Screen) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		return s.handleKey(event.Key)
	}
	return ui.EventResult{}
}
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if action.ID == "launcher.retry" {
		s.focused = RetryID
		return s.command(action.ID, "")
	}
	if action.ID == ui.ActionSplitResize {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace home"}
}
