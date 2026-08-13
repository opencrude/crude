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
	ListID       ui.ComponentID = "filepicker/list"
	ScrollbarID  ui.ComponentID = "filepicker/scrollbar"
	ActionsID    ui.ComponentID = "filepicker/actions"
)

type Mode uint8

const (
	ModeFile Mode = iota
	ModeFolder
)

type Entry struct {
	Name  string
	Path  string
	IsDir bool
}

type NavigateMsg struct {
	Target *Screen
	Path   string
}

type SelectMsg struct {
	Target *Screen
	Path   string
	Folder bool
}

type CancelMsg struct{ Target *Screen }

type Screen struct {
	ui.Base
	th         *theme.Theme
	tokens     *ui.TokenSet
	mode       Mode
	root       string
	current    string
	entries    []Entry
	visible    []Entry
	active     bool
	errText    string
	filter     *pickerFilter
	breadcrumb *ui.Label
	list       *pickerList
	scrollbar  *ui.ScrollbarControl
	actions    *ui.ActionBar
	focus      func(ui.ComponentID)
	icons      *filetree.IconResolver
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens, icons: filetree.NewIconResolver()}
	s.icons.ApplyTheme(&th.Colors.Tree)
	field := ui.NewFilterField(FilterID, tokens)
	field.SetPrompt(ui.SearchGlyphEnd(tokens))
	field.SetPromptPlacement(ui.PromptEnd)
	field.SetSubmitAction("")
	field.OnChanged(func(string) { s.sync() })
	s.filter = &pickerFilter{Base: ui.NewBase(FilterID), field: field, owner: s}
	s.breadcrumb = ui.NewLabel(BreadcrumbID, "", th)
	s.breadcrumb.SetColorRole(ui.ColorMuted)
	list := ui.NewList(ListID, tokens)
	list.SetActivateAction("filepicker.entry.activate")
	s.list = &pickerList{Base: ui.NewBase(ListID), list: list, owner: s, lastClick: -1}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)
	s.rebuildActions()
	s.sync()
	return s
}

func (s *Screen) Open(root string, mode Mode, entries []Entry) {
	s.mode = mode
	s.root = filepath.Clean(root)
	s.current = s.root
	s.active = true
	s.errText = ""
	if mode == ModeFolder {
		s.filter.field.SetPlaceholder("Search folders")
	} else {
		s.filter.field.SetPlaceholder("Search files")
	}
	s.filter.field.SetValue("")
	s.rebuildActions()
	s.setEntries(entries)
	s.sync()
}

func (s *Screen) SetDirectory(path string, entries []Entry) {
	s.current = filepath.Clean(path)
	s.errText = ""
	s.filter.field.SetValue("")
	s.list.lastClick = -1
	s.setEntries(entries)
	s.sync()
}

func (s *Screen) SetError(err error) {
	if err == nil {
		s.errText = ""
	} else {
		s.errText = err.Error()
	}
	s.syncBreadcrumb()
}

