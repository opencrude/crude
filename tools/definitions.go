package tools

import (
	"encoding/json"
)

// Types

// ToolCall is a function invocation requested by the model.
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall holds the name and JSON-encoded arguments of a tool call.
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

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
				"Output includes line numbers. Always read a file before editing it.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"path":{"type":"string","description":"Path to the file"},
					"offset":{"type":"integer","description":"First line to return (1-based, optional; default: 1)"},
					"limit":{"type":"integer","description":"Maximum number of lines to return (optional; default: all)"}
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
				"Prefer edit_file for targeted changes to existing files — only use write_file for new files or full rewrites.",
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
				"All occurrences of old_string are replaced. Returns an error if old_string is not found.",
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
			Name:        "delete_file",
			Description: "Delete a file (Delete primitive). This is irreversible — verify the path with read_file or list_directory before calling.",
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
			Name:        "move_file",
			Description: "Move or rename a file (Update primitive). Parent directories of the destination are created automatically.",
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
			Name:        "copy_file",
			Description: "Copy a file to a new location (Create primitive). Parent directories of the destination are created automatically.",
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
				"Use to explore the filesystem and check file sizes before reading.",
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
			Description: "Search file contents using a regular expression (Read primitive). " +
				"Returns matching lines with file path and line number. " +
				"Use to locate specific code, config values, or patterns before editing.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"pattern":{"type":"string","description":"Regular expression to search for"},
					"path":{"type":"string","description":"File or directory to search (optional; default: current directory)"},
					"recursive":{"type":"boolean","description":"Recurse into subdirectories (optional; default: true)"}
				},
				"required":["pattern"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "web_search",
			Description: "Search the web for current information (Read primitive). " +
				"Use when you need up-to-date facts, recent events, or information not in local files. " +
				"Queries multiple search engines concurrently (Brave, DuckDuckGo, Startpage) and deduplicates results. " +
				"Automatically falls back to working engines if some are unavailable.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"Search query"}
				},
				"required":["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "arxiv_search",
			Description: "Search arXiv for academic papers (Read primitive). " +
				"Use when you need research papers, scientific references, or peer-reviewed sources. " +
				"Returns titles, authors, abstracts, and PDF links.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"query":{"type":"string","description":"Search query for arXiv papers"},
					"max_results":{"type":"integer","description":"Maximum results to return (default 5, max 10)"}
				},
				"required":["query"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "web_fetch",
			Description: "Fetch a URL and extract its text content (Read primitive). " +
				"Use to read documentation pages, articles, or any web resource. " +
				"Handles redirects, extracts main content from HTML, and supports plain text/JSON. " +
				"Returns title and extracted text content.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"url":{"type":"string","description":"URL to fetch"},
					"max_chars":{"type":"integer","description":"Maximum characters to return (default: unlimited)"}
				},
				"required":["url"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name: "run_javascript",
			Description: "Execute JavaScript code for calculations, data processing, or logic (Execute primitive). " +
				"Use instead of guessing math results. The last expression value is returned. " +
				"Available: full ES5+, Math, JSON, console.log, Array methods. " +
				"Extra helpers: sum(arr), mean(arr), round(v, decimals). " +
				"Timeout: 10s. Use for: unit conversions, statistical calculations, data transformations, regex testing.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"code":{"type":"string","description":"JavaScript code to execute. Last expression is the return value."}
				},
				"required":["code"]
			}`),
		},
	},
	{
		Type: "function",
		Function: ToolFuncDef{
			Name:        "reply",
			Description: "Send a conversational reply to the user (Read primitive). Use for greetings, chitchat, clarifications, or when no system operations are needed.",
			Parameters: json.RawMessage(`{
				"type":"object",
				"properties":{
					"content":{"type":"string","description":"The conversational response to show the user"}
				},
				"required":["content"]
			}`),
		},
	},
}
