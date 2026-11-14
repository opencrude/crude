package prompt

import "strings"

// AttentionNotice is the one sentence Crude sends the workspace Aeron about a
// problem it found. It carries only Crude's own summary of the issue: the
// server's own text stays in diagnostics.workspace, where it is labelled as
// evidence. WorkspaceAgentContract holds the rules for handling it.
func AttentionNotice(summary string) string {
	summary = strings.TrimRight(strings.TrimSpace(summary), ".")
	return "Crude found a problem in this workspace: " + summary + ". Investigate it."
}
