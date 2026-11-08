package settings

import (
	"slices"
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// The agent command shell is chosen on Settings › Agents › Main Agent, from
// the shells detected on the core's machine. Auto names what it resolves to,
// a shell from another machine stays selected, and Auto is stored as absent.
// Searching Settings for the field finds the Agents page that holds it. The
// Main Agent's fields were not searched, so "command shell" found no page.
func TestSettingsSearchFindsMainAgentFields(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{TerminalShells: []types.TerminalShell{{ID: "cmd", Label: "Command Prompt", Path: `C:\cmd.exe`}}}, nil)
	for _, query := range []string{"command shell", "Concurrent sub-agents"} {
		m.SetSearchQuery(query)
		if visible := m.visibleCategoryIndices(); !slices.Contains(visible, catAgents) {
			t.Fatalf("search %q shows pages %v; want the Agents page", query, visible)
		}
		if target := m.searchTarget(); target != catAgents {
			t.Fatalf("search %q lands on page %d; want Agents", query, target)
		}
	}
	m.SetSearchQuery("no such setting anywhere")
	if visible := m.visibleCategoryIndices(); len(visible) != 0 {
		t.Fatalf("an unmatched search shows pages %v", visible)
	}
}

func TestMainAgentChoosesTheAgentCommandShell(t *testing.T) {
	shells := []types.TerminalShell{
		{ID: "pwsh", Label: "PowerShell 7", Path: `C:\pwsh.exe`},
		{ID: "cmd", Label: "Command Prompt", Path: `C:\cmd.exe`},
		{ID: "git-bash", Label: "Git Bash", Path: `C:\Git\bin\bash.exe`},
		{ID: "wsl", Label: "WSL", Path: `C:\wsl.exe`},
	}
	m := New(theme.DefaultDark(), types.AppSettings{Agents: types.AgentsSettings{Shell: "zsh"}, TerminalShells: shells}, nil)
	if m.hasUnsavedPreferences() {
		t.Fatal("an untouched agent shell reports unsaved changes")
	}
	s := NewSurface(theme.DefaultDark(), m)
	m.SelectCategory(catAgents)
	host := ui.NewHost(theme.DefaultDark(), s)
	s.SetFocusHandler(func(id ui.ComponentID) { host.Focus(id) })
	host.Resize(130, 40)
	host.Layout()
	c := s.agents
	c.begin("role/default", false)
	host.MarkLayout()
	host.Layout()
	field := c.draft.Field(fieldAgentShell)
	if field == nil || field.Label != "Command shell" || field.Type != ui.FieldSelect {
		t.Fatal("Main Agent has no Command shell select")
	}
	var labels []string
	for _, option := range field.Options {
		labels = append(labels, option.Label)
	}
	if !slices.Equal(labels, []string{"Auto (Git Bash)", "PowerShell 7", "Command Prompt", "Git Bash", "zsh (not installed)"}) {
		t.Fatalf("shell choices = %v; want Auto named, WSL excluded and the synced choice kept", labels)
	}
	if field.Value.String() != "zsh" {
		t.Fatal("a shell synced from another machine was silently replaced")
	}
	if !c.form.FocusField(fieldAgentShell) {
		t.Fatal("Command shell cannot be reached")
	}
	buf := surface.NewCellBuffer(130, 40)
	host.Paint(buf)
	if !strings.Contains(bufferPlain(buf), "Command shell") {
		t.Fatal("Command shell is not painted")
	}

	c.draft.SetValue(fieldAgentShell, ui.StringValue("pwsh"))
	if !c.apply() || m.AppSettings().Agents.Shell != "pwsh" {
		t.Fatalf("saved shell = %q; want pwsh", m.AppSettings().Agents.Shell)
	}
	c.begin("role/default", false)
	if got := c.draft.Field(fieldAgentShell).Value.String(); got != "pwsh" {
		t.Fatalf("reopened shell = %q; want pwsh", got)
	}
	c.draft.SetValue(fieldAgentShell, ui.StringValue(types.AgentShellAuto))
	if !c.apply() || m.AppSettings().Agents.Shell != "" {
		t.Fatalf("Auto stored as %q; want absent", m.AppSettings().Agents.Shell)
	}
	if path := agentControlPath(catMain, fieldAgentShell); path != "agents.shell" {
		t.Fatalf("control path = %q", path)
	}
	for _, role := range []int{catResearcher, catCrafter} {
		if form := m.categories[role].Form; form != nil && form.Field(fieldAgentShell) != nil {
			t.Fatal("a role page offers its own command shell; one shell runs every agent's commands")
		}
	}
}
