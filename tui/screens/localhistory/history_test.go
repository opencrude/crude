package localhistory

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	model "crude/workspace/localhistory"
)

func paint(t *testing.T, host *ui.Host, w, h int) string {
	t.Helper()
	host.Resize(w, h)
	host.MarkLayout()
	host.Layout()
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var text strings.Builder
	for y := range h {
		text.WriteString(plain(buf, y) + "\n")
	}
	return text.String()
}

func plain(buf *surface.CellBuffer, y int) string {
	out := ""
	for x := 0; x < buf.Width(); x++ {
		c := buf.Get(x, y)
		if c.Char != 0 {
			out += string(c.Char)
		}
	}
	return out
}

// answer delivers msg as the plan of the dialog's current preview.
func answer(s *Screen, msg PlanMsg) {
	msg.Generation = s.Request().Generation
	s.Handle(ui.EventContext{}, ui.CustomEvent(msg))
}

func reviewPlan() PlanMsg {
	return PlanMsg{Plan: model.RestorePlan{ID: "plan", Changes: []model.Change{
		{Path: "safe.go", Action: model.ChangeRestore, TextDiff: "--- a/safe.go\n+++ b/safe.go\n@@ -1 +1 @@\n-later\n+earlier\n"},
		{Path: "conflict.go", Action: model.ChangeRestore, ConflictReason: "changed later", ExactAllowed: true},
		{Path: "escape", Action: model.ChangeRestore, UnsafeReason: "link points outside the workspace"},
	}}}
}

// The review shows every file with a check box and the selected file's diff.
// Space ticks files, E replaces a file that changed later, and only Restore
// applies; Cancel is focused, so a stray Enter on the action row never does.
func TestRestorePreviewChoosesFilesAndRestoresOnlyWithRestore(t *testing.T) {
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	s.Begin(PreviewRequest{SnapshotID: "snap", Heading: "Restore every file to Today 10:42?"})
	answer(s, reviewPlan())
	screen := paint(t, host, 110, 24)
	for _, want := range []string{"Restore every file to Today 10:42?", "[x] safe.go", "[ ] conflict.go", "cannot restore", "-later", "+earlier", "Restore 1 file", "Cancel"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("review is missing %q:\n%s", want, screen)
		}
	}
	if !s.selected["safe.go"] || s.selected["conflict.go"] || s.selected["escape"] {
		t.Fatalf("default selection=%v", s.selected)
	}
	host.Focus(ListID)
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: 'e', Text: "e"})
	if !s.exact["conflict.go"] || !s.selected["conflict.go"] {
		t.Fatalf("exact=%v selected=%v", s.exact, s.selected)
	}
	screen = paint(t, host, 110, 24)
	if !strings.Contains(screen, "Later edits in 1 file will be lost") || !strings.Contains(screen, "Restore 2 files") {
		t.Fatalf("replacing a later edit is not called out:\n%s", screen)
	}
	host.Focus(ActionsID)
	if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter}); result.Action != nil && result.Action.ID == ActionApply {
		t.Fatal("Enter on the focused Cancel restored files")
	}
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: barRestore})
	if result.Action == nil || result.Action.ID != ActionApply {
		t.Fatalf("Restore did not apply: %+v", result.Action)
	}
	request := result.Action.Payload.(ApplyRequest)
	if request.PlanID != "plan" || len(request.Choices) != 2 || !request.Choices[1].Exact {
		t.Fatalf("choices=%+v", request.Choices)
	}
	for range 3 {
		if again := s.HandleAction(ui.EventContext{}, ui.Action{ID: barRestore}); again.Action != nil {
			t.Fatal("a restore in flight allowed another restore")
		}
	}
}

func TestRestorePreviewStatesUseTheUsersWords(t *testing.T) {
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	s.Begin(PreviewRequest{Heading: "Undo “Bisi changed 2 files”?"})
	if screen := paint(t, host, 80, 16); !strings.Contains(screen, "Preparing the restore preview") || !strings.Contains(screen, "Cancel") {
		t.Fatalf("loading state:\n%s", screen)
	}
	answer(s, PlanMsg{Error: "the snapshot is no longer kept"})
	if screen := paint(t, host, 80, 16); !strings.Contains(screen, "the snapshot is no longer kept") || !strings.Contains(screen, "Close") {
		t.Fatalf("error state:\n%s", screen)
	}
	s.Begin(PreviewRequest{Heading: "Undo the changes of this message?"})
	answer(s, PlanMsg{Plan: model.RestorePlan{ID: "empty", MessageID: "m"}})
	if screen := paint(t, host, 80, 16); !strings.Contains(screen, "already undone") || strings.Contains(screen, "Restore") && strings.Contains(screen, "Restore files") {
		t.Fatalf("empty state:\n%s", screen)
	}
	for _, internal := range []string{"after_message", "snapshot_id", "plan"} {
		if strings.Contains(paint(t, host, 80, 16), internal) {
			t.Fatalf("the dialog shows internal text %q", internal)
		}
	}
}

