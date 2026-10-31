package localhistory

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	SafetyScreenID        ui.ComponentID = "local-history/safety"
	safetyListID          ui.ComponentID = "local-history/safety/list"
	safetyScrollbarID     ui.ComponentID = "local-history/safety/scrollbar"
	safetyTextID          ui.ComponentID = "local-history/safety/text"
	safetyActionsID       ui.ComponentID = "local-history/safety/actions"
	ActionSafetyInspect                  = "history.safety.inspect"
	ActionSafetyNext                     = "history.safety.next"
	ActionSafetyRefresh                  = "history.safety.refresh"
	ActionSafetyBack                     = "history.safety.back"
	ActionSafetyRelease                  = "history.safety.release"
	ActionSafetyReprotect                = "history.safety.reprotect"
	ActionSafetyRestore                  = "history.safety.restore"
	ActionSafetyCheck                    = "history.safety.check"
	safetyActivate                       = "history.safety.activate"
)

type safetyView uint8

const (
	safetyLoading safetyView = iota
	safetyPage
	safetyDetail
	safetyError
	safetyPending
)

// SafetyScreen is only the body of the existing Local History window. Its
// detached projections grant no domain authority; the caller owns async work,
// confirmation, generations, receipts and restore preview.
type SafetyScreen struct {
	ui.Base
	tokens    *ui.TokenSet
	list      *safetyList
	scrollbar *ui.ScrollbarControl
	text      *safetyText
	actions   *ui.ActionBar
	view      safetyView
	page      workspace.HistorySafetyPage
	detail    workspace.HistorySafetyDetail
	requestID string
	busy      bool
}

func NewSafety(th *theme.Theme) *SafetyScreen {
	if th == nil {
		th = theme.DefaultDark()
	}
	ts := ui.NewTokenSet(th)
	s := &SafetyScreen{Base: ui.NewBase(SafetyScreenID), tokens: ts}
	s.list = &safetyList{List: ui.NewList(safetyListID, ts), owner: s}
	s.list.SetActivateAction(safetyActivate)
	s.list.SetDefaultRowBackground(ui.ColorPopupBg)
	s.scrollbar = ui.NewScrollbarControl(safetyScrollbarID, s.list.Scroll(), ts)
	s.text = &safetyText{TextArea: ui.NewTextArea(safetyTextID, ts, true)}
	s.text.SetReadOnly(true)
	s.actions = ui.NewActionBar(safetyActionsID, ts)
	s.actions.SetShowRule(true)
	s.actions.SetSurfaceBackground(ui.ColorPopupBg)
	s.actions.SetCancelAction(ActionSafetyBack)
	s.SetTokens(ts)
	s.SetLoading("Loading review safety references…")
	return s
}

func (s *SafetyScreen) SetTokens(ts *ui.TokenSet) {
	if ts == nil {
		return
	}
	s.tokens = ts
	s.list.SetTokens(ts)
	s.scrollbar.SetTokens(ts)
	s.text.SetTokens(ts)
	s.text.SetBackgroundColor(ts.Color(ui.ColorPopupBg))
	s.actions.SetTokens(ts)
}

func (s *SafetyScreen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}

// SetPage replaces, never accumulates, one server-bounded page. An oversized
// response fails visibly rather than silently dropping identities or a cursor.
func (s *SafetyScreen) SetPage(page workspace.HistorySafetyPage) {
	if len(page.Records) > 200 || page.RetainedCount < 0 {
		s.SetError("Invalid review safety page: expected at most 200 rows and a nonnegative retained-reference count.")
		return
	}
	s.view, s.busy, s.requestID = safetyPage, false, ""
	s.detail = workspace.HistorySafetyDetail{}
	s.page = page
	s.page.Records = slices.Clone(page.Records)
	s.rebuildPage()
}

func (s *SafetyScreen) SetDetail(detail workspace.HistorySafetyDetail) {
	s.view, s.busy, s.requestID = safetyDetail, false, ""
	s.detail = detachSafetyDetail(detail)
	s.setText(safetyDetailText(s.detail))
	s.rebuildActions()
	s.actions.FocusLast() // Back, never Release, is the initial decision.
}

func (s *SafetyScreen) SetLoading(label string) {
	if label == "" {
		label = "Loading review safety references…"
	}
	s.view, s.busy, s.requestID = safetyLoading, true, ""
	s.detail = workspace.HistorySafetyDetail{}
	s.setText(label)
	s.rebuildActions()
	s.actions.FocusLast()
}

