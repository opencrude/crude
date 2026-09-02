package localhistory

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/localhistory"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID      ui.ComponentID = "local-history"
	ListID        ui.ComponentID = "local-history/list"
	ScrollbarID   ui.ComponentID = "local-history/scrollbar"
	ActionPreview                = "history.preview"
	ActionApply                  = "history.apply"
	ActionBack                   = "history.back"
)

type TimelineMsg struct {
	Records  []localhistory.SnapshotRecord
	Stats    localhistory.Stats
	Paths    []string
	Messages map[string]MessageRef
	Error    string
}
type MessageRef struct {
	SessionID string
	MessageID string
}
type PlanMsg struct {
	Plan  localhistory.RestorePlan
	Error string
}
type AppliedMsg struct {
	Result  localhistory.RestoreResult
	Error   string
	Warning string
}
type PreviewRequest struct {
	SnapshotID string
	Paths      []string
	SessionID  string
	MessageID  string
}
type ApplyRequest struct {
	PlanID  string
	Choices []localhistory.RestoreChoice
}

type Screen struct {
	ui.Base
	tokens       *ui.TokenSet
	list         *ui.List
	scrollbar    *ui.ScrollbarControl
	timeline     []localhistory.SnapshotRecord
	plan         *localhistory.RestorePlan
	selected     map[string]bool
	exact        map[string]bool
	loading      bool
	errorText    string
	status       string
	armed        bool
	lastRestore  *localhistory.RestoreResult
	previewPaths []string
	messageRefs  map[string]MessageRef
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	list := ui.NewList(ListID, tokens)
	list.SetWrap(false)
	list.SetActivateAction("history.activate")
	return &Screen{Base: ui.NewBase(ScreenID), tokens: tokens, list: list, scrollbar: ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens), selected: make(map[string]bool), exact: make(map[string]bool), loading: true}
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
}
func (s *Screen) Children() []ui.Component { return nil }
func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 72, H: 24})
}
func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	bodyH := max(1, r.H-2-s.detailHeight(r.H))
	barW := 0
	if s.scrollbar.NeededForViewport(bodyH) {
		barW = 1
	}
	s.list.Layout(ctx, ui.Rect{W: max(1, r.W-barW), H: bodyH})
	s.scrollbar.Layout(ctx, ui.Rect{X: r.W - barW, W: barW, H: bodyH})
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorPopupBg)
	fg := s.tokens.Color(ui.ColorFg)
	muted := s.tokens.Color(ui.ColorMuted)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	title := "Timeline"
	hint := "Enter preview · Esc close"
	if s.lastRestore != nil {
		hint = "Enter/U preview undo of restore · Esc close"
	}
	s.list.Paint(buf, ox+r.X, oy+r.Y, ctx)
	s.scrollbar.Paint(buf, ox+r.X, oy+r.Y, ctx)
	if detailH := s.detailHeight(r.H); detailH > 0 {
		s.paintDetail(buf, ox+r.X, oy+r.Y+r.H-2-detailH, r.W, detailH, bg, muted)
	}
	if s.plan != nil {
		title = "Restore preview"
		hint = "Space select · E exact · Enter review/apply · Esc back"
		if len(s.plan.Changes) == 0 {
			hint = "Snapshot matches the workspace · Esc back"
		}
	}
	if s.loading {
		title = "Loading local history…"
	}
	if s.errorText != "" {
		title = "Local history error"
	}
	buf.WriteString(ox+r.X, oy+r.Y+r.H-2, truncate(title, r.W), fg, bg, cell.AttrBold)
	line := hint
	if s.errorText != "" {
		line = s.errorText
	} else if s.status != "" {
		line = s.status
	}
	buf.WriteString(ox+r.X, oy+r.Y+r.H-1, truncate(line, r.W), muted, bg, 0)
}

func (s *Screen) detailHeight(total int) int {
	if s.plan == nil || len(s.plan.Changes) == 0 {
		return 0
	}
	return min(8, max(3, total/3))
}

