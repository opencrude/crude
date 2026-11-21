package prompt

import (
	"fmt"
	"strconv"
	"strings"
)

// PlanModeContract is the root contract while the user has Plan mode on. It
// follows the established Plan mode collaboration contract: explore the source
// without mutating it, settle intent and implementation decisions, then
// present one decision-complete <proposed_plan>. The runtime enforces the
// read-only boundary independently; this text explains it.
const PlanModeContract = `<plan_mode>
<rule>You are in Plan mode until the runtime says it ended. User tone, imperative wording or a request to "just do it" does not end it: treat a request for execution as a request to plan that execution. The user ends Plan mode from Crude, not by asking you.</rule>
<rule>Work in three phases and chat your way to a great plan before finalizing it. The plan must be decision complete: another engineer or agent could implement it without making any decision.</rule>
<rule>Phase 1, ground in the environment: explore first, ask second. Resolve every unknown that reading, searching, inspection or a read-only check can answer before asking. Do at least one targeted exploration pass before asking anything, except for an obvious contradiction in the request itself.</rule>
<rule>Phase 2, intent: keep asking until you can state the goal and success criteria, audience, in and out of scope, constraints, current state, and the key preferences and tradeoffs. If a high-impact ambiguity remains, do not plan yet.</rule>
<rule>Phase 3, implementation: once intent is stable, keep asking until the approach, interfaces, data flow, edge cases and failure modes, tests and acceptance criteria, rollout and any migration are decided.</rule>
<rule>Keep the source unchanged. Read and search it with the normal inspection tools. Plain inspection commands can run on the source; builds, tests, scripts and other project commands cannot.</rule>

<rule>If inspection cannot answer a material question, use open_plan_investigation to open a separate checkout for a small experiment. Read its files by absolute path. File changes use only that checkout. Run a focused check there only with the user's permission. Commands run on the user's computer, not in a sandbox; do not use them to access or change anything outside the checkout.</rule>

<rule>Do not implement the plan as an experiment. Report what the check established, what failed, and what remains unverified. Treat denied, interrupted or unfinished checks as unverified. The experiment files are not implementation changes.</rule>
<rule>Ask with the question tool. Offer two to four meaningful, mutually exclusive options with a recommended default; never filler options. Ask only what materially changes the plan, confirms an important assumption or chooses between real tradeoffs, and never what exploration can answer. If a question goes unanswered, proceed with the recommended option and record it as an assumption in the plan.</rule>
<rule>update_plan tracks progress on work being done; it is not available in Plan mode, which produces a proposal instead.</rule>
<rule>Only present the final plan when it is decision complete. Wrap it in a proposed_plan block: the opening <proposed_plan> tag alone on its own line, Markdown content from the next line, and the closing </proposed_plan> tag alone on its own line. Keep the tag names exactly as written, in any language. At most one proposed_plan block per turn.</rule>
<rule>The plan is concise and digestible by people and agents: a clear title, a brief summary, important changes to public APIs, interfaces or types, test cases and scenarios, and explicit assumptions and defaults. Prefer three to five short sections, usually Summary, Key Changes, Test Plan and Assumptions; group changes by subsystem or behavior rather than file by file, and name files only where that prevents a mistake. Do not invent detailed policy the request does not need.</rule>
<rule>Do not ask whether to proceed: the user implements the plan from Crude or keeps refining it. A revised plan is a complete replacement. If the user rejects the plan without enough information for a replacement, address the concern and keep planning without a proposed_plan block. If a follow-up neither changes nor questions the plan, answer it, then repeat the prior proposed_plan unchanged.</rule>
</plan_mode>`

// PlanModeEndedNotice is runtime context for the first turn after the user
// turned Plan mode off.
func PlanModeEndedNotice() string {
	return "The user turned Plan mode off. You are in normal mode again: earlier Plan mode limits no longer apply, so carry out requested work with your normal tools. If the user asked to implement a proposed plan, implement it as written."
}

// PlanImplementationRequest is the user message that implements the proposed
// plan in the conversation that produced it.
const PlanImplementationRequest = "Implement the plan."

// PlanImplementationFreshContext is the first user message of a new session
// that implements a proposed plan without the planning conversation.
func PlanImplementationFreshContext(plan string) string {
	return "A previous agent produced the plan below to accomplish the user's task. Implement the plan in a fresh context. Treat the plan as the source of user intent, re-read files as needed, and carry the work through implementation and verification.\n\n" + plan
}

// PlanFeedbackComment is one review comment on a proposed plan, located by
// one-based lines of the plan the user sends with it.
type PlanFeedbackComment struct {
	FirstLine, LastLine int
	Quote, Note         string
	// Changed: the commented text was edited or removed after the comment,
	// so Quote is what the user commented on, not the current text.
	Changed bool
}

// PlanReviewFeedback is the user's message that returns their review of a
// proposed plan: comments on its text, their own edits as a unified diff, and
// the plan as they see it now. It is the user's own request, not policy; Plan
// mode stays on, so the reply is a revised proposed_plan.
func PlanReviewFeedback(revision int, plan, edits string, comments []PlanFeedbackComment) string {
	var b strings.Builder
	fmt.Fprintf(&b, "I reviewed your proposed plan (revision %d) and want changes before I approve it.", revision)
	switch {
	case len(comments) > 0 && edits != "":
		b.WriteString(" Address every comment, keep my edits unless a comment says otherwise, and propose the complete revised plan.")
	case len(comments) > 0:
		b.WriteString(" Address every comment and propose the complete revised plan.")
	default:
		b.WriteString(" I edited the plan. Keep my edits, make the rest of the plan agree with them, and propose the complete revised plan.")
	}
	fmt.Fprintf(&b, "\n\n<plan_review revision=\"%d\">\n", revision)
	if len(comments) > 0 {
		b.WriteString("<comments>\n")
		for _, comment := range comments {
			lines := strconv.Itoa(comment.FirstLine)
			if comment.LastLine > comment.FirstLine {
				lines += "-" + strconv.Itoa(comment.LastLine)
			}
			fmt.Fprintf(&b, "<comment lines=\"%s\"", lines)
			if comment.Changed {
				b.WriteString(" text_changed=\"true\"")
			}
			b.WriteString(">\n")
			if comment.Quote != "" {
				fmt.Fprintf(&b, "<quoted_text>%s</quoted_text>\n", XMLText(comment.Quote))
			}
			fmt.Fprintf(&b, "<note>%s</note>\n</comment>\n", XMLText(comment.Note))
		}
		b.WriteString("</comments>\n")
	}
	if edits != "" {
		fmt.Fprintf(&b, "<my_edits>\n%s\n</my_edits>\n", XMLText(strings.TrimRight(edits, "\n")))
	}
	fmt.Fprintf(&b, "<plan>\n%s\n</plan>\n</plan_review>", XMLText(strings.TrimRight(plan, "\n")))
	return b.String()
}

// PlanImplementationEdited is the user message that implements a proposed
// plan the user edited before approving it, in the conversation that
// produced it.
func PlanImplementationEdited(plan, edits string) string {
	var b strings.Builder
	b.WriteString("Implement the plan. I edited your proposal before approving it: the plan below is the approved text, and my edits show what I changed.\n\n")
	fmt.Fprintf(&b, "<approved_plan>\n%s\n</approved_plan>", XMLText(strings.TrimRight(plan, "\n")))
	if edits != "" {
		fmt.Fprintf(&b, "\n<my_edits>\n%s\n</my_edits>", XMLText(strings.TrimRight(edits, "\n")))
	}
	return b.String()
}
