package viewmodel

import (
	"strings"
	"testing"

	"crude/core/shellport"
	"crude/tui/screens/problems"
	"crude/tui/ui"
	"crude/types/insight"
	"crude/types/statuscenter"
)

type coverageFixture struct {
	shellport.LSPService
	insights insight.ProjectInsights
	plan     insight.AssemblyPlan
	servers  []statuscenter.LSPServerHealth
}

func (f *coverageFixture) ProjectInsights() insight.ProjectInsights              { return f.insights }
func (f *coverageFixture) Assembly() insight.AssemblyPlan                        { return f.plan }
func (f *coverageFixture) ServerHealthSnapshots() []statuscenter.LSPServerHealth { return f.servers }

// problemByRef finds the pane row of one diagnostic in one exact batch.
func problemByRef(model problems.Model, factsID string, index int) (problems.Problem, bool) {
	for _, problem := range model.Problems {
		if ref, ok := problem.Ref.(problemRef); ok && ref.factsID == factsID && ref.index == index {
			return problem, true
		}
	}
	return problems.Problem{}, false
}

// The Problems status says how many language servers work, or, when the user
// must act, the state and the control that fixes it. It never lists
// installation detail.
func TestProblemsStatusCountsServersOrNamesNextStep(t *testing.T) {
	app, ed, _, facts := qualifiedDiagnosticApp(t)
	f := &coverageFixture{LSPService: app.ws.LSPMgr()}
	app.ws = &diagnosticFixtureWorkspace{Workspace: app.ws, service: f, root: app.ws.RootPath()}
	app.lspFacts = map[string]insight.Diagnostics{ed.FilePath(): facts}
	for _, tc := range []struct {
		name       string
		status     insight.LanguageServiceStatus
		servers    []statuscenter.LSPServerHealth
		wantAction string
		wantLabel  string
		wantText   string
		attention  bool
	}{
		{"restricted", insight.LanguageServiceStatus{State: insight.LanguageRestricted, Message: "Language servers do not run in restricted mode.", Action: insight.LanguageActionTrustFolder, ActionLabel: "Trust folder"}, nil, "language:trust", "Trust folder", "Language servers do not run in restricted mode.", true},
		{"off", insight.LanguageServiceStatus{State: insight.LanguageOff, Message: "Language servers are off.", Action: insight.LanguageActionOpenSettings, ActionLabel: "Open settings"}, nil, "language:settings", "Open settings", "Language servers are off.", true},
		{"paused", insight.LanguageServiceStatus{State: insight.LanguageOff, Message: "Language servers are stopped.", Action: insight.LanguageActionStartServers, ActionLabel: "Start language servers"}, nil, "lsp:start-all", "Start language servers", "Language servers are stopped.", true},
		{"two running", insight.LanguageServiceStatus{State: insight.LanguageReady}, []statuscenter.LSPServerHealth{{Name: "gopls", Connected: true}, {Name: "pyright", Connected: true}, {Name: "rust", Starting: true}}, "language:settings", "Language settings", "2 language servers running, 1 starting", false},
		{"one failed", insight.LanguageServiceStatus{State: insight.LanguageReady}, []statuscenter.LSPServerHealth{{Name: "gopls", Connected: true}, {Name: "rust", Error: "exit 1"}}, "language:settings", "Language settings", "1 language server running, 1 stopped with an error", true},
		{"none", insight.LanguageServiceStatus{State: insight.LanguageReady}, nil, "language:settings", "Language settings", "No language server is running.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.insights = insight.ProjectInsights{Language: tc.status, DiagnosticsObserved: 1}
			f.servers = tc.servers
			model := app.problemsModel()
			status := model.Status
			if status.Action != tc.wantAction || status.ActionLabel != tc.wantLabel || status.Text != tc.wantText || status.Attention != tc.attention {
				t.Fatalf("status = %+v, want %q %q %q attention=%v", status, tc.wantAction, tc.wantLabel, tc.wantText, tc.attention)
			}
			if len(model.Problems) != 1 || model.Problems[0].Severity != problems.Error || model.Problems[0].Note != "" {
				t.Fatalf("problems = %+v", model.Problems)
			}
			if strings.Contains(status.Text, "gopls") || strings.Contains(status.Text, "Scope") {
				t.Fatalf("status lists installation detail: %q", status.Text)
			}
		})
	}
}

