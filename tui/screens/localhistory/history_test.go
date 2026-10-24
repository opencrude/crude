package localhistory

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	model "crude/localhistory"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

func TestTimelinePreviewAndRestoreChoiceContract(t *testing.T) {
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(72, 20)
	host.Layout()
	host.Focus(ScreenID)
	s.Handle(ui.EventContext{}, ui.CustomEvent(TimelineMsg{Records: []model.SnapshotRecord{{ID: "snap", CreatedAt: time.Unix(1, 0).UnixNano(), Kind: "manual", Label: "Before refactor"}}}))
	result := host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.ID != ActionPreview {
		t.Fatalf("preview action=%+v", result.Action)
	}
	s.Handle(ui.EventContext{}, ui.CustomEvent(PlanMsg{Plan: model.RestorePlan{ID: "plan", Changes: []model.Change{
		{Path: "safe.go", Action: model.ChangeRestore},
		{Path: "conflict.go", Action: model.ChangeRestore, ConflictReason: "changed later", ExactAllowed: true},
		{Path: "escape", Action: model.ChangeRestore, UnsafeReason: "symlink target escapes workspace"},
	}}}))
	if !s.selected["safe.go"] || s.selected["conflict.go"] || s.selected["escape"] {
		t.Fatalf("default selection=%v", s.selected)
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	host.Handle(tea.KeyPressMsg{Code: 'e'})
	if !s.exact["conflict.go"] || !s.selected["conflict.go"] {
		t.Fatalf("exact=%v selected=%v", s.exact, s.selected)
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action != nil || !s.armed {
		t.Fatalf("first enter must arm restore: %+v", result.Action)
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if result.Action == nil || result.Action.ID != ActionApply {
		t.Fatalf("apply action=%+v", result.Action)
	}
	request := result.Action.Payload.(ApplyRequest)
	if len(request.Choices) != 2 || !request.Choices[1].Exact {
		t.Fatalf("choices=%+v", request.Choices)
	}
}

func TestHistoryScreenPaintsLoadingErrorAndEmptyStates(t *testing.T) {
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(60, 16)
	host.Layout()
	buf := surface.NewCellBuffer(60, 16)
	host.Paint(buf)
	if got := plain(buf, 14); got == "" {
		t.Fatal("loading footer missing")
	}
	s.Handle(ui.EventContext{}, ui.CustomEvent(TimelineMsg{}))
	host.Layout()
	buf = surface.NewCellBuffer(60, 16)
	host.Paint(buf)
	if len(s.list.Items()) != 1 || s.list.Items()[0].Label != "No history snapshots yet" {
		t.Fatalf("empty items=%+v", s.list.Items())
	}
	s.Handle(ui.EventContext{}, ui.CustomEvent(TimelineMsg{Error: "store corrupt"}))
	host.Paint(buf)
	if s.errorText == "" {
		t.Fatal("error state missing")
	}
}

func TestAppliedRestoreOffersUndoViaSafetySnapshot(t *testing.T) {
	s := New(theme.DefaultDark())
	s.Handle(ui.EventContext{}, ui.CustomEvent(AppliedMsg{Result: model.RestoreResult{SafetySnapshotID: "safety", Paths: []string{"a"}}}))
	res := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'u'}))
	if res.Action == nil || res.Action.ID != ActionPreview || res.Action.Payload.(PreviewRequest).SnapshotID != "safety" {
		t.Fatalf("undo action=%+v", res.Action)
	}
}

func TestRestoreLoadingCannotIssueAnotherApply(t *testing.T) {
	s := New(theme.DefaultDark())
	s.Handle(ui.EventContext{}, ui.CustomEvent(PlanMsg{Plan: model.RestorePlan{ID: "review", Changes: []model.Change{{Path: "file", Action: model.ChangeRestore}}}}))
	s.activate() // arm
	if got := s.activate(); got.Action == nil || got.Action.ID != ActionApply {
		t.Fatal("initial confirmed apply absent")
	}
	for range 4 {
		if got := s.activate(); got.Action != nil {
			t.Fatal("in-flight restore allowed another mutation")
		}
	}
}

func TestTimelineMouseActivationMatchesKeyboard(t *testing.T) {
	s := New(theme.DefaultDark())
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(60, 16)
	host.Layout()
	host.Focus(ScreenID)
	s.Handle(ui.EventContext{}, ui.CustomEvent(TimelineMsg{Records: []model.SnapshotRecord{{ID: "snap", CreatedAt: 1}}}))
	result := host.Handle(tea.MouseClickMsg{X: 1, Y: 0, Button: tea.MouseLeft})
	if result.Action == nil || result.Action.ID != ActionPreview {
		t.Fatalf("click action=%+v", result.Action)
	}
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

// An unresolved original restore allows only observation of that request; a
// known committed partial restore keeps its safety Undo instead of a fresh Apply.
func TestRestoreFailureEvidenceGatesFurtherMutation(t *testing.T) {
	s := New(theme.DefaultDark())
	review := PlanMsg{Plan: model.RestorePlan{ID: "review", Changes: []model.Change{{Path: "file", Action: model.ChangeRestore}}}}
	s.Handle(ui.EventContext{}, ui.CustomEvent(review))
	s.activate()
	if got := s.activate(); got.Action == nil || got.Action.ID != ActionApply {
		t.Fatal("initial confirmed apply absent")
	}
	s.Handle(ui.EventContext{}, ui.CustomEvent(AppliedMsg{RequestID: "original", Error: "transport lost · Check original result; do not repeat"}))
	s.Handle(ui.EventContext{}, ui.CustomEvent(review))
	for _, key := range []rune{tea.KeyEnter, 'u'} {
		res := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: key}))
		if res.Action != nil && res.Action.ID != ActionCheck {
			t.Fatalf("unresolved restore offered %q", res.Action.ID)
		}
	}
	host := ui.NewHost(theme.DefaultDark(), s)
	host.Resize(72, 20)
	host.Layout()
	buf := surface.NewCellBuffer(72, 20)
	host.Paint(buf)
	screen := ""
	for y := range 20 {
		screen += plain(buf, y) + "\n"
	}
	if !strings.Contains(screen, "Check original restore result") || !strings.Contains(screen, "do not repeat") || strings.Contains(screen, "Apply") {
		t.Fatalf("unresolved restore screen:\n%s", screen)
	}
	s.loading = false
	if got := s.activate(); got.Action == nil || got.Action.ID != ActionCheck || got.Action.Payload != "original" {
		t.Fatalf("check action=%+v", got.Action)
	}
	s.Handle(ui.EventContext{}, ui.CustomEvent(AppliedMsg{Result: model.RestoreResult{SafetySnapshotID: "safety", Paths: []string{"file"}}, Warning: "publication failed", Incomplete: true}))
	if s.requestID != "" || !strings.Contains(s.status, "Restore incomplete") {
		t.Fatalf("committed partial restore status=%q request=%q", s.status, s.requestID)
	}
	res := s.Handle(ui.EventContext{}, ui.KeyEvent(tea.KeyPressMsg{Code: 'u'}))
	if res.Action == nil || res.Action.ID != ActionPreview || res.Action.Payload.(PreviewRequest).SnapshotID != "safety" {
		t.Fatalf("committed partial restore lost its safety Undo: %+v", res.Action)
	}
}
