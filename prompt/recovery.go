package prompt

import "strings"

// RecoveryKind selects canonical, code-authored continuation policy. Runtime
// errors and caller prose are data and must never become recovery instructions.
type RecoveryKind string

const (
	RecoveryTransport       RecoveryKind = "transport"
	RecoveryPartialResponse RecoveryKind = "partial_response"
	RecoveryPartialTool     RecoveryKind = "partial_tool_call"
	RecoveryOutputTruncated RecoveryKind = "output_truncated"
	RecoveryRepeatedTools   RecoveryKind = "repeated_tools"
	RecoveryMissingReport   RecoveryKind = "missing_final_report"
	RecoveryInterrupted     RecoveryKind = "interrupted"
	RecoveryUncertain       RecoveryKind = "uncertain_effects"
)

// RecoveryContinuation returns the only model-facing continuation installed by
// the recoverable-run supervisor.
func RecoveryContinuation(kind RecoveryKind) string {
	var rules []string
	switch kind {
	case RecoveryPartialResponse:
		rules = []string{
			"Accepted assistant output before the interruption is already preserved in canonical context.",
			"Continue from the exact interruption point without repeating accepted content.",
			"Do not repeat completed tool effects; inspect authoritative state before deciding that more work is required.",
		}
	case RecoveryPartialTool:
		rules = []string{
			"The provider stream ended while constructing tool arguments.",
			"No incomplete tool call from that response was executed.",
			"Re-evaluate the pending step and issue a complete call only if it is still required.",
			"Do not repeat earlier completed tool effects.",
		}
	case RecoveryOutputTruncated:
		rules = []string{
			"Accepted output before the output limit is already preserved.",
			"Continue exactly where it stopped without repeating completed content or tool effects.",
		}
	case RecoveryRepeatedTools:
		rules = []string{
			"The prior approach repeated the same tool operation and result without progress.",
			"Review the canonical results, choose a materially different next step, or report the exact blocker.",
			"Do not issue the identical effectful operation again merely to test whether it changes.",
		}
	case RecoveryMissingReport:
		rules = []string{
			"The child run is unfinished because its required final report was not published.",
			"Publish exactly one final report to the assigned parent with findings, changes, tests, risks, paths, and immediate parent actions.",
			"Do not claim success from chat text or an interim report.",
		}
	case RecoveryInterrupted:
		rules = []string{
			"This unfinished run was restored passively after interruption.",
			"Use the preserved canonical context and current owner facts; do not restart children, commands, tools, or provider work merely to rediscover state.",
			"Continue only the still-pending work and do not repeat completed effects.",
		}
	case RecoveryUncertain:
		rules = []string{
			"Execution stopped unexpectedly and the last operation's effects may be uncertain.",
			"Inspect authoritative current state before acting.",
			"Never replay an effectful operation unless evidence establishes that it did not commit; otherwise continue from the observed state or ask for intervention.",
		}
	default:
		rules = []string{
			"The provider request failed before a natural finish.",
			"Continue the unfinished request from preserved canonical context.",
			"Do not repeat accepted output or completed tool effects.",
		}
	}
	return `<runtime_recovery kind="` + XMLText(string(kind)) + `">` +
		XMLText(strings.Join(rules, " ")) + `</runtime_recovery>`
}
