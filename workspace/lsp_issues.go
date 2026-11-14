package workspace

import (
	"fmt"
	"strings"

	ttypes "crude/types"
)

// Language-server issues are derived on the core from the one LSP owner, so
// they exist without an attached shell and no shell writes them. Each issue
// that a person or the workspace Aeron can act on is marked Investigate. A
// server that Crude is restarting by itself is not an issue yet: the restart
// budget decides when it needs help.
const (
	lspIssuePrefix        = "lsp:"
	lspMessageIssuePrefix = "lsp-message:"
	lspResultsIssuePrefix = "lsp-results:"
)

// SyncLSPIssues publishes the issues that the language servers' state shows
// and resolves the ones that no longer apply.
func SyncLSPIssues(center *StatusCenter, manager *LSPManager) {
	if center == nil || manager == nil {
		return
	}
	syncLSPIssuesFrom(center, manager.ServerHealthSnapshots())
}

func syncLSPIssuesFrom(center *StatusCenter, servers []ttypes.LSPServerHealth) {
	open := make(map[string]bool)
	for _, issue := range lspIssues(servers) {
		open[issue.ID] = true
		center.SetIssue(issue)
	}
	for _, issue := range center.Issues() {
		if issue.Owner.Kind == ttypes.ResourceLSP && issue.ResolvedAt.IsZero() && !open[issue.ID] {
			center.ResolveIssue(issue.ID)
		}
	}
}

func lspIssues(servers []ttypes.LSPServerHealth) []ttypes.IssueSnapshot {
	var out []ttypes.IssueSnapshot
	for _, server := range servers {
		owner := ttypes.ResourceRef{Kind: ttypes.ResourceLSP, ID: server.ID}
		if server.Error != "" && !server.Restarting {
			out = append(out, ttypes.IssueSnapshot{
				ID: lspIssuePrefix + server.ID, Owner: owner, Severity: ttypes.SeverityError,
				Summary: server.Name + " failed", Detail: server.Error, Investigate: true,
				Actions: []ttypes.StatusAction{{ID: "lsp:restart", Label: "Retry", Enabled: true}},
			})
		}
		if message := strings.TrimSpace(server.ErrorMessage); message != "" && server.Error == "" {
			out = append(out, ttypes.IssueSnapshot{
				ID: lspMessageIssuePrefix + server.ID, Owner: owner, Severity: ttypes.SeverityWarning,
				Summary: server.Name + " reported a problem", Detail: message, Investigate: true,
			})
		}
		if lspResultsSuspect(server) {
			// The detail stays the same while the counts change, so the
			// workspace Aeron hears about it once, not on every new count.
			out = append(out, ttypes.IssueSnapshot{
				ID: lspResultsIssuePrefix + server.ID, Owner: owner, Severity: ttypes.SeverityWarning,
				Summary:     fmt.Sprintf("%s reports errors in %d of %d checked files", server.Name, server.FilesAffected, server.FilesTracked),
				Detail:      "Errors in many files often mean that the server cannot find the project's headers, modules or build settings.",
				Investigate: true,
			})
		}
	}
	return out
}

// lspResultsSuspect is the one structural rule for a server that cannot see
// the project: at least one in five of the files it checked has errors. It
// applies only to servers whose closed files Crude checks, because a server
// that reports the project itself sends results only for files with problems.
func lspResultsSuspect(server ttypes.LSPServerHealth) bool {
	return server.Error == "" && server.Scope == ttypes.CoverageOpenFiles &&
		server.FilesTracked > 0 && server.FilesAffected > 0 && server.FilesAffected*5 >= server.FilesTracked
}
