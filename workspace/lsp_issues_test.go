package workspace

import (
	"testing"

	"crude/types/insight"
	"crude/types/statuscenter"
)

// The reported failure: Crude restarted a failing server again and again and
// nothing told anyone. A server that Crude still restarts by itself is not a
// problem yet; once its restart budget is used up it is, and the workspace
// Errand may investigate it. The issue exists on the core, without a shell.
func TestLSPIssuesFollowTheRestartBudget(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	restarting := statuscenter.LSPServerHealth{ID: "clangd-key", Name: "clangd", Status: "Failed",
		Error: "clangd stopped unexpectedly. Crude is starting it again.", Restarting: true, Restarts: 2}
	for _, issue := range lspIssues([]statuscenter.LSPServerHealth{restarting}) {
		t.Fatalf("a server that Crude restarts by itself raised %+v", issue)
	}
	failed := restarting
	failed.Restarting, failed.Restarts = false, 5
	failed.Error = "clangd stopped unexpectedly 5 times in 3 minutes. Find the cause, then restart it."
	syncLSPIssuesFrom(center, []statuscenter.LSPServerHealth{failed}, insight.AssemblyPlan{})
	issues := center.Issues()
	if len(issues) != 1 || issues[0].ID != "lsp:clangd-key" || !issues[0].Investigate || issues[0].Severity != statuscenter.SeverityError ||
		issues[0].Summary != "clangd failed" || issues[0].Detail != failed.Error {
		t.Fatalf("issues after the budget was used up = %+v", issues)
	}
	// Restart by the user or Errand: the server connects and the issue resolves.
	syncLSPIssuesFrom(center, []statuscenter.LSPServerHealth{{ID: "clangd-key", Name: "clangd", Status: "Connected"}}, insight.AssemblyPlan{})
	if issues := center.Issues(); len(issues) != 1 || issues[0].ResolvedAt.IsZero() {
		t.Fatalf("a connected server kept its open issue: %+v", issues)
	}
}

// The user's example: clangd reports a missing header in the project's only
// C header, one checked file of one. A server whose closed files Crude checks
// and that finds errors in at least one in five of them probably cannot see
// the project. A server that reports the project itself sends only files with
// problems, so the ratio says nothing about it.
func TestLSPIssueForAServerThatCannotSeeTheProject(t *testing.T) {
	clangd := statuscenter.LSPServerHealth{ID: "c", Name: "clangd", Status: "Connected", Scope: insight.CoverageOpenFiles,
		FilesTracked: 1, FilesAffected: 1, Errors: 21}
	issues := lspIssues([]statuscenter.LSPServerHealth{clangd})
	if len(issues) != 1 || issues[0].ID != "lsp-results:c" || !issues[0].Investigate ||
		issues[0].Summary != "clangd reports errors in many of the files it checked" {
		t.Fatalf("issues = %+v", issues)
	}
	healthy := clangd
	healthy.FilesTracked, healthy.FilesAffected = 40, 3
	project := clangd
	project.Scope = insight.CoverageProject
	if issues := lspIssues([]statuscenter.LSPServerHealth{healthy, project}); len(issues) != 0 {
		t.Fatalf("a mostly clean project or a project reporter raised %+v", issues)
	}
}

// Found after a real core restart: the issue text carried the counts ("7 of
// 13 checked files"). Each file that the closed-file check finished made a
// new report, so the issue never stayed the same for the settle time and
// Errand never heard about it. While Crude checks files, the report and its
// time do not change.
func TestLSPIssueStaysTheSameReportWhileCountsChange(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	pyright := statuscenter.LSPServerHealth{ID: "py", Name: "pyright-langserver", Status: "Connected", Scope: insight.CoverageOpenFiles,
		FilesTracked: 3, FilesAffected: 2, Errors: 5}
	syncLSPIssuesFrom(center, []statuscenter.LSPServerHealth{pyright}, insight.AssemblyPlan{})
	first := center.Issues()
	if len(first) != 1 {
		t.Fatalf("issues = %+v", first)
	}
	for tracked := 4; tracked <= 13; tracked++ {
		pyright.FilesTracked, pyright.FilesAffected = tracked, tracked/2
		syncLSPIssuesFrom(center, []statuscenter.LSPServerHealth{pyright}, insight.AssemblyPlan{})
	}
	if again := center.Issues(); len(again) != 1 || !again[0].UpdatedAt.Equal(first[0].UpdatedAt) || !again[0].ResolvedAt.IsZero() {
		t.Fatalf("new counts made a new report: before %+v after %+v", first, again)
	}
}

// An error message from the server itself, such as a failed workspace load,
// is a problem even while the server runs.
func TestLSPIssueForAServerErrorMessage(t *testing.T) {
	issues := lspIssues([]statuscenter.LSPServerHealth{{ID: "r", Name: "rust-analyzer", Status: "Connected",
		ErrorMessage: "Failed to load workspaces."}})
	if len(issues) != 1 || issues[0].ID != "lsp-message:r" || !issues[0].Investigate || issues[0].Detail != "Failed to load workspaces." {
		t.Fatalf("issues = %+v", issues)
	}
}

// Found live: the user's settings named a command that does not exist (a
// typing error), and nothing told anyone; the language only showed as
// missing. A command from the settings that is not installed is a problem
// for Errand to investigate. A catalog server that is not installed is a
// setup choice and raises nothing. The issue resolves when the command is
// corrected.
func TestLSPIssueForAMissingCommandFromSettings(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	typo := insight.AssemblyLanguage{ID: "bash", Name: "Shell", Command: "bash-language-servr", Status: insight.ToolMissing,
		Source: insight.ToolSourceSettings, Reason: "Shell needs bash-language-servr, which is not installed. Install it yourself, then check again."}
	catalogMissing := insight.AssemblyLanguage{ID: "java", Name: "Java", Command: "jdtls", Status: insight.ToolMissing,
		Reason: "Java needs jdtls, which is not installed."}
	syncLSPIssuesFrom(center, nil, insight.AssemblyPlan{Languages: []insight.AssemblyLanguage{typo, catalogMissing}})
	issues := center.Issues()
	if len(issues) != 1 || issues[0].ID != "lsp-command:bash" || !issues[0].Investigate ||
		issues[0].Summary != "Shell server bash-language-servr is not installed" || issues[0].Detail != typo.Reason {
		t.Fatalf("issues = %+v", issues)
	}
	fixed := typo
	fixed.Command, fixed.Status, fixed.Reason = "bash-language-server", insight.ToolReady, ""
	syncLSPIssuesFrom(center, nil, insight.AssemblyPlan{Languages: []insight.AssemblyLanguage{fixed}})
	if issues := center.Issues(); len(issues) != 1 || issues[0].ResolvedAt.IsZero() {
		t.Fatalf("a corrected command kept its issue: %+v", issues)
	}
}
