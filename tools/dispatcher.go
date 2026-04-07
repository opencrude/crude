package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"crude/backend"
	"crude/js"
	"crude/search"
)

// Execute dispatches a tool call to the given backend executor and returns the result string.
func Execute(tc ToolCall, exec backend.Executor) string {
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
		out, err := exec.Execute(args.Command, opts)
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
		content, err := exec.ReadFile(args.Path)
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
		if err := exec.WriteFile(args.Path, args.Content); err != nil {
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
		n, err := exec.EditFile(args.Path, args.OldString, args.NewString)
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
		if err := exec.DeleteFile(args.Path); err != nil {
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
		if err := exec.MoveFile(args.Source, args.Destination); err != nil {
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
		if err := exec.CopyFile(args.Source, args.Destination); err != nil {
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
			return listDirectoryRecursive(args.Path, exec)
		}
		entries, err := exec.ListDir(args.Path)
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
		matches, err := exec.GlobFiles(args.BaseDir, args.Pattern)
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
		matches, err := exec.GrepFiles(args.Pattern, args.Path, recursive)
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
