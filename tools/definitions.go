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
			Name: "consult_oracle",
			Description: "Consult the oracle model for high-stakes architectural, algorithmic, or structural decisions. " +
				"Use when: (1) choosing between multiple architectural approaches, (2) solving non-trivial algorithmic problems, " +
				"(3) deciding module boundaries, data flow, or folder structure, (4) making decisions with significant downstream impact, " +
				"(5) you are uncertain which approach is best. The oracle has stronger reasoning but no tool access — it produces pure analysis, not actions. " +
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
			Name: "reply",
			Description: "Send a conversational reply to the user. Use for greetings, chitchat, clarifications, or when no system operations are needed. Also use after completing a plan to present results.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"content":{"type":"string","description":"The conversational response to show the user"}
				},
				"required":["content"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "plan",
			Description: "Create and execute a multi-step plan of operations. Use this when the task requires multiple coordinated steps (e.g., read a file, then edit it, then verify). For simple single-step tasks, call the tool directly instead.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"steps":{
						"type":"array",
						"description":"Ordered list of plan steps to execute",
						"items":{
							"type":"object",
							"properties":{
								"id":{"type":"string","description":"Unique step identifier (e.g. step1, step2)"},
								"tool":{"type":"string","description":"Tool to call for this step"},
								"description":{"type":"string","description":"Brief description of what this step does"},
								"params":{"type":"object","description":"Tool parameters as key-value pairs"},
								"depends_on":{"type":"array","items":{"type":"string"},"description":"IDs of steps that must complete before this one starts"}
							},
							"required":["id","tool","description","params"]
						}
					}
				},
				"required":["steps"]
			}`),
		},
	},
}
