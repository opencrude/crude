package tools

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"crude/fileio"
	"crude/js"
	"crude/search"
)

func Execute(tc ToolCall, exec fileio.Executer, ed *fileio.Editor) string {
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
		opts := fileio.ExecOptions{CWD: args.CWD, TimeoutSeconds: args.Timeout}
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
			Path    string `json:"path"`
			Offset  int    `json:"offset"`
			Limit   int    `json:"limit"`
			Encoded bool   `json:"encoded"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if ed != nil {
			if args.Encoded {
				content, err := ed.Read(args.Path)
				if err != nil {
					if _, ok := err.(*fileio.BinaryFileError); ok {
						return fmt.Sprintf("Error: %v", err)
					}
					return "Error: " + err.Error()
				}
				return formatReadFile(args.Path, content, args.Offset, args.Limit)
			}
			content, err := ed.ReadRaw(args.Path)
			if err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return "Error: " + err.Error()
			}
			return formatReadFile(args.Path, content, args.Offset, args.Limit)
		}
		data, err := os.ReadFile(args.Path)
		if err != nil {
			return "Error: " + err.Error()
		}
		return formatReadFile(args.Path, string(data), args.Offset, args.Limit)

	case "write_file":
		var args struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if ed != nil {
			if err := ed.Write(args.Path, args.Content); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return "Error: " + err.Error()
			}
			lines := strings.Count(args.Content, "\n") + 1
			return fmt.Sprintf("Written %d lines to %s", lines, args.Path)
		}
		if err := os.MkdirAll(filepath.Dir(args.Path), 0755); err != nil {
			return "Error: " + err.Error()
		}
		if err := os.WriteFile(args.Path, []byte(args.Content), 0644); err != nil {
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
		if ed != nil {
			ops := []fileio.SearchReplace{{Search: args.OldString, Replace: args.NewString}}
			if err := ed.Patch(args.Path, ops); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
			}
			return fmt.Sprintf("Replaced 1 occurrence in %s", args.Path)
		}
		data, err := os.ReadFile(args.Path)
		if err != nil {
			return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
		}
		orig := string(data)
		count := strings.Count(orig, args.OldString)
		if count == 0 {
			return fmt.Sprintf("Error: old_string not found in %s\nHint: use read_file to verify the exact content before retrying.", args.Path)
		}
		replaced := strings.ReplaceAll(orig, args.OldString, args.NewString)
		if err := os.WriteFile(args.Path, []byte(replaced), 0644); err != nil {
			return "Error: " + err.Error()
		}
		if count == 1 {
			return fmt.Sprintf("Replaced 1 occurrence in %s", args.Path)
		}
		return fmt.Sprintf("Replaced %d occurrences in %s", count, args.Path)

	case "delete_file":
		var args struct {
			Path string `json:"path"`
		}
		if err := json.Unmarshal([]byte(tc.Function.Arguments), &args); err != nil {
			return "Error: invalid arguments: " + err.Error()
		}
		if ed != nil {
			if err := ed.Delete(args.Path); err != nil {
				return "Error: " + err.Error()
			}
			return "Deleted: " + args.Path
		}
		if err := os.Remove(args.Path); err != nil {
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
		if ed != nil {
			if err := ed.Move(args.Source, args.Destination); err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Moved %s → %s", args.Source, args.Destination)
		}
		if err := os.MkdirAll(filepath.Dir(args.Destination), 0755); err != nil {
			return "Error: " + err.Error()
		}
		if err := os.Rename(args.Source, args.Destination); err != nil {
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
		if ed != nil {
			if err := ed.Copy(args.Source, args.Destination); err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Copied %s → %s", args.Source, args.Destination)
		}
		in, err := os.Open(args.Source)
		if err != nil {
			return "Error: " + err.Error()
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(args.Destination), 0755); err != nil {
			return "Error: " + err.Error()
		}
		out, err := os.Create(args.Destination)
		if err != nil {
			return "Error: " + err.Error()
		}
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
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