func (s *Screen) paintDetail(buf *surface.CellBuffer, x, y, w, h int, bg, fg cell.Color) {
	if h < 1 {
		return
	}
	for col := 0; col < w; col++ {
		buf.Set(x+col, y, cell.NewCell('─', s.tokens.Color(ui.ColorPopupDivider), bg, 0))
	}
	change, ok := s.selectedChange()
	if !ok {
		return
	}
	header := string(change.Action) + " · " + change.Path
	if change.AutoMerged {
		header += " · auto-merged"
	}
	if change.ConflictReason != "" {
		header += " · " + change.ConflictReason
	}
	buf.WriteString(x, y+1, truncate(header, w), fg, bg, cell.AttrBold)
	lines := strings.Split(change.TextDiff, "\n")
	for row := 2; row < h && row-2 < len(lines); row++ {
		line := lines[row-2]
		color := fg
		if strings.HasPrefix(line, "+") {
			color = s.tokens.Color(ui.ColorDiffAddition)
		} else if strings.HasPrefix(line, "-") {
			color = s.tokens.Color(ui.ColorDiffDeletion)
		}
		buf.WriteString(x, y+row, rendering.TruncateVisual(line, w, "…"), color, bg, 0)
	}
}

func truncate(text string, width int) string { return rendering.TruncateVisual(text, width, "…") }

func (s *Screen) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.Kind == ui.EventCustom {
		switch msg := evt.Custom.(type) {
		case TimelineMsg:
			s.setTimeline(msg)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		case PlanMsg:
			s.setPlan(msg)
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		case AppliedMsg:
			s.loading = false
			if msg.Error != "" {
				s.errorText = msg.Error
			} else {
				s.status = fmt.Sprintf("Restored %d path(s). Safety snapshot %s", len(msg.Result.Paths), short(msg.Result.SafetySnapshotID))
				if msg.Warning != "" {
					s.status += " · warning: " + msg.Warning
				}
				copy := msg.Result
				s.lastRestore = &copy
				s.plan = nil
				s.list.SetItems([]ui.ListItem{{ID: "undo-restore", Label: "Undo this restore", Detail: "preview safety snapshot", Command: true}})
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
	}
	if evt.IsKey() {
		if (evt.Key.Code == 'u' || evt.Key.Code == 'U') && s.lastRestore != nil {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionPreview, Payload: PreviewRequest{SnapshotID: s.lastRestore.SafetySnapshotID}}}
		}
		switch evt.Key.Code {
		case tea.KeyEsc:
			if s.plan != nil {
				s.plan = nil
				s.rebuildTimeline()
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionBack}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
			}
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "window.close"}}
		case tea.KeySpace:
			if s.plan != nil {
				s.toggleSelected()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
		case 'e', 'E':
			if s.plan != nil {
				s.toggleExact()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
		case tea.KeyEnter:
			return s.activate()
		}
	}
	if s.scrollbar.IsDragging() || s.scrollbar.PointerHitBounds(s.scrollbar.Bounds()).Contains(ctx.LocalX, ctx.LocalY) {
		res := s.scrollbar.Handle(ui.ChildEventContext(ctx, s.scrollbar.Bounds()), evt)
		if res.Handled {
			return res
		}
	}
	res := s.list.Handle(ui.ChildEventContext(ctx, s.list.Bounds()), evt)
	if res.Action != nil && res.Action.ID == "history.activate" {
		return s.activate()
	}
	return res
}

func (s *Screen) CancelPointerCapture() { s.scrollbar.CancelPointerCapture() }