func (s *Screen) Close()                  { s.active = false }
func (s *Screen) IsOpen() bool            { return s.active }
func (s *Screen) Mode() Mode              { return s.mode }
func (s *Screen) Current() string         { return s.current }
func (s *Screen) VisibleCount() int       { return len(s.visible) }
func (s *Screen) Selected() (Entry, bool) { return s.selectedEntry() }

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *Screen) FocusFilter()                            { s.focusID(FilterID) }
func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) Children() []ui.Component {
	return []ui.Component{s.filter, s.breadcrumb, s.list, s.scrollbar, s.actions}
}
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.filter.SetTokens(tokens)
	s.breadcrumb.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.actions.SetTokens(tokens)
	if s.filter != nil && s.filter.field != nil {
		s.filter.field.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.filter.field.SetPromptPlacement(ui.PromptEnd)
	}
}
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	filterY := 0
	breadcrumbY := 2
	listY := 3
	if s.mode == ModeFolder {
		filterY = 3
		breadcrumbY = 5
		listY = 7
	}
	inset := 1
	s.filter.Layout(ctx, ui.Rect{X: inset, Y: filterY, W: max(1, r.W-inset*2), H: 1})
	s.breadcrumb.Layout(ctx, ui.Rect{X: inset, Y: breadcrumbY, W: max(1, r.W-inset*2), H: 1})
	listH := max(1, r.H-listY-2)
	s.list.Layout(ctx, ui.Rect{X: inset, Y: listY, W: max(1, r.W-inset-1), H: listH})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), Y: listY, W: 1, H: listH})
	s.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	if buf == nil || s.tokens == nil || s.mode != ModeFolder {
		return
	}
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorPopupBg)
	fg := s.tokens.Color(ui.ColorFg)
	accent := s.tokens.Color(ui.ColorAccent)
	muted := s.tokens.Color(ui.ColorMuted)
	ui.FillRect(buf, ox, oy, ui.Rect{X: r.X, Y: r.Y, W: r.W, H: min(3, r.H)}, cell.BlankCell(bg))
	x := ox + r.X + 1
	y := oy + r.Y
	x = buf.WriteString(x, y, "◆ crude", accent, bg, cell.AttrBold)
	if x < ox+r.X+r.W-1 {
		buf.WriteString(x, y, "  OPEN WORKSPACE", fg, bg, cell.AttrBold)
	}
	if r.H > 1 {
		buf.WriteString(ox+r.X+1, y+1,
			rendering.TruncateLine("Choose a project folder · fuzzy-search as you type", max(1, r.W-2)),
			muted, bg, 0)
	}
	if r.H > 2 {
		line := strings.Repeat(string(ui.FirstRune(s.tokens.Glyphs().BorderH, '─')), max(0, r.W))
		buf.WriteString(ox+r.X, y+2, line, s.tokens.Color(ui.ColorPopupDivider), bg, 0)
	}
}
func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "filepicker.entry.activate":
		entry, ok := action.Payload.(ui.ListItem)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		selected, ok := entry.Payload.(Entry)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		return s.activateEntry(selected)
	case "filepicker.select":
		if s.mode == ModeFolder {
			return s.selectPath(s.current, true)
		}
		if selected, ok := s.selectedEntry(); ok && !selected.IsDir {
			return s.selectPath(selected.Path, false)
		}
		return ui.EventResult{Handled: true}
	case "filepicker.cancel":
		return s.cancel()
	case "actionbar.exit.backward":
		if len(s.visible) > 0 {
			s.focusID(ListID)
		} else {
			s.focusID(FilterID)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.forward":
		s.focusID(FilterID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "File picker", Value: s.current}
}

func (s *Screen) rebuildActions() {
	primary := "Attach"
	if s.mode == ModeFolder {
		primary = "Open Workspace"
	}
	s.actions = ui.NewActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "select"), Label: primary, Action: "filepicker.select", Variant: ui.VariantPrimary, Enabled: s.mode == ModeFolder},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "cancel"), Label: "Cancel", Action: "filepicker.cancel", Variant: ui.VariantSecondary, Enabled: true},
	)
	s.actions.SetCancelAction("filepicker.cancel")
}

func (s *Screen) setEntries(entries []Entry) {
	s.entries = s.entries[:0]
	parent := filepath.Dir(s.current)
	if parent != s.current && (s.mode == ModeFolder || s.current != s.root) {
		s.entries = append(s.entries, Entry{Name: "..", Path: parent, IsDir: true})
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name, ".") || (s.mode == ModeFolder && !entry.IsDir) {
			continue
		}
		s.entries = append(s.entries, entry)
	}
	start := 0
	if len(s.entries) > 0 && s.entries[0].Name == ".." {
		start = 1
	}
	sort.Slice(s.entries[start:], func(i, j int) bool {
		left, right := s.entries[start+i], s.entries[start+j]
		if left.IsDir != right.IsDir {
			return left.IsDir
		}
		return strings.ToLower(left.Name) < strings.ToLower(right.Name)
	})
}

func (s *Screen) sync() {
	query := strings.ToLower(s.filter.field.Value())
	s.visible = s.visible[:0]
	type rankedEntry struct {
		entry Entry
		score int
		order int
	}
	ranked := make([]rankedEntry, 0, len(s.entries))
	for order, entry := range s.entries {
		score := 0
		if query != "" {
			if entry.Name == ".." {
				continue
			}
			var ok bool
			score, ok = fuzzyScore(entry.Name, query)
			if !ok {
				continue
			}
		}
		ranked = append(ranked, rankedEntry{entry: entry, score: score, order: order})
	}
	if query != "" {
		sort.SliceStable(ranked, func(i, j int) bool {
			if ranked[i].entry.IsDir != ranked[j].entry.IsDir {
				return ranked[i].entry.IsDir
			}
			if ranked[i].score != ranked[j].score {
				return ranked[i].score > ranked[j].score
			}
			return ranked[i].order < ranked[j].order
		})
	}
	items := make([]ui.ListItem, 0, len(ranked))
	for _, result := range ranked {
		entry := result.entry
		s.visible = append(s.visible, entry)
		label, icon := entry.Name, ""
		if entry.Name == ".." {
			label, icon = "Parent folder", "↰"
		} else if s.icons != nil {
			icon = s.icons.ResolveIcon(entry.Name, entry.IsDir, false, false).Icon
		}
		if s.mode == ModeFolder && entry.Name != ".." {
			label = string(ui.FirstRune(s.tokens.Glyphs().TreeCollapsed, '▸')) + " " + label
		}
		items = append(items, ui.ListItem{
			ID: entry.Path, Label: label, Icon: icon, Detail: pickerEntryDetail(entry), Payload: entry,
		})
	}
	s.list.list.SetItems(items)
	s.syncPrimary()
	s.syncBreadcrumb()
}