func TestAppliedRestoreOffersUndoThroughItsSafetyCopy(t *testing.T) {
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	s.Handle(ui.EventContext{}, ui.CustomEvent(AppliedMsg{Result: model.RestoreResult{SafetySnapshotID: "safety", Paths: []string{"a", "b"}}}))
	if screen := paint(t, host, 80, 16); !strings.Contains(screen, "Restored 2 files.") || !strings.Contains(screen, "Undo this restore") {
		t.Fatalf("result state:\n%s", screen)
	}
	res := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'u', Text: "u"}))
	if res.Action == nil || res.Action.ID != ActionPreview || res.Action.Payload.(PreviewRequest).SnapshotID != "safety" {
		t.Fatalf("undo action=%+v", res.Action)
	}
}

// An unresolved original restore allows only checking that request; a known
// partial restore keeps its safety Undo instead of a fresh Apply.
func TestRestoreFailureEvidenceGatesFurtherMutation(t *testing.T) {
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	answer(s, reviewPlan())
	if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: barRestore}); got.Action == nil || got.Action.ID != ActionApply {
		t.Fatal("initial restore absent")
	}
	s.Handle(ui.EventContext{}, ui.CustomEvent(AppliedMsg{RequestID: "original", Error: "The reply did not arrive. Check the result before you restore again."}))
	s.Begin(PreviewRequest{SnapshotID: "other"})
	answer(s, reviewPlan())
	if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: barRestore}); got.Action != nil {
		t.Fatalf("unresolved restore offered %q", got.Action.ID)
	}
	screen := paint(t, host, 80, 16)
	if !strings.Contains(screen, "Check the result") || strings.Contains(screen, "Restore 1 file") {
		t.Fatalf("unresolved restore screen:\n%s", screen)
	}
	if got := s.HandleAction(ui.EventContext{}, ui.Action{ID: barCheck}); got.Action == nil || got.Action.ID != ActionCheck || got.Action.Payload != "original" {
		t.Fatalf("check action=%+v", got.Action)
	}
	s.Handle(ui.EventContext{}, ui.CustomEvent(AppliedMsg{Result: model.RestoreResult{SafetySnapshotID: "safety", Paths: []string{"file"}}, Warning: "publication failed", Incomplete: true}))
	if s.requestID != "" || !strings.Contains(s.status, "The restore did not complete") {
		t.Fatalf("committed partial restore status=%q request=%q", s.status, s.requestID)
	}
	res := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'u', Text: "u"}))
	if res.Action == nil || res.Action.ID != ActionPreview || res.Action.Payload.(PreviewRequest).SnapshotID != "safety" {
		t.Fatalf("committed partial restore lost its safety Undo: %+v", res.Action)
	}
}

// The timeline names what happened and who did it, never capture kinds or
// message identifiers; the filter names whose edits the list shows.
func TestPanelTimelineUsesUserLanguageAndFilter(t *testing.T) {
	tokens := ui.NewTokenSet(theme.DefaultDark())
	p := NewPanel(tokens)
	host := ui.NewHost(theme.DefaultDark(), p)
	now := time.Now()
	p.SetAgentNames(map[string]string{"a1": "Bisi"})
	p.BeginTimeline("")
	p.SetActivity([]float64{0, 1, 3, 0, 5})
	p.SetTimeline("", []model.Event{
		{ID: "e3", BaseID: "e2", CreatedAt: now.UnixNano(), Kind: "after_message", SessionID: "s", MessageID: "client-8bceda086ff50f2410d9", AgentIDs: []string{"a1"}, Paths: []string{"a.go", "b.go"}},
		{ID: "e2", BaseID: "e1", CreatedAt: now.Add(-time.Minute).UnixNano(), Kind: "external_change", Paths: []string{"c.go"}},
		{ID: "e1", CreatedAt: now.AddDate(0, 0, -1).UnixNano(), Kind: "baseline"},
	}, "3 changes recorded since Nov 26 · 3.0 MB on disk", "")
	screen := paint(t, host, 120, 14)
	for _, want := range []string{"All changes ▾", "Bisi changed 2 files", "Changed outside agents · 1 file", "History started", "3 changes recorded", "Undo changes…", "Restore to here…"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("timeline is missing %q:\n%s", want, screen)
		}
	}
	for _, internal := range []string{"client-8bceda", "external_change", "after_message"} {
		if strings.Contains(screen, internal) {
			t.Fatalf("timeline shows internal text %q:\n%s", internal, screen)
		}
	}
	p.SetMode(ModeAgent)
	p.SetAgent(AgentRef{SessionID: "s", AgentID: "a1", Name: "Bisi"})
	if screen = paint(t, host, 120, 14); !strings.Contains(screen, "Bisi's edits ▾") || !strings.Contains(screen, "Undo latest changes…") {
		t.Fatalf("agent filter:\n%s", screen)
	}
}

