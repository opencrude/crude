package localhistory

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/document"
	history "crude/localhistory"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func safetyFixture() workspace.HistorySafetyDetail {
	return workspace.HistorySafetyDetail{
		Reference: "review-exact-original-reference", SnapshotID: strings.Repeat("a", 64),
		Revision: 7, Retained: true, Available: true, OriginalState: "committed",
		OriginalOwner:    document.Owner{ClientID: "original-client-not-current-shell", Epoch: 29},
		InspectionDigest: strings.Repeat("d", 64), JournalSHA256: strings.Repeat("e", 64), CanRelease: true,
		Snapshot: history.SnapshotRecord{Label: "Before reviewed rename"},
		Preview: workspace.ReviewedEditPreview{
			ID: "review-exact-original-reference", Digest: strings.Repeat("f", 64), Label: "Rename exact target",
			Text: strings.Repeat("- original line\n+ intended line\n", 70) + "FINAL COMPLETE PREVIEW EVIDENCE",
			Files: []workspace.ReviewedEditFile{{
				Path: "/workspace/精確/exact-original.go", BeforeSHA256: strings.Repeat("b", 64), AfterSHA256: strings.Repeat("c", 64),
				BeforeBytes: 41, AfterBytes: 42, Mode: 0644,
				Source: types.InsightSource{DocumentID: "exact-document-generation", Revision: 81},
			}},
			Boundary: types.InsightBoundary{Context: types.InsightContext{UnknownInputs: []string{"original-unknown-build-input"}}},
		},
		Result: workspace.ReviewedEditResult{ID: "review-exact-original-reference", State: "committed", Detail: "original publication detail", Error: "original warning"},
		LastEvent: &history.SafetyProtectionEvent{
			ID: "audit-event-seven", Action: "reprotect", Revision: 7,
			Change: history.SafetyProtectionChange{ActorClientID: "different-disposition-actor", ActorEpoch: 63, RequestID: "original-disposition-request"},
		},
	}
}

type safetyInset struct {
	ui.Base
	body *SafetyScreen
}

func (s *safetyInset) Children() []ui.Component { return []ui.Component{s.body} }
func (s *safetyInset) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.body.Layout(ctx, ui.Rect{X: min(3, r.W), Y: min(2, r.H), W: max(0, r.W-6), H: max(0, r.H-4)})
}

func safetyHost(s *SafetyScreen, w, h int) *ui.Host {
	root := &safetyInset{Base: ui.NewBase("safety-test-root"), body: s}
	host := ui.NewHost(theme.DefaultDark(), root)
	host.Resize(w, h)
	host.Layout()
	host.Focus(SafetyScreenID)
	return host
}

func safetyPaint(host *ui.Host, w, h int) (*surface.CellBuffer, string) {
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var text strings.Builder
	for y := range h {
		text.WriteString(plain(buf, y))
		text.WriteByte('\n')
	}
	return buf, text.String()
}

func safetyWantAction(t *testing.T, res ui.EventResult, id string, payload any) {
	t.Helper()
	if res.Action == nil || res.Action.ID != id || res.Action.Payload != payload {
		t.Fatalf("action=%+v, want %s payload=%v", res.Action, id, payload)
	}
}

func safetyButtonClick(t *testing.T, host *ui.Host, s *SafetyScreen, action string) ui.EventResult {
	t.Helper()
	host.Layout()
	b, ok := s.actions.ButtonBounds(action)
	if !ok || b.Empty() {
		t.Fatalf("missing native button %s: %+v", action, b)
	}
	r, a := s.Bounds(), s.actions.Bounds()
	x, y := r.X+a.X+b.X, r.Y+a.Y+b.Y
	result := host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
	return result
}

