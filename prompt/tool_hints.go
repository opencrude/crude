package prompt

import (
	"fmt"
	"strings"
)

// ToolHint is appended only while composing provider input, never display data.
func ToolHint(hint string) string { return "Hint: " + hint }

const (
	HintDenied            = "Do not retry variants to bypass this decision. Use an allowed approach."
	HintUncertain         = "Inspect authoritative state before another effect. Do not replay an operation that may have committed."
	HintQuestionRequired  = "A missing answer is not approval. Continue only if it is not required; otherwise explain what information is still needed."
	HintPlanMode          = "Explore read-only and put the proposed action in the plan. Only the user can end Plan mode."
	HintForwardedQuestion = "Continue with available information unless the missing answer is required."
	HintWaitNoSnapshot    = "No fresh snapshot was available; retry only if another observation is still required."
	HintWaitDirected      = "The runtime delivers this event at its normal safe boundary; waiting does not consume it. Inspect the exact event and lifecycle state before continuing."
	HintWaitRecovery      = "Address the cause, then agent_control operation continue (or cancel) with the returned recovery_id and recovery_generation."
	HintWaitAvailability  = "Inspect or explicitly resume/reopen the exact agent before waiting again."
	HintWaitTerminal      = "Inspect the terminal outcome and unmet conditions; report publication is separate from run completion."
	HintWaitSnapshot      = "Use the returned exact snapshot to decide whether to wait again or take another action."
)

const (
	HintVerifyEditContent = "use read_file to verify the exact content before retrying. Copy full lines, including indentation and complete middle lines. Add surrounding lines if the match is ambiguous."
	HintNoChangeEdit      = "match the file's current bytes exactly: read_file the section first, and make old_string and new_string differ."
	HintDiscoverNames     = "file not found. Use glob_files or list_directory to discover actual names."
)

// HintEmptyDirectory explains a missing file whose nearest directory is empty.
func HintEmptyDirectory(dir string) string {
	return fmt.Sprintf("file not found; %s is empty.", dir)
}

func HintClosestTool(name string) string {
	return fmt.Sprintf(" The closest available tool is %q. Call it by the exact name in your tool list, including its prefix.", name)
}

func HintWriteScope(privateDir string) string {
	return fmt.Sprintf("Work within your file scope. Use your temporary directory (%s) for scratch files. Coordinate with the owning agent or ask your parent with agent_send to extend scope via agent_update_claims. Nothing changed and your run continues.", privateDir)
}

// HintNearbyEntries names the closest entries of the nearest existing
// directory of a missing file.
func HintNearbyEntries(dir string, entries []string) string {
	return fmt.Sprintf("file not found. Entries in %s (closest first): %s", dir, strings.Join(entries, ", "))
}