func (s *Screen) syncPrimary() {
	enabled := s.mode == ModeFolder
	if selected, ok := s.selectedEntry(); ok && !selected.IsDir {
		enabled = true
	}
	s.actions.SetEnabled("filepicker.select", enabled)
}

func (s *Screen) syncBreadcrumb() {
	detail := s.current
	if s.mode == ModeFolder {
		detail = s.folderBreadcrumb()
	}
	if s.errText != "" {
		detail += "  ·  " + s.errText
	} else {
		count := len(s.visible)
		if s.mode == ModeFolder {
			detail += "  ·  " + plural(count, "folder", "folders")
		} else {
			detail += " | " + plural(count, "item", "items")
		}
	}
	s.breadcrumb.SetText(detail)
}

func (s *Screen) folderBreadcrumb() string {
	clean := filepath.Clean(s.current)
	volume := filepath.VolumeName(clean)
	rest := strings.TrimPrefix(clean, volume)
	segments := strings.FieldsFunc(rest, func(r rune) bool { return r == '/' || r == '\\' })
	parts := make([]string, 0, len(segments)+1)
	root := volume + string(filepath.Separator)
	if volume == "" {
		root = string(filepath.Separator)
	}
	parts = append(parts, "⌂ "+root)
	for _, segment := range segments {
		icon := ""
		if s.icons != nil {
			icon = s.icons.ResolveIcon(segment, true, false, true).Icon
		}
		if icon != "" {
			parts = append(parts, icon+" "+segment)
		} else {
			parts = append(parts, segment)
		}
	}
	sep := " " + s.tokens.Glyphs().BreadcrumbSep + " "
	if strings.TrimSpace(sep) == "" {
		sep = " › "
	}
	return strings.Join(parts, sep)
}

func pickerEntryDetail(entry Entry) string {
	if entry.Name == ".." {
		return "Backspace"
	}
	if entry.IsDir {
		return ""
	}
	return "file"
}

// fuzzyScore performs a case-insensitive subsequence match. Contiguous runs,
// word starts, and early matches score higher, producing stable command-palette
// style results without recursively crawling the user's filesystem.
func fuzzyScore(value, query string) (int, bool) {
	valueRunes := []rune(strings.ToLower(value))
	queryRunes := []rune(strings.ToLower(strings.TrimSpace(query)))
	if len(queryRunes) == 0 {
		return 0, true
	}
	qi, score, last := 0, 0, -2
	for i, r := range valueRunes {
		if qi >= len(queryRunes) || r != queryRunes[qi] {
			continue
		}
		score += 10
		if i == last+1 {
			score += 8
		}
		if i == 0 || valueRunes[i-1] == '-' || valueRunes[i-1] == '_' || valueRunes[i-1] == ' ' {
			score += 6
		}
		score -= i
		last = i
		qi++
	}
	if qi != len(queryRunes) {
		return 0, false
	}
	return score, true
}

func (s *Screen) selectedEntry() (Entry, bool) {
	item, ok := s.list.list.Selected()
	if !ok {
		return Entry{}, false
	}
	entry, ok := item.Payload.(Entry)
	return entry, ok
}

func (s *Screen) activateEntry(entry Entry) ui.EventResult {
	if entry.IsDir {
		return s.navigate(entry.Path)
	}
	if s.mode == ModeFile {
		return s.selectPath(entry.Path, false)
	}
	return ui.EventResult{Handled: true}
}

