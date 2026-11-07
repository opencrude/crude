package viewmodel

import (
	"strings"
	"testing"

	"crude/shellport"
	"crude/tui/screens/problems"
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
	app.lspFacts = map[string]types.InsightDiagnostics{ed.FilePath(): facts}
	for _, tc := range []struct {
		name       string
		status     types.LanguageServiceStatus
		servers    []types.LSPServerHealth
		wantAction string
		wantLabel  string
		wantText   string
		attention  bool
	}{
		{"restricted", types.LanguageServiceStatus{State: types.LanguageRestricted, Message: "Language servers do not run in restricted mode.", Action: types.LanguageActionTrustFolder, ActionLabel: "Trust folder"}, nil, "language:trust", "Trust folder", "Language servers do not run in restricted mode.", true},
		{"off", types.LanguageServiceStatus{State: types.LanguageOff, Message: "Language servers are off.", Action: types.LanguageActionOpenSettings, ActionLabel: "Open settings"}, nil, "language:settings", "Open settings", "Language servers are off.", true},
		{"paused", types.LanguageServiceStatus{State: types.LanguageOff, Message: "Language servers are stopped.", Action: types.LanguageActionStartServers, ActionLabel: "Start language servers"}, nil, "lsp:start-all", "Start language servers", "Language servers are stopped.", true},
		{"two running", types.LanguageServiceStatus{State: types.LanguageReady}, []types.LSPServerHealth{{Name: "gopls", Connected: true}, {Name: "pyright", Connected: true}, {Name: "rust", Starting: true}}, "language:settings", "Language settings", "2 language servers running, 1 starting", false},
		{"one failed", types.LanguageServiceStatus{State: types.LanguageReady}, []types.LSPServerHealth{{Name: "gopls", Connected: true}, {Name: "rust", Error: "exit 1"}}, "language:settings", "Language settings", "1 language server running, 1 stopped with an error", true},
		{"none", types.LanguageServiceStatus{State: types.LanguageReady}, nil, "language:settings", "Language settings", "No language server is running.", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f.insights = types.ProjectInsights{Language: tc.status, DiagnosticsObserved: 1}
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
	facts.Diagnostics = []types.LSPDiagnostic{
		{Line: 8, Col: 2, Severity: types.DiagWarning, Message: "late warning", Source: "vet"},
		{Line: 4, Col: 9, Severity: types.DiagError, Message: "second error", Source: "compiler"},
	}
	app.lspFacts = map[string]types.InsightDiagnostics{path: facts}
	model := app.problemsModel()
	if len(model.Problems) != 2 {
		t.Fatalf("problems = %d, want 2", len(model.Problems))
	}
	second, ok := problemByRef(model, facts.ID, 1)
	if !ok || second.Line != 5 || second.Col != 10 || second.Source != "compiler" || second.Severity != problems.Error || second.Path != path {
		t.Fatalf("second problem = %+v", second)
	}
}
