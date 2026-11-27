package workspacehub

import (
	"fmt"
	"strings"
	"time"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	// StorageID is the Storage page.
	StorageID        ui.ComponentID = "workspace-hub/storage"
	storageMeasureID ui.ComponentID = "workspace-hub/storage/measure"
	// ActionMeasure measures the workspace folder again.
	ActionMeasure = "workspacehub.measure"
	// ActionCleanup asks to clean one kind of data (payload category ID).
	// The owner confirms before anything is removed.
	ActionCleanup = "workspacehub.cleanup"
)

// StorageData is what the Storage tab shows. Busy names the cleanup that is
// running; Notice reports the last cleanup in the user's words.
type StorageData struct {
	Loading    bool
	Problem    string
	Report     workspace.StorageReport
	Busy       string
	Notice     string
	NoticeRole ui.SemanticColor
	Read       time.Time
}

// category is how the tab names and colors one kind of data, and which
// cleanup, if any, it offers.
type category struct {
	label   string
	role    ui.SemanticColor
	cleanup string
}

var categories = map[string]category{
	workspace.StorageSessions:  {"Sessions", ui.ColorAccent, ""},
	workspace.StorageSubagents: {"Sub-agents", ui.ColorAccent2, ""},
	workspace.StorageTemporary: {"Temporary files", ui.ColorWarning, "Remove…"},
	workspace.StorageHistory:   {"Local History", ui.ColorSuccess, "Clean up…"},
	workspace.StorageWorktrees: {"Worktrees", ui.ColorDiffFile, "Remove finished…"},
	workspace.StorageIndex:     {"Workspace index", ui.ColorDiffHunk, ""},
	workspace.StorageOther:     {"Other", ui.ColorMuted, ""},
}

// Storage shows how much space Crude keeps for this workspace and offers the
// cleanups that have a safe owner.
type Storage struct {
	ui.Base
	tokens  *ui.TokenSet
	data    StorageData
	measure *ui.Button
	clean   map[string]*ui.Button
	scroll  pageScroll
	rows    map[string]int // content row of each category
	now     func() time.Time
}

// NewStorage builds an empty Storage tab that shows that it is measuring.
func NewStorage(tokens *ui.TokenSet) *Storage {
	s := &Storage{Base: ui.NewBase(StorageID), tokens: tokens, data: StorageData{Loading: true}, clean: map[string]*ui.Button{}, rows: map[string]int{}, now: time.Now}
	s.measure = ui.NewButton(storageMeasureID, "Measure again", ActionMeasure)
	s.measure.SetVariant(ui.VariantSecondary)
	for id, c := range categories {
		if c.cleanup != "" {
			button := ui.NewButton(ui.NewID(StorageID, "clean-"+id), c.cleanup, ActionCleanup+":"+id)
			button.SetVariant(ui.VariantSecondary)
			s.clean[id] = button
		}
	}
	s.scroll = newPageScroll(StorageID, tokens)
	s.SetTokens(tokens)
	return s
}

// SetTokens applies a theme.
func (s *Storage) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	s.tokens = tokens
	for _, button := range s.buttons() {
		button.SetTokens(tokens)
		button.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	}
	s.scroll.bar.SetTokens(tokens)
	s.scroll.bar.SetSurfaceBackground(ui.ColorBg)
}

func (s *Storage) buttons() []*ui.Button {
	out := []*ui.Button{s.measure}
	for _, id := range cleanupOrder {
		out = append(out, s.clean[id])
	}
	return out
}

var cleanupOrder = []string{workspace.StorageTemporary, workspace.StorageHistory, workspace.StorageWorktrees}

// SetData replaces what the tab shows.
func (s *Storage) SetData(data StorageData) {
	s.data = data
	busy := data.Loading || data.Busy != ""
	s.measure.SetDisabled(busy)
	for id, button := range s.clean {
		c := s.category(id)
		enabled := !busy && c.Bytes > 0
		if id == workspace.StorageTemporary {
			enabled = enabled && c.Reclaimable > 0
		}
		button.SetDisabled(!enabled)
		label := categories[id].cleanup
		if data.Busy == id {
			label = "Working…"
		}
		button.SetLabel(label)
	}
}