func (s *SafetyScreen) SetError(message string) {
	if message == "" {
		message = "Review safety inspection failed; no successful page is available."
	}
	s.view, s.busy, s.requestID = safetyError, false, ""
	s.detail = workspace.HistorySafetyDetail{}
	s.setText("Review safety error\n\n" + message)
	s.rebuildActions()
	s.actions.FocusLast()
}

func (s *SafetyScreen) SetPending(requestID, message string) {
	s.view, s.busy, s.requestID = safetyPending, false, requestID
	s.detail = workspace.HistorySafetyDetail{}
	s.setText("Protection change result not confirmed\n\nOriginal request ID: " + requestID +
		"\n\n" + message + "\n\nCheck only the original result. Do not repeat the protection change.")
	s.rebuildActions()
	s.actions.FocusLast()
}

// SetBusy disables activation immediately without replacing the evidence.
// Every emitted non-Back intent latches busy, including an intent to open a
// confirmation. The parent must clear it on cancellation or deliver a new
// projection. This closes the repeated-key/click gap before async admission.
func (s *SafetyScreen) SetBusy(busy bool) {
	if s.busy == busy {
		return
	}
	s.busy = busy
	if s.view == safetyPage {
		s.rebuildPage()
	} else {
		s.rebuildActions()
	}
}

// SelectedDetail returns a detached copy, including nested slice/pointer
// evidence. Release/Reprotect intentionally carry no payload: the caller takes
// this exact inspection before opening its standard confirmation.
func (s *SafetyScreen) SelectedDetail() (workspace.HistorySafetyDetail, bool) {
	if s.view != safetyDetail {
		return workspace.HistorySafetyDetail{}, false
	}
	return detachSafetyDetail(s.detail), true
}

func detachSafetyDetail(d workspace.HistorySafetyDetail) workspace.HistorySafetyDetail {
	d.Preview.Files = slices.Clone(d.Preview.Files)
	d.Preview.Boundary.Context.UnknownInputs = slices.Clone(d.Preview.Boundary.Context.UnknownInputs)
	if d.LastEvent != nil {
		event := *d.LastEvent
		d.LastEvent = &event
	}
	return d
}

func (s *SafetyScreen) rebuildPage() {
	items := make([]ui.ListItem, 0, len(s.page.Records)+4)
	for _, row := range s.page.Records {
		state := "released"
		if row.Retained {
			state = "retained"
		}
		label := row.Label
		if label == "" {
			label = row.Reference
		}
		items = append(items, ui.ListItem{
			ID: row.Reference, Label: state + " · " + label,
			Detail:   row.Reference + " · snapshot " + row.SnapshotID,
			Disabled: s.busy || row.Reference == "", Payload: ui.Action{ID: ActionSafetyInspect, Payload: row.Reference},
		})
	}
	if len(s.page.Records) == 0 {
		items = append(items, ui.ListItem{ID: "empty", Label: "No review safety references in this page", Disabled: true})
	}
	if s.page.NextCursor != "" {
		items = append(items, ui.ListItem{ID: "next", Label: "Next page", Command: true, Disabled: s.busy,
			Payload: ui.Action{ID: ActionSafetyNext, Payload: s.page.NextCursor}})
	}
	items = append(items,
		ui.ListItem{ID: "refresh", Label: "Refresh", Command: true, Disabled: s.busy, Payload: ui.Action{ID: ActionSafetyRefresh}},
		ui.ListItem{ID: "back", Label: "Back", Command: true, Disabled: s.busy, Payload: ui.Action{ID: ActionSafetyBack}},
	)
	s.list.SetItemsStable(items)
}

func (s *SafetyScreen) hasExactEvidence() bool {
	d := s.detail
	return s.view == safetyDetail && d.Reference != "" && d.SnapshotID != "" &&
		d.Available && d.InspectionDigest != "" && d.JournalSHA256 != "" && d.BlockedReason == "" &&
		(d.OriginalState == "committed" || d.OriginalState == "refused")
}

func (s *SafetyScreen) rebuildActions() {
	var buttons []ui.ActionBarButton
	add := func(action, label string, variant ui.ButtonVariant, enabled bool) {
		buttons = append(buttons, ui.ActionBarButton{
			ID: ui.NewID(safetyActionsID, action), Action: action, Label: label, Variant: variant, Enabled: enabled,
		})
	}
	switch s.view {
	case safetyDetail:
		if s.hasExactEvidence() {
			if s.detail.CanRelease && s.detail.Retained {
				add(ActionSafetyRelease, "Release protection…", ui.VariantDanger, !s.busy)
			}
			if s.detail.CanReprotect && !s.detail.Retained {
				add(ActionSafetyReprotect, "Keep protected again", ui.VariantPrimary, !s.busy)
			}
		}
		if s.detail.Available && s.detail.SnapshotID != "" {
			add(ActionSafetyRestore, "Restore preview", ui.VariantSecondary, !s.busy)
		}
	case safetyPending:
		if s.requestID != "" {
			add(ActionSafetyCheck, "Check original result", ui.VariantSecondary, !s.busy)
		}
	case safetyError:
		add(ActionSafetyRefresh, "Refresh", ui.VariantSecondary, !s.busy)
	}
	add(ActionSafetyBack, "Back", ui.VariantSecondary, true)
	s.actions.SetButtons(buttons...)
}

