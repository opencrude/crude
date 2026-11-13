package settings

import (
	"testing"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// Main Agent › Longest wait bounds every agent wait. Blank is Auto (stored as
// absent); a value persists in minutes and reopens unchanged.
func TestMainAgentLongestWaitPersists(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	host := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(130, 40)
	host.Layout()
	c := s.agents
	c.begin("role/default", false)
	field := c.draft.Field(fieldMaxWait)
	if field == nil || field.Label != "Longest wait" || field.Value.String() != "" || field.Unit != "min" {
		t.Fatalf("Main Agent has no Auto Longest wait field: %+v", field)
	}
	if errs := field.Validator(ui.StringValue("0")); len(errs) == 0 {
		t.Fatal("a zero-minute wait bound was accepted")
	}
	c.draft.SetValue(fieldMaxWait, ui.StringValue("25"))
	if !c.apply() || m.AppSettings().Agents.MaxWaitMinutes != 25 {
		t.Fatalf("saved wait bound = %d; want 25", m.AppSettings().Agents.MaxWaitMinutes)
	}
	c.begin("role/default", false)
	if got := c.draft.Field(fieldMaxWait).Value.String(); got != "25" {
		t.Fatalf("reopened wait bound = %q", got)
	}
	c.draft.SetValue(fieldMaxWait, ui.StringValue(""))
	if !c.apply() || m.AppSettings().Agents.MaxWaitMinutes != 0 {
		t.Fatalf("Auto stored as %d; want absent", m.AppSettings().Agents.MaxWaitMinutes)
	}
	if path := agentControlPath(catMain, fieldMaxWait); path != "agents.max_wait_minutes" {
		t.Fatalf("control path = %q", path)
	}
}
