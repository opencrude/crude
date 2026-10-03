package launcher

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func TestCapabilitiesShowNamedLiveStateAndHideInactiveEntries(t *testing.T) {
	s, h, buf := testScreen(t, 130, 32, nil)
	s.SetCapabilities(captypes.Snapshot{
		Entries: []captypes.Entry{
			{ID: "team:review", Name: "Code review", Kind: captypes.KindSkill, State: "available", Mode: "automatic"},
			{ID: "local:tests", Name: "Test workflow", Kind: captypes.KindSkill, State: "available", Mode: "manual"},
			{ID: "off", Name: "Disabled skill", Kind: captypes.KindSkill, State: "disabled"},
			{ID: "git", Name: "GitHub", Kind: captypes.KindMCP, State: "not started"},
			{ID: "docs", Name: "Documentation", Kind: captypes.KindMCP, State: "not started"},
			{ID: "cached", Name: "Cached service", Kind: captypes.KindMCP, State: "connected"},
			{ID: "disconnected", Name: "Disconnected service", Kind: captypes.KindMCP, State: "disconnected"},
		},
		Connections: []captypes.ConnectionState{{ID: "git", State: "connected"}, {ID: "docs", State: "needs sign-in"}, {ID: "cached", State: "connected", Cached: true}},
		Bindings:    []captypes.AgentBinding{{AgentID: "running", ID: "team:review", Kind: captypes.KindSkill}},
	})
	h.MarkLayout()
	buf.Clear()
	h.Paint(buf)
	text := bufferText(buf)
	for _, want := range []string{"Code review", "loaded", "Test workflow", "manual", "GitHub", "connected", "Documentation", "needs sign-in"} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing %q:\n%s", want, text)
		}
	}
	for _, absent := range []string{"Disabled skill", "Cached service", "Disconnected service", "Browse skills", "Manage MCP", "Settings"} {
		if strings.Contains(text, absent) {
			t.Fatalf("unwanted %q:\n%s", absent, text)
		}
	}
	r := s.skills.Bounds()
	result := h.Handle(tea.MouseClickMsg{X: r.X + 1, Y: r.Y + 1, Button: tea.MouseLeft})
	if result.Cmd == nil {
		t.Fatal("skill row is not actionable")
	}
	msg, ok := result.Cmd().(CommandMsg)
	if !ok || msg.Action != "skills.manage" || msg.TargetID != "team:review" || msg.Screen != s {
		t.Fatalf("row did not retain exact identity: %+v", msg)
	}
}

func TestCapabilityOverflowScrollsWithoutMovingWorkspaceResults(t *testing.T) {
	s, h, buf := testScreen(t, 120, 30, manyWorkspaces(25))
	var snapshot captypes.Snapshot
	for i := 0; i < 15; i++ {
		snapshot.Entries = append(snapshot.Entries, captypes.Entry{ID: fmt.Sprint(i), Name: fmt.Sprintf("Skill %02d", i), Kind: captypes.KindSkill, State: "available"})
	}
	s.SetCapabilities(snapshot)
	h.MarkLayout()
	h.Paint(buf)
	r, ok := h.ScreenRect(ui.NewID(SkillsID, "scrollbar"))
	if !ok || r.Empty() {
		t.Fatal("overflow lacks a visible scrollbar")
	}
	before := s.ListScrollOffset()
	h.Handle(tea.MouseWheelMsg{X: r.X, Y: r.Y, Button: tea.MouseWheelDown})
	h.MarkLayout()
	buf.Clear()
	h.Paint(buf)
	if s.skills.list.Scroll().Offset() == 0 || s.ListScrollOffset() != before {
		t.Fatal("capability wheel did not retain its own viewport")
	}
	if strings.Contains(bufferText(buf), "Skill 00") {
		t.Fatal("capability scroll didn't change painted names")
	}
	offset := s.skills.list.Scroll().Offset()
	s.SetCapabilities(snapshot)
	h.MarkLayout()
	h.Paint(buf)
	if s.skills.list.Scroll().Offset() != offset {
		t.Fatal("unchanged live snapshot snapped the capability viewport")
	}
}

func TestWorkspaceRightPaneNavigationAndHit(t *testing.T) {
	s, h, _ := testScreen(t, 120, 30, manyWorkspaces(7))
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	h.Handle(tea.KeyPressMsg{Code: tea.KeyDown})
	for _, step := range []struct {
		key  rune
		name string
	}{{tea.KeyDown, "ws-01"}, {tea.KeyDown, "ws-02"}, {tea.KeyUp, "ws-01"}, {tea.KeyUp, "ws-00"}} {
		h.Handle(tea.KeyPressMsg{Code: step.key})
		if item := s.SelectedItem(); item == nil || item.Workspace == nil || item.Workspace.Name != step.name {
			t.Fatalf("after %v got %+v want %s", step.key, item, step.name)
		}
	}
	r := s.list.Bounds()
	if got := s.WorkspaceItemAt(r.W/2, 2); got != nil {
		t.Fatalf("separator targeted a resource: %+v", got)
	}
	if got := s.WorkspaceItemAt(r.W-2, 0); got == nil || got.Name != "ws-00" {
		t.Fatalf("row target=%+v", got)
	}
	if got := s.WorkspaceItemAt(r.W, 0); got != nil {
		t.Fatalf("outside row invented item: %+v", got)
	}
}