func (s *SafetyScreen) setText(value string) {
	s.text.CancelPointerCapture()
	s.text.SetValue(value)
	s.text.SetCursorFlat(0)
	s.text.SetScrollOffset(0)
}

func (s *SafetyScreen) Children() []ui.Component {
	if s.view == safetyPage {
		return []ui.Component{s.list, s.scrollbar}
	}
	return []ui.Component{s.text, s.actions}
}

func (s *SafetyScreen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 100, H: 30})
}

func (s *SafetyScreen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	if s.view == safetyPage {
		headerH := min(3, h)
		bodyH := h - headerH
		barW := 0
		if w > 0 && s.scrollbar.NeededForViewport(bodyH) {
			barW = 1
		}
		s.list.Layout(ctx, ui.Rect{Y: headerH, W: w - barW, H: bodyH})
		s.scrollbar.Layout(ctx, ui.Rect{X: w - barW, Y: headerH, W: barW, H: bodyH})
		return
	}
	barH := min(h, s.actions.PreferredHeight())
	s.text.Layout(ctx, ui.Rect{W: w, H: h - barH})
	s.actions.Layout(ctx, ui.Rect{Y: h - barH, W: w, H: barH})
}

func (s *SafetyScreen) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	bg := s.tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if s.view == safetyPage {
		coverage := "End of this traversal (not an atomic cross-page view)"
		if s.page.NextCursor != "" {
			coverage = "More references available · Next page continues this traversal"
		}
		lines := []string{
			fmt.Sprintf("Review safety · %d active retained references · %d rows in this page", s.page.RetainedCount, len(s.page.Records)),
			coverage,
			"Summaries do not establish original outcomes or authorize release.",
		}
		for y := 0; y < min(3, r.H); y++ {
			buf.WriteString(ox+r.X, oy+r.Y+y, rendering.TruncateVisual(lines[y], r.W, "…"), s.tokens.Color(ui.ColorMuted), bg, 0)
		}
	}
	// Host owns traversal/painting of registered children; never paint them twice.
}

func (s *SafetyScreen) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if !e.IsKey() {
		return ui.EventResult{}
	}
	if e.Key.Code == tea.KeyEsc {
		return s.HandleAction(ctx, ui.Action{ID: ActionSafetyBack})
	}
	if s.view == safetyPage {
		res := s.list.Handle(ctx, e)
		if res.Action != nil {
			translated := s.HandleAction(ctx, *res.Action)
			res = res.Merge(translated)
			res.Action = translated.Action
		}
		return res
	}
	if e.Key.Code == tea.KeyTab {
		return safetyFocus(safetyTextID)
	}
	res := s.actions.Handle(ctx, e)
	if res.Action != nil {
		return s.HandleAction(ctx, *res.Action)
	}
	return res
}

func safetyFocus(id ui.ComponentID) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id}, Invalidate: ui.InvalidatePaint}
}

func (s *SafetyScreen) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if a.ID == safetyActivate {
		item, ok := a.Payload.(ui.ListItem)
		if !ok || !item.Selectable() || s.view != safetyPage || s.busy {
			return ui.EventResult{Handled: true}
		}
		intent, ok := item.Payload.(ui.Action)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		a = intent
	}
	if a.ID == "actionbar.exit.backward" || a.ID == "actionbar.exit.forward" {
		return safetyFocus(safetyTextID)
	}
	if a.ID == ActionSafetyBack {
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionSafetyBack}}
	}
	allowed := false
	switch a.ID {
	case ActionSafetyInspect, ActionSafetyNext:
		allowed = s.view == safetyPage
	case ActionSafetyRefresh:
		allowed = s.view == safetyPage || s.view == safetyError
	case ActionSafetyRelease, ActionSafetyReprotect, ActionSafetyRestore, ActionSafetyCheck:
		allowed = s.actions.ActionEnabled(a.ID)
		switch a.ID {
		case ActionSafetyRelease, ActionSafetyReprotect:
			a.Payload = nil // See SelectedDetail.
		case ActionSafetyRestore:
			a.Payload = s.detail.SnapshotID
		case ActionSafetyCheck:
			a.Payload = s.requestID
		}
	default:
		return ui.EventResult{}
	}
	if !allowed || s.busy {
		return ui.EventResult{Handled: true}
	}
	s.SetBusy(true)
	return ui.EventResult{Handled: true, Action: &a, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *SafetyScreen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Review safety references"}
}