func TestSafetyNativeDetailHostReadingCopyAndTheme(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprintf("%dx%d", size.W, size.H), func(t *testing.T) {
			s := NewSafety(nil)
			d := safetyFixture()
			s.SetDetail(d)
			host := safetyHost(s, size.W, size.H)
			_, painted := safetyPaint(host, size.W, size.H)
			if !strings.Contains(painted, d.Reference) || !strings.Contains(painted, "Original owner ClientID:") {
				t.Fatalf("initial detail not readable:\n%s", painted)
			}
			for _, full := range []string{d.Reference, d.SnapshotID, d.OriginalOwner.ClientID, "Original owner Epoch: 29",
				"persisted): committed", "Protection revision: 7", d.Preview.Files[0].Path,
				d.Preview.Files[0].BeforeSHA256, d.Preview.Files[0].AfterSHA256,
				d.Result.Detail, d.Result.Error, d.Preview.Text, "audit-event-seven",
				"different-disposition-actor", "original-disposition-request"} {
				if !strings.Contains(s.text.Value(), full) {
					t.Fatalf("complete evidence missing %q", full)
				}
			}
			safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}), ActionSafetyBack, nil)
			host.Focus(safetyTextID)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyPgDown})
			if s.text.ScrollOffset() == 0 {
				t.Fatal("native page movement did not scroll")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
			_, painted = safetyPaint(host, size.W, size.H)
			if !strings.Contains(painted, "original snapshot remains available.") {
				t.Fatalf("end of complete evidence unreachable:\n%s", painted)
			}
			// The same native owner supplies clipboard commands, never OS I/O
			// from a component callback.
			var copied string
			s.text.SetClipboardCopy(func(value string) tea.Cmd {
				return func() tea.Msg { copied = value; return nil }
			})
			host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			res := host.Handle(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
			if copied != "" || res.Cmd == nil {
				t.Fatal("copy must be a deferred native clipboard command")
			}
			_ = res.Cmd()
			if copied != s.text.Value() {
				t.Fatal("copy did not preserve complete evidence")
			}
			res = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
			if res.Action != nil || s.text.HasActiveSelection() {
				t.Fatal("Esc did not first dismiss text selection")
			}
			safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}), ActionSafetyBack, nil)
			host.Handle(tea.KeyPressMsg{Code: tea.KeyHome, Mod: tea.ModCtrl})
			for _, key := range []rune{tea.KeyDown, tea.KeyUp, tea.KeyPgDown, tea.KeyPgUp, tea.KeyHome, tea.KeyEnd} {
				host.Handle(tea.KeyPressMsg{Code: key})
			}
			host.Handle(tea.MouseWheelMsg{X: s.Bounds().X + 1, Y: s.Bounds().Y + 1, Button: tea.MouseWheelDown})
			if s.text.ScrollOffset() == 0 {
				t.Fatal("native wheel failed to scroll evidence")
			}
			host.Handle(tea.KeyPressMsg{Code: 'x', Text: "x"})
			if s.text.Value() != copied {
				t.Fatal("read-only evidence changed")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
			if host.Focused() != safetyActionsID {
				t.Fatal("reader Tab did not reach native action scope")
			}
			host.Handle(tea.KeyPressMsg{Code: tea.KeyUp})
			if host.Focused() != safetyTextID {
				t.Fatal("action scope cannot return to evidence")
			}
			for _, th := range []*theme.Theme{theme.DefaultLight(), theme.DefaultDark()} {
				host.SetTheme(th)
				buf, _ := safetyPaint(host, size.W, size.H)
				if !buf.Get(s.Bounds().X, s.Bounds().Y).BG.Equal(ui.NewTokenSet(th).Color(ui.ColorPopupBg)) {
					t.Fatal("native reader did not receive popup theme")
				}
				safetyCheckBounds(t, s)
			}
		})
	}
}

func safetyCheckBounds(t *testing.T, s *SafetyScreen) {
	t.Helper()
	for _, c := range s.Children() {
		r := c.Bounds()
		if !strings.HasPrefix(string(c.ID()), string(SafetyScreenID)+"/") ||
			r.X < 0 || r.Y < 0 || r.W < 0 || r.H < 0 || r.X+r.W > s.Bounds().W || r.Y+r.H > s.Bounds().H {
			t.Fatalf("child %s outside parent-local bounds: %+v in %+v", c.ID(), r, s.Bounds())
		}
	}
}

