package tools

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"crude/diff"
	"crude/fileio"
	"crude/js"
	"crude/search"
)

func getStrArg(args map[string]any, key string, defaultVal string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return defaultVal
}

func getIntArgAny(args map[string]any, key string, defaultVal int) int {
	if v, ok := args[key]; ok {
		switch val := v.(type) {
		case float64:
			return int(val)
		case int:
			return val
		case string:
			if val == "" {
				return defaultVal
			}
			n, err := strconv.Atoi(strings.TrimSpace(val))
			if err != nil {
				return defaultVal
			}
			return n
		}
	}
	return defaultVal
}

func getBoolArgAny(args map[string]any, key string, defaultVal bool) bool {
	if v, ok := args[key]; ok {
		switch val := v.(type) {
		case bool:
			return val
		case string:
			switch strings.ToLower(strings.TrimSpace(val)) {
			case "true", "1", "yes":
				return true
			case "false", "0", "no":
				return false
			}
		}
	}
	return defaultVal
}

func ExecuteWithJSON(tool string, params map[string]any, exec fileio.Executer, ed *fileio.Editor) string {
	switch tool {

	case "execute_command":
		opts := fileio.ExecOptions{
			CWD:            getStrArg(params, "cwd", ""),
			TimeoutSeconds: getIntArgAny(params, "timeout_seconds", 0),
		}
		out, err := exec.Execute(getStrArg(params, "command", ""), opts)
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
			encoded := getBoolArgAny(params, "encoded", false)
			if encoded {
				content, err := ed.Read(getStrArg(params, "path", ""))
				if err != nil {
					if _, ok := err.(*fileio.BinaryFileError); ok {
						return fmt.Sprintf("Error: %v", err)
					}
					return "Error: " + err.Error()
				}
				return formatReadFile(getStrArg(params, "path", ""), content, getIntArgAny(params, "offset", 0), getIntArgAny(params, "limit", 0))
			}
			content, err := ed.ReadRaw(getStrArg(params, "path", ""))
			if err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return "Error: " + err.Error()
			}
			return formatReadFile(getStrArg(params, "path", ""), content, getIntArgAny(params, "offset", 0), getIntArgAny(params, "limit", 0))
		}
		data, err := os.ReadFile(getStrArg(params, "path", ""))
		if err != nil {
			return "Error: " + err.Error()
		}
		return formatReadFile(getStrArg(params, "path", ""), string(data), getIntArgAny(params, "offset", 0), getIntArgAny(params, "limit", 0))

	case "write_file":
		content := getStrArg(params, "content", "")
		path := getStrArg(params, "path", "")
		oldContent := ""
		existing, err := os.ReadFile(path)
		if err == nil {
			oldContent = string(existing)
		}
		if ed != nil {
			if err := ed.Write(path, content); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return "Error: " + err.Error()
			}
			lines := strings.Count(content, "\n") + 1
			return formatWriteResult(path, lines, oldContent, content)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			return "Error: " + err.Error()
		}
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			return "Error: " + err.Error()
		}
		lines := strings.Count(content, "\n") + 1
		return formatWriteResult(path, lines, oldContent, content)

	case "edit_file":
		path := getStrArg(params, "path", "")
		oldString := getStrArg(params, "old_string", "")
		newString := getStrArg(params, "new_string", "")
		orig := ""
		if ed != nil {
			origData, _ := ed.ReadRaw(path)
			orig = origData
			ops := []fileio.SearchReplace{{Search: oldString, Replace: newString}}
			if err := ed.Patch(path, ops); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
			}
			newData, _ := os.ReadFile(path)
			replaced := string(newData)
			count := strings.Count(replaced, newString)
			return formatEditResult(path, count, orig, replaced)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
		}
		orig = string(data)
		count := strings.Count(orig, oldString)
		if count == 0 {
			return fmt.Sprintf("Error: old_string not found in %s\nHint: use read_file to verify the exact content before retrying.", path)
		}
		replaced := strings.ReplaceAll(orig, oldString, newString)
		if err := os.WriteFile(path, []byte(replaced), 0644); err != nil {
			return "Error: " + err.Error()
		}
		return formatEditResult(path, count, orig, replaced)

	case "delete_file":
		path := getStrArg(params, "path", "")
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
		source := getStrArg(params, "source", "")
		dest := getStrArg(params, "destination", "")
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
		source := getStrArg(params, "source", "")
		dest := getStrArg(params, "destination", "")
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
		path := getStrArg(params, "path", "")
		recursive := getBoolArgAny(params, "recursive", false)
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
		pattern := getStrArg(params, "pattern", "")
		baseDir := getStrArg(params, "base_dir", ".")
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
		pattern := getStrArg(params, "pattern", "")
		searchPath := getStrArg(params, "path", ".")
		recursive := getBoolArgAny(params, "recursive", true)
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
		result, err := search.WebSearch(getStrArg(params, "query", ""), 6)
		if err != nil {
			return "Search error: " + err.Error()
		}
		return result

	case "arxiv_search":
		maxResults := getIntArgAny(params, "max_results", 5)
		if maxResults <= 0 || maxResults > 10 {
			maxResults = 5
		}
		result, err := search.ArxivSearch(getStrArg(params, "query", ""), maxResults)
		if err != nil {
			return "ArXiv search error: " + err.Error()
		}
		return result

	case "web_fetch":
		maxChars := getIntArgAny(params, "max_chars", 0)
		result, err := search.WebFetch(getStrArg(params, "url", ""), maxChars)
		if err != nil {
			return "Fetch error: " + err.Error()
		}
		return result

	case "run_javascript":
		code := getStrArg(params, "code", "")
		result, err := js.Run(code)
		if err != nil {
			return "JavaScript error: " + err.Error()
		}
		return result

	case "reply":
		return getStrArg(params, "content", "")

	default:
		return "Error: unknown tool: " + tool
	}
}

func formatWriteResult(path string, lines int, oldContent, newContent string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Written %d lines to %s\n", lines, path))
	diffText, _, _ := diff.GenerateDiff(oldContent, newContent, path)
	diffText = strings.TrimSpace(diffText)
	if !diff.IsEmptyDiff(diffText) {
		sb.WriteString("\n```diff\n")
		sb.WriteString(diffText)
		sb.WriteString("\n```\n")
	}
	return sb.String()
}

func formatEditResult(path string, count int, oldContent, newContent string) string {
	var sb strings.Builder
	if count == 1 {
		sb.WriteString(fmt.Sprintf("Replaced 1 occurrence in %s\n", path))
	} else {
		sb.WriteString(fmt.Sprintf("Replaced %d occurrences in %s\n", count, path))
	}
	diffText, _, _ := diff.GenerateDiff(oldContent, newContent, path)
	diffText = strings.TrimSpace(diffText)
	if !diff.IsEmptyDiff(diffText) {
		sb.WriteString("\n```diff\n")
		sb.WriteString(diffText)
		sb.WriteString("\n```\n")
	}
	return sb.String()
}


