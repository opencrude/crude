package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"crude/types/insight"
	"crude/types/statuscenter"
)

// Language-server issues are derived on the core from the one LSP owner, so
// they exist without an attached shell and no shell writes them. Each issue
// that a person or the workspace Errand can act on is marked Investigate. A
// server that Crude is restarting by itself is not an issue yet: the restart
// budget decides when it needs help.
const (
	lspIssuePrefix        = "lsp:"
	lspMessageIssuePrefix = "lsp-message:"
	lspResultsIssuePrefix = "lsp-results:"
	lspCommandIssuePrefix = "lsp-command:"
)

// SyncLSPIssues publishes the issues that the language servers' state shows
// and resolves the ones that no longer apply.
func SyncLSPIssues(center *StatusCenter, manager *LSPManager) {
	if center == nil || manager == nil {
		return
	}
	syncLSPIssuesFrom(center, append(manager.ServerHealthSnapshots(), manager.retainedResultHealth()...), manager.Assembly())
}

func syncLSPIssuesFrom(center *StatusCenter, servers []statuscenter.LSPServerHealth, plan insight.AssemblyPlan) {
	open := make(map[string]bool)
	for _, issue := range append(lspIssues(servers), lspCommandIssues(plan)...) {
		open[issue.ID] = true
		center.SetIssue(issue)
	}
	for _, issue := range center.Issues() {
		if issue.Owner.Kind == statuscenter.ResourceLSP && issue.ResolvedAt.IsZero() && !open[issue.ID] {
			center.ResolveIssue(issue.ID)
		}
	}
}

func lspIssues(servers []statuscenter.LSPServerHealth) []statuscenter.IssueSnapshot {
	var out []statuscenter.IssueSnapshot
	for _, server := range servers {
		owner := statuscenter.ResourceRef{Kind: statuscenter.ResourceLSP, ID: server.ID}
		if server.Error != "" && !server.Restarting {
			out = append(out, statuscenter.IssueSnapshot{
				ID: lspIssuePrefix + server.ID, Owner: owner, Severity: statuscenter.SeverityError,
				Summary: server.Name + " failed", Detail: server.Error, Investigate: true,
				Actions: []statuscenter.Action{{ID: "lsp:restart", Label: "Retry", Enabled: true}},
			})
		}
		if message := strings.TrimSpace(server.ErrorMessage); message != "" && server.Error == "" {
			out = append(out, statuscenter.IssueSnapshot{
				ID: lspMessageIssuePrefix + server.ID, Owner: owner, Severity: statuscenter.SeverityWarning,
				Summary: server.Name + " reported a problem", Detail: message, Investigate: true,
			})
		}
		if lspResultsSuspect(server) {
			// Summary and detail do not carry the counts. The counts change
			// while Crude checks files; the issue must stay the same report so
			// that it settles and the workspace Errand hears about it once.
			// diagnostics.workspace shows the current counts.
			out = append(out, statuscenter.IssueSnapshot{
				ID: lspResultsIssuePrefix + server.ID, Owner: owner, Severity: statuscenter.SeverityWarning,
				Summary:     server.Name + " reports errors in many of the files it checked",
				Detail:      "Errors in at least one in five checked files often mean that the server cannot find the project's headers, modules or build settings.",
				Investigate: true,
			})
		}
	}
	return out
}

// lspResultsSuspect is the one structural rule for a server that cannot see
// the project: errors in at least one in five of the files it checked. The
// project's only C header with a missing include is one file of one, so one
// file counts. The workspace Errand tells a misconfiguration from errors in
// the user's code and asks only about the first. The rule applies only to
// servers whose closed files Crude checks, because a server that reports the
// project itself sends results only for files with problems.
func lspResultsSuspect(server statuscenter.LSPServerHealth) bool {
	return server.Error == "" && server.Scope == insight.CoverageOpenFiles &&
		server.FilesAffected > 0 && server.FilesAffected*5 >= server.FilesTracked
}

// lspCommandIssues reports language servers that the user's settings name but
// that are not installed: a typing error in a command or a tool that was
// removed. A catalog server that is not installed is a setup choice, not a
// problem, so it raises nothing here. An installed but unlaunchable catalog
// tool is a problem and keeps its actionable launch cause.
func lspCommandIssues(plan insight.AssemblyPlan) []statuscenter.IssueSnapshot {
	var out []statuscenter.IssueSnapshot
	for _, language := range plan.Languages {
		if language.Status != insight.ToolUnavailable &&
			(language.Status != insight.ToolMissing || language.Source != insight.ToolSourceSettings) {
			continue
		}
		summary := fmt.Sprintf("%s server %s is not installed", language.Name, filepath.Base(language.Command))
		if language.Status == insight.ToolUnavailable {
			summary = fmt.Sprintf("%s server %s cannot start", language.Name, filepath.Base(language.Command))
		}
		out = append(out, statuscenter.IssueSnapshot{
			ID: lspCommandIssuePrefix + language.ID, Owner: statuscenter.ResourceRef{Kind: statuscenter.ResourceLSP, ID: lspCommandIssuePrefix + language.ID},
			Severity: statuscenter.SeverityWarning, Investigate: true,
			Summary: summary,
			Detail:  language.Reason,
		})
	}
	return out
}
