package capabilitypicker

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func pickerFixture() captypes.Snapshot {
	return captypes.Snapshot{Settings: captypes.Settings{
		Skills:      map[string]captypes.SkillConfig{"review": {ID: "review", Name: "Code review", Scope: captypes.ScopeWorkspace}, "guide": {ID: "guide", Name: "Project guide", Disabled: true}},
		Connections: map[string]captypes.ConnectionConfig{"docs": {ID: "docs", Name: "Documentation", Scope: captypes.ScopeApplication}},
	}}
}

func pickerText(host *ui.Host, w, h int) string {
	host.Resize(w, h)
	host.Layout()
	buf := surface.NewCellBuffer(w, h)
	host.Paint(buf)
	var text strings.Builder
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			text.WriteRune(buf.Get(x, y).Char)
		}
		text.WriteByte('\n')
	}
	return text.String()
}

func TestSimplePickerPaintSearchAndScopedToggle(t *testing.T) {
	snapshot := pickerFixture()
	screen := New(theme.DefaultDark(), snapshot)
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	for _, size := range []ui.Size{{W: 36, H: 12}, {W: 76, H: 22}} {
		paint := pickerText(host, size.W, size.H)
		for _, label := range []string{"Skills", "MCPs", "Code review", "Deactivate", "Close"} {
			if !strings.Contains(paint, label) {
				t.Fatalf("missing %s:\n%s", label, paint)
			}
		}
		for _, removed := range []string{"Tools", "Resources", "Prompts", "Fetch", "Stage", "Inspect"} {
			if strings.Contains(paint, removed) {
				t.Fatal("old staging UI remains", removed)
			}
		}
	}
	screen.SetQuery("guide")
	paint := pickerText(host, 76, 22)
	if strings.Contains(paint, "Code review") || !strings.Contains(paint, "Project guide") || !strings.Contains(paint, "Inactive") {
		t.Fatal("search did not filter friendly names", paint)
	}
	request, ok := screen.BeginAction()
	if !ok || request.ID != "guide" || request.Operation != "enable" || request.Scope != captypes.ScopeApplication {
		t.Fatal("wrong exact toggle", request)
	}
	if _, ok := screen.BeginAction(); ok {
		t.Fatal("duplicate busy mutation admitted")
	}
	screen.SetSnapshot(snapshot)
	if !screen.Busy() || screen.filter.Value() != "guide" {
		t.Fatal("observation cleared mutation or search")
	}
	screen.Apply(snapshot, errors.New("write failed"))
	if screen.Busy() || !strings.Contains(pickerText(host, 76, 22), "write failed") {
		t.Fatal("error did not settle and stay visible")
	}
	screen.SetQuery("review")
	request, ok = screen.BeginAction()
	if !ok || request.Operation != "disable" || request.Scope != captypes.ScopeWorkspace {
		t.Fatal("workspace binding was not preserved", request)
	}
}

func TestSimplePickerPointerSelectsKeyboardToggles(t *testing.T) {
	screen := New(theme.DefaultDark(), pickerFixture())
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	pickerText(host, 76, 22)
	screen.FocusSearch()
	if host.Focused() != screen.filter.ID() {
		t.Fatal("search not focused")
	}
	host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
	if host.Focused() != screen.list.ID() {
		t.Fatal("Enter did not reach results")
	}
	r := screen.list.Bounds()
	result := host.Handle(tea.MouseClickMsg{X: r.X + 2, Y: r.Y + 1, Button: tea.MouseLeft})
	if result.Action != nil || screen.Busy() {
		t.Fatal("row click mutated instead of selecting", result)
	}
	if value, _ := screen.selected(); value.id != "guide" {
		t.Fatal("click selected wrong row", value)
	}
	if !strings.Contains(pickerText(host, 76, 22), "Activate") {
		t.Fatal("selection did not refresh action")
	}
	for _, key := range []rune{tea.KeyEnter, tea.KeySpace} {
		result = host.Handle(tea.KeyPressMsg{Code: key})
		if result.Action == nil || result.Action.ID != "capability-picker.toggle" {
			t.Fatal("key did not request toggle", key, result)
		}
	}
	result = host.Handle(tea.KeyPressMsg{Code: tea.KeyEsc})
	if result.Action == nil || result.Action.ID != "capability-picker.close" {
		t.Fatal("Esc did not close")
	}
}

