// Package filepicker provides the native file and workspace folder picker body.
package filepicker

import (
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/filetree"
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
	s := &Screen{Base: ui.NewBase(ScreenID), tokens: tokens, icons: filetree.NewIconResolver()}
	s.icons.ApplyTheme(&th.Colors.Tree)
	field := ui.NewTextField(FilterID, tokens)
	field.SetPrompt("> ")
	field.SetSubmitAction("")
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
}
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.filter.Layout(ctx, ui.Rect{W: r.W, H: 1})
	s.breadcrumb.Layout(ctx, ui.Rect{Y: 2, W: r.W, H: 1})
	listY := 3
	listH := max(1, r.H-listY-2)
	s.list.Layout(ctx, ui.Rect{X: 0, Y: listY, W: max(1, r.W-1), H: listH})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-1), Y: listY, W: 1, H: listH})
	s.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}
func (s *Screen) Paint(_ *surface.CellBuffer, _, _ int, _ ui.PaintContext) {}
func (s *Screen) Handle(_ ui.EventContext, _ tea.Msg) ui.EventResult       { return ui.EventResult{} }
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
		primary = "Create workspace"
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
	items := make([]ui.ListItem, 0, len(s.entries))
	for _, entry := range s.entries {
		if query != "" && !strings.Contains(strings.ToLower(entry.Name), query) {
			continue
		}
		s.visible = append(s.visible, entry)
		label, icon := entry.Name, ""
		if entry.Name == ".." {
			label, icon = "Parent folder", "↰"
		} else if s.icons != nil {
			icon = s.icons.ResolveIcon(entry.Name, entry.IsDir, false, false).Icon
		}
		items = append(items, ui.ListItem{ID: entry.Path, Label: label, Icon: icon, Payload: entry})
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
	if s.errText != "" {
		detail += " | " + s.errText
	} else {
		count := len(s.visible)
		detail += " | " + plural(count, "item", "items")
	}
	s.breadcrumb.SetText(detail)
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
	field *ui.TextField
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
func (p *pickerFilter) Handle(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
	if p.owner == nil {
		return p.field.Handle(ctx, msg)
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
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
			result := p.owner.list.list.Handle(ui.EventContext{Focused: true}, msg)
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
	before := p.field.Value()
	result := p.field.Handle(ctx, msg)
	if p.field.Value() != before {
		p.owner.sync()
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return result
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
func (p *pickerList) Handle(ctx ui.EventContext, msg tea.Msg) ui.EventResult {
	if p.owner == nil {
		return p.list.Handle(ctx, msg)
	}
	if key, ok := msg.(tea.KeyPressMsg); ok {
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
			return p.owner.filter.Handle(ui.EventContext{Focused: true}, msg)
		case key.Code == tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				p.owner.focusID(FilterID)
			} else {
				p.owner.focusID(ActionsID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case key.Text != "" && !key.Mod.Contains(tea.ModCtrl) && !key.Mod.Contains(tea.ModAlt):
			p.owner.focusID(FilterID)
			return p.owner.filter.Handle(ui.EventContext{Focused: true}, msg)
		}
	}
	if _, ok := msg.(tea.PasteMsg); ok {
		p.owner.focusID(FilterID)
		return p.owner.filter.Handle(ui.EventContext{Focused: true}, msg)
	}
	result := p.list.Handle(ctx, msg)
	p.owner.syncPrimary()
	click, clicked := msg.(tea.MouseClickMsg)
	if clicked && click.Button == tea.MouseLeft && result.Action != nil {
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
