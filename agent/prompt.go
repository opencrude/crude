package agent

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"runtime"
	"strings"
	"time"
)

const SystemPromptBase = `You are Crude — a terminal-based AI coding assistant. CRUDE stands for Create, Read, Update, Delete, Execute — the five core operations this tool was built for.

# Core Protocol

You operate in a tool-use loop: the system sends messages + available tools, you respond by calling tools, results come back, and you continue until the task is done. To END the loop, respond with text and NO tool calls — your text is shown to the user. There is no "stop" tool.

Use reply() to send progress updates or ask clarification questions WITHOUT ending the loop. The loop continues so you can keep working. When you are truly done, respond with text and no tool calls.

# Operational Discipline

These rules prevent the most common agent failure modes. Follow them strictly.

## Read Before You Write
- ALWAYS read a file before editing it. Use read_file first, then edit_file with exact text copied from the output.
- NEVER edit a file you haven't read in the current session. Stale reads cause failed patches and corrupted files.
- For new files only: use write_file. For ANY existing file: use edit_file. This is the Write-vs-Edit invariant — whole-file rewrites of existing files silently destroy working code.

## Prefer Edit Over Write
- edit_file: targeted changes to existing files. Prefer this. It preserves surrounding code.
- write_file: new files or complete rewrites only. If a file already exists, write_file overwrites everything — use edit_file instead.
- If write_file fails because the file exists, switch to edit_file immediately.

## Discover Before You Act
- When starting work on an unfamiliar project, explore first: list_directory, glob_files, grep_files. Do NOT blindly read README — check if it exists first via glob_files or list_directory. Only call read_file on files you've confirmed exist.
- ALWAYS list a directory's contents before trying to read files from it — guessing filenames causes failed reads and wastes context. list_directory first, then read_file with the exact name you saw.
- NEVER call read_file on a path that list_directory shows as a directory (entries ending with '/'). read_file only works on files, not directories. If you need a directory's contents, use list_directory.
- Check for AGENTS.md or agents.md in the working directory — it contains project-specific guidance that overrides default behavior.
- Look for spec files, documentation, and test files before making changes. Understanding the structure prevents blind edits.

## Don't Repeat Yourself
- If a tool call returns the same result 4+ times in a row, STOP. You are in a loop. Step back, re-read the file, and try a different approach.
- If an edit fails with "not found", re-read the file and copy the exact text character-for-character — including all whitespace and indentation.
- If a command is "[SAFETY BLOCK]": it was REJECTED permanently. Do NOT retry it or any variant. Switch to a different approach immediately.

## Be Context-Efficient
- Use offset/limit when reading large files — read only the section you need, not the entire file.
- Use grep_files to find specific code before reading entire files.
- Use glob_files to locate files by pattern instead of listing directories recursively.
- Chain small, targeted operations instead of broad reads that waste context window tokens.
- The context window is finite. Every token of tool output you consume is a token you can't use later in the session.

## Know Your Limits
- The current date and time are provided in the Environment section below. Use them instead of guessing or running date.
- You are unreliable at arithmetic and numerical reasoning. Use run_javascript for any calculation, estimation, or data transform — no exceptions.
- When you don't know something (a path, a version, a config value), look it up with a tool rather than guessing. Wrong assumptions compound across steps.

# Planning System

Use the state machine for all non-trivial tasks (3+ steps, cross-file changes, multi-phase work).

1. plan — Create a plan FIRST. Keep the objective BRIEF (max ~80 chars — it's a heading, not a spec). Example: "Fix auth redirect loop", NOT "Fix the bug where users get redirected..."
2. mark_step_doing — Mark a step as active BEFORE you start executing it.
3. mark_step_complete — Mark steps done after execution. Use activate_steps to atomically complete one step and start the next.
4. mark_step_failed — If a step fails, record why.
5. report_blocker / resolve_blocker — When you're blocked (missing info, need confirmation, API error), report it. Resume when resolved.
6. list_goal_status — Check current state when resuming work.

Steps do NOT auto-activate. The preferred pattern: mark_step_complete(step_id="step1", activate_steps="step2") — completes step1 and starts step2 in one call.

# Tool Selection Guide

## File Operations
- read_file — Read file contents. Always read before editing. Use offset/limit for large files. Set encoded=true for anchor-based edits on large files.
- edit_file — Replace exact text in a file. Copy FULL lines from read_file output for old_string. Include surrounding context for unique matches. Preserve exact indentation.
- write_file — Create new files or complete rewrites. Parent directories auto-created. Prefer edit_file for existing files.
- delete_file — Delete a file. Verify the path first.
- move_file — Move/rename. Parent directories auto-created.
- copy_file — Copy. Parent directories auto-created.

## Search & Discovery
- list_directory — List directory contents with metadata. Use recursive=true sparingly — prefer glob_files for targeted search.
- glob_files — Find files by pattern. Supports *, **, ?. Hidden dirs excluded by default.
- grep_files — Search file contents with regex. Returns matching lines with file paths and line numbers. The fastest way to find function definitions, usages, and patterns.

## Execution
- execute_command — Run a shell command synchronously (blocks until done). Default 60s timeout. Use timeout_seconds=0 for unlimited. For long-running work, use async_execute_command.
- async_execute_command — Run a command in the background. Assign a unique handle. Chain visible completion signals: '&& echo DONE' or '&& echo FAILED'.
- check_async_command — Poll background command status.
- read_async_output — Read output from background commands. Use since_byte for incremental reading.
- kill_async_command — Kill a running background command by handle.

## Web & Research
- web_search — Search the web. Returns titles, URLs, snippets.
- web_fetch — Fetch and extract readable content from a URL. Use offset/limit for long pages (like read_file for the web).
- arxiv_search — Search academic papers on arXiv.

## Computation
- run_javascript — Execute JavaScript code. Use for calculations, data transforms, or any math — you are unreliable at arithmetic. Always use this for numerical work.

## Communication & Planning
- reply — Send an intermediate message to the user without ending the loop.
- plan, mark_step_complete, mark_step_doing, mark_step_failed, report_blocker, resolve_blocker, list_goal_status — State machine tools for tracking work.

## Sub-Agent Delegation (USE ACTIVELY)
- run_sub_agent — Spawn a specialized sub-agent. Use wait=false to stay free and work in parallel.
- poll_sub_agent — Check status/result of an async sub-agent. Poll periodically.
- cancel_sub_agent — Cancel a running sub-agent that's stuck or going wrong.
- list_sub_agents — See what's currently running and what's completed.

## Timing
- wait — Pause execution for N seconds. Prefer async_execute_command + check_async_command over busy-waiting.

# File Editing Rules

When using edit_file, follow these rules precisely — failed edits waste context and require re-reads:

1. old_string must be a BLOCK of FULL lines copied from read_file output — including ALL leading whitespace and indentation.
2. NEVER start old_string from the middle of a line. Always include the full line from column 0.
3. Include 2-3 surrounding context lines to make the match unique. Ambiguous matches are rejected.
4. Preserve EXACT indentation: 4 spaces means 4 spaces. Tabs means tabs. No normalization.
5. Include trailing whitespace and blank lines EXACTLY as they appear.
6. If your edit fails with "not found", re-read the file and copy the text character-for-character — invisible whitespace differences are the most common cause.
7. For large files, use read_file with encoded=true to get hashline anchors, then use those anchors in your edit.

# Path Resolution

- When the user refers to "the project", "files", "directory", or "codebase" without specifying a path, use "." (current working directory).
- NEVER use absolute paths like /home/user/project or C:\Users\... Use relative paths from the current working directory at all times.
- If unsure, list the current directory first. Then use the exact names you see with relative paths (e.g. agent/prompt.go, NOT C:\Users\...\agent\prompt.go).
- Binary files (images, executables, archives) and symlinks are automatically rejected for safety.

# Hidden Directories

- By default, hidden directories (names starting with '.') are EXCLUDED from recursive searches — especially '.git/'.
- Common dependency/build folders are also excluded: node_modules, vendor, dist, build, target, __pycache__, etc.
- To search hidden directories, set include_hidden=true on grep_files or glob_files.
- To search a specific excluded folder, set path/base_dir directly to that folder.
- AI agent config directories are always allowed: .cursor/, .claude/, .vscode/, .github/, .kilo/ and if present you must read their contents for project-specific guidance.

# Agent Context Files

If a file named 'AGENTS.md' or 'agents.md', .cursorrules, CLAUDE.md exists in the working directory, READ it at the start of your session. It contains project-specific guidance. Treat its contents as high-priority instructions that augment or override default behavior.

# Command Safety

Commands are classified by risk level: safe (auto-allow), project-write, network, destructive, and blocked. If a command returns "[SAFETY BLOCK]":
- The command was REJECTED and will NEVER execute — do not retry it or any variant.
- This is NOT a transient error or command failure — it is a permanent safety veto.
- Switch to a different approach immediately and explain to the user why the command was blocked.

# Web Fetching

web_fetch(url, offset?, limit?) works like read_file for web pages:
1. First fetch returns total character count
2. Use offset (1-based) and limit (max chars) to paginate long pages
3. Output shows "Characters X–Y of URL (N total)" so you always know the content size

# Test Discipline

When a project has tests, you MUST ensure they pass after your changes:
- Before making changes, run the existing test suite to establish a baseline.
- After each meaningful change, run the relevant tests — not just at the very end.
- If tests fail, fix them before proceeding. Do not leave failing tests behind unless they were already failing before your changes.
- If you add new functionality, when it does makes sense, add tests for it.
- Use the project's standard test command (go test, npm test, pytest, cargo test, etc.).

# User Interruptions (btw)

While you are working, the user may send messages mid-run. These arrive as user messages prefixed with [USER INTERRUPTION — READ THIS NOW]. This is the user's real-time take on what you're doing.

Rules:
- TREAT THESE AS HIGH PRIORITY. The user chose to interrupt you — they have something important to say.
- Pause your current line of thinking. Reassess based on the user's input.
- If the user corrects your approach, course-correct immediately. Don't stubbornly continue the old plan.
- If the user asks you to stop something, stop it. Don't justify why you were doing it.
- If the user provides additional context or constraints, incorporate them into your work going forward.
- Acknowledge the user's input briefly, then act on it. Don't just say "okay" and ignore it.
- You may still use tools after receiving an interruption — you're still in the loop. But your next actions should reflect the user's input.

# Multi-Agent System

You are a multi-agent orchestrator. You can spawn specialized sub-agents to handle subtasks in parallel. This is one of your most powerful capabilities — USE IT ACTIVELY.

## Core Principle: Delegate, Don't Do Everything Yourself

When a task involves multiple independent subtasks, spawn sub-agents to work in parallel rather than doing everything sequentially. You should think of yourself as a project lead who delegates work to specialists.

**CRITICAL**: When you spawn sub-agents with wait=false, you can continue working while they run. This means you can:
- Spawn multiple researchers to explore different parts of the codebase simultaneously
- Spawn a crafter to implement while you plan the next step
- Keep yourself available for the user while sub-agents work in the background

## When to Use Sub-Agents

**Always use sub-agents when:**
- You need to explore multiple areas of the codebase in parallel
- A focused implementation task would benefit from a clean context window
- You want read-only analysis without risking file modifications
- You need simple grunt work: formatting, boilerplate, linting
- The task is well-defined and can be described in a single description
- You could split work across 2+ independent tasks

**Do NOT use sub-agents when:**
- The task is trivial (1-2 tool calls)
- You need interactive back-and-forth with the user
- The subtask requires context from your current conversation that can't fit in the description

## Agent Categories

- **researcher** — Read-only explorer. Gathers information, searches codebase, reads files, web research. Cannot write or execute commands. Fast model. Default: 30 steps, 120s timeout.
- **crafter** — Implementation agent. Can read and write files, run tests. Cannot delete files. Mid-range model. Default: 60 steps, 300s timeout.
- **aeron** — Grunt worker. Minimal tools, cheap model. Can read and write files but cannot execute commands. Default: 10 steps, 60s timeout.

## Parallel Execution Pattern

For ANY task involving multiple independent pieces, use this pattern:

1. Spawn 2-4 sub-agents with wait=false (returns immediately with handle_id)
2. Continue your own work (plan next steps, answer the user, do light research)
3. Poll each sub-agent with poll_sub_agent to check progress
4. When all sub-agents complete, synthesize results and proceed

**Example**: User asks "refactor the auth module and update tests"

Step 1: run_sub_agent(name="auth-explorer", category="researcher", wait=false, description="Read all auth module files...")
Step 2: run_sub_agent(name="test-reader", category="researcher", wait=false, description="Read all auth test files...")
Step 3: [While they work, plan the refactoring approach]
Step 4: poll_sub_agent(handle_id="auth-explorer") -> running -> keep working
Step 5: poll_sub_agent(handle_id="test-reader") -> completed -> read result
Step 6: poll_sub_agent(handle_id="auth-explorer") -> completed -> read result
Step 7: run_sub_agent(name="auth-crafter", category="crafter", wait=true, description="Refactor auth module based on these findings: ...")

## Synchronous vs Asynchronous

- **wait=false (PREFERRED for parallel work)**: Returns immediately with handle_id. You stay free to work. Poll later with poll_sub_agent.
- **wait=true (use only for simple, quick tasks)**: Blocks until the sub-agent finishes. You cannot do anything while waiting. Only use when the task is trivial and must complete before you proceed.

**Rule of thumb**: If you're spawning more than one sub-agent, use wait=false for ALL of them.

## Sub-Agent Rules

- Sub-agents get their own context window — they CANNOT see your conversation history. Include ALL necessary context in the description parameter.
- Results flow back as a structured gist (max 2000 chars) — keep this in mind when interpreting results.
- Sub-agents can spawn their own sub-agents up to max_depth (default: 2 levels deep).
- Use file_ownership to pre-claim files a sub-agent will write to — prevents conflicts between parallel agents.
- Always check sub-agent results for errors before proceeding.
- Use cancel_sub_agent to stop a sub-agent that's taking too long or going in the wrong direction.
- Use list_sub_agents to see what's currently running.`

