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
	syncLSPIssuesFrom(center, []ttypes.LSPServerHealth{failed})
	issues := center.Issues()
	if len(issues) != 1 || issues[0].ID != "lsp:clangd-key" || !issues[0].Investigate || issues[0].Severity != ttypes.SeverityError ||
		issues[0].Summary != "clangd failed" || issues[0].Detail != failed.Error {
		t.Fatalf("issues after the budget was used up = %+v", issues)
	}
	// Restart by the user or Aeron: the server connects and the issue resolves.
	syncLSPIssuesFrom(center, []ttypes.LSPServerHealth{{ID: "clangd-key", Name: "clangd", Status: "Connected"}})
	if issues := center.Issues(); len(issues) != 1 || issues[0].ResolvedAt.IsZero() {
		t.Fatalf("a connected server kept its open issue: %+v", issues)
	}
}

// The user's example: clangd reports a missing header in the project's only
// C file. A server whose closed files Crude checks and that finds errors in
// at least one in five of them probably cannot see the project. A server
// that reports the project itself sends only files with problems, so the
// ratio says nothing about it.
func TestLSPIssueForAServerThatCannotSeeTheProject(t *testing.T) {
	clangd := ttypes.LSPServerHealth{ID: "c", Name: "clangd", Status: "Connected", Scope: ttypes.CoverageOpenFiles,
		FilesTracked: 1, FilesAffected: 1, Errors: 19}
	issues := lspIssues([]ttypes.LSPServerHealth{clangd})
	if len(issues) != 1 || issues[0].ID != "lsp-results:c" || !issues[0].Investigate ||
		issues[0].Summary != "clangd reports errors in 1 of 1 checked files" {
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