func TestSafetyExplicitActionsParityAndBusyGuard(t *testing.T) {
	for _, activate := range []string{"enter", "space", "click"} {
		t.Run(activate, func(t *testing.T) {
			s := NewSafety(nil)
			s.SetDetail(safetyFixture())
			host := safetyHost(s, 80, 24)
			safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}), ActionSafetyBack, nil)
			host.Focus(safetyActionsID)
			// Back is last; Left goes to Restore, then to Release.
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyLeft})
			var res ui.EventResult
			switch activate {
			case "click":
				res = safetyButtonClick(t, host, s, ActionSafetyRelease)
			case "space":
				res = host.Handle(tea.KeyPressMsg{Code: tea.KeySpace})
			default:
				res = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			}
			safetyWantAction(t, res, ActionSafetyRelease, nil)
			d, ok := s.SelectedDetail()
			if !ok || d.Reference != "review-exact-original-reference" || d.Revision != 7 {
				t.Fatal("action lost exact inspected detail")
			}
			for range 3 {
				if res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); res.Action != nil {
					t.Fatalf("busy repeated release: %+v", res.Action)
				}
				if res := safetyButtonClick(t, host, s, ActionSafetyRelease); res.Action != nil {
					t.Fatalf("busy pointer release: %+v", res.Action)
				}
			}
			safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}), ActionSafetyBack, nil)
			s.SetBusy(false) // Parent's confirmation cancellation path.
			res = safetyButtonClick(t, host, s, ActionSafetyRestore)
			safetyWantAction(t, res, ActionSafetyRestore, strings.Repeat("a", 64))
		})
	}
	s := NewSafety(nil)
	d := safetyFixture()
	d.Retained, d.CanRelease, d.CanReprotect = false, false, true
	d.LastEvent.Action = "release"
	s.SetDetail(d)
	host := safetyHost(s, 80, 24)
	safetyWantAction(t, safetyButtonClick(t, host, s, ActionSafetyReprotect), ActionSafetyReprotect, nil)
}

func TestSafetyBlockedEvidenceAndPendingNeverOfferNewEffect(t *testing.T) {
	for _, state := range []string{"", "unknown", "prepared", "unconfirmed", "corrupt", "committed", "refused"} {
		for _, missing := range []string{"blocker", "inspection", "journal", "snapshot", "unavailable"} {
			t.Run(state+"/"+missing, func(t *testing.T) {
				s := NewSafety(nil)
				d := safetyFixture()
				d.OriginalState = state
				d.CanReprotect = true // Even inconsistent flags cannot bypass missing facts.
				switch missing {
				case "blocker":
					d.BlockedReason = "exact original evidence is corrupt"
				case "inspection":
					d.InspectionDigest = ""
				case "journal":
					d.JournalSHA256 = ""
				case "snapshot":
					d.SnapshotID = ""
				case "unavailable":
					d.Available = false
				}
				s.SetDetail(d)
				host := safetyHost(s, 80, 24)
				for _, action := range []string{ActionSafetyRelease, ActionSafetyReprotect} {
					if s.actions.ActionEnabled(action) {
						t.Fatalf("blocked detail offers %s", action)
					}
					if res := s.HandleAction(ui.EventContext{}, ui.Action{ID: action}); res.Action != nil {
						t.Fatalf("blocked detail emitted %s", res.Action.ID)
					}
				}
				_, painted := safetyPaint(host, 80, 24)
				if strings.Contains(painted, "Release protection") || strings.Contains(painted, "Keep protected again") {
					t.Fatalf("blocked effect visible:\n%s", painted)
				}
				if !strings.Contains(s.text.Value(), "blocker:") {
					t.Fatal("blocked detail lacks explanation")
				}
			})
		}
	}
	s := NewSafety(nil)
	s.SetDetail(safetyFixture())
	s.SetPending("original-request/opaque-exact", strings.Repeat("lost transport evidence\n", 50)+"final uncertain observation")
	host := safetyHost(s, 80, 24)
	safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}), ActionSafetyBack, nil)
	if _, ok := s.SelectedDetail(); ok {
		t.Fatal("pending retained stale disposition authority")
	}
	for _, action := range []string{ActionSafetyRelease, ActionSafetyReprotect, ActionSafetyRestore, ActionSafetyRefresh} {
		if s.actions.ActionEnabled(action) {
			t.Fatalf("pending offers %s", action)
		}
	}
	safetyWantAction(t, safetyButtonClick(t, host, s, ActionSafetyCheck), ActionSafetyCheck, "original-request/opaque-exact")
	for range 3 {
		if res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); res.Action != nil {
			t.Fatalf("busy receipt check repeated: %+v", res.Action)
		}
	}
	host.Focus(safetyTextID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd, Mod: tea.ModCtrl})
	if !strings.Contains(s.text.Value(), "original-request/opaque-exact") || s.text.ScrollOffset() == 0 {
		t.Fatal("pending exact request/message is not retained and scrollable")
	}
}

