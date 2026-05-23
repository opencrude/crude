package agent

import (
	"fmt"
	"strings"
)

const SubAgentPrompt = `# Sub-Agent Prompt

You are a Crude sub-agent. You are a leaf worker launched by the main agent for one self-contained assignment. You do not talk directly to the user, you do not know the main conversation history, and you cannot delegate further. Your job is to complete the assigned task, write a detailed report, and return a compact status to the main agent.

# Isolation Contract

- Use only the task description, profile instructions, policy constraints, available tools, and environment shown in this prompt.
- Do not assume facts from the main conversation. If needed context is missing, ask through reply or question if those tools are available.
- Stay inside your assignment. Do not broaden scope just because you discover adjacent issues.
- Respect your tool and policy limits. If a tool is unavailable or blocked, choose another allowed approach or report the blocker.
- Never spawn sub-agents. Delegation is reserved for the main agent only.

# Workflow

1. Restate the assignment internally as concrete deliverables.
2. Ground yourself with read_file, grep_files, glob_files, or list_directory before editing or concluding.
3. Read files before editing them.
4. Make only the changes required by the assignment and your category.
5. Verify your work when your tools allow it.
6. Call write_report with the full report before finishing.
7. After write_report succeeds, stop tool use and respond with a brief status plus the report path.

# File And Command Rules

- Use edit_file for existing files. Use write_file only for new files or intentional full rewrites.
- Copy exact full-line context when editing. Re-read after failed edits.
- Use relative paths from the working directory.
- Do not delete files unless explicitly allowed by both your task and your tools.
- Do not run destructive commands. Do not retry commands rejected by safety policy.
- Use run_javascript for arithmetic or data transforms.

# Reporting Requirements

Your write_report content must be detailed enough that the main agent can make decisions without your context. Include:

- Summary: final status and direct answer to the assignment.
- Evidence: important files, symbols, commands, outputs, and file:line references.
- Work performed: edits made, files changed, and reasoning for non-obvious choices.
- Verification: checks run and their exact result, or why checks were not run.
- Risks: unresolved issues, assumptions, conflicts, or follow-up work.

Do not rely on the final chat message for important details. The report is the durable handoff.`

func BuildSubAgentPrompt(spec SubAgentSpec, profileInstructions string) string {
	var b strings.Builder
	b.WriteString(SubAgentPrompt)
	b.WriteString("\n\n")
	b.WriteString(buildSubAgentAssignment(spec))

	if category := subAgentCategoryBriefing(spec.Category); category != "" {
		b.WriteString("\n\n# Category Briefing\n")
		b.WriteString(category)
	}

	if policy := buildSubAgentPolicySection(spec); policy != "" {
		b.WriteString("\n\n")
		b.WriteString(policy)
	}

	if len(spec.FileOwnership) > 0 {
		b.WriteString("\n\n# File Ownership\n")
		b.WriteString("You have pre-declared write ownership of these paths: ")
		b.WriteString(strings.Join(spec.FileOwnership, ", "))
		b.WriteString(". Do not write outside this ownership when parallel work could conflict.")
	}

	if profileInstructions != "" {
		b.WriteString("\n\n# Profile Instructions\n")
		b.WriteString(strings.TrimSpace(profileInstructions))
	}

	if spec.Instructions != "" {
		b.WriteString("\n\n# Assignment Instructions\n")
		b.WriteString(strings.TrimSpace(spec.Instructions))
	}

	b.WriteString("\n\n")
	b.WriteString(gatherEnvContext().buildSection())
	return b.String()
}

func buildSubAgentAssignment(spec SubAgentSpec) string {
	var b strings.Builder
	b.WriteString("# Assignment\n")
	b.WriteString(fmt.Sprintf("- Name: %s\n", emptyDefault(spec.Name, "unnamed")))
	b.WriteString(fmt.Sprintf("- Category: %s\n", emptyDefault(spec.Category, "sub-agent")))
	b.WriteString(fmt.Sprintf("- Description: %s", emptyDefault(spec.Description, "No description provided.")))
	if spec.ParentID != "" {
		b.WriteString(fmt.Sprintf("\n- Parent: %s", spec.ParentID))
	}
	if spec.ID != "" {
		b.WriteString(fmt.Sprintf("\n- Agent ID: %s", spec.ID))
	}
	return b.String()
}

func buildSubAgentPolicySection(spec SubAgentSpec) string {
	var lines []string
	if spec.PolicyName != "" {
		lines = append(lines, fmt.Sprintf("- Permission policy: %s", spec.PolicyName))
	}
	if len(spec.AllowedTools) > 0 {
		lines = append(lines, fmt.Sprintf("- Allowed tools: %s", strings.Join(spec.AllowedTools, ", ")))
	}
	if len(spec.DeniedTools) > 0 {
		lines = append(lines, fmt.Sprintf("- Denied tools: %s", strings.Join(spec.DeniedTools, ", ")))
	}
	if len(lines) == 0 {
		return ""
	}
	return "# Policy Constraints\n" + strings.Join(lines, "\n")
}

func emptyDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}

func subAgentCategoryBriefing(category string) string {
	switch category {
	case CatResearcher:
		return "You are in the researcher tier. Prioritize search, reading, and analysis. Do not modify files. Return evidence with file:line references. Use read-only tools and safe non-destructive commands only."
	case CatCrafter:
		return "You are in the crafter tier. You may make targeted edits and run verification. Prefer the smallest correct implementation. Do not delete files. Report every file changed and every check run."
	case CatAeron:
		return "You are in the aeron tier. Keep the task narrow, cheap, and mechanical. Do not over-engineer. You may not execute arbitrary commands unless explicitly allowed by your available tools."
	default:
		return "Follow the assignment exactly, respect your available tools, and keep the handoff report complete."
	}
}
