package tools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode"

	"crude/diff"
	"crude/docconv"
	"crude/fileio"
	"crude/js"
	"crude/logger"
	"crude/provider"
	"crude/search"
)

// readFileHint wraps a read_file error with a helpful hint when the file
// is not found — this guides less experienced LLMs toward using list_directory
// to discover actual filenames instead of guessing them.
func readFileHint(err error, path string) string {
	msg := "Error: " + err.Error()
	if errors.Is(err, fs.ErrNotExist) {
		msg += "\n\nHint: file not found. Use list_directory on the parent directory to discover actual filenames — package directories often contain multiple files with different names."
	}
	return msg
}

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

type ToolResult struct {
	Content     string
	Added       int
	Removed     int
	Attachments []provider.ContentPart
}

func (r ToolResult) String() string {
	return r.Content
}

// ToolOptions carries optional per-session capability context for the
// dispatcher. The zero value preserves legacy behavior exactly.
type ToolOptions struct {
	// Media describes the active model's media attachment policy for
	// read_file. nil keeps binary reads refused.
	Media *MediaReadPolicy
	// PrepareMutation receives an exact receipt before an Editor mutation.
	PrepareMutation fileio.MutationPrepare
}

func mutationPreparer(opts *ToolOptions) fileio.MutationPrepare {
	if opts == nil {
		return nil
	}
	return opts.PrepareMutation
}

func ExecuteWithJSON(tool string, params map[string]any, exec fileio.Executer, ed *fileio.Editor, yolo bool, lg *logger.Logger, ctx context.Context) ToolResult {
	return ExecuteWithJSONOptions(tool, params, exec, ed, yolo, lg, ctx, nil)
}