func TestSafetyPagedListNativeActionsAndStates(t *testing.T) {
	page := workspace.HistorySafetyPage{
		RetainedCount: 1007, NextCursor: "cursor/+opaque==/next",
		Records: []workspace.HistorySafetySummary{
			{Reference: "review-exact-A", SnapshotID: "snapshot-A", Label: "same label", Retained: true},
			{Reference: "review-exact-B", SnapshotID: "snapshot-B", Label: "same label", Retained: false},
		},
	}
	for _, activate := range []string{"enter", "space", "click"} {
		s := NewSafety(nil)
		s.SetPage(page)
		host := safetyHost(s, 80, 24)
		_, painted := safetyPaint(host, 80, 24)
		for _, label := range []string{"1007 active retained references", "2 rows in this page", "More references", "retained", "released"} {
			if !strings.Contains(painted, label) {
				t.Fatalf("paged rendering missing %q:\n%s", label, painted)
			}
		}
		safetyCheckBounds(t, s)
		host.Focus(safetyListID)
		var res ui.EventResult
		switch activate {
		case "click":
			res = host.Handle(tea.MouseClickMsg{X: s.Bounds().X + 1, Y: s.Bounds().Y + s.list.Bounds().Y, Button: tea.MouseLeft})
		case "space":
			res = host.Handle(tea.KeyPressMsg{Code: tea.KeySpace})
		default:
			res = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
		}
		safetyWantAction(t, res, ActionSafetyInspect, "review-exact-A")
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		if res := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); res.Action != nil {
			t.Fatalf("busy list reactivated: %+v", res.Action)
		}
		s.SetPage(page)
		host.MarkLayout()
		host.Focus(safetyListID)
		host.Handle(tea.KeyPressMsg{Code: tea.KeyHome})
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
		safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}), ActionSafetyNext, page.NextCursor)
		s.SetPage(workspace.HistorySafetyPage{})
		host.MarkLayout()
		_, painted = safetyPaint(host, 80, 24)
		if !strings.Contains(painted, "No review safety references in this page") || strings.Contains(painted, "Next page") {
			t.Fatalf("empty successful page wrong:\n%s", painted)
		}
		safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}), ActionSafetyRefresh, nil)
	}
	s := NewSafety(nil)
	host := safetyHost(s, 80, 24)
	for _, transition := range []func(){func() {}, func() { s.SetError("history evidence corrupt") }, func() { s.SetLoading("") }} {
		transition()
		host.MarkLayout()
		_, painted := safetyPaint(host, 80, 24)
		if strings.Contains(painted, "No review safety references") {
			t.Fatalf("unsuccessful state became empty success:\n%s", painted)
		}
		safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc}), ActionSafetyBack, nil)
	}
	large := workspace.HistorySafetyPage{Records: make([]workspace.HistorySafetySummary, 200), RetainedCount: 200}
	for i := range large.Records {
		large.Records[i] = workspace.HistorySafetySummary{Reference: fmt.Sprintf("exact-%03d", i), Label: "reference", Retained: true}
	}
	s.SetPage(large)
	host.MarkLayout()
	host.Layout()
	if len(s.page.Records) != 200 || !s.scrollbar.Needed() {
		t.Fatal("maximum bounded page lacks native scrolling")
	}
	host.Focus(safetyListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnd})
	if s.list.Scroll().Offset() == 0 {
		t.Fatal("native list End did not reach final rows")
	}
	safetyWantAction(t, host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}), ActionSafetyBack, nil)
	large.Records = append(large.Records, workspace.HistorySafetySummary{})
	s.SetPage(large)
	if s.view != safetyError {
		t.Fatal("oversized page silently truncated")
	}
}

