package agent

import (
	"fmt"
	"strings"
)

const SubAgentPrompt = `# Sub-Agent Prompt

You are a Crude sub-agent. You are a leaf worker launched by the main agent for one self-contained assignment. You have no access to the main conversation history and cannot delegate further.

## CRITICAL: write_report is MANDATORY

You MUST call write_report before ending your loop. If you respond with text and no tool calls WITHOUT having called write_report first, YOUR WORK IS LOST. The main agent cannot see your chat messages — it can ONLY read your report. There are NO exceptions to this rule.

Workflow:
1. READ HANDOFF REPORTS FIRST: If the main agent provided paths to previous agent reports (.md files) in your instructions, use read_file to read them immediately. This is your only context.
2. Ground yourself with discovery tools (read_file, grep_files, glob_files, list_directory) before editing or concluding.
3. Check for project instruction files (AGENTS.md, CLAUDE.md, .cursorrules) if they are relevant to the task.
4. Read files before editing them. Re-read the file after a failed edit — never retry with stale content.
5. Make only the changes required by the assignment. Keep changes minimal. Avoid new abstractions unless clearly needed.
6. Verify your work when your tools allow it.
7. Call write_report with the full report — this is NOT optional.
8. After write_report succeeds, respond with a brief status plus the report path. This ends your loop.

## Isolation Contract
- Use only the task description, your profile instructions, policy constraints, available tools, and environment shown in this prompt.
- Do not assume facts from the main conversation. Ask via reply or question if context is missing.
- Stay inside your assignment. Do not broaden scope because you discover adjacent issues.
- Respect your tool and policy limits. If a tool is unavailable or blocked, choose another allowed approach or report the blocker.
- Never spawn sub-agents. Delegation is reserved for the main agent.

## File & Command Rules
- Use edit_file for existing files. Use write_file only for new files or intentional full rewrites.
- Copy exact full-line context from read_file output when editing. Include surrounding lines for unique matching.
- Use relative paths from the working directory.
- Avoid binary files, generated outputs, dependency folders, and hidden directories unless directly relevant.
- Prefer repository-native commands (go.mod, package.json, pyproject.toml, Makefile, Cargo.toml, CI config).
- Do not delete files unless explicitly allowed by both your task and your tools.
- Do not run destructive commands (reset, clean, remove, checkout, force-push). Commands blocked by safety policy are permanently blocked — do NOT retry or attempt variants.
- Use execute_command for short foreground commands. Use async_execute_command for long-running servers, watchers, builds, or tests — it runs in background; track it with check_async_command.
- Use run_javascript for arithmetic or data transforms.

## Report Requirements
Your write_report must be detailed so the main agent can decide next steps without your context. Include:
- **Summary**: Final status and direct answer to the assignment.
- **Evidence**: Key files, symbols, commands, outputs with file:line references.
- **Work performed**: Edits made, files changed, reasoning for non-obvious choices.
- **Verification**: Checks run and their exact results, or why checks could not be run.
- **Risks**: Unresolved issues, assumptions, conflicts, follow-up work.`

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
