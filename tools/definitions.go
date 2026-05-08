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
			Description: "Execute a shell command (Execute primitive). " +
				"Use for running scripts, checking system state, managing services, or any shell operation. " +
				"Set cwd to run in a specific directory. Set timeout_seconds for long-running commands.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"command":{"type":"string","description":"Shell command to run"},
					"cwd":{"type":"string","description":"Working directory for the command (optional)"},
					"timeout_seconds":{"type":"integer","description":"Abort after this many seconds; 0 = no limit (optional)"}
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
				"Binary files are detected and rejected with a helpful error message.",
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
				"old_string must match the file content exactly (including whitespace and indentation). " +
				"Use read_file first to verify the exact text. " +
				"If old_string is not found exactly, a whitespace-normalised fallback match is attempted automatically. " +
				"Ambiguous matches (found in multiple locations) are rejected — add more surrounding lines to make the match unique. " +
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
				"Use to discover files before reading or editing them.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"pattern":{"type":"string","description":"Glob pattern (e.g. '**/*.go')"},
					"base_dir":{"type":"string","description":"Directory to search in (optional; default: current directory)"}
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
				"Supports full regex syntax. Searches recursively by default.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"pattern":{"type":"string","description":"Regex pattern to search for (e.g. 'func\\\\s+\\\\w+')"},
					"path":{"type":"string","description":"Directory or file to search in (optional; default: current directory)"},
					"recursive":{"type":"boolean","description":"Search subdirectories recursively (optional; default: true)"}
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
				"Use to read documentation, articles, or any web page content.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"url":{"type":"string","description":"URL to fetch content from"},
					"max_chars":{"type":"integer","description":"Maximum characters to return (optional; 0 = no limit)"}
				},
				"required":["url"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "consult_oracle",
			Description: "Consult the oracle model for high-stakes architectural, algorithmic, or structural decisions. " +
				"Use when: (1) choosing between multiple architectural approaches, (2) solving non-trivial algorithmic problems, " +
				"(3) deciding module boundaries, data flow, or folder structure, (4) making decisions with significant downstream impact, " +
				"(5) you are confused or uncertain which approach is best. The oracle has stronger reasoning but no tool access — it produces pure analysis, not actions. " +
				"Provide maximal relevant context so the oracle can make an informed decision.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"task":{"type":"string","description":"The high-level goal or problem statement"},
					"context":{"type":"string","description":"Relevant codebase context: file structure, key code excerpts, dependencies, constraints"},
					"decision_point":{"type":"string","description":"The specific decision or question you need resolved"},
					"constraints":{"type":"string","description":"Hard constraints that any solution must respect"}
				},
				"required":["task","decision_point"]
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
			Name:        "stop",
			Description: "End the agentic loop and deliver your final message to the user. Call this when you have completed the task, answered the question, or need to hand control back to the user. This is the ONLY way to reliably end the conversation turn — always end with stop().",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"message":{"type":"string","description":"Your final message to the user. Summarize what you did, what you found, or your answer to their question."}
				},
				"required":["message"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "reply",
			Description: "Send an intermediate message to the user WITHOUT ending the loop. Use for progress updates, partial results, or questions that need a response before continuing work. The loop continues after this — call stop() when truly done.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"content":{"type":"string","description":"The intermediate message to show the user"}
				},
				"required":["content"]
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
					"objective":{"type":"string","description":"High-level description of what this plan aims to achieve"},
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
			Description: "Mark a plan step as completed. Call this after successfully executing a step of your plan. " +
				"Provide the step ID and any results or output data generated by the step. " +
				"This updates the state machine and unblocks any dependent steps.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"step_id":{"type":"string","description":"The step ID to mark as complete (e.g. 'step1')"},
					"result":{"type":"string","description":"Summary of what was accomplished in this step (optional)"}
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
}
