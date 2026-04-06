package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"crude/backend"
	"crude/js"
	"crude/search"
)

// activeBackend is the currently configured execution backend.
var activeBackend backend.Executor = backend.NewLocal()

// agentTools lists the tools exposed to the model every conversation turn.
var agentTools = []ToolDef{
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
				"Returns titles, URLs, and snippets from DuckDuckGo.",
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

// executeTool dispatches a tool call to the active backend and returns the result string.
func executeTool(tc ToolCall) string {
	switch tc.Function.Name {

	case "execute_command":
		var args struct {
			Command string `json:"command"`
			CWD     string `json:"cwd"`
			Timeout int    `json:"timeout_seconds"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		opts := backend.ExecOptions{CWD: args.CWD, TimeoutSeconds: args.Timeout}
		out, err := activeBackend.Execute(args.Command, opts)
		if err != nil {
			if out != "" {
				return fmt.Sprintf("Command failed: %v\n\n%s", err, out)
			}
			return "Command failed: " + err.Error()
		}
		if out == "" {
			return "(command completed with no output)"
		}
		return out

	case "read_file":
		var args struct {
			Path   string `json:"path"`
			Offset int    `json:"offset"`
			Limit  int    `json:"limit"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		content, err := activeBackend.ReadFile(args.Path)
		if err != nil {
			return "Error: " + err.Error()
		}
		return formatReadFile(args.Path, content, args.Offset, args.Limit)

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if err := activeBackend.WriteFile(args.Path, args.Content); err != nil {
			return "Error: " + err.Error()
		}
		lines := strings.Count(args.Content, "\n") + 1
		return fmt.Sprintf("Written %d lines to %s", lines, args.Path)

	case "edit_file":
		var args struct {
			Path      string `json:"path"`
			OldString string `json:"old_string"`
			NewString string `json:"new_string"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		n, err := activeBackend.EditFile(args.Path, args.OldString, args.NewString)
		if err != nil {
			return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
		}
		if n == 1 {
			return fmt.Sprintf("Replaced 1 occurrence in %s", args.Path)
		}
		return fmt.Sprintf("Replaced %d occurrences in %s", n, args.Path)

	case "delete_file":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if err := activeBackend.DeleteFile(args.Path); err != nil {
			return "Error: " + err.Error()
		}
		return "Deleted: " + args.Path

	case "move_file":
		var args struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if err := activeBackend.MoveFile(args.Source, args.Destination); err != nil {
			return "Error: " + err.Error()
		}
		return fmt.Sprintf("Moved %s → %s", args.Source, args.Destination)

	case "copy_file":
		var args struct {
			Source      string `json:"source"`
			Destination string `json:"destination"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if err := activeBackend.CopyFile(args.Source, args.Destination); err != nil {
			return "Error: " + err.Error()
		}
		return fmt.Sprintf("Copied %s → %s", args.Source, args.Destination)

	case "list_directory":
		var args struct {
			Path      string `json:"path"`
			Recursive bool   `json:"recursive"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if args.Recursive {
			return listDirectoryRecursive(args.Path)
		}
		entries, err := activeBackend.ListDir(args.Path)
		if err != nil {
			return "Error: " + err.Error()
		}
		if len(entries) == 0 {
			return fmt.Sprintf("%s — (empty directory)", args.Path)
		}
		return formatDirEntries(args.Path, entries)

	case "glob_files":
		var args struct {
			Pattern string `json:"pattern"`
			BaseDir string `json:"base_dir"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if args.BaseDir == "" {
			args.BaseDir = "."
		}
		matches, err := activeBackend.GlobFiles(args.BaseDir, args.Pattern)
		if err != nil {
			return "Error: " + err.Error()
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No files matching %q under %s", args.Pattern, args.BaseDir)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d file(s) matching %q under %s:\n", len(matches), args.Pattern, args.BaseDir)
		for _, m := range matches {
			sb.WriteString(m)
			sb.WriteByte('\n')
		}
		return strings.TrimRight(sb.String(), "\n")

	case "grep_files":
		var args struct {
			Pattern   string `json:"pattern"`
			Path      string `json:"path"`
			Recursive *bool  `json:"recursive"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if args.Path == "" {
			args.Path = "."
		}
		recursive := true
		if args.Recursive != nil {
			recursive = *args.Recursive
		}
		matches, err := activeBackend.GrepFiles(args.Pattern, args.Path, recursive)
		if err != nil {
			return "Error: " + err.Error()
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No matches for %q in %s", args.Pattern, args.Path)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d match(es) for %q in %s:\n", len(matches), args.Pattern, args.Path)
		for _, m := range matches {
			fmt.Fprintf(&sb, "%s:%d: %s\n", m.Path, m.LineNum, m.Line)
		}
		return strings.TrimRight(sb.String(), "\n")

	case "web_search":
		var args struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		result, err := search.WebSearch(args.Query, 6)
		if err != nil {
			return "Search error: " + err.Error()
		}
		return result

	case "arxiv_search":
		var args struct {
			Query      string `json:"query"`
			MaxResults int    `json:"max_results"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if args.MaxResults <= 0 || args.MaxResults > 10 {
			args.MaxResults = 5
		}
		result, err := search.ArxivSearch(args.Query, args.MaxResults)
		if err != nil {
			return "ArXiv search error: " + err.Error()
		}
		return result

	case "web_fetch":
		var args struct {
			URL      string `json:"url"`
			MaxChars int    `json:"max_chars"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		result, err := search.WebFetch(args.URL, args.MaxChars)
		if err != nil {
			return "Fetch error: " + err.Error()
		}
		return result

	case "run_javascript":
		var args struct {
			Code string `json:"code"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		result, err := js.Run(args.Code)
		if err != nil {
			return "JavaScript error: " + err.Error()
		}
		return result

	case "reply":
		var args struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		return args.Content

	default:
		return "Error: unknown tool: " + tc.Function.Name
	}
}

// formatReadFile formats file content with line numbers, applying offset/limit.
func formatReadFile(path, content string, offset, limit int) string {
	lines := strings.Split(content, "\n")
	total := len(lines)

	// Handle empty file edge case.
	if total == 1 && lines[0] == "" {
		return fmt.Sprintf("%s — (empty file)", path)
	}

	// Normalise offset (1-based → 0-based index).
	startIdx := 0
	if offset > 1 {
		startIdx = offset - 1
	}
	if startIdx >= total {
		return fmt.Sprintf("Error: offset %d exceeds file length (%d lines)", offset, total)
	}

	endIdx := total
	if limit > 0 && startIdx+limit < total {
		endIdx = startIdx + limit
	}

	slice := lines[startIdx:endIdx]
	var sb strings.Builder

	if offset > 1 || limit > 0 {
		fmt.Fprintf(&sb, "Lines %d–%d of %s (%d total):\n", startIdx+1, endIdx, path, total)
	}

	width := len(fmt.Sprintf("%d", endIdx))
	for i, line := range slice {
		fmt.Fprintf(&sb, "%*d  %s\n", width, startIdx+1+i, line)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// formatDirEntries formats a list of DirEntry values into a readable table.
func formatDirEntries(path string, entries []backend.DirEntry) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d entries:\n", path, len(entries))
	for _, e := range entries {
		name := e.Name
		if e.IsDir {
			name += "/"
		} else if e.IsLink {
			name += "@"
		}
		modStr := ""
		if !e.ModTime.IsZero() {
			modStr = e.ModTime.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&sb, "%-12s  %8d  %s  %s\n", e.Mode, e.Size, modStr, name)
	}
	return strings.TrimRight(sb.String(), "\n")
}

// listDirectoryRecursive performs a recursive listing via glob.
func listDirectoryRecursive(path string) string {
	matches, err := activeBackend.GlobFiles(path, "**")
	if err != nil {
		return "Error: " + err.Error()
	}
	if len(matches) == 0 {
		return fmt.Sprintf("%s — (empty)", path)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d entries (recursive):\n", path, len(matches))
	for _, m := range matches {
		sb.WriteString(m)
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}