// Every diagnostic reaches the pane with its 1-based position, source and
// workspace-relative folder.
func TestProblemsModelCarriesEveryDiagnostic(t *testing.T) {
	app, ed, _, facts := qualifiedDiagnosticApp(t)
	path := ed.FilePath()
	facts.Diagnostics = []insight.LSPDiagnostic{
		{Line: 8, Col: 2, Severity: insight.DiagWarning, Message: "late warning", Source: "vet"},
		{Line: 4, Col: 9, Severity: insight.DiagError, Message: "second error", Source: "compiler"},
	}
	app.lspFacts = map[string]insight.Diagnostics{path: facts}
	model := app.problemsModel()
	if len(model.Problems) != 2 {
		t.Fatalf("problems = %d, want 2", len(model.Problems))
	}
	second, ok := problemByRef(model, facts.ID, 1)
	if !ok || second.Line != 5 || second.Col != 10 || second.Source != "compiler" || second.Severity != problems.Error || second.Path != path {
		t.Fatalf("second problem = %+v", second)
	}
}

type restartRecorder struct {
	*coverageFixture
	restarted []string
}

func (r *restartRecorder) RestartServer(id string) bool {
	r.restarted = append(r.restarted, id)
	return true
}

// The Problems button for a failed server restarts that server, never opens
// Settings and never restarts the servers that still work. Several failures
// share one button.
func TestProblemsRestartButtonRestartsOnlyFailedServers(t *testing.T) {
	app, ed, _, facts := qualifiedDiagnosticApp(t)
	fixture := &restartRecorder{coverageFixture: &coverageFixture{LSPService: app.ws.LSPMgr()}}
	app.ws = &diagnosticFixtureWorkspace{Workspace: app.ws, service: fixture, root: app.ws.RootPath()}
	app.lspFacts = map[string]insight.Diagnostics{ed.FilePath(): facts}
	failed := insight.LanguageServiceStatus{State: insight.LanguageFailed, Label: "Language server failed",
		Message: "gopls failed. The server reported an error. Check its installation and restart it.",
		Action:  insight.LanguageActionRestartServer, ActionLabel: "Restart gopls"}
	fixture.insights = insight.ProjectInsights{Language: failed, DiagnosticsObserved: 1}
	fixture.servers = []statuscenter.LSPServerHealth{{ID: "go", Name: "gopls", Error: "exit 2"}, {ID: "py", Name: "pyright", Connected: true}}
	status := app.problemsModel().Status
	if status.Action != "lsp:restart-failed" || status.ActionLabel != "Restart gopls" {
		t.Fatalf("one failure: %+v", status)
	}
	app.Update(problems.ActionMsg{Action: status.Action})
	if len(fixture.restarted) != 1 || fixture.restarted[0] != "go" || app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings) {
		t.Fatalf("restart = %v, settings open = %v", fixture.restarted, app.overlayHost.IsOpen(ui.OverlayWorkspaceSettings))
	}
	fixture.restarted = nil
	fixture.servers = append(fixture.servers, statuscenter.LSPServerHealth{ID: "ts", Name: "tsserver", Error: "exit 1"})
	if status := app.problemsModel().Status; status.ActionLabel != "Restart failed servers" {
		t.Fatalf("two failures: %+v", status)
	}
	app.Update(problems.ActionMsg{Action: "lsp:restart-failed"})
	if strings.Join(fixture.restarted, ",") != "go,ts" {
		t.Fatalf("restart = %v", fixture.restarted)
	}
}
