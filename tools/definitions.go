package tools

import (
	"encoding/json"
)

// ToolDef describes a tool available to the model.
type ToolDef struct {
	Type     string      `json:"type"`
	Function ToolFuncDef `json:"function"`
}

// ToolFuncDef is the function schema within a tool definition.
type ToolFuncDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Tool Definitions

// AgentTools lists the tools exposed to the model every conversation turn.
var AgentTools = []ToolDef{
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "execute_command",
			Description: "Execute a shell command SYNCHRONOUSLY (blocks until completion). " +
				"The agent loop PAUSES while this runs. Returns stdout+stderr. " +
				"Default timeout is 60 seconds — commands are auto-killed if they exceed this. " +
				"Set timeout_seconds=0 for unlimited. For background / long-running work, use async_execute_command instead. " +
				"Set cwd to run in a specific directory.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"command":{"type":"string","description":"Shell command to run synchronously"},
					"cwd":{"type":"string","description":"Working directory for the command (optional)"},
					"timeout_seconds":{"type":"integer","description":"Abort after this many seconds; default=60, 0=no limit (optional)"}
				},
				"required":["command"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "read_file",
			Description: "Read the contents of a file (Read primitive). " +
				"Use offset and limit to read a specific range of lines in large files. " +
				"Output includes line numbers. Always read a file before editing it. " +
				"Set encoded=true for hashline format with line anchors (e.g. '1:a3b7| code') — " +
				"useful for anchor-based edits that save tokens on large files. " +
				"Binary files (PDF, DOCX, PPTX, XLSX, etc.) are automatically converted to Markdown — " +
				"you can read PDFs, Word docs, spreadsheets, and presentations like text files.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"path":{"type":"string","description":"Path to the file"},
					"offset":{"type":"integer","description":"First line to return (1-based, optional; default: 1)"},
					"limit":{"type":"integer","description":"Maximum number of lines to return (optional; default: all)"},
					"encoded":{"type":"boolean","description":"Return hashline-encoded format with line:hash anchors (optional; default: false)"}
				},
				"required":["path"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "write_file",
			Description: "Create or completely overwrite a file (Create/Update primitive). " +
				"Parent directories are created automatically. " +
				"Prefer edit_file for targeted changes to existing files — only use write_file for new files or full rewrites. " +
				"Operations are journalled and revertable with /undo. " +
				"Binary files and symlinks are rejected for safety.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"path":{"type":"string","description":"Path to the file"},
					"content":{"type":"string","description":"Full file content to write"}
				},
				"required":["path","content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "edit_file",
			Description: "Replace an exact string inside a file (Update primitive). " +
				"Copy FULL lines from read_file output for old_string — start each line from the very beginning including ALL leading whitespace/indentation. " +
				"Use read_file first to verify the exact text. " +
				"If old_string is not found exactly, a whitespace-normalised fallback match is attempted automatically. " +
				"Ambiguous matches (found in multiple locations) are rejected — add more surrounding lines to make the match unique. " +
				"NEVER start old_string from the middle of a line — always include the full line from column 0. " +
				"Operations are journalled and revertable with /undo. " +
				"Binary files and symlinks are rejected for safety.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"path":{"type":"string","description":"Path to the file to edit"},
					"old_string":{"type":"string","description":"Exact text to find and replace"},
					"new_string":{"type":"string","description":"Text to replace it with"}
				},
				"required":["path","old_string","new_string"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "delete_file",
			Description: "Delete a file (Delete primitive). Verify the path with read_file or list_directory before calling. " +
				"Operations are journalled and revertable with /undo. " +
				"Symlinks are rejected for safety.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"path":{"type":"string","description":"Path to the file to delete"}
				},
				"required":["path"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "move_file",
			Description: "Move or rename a file (Update primitive). Parent directories of the destination are created automatically. " +
				"Operations are journalled and revertable with /undo. " +
				"Symlinks are rejected for safety.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"source":{"type":"string","description":"Source path"},
					"destination":{"type":"string","description":"Destination path"}
				},
				"required":["source","destination"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "copy_file",
			Description: "Copy a file to a new location (Create primitive). Parent directories of the destination are created automatically. " +
				"Operations are journalled and revertable with /undo. " +
				"Symlinks are rejected for safety.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"source":{"type":"string","description":"Source path"},
					"destination":{"type":"string","description":"Destination path"}
				},
				"required":["source","destination"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "list_directory",
			Description: "List the contents of a directory with metadata (Read primitive). " +
				"Returns type, size, modification time, and permissions for each entry. " +
				"Use to explore the filesystem and check file sizes before reading. " +
				"An empty directory returns a message confirming it is empty — this is a success, not an error.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"path":{"type":"string","description":"Path to the directory"},
					"recursive":{"type":"boolean","description":"Recurse into subdirectories (optional; default: false)"}
				},
				"required":["path"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "glob_files",
			Description: "Find files matching a glob pattern (Read primitive). " +
				"Supports * (any chars in one segment), ** (any path depth), and ? (single char). " +
				"Examples: '**/*.go', 'src/**/*.ts', '*.json'. " +
				"By default hidden directories and common dependency/build folders are excluded from recursion. " +
				"Use include_hidden=true to search hidden directories too, or set base_dir to a specific folder (e.g. 'node_modules') to search it directly.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"pattern":{"type":"string","description":"Glob pattern (e.g. '**/*.go')"},
					"base_dir":{"type":"string","description":"Directory to search in (optional; default: current directory)"},
					"include_hidden":{"type":"boolean","description":"Recurse into hidden directories (names starting with '.') in addition to normal directories (optional; default: false)"}
				},
				"required":["pattern"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "grep_files",
			Description: "Search file contents using regular expressions (Read primitive). " +
				"Returns matching lines with file paths and line numbers. " +
				"Use to find usages of functions, variables, imports, or patterns across the codebase. " +
				"Supports full regex syntax. Searches recursively by default. " +
				"By default hidden directories and common dependency/build folders are excluded from recursion. " +
				"Use include_hidden=true to search hidden directories too, or set path to a specific folder (e.g. 'node_modules') to search it directly.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"pattern":{"type":"string","description":"Regex pattern to search for (e.g. 'func\\s+\\w+')"},
					"path":{"type":"string","description":"Directory or file to search in (optional; default: current directory)"},
					"recursive":{"type":"boolean","description":"Search subdirectories recursively (optional; default: true)"},
					"include_hidden":{"type":"boolean","description":"Recurse into hidden directories (names starting with '.') in addition to normal directories (optional; default: false)"}
				},
				"required":["pattern"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "arxiv_search",
			Description: "Search academic papers on arXiv by query. " +
				"Returns paper titles, authors, publication dates, summaries, and links. " +
				"Use for finding research papers, academic references, or staying current with ML/AI literature.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"Search query for arXiv papers"},
					"max_results":{"type":"integer","description":"Maximum number of results to return (1-10, optional; default: 5)"}
				},
				"required":["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "web_search",
			Description: "Search the web using Brave Search API. " +
				"Returns titles, URLs, and snippets for web pages matching the query. " +
				"Use for finding current information, documentation, or general web content.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"Search query for web search"}
				},
				"required":["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "web_fetch",
			Description: "Fetch and extract readable content from a URL. " +
				"Returns the main text content of a web page, stripped of HTML markup. " +
				"Use offset and limit to read a specific range of characters (like read_file). " +
				"First fetch gives the total content length — use offset/limit for subsequent pagination. " +
				"Use to read documentation, articles, or any web page content. " +
				"PDFs, Word docs, and other document URLs are automatically converted to Markdown.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"url":{"type":"string","description":"URL to fetch content from"},
					"offset":{"type":"integer","description":"Starting character position (1-based, optional; default: 1)"},
					"limit":{"type":"integer","description":"Maximum characters to return (optional; default: 0 = all)"}
				},
				"required":["url"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "run_javascript",
			Description: "Execute JavaScript code and return the result. " +
				"Use for calculations, data transformations, or any logic that benefits from JavaScript's capabilities. " +
				"You are TERRIBLE at math — use this tool for any calculations.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"JavaScript code to execute"}
				},
				"required":["code"]
			}`),
		},
	},
 {
		Type: "function",
		Function: ToolFuncDef{
			Name:        "reply",
			Description: "Send a brief progress update to the user WITHOUT ending the loop. Maximum 1 line — this is for short status updates only (e.g. 'Running tests...', 'Found 3 issues'). Do NOT use this for full reports, detailed explanations, or long text — instead, respond with text only (no tool calls) when done. Long content will not be displayed properly.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"content":{"type":"string","description":"A short one-line status update. NOT for full reports or long text."}
				},
				"required":["content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "question",
			Description: "Ask the user one or more questions with suggested options. " +
				"Use when you need clarification, a decision, or user input to proceed. " +
				"Batch multiple questions into a single call using the 'questions' array — " +
				"this presents all questions together in one form for efficient interaction. " +
				"For yes/no questions, provide options like ['yes', 'no']. " +
				"For multiple choice, list the choices. " +
				"For free-text input, omit the options array. " +
				"Returns a JSON object mapping question indices (1-based) to answers.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"questions":{
						"type":"array",
						"items":{
							"type":"object",
							"properties":{
								"question":{"type":"string","description":"The question text"},
								"header":{"type":"string","description":"Short label for this question (max 30 chars, e.g. 'Language', 'Rating')"},
								"options":{
									"type":"array",
									"items":{"type":"string"},
									"description":"Suggested options for the user to choose from. Omit for free-text input."
								},
								"multiple":{"type":"boolean","description":"Allow selecting multiple choices (default: false)"}
							},
							"required":["question"]
						},
						"description":"Array of questions to ask. Batch related questions together."
					},
					"question":{"type":"string","description":"Single question (legacy, prefer 'questions' array)"},
					"options":{
						"type":"array",
						"items":{"type":"string"},
						"description":"Options for single question (legacy, prefer 'questions' array)"
					}
				},
				"required":[]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "plan",
			Description: "Create a multi-step plan stored in the state machine. " +
				"Use this when the task requires multiple coordinated steps (e.g., read a file, then edit it, then verify). " +
				"Each step is registered as a tracked sub-goal. After calling plan, execute each step individually using " +
				"regular tools. After completing a step, call mark_step_complete to update the plan's progress. " +
				"For simple single-step tasks, call the tool directly instead.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"objective":{"type":"string","description":"Brief one-line title for this plan (max ~80 chars). Be concise — this is displayed as a heading, not a detailed spec. Example: 'Fix auth redirect loop' NOT 'Fix the bug where users get redirected in a loop when their session expires and they try to access /dashboard'"},
					"steps":{
						"type":"array",
						"description":"Ordered list of plan steps to track",
						"items":{
							"type":"object",
							"properties":{
								"id":{"type":"string","description":"Unique step identifier (e.g. step1, step2)"},
								"description":{"type":"string","description":"Brief description of what this step does"},
								"depends_on":{"type":"array","items":{"type":"string"},"description":"IDs of steps that must complete before this one starts (optional)"}
							},
							"required":["id","description"]
						}
					}
				},
				"required":["objective","steps"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "mark_step_complete",
			Description: "Mark one or more plan steps as completed. Call this after successfully executing steps of your plan. " +
				"Provide the step ID (or an array of step IDs for batch completion) and optionally a result summary. " +
				"Optionally provide 'activate_steps' to also mark steps as doing in the same call — " +
				"this is a convenience so you don't need a separate mark_step_doing call. " +
				"Does NOT auto-activate the next step unless you specify activate_steps.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"step_id":{
						"type":"string",
						"description":"A single step ID or an array of step IDs to mark as complete (e.g. 'step1' or 'step1,step2')"
					},
					"result":{"type":"string","description":"Summary of what was accomplished (optional)"},
					"activate_steps":{
						"type":"string",
						"description":"A single step ID or comma-separated step IDs to mark as doing (e.g. 'step3' or 'step3,step4'). Optional convenience to activate next step(s) without a separate call."
					}
				},
				"required":["step_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "mark_step_doing",
			Description: "Mark one or more plan steps as actively being worked on (doing). " +
				"Call this before you start executing a step to indicate which step(s) you are currently working on. " +
				"You can mark multiple steps as doing if you are working on them in parallel.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"step_id":{
						"type":"string",
						"description":"A single step ID or an array of step IDs to mark as doing (e.g. 'step1' or 'step1,step2')"
					}
				},
				"required":["step_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "mark_step_failed",
			Description: "Mark a plan step as failed. Call this when a step cannot be completed due to an error. " +
				"This updates the state machine to reflect the failure.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"step_id":{"type":"string","description":"The step ID that failed (e.g. 'step1')"},
					"reason":{"type":"string","description":"Why the step failed"}
				},
				"required":["step_id","reason"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "report_blocker",
			Description: "Report a blocking condition that prevents progress. " +
				"Use when you encounter missing information, need user confirmation, hit API errors, or face ambiguous requirements. " +
				"The blocker is recorded in the state machine and shown as the current status.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"blocker_type":{"type":"string","description":"Type of blocker: 'missing_info', 'confirmation_needed', 'api_error', 'ambiguous', 'auth_required'"},
					"message":{"type":"string","description":"Description of what is blocking progress"}
				},
				"required":["blocker_type","message"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "resolve_blocker",
			Description: "Resolve a previously reported blocker. Call this when the blocking condition has been addressed " +
				"and normal plan execution can resume.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"message":{"type":"string","description":"How the blocker was resolved (optional)"}
				},
				"required":[]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "list_goal_status",
			Description: "List all current goals, plan steps, their statuses, and any active blockers. " +
				"Returns a formatted overview of the state machine: active goal, completed steps, blocked steps, and suspended goals. " +
				"Use this to get your bearings when resuming work.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{},
				"required":[]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "async_execute_command",
			Description: "Start a shell command in the BACKGROUND. Does NOT block the agent loop. " +
				"Assign a unique 'handle' name to track this command. " +
				"Use check_async_command to poll status, read_async_output to read partial output, kill_async_command to stop it. " +
				"Best for: dev servers, watchers, long builds, any command you want to run while doing other work. " +
				"Each handle must be unique — kill or wait for completion before reusing. " +
				"RELIABILITY: chain a visible completion signal like '&& echo DONE' or '&& echo SUCCESS' " +
				"so you can verify completion by reading output rather than relying on exit codes alone. " +
				"Also use '|| echo FAILED' to catch errors in output.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"command":{"type":"string","description":"Shell command to run in background"},
					"handle":{"type":"string","description":"Unique name to track this command (e.g. 'dev_server', 'build')"},
					"cwd":{"type":"string","description":"Working directory for the command (optional)"},
					"timeout_seconds":{"type":"integer","description":"Auto-kill after this many seconds; default=0=no limit (optional)"}
				},
				"required":["command","handle"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "check_async_command",
			Description: "Check the status of a background command started via async_execute_command. " +
				"Returns: handle, PID, status (running/completed/failed/killed), exit code, and error if any. " +
				"Use this to poll whether a command finished before acting on its output.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"handle":{"type":"string","description":"Handle of the async command to check"}
				},
				"required":["handle"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "kill_async_command",
			Description: "Kill a running background command by its handle. " +
				"Use when a dev server needs restarting, a build is taking too long, or a watcher should stop. " +
				"After killing, call check_async_command to confirm status, then the handle can be reused.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"handle":{"type":"string","description":"Handle of the async command to kill"}
				},
				"required":["handle"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "read_async_output",
			Description: "Read accumulated stdout+stderr from a running or completed background command. " +
				"Track `since_byte` to read incrementally (returns new output since that offset). " +
				"The returned `next_byte` value is the new cursor — pass it on the next call to get only fresh output. " +
				"Start with since_byte=0 on first call.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"handle":{"type":"string","description":"Handle of the async command to read output from"},
					"since_byte":{"type":"integer","description":"Byte offset to read from; 0 for all output, or use previous next_byte for incremental (optional)"}
				},
				"required":["handle"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "wait",
			Description: "Pause execution for a specified number of seconds. " +
				"The agent loop BLOCKS during this time — no other work happens. " +
				"Use for: rate-limiting API calls, waiting for async commands to finish (pair with check_async_command), " +
				"or introducing deliberate delays between operations. " +
				"Prefer async_execute_command + check_async_command over wait-based polling when possible.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"seconds":{"type":"number","description":"Number of seconds to wait (supports decimal for sub-second waits)"}
				},
				"required":["seconds"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "run_sub_agent",
			Description: "Spawn a specialized sub-agent to handle a well-defined subtask. The sub-agent gets its own context window, tool set, permission policy, session, and TUI tab. It runs to completion and returns a structured result. USE when a task is: self-contained (fully describable in 1-2 sentences), non-trivial (3+ tool calls), and benefits from a separate context window or cheaper model. Do NOT use for tasks requiring context from the current conversation or trivial 1-2 call operations. Sub-agents can spawn their own sub-agents up to max_depth.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"name":{"type":"string","description":"Short name for this sub-agent (e.g. 'explorer', 'fixer', 'reviewer'). Used for display and tracking."},
					"category":{"type":"string","description":"Capability tier: 'researcher' (read-only, cheap, fast), 'crafter' (implementation, write-capable, mid-cost), or 'aeron' (grunt work, ultra-cheap, restricted). Default: 'crafter'.","enum":["researcher","crafter","aeron"]},
					"description":{"type":"string","description":"The task this sub-agent should accomplish. Be specific."},
					"instructions":{"type":"string","description":"Custom instructions appended to the sub-agent's system prompt. Use to specialize behavior."},
					"model":{"type":"string","description":"Model override for this sub-agent. Empty = inherit from category default."},
					"provider":{"type":"string","description":"Provider key override. Empty = inherit from category default."},
					"policy":{"type":"string","description":"Permission policy: 'default', 'read-only', 'no-delete', or 'no-execute'. Default: inherited from category.","enum":["default","read-only","no-delete","no-execute"]},
					"allowed_tools":{"type":"array","items":{"type":"string"},"description":"Explicit tool whitelist. If provided, only these tools are available (intersected with category defaults)."},
					"denied_tools":{"type":"array","items":{"type":"string"},"description":"Explicit tool blacklist. These tools are removed (unioned with category defaults)."},
					"file_ownership":{"type":"array","items":{"type":"string"},"description":"Optional pre-declared paths this sub-agent claims exclusive write access to."},
					"max_steps":{"type":"integer","description":"Maximum tool-use steps. Default from category. Max 200."},
					"timeout_seconds":{"type":"integer","description":"Maximum runtime in seconds. Default from category."},
					"wait":{"type":"boolean","description":"If true (default), block until the sub-agent completes. If false, return immediately with a handle ID for later polling."}
				},
				"required":["name","description"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "poll_sub_agent",
			Description: "Check the status and retrieve results from an asynchronously spawned sub-agent. Returns the SubAgentResult if completed, or current status if still running.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"handle_id":{"type":"string","description":"The handle ID returned by run_sub_agent(wait=false)."}
				},
				"required":["handle_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "cancel_sub_agent",
			Description: "Cancel a running sub-agent. The sub-agent's context is cancelled, it returns a SubAgentResult with status 'cancelled', and all its file ownership claims are released.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"handle_id":{"type":"string","description":"The handle ID of the sub-agent to cancel."}
				},
				"required":["handle_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "list_sub_agents",
			Description: "List all active and recently completed sub-agents. Shows status, category, model, elapsed time, and steps used.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"include_completed":{"type":"boolean","description":"If true, also include completed/cancelled/failed agents from this session. Default: false (active only)."}
				}
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "write_report",
			Description: "Write your final result report. This becomes the sub-agent's official result. The gist (first 2000 chars) enters the parent's context window. The full report is saved to disk. If you don't call this, your last non-tool-call message becomes the result.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"gist":{"type":"string","description":"Concise summary of results, max 2000 characters. This enters the parent's context window."},
					"report":{"type":"string","description":"Full detailed report. Saved to disk. No size limit."}
				},
				"required":["gist"]
			}`),
		},
	},
}