func TestSafetyDetachedEvidenceAndTinyBounds(t *testing.T) {
	s := NewSafety(nil)
	d := safetyFixture()
	s.SetDetail(d)
	d.Preview.Files[0].Path = "mutated-input"
	d.Preview.Boundary.Context.UnknownInputs[0] = "mutated-input"
	d.LastEvent.ID = "mutated-input"
	copy, ok := s.SelectedDetail()
	if !ok || copy.Preview.Files[0].Path != "/workspace/精確/exact-original.go" ||
		copy.LastEvent.ID != "audit-event-seven" || copy.Preview.Boundary.Context.UnknownInputs[0] != "original-unknown-build-input" {
		t.Fatal("SetDetail aliased caller evidence")
	}
	copy.Preview.Files[0].Path, copy.LastEvent.ID = "mutated-output", "mutated-output"
	again, _ := s.SelectedDetail()
	if again.Preview.Files[0].Path == "mutated-output" || again.LastEvent.ID == "mutated-output" {
		t.Fatal("SelectedDetail exposed mutable evidence")
	}
	for _, page := range []bool{false, true} {
		if page {
			s.SetPage(workspace.HistorySafetyPage{})
		}
		for w := 0; w < 9; w++ {
			for h := 0; h < 6; h++ {
				host := ui.NewHost(theme.DefaultDark(), s)
				host.Resize(w, h)
				host.Layout()
				safetyPaint(host, w, h)
				safetyCheckBounds(t, s)
			}
		}
	}
}

func TestSafetyNativePointerCaptureAndScrollbar(t *testing.T) {
	s := NewSafety(nil)
	s.SetDetail(safetyFixture())
	host := safetyHost(s, 80, 24)
	var copied string
	s.text.SetClipboardCopy(func(value string) tea.Cmd {
		return func() tea.Msg { copied = value; return nil }
	})
	x, y := s.Bounds().X, s.Bounds().Y
	host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	// Captured drag extends outside the reader's right edge. The native text
	// owner clamps selection, not a second screen-owned pointer path.
	host.Handle(tea.MouseMotionMsg{X: x + s.Bounds().W + 2, Y: y, Button: tea.MouseLeft})
	res := host.Handle(tea.MouseReleaseMsg{X: x + s.Bounds().W + 2, Y: y, Button: tea.MouseLeft})
	if res.Cmd == nil || copied != "" {
		t.Fatal("read-only drag did not defer its native copy")
	}
	_ = res.Cmd()
	if copied != "Review safety reference" || s.text.IsSelecting() || s.text.HasActiveSelection() {
		t.Fatalf("native captured reading selection copy=%q selecting=%t", copied, s.text.IsSelecting())
	}

	page := workspace.HistorySafetyPage{Records: make([]workspace.HistorySafetySummary, 100), RetainedCount: 100}
	for i := range page.Records {
		page.Records[i] = workspace.HistorySafetySummary{Reference: fmt.Sprintf("exact-ref-%03d", i), Label: "snapshot label", Retained: true}
	}
	s.SetPage(page)
	host.MarkLayout()
	host.Layout()
	safetyCheckBounds(t, s)
	safetyPaint(host, 80, 24)
	bar := s.scrollbar.Bounds()
	x, y = s.Bounds().X+bar.X, s.Bounds().Y+bar.Y
	host.Handle(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	host.Handle(tea.MouseMotionMsg{X: x, Y: y + bar.H + 3, Button: tea.MouseLeft})
	host.Handle(tea.MouseReleaseMsg{X: x, Y: y + bar.H + 3, Button: tea.MouseLeft})
	if s.list.Scroll().Offset() == 0 || s.scrollbar.IsDragging() {
		t.Fatal("native scrollbar capture did not scroll/release")
	}
	before := s.list.Scroll().Offset()
	host.Handle(tea.MouseWheelMsg{X: s.Bounds().X + 1, Y: s.Bounds().Y + s.list.Bounds().Y + 1, Button: tea.MouseWheelUp})
	if s.list.Scroll().Offset() >= before {
		t.Fatal("native wheel did not scroll paged list")
	}
}