func (s *Screen) setTimeline(msg TimelineMsg) {
	s.loading = false
	s.errorText = msg.Error
	s.timeline = msg.Records
	s.previewPaths = append([]string(nil), msg.Paths...)
	s.messageRefs = msg.Messages
	s.lastRestore = nil
	if msg.Error == "" {
		prefix := ""
		if len(msg.Paths) > 0 {
			prefix = "History for " + strings.Join(msg.Paths, ", ") + " · "
		}
		s.status = prefix + fmt.Sprintf("%d snapshots · %s stored · %d chunk objects", msg.Stats.Snapshots, humanBytes(msg.Stats.PhysicalBytes), msg.Stats.ChunkObjects)
	}
	s.plan = nil
	s.rebuildTimeline()
}
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for value := n / unit; value >= unit; value /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
func (s *Screen) rebuildTimeline() {
	items := make([]ui.ListItem, 0, len(s.timeline))
	for _, r := range s.timeline {
		label := r.Label
		if label == "" {
			label = r.Kind
		}
		items = append(items, ui.ListItem{ID: r.ID, Label: time.Unix(0, r.CreatedAt).Format("2006-01-02 15:04:05") + "  " + label, Detail: r.Kind, Payload: r})
	}
	if len(items) == 0 && !s.loading {
		items = []ui.ListItem{{ID: "empty", Label: "No history snapshots yet", Disabled: true}}
	}
	s.list.SetItems(items)
}
func (s *Screen) setPlan(msg PlanMsg) {
	s.loading = false
	s.errorText = msg.Error
	s.status = ""
	if msg.Error != "" {
		return
	}
	s.plan = &msg.Plan
	s.armed = false
	s.selected = make(map[string]bool)
	s.exact = make(map[string]bool)
	for _, c := range msg.Plan.Changes {
		if c.UnsafeReason == "" && c.ConflictReason == "" {
			s.selected[c.Path] = true
		}
	}
	s.rebuildPlan()
}
func (s *Screen) rebuildPlan() {
	if s.plan == nil {
		return
	}
	items := make([]ui.ListItem, 0, len(s.plan.Changes))
	for _, c := range s.plan.Changes {
		mark := "[ ]"
		if s.selected[c.Path] {
			mark = "[x]"
		}
		detail := string(c.Action)
		if c.MovePeer != "" {
			detail = "move · " + c.MovePeer
		}
		if c.AutoMerged {
			detail = "auto-merged"
		}
		if c.ConflictReason != "" {
			detail = "conflict: " + c.ConflictReason
			if s.exact[c.Path] {
				detail = "EXACT replacement"
			}
		}
		if c.UnsafeReason != "" {
			detail = "blocked: " + c.UnsafeReason
		}
		items = append(items, ui.ListItem{ID: c.Path, Label: mark + " " + c.Path, Detail: detail, Disabled: c.UnsafeReason != "", Payload: c})
	}
	if len(items) == 0 {
		label := "No workspace differences from this snapshot"
		if s.plan.MessageID != "" {
			label = "No changes from this message"
		}
		items = []ui.ListItem{{ID: "empty", Label: label, Disabled: true}}
	}
	s.list.SetItems(items)
}
func (s *Screen) selectedChange() (localhistory.Change, bool) {
	item, ok := s.list.Selected()
	if !ok {
		return localhistory.Change{}, false
	}
	c, ok := item.Payload.(localhistory.Change)
	return c, ok
}
func (s *Screen) toggleSelected() {
	c, ok := s.selectedChange()
	if !ok || c.UnsafeReason != "" {
		return
	}
	if c.ConflictReason != "" && !s.exact[c.Path] {
		return
	}
	s.selected[c.Path] = !s.selected[c.Path]
	s.armed = false
	s.status = ""
	s.rebuildPlan()
}
func (s *Screen) toggleExact() {
	c, ok := s.selectedChange()
	if !ok || c.ConflictReason == "" || !c.ExactAllowed {
		return
	}
	s.exact[c.Path] = !s.exact[c.Path]
	s.armed = false
	s.status = ""
	if s.exact[c.Path] {
		s.selected[c.Path] = true
	} else {
		s.selected[c.Path] = false
	}
	s.rebuildPlan()
}
func (s *Screen) activate() ui.EventResult {
	if s.plan == nil {
		item, ok := s.list.Selected()
		if !ok {
			return ui.EventResult{Handled: true}
		}
		if item.ID == "undo-restore" && s.lastRestore != nil {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionPreview, Payload: PreviewRequest{SnapshotID: s.lastRestore.SafetySnapshotID}}}
		}
		r, ok := item.Payload.(localhistory.SnapshotRecord)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		s.loading = true
		request := PreviewRequest{SnapshotID: r.ID, Paths: append([]string(nil), s.previewPaths...)}
		if ref, ok := s.messageRefs[r.ID]; ok {
			request.SessionID, request.MessageID = ref.SessionID, ref.MessageID
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionPreview, Payload: request}, Invalidate: ui.InvalidatePaint}
	}
	choices := make([]localhistory.RestoreChoice, 0)
	for _, c := range s.plan.Changes {
		if s.selected[c.Path] {
			choices = append(choices, localhistory.RestoreChoice{Path: c.Path, Exact: s.exact[c.Path]})
		}
	}
	if len(choices) == 0 {
		s.status = "Select at least one safe path"
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	if !s.armed {
		s.armed = true
		exact := 0
		for _, choice := range choices {
			if choice.Exact {
				exact++
			}
		}
		if exact > 0 {
			s.status = fmt.Sprintf("WARNING: %d exact replacement(s) overwrite later edits. Press Enter again.", exact)
		} else {
			s.status = fmt.Sprintf("Ready to restore %d path(s). Press Enter again to confirm.", len(choices))
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.loading = true
	s.armed = false
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionApply, Payload: ApplyRequest{PlanID: s.plan.ID, Choices: choices}}, Invalidate: ui.InvalidatePaint}
}
func short(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