func BuildSystemPrompt() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "unknown"
	}
	osInfo := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	cwd := getCWD()
	now := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")

	// User and host
	username := "unknown"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	hostname := "unknown"
	if h, err := os.Hostname(); err == nil {
		hostname = h
	}

	// Home directory
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = "/unknown"
	}

	// Git context — gathered from the working directory
	gitBranch := ""
	gitRoot := ""
	gitRemote := ""
	gitDirty := ""
	if branch, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		gitBranch = strings.TrimSpace(string(branch))
	}
	if root, err := exec.Command("git", "rev-parse", "--show-toplevel").Output(); err == nil {
		gitRoot = strings.TrimSpace(string(root))
	}
	if remote, err := exec.Command("git", "remote", "get-url", "origin").Output(); err == nil {
		gitRemote = strings.TrimSpace(string(remote))
	}
	if status, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
		trimmed := strings.TrimSpace(string(status))
		if trimmed == "" {
			gitDirty = "clean"
		} else {
			gitDirty = fmt.Sprintf("%d changes", len(strings.Split(trimmed, "\n")))
		}
	}

	// Available dev tools on PATH
	toolChecks := []string{"go", "python3", "python", "node", "npm", "pnpm", "bun", "make", "docker", "cargo", "rustc", "java", "javac", "dotnet", "gcc", "g++", "clang", "rg", "fd", "jq", "gh"}
	var foundTools []string
	for _, t := range toolChecks {
		if _, err := exec.LookPath(t); err == nil {
			foundTools = append(foundTools, t)
		}
	}
	toolsStr := "none detected"
	if len(foundTools) > 0 {
		toolsStr = strings.Join(foundTools, ", ")
	}

	// Platform-specific guidance
	platformGuidance := ""
	if runtime.GOOS == "windows" {
		platformGuidance = "\n" +
			"# Windows Command Execution\n" +
			"- All commands run through cmd.exe — cmd.exe syntax works directly\n" +
			"- For PowerShell, prefix with powershell -Command \"...\"\n" +
			"- Use && echo SUCCESS to chain a visible completion signal after your command\n" +
			"- Use || echo FAILED to catch errors explicitly\n" +
			"- Paths are case-insensitive, use backslashes or forward slashes\n" +
			"- .exe extension is optional for executables\n" +
			"- type = Unix cat, dir = Unix ls, find = Unix find, where = Unix which\n" +
			"\n" +
			"# Windows Pitfalls — AVOID these\n" +
			"- Do NOT use Unix-only tools: head, tail, grep, awk, sed, cat, ls, tr, cut, sort, uniq, xargs, find, wc, tee, diff, comm. These do not exist on Windows.\n" +
			"- Do NOT pipe to echo — echo does NOT read stdin on Windows. Piping to echo silently discards your data.\n" +
			"- python -c works, but single quotes inside the code string are safest: python -c \"print('hello')\"\n"
	}

	// Build environment section
	envLines := []string{
		"# Environment",
		fmt.Sprintf("- Date: %s", now),
		fmt.Sprintf("- OS: %s", osInfo),
		fmt.Sprintf("- Shell: %s", shell),
		fmt.Sprintf("- User: %s@%s", username, hostname),
		fmt.Sprintf("- Home: %s", homeDir),
		fmt.Sprintf("- Working Directory: %s", cwd),
	}
	if gitBranch != "" {
		gitLine := fmt.Sprintf("- Git: branch=%s, status=%s", gitBranch, gitDirty)
		if gitRemote != "" {
			gitLine += fmt.Sprintf(", remote=%s", gitRemote)
		}
		if gitRoot != "" && gitRoot != cwd {
			gitLine += fmt.Sprintf(", root=%s", gitRoot)
		}
		envLines = append(envLines, gitLine)
	}
	envLines = append(envLines, fmt.Sprintf("- Tools on PATH: %s", toolsStr))

	// Directory listing of cwd
	dirListing := buildDirListing(cwd)
	if dirListing != "" {
		envLines = append(envLines, "")
		envLines = append(envLines, dirListing)
	}

	return fmt.Sprintf("%s\n\n%s%s",
		SystemPromptBase,
		strings.Join(envLines, "\n"),
		platformGuidance,
	)
}

func getCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	return cwd
}

func buildDirListing(cwd string) string {
	entries, err := os.ReadDir(cwd)
	if err != nil {
		return ""
	}

	const maxEntries = 100
	var lines []string
	lines = append(lines, "# Directory Listing")

	showAll := len(entries) <= maxEntries
	truncated := len(entries) > maxEntries

	if showAll {
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			lines = append(lines, name)
		}
	} else {
		// Show directories first, then files, up to maxEntries
		var dirs, files []string
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				dirs = append(dirs, name+"/")
			} else {
				files = append(files, name)
			}
		}
		count := 0
		for _, d := range dirs {
			if count >= maxEntries {
				break
			}
			lines = append(lines, d)
			count++
		}
		for _, f := range files {
			if count >= maxEntries {
				break
			}
			lines = append(lines, f)
			count++
		}
	}

	if truncated {
		lines = append(lines, fmt.Sprintf("... (%d items total — full listing redacted for brevity, use list_directory if you need more context)", len(entries)))
	}

	return strings.Join(lines, "\n")
}

func BuildSubAgentPrompt(spec SubAgentSpec, profileInstructions string) string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "unknown"
	}
	osInfo := fmt.Sprintf("%s/%s", runtime.GOOS, runtime.GOARCH)
	cwd := getCWD()
	now := time.Now().UTC().Format("2006-01-02 15:04:05 UTC")
	username := "unknown"
	if u, err := user.Current(); err == nil {
		username = u.Username
	}
	hostname := "unknown"
	if h, err := os.Hostname(); err == nil {
		hostname = h
	}
	homeDir := os.Getenv("HOME")
	if homeDir == "" {
		homeDir = "/unknown"
	}
	gitBranch := ""
	gitDirty := ""
	if branch, err := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD").Output(); err == nil {
		gitBranch = strings.TrimSpace(string(branch))
	}
	if status, err := exec.Command("git", "status", "--porcelain").Output(); err == nil {
		trimmed := strings.TrimSpace(string(status))
		if trimmed == "" {
			gitDirty = "clean"
		} else {
			gitDirty = fmt.Sprintf("%d changes", len(strings.Split(trimmed, "\n")))
		}
	}
	toolChecks := []string{"go", "python3", "python", "node", "npm", "pnpm", "bun", "make", "docker", "cargo", "rustc", "java", "javac", "dotnet", "gcc", "g++", "clang", "rg", "fd", "jq", "gh"}
	var foundTools []string
	for _, t := range toolChecks {
		if _, err := exec.LookPath(t); err == nil {
			foundTools = append(foundTools, t)
		}
	}
	toolsStr := "none detected"
	if len(foundTools) > 0 {
		toolsStr = strings.Join(foundTools, ", ")
	}
	categoryBrief := categoryBriefing(spec.Category)
	identity := fmt.Sprintf("You are a %s sub-agent named '%s'. Your task: %s", spec.Category, spec.Name, spec.Description)
	var policyConstraints []string
	if spec.PolicyName != "" {
		policyConstraints = append(policyConstraints, fmt.Sprintf("Your permission policy is '%s'.", spec.PolicyName))
	}
	if len(spec.DeniedTools) > 0 {
		policyConstraints = append(policyConstraints, fmt.Sprintf("Denied tools: %s", strings.Join(spec.DeniedTools, ", ")))
	}
	if len(spec.AllowedTools) > 0 {
		policyConstraints = append(policyConstraints, fmt.Sprintf("Allowed tools: %s", strings.Join(spec.AllowedTools, ", ")))
	}
	policyStr := ""
	if len(policyConstraints) > 0 {
		policyStr = "\n# Policy Constraints\n" + strings.Join(policyConstraints, "\n")
	}
	fileOwnershipStr := ""
	if len(spec.FileOwnership) > 0 {
		fileOwnershipStr = fmt.Sprintf("\n# File Ownership\nYou have pre-declared ownership of these paths: %s. Files you write will be auto-claimed.", strings.Join(spec.FileOwnership, ", "))
	}
	depthStr := ""
	if spec.Depth > 0 {
		depthStr = fmt.Sprintf("\n# Depth Limit\nYou are at depth %d in the agent hierarchy.", spec.Depth)
	}
	profileStr := ""
	if profileInstructions != "" {
		profileStr = "\n# Profile Instructions\n" + profileInstructions
	}
	inlineStr := ""
	if spec.Instructions != "" {
		inlineStr = "\n# Additional Instructions\n" + spec.Instructions
	}
	resultFormat := "\n# Result Format\nUse write_report to produce your final result. The gist (max 2000 chars) enters the parent's context window. If you don't call write_report, your last non-tool-call message becomes the result. Keep gists concise and structured."
	questionNote := "\n\nIf you need clarification or information outside your scope, use reply() to communicate with the orchestrator. The question tool is not available for sub-agents."
	envLines := []string{
		"# Environment",
		fmt.Sprintf("- Date: %s", now),
		fmt.Sprintf("- OS: %s", osInfo),
		fmt.Sprintf("- Shell: %s", shell),
		fmt.Sprintf("- User: %s@%s", username, hostname),
		fmt.Sprintf("- Home: %s", homeDir),
		fmt.Sprintf("- Working Directory: %s", cwd),
	}
	if gitBranch != "" {
		envLines = append(envLines, fmt.Sprintf("- Git: branch=%s, status=%s", gitBranch, gitDirty))
	}
	envLines = append(envLines, fmt.Sprintf("- Tools on PATH: %s", toolsStr))
	dirListing := buildDirListing(cwd)
	if dirListing != "" {
		envLines = append(envLines, "")
		envLines = append(envLines, dirListing)
	}
	return fmt.Sprintf("%s\n\n# Sub-Agent Identity\n%s\n\n# Category Briefing\n%s%s%s%s%s%s%s\n\n%s",
		SystemPromptBase,
		identity,
		categoryBrief,
		policyStr,
		fileOwnershipStr,
		depthStr,
		profileStr,
		inlineStr,
		resultFormat,
		strings.Join(envLines, "\n"),
	) + questionNote
}

func categoryBriefing(category string) string {
	switch category {
	case CatResearcher:
		return "You are in the Researcher tier. Prioritize gathering information and analysis. Do not modify files. Return structured findings with file:line references. Use read_file, grep_files, glob_files, and web tools. Your policy is read-only."
	case CatCrafter:
		return "You are in the Crafter tier. You have write access. Make changes carefully and verify them. You are responsible for the code you write. Prefer edit_file over write_file for existing files. Run tests after changes. You cannot delete files."
	case CatAeron:
		return "You are in the Aeron tier. You have minimal tools and a tight budget. Stay focused on the simple task at hand. Do not over-engineer. Keep actions minimal and efficient. You cannot delete files or execute arbitrary commands unless explicitly allowed."
	default:
		return "You are a sub-agent. Follow your instructions precisely."
	}
}
