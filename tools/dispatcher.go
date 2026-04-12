package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"crude/fileio"
	"crude/js"
	"crude/search"
	"crude/xmlcodec"
)

// parseArgs extracts tool arguments from XML. The LLM provides XML in FunctionCall.Arguments.
// Returns a map[string]string of parameter name → value.
func parseArgs(argsXML string) (map[string]string, error) {
	return xmlcodec.ParseParams(argsXML)
}

// getArg retrieves a named argument from the parsed XML args, with an optional default.
func getArg(args map[string]string, key string, defaultVal string) string {
	if v, ok := args[key]; ok {
		return v
	}
	return defaultVal
}

// getIntArg parses an integer argument, returning the default if missing or invalid.
func getIntArg(args map[string]string, key string, defaultVal int) int {
	s := getArg(args, key, "")
	if s == "" {
		return defaultVal
	}
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return defaultVal
	}
	return n
}

// getBoolArg parses a boolean argument, returning the default if missing or invalid.
func getBoolArg(args map[string]string, key string, defaultVal bool) bool {
	s := getArg(args, key, "")
	if s == "" {
		return defaultVal
	}
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "1", "yes":
		return true
	case "false", "0", "no":
		return false
	default:
		return defaultVal
	}
}

func Execute(tc ToolCall, exec fileio.Executer, ed *fileio.Editor) string {
	args, parseErr := parseArgs(tc.Function.Arguments)
	if parseErr != nil {
		return "Error: invalid XML arguments: " + parseErr.Error()
	}

	switch tc.Function.Name {

	case "execute_command":
		opts := fileio.ExecOptions{
			CWD:            getArg(args, "cwd", ""),
			TimeoutSeconds: getIntArg(args, "timeout_seconds", 0),
		}
		out, err := exec.Execute(getArg(args, "command", ""), opts)
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
		if ed != nil {
			encoded := getBoolArg(args, "encoded", false)
			if encoded {
				content, err := ed.Read(getArg(args, "path", ""))
				if err != nil {
					if _, ok := err.(*fileio.BinaryFileError); ok {
						return fmt.Sprintf("Error: %v", err)
					}
					return "Error: " + err.Error()
				}
				return formatReadFile(getArg(args, "path", ""), content, getIntArg(args, "offset", 0), getIntArg(args, "limit", 0))
			}
			content, err := ed.ReadRaw(getArg(args, "path", ""))
			if err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return "Error: " + err.Error()
			}
			return formatReadFile(getArg(args, "path", ""), content, getIntArg(args, "offset", 0), getIntArg(args, "limit", 0))
		}
		data, err := os.ReadFile(getArg(args, "path", ""))
		if err != nil {
			return "Error: " + err.Error()
		}
		return formatReadFile(getArg(args, "path", ""), string(data), getIntArg(args, "offset", 0), getIntArg(args, "limit", 0))

	case "write_file":
		content := getArg(args, "content", "")
		path := getArg(args, "path", "")
		if ed != nil {
			if err := ed.Write(path, content); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return "Error: " + err.Error()
			}
			lines := strings.Count(content, "\n") + 1
			return fmt.Sprintf("Written %d lines to %s", lines, path)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "Error: " + err.Error()
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "Error: " + err.Error()
		}
		lines := strings.Count(content, "\n") + 1
		return fmt.Sprintf("Written %d lines to %s", lines, path)

	case "edit_file":
		path := getArg(args, "path", "")
		oldString := getArg(args, "old_string", "")
		newString := getArg(args, "new_string", "")
		if ed != nil {
			ops := []fileio.SearchReplace{{Search: oldString, Replace: newString}}
			if err := ed.Patch(path, ops); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
			}
			return fmt.Sprintf("Replaced 1 occurrence in %s", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
		}
		orig := string(data)
		count := strings.Count(orig, oldString)
		if count == 0 {
			return fmt.Sprintf("Error: old_string not found in %s\nHint: use read_file to verify the exact content before retrying.", path)
		}
		replaced := strings.ReplaceAll(orig, oldString, newString)
		if err := os.WriteFile(path, []byte(replaced), 0644); err != nil {
			return "Error: " + err.Error()
		}
		if count == 1 {
			return fmt.Sprintf("Replaced 1 occurrence in %s", path)
		}
		return fmt.Sprintf("Replaced %d occurrences in %s", count, path)

	case "delete_file":
		path := getArg(args, "path", "")
		if ed != nil {
			if err := ed.Delete(path); err != nil {
				return "Error: " + err.Error()
			}
			return "Deleted: " + path
		}
		if err := os.Remove(path); err != nil {
			return "Error: " + err.Error()
		}
		return "Deleted: " + path

	case "move_file":
		source := getArg(args, "source", "")
		dest := getArg(args, "destination", "")
		if ed != nil {
			if err := ed.Move(source, dest); err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Moved %s → %s", source, dest)
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return "Error: " + err.Error()
		}
		if err := os.Rename(source, dest); err != nil {
			return "Error: " + err.Error()
		}
		return fmt.Sprintf("Moved %s → %s", source, dest)

	case "copy_file":
		source := getArg(args, "source", "")
		dest := getArg(args, "destination", "")
		if ed != nil {
			if err := ed.Copy(source, dest); err != nil {
				return "Error: " + err.Error()
			}
			return fmt.Sprintf("Copied %s → %s", source, dest)
		}
		in, err := os.Open(source)
		if err != nil {
			return "Error: " + err.Error()
		}
		defer in.Close()
		if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
			return "Error: " + err.Error()
		}
		out, err := os.Create(dest)
		if err != nil {
			return "Error: " + err.Error()
		}
		defer out.Close()
		if _, err := io.Copy(out, in); err != nil {
			return "Error: " + err.Error()
		}
		return fmt.Sprintf("Copied %s → %s", source, dest)

	case "list_directory":
		path := getArg(args, "path", "")
		recursive := getBoolArg(args, "recursive", false)
		if recursive {
			return listDirectoryRecursive(path, exec)
		}
		entries, err := exec.ListDir(path)
		if err != nil {
			return "Error: " + err.Error()
		}
		if len(entries) == 0 {
			return fmt.Sprintf("Directory %s is empty (no files or subdirectories). This is a successful result, not an error — the directory exists but contains nothing.", path)
		}
		return formatDirEntries(path, entries)

	case "glob_files":
		pattern := getArg(args, "pattern", "")
		baseDir := getArg(args, "base_dir", ".")
		matches, err := exec.GlobFiles(baseDir, pattern)
		if err != nil {
			return "Error: " + err.Error()
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No files matching %q under %s", pattern, baseDir)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d file(s) matching %q under %s:\n", len(matches), pattern, baseDir)
		for _, m := range matches {
			sb.WriteString(m)
			sb.WriteByte('\n')
		}
		return strings.TrimRight(sb.String(), "\n")

	case "grep_files":
		pattern := getArg(args, "pattern", "")
		searchPath := getArg(args, "path", ".")
		recursive := getBoolArg(args, "recursive", true)
		matches, err := exec.GrepFiles(pattern, searchPath, recursive)
		if err != nil {
			return "Error: " + err.Error()
		}
		if len(matches) == 0 {
			return fmt.Sprintf("No matches for %q in %s", pattern, searchPath)
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "%d match(es) for %q in %s:\n", len(matches), pattern, searchPath)
		for _, m := range matches {
			fmt.Fprintf(&sb, "%s:%d: %s\n", m.Path, m.LineNum, m.Line)
		}
		return strings.TrimRight(sb.String(), "\n")

	case "web_search":
		result, err := search.WebSearch(getArg(args, "query", ""), 6)
		if err != nil {
			return "Search error: " + err.Error()
		}
		return result

	case "arxiv_search":
		maxResults := getIntArg(args, "max_results", 5)
		if maxResults <= 0 || maxResults > 10 {
			maxResults = 5
		}
		result, err := search.ArxivSearch(getArg(args, "query", ""), maxResults)
		if err != nil {
			return "ArXiv search error: " + err.Error()
		}
		return result

	case "web_fetch":
		maxChars := getIntArg(args, "max_chars", 0)
		result, err := search.WebFetch(getArg(args, "url", ""), maxChars)
		if err != nil {
			return "Fetch error: " + err.Error()
		}
		return result

	case "run_javascript":
		code := getArg(args, "code", "")
		result, err := js.Run(code)
		if err != nil {
			return "JavaScript error: " + err.Error()
		}
		return result

	case "reply":
		return getArg(args, "content", "")

	default:
		return "Error: unknown tool: " + tc.Function.Name
	}
}
