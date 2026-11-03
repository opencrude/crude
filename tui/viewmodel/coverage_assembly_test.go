package viewmodel

import (
	"strings"
	"testing"

	"crude/shellport"
	"crude/tui/screens/statusoverview"
	"crude/types"
)

type coverageFixture struct {
	shellport.LSPService
	insights types.ProjectInsights
	plan     types.AssemblyPlan
	servers  []types.LSPServerHealth
}

func (f *coverageFixture) ProjectInsights() types.ProjectInsights         { return f.insights }
func (f *coverageFixture) Assembly() types.AssemblyPlan                   { return f.plan }
func (f *coverageFixture) ServerHealthSnapshots() []types.LSPServerHealth { return f.servers }

func coverageItem(t *testing.T, items []statusoverview.Item, id string) statusoverview.Item {
	t.Helper()
	for _, item := range items {
		if item.ID == id {
			return item
		}
	}
	t.Fatalf("missing item %s", id)
	return statusoverview.Item{}
}

func coverageText(item statusoverview.Item) string {
	text := item.Summary
	for _, line := range item.Lines {
		text += " " + line.Value
	}
	return text
}

func TestCoveragePanelStatesAndCounts(t *testing.T) {
	app, ed, _, facts := qualifiedDiagnosticApp(t)
	f := &coverageFixture{LSPService: app.ws.LSPMgr(), plan: types.AssemblyPlan{
		State: types.AssemblyReady, Languages: []types.AssemblyLanguage{
			{ID: "sample", Name: "Sample", Status: types.ToolReady, Scope: types.CoverageProject, Reason: "Checks the whole project."},
			{ID: "missing", Name: "Missing", Status: types.ToolMissing, Reason: "Install the server."},
			{ID: "none", Name: "None", Status: types.ToolNoServer, Reason: "No server is known."},
		},
	}}
	app.ws = &diagnosticFixtureWorkspace{Workspace: app.ws, service: f, root: app.ws.RootPath()}
	app.lspFacts = map[string]types.InsightDiagnostics{ed.FilePath(): facts}
	for _, tc := range []struct {
		name   string
		status types.LanguageServiceStatus
		server types.LSPServerHealth
		want   string
	}{
		{"restricted", types.LanguageServiceStatus{State: types.LanguageRestricted, Message: "Language servers do not run in restricted mode.", Action: types.LanguageActionTrustFolder, ActionLabel: "Trust folder"}, types.LSPServerHealth{}, "Trust folder"},
		{"off", types.LanguageServiceStatus{State: types.LanguageOff, Message: "Language servers are off. Turn them on in Language servers settings.", Action: types.LanguageActionOpenSettings, ActionLabel: "Open settings"}, types.LSPServerHealth{}, "Open settings"},
		{"paused", types.LanguageServiceStatus{State: types.LanguageOff, Message: "Language servers are stopped. Start language servers to check files.", Action: types.LanguageActionStartServers, ActionLabel: "Start Language Servers"}, types.LSPServerHealth{}, "Start Language Servers"},
		{"stopped", types.LanguageServiceStatus{State: types.LanguageIdle}, types.LSPServerHealth{ID: "s", Status: "Stopped", Scope: types.CoverageProject}, "Stopped"},
		{"starting", types.LanguageServiceStatus{State: types.LanguageStarting}, types.LSPServerHealth{ID: "s", Starting: true, Scope: types.CoverageProject}, "Checking…"},
		{"failed", types.LanguageServiceStatus{State: types.LanguageFailed}, types.LSPServerHealth{ID: "s", Error: "failed", Scope: types.CoverageProject}, "Language server failed"},
		{"other running", types.LanguageServiceStatus{State: types.LanguageReady}, types.LSPServerHealth{ID: "other", Connected: true, Scope: types.CoverageProject}, "Running"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.insights = types.ProjectInsights{Language: tc.status, Errors: 1, Warnings: 2}
			f.servers = nil
			if tc.server.ID != "" {
				f.servers = []types.LSPServerHealth{tc.server}
			}
			items := app.problemPanelItems()
			if got := coverageItem(t, items, "workspace:summary").Summary; got != "1 error, 2 warnings." {
				t.Fatal(got)
			}
			if got := coverageItem(t, items, "problem-file:"+ed.FilePath()).Summary; got != "1 problem" {
				t.Fatal(got)
			}
			lang := coverageItem(t, items, "language:sample")
			text := coverageText(lang)
			if lang.Summary != "Installed" || !strings.Contains(text, "may not be running") || strings.Contains(text, "Checks the whole project") {
				t.Fatalf("%+v", lang)
			}
			if tc.server.ID == "" && !strings.Contains(text, tc.want) {
				t.Fatal(text)
			}
			if tc.server.ID != "" {
				server := coverageItem(t, items, "lsp:"+tc.server.ID)
				if server.Summary != tc.want || strings.Contains(coverageText(server), "Checks the whole project") {
					t.Fatalf("%+v", server)
				}
			}
			if item := coverageItem(t, items, "language:missing"); !item.PrimaryEnabled || item.PrimaryLabel != "Check again" {
				t.Fatalf("%+v", item)
			}
			if item := coverageItem(t, items, "language:none"); item.Summary != "No server" {
				t.Fatalf("%+v", item)
			}
		})
	}
	f.insights = types.ProjectInsights{Errors: 2, Warnings: 1}
	if got := coverageItem(t, app.problemPanelItems(), "workspace:summary").Summary; got != "2 errors, 1 warning." {
		t.Fatal(got)
	}
	facts.Diagnostics = append(facts.Diagnostics, facts.Diagnostics[0])
	app.lspFacts[ed.FilePath()] = facts
	if got := coverageItem(t, app.problemPanelItems(), "problem-file:"+ed.FilePath()).Summary; got != "2 problems" {
		t.Fatal(got)
	}
}