func TestSimplePickerSnapshotPreservesScrollAndRejectsEmptyActivation(t *testing.T) {
	snapshot := pickerFixture()
	for i := 0; i < 30; i++ {
		id := fmt.Sprintf("skill-%02d", i)
		snapshot.Settings.Skills[id] = captypes.SkillConfig{ID: id, Name: id}
	}
	screen := New(theme.DefaultDark(), snapshot)
	host := ui.NewHost(theme.DefaultDark(), screen)
	pickerText(host, 36, 12)
	screen.list.SelectIndex(20)
	offset := screen.list.Scroll().Offset()
	selected, _ := screen.selected()
	screen.SetSnapshot(snapshot)
	if current, _ := screen.selected(); current.id != selected.id || screen.list.Scroll().Offset() != offset {
		t.Fatal("refresh lost selection/scroll")
	}
	screen.SetQuery("absent")
	if _, ok := screen.BeginAction(); ok {
		t.Fatal("empty search mutated registration")
	}
	screen.SetQuery("")
	screen.SetCategory(captypes.KindMCP)
	if paint := pickerText(host, 76, 22); !strings.Contains(paint, "Active · Not connected") {
		t.Fatal("activation implied live connection", paint)
	}
	request, ok := screen.BeginAction()
	if !ok || request.Kind != captypes.KindMCP || request.Operation != "disable" {
		t.Fatal("MCP cannot deactivate", request)
	}
}

// The modal explains the selection: description, source, scope and observed
// connection facts, never a connection claim the snapshot does not report.
func TestSimplePickerDetailExplainsSelectionWithObservedFacts(t *testing.T) {
	snapshot := pickerFixture()
	snapshot.Entries = []captypes.Entry{
		{ID: "review", Kind: captypes.KindSkill, Description: "Reviews diffs for correctness and missing tests."},
		{ID: "docs", Kind: captypes.KindMCP, Description: "Library documentation lookup."},
	}
	snapshot.Settings.Sources = map[string]captypes.SourceConfig{"vendor": {Name: "Vendor skills"}}
	review := snapshot.Settings.Skills["review"]
	review.Source = "vendor"
	snapshot.Settings.Skills["review"] = review
	screen := New(theme.DefaultDark(), snapshot)
	host := ui.NewHost(theme.DefaultDark(), screen)
	screen.SetQuery("review")
	paint := pickerText(host, 84, 28)
	for _, want := range []string{"Code review", "Skill", "Reviews diffs for correctness", "Vendor skills", "This workspace", "Automatic"} {
		if !strings.Contains(paint, want) {
			t.Fatalf("detail pane missing %q:\n%s", want, paint)
		}
	}
	if !strings.Contains(paint, "1 of 2 skills active") {
		t.Fatalf("summary does not count activation:\n%s", paint)
	}
	screen.SetQuery("")
	screen.SetCategory(captypes.KindMCP)
	host.MarkLayout()
	paint = pickerText(host, 84, 28)
	if !strings.Contains(paint, "Library documentation lookup.") || !strings.Contains(paint, "Connection  Not connected") || strings.Contains(paint, "Connected ·") {
		t.Fatalf("MCP detail invented or lost connection facts:\n%s", paint)
	}
	snapshot.Connections = []captypes.ConnectionState{{ID: "docs", State: "connected", Tools: []captypes.Tool{{Name: "search"}, {Name: "fetch"}}}}
	screen.SetSnapshot(snapshot)
	if paint = pickerText(host, 84, 28); !strings.Contains(paint, "Active · Connected · 2 tools") || !strings.Contains(paint, "Connection  Connected · 2 tools") {
		t.Fatalf("observed connection was not reported:\n%s", paint)
	}
}