// Data is what the tab shows now.
func (s *Storage) Data() StorageData { return s.data }

func (s *Storage) category(id string) workspace.StorageCategory {
	for _, c := range s.data.Report.Categories {
		if c.ID == id {
			return c
		}
	}
	return workspace.StorageCategory{ID: id}
}

// PreferredFocus is the page itself, so the keyboard can scroll it.
func (s *Storage) PreferredFocus() ui.ComponentID { return StorageID }

// FocusOrder is the page, Measure again, then each enabled cleanup.
func (s *Storage) FocusOrder() []ui.ComponentID {
	order := []ui.ComponentID{StorageID}
	for _, button := range s.buttons() {
		if !button.Bounds().Empty() {
			order = append(order, button.ID())
		}
	}
	return order
}

// Children are the buttons on screen and the scrollbar.
func (s *Storage) Children() []ui.Component {
	var children []ui.Component
	for _, button := range s.buttons() {
		if !button.Bounds().Empty() {
			children = append(children, button)
		}
	}
	if !s.scroll.bar.Bounds().Empty() {
		children = append(children, s.scroll.bar)
	}
	return children
}

// Measure fills the space it is given.
func (s *Storage) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

// storageRowsTop is the first category row: the heading is row 1 and the
// stacked bar is row 3.
const storageRowsTop = 5

func (s *Storage) contentHeight() int {
	if len(s.data.Report.Categories) == 0 {
		return 4
	}
	return storageRowsTop + 2*len(s.data.Report.Categories) + 3
}

func (s *Storage) innerWidth(width int) int {
	inner := width - 2
	if s.scroll.bar.NeededForViewport(s.Bounds().H) {
		inner -= ui.ScrollbarWidth
	}
	return max(0, inner)
}

// Layout places Measure again in the heading row and each cleanup button at
// the end of its category's row, when those rows are on screen.
func (s *Storage) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	s.SetBounds(bounds)
	for _, button := range s.buttons() {
		button.Layout(ctx, ui.Rect{})
	}
	s.scroll.bar.Layout(ctx, ui.Rect{})
	if bounds.Empty() {
		return
	}
	s.scroll.model.SetViewport(bounds.H)
	s.scroll.model.SetContent(s.contentHeight())
	if s.scroll.bar.NeededForViewport(bounds.H) {
		s.scroll.bar.Layout(ctx, ui.Rect{X: bounds.W - ui.ScrollbarWidth, W: ui.ScrollbarWidth, H: bounds.H})
	}
	width, offset := s.innerWidth(bounds.W), s.scroll.model.Offset()
	place := func(button *ui.Button, row int) {
		w := button.Measure(ctx, ui.Loose(24, 1)).W
		if y := row - offset; y >= 0 && y < bounds.H && width > w+40 {
			button.Layout(ctx, ui.Rect{X: 1 + width - w, Y: y, W: w, H: 1})
		}
	}
	place(s.measure, 1)
	s.rows = map[string]int{}
	for index, c := range s.data.Report.Categories {
		s.rows[c.ID] = storageRowsTop + 2*index
		if button := s.clean[c.ID]; button != nil {
			place(button, s.rows[c.ID])
		}
	}
}