// Tab reaches every action in reading order: the filter and Kept for review,
// the row actions above a narrow list or in the preview heading of a wide
// one, and the list. Unavailable actions are skipped.
func TestPanelFocusOrderReachesEveryAction(t *testing.T) {
	p := NewPanel(ui.NewTokenSet(theme.DefaultDark()))
	host := ui.NewHost(theme.DefaultDark(), p)
	p.SetSafetyAvailable(true)
	p.BeginTimeline("")
	now := time.Now()
	p.SetTimeline("", []model.Event{
		{ID: "e2", BaseID: "e1", CreatedAt: now.UnixNano(), Kind: "external_change", Paths: []string{"c.go"}},
		{ID: "e1", CreatedAt: now.Add(-time.Hour).UnixNano(), Kind: "baseline"},
	}, "2 changes recorded", "")
	paint(t, host, 140, 20)
	want := []ui.ComponentID{FilterID, KeptID, EventsID, UndoButtonID, RestoreButtonID, PanelPreviewID}
	if got := p.FocusOrder(); strings.Join(ids(got), " ") != strings.Join(ids(want), " ") {
		t.Fatalf("wide focus order = %v, want %v", got, want)
	}
	paint(t, host, 80, 20)
	want = []ui.ComponentID{FilterID, KeptID, UndoButtonID, RestoreButtonID, EventsID}
	if got := p.FocusOrder(); strings.Join(ids(got), " ") != strings.Join(ids(want), " ") {
		t.Fatalf("narrow focus order = %v, want %v", got, want)
	}
	// The baseline has nothing earlier to undo.
	p.SelectEvent("e1")
	paint(t, host, 80, 20)
	for _, id := range p.FocusOrder() {
		if id == UndoButtonID {
			t.Fatal("Tab reaches Undo changes on a point that cannot be undone")
		}
	}
}

// A long title stays whole: the row actions move beside its time instead of
// cutting it.
func TestPanelHeadingKeepsALongTitleWhole(t *testing.T) {
	p := NewPanel(ui.NewTokenSet(theme.DefaultDark()))
	host := ui.NewHost(theme.DefaultDark(), p)
	p.BeginTimeline("")
	now := time.Now()
	p.SetTimeline("", []model.Event{
		{ID: "e2", BaseID: "e1", CreatedAt: now.UnixNano(), Kind: "external_change", Paths: []string{"c.go"}},
		{ID: "e1", CreatedAt: now.Add(-time.Hour).UnixNano(), Kind: "baseline"},
	}, "2 changes recorded", "")
	title := "Main Agent changed 12 files in the workspace"
	p.BeginPreview("e2", title, "Today 13:19:56", false)
	p.SetPreview("e2", nil, "", "No files")
	screen := paint(t, host, 120, 20)
	if !strings.Contains(screen, title) {
		t.Fatalf("the preview title is cut:\n%s", screen)
	}
	for _, want := range []string{"Today 13:19:56", "Undo changes…", "Restore to here…"} {
		if !strings.Contains(screen, want) {
			t.Fatalf("the heading lost %q:\n%s", want, screen)
		}
	}
	undo, okUndo := host.ScreenRect(UndoButtonID)
	restore, okRestore := host.ScreenRect(RestoreButtonID)
	if !okUndo || !okRestore || undo.Y != restore.Y || undo.Empty() {
		t.Fatalf("row actions are not on one row: %v %v", undo, restore)
	}
}

func ids(list []ui.ComponentID) []string {
	out := make([]string, len(list))
	for i, id := range list {
		out[i] = string(id)
	}
	return out
}

// A plan read for an earlier preview never fills a later one: the user
// could otherwise restore the earlier files under the later heading.
func TestRestorePreviewDropsAPlanForAnEarlierPreview(t *testing.T) {
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	s.Begin(PreviewRequest{SnapshotID: "first", Heading: "Restore every file to 10:00?"})
	first := s.Request().Generation
	s.Begin(PreviewRequest{SnapshotID: "second", Heading: "Restore every file to 11:00?"})
	stale := reviewPlan()
	stale.Generation = first
	s.Handle(ui.EventContext{}, ui.CustomEvent(stale))
	if screen := paint(t, host, 120, 20); strings.Contains(screen, "safe.go") || !strings.Contains(screen, "Preparing the restore preview") {
		t.Fatalf("a stale plan filled the later preview:\n%s", screen)
	}
	answer(s, reviewPlan())
	if screen := paint(t, host, 120, 20); !strings.Contains(screen, "safe.go") || !strings.Contains(screen, "11:00") {
		t.Fatalf("the current plan is not shown:\n%s", screen)
	}
}