// These focused adapters leave scrolling, selection, clipboard and pointer
// capture with the native controls. Only scope-exit/activation policy differs.
type safetyText struct{ *ui.TextArea }

func (t *safetyText) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyTab {
		return safetyFocus(safetyActionsID)
	}
	return t.TextArea.Handle(ctx, e)
}

type safetyList struct {
	*ui.List
	owner *SafetyScreen
}

func (l *safetyList) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if l.owner.busy && (e.IsKey() && e.Key.Code != tea.KeyEsc || e.Kind == ui.EventMouseClick) {
		return ui.EventResult{Handled: true}
	}
	if e.IsKey() && e.Key.Code == tea.KeySpace {
		e.Key.Code = tea.KeyEnter
	}
	return l.List.Handle(ctx, e)
}

func safetyDetailText(d workspace.HistorySafetyDetail) string {
	var out strings.Builder
	state := "released (subject to ordinary retention and other protections)"
	if d.Retained {
		state = "retained"
	}
	original := d.OriginalState
	if original == "" {
		original = "unknown; no settled original outcome evidence"
	}
	fmt.Fprintf(&out, "Review safety reference\nReference: %s\nSnapshot ID: %s\nSnapshot label: %s\nSnapshot available: %t\n\n"+
		"Original operation outcome (persisted): %s\nOriginal owner ClientID: %s\nOriginal owner Epoch: %d\n\n"+
		"Current protection: %s\nProtection revision: %d\nInspection digest: %s\nOriginal journal SHA-256: %s\n",
		d.Reference, d.SnapshotID, d.Snapshot.Label, d.Available, original, d.OriginalOwner.ClientID, d.OriginalOwner.Epoch,
		state, d.Revision, d.InspectionDigest, d.JournalSHA256)
	blocker := d.BlockedReason
	if blocker == "" && (!d.CanRelease && !d.CanReprotect || !d.Available || d.Reference == "" || d.SnapshotID == "" ||
		d.InspectionDigest == "" || d.JournalSHA256 == "" ||
		(original != "committed" && original != "refused")) {
		blocker = "No exact verified protection transition is available. Unknown, prepared and unconfirmed outcomes remain protected."
	}
	if blocker != "" {
		fmt.Fprintf(&out, "\nIntegrity / eligibility blocker: %s\n", blocker)
	}
	fmt.Fprintf(&out, "\nOriginal operation ID: %s\nOriginal operation digest: %s\nOriginal operation label: %s\n"+
		"Original result ID: %s\nOriginal result digest: %s\nOriginal observed result state: %s\n"+
		"Original safety reference: %s\nOriginal safety snapshot: %s\nOriginal detail: %s\nOriginal error: %s\n",
		d.Preview.ID, d.Preview.Digest, d.Preview.Label, d.Result.ID, d.Result.Digest, d.Result.State,
		d.Result.SafetyReference, d.Result.SafetySnapshotID, d.Result.Detail, d.Result.Error)
	// These structs contain only bounded detached evidence, no I/O handles.
	boundary, _ := json.MarshalIndent(d.Preview.Boundary, "", "  ")
	fmt.Fprintf(&out, "\nOriginal source / context / producer boundary:\n%s\n\nReviewed files (%d):\n", boundary, len(d.Preview.Files))
	for _, f := range d.Preview.Files {
		source, _ := json.MarshalIndent(f.Source, "", "  ")
		fmt.Fprintf(&out, "\nPath: %s\nBefore SHA-256: %s\nAfter SHA-256: %s\nMode: %o\nBefore bytes: %d\nAfter bytes: %d\nSource:\n%s\n",
			f.Path, f.BeforeSHA256, f.AfterSHA256, f.Mode, f.BeforeBytes, f.AfterBytes, source)
	}
	fmt.Fprintf(&out, "\nOriginal reviewed preview (complete):\n%s\n", d.Preview.Text)
	if event := d.LastEvent; event != nil {
		audit, _ := json.MarshalIndent(event, "", "  ")
		fmt.Fprintf(&out, "\nLatest immutable protection audit event (including actor and original request):\n%s\n", audit)
	} else {
		out.WriteString("\nLatest protection audit event: none. Absence is not proof of a successful human release.\n")
	}
	out.WriteString("\nProtection changes do not change project files or delete this snapshot now. Other protections still apply. Re-protection is possible only while the exact original snapshot remains available.")
	return out.String()
}