func (s *Screen) navigate(path string) ui.EventResult {
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return NavigateMsg{Target: s, Path: path} }, Invalidate: ui.InvalidatePaint}
}
func (s *Screen) selectPath(path string, folder bool) ui.EventResult {
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return SelectMsg{Target: s, Path: path, Folder: folder} }}
}
func (s *Screen) cancel() ui.EventResult {
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CancelMsg{Target: s} }}
}
func (s *Screen) navigateParent() ui.EventResult {
	parent := filepath.Dir(s.current)
	if parent == s.current || (s.mode == ModeFile && s.current == s.root) {
		return ui.EventResult{Handled: true}
	}
	return s.navigate(parent)
}

type pickerFilter struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

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
		case key.String() == "ctrl+o" && p.owner.mode == ModeFolder:
			return p.owner.selectPath(p.owner.current, true)
		case key.Code == tea.KeyEnter:
			if selected, ok := p.owner.selectedEntry(); ok {
				return p.owner.activateEntry(selected)
			}
			if p.owner.mode == ModeFolder {
				return p.owner.selectPath(p.owner.current, true)
			}
			return ui.EventResult{Handled: true}
		case key.Code == tea.KeyBackspace && p.field.Edit().Len() == 0:
			return p.owner.navigateParent()
		case key.Code == tea.KeyUp || key.Code == tea.KeyDown || key.Code == tea.KeyPgUp || key.Code == tea.KeyPgDown || key.Code == tea.KeyHome || key.Code == tea.KeyEnd:
			p.owner.focusID(ListID)
			result := p.owner.list.list.Handle(ui.EventContext{Focused: true}, evt)
			p.owner.syncPrimary()
			return result
		case key.Code == tea.KeyTab:
			if len(p.owner.visible) > 0 && !key.Mod.Contains(tea.ModShift) {
				p.owner.focusID(ListID)
			} else {
				p.owner.focusID(ActionsID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	// OnChanged (see New) re-applies the filter after the shared engine edits.
	return p.field.Handle(ctx, evt)
}
func (p *pickerFilter) Semantics() ui.Semantics       { return p.field.Semantics() }
func (p *pickerFilter) SetTokens(tokens *ui.TokenSet) { p.field.SetTokens(tokens) }

type pickerList struct {
	ui.Base
	list          *ui.List
	owner         *Screen
	lastClick     int
	lastClickTime time.Time
}

func (p *pickerList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return p.list.Measure(ctx, cs)
}
func (p *pickerList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (p *pickerList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	p.list.Paint(buf, ox+p.Bounds().X, oy+p.Bounds().Y, ctx)
}
func (p *pickerList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if p.owner == nil {
		return p.list.Handle(ctx, evt)
	}
	if evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			return p.owner.cancel()
		case key.String() == "ctrl+o" && p.owner.mode == ModeFolder:
			return p.owner.selectPath(p.owner.current, true)
		case key.Code == tea.KeyBackspace:
			if p.owner.filter.field.Edit().Len() == 0 {
				return p.owner.navigateParent()
			}
			p.owner.focusID(FilterID)
			return p.owner.filter.Handle(ui.EventContext{Focused: true}, evt)
		case key.Code == tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				p.owner.focusID(FilterID)
			} else {
				p.owner.focusID(ActionsID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case key.Text != "" && !key.Mod.Contains(tea.ModCtrl) && !key.Mod.Contains(tea.ModAlt):
			p.owner.focusID(FilterID)
			return p.owner.filter.Handle(ui.EventContext{Focused: true}, evt)
		}
	}
	if evt.Kind == ui.EventCustom {
		if _, ok := evt.Custom.(tea.PasteMsg); ok {
			p.owner.focusID(FilterID)
			return p.owner.filter.Handle(ui.EventContext{Focused: true}, evt)
		}
	}
	result := p.list.Handle(ctx, evt)
	p.owner.syncPrimary()
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && result.Action != nil {
		index := p.list.SelectedIndex()
		now := time.Now()
		if p.lastClick != index || now.Sub(p.lastClickTime) > 500*time.Millisecond {
			p.lastClick, p.lastClickTime = index, now
			result.Action = nil
		}
	}
	return result
}
func (p *pickerList) Semantics() ui.Semantics       { return p.list.Semantics() }
func (p *pickerList) SetTokens(tokens *ui.TokenSet) { p.list.SetTokens(tokens) }

func plural(count int, singular, multiple string) string {
	word := multiple
	if count == 1 {
		word = singular
	}
	return strings.Join([]string{strconv.Itoa(count), word}, " ")
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