func ExecuteWithJSONOptions(tool string, params map[string]any, exec fileio.Executer, ed *fileio.Editor, yolo bool, lg *logger.Logger, ctx context.Context, opts *ToolOptions) ToolResult {
	start := time.Now()
	lg.Debug("DISPATCH", "route", "tool", tool, "has_editor", ed != nil)

	var result ToolResult

	switch tool {

	case "read_file":
		path := GetStrArg(params, "path", "")
		encoded := GetBoolArgAny(params, "encoded", false)
		offset := GetIntArgAny(params, "offset", 0)
		limit := GetIntArgAny(params, "limit", 0)
		lg.Info("TOOL", "call", "tool", tool, "path", path, "encoded", encoded, "offset", offset, "limit", limit)

		// Capability gate: recognised media attachments go through the
		// model's modality policy before generic binary refusal. This is the
		// only place read_file can turn a binary into a context attachment.
		if fi, statErr := os.Lstat(path); statErr == nil && fi.Mode().IsRegular() {
			if handled, mediaResult, attachments := mediaPolicyFor(opts).tryMediaAttachment(path, lg); handled {
				result = ToolResult{Content: mediaResult, Attachments: attachments}
				lg.Info("TOOL", "result", "tool", tool, "path", path, "media", true, "latency_ms", time.Since(start).Milliseconds())
				break
			}
		}
		if fi, err := os.Stat(path); err == nil && fi.IsDir() {
			entries, err := exec.ListDir(path)
			if err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			if len(entries) == 0 {
				result = ToolResult{Content: fmt.Sprintf("Directory %s is empty (no files or subdirectories). This is a successful result, not an error — the directory exists but contains nothing.", path)}
			} else {
				result = ToolResult{Content: formatDirEntries(path, entries)}
			}
			lg.Info("TOOL", "result", "tool", tool, "path", path, "redirected", "list_directory", "latency_ms", time.Since(start).Milliseconds())
			break
		}
		if ed != nil {
			if encoded {
				content, err := ed.Read(path)
				if err != nil {
					if _, ok := err.(*fileio.BinaryFileError); ok {
						conv, convOk := tryDocRead(path, offset, limit, lg)
						if convOk {
							result = ToolResult{Content: conv}
							lg.Info("TOOL", "doc_converted", "path", path)
							break
						}
						if docconv.SupportedExt(strings.ToLower(filepath.Ext(path))) {
							result = ToolResult{Content: conv}
							break
						}
						result = ToolResult{Content: fmt.Sprintf("Error: %v", err)}
						lg.Warn("TOOL", "binary_file", "path", path)
						break
					}
					result = ToolResult{Content: readFileHint(err, path)}
					lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
					break
				}
				result = ToolResult{Content: formatReadFile(path, content, offset, limit)}
			} else {
				content, err := ed.ReadRaw(path)
				if err != nil {
					if _, ok := err.(*fileio.BinaryFileError); ok {
						conv, convOk := tryDocRead(path, offset, limit, lg)
						if convOk {
							result = ToolResult{Content: conv}
							lg.Info("TOOL", "doc_converted", "path", path)
							break
						}
						if docconv.SupportedExt(strings.ToLower(filepath.Ext(path))) {
							result = ToolResult{Content: conv}
							break
						}
						result = ToolResult{Content: fmt.Sprintf("Error: %v", err)}
						lg.Warn("TOOL", "binary_file", "path", path)
						break
					}
					result = ToolResult{Content: readFileHint(err, path)}
					lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
					break
				}
				result = ToolResult{Content: formatReadFile(path, content, offset, limit)}
			}
		} else {
			path := GetStrArg(params, "path", "")
			ext := strings.ToLower(filepath.Ext(path))
			if docconv.SupportedExt(ext) {
				conv, convOk := tryDocRead(path, offset, limit, lg)
				if convOk {
					result = ToolResult{Content: conv}
					lg.Info("TOOL", "doc_converted", "path", path)
					break
				}
				result = ToolResult{Content: conv}
				break
			}
			data, err := os.ReadFile(path)
			if err != nil {
				result = ToolResult{Content: readFileHint(err, path)}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			if fileio.IsBinary(data) {
				conv, convOk := tryDocRead(path, offset, limit, lg)
				if convOk {
					result = ToolResult{Content: conv}
					lg.Info("TOOL", "doc_converted", "path", path)
					break
				}
				if docconv.SupportedExt(strings.ToLower(filepath.Ext(path))) {
					result = ToolResult{Content: conv}
					break
				}
				result = ToolResult{Content: fmt.Sprintf("Error: binary file detected: %s (%d bytes) — cannot read binary files, use execute_command instead", path, len(data))}
				lg.Warn("TOOL", "binary_file", "path", path)
				break
			}
			result = ToolResult{Content: formatReadFile(path, string(data), offset, limit)}
		}
		lg.Info("TOOL", "result", "tool", tool, "path", path, "latency_ms", time.Since(start).Milliseconds())

	case "write_file":
		content := GetStrArg(params, "content", "")
		path := GetStrArg(params, "path", "")
		lg.Info("TOOL", "call", "tool", tool, "path", path, "content_len", len(content))
		if content != "" && len([]rune(content)) > 200 {
			lg.Debug("DISPATCH", "content_preview", "first_80", string([]rune(content)[:80])+"…")
		}
		oldContent := ""
		existing, err := os.ReadFile(path)
		if err == nil {
			oldContent = string(existing)
		}
		if ed != nil {
			if err := ed.WriteTracked(path, content, mutationPreparer(opts)); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					result = ToolResult{Content: fmt.Sprintf("Error: %v", err)}
					lg.Warn("TOOL", "binary_file", "path", path)
					break
				}
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			lines := strings.Count(content, "\n") + 1
			result = formatWriteResult(path, lines, oldContent, content)
		} else {
			if mutationPreparer(opts) != nil {
				result = ToolResult{Content: "Error: mutation receipt requires an editor"}
				break
			}
			if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			if err := fileio.WriteFile(path, []byte(content), 0o644); err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			lines := strings.Count(content, "\n") + 1
			result = formatWriteResult(path, lines, oldContent, content)
		}
		lg.Info("TOOL", "result", "tool", tool, "path", path, "latency_ms", time.Since(start).Milliseconds())

	case "edit_file":
		path := GetStrArg(params, "path", "")
		oldString := GetStrArg(params, "old_string", "")
		newString := GetStrArg(params, "new_string", "")
		lg.Info("TOOL", "call", "tool", tool, "path", path, "old_len", len(oldString), "new_len", len(newString))
		if len([]rune(oldString)) > 80 {
			lg.Debug("DISPATCH", "old_preview", "first_80", string([]rune(oldString)[:80])+"…")
		}
		orig := ""
		if ed != nil {
			origData, _ := ed.ReadRaw(path)
			orig = origData
			ops := []fileio.SearchReplace{{Search: oldString, Replace: newString}}
			if err := ed.PatchTracked(path, ops, mutationPreparer(opts)); err != nil {
				if _, ok := err.(*fileio.BinaryFileError); ok {
					result = ToolResult{Content: fmt.Sprintf("Error: %v", err)}
					break
				}
				result = ToolResult{Content: fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			newData, _ := os.ReadFile(path)
			replaced := string(newData)
			result = formatEditResult(path, 1, orig, replaced)
		} else {
			if mutationPreparer(opts) != nil {
				result = ToolResult{Content: "Error: mutation receipt requires an editor"}
				break
			}
			data, err := os.ReadFile(path)
			if err != nil {
				result = ToolResult{Content: fmt.Sprintf("Error: %v\nHint: use read_file to verify the exact content before retrying.", err)}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			orig = string(data)
			count := strings.Count(orig, oldString)
			if count == 0 {
				result = ToolResult{Content: fmt.Sprintf("Error: old_string not found in %s\nHint: use read_file to verify the exact content before retrying.", path)}
				lg.Warn("TOOL", "old_string_not_found", "path", path)
				break
			}
			replaced := strings.ReplaceAll(orig, oldString, newString)
			if err := fileio.WriteFile(path, []byte(replaced), 0o644); err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			result = formatEditResult(path, count, orig, replaced)
		}
		lg.Info("TOOL", "result", "tool", tool, "path", path, "latency_ms", time.Since(start).Milliseconds())

	case "delete_file":
		path := GetStrArg(params, "path", "")
		lg.Info("TOOL", "call", "tool", tool, "path", path)
		if ed != nil {
			if err := ed.DeleteTracked(path, mutationPreparer(opts)); err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			result = ToolResult{Content: "Deleted: " + path}
		} else {
			if mutationPreparer(opts) != nil {
				result = ToolResult{Content: "Error: mutation receipt requires an editor"}
				break
			}
			unlock := fileio.LockTreeMutation()
			err := os.Remove(path)
			unlock()
			if err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			result = ToolResult{Content: "Deleted: " + path}
		}
		lg.Info("TOOL", "result", "tool", tool, "path", path, "latency_ms", time.Since(start).Milliseconds())

	case "move_file":
		source := GetStrArg(params, "source", "")
		dest := GetStrArg(params, "destination", "")
		lg.Info("TOOL", "call", "tool", tool, "source", source, "destination", dest)
		if ed != nil {
			if err := ed.MoveTracked(source, dest, mutationPreparer(opts)); err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			result = ToolResult{Content: fmt.Sprintf("Moved %s → %s", source, dest)}
		} else {
			if mutationPreparer(opts) != nil {
				result = ToolResult{Content: "Error: mutation receipt requires an editor"}
				break
			}
			unlock := fileio.LockTreeMutation()
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				unlock()
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			if err := os.Rename(source, dest); err != nil {
				unlock()
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			unlock()
			result = ToolResult{Content: fmt.Sprintf("Moved %s → %s", source, dest)}
		}
		lg.Info("TOOL", "result", "tool", tool, "source", source, "destination", dest, "latency_ms", time.Since(start).Milliseconds())

	case "copy_file":
		source := GetStrArg(params, "source", "")
		dest := GetStrArg(params, "destination", "")
		lg.Info("TOOL", "call", "tool", tool, "source", source, "destination", dest)
		if ed != nil {
			if err := ed.CopyTracked(source, dest, mutationPreparer(opts)); err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			result = ToolResult{Content: fmt.Sprintf("Copied %s → %s", source, dest)}
		} else {
			if mutationPreparer(opts) != nil {
				result = ToolResult{Content: "Error: mutation receipt requires an editor"}
				break
			}
			unlock := fileio.LockTreeMutation()
			in, err := os.Open(source)
			if err != nil {
				unlock()
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			defer in.Close()
			if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
				unlock()
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			out, err := os.Create(dest)
			if err != nil {
				unlock()
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			defer out.Close()
			if _, err := io.Copy(out, in); err != nil {
				unlock()
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			unlock()
			result = ToolResult{Content: fmt.Sprintf("Copied %s → %s", source, dest)}
		}
		lg.Info("TOOL", "result", "tool", tool, "source", source, "destination", dest, "latency_ms", time.Since(start).Milliseconds())

	case "list_directory":
		path := GetStrArg(params, "path", "")
		recursive := GetBoolArgAny(params, "recursive", false)
		lg.Info("TOOL", "call", "tool", tool, "path", path, "recursive", recursive)
		if recursive {
			result = ToolResult{Content: listDirectoryRecursive(path, exec)}
		} else {
			entries, err := exec.ListDir(path)
			if err != nil {
				result = ToolResult{Content: "Error: " + err.Error()}
				lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
				break
			}
			if len(entries) == 0 {
				result = ToolResult{Content: fmt.Sprintf("Directory %s is empty (no files or subdirectories). This is a successful result, not an error — the directory exists but contains nothing.", path)}
			} else {
				result = ToolResult{Content: formatDirEntries(path, entries)}
			}
		}
		lg.Info("TOOL", "result", "tool", tool, "path", path, "latency_ms", time.Since(start).Milliseconds())

	case "glob_files":
		pattern := GetStrArg(params, "pattern", "")
		baseDir := GetStrArg(params, "base_dir", ".")
		includeHidden := GetBoolArgAny(params, "include_hidden", false)
		lg.Info("TOOL", "call", "tool", tool, "pattern", pattern, "base_dir", baseDir, "include_hidden", includeHidden)
		matches, err := exec.GlobFiles(baseDir, pattern, includeHidden)
		if err != nil {
			result = ToolResult{Content: "Error: " + err.Error()}
			lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
			break
		}
		if len(matches) == 0 {
			result = ToolResult{Content: fmt.Sprintf("No files matching %q under %s", pattern, baseDir)}
		} else {
			var sb strings.Builder
			fmt.Fprintf(&sb, "%d file(s) matching %q under %s:\n", len(matches), pattern, baseDir)
			for _, m := range matches {
				sb.WriteString(m)
				sb.WriteByte('\n')
			}
			result = ToolResult{Content: strings.TrimRight(sb.String(), "\n")}
		}
		lg.Info("TOOL", "result", "tool", tool, "matches", len(matches), "latency_ms", time.Since(start).Milliseconds())

	case "grep_files":
		pattern := GetStrArg(params, "pattern", "")
		searchPath := GetStrArg(params, "path", ".")
		recursive := GetBoolArgAny(params, "recursive", true)
		includeHidden := GetBoolArgAny(params, "include_hidden", false)
		lg.Info("TOOL", "call", "tool", tool, "pattern", pattern, "path", searchPath, "recursive", recursive, "include_hidden", includeHidden)
		matches, err := exec.GrepFiles(pattern, searchPath, recursive, includeHidden)
		if err != nil {
			result = ToolResult{Content: "Error: " + err.Error()}
			lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
			break
		}
		if len(matches) == 0 {
			result = ToolResult{Content: fmt.Sprintf("No matches for %q in %s", pattern, searchPath)}
		} else {
			var sb strings.Builder
			fmt.Fprintf(&sb, "%d match(es) for %q in %s:\n", len(matches), pattern, searchPath)
			for _, m := range matches {
				fmt.Fprintf(&sb, "%s:%d: %s\n", m.Path, m.LineNum, m.Line)
			}
			result = ToolResult{Content: strings.TrimRight(sb.String(), "\n")}
		}
		lg.Info("TOOL", "result", "tool", tool, "matches", len(matches), "latency_ms", time.Since(start).Milliseconds())

	case "web_search":
		query := GetStrArg(params, "query", "")
		lg.Info("TOOL", "call", "tool", tool, "query_len", len(query))
		resultText, err := search.WebSearch(query, 6)
		if err != nil {
			result = ToolResult{Content: "Search error: " + err.Error()}
			lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
			break
		}
		result = ToolResult{Content: resultText}
		lg.Info("TOOL", "result", "tool", tool, "latency_ms", time.Since(start).Milliseconds())

	case "arxiv_search":
		query := GetStrArg(params, "query", "")
		maxResults := GetIntArgAny(params, "max_results", 5)
		if maxResults <= 0 || maxResults > 10 {
			maxResults = 5
		}
		lg.Info("TOOL", "call", "tool", tool, "query_len", len(query), "max_results", maxResults)
		resultText, err := search.ArxivSearch(query, maxResults)
		if err != nil {
			result = ToolResult{Content: "ArXiv search error: " + err.Error()}
			lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
			break
		}
		result = ToolResult{Content: resultText}
		lg.Info("TOOL", "result", "tool", tool, "latency_ms", time.Since(start).Milliseconds())

	case "web_fetch":
		url := GetStrArg(params, "url", "")
		offset := GetIntArgAny(params, "offset", 0)
		limit := GetIntArgAny(params, "limit", 0)
		lg.Info("TOOL", "call", "tool", tool, "url", url, "offset", offset, "limit", limit)
		resultText, err := search.WebFetch(url, offset, limit)
		if err != nil {
			result = ToolResult{Content: "Fetch error: " + err.Error()}
			lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
			break
		}
		result = ToolResult{Content: resultText}
		lg.Info("TOOL", "result", "tool", tool, "url", url, "latency_ms", time.Since(start).Milliseconds())

	case "run_javascript":
		code := GetStrArg(params, "code", "")
		lg.Info("TOOL", "call", "tool", tool, "code_len", len(code))
		resultText, err := js.Run(code)
		if err != nil {
			result = ToolResult{Content: "JavaScript error: " + sanitizeDispatcherError(err.Error())}
			lg.Warn("TOOL", "error", "tool", tool, "error", err.Error())
			break
		}
		result = ToolResult{Content: resultText}
		lg.Info("TOOL", "result", "tool", tool, "latency_ms", time.Since(start).Milliseconds())

	case "reply":
		content := GetStrArg(params, "content", "")
		lg.Info("TOOL", "call", "tool", tool, "content_len", len(content))
		result = ToolResult{Content: content}
		lg.Info("TOOL", "result", "tool", tool, "content_len", len(content), "latency_ms", time.Since(start).Milliseconds())

	default:
		lg.Error("TOOL", "unknown_tool", "tool", tool)
		result = ToolResult{Content: "Error: unknown tool: " + tool}
	}

	return result
}

func sanitizeDispatcherError(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsControl(r) {
			b.WriteString(" ")
		} else {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// tryDocRead attempts to convert a binary document file to Markdown
// and format it for display (with offset/limit support, line-based).
// Returns the formatted content and true if the conversion succeeded,
// or ("", false) if the format is not supported or conversion failed.
func tryDocRead(path string, offset, limit int, lg *logger.Logger) (string, bool) {
	ext := strings.ToLower(filepath.Ext(path))
	if !docconv.SupportedExt(ext) {
		return "", false
	}

	formatName := docconv.FormatName(ext)
	lg.Info("DOCONV", "converting", "path", path, "format", formatName)
	md, meta, err := docconv.ConvertFile(path)
	if err != nil {
		lg.Warn("DOCONV", "conversion_failed", "path", path, "error", err.Error())
		return fmt.Sprintf("Error: %s file detected but conversion failed: %s — the file may be corrupted or image-only", formatName, err.Error()), false
	}

	// Build full output: header line + blank line + markdown content
	var sb strings.Builder
	sb.WriteString(docconv.Header(path, meta))
	sb.WriteString(md)
	full := sb.String()

	// Apply line-based offset/limit (same model as formatReadFile)
	lines := strings.Split(full, "\n")
	total := len(lines)

	startIdx := 0
	if offset > 1 {
		startIdx = offset - 1
	}
	if startIdx >= total {
		if total == 0 {
			return "", false
		}
		startIdx = total - 1
	}

	endIdx := total
	if limit > 0 && startIdx+limit < total {
		endIdx = startIdx + limit
	}

	slice := lines[startIdx:endIdx]
	var out strings.Builder
	if offset > 1 || limit > 0 {
		fmt.Fprintf(&out, "Lines %d–%d of converted document (%d total)\n\n", startIdx+1, endIdx, total)
	}
	out.WriteString(strings.Join(slice, "\n"))
	return strings.TrimRight(out.String(), "\n"), true
}

func formatWriteResult(path string, lines int, oldContent, newContent string) ToolResult {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Written %d lines to %s\n", lines, path)
	diffText, additions, removals := diff.GenerateDiff(oldContent, newContent, path)
	diffText = strings.TrimSpace(diffText)
	if !diff.IsEmptyDiff(diffText) {
		sb.WriteString("\n```diff\n")
		sb.WriteString(diffText)
		sb.WriteString("\n```\n")
	}
	return ToolResult{Content: sb.String(), Added: additions, Removed: removals}
}

func formatEditResult(path string, count int, oldContent, newContent string) ToolResult {
	var sb strings.Builder
	if count == 1 {
		fmt.Fprintf(&sb, "Replaced 1 occurrence in %s\n", path)
	} else {
		fmt.Fprintf(&sb, "Replaced %d occurrences in %s\n", count, path)
	}
	diffText, additions, removals := diff.GenerateDiff(oldContent, newContent, path)
	diffText = strings.TrimSpace(diffText)
	if !diff.IsEmptyDiff(diffText) {
		sb.WriteString("\n```diff\n")
		sb.WriteString(diffText)
		sb.WriteString("\n```\n")
	}
	return ToolResult{Content: sb.String(), Added: additions, Removed: removals}
}
