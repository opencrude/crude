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
	ScreenID         ui.ComponentID = "local-history"
	ListID           ui.ComponentID = "local-history/list"
	ScrollbarID      ui.ComponentID = "local-history/scrollbar"
	ActionPreview                   = "history.preview"
	ActionApply                     = "history.apply"
	ActionBack                      = "history.back"
	ActionCheck                     = "history.check_result"
	ActionSafetyOpen                = "history.safety.open"
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
	AgentID   string
}
type PlanMsg struct {
	Plan  localhistory.RestorePlan
	Error string
}
type AppliedMsg struct {
	Result     localhistory.RestoreResult
	Error      string
	Warning    string
	RequestID  string // unresolved original mutation; only observation is allowed
	Incomplete bool   // retained safety evidence, not a fully completed restore
}
type PreviewRequest struct {
	SnapshotID string
	Paths      []string
	SessionID  string
	MessageID  string
	AgentID    string
}
type ApplyRequest struct {
	PlanID  string
	Choices []localhistory.RestoreChoice
}

type Screen struct {
	ui.Base
	tokens          *ui.TokenSet
	list            *ui.List
	scrollbar       *ui.ScrollbarControl
	timeline        []localhistory.SnapshotRecord
	plan            *localhistory.RestorePlan
	selected        map[string]bool
	exact           map[string]bool
	loading         bool
	errorText       string
	status          string
	armed           bool
	lastRestore     *localhistory.RestoreResult
	previewPaths    []string
	messageRefs     map[string]MessageRef
	requestID       string
	safetyAvailable bool
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
		hint = "Enter or U preview undo · Esc close"
	}
	if s.requestID != "" {
		hint = "Enter check the result · Esc close"
	}
	s.list.Paint(buf, ox+r.X, oy+r.Y, ctx)
	s.scrollbar.Paint(buf, ox+r.X, oy+r.Y, ctx)
	if detailH := s.detailHeight(r.H); detailH > 0 {
		s.paintDetail(buf, ox+r.X, oy+r.Y+r.H-2-detailH, r.W, detailH, bg, muted)
	}
	if s.plan != nil {
		title = "Restore preview"
		hint = "Space select · E replace conflict · Enter restore · Esc back"
		if len(s.plan.Changes) == 0 {
			hint = "The files match this snapshot · Esc back"
		}
	}
	if s.loading {
		title = "Loading Local History…"
	}
	if s.errorText != "" {
		title = "Local History cannot load"
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
			s.armed = false
			if msg.RequestID != "" {
				s.requestID = msg.RequestID
				s.errorText = msg.Error
				s.plan = nil
				s.lastRestore = nil
				s.list.SetItems([]ui.ListItem{{ID: "check-result", Label: "Check the restore result", Detail: "the reply did not arrive", Command: true}})
			} else if msg.Error != "" {
				s.errorText = msg.Error
				s.requestID = ""
				// A refusal invalidates the armed preview. Re-review current disk
				// state instead of leaving an old Apply action reachable.
				s.plan = nil
				s.rebuildTimeline()
			} else {
				s.errorText, s.requestID = "", ""
				s.status = "Restored " + fileCount(len(msg.Result.Paths)) + ". Press U to undo."
				if msg.Incomplete {
					s.status = "The restore did not complete. Press U to return to the files before the restore."
				}
				if msg.Warning != "" {
					s.status += " " + msg.Warning
				}
				copy := msg.Result
				s.lastRestore = &copy
				s.plan = nil
				s.list.SetItems([]ui.ListItem{{ID: "undo-restore", Label: "Undo this restore", Detail: "preview the files before the restore", Command: true}})
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

func (s *Screen) SetSafetyAvailable(available bool) {
	s.safetyAvailable = available
	if s.plan == nil && s.requestID == "" {
		s.rebuildTimeline()
	}
}

func (s *Screen) setTimeline(msg TimelineMsg) {
	s.loading = false
	s.errorText = msg.Error
	s.timeline = msg.Records
	s.previewPaths = append([]string(nil), msg.Paths...)
	s.messageRefs = msg.Messages
	s.lastRestore = nil
	s.requestID = ""
	if msg.Error == "" {
		prefix := ""
		if len(msg.Paths) > 0 {
			prefix = "History of " + strings.Join(msg.Paths, ", ") + " · "
		}
		s.status = prefix + countLabel(msg.Stats.Snapshots, "snapshot") + " · " + humanBytes(msg.Stats.PhysicalBytes) + " on disk"
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

// snapshotDescription names a snapshot by what the user did, not by the
// capture mechanism or internal identifiers.
func snapshotDescription(r localhistory.SnapshotRecord) (label, detail string) {
	switch r.Kind {
	case "before_message":
		return "Before agent changes", "your message"
	case "after_message":
		return "After agent changes", "agent turn ended"
	case "external_change", "reconcile", "automatic":
		return "File changes", "saved outside agent turns"
	case "startup_reconcile":
		return "File changes found at startup", "made while Crude was closed"
	case "baseline":
		return "First snapshot", "workspace opened"
	case "safety_before_restore":
		return "Before restore", "files before a restore"
	case "after_restore":
		return "After restore", "restored files"
	case "safety":
		if r.Label != "" {
			return r.Label, "kept for safety"
		}
		return "Safety snapshot", "kept for safety"
	case "manual":
		if r.Label != "" {
			return r.Label, "manual snapshot"
		}
		return "Manual snapshot", ""
	}
	if r.Label != "" {
		return r.Label, ""
	}
	return "Snapshot", ""
}

func snapshotTime(at, now time.Time) string {
	const day = "2006-01-02"
	switch at.Format(day) {
	case now.Format(day):
		return "Today     " + at.Format("15:04:05")
	case now.AddDate(0, 0, -1).Format(day):
		return "Yesterday " + at.Format("15:04:05")
	}
	if at.Year() == now.Year() {
		return at.Format("Jan 02    15:04:05")
	}
	return at.Format("2006-01-02 15:04")
}

func countLabel(n int, noun string) string {
	if n == 1 {
		return "1 " + noun
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func fileCount(n int) string { return countLabel(n, "file") }

func (s *Screen) rebuildTimeline() {
	items := make([]ui.ListItem, 0, len(s.timeline))
	if s.safetyAvailable {
		items = append(items, ui.ListItem{ID: "review-safety", Label: "Review safety references", Detail: "snapshots kept for reviewed edits", Command: true})
	}
	now := time.Now()
	for _, r := range s.timeline {
		label, detail := snapshotDescription(r)
		items = append(items, ui.ListItem{ID: r.ID, Label: snapshotTime(time.Unix(0, r.CreatedAt), now) + "  " + label, Detail: detail, Payload: r})
	}
	if len(items) == 0 && !s.loading {
		items = []ui.ListItem{{ID: "empty", Label: "No history snapshots yet", Disabled: true}}
	}
	s.list.SetItems(items)
}
func (s *Screen) setPlan(msg PlanMsg) {
	if s.requestID != "" {
		return
	}
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
			detail = "merged with later edits"
		}
		if c.ConflictReason != "" {
			detail = "conflict: " + c.ConflictReason + " · E to replace"
			if s.exact[c.Path] {
				detail = "replace · later edits are lost"
			}
		}
		if c.UnsafeReason != "" {
			detail = "cannot restore: " + c.UnsafeReason
		}
		items = append(items, ui.ListItem{ID: c.Path, Label: mark + " " + c.Path, Detail: detail, Disabled: c.UnsafeReason != "", Payload: c})
	}
	if len(items) == 0 {
		label := "The files match this snapshot"
		if s.plan.MessageID != "" {
			label = "This message changed no files"
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
	selected := !s.selected[c.Path]
	paths := c.AtomicPaths
	if len(paths) == 0 {
		paths = []string{c.Path}
	}
	for _, path := range paths {
		s.selected[path] = selected
	}
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
	if s.loading {
		return ui.EventResult{Handled: true}
	}
	if s.requestID != "" {
		s.loading = true
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionCheck, Payload: s.requestID}, Invalidate: ui.InvalidatePaint}
	}
	if s.plan == nil {
		item, ok := s.list.Selected()
		if !ok {
			return ui.EventResult{Handled: true}
		}
		if item.ID == "undo-restore" && s.lastRestore != nil {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionPreview, Payload: PreviewRequest{SnapshotID: s.lastRestore.SafetySnapshotID}}}
		}
		if item.ID == "review-safety" && s.safetyAvailable {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionSafetyOpen}}
		}
		r, ok := item.Payload.(localhistory.SnapshotRecord)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		s.loading = true
		request := PreviewRequest{SnapshotID: r.ID, Paths: append([]string(nil), s.previewPaths...)}
		if ref, ok := s.messageRefs[r.ID]; ok {
			request.SessionID, request.MessageID, request.AgentID = ref.SessionID, ref.MessageID, ref.AgentID
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
		s.status = "Select a file to restore. Press Space to select."
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
			s.status = fmt.Sprintf("Later edits in %s will be lost. Press Enter again to restore %s.", fileCount(exact), fileCount(len(choices)))
		} else {
			s.status = "Press Enter again to restore " + fileCount(len(choices)) + "."
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.loading = true
	s.armed = false
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionApply, Payload: ApplyRequest{PlanID: s.plan.ID, Choices: choices}}, Invalidate: ui.InvalidatePaint}
}
