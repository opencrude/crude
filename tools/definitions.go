package tools

import (
	"encoding/json"

	"crude/codexio"
)

// ToolDef describes a tool available to the model.
type ToolDef struct {
	Type     string              `json:"type"`
	Function ToolFuncDef         `json:"function"`
	Format   *codexio.ToolFormat `json:"format,omitempty"`
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
				"Set timeout_seconds=0 for unlimited. Commands still running after 30 seconds are automatically " +
				"promoted to an async handle (manage them with check_async_command, read_async_output, kill_async_command) " +
				"instead of blocking the turn. For work you already know is long-running, use async_execute_command directly. " +
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
			Description: "Read a local path for direct inspection (Read primitive). Handles text, documents, images, audio, and video; use this tool directly to inspect screenshots or other supported media, without a separate viewer or integration.\n\n" +
				"Returns by file type:\n" +
				"- Text and source files: text with line numbers. offset and limit select a line range; encoded=true adds hashline anchors (e.g. '1:a3b7| code') for precise edits.\n" +
				"- Office documents, ebooks, and notebooks (DOCX, PPTX, XLSX/XLS, EPUB, IPYNB): extracted Markdown. offset and limit select converted lines.\n" +
				"- Images/screenshots (PNG, JPEG, GIF, WebP), PDF documents, audio (e.g. WAV, MP3), and video (e.g. MP4, MOV, WebM): whole-file native attachments for the model to inspect, not a text, hex, or base64 dump. Text-range and hashline options do not alter media attachments.\n" +
				"- Directories: a directory listing. Use list_directory for explicit or recursive directory reads.\n\n" +
				"Native media requires the active model's enabled input modality and a provider protocol that supports the file's MIME type. Size and filesystem safety checks apply. Unsupported or disabled media returns an explicit refusal; it is not silently converted to text. Always read a file before editing it.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"path":{"type":"string","description":"Path to the file"},
					"offset":{"type":"integer","description":"First text or converted-document line to return (1-based; default: 1). Does not crop media attachments."},
					"limit":{"type":"integer","description":"Maximum text or converted-document lines to return (default: all). Does not limit media attachments."},
					"encoded":{"type":"boolean","description":"Add line:hash anchors to plain-text reads (default: false). Does not encode or change media attachments."}
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
				"Use read_file first to verify the exact text. Do not call edit_file if old_string and new_string are identical; use read_file for inspection and skip edits when nothing changes. " +
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
					"new_string":{"type":"string","description":"Replacement text. MUST differ from old_string; do not use this tool for inspection or no-op edits."}
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
			Name:        "reply",
			Description: "Send a brief, truthful one-line progress update to the user WITHOUT ending the loop. Use at a meaningful phase change, milestone, or blocker, not after every tool call: routine tool calls are already grouped into an expandable activity trail. Explain what the work is for, not just the next tool's name. Do NOT use for full reports or final answers; end with a normal response (no tool calls).",
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
			Description: "Ask the user 1-4 focused questions only when an answer is genuinely required to continue. " +
				"Supply questions as a real JSON array, not a JSON-encoded string. Each item needs a short header and a clear question. " +
				"Options are optional because the user can write a custom response. Batch related questions in one call. " +
				"The result maps each question to the user's answer, or explicitly reports cancellation or validation failure.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"questions":{
						"type":"array",
						"minItems":1,
						"maxItems":4,
						"items":{
							"type":"object",
							"additionalProperties":false,
							"properties":{
								"question":{"type":"string","minLength":1,"description":"The complete question shown to the user."},
								"header":{"type":"string","minLength":1,"maxLength":30,"description":"Short topic label, at most 30 characters."},
								"options":{
									"type":"array",
									"maxItems":5,
									"uniqueItems":true,
									"items":{"type":"string","minLength":1},
									"description":"Suggested options for the user to choose from. Omit for free-text input."
								},
								"multiple":{"type":"boolean","default":false,"description":"Allow selecting multiple choices."}
							},
							"required":["question","header"]
						},
						"description":"Array of questions to ask. Batch related questions together."
					}
				},
				"required":["questions"],
				"additionalProperties":false
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "update_plan",
			Description: "Create or update the session plan the user sees as a compact banner. Creation requires plan_id, objective, and a complete ordered steps snapshot. On an existing plan omit objective/status/steps to preserve them and send the latest revision; when steps are supplied they are the complete desired ordered snapshot — reorder by array order and omit obsolete steps. Prior snapshots remain in revision history, not the current banner. For a materially new goal use replace=true with objective and steps to atomically supersede the old goal. Review latest intent, update at each milestone before starting the next stage, and honestly complete or cancel before the final answer. Step statuses are declarative reporting, not execution gates: update any step in any order, including cancelling dependencies or reopening prior work. The banner is not a notebook: keep each step concise and never paste logs, code, or running narration into a plan.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"plan_id":{"type":"string","description":"Stable plan identifier. Reuse the same id across updates."},
					"replace":{"type":"boolean","description":"Atomically replace the current goal and reset step provenance; requires objective and complete nonempty steps. Prior revisions remain in history. Default false."},
					"revision":{"type":"integer","description":"Last known revision. Omit or 0 to create a new plan. A mismatch returns the current plan."},
					"objective":{"type":"string","description":"Required on creation. One short line naming the outcome the user asked for. Omit or leave empty on update to preserve the current objective."},
					"status":{"type":"string","enum":["active","paused","completed","cancelled"],"description":"Desired plan status. Omit on update to preserve current status."},
					"explanation":{"type":"string","description":"Optional human-readable note (e.g. why paused/cancelled)."},
					"steps":{
						"type":"array",
						"description":"Required on creation. On update, omit to preserve current steps; when supplied this is the complete ordered snapshot.",
						"items":{
							"type":"object",
							"properties":{
								"id":{"type":"string","description":"Stable step id (e.g. 'audit')."},
								"description":{"type":"string","description":"What this step does, as a short one-line action (a few words) shown verbatim on the plan card. Never paste logs, code, or narration."},
								"status":{"type":"string","enum":["pending","in_progress","completed","skipped","blocked","failed","cancelled"],"description":"Step status. Default pending."},
								"depends_on":{"type":"array","items":{"type":"string"},"description":"Related prerequisite ids for display and context; does not restrict update order."},
								"reason":{"type":"string","description":"Optional one-line context for this status, especially blocked/failed/skipped/cancelled work."}
							},
							"required":["id","description"]
						}
					}
				},
				"required":["plan_id"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "get_plan",
			Description: "Fetch the current plan. Pass plan_id to fetch a specific plan, or omit it to fetch the active plan for this session. Returns the canonical plan JSON (id, revision, objective, status, steps with their statuses and dependencies). Use this after a revision conflict or before a major update to re-read the exact current state.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"plan_id":{"type":"string","description":"Plan id to fetch. Omit to get the active plan."},
					"include_history":{"type":"boolean","description":"With plan_id, include revision events. Without plan_id, list all plan summaries."}
				},
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
				"Use check_async_command only for lifecycle status, read_async_output for retained content pages, and kill_async_command to stop it. " +
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
			Description: "Check lifecycle status for a background command started via async_execute_command. " +
				"Returns handle, PID, status, retained output byte count, and terminal exit/error only when known. " +
				"This does not read or advance output; use read_async_output for content rather than repeating status checks.",
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
			Description: "Read one bounded stdout+stderr page from a running or completed background command. " +
				"Track `since_byte` to read incrementally; the result reports the exact byte range, next_byte, total_bytes, status, and whether more remains. " +
				"If a completed synchronous/native command retained a real handle, read that handle instead of rerunning the command or polling status for content. " +
				"Do not drain huge logs into model context: redirect high-volume output to CRUDE_TEMP_DIR and inspect relevant ranges/searches instead.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"handle":{"type":"string","description":"Handle of the async command to read output from"},
					"since_byte":{"type":"integer","description":"Raw byte offset to read from; use the previous next_byte for the next page (optional)"},
					"max_bytes":{"type":"integer","minimum":1,"maximum":65536,"description":"Maximum raw bytes returned in this page; default 32768"}
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
			Name: "remember",
			Description: "Save a durable workspace memory for future turns and sessions. " +
				"Use for stable project conventions, user preferences, verified architectural decisions/facts, " +
				"useful external references, and lessons from failures whose fix was confirmed (kind=lesson). " +
				"Never store secrets, transient state, command output, or unverified guesses.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"title":{"type":"string","description":"Short descriptive title. IDs are stable independently of titles; duplicates do not replace records."},
					"content":{"type":"string","description":"Self-contained knowledge to retain (1-4 sentences)."},
					"kind":{"type":"string","enum":["fact","decision","convention","preference","lesson","reference"],"description":"Memory category; default fact."},
					"importance":{"type":"integer","description":"0-100; most memories are 30-60, 80+ is rare. Default 40."},
					"paths":{"type":"array","items":{"type":"string"},"description":"Repo-relative files/dirs this memory applies to. Boosts recall when those files are active. Omit for project-wide."},
					"pinned":{"type":"boolean","description":"Always inject this memory into context. Reserve for critical rules."},
					"applies_when":{"type":"string","description":"Conditions or exceptions that limit this assertion."},
					"evidence":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"session_id":{"type":"string"},"message_id":{"type":"string"},"tool_call_id":{"type":"string"},"kind":{"type":"string","enum":["user_statement","tool_result","verified_change","validation"]},"summary":{"type":"string","maxLength":1024},"outcome":{"type":"string","enum":["succeeded","failed","unmeasured","cancelled"]}},"required":["session_id","kind","summary"]}}
				},
				"required":["title","content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "recall_memory",
			Description: "Search durable workspace memory (facts, decisions, conventions, preferences, lessons) from all prior work in this project. " +
				"Results include exact revision, applicability and evidence indicators, ranked by relevance and importance. Exposure does not increase rank or knowledge freshness.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"Terms describing the information to recall (identifiers, paths, and error strings work well)."},
					"limit":{"type":"integer","description":"Maximum memories to return; default 12, maximum 50."}
				},
				"required":["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "update_memory",
			Description: "Replace an existing workspace memory with corrected or refined content. " +
				"Requires the expected current revision and appends an immutable audited revision under the same stable ID. A version conflict preserves the current correction; recall before retrying.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"Memory ID being corrected (from recall_memory or the Workspace Memory context block)."},
					"expected_revision":{"type":"integer","minimum":1,"description":"Exact revision being corrected; required for compare-and-swap."},
					"content":{"type":"string","description":"The corrected, self-contained content."},
					"title":{"type":"string","description":"New title; omit to keep the existing title."},
					"kind":{"type":"string","enum":["fact","decision","convention","preference","lesson","reference"],"description":"New category; omit to keep."},
					"importance":{"type":"integer","description":"0-100; omit to keep."},
					"paths":{"type":"array","items":{"type":"string"},"description":"New scope paths; omit to keep."},
					"pinned":{"type":"boolean","description":"New pin preference; omit to keep."},
					"applies_when":{"type":"string","description":"New applicability conditions; omit to keep."},
					"evidence":{"type":"array","maxItems":16,"items":{"type":"object","properties":{"session_id":{"type":"string"},"message_id":{"type":"string"},"tool_call_id":{"type":"string"},"kind":{"type":"string","enum":["user_statement","tool_result","verified_change","validation"]},"summary":{"type":"string","maxLength":1024},"outcome":{"type":"string","enum":["succeeded","failed","unmeasured","cancelled"]}},"required":["session_id","kind","summary"]}}
				},
				"required":["id","expected_revision","content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "forget_memory",
			Description: "Retire one workspace memory by ID when it is obsolete or incorrect. " +
				"The record is superseded, not destroyed, so the change stays auditable.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"id":{"type":"string","description":"Memory ID returned by remember or recall_memory."},
					"expected_revision":{"type":"integer","minimum":1,"description":"Exact revision to retire; a concurrent correction is never overwritten."}
				},
				"required":["id","expected_revision"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "memory_feedback",
			Description: "Record evidence-based useful, misleading, contradicted or not_applicable feedback for exact memory revisions. Mere exposure or successful turns are not usefulness evidence; peer claims are not verification.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{"items":{"type":"array","minItems":1,"maxItems":32,"items":{"type":"object","properties":{"id":{"type":"string"},"revision":{"type":"integer","minimum":1},"kind":{"type":"string","enum":["useful","misleading","contradicted","not_applicable"]},"evidence":{"type":"array","minItems":1,"maxItems":16,"items":{"type":"object","properties":{"session_id":{"type":"string"},"message_id":{"type":"string"},"tool_call_id":{"type":"string"},"kind":{"type":"string","enum":["user_statement","tool_result","verified_change","validation"]},"summary":{"type":"string","maxLength":1024},"outcome":{"type":"string","enum":["succeeded","failed","unmeasured","cancelled"]}},"required":["session_id","kind","summary"]}}},"required":["id","revision","kind","evidence"]}}},
				"required":["items"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "search_history",
			Description: "Search everything ever done in this workspace: past conversation turns (what was asked, what was done, which files were touched) and per-session summaries across ALL sessions. " +
				"Use to answer questions like 'what did we do last time', 'have we seen this error before', or 'which session changed file X'.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"Terms to search for (identifiers, paths, error strings, and topics all work)."},
					"limit":{"type":"integer","description":"Maximum results per category; default 10, maximum 50."}
				},
				"required":["query"]
			}`),
		},
	},
}
