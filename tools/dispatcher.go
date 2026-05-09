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
	"crude/safety"
	"crude/search"
)

func GetStrArg(args map[string]any, key string, defaultVal string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprintf("%v", v)
	}
	return defaultVal
}

func GetIntArgAny(args map[string]any, key string, defaultVal int) int {
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

func GetBoolArgAny(args map[string]any, key string, defaultVal bool) bool {
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

func GetFloatArgAny(args map[string]any, key string, defaultVal float64) float64 {
	if v, ok := args[key]; ok {
		switch val := v.(type) {
		case float64:
			return val
		case int:
			return float64(val)
		case string:
			n, err := strconv.ParseFloat(strings.TrimSpace(val), 64)
			if err != nil {
				return defaultVal
			}
			return n
		}
	}
	return defaultVal
}

func ExecuteWithJSON(tool string, params map[string]any, exec fileio.Executer, ed *fileio.Editor, yolo bool) string {
	switch tool {

	case "execute_command":
		cmd := GetStrArg(params, "command", "")
		if !yolo {
			safetyResult := safety.CheckCommandSafety(cmd)
			if safetyResult.Risk.IsAlwaysBlocked() {
				return fmt.Sprintf("Error: [BLOCKED] %s\nSuggestion: %s",
					safetyResult.Reason, safetyResult.Suggestion)
			}
		}
		timeout := GetIntArgAny(params, "timeout_seconds", -1)
		if timeout < 0 {
			timeout = 60
		}
		opts := fileio.ExecOptions{
			CWD:            GetStrArg(params, "cwd", ""),
			TimeoutSeconds: timeout,
		}
		out, err := exec.Execute(cmd, opts)
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
			encoded := GetBoolArgAny(params, "encoded", false)
			if encoded {
				content, err := ed.Read(GetStrArg(params, "path", ""))
				if err != nil {
					if _, ok := err.(*fileio.BinaryFileError); ok {
						return fmt.Sprintf("Error: %v", err)
					}
					return "Error: " + err.Error()
				}
				return formatReadFile(GetStrArg(params, "path", ""), content, GetIntArgAny(params, "offset", 0), GetIntArgAny(params, "limit", 0))
			}
			content, err := ed.ReadRaw(GetStrArg(params, "path", ""))
			if err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return "Error: " + err.Error()
			}
			return formatReadFile(GetStrArg(params, "path", ""), content, GetIntArgAny(params, "offset", 0), GetIntArgAny(params, "limit", 0))
		}
		path := GetStrArg(params, "path", "")
		data, err := os.ReadFile(path)
		if err != nil {
			return "Error: " + err.Error()
		}
		if fileio.IsBinary(data) {
			return fmt.Sprintf("Error: binary file detected: %s (%d bytes) — cannot read binary files, use execute_command instead", path, len(data))
		}
		return formatReadFile(path, string(data), GetIntArgAny(params, "offset", 0), GetIntArgAny(params, "limit", 0))

	case "write_file":
		content := GetStrArg(params, "content", "")
		path := GetStrArg(params, "path", "")
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
		path := GetStrArg(params, "path", "")
		oldString := GetStrArg(params, "old_string", "")
		newString := GetStrArg(params, "new_string", "")
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
			return formatEditResult(path, 1, orig, replaced)
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

	case "delete_lines":
		path := GetStrArg(params, "path", "")
		prefix := GetStrArg(params, "prefix", "")
		suffix := GetStrArg(params, "suffix", "")
		orig := ""
		if ed != nil {
			origData, _ := ed.ReadRaw(path)
			orig = origData
			if err := ed.DeleteLines(path, prefix, suffix); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content at the line boundaries before retrying.", err)
			}
			newData, _ := os.ReadFile(path)
			replaced := string(newData)
			return formatEditResult(path, 1, orig, replaced)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
		}
		orig = string(data)
		newContent, err := fileio.DeleteLines(orig, prefix, suffix)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
			return "Error: " + err.Error()
		}
		return formatEditResult(path, 1, orig, newContent)

	case "replace_lines":
		path := GetStrArg(params, "path", "")
		prefix := GetStrArg(params, "prefix", "")
		suffix := GetStrArg(params, "suffix", "")
		newText := GetStrArg(params, "new_text", "")
		orig := ""
		if ed != nil {
			origData, _ := ed.ReadRaw(path)
			orig = origData
			if err := ed.ReplaceLines(path, prefix, suffix, newText); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					return fmt.Sprintf("Error: %v", err)
				}
				return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content at the line boundaries before retrying.", err)
			}
			newData, _ := os.ReadFile(path)
			replaced := string(newData)
			return formatEditResult(path, 1, orig, replaced)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)
		}
		orig = string(data)
		newContent, err := fileio.ReplaceLines(orig, prefix, suffix, newText)
		if err != nil {
			return fmt.Sprintf("Error: %v", err)
		}
		if err := os.WriteFile(path, []byte(newContent), 0644); err != nil {
			return "Error: " + err.Error()
		}
		return formatEditResult(path, 1, orig, newContent)

	case "delete_file":
		path := GetStrArg(params, "path", "")
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
		source := GetStrArg(params, "source", "")
		dest := GetStrArg(params, "destination", "")
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
		source := GetStrArg(params, "source", "")
		dest := GetStrArg(params, "destination", "")
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
		path := GetStrArg(params, "path", "")
		recursive := GetBoolArgAny(params, "recursive", false)
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
		pattern := GetStrArg(params, "pattern", "")
		baseDir := GetStrArg(params, "base_dir", ".")
		includeHidden := GetBoolArgAny(params, "include_hidden", false)
		matches, err := exec.GlobFiles(baseDir, pattern, includeHidden)
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
		pattern := GetStrArg(params, "pattern", "")
		searchPath := GetStrArg(params, "path", ".")
		recursive := GetBoolArgAny(params, "recursive", true)
		includeHidden := GetBoolArgAny(params, "include_hidden", false)
		matches, err := exec.GrepFiles(pattern, searchPath, recursive, includeHidden)
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
		result, err := search.WebSearch(GetStrArg(params, "query", ""), 6)
		if err != nil {
			return "Search error: " + err.Error()
		}
		return result

	case "arxiv_search":
		maxResults := GetIntArgAny(params, "max_results", 5)
		if maxResults <= 0 || maxResults > 10 {
			maxResults = 5
		}
		result, err := search.ArxivSearch(GetStrArg(params, "query", ""), maxResults)
		if err != nil {
			return "ArXiv search error: " + err.Error()
		}
		return result

	case "web_fetch":
		maxChars := GetIntArgAny(params, "max_chars", 0)
		result, err := search.WebFetch(GetStrArg(params, "url", ""), maxChars)
		if err != nil {
			return "Fetch error: " + err.Error()
		}
		return result

	case "run_javascript":
		code := GetStrArg(params, "code", "")
		result, err := js.Run(code)
		if err != nil {
			return "JavaScript error: " + err.Error()
		}
		return result

	case "reply":
		return GetStrArg(params, "content", "")

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


