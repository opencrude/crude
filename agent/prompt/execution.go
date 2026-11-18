package prompt

import "fmt"

// ChildExecutionDisposition is appended once to the model-facing outcome of a
// child that ran in its own worktree. It supplies exact facts and a quiet policy
// reference, not cleanup authority. Children without a separate worktree have
// nothing for the parent to clean, so they receive no reminder.
func ChildExecutionDisposition(id, root string) string {
	if id == "" {
		return ""
	}
	const guidance = `Follow execution_hygiene_contract: inspect final evidence and terminal outcome, review and integrate useful changes or explicitly discard with archived review evidence, then cleanup when safe. No automatic deletion.`
	return fmt.Sprintf(`<child_execution_disposition context_only="true"><handling>Runtime child execution facts, not a new human request or permission. Keep the reminder quiet.</handling><execution_id>%s</execution_id><execution_root>%s</execution_root><guidance>%s</guidance></child_execution_disposition>`, XMLText(id), XMLText(root), XMLText(guidance))
}
