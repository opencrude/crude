package workspace

import (
	"testing"

	ttypes "crude/types"
)

// The reported failure: Crude restarted a failing server again and again and
// nothing told anyone. A server that Crude still restarts by itself is not a
// problem yet; once its restart budget is used up it is, and the workspace
// Aeron may investigate it. The issue exists on the core, without a shell.
func TestLSPIssuesFollowTheRestartBudget(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	restarting := ttypes.LSPServerHealth{ID: "clangd-key", Name: "clangd", Status: "Failed",
		Error: "clangd stopped unexpectedly. Crude is starting it again.", Restarting: true, Restarts: 2}
	for _, issue := range lspIssues([]ttypes.LSPServerHealth{restarting}) {
		t.Fatalf("a server that Crude restarts by itself raised %+v", issue)
	}
	failed := restarting
	failed.Restarting, failed.Restarts = false, 5
	failed.Error = "clangd stopped unexpectedly 5 times in 3 minutes. Find the cause, then restart it."
	syncLSPIssuesFrom(center, []ttypes.LSPServerHealth{failed}, ttypes.AssemblyPlan{})
	issues := center.Issues()
	if len(issues) != 1 || issues[0].ID != "lsp:clangd-key" || !issues[0].Investigate || issues[0].Severity != ttypes.SeverityError ||
		issues[0].Summary != "clangd failed" || issues[0].Detail != failed.Error {
		t.Fatalf("issues after the budget was used up = %+v", issues)
	}
	// Restart by the user or Aeron: the server connects and the issue resolves.
	syncLSPIssuesFrom(center, []ttypes.LSPServerHealth{{ID: "clangd-key", Name: "clangd", Status: "Connected"}}, ttypes.AssemblyPlan{})
	if issues := center.Issues(); len(issues) != 1 || issues[0].ResolvedAt.IsZero() {
		t.Fatalf("a connected server kept its open issue: %+v", issues)
	}
}

// The user's example: clangd reports a missing header in the project's C
// files. A server whose closed files Crude checks and that finds errors in
// more than one file, and in at least one in five of them, probably cannot
// see the project. A server that reports the project itself sends only files
// with problems, so the ratio says nothing about it.
func TestLSPIssueForAServerThatCannotSeeTheProject(t *testing.T) {
	clangd := ttypes.LSPServerHealth{ID: "c", Name: "clangd", Status: "Connected", Scope: ttypes.CoverageOpenFiles,
		FilesTracked: 2, FilesAffected: 2, Errors: 19}
	issues := lspIssues([]ttypes.LSPServerHealth{clangd})
	if len(issues) != 1 || issues[0].ID != "lsp-results:c" || !issues[0].Investigate ||
		issues[0].Summary != "clangd reports errors in 2 of 2 checked files" {
		t.Fatalf("issues = %+v", issues)
	}
	// Counts change while the cause stays the same: the detail is stable.
	clangd.FilesTracked, clangd.FilesAffected = 3, 2
	if again := lspIssues([]ttypes.LSPServerHealth{clangd}); len(again) != 1 || again[0].Detail != issues[0].Detail {
		t.Fatalf("the cause changed with the counts: %+v", again)
	}
	healthy := clangd
	healthy.FilesTracked, healthy.FilesAffected = 40, 3
	project := clangd
	project.Scope = ttypes.CoverageProject
	if issues := lspIssues([]ttypes.LSPServerHealth{healthy, project}); len(issues) != 0 {
		t.Fatalf("a mostly clean project or a project reporter raised %+v", issues)
	}
}

// An error message from the server itself, such as a failed workspace load,
// is a problem even while the server runs.
func TestLSPIssueForAServerErrorMessage(t *testing.T) {
	issues := lspIssues([]ttypes.LSPServerHealth{{ID: "r", Name: "rust-analyzer", Status: "Connected",
		ErrorMessage: "Failed to load workspaces."}})
	if len(issues) != 1 || issues[0].ID != "lsp-message:r" || !issues[0].Investigate || issues[0].Detail != "Failed to load workspaces." {
		t.Fatalf("issues = %+v", issues)
	}
}

// Found live: one Dockerfile, one YAML file or one TypeScript file with real
// errors raised "the server cannot find the project's headers" and woke Aeron
// for an ordinary error in that file. One file is never "many files".
func TestLSPIssueIgnoresErrorsInASingleFile(t *testing.T) {
	for _, tracked := range []int{1, 2, 5} {
		server := ttypes.LSPServerHealth{ID: "d", Name: "docker-langserver", Status: "Connected", Scope: ttypes.CoverageOpenFiles,
			FilesTracked: tracked, FilesAffected: 1, Errors: 2}
		if issues := lspIssues([]ttypes.LSPServerHealth{server}); len(issues) != 0 {
			t.Fatalf("errors in 1 of %d files raised %+v", tracked, issues)
		}
	}
}

// Found live: the user's settings named a command that does not exist (a
// typing error), and nothing told anyone; the language only showed as
// missing. A command from the settings that is not installed is a problem
// for Aeron to investigate. A catalog server that is not installed is a
// setup choice and raises nothing. The issue resolves when the command is
// corrected.
func TestLSPIssueForAMissingCommandFromSettings(t *testing.T) {
	center := NewStatusCenter(nil, nil)
	typo := ttypes.AssemblyLanguage{ID: "bash", Name: "Shell", Command: "bash-language-servr", Status: ttypes.ToolMissing,
		Source: ttypes.ToolSourceSettings, Reason: "Shell needs bash-language-servr, which is not installed. Install it yourself, then check again."}
	catalogMissing := ttypes.AssemblyLanguage{ID: "java", Name: "Java", Command: "jdtls", Status: ttypes.ToolMissing,
		Reason: "Java needs jdtls, which is not installed."}
	syncLSPIssuesFrom(center, nil, ttypes.AssemblyPlan{Languages: []ttypes.AssemblyLanguage{typo, catalogMissing}})
	issues := center.Issues()
	if len(issues) != 1 || issues[0].ID != "lsp-command:bash" || !issues[0].Investigate ||
		issues[0].Summary != "Shell server bash-language-servr is not installed" || issues[0].Detail != typo.Reason {
		t.Fatalf("issues = %+v", issues)
	}
	fixed := typo
	fixed.Command, fixed.Status, fixed.Reason = "bash-language-server", ttypes.ToolReady, ""
	syncLSPIssuesFrom(center, nil, ttypes.AssemblyPlan{Languages: []ttypes.AssemblyLanguage{fixed}})
	if issues := center.Issues(); len(issues) != 1 || issues[0].ResolvedAt.IsZero() {
		t.Fatalf("a corrected command kept its issue: %+v", issues)
	}
}