// Paint draws the heading, the stacked bar and one row per category.
func (s *Storage) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	bounds := s.Bounds()
	bg := s.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(bg))
	p := pen{buf: buf, tokens: s.tokens, bg: bg, x: originX + bounds.X + 1, y: originY + bounds.Y, offset: s.scroll.model.Offset(), viewport: bounds.H}
	width, now := s.innerWidth(bounds.W), s.now()
	report := s.data.Report
	switch {
	case s.data.Loading && len(report.Categories) == 0:
		p.text(0, 1, width, "Measuring the saved data of this workspace…", ui.ColorMuted, 0)
		return
	case s.data.Problem != "" && len(report.Categories) == 0:
		p.text(0, 1, width, s.data.Problem, ui.ColorError, 0)
		return
	}
	end := p.text(0, 1, width, "Crude keeps "+rendering.FormatBytes(report.Bytes)+" for this workspace", ui.ColorFg, cell.AttrBold)
	if !s.data.Read.IsZero() {
		state := "measured " + ui.RelativeAge(s.data.Read, now)
		if s.data.Loading {
			state = "measuring…"
		}
		p.text(end+1, 1, max(0, width-end-20), "· "+state, ui.ColorMuted, 0)
	}
	// One bar shows every category's share, in the colors of the rows below.
	x, last := 0, -1
	for index, c := range report.Categories {
		if c.Bytes > 0 {
			last = index
		}
	}
	for index, c := range report.Categories {
		if report.Bytes <= 0 || c.Bytes <= 0 {
			continue
		}
		cells := int(float64(c.Bytes) / float64(report.Bytes) * float64(width))
		if index == last || x+cells > width {
			cells = width - x
		}
		cells = max(1, cells)
		for i := range cells {
			if x+i < width {
				p.glyph(x+i, 3, '█', categories[c.ID].role)
			}
		}
		x += cells
	}
	for ; x < width; x++ {
		p.glyph(x, 3, '·', ui.ColorBorder)
	}
	shareW := min(20, max(0, width-70))
	for _, c := range report.Categories {
		row := s.rows[c.ID]
		info := categories[c.ID]
		p.glyph(0, row, '■', info.role)
		p.text(2, row, 18, info.label, ui.ColorFg, 0)
		p.right(20, row, 9, rendering.FormatBytes(c.Bytes), ui.ColorFg, cell.AttrBold)
		if shareW > 0 && report.Bytes > 0 {
			share := float64(c.Bytes) / float64(report.Bytes)
			p.bar(31, row, shareW, share, info.role)
			p.right(31+shareW, row, 5, fmt.Sprintf("%.0f%%", share*100), ui.ColorMuted, 0)
		}
		p.text(2, row+1, max(0, width-2), s.detail(c, now), ui.ColorMuted, 0)
	}
	if notice := s.data.Notice; notice != "" {
		row := storageRowsTop + 2*len(report.Categories) + 1
		role := s.data.NoticeRole
		if role == 0 {
			role = ui.ColorMuted
		}
		p.text(0, row, width, notice, role, 0)
	}
}

// detail is the second line of a category: what it holds and when.
func (s *Storage) detail(c workspace.StorageCategory, now time.Time) string {
	dates := DateRange(c.Oldest, c.Newest, now)
	switch c.ID {
	case workspace.StorageSessions:
		return join(plural(c.Items, "session"), dates)
	case workspace.StorageSubagents:
		return join(plural(c.Items, "sub-agent"), dates)
	case workspace.StorageTemporary:
		if c.Reclaimable > 0 {
			return rendering.FormatBytes(c.Reclaimable) + " in closed sessions can be removed. Open sessions keep theirs."
		}
		if c.Bytes > 0 {
			return "Only open sessions have temporary files now."
		}
		return "No temporary files."
	case workspace.StorageHistory:
		return join("Copies of your files before and after each change", dates)
	case workspace.StorageWorktrees:
		if c.Items == 0 {
			return "No separate checkouts."
		}
		return join(plural(c.Items, "agent checkout"), "finished ones without unsaved work can be removed")
	case workspace.StorageIndex:
		return "Session list, settings and notices"
	}
	return "Locks and other small files"
}

func plural(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return Count(int64(n)) + " " + noun + "s"
}

func join(parts ...string) string {
	out := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		if out != "" {
			out += " · "
		}
		out += part
	}
	return out
}

// Handle scrolls the page.
func (s *Storage) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if s.scroll.handle(evt) {
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

// HandleAction names the category of a pressed cleanup button.
func (s *Storage) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if id, ok := strings.CutPrefix(action.ID, ActionCleanup+":"); ok {
		if button := s.clean[id]; button != nil && !button.Bounds().Empty() {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionCleanup, Payload: id}}
		}
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{Action: &action}
}

// Semantics describes the page.
func (s *Storage) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace storage", Description: rendering.FormatBytes(s.data.Report.Bytes)}
}

var _ ui.Parent = (*Storage)(nil)
var _ ui.ActionHandler = (*Storage)(nil)
