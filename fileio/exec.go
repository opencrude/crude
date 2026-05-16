package fileio

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"crude/logger"
)

// windowsCommandWarning checks for common patterns that fail on Windows and
// returns a warning string that should be prepended to the command output.
func windowsCommandWarning(cmd string) string {
	lower := strings.ToLower(cmd)
	var warnings []string

	// Unix-only tools that don't exist on Windows.
	unixTools := []string{"head", "tail", "grep", "awk", "sed", "cat", "ls", "tr", "cut", "sort", "uniq", "xargs", "find", "wc", "tee", "diff", "comm"}
	for _, tool := range unixTools {
		// Look for the tool as a pipe target or at the start of the command.
		if strings.Contains(lower, "| "+tool) || strings.Contains(lower, "|"+tool) ||
			strings.HasPrefix(lower, tool+" ") || strings.HasPrefix(lower, tool+"\t") {
			warnings = append(warnings, fmt.Sprintf("'%s' is a Unix-only tool and does not exist on Windows", tool))
		}
	}

	// echo on Windows does not read stdin — piping to it silently discards data.
	if strings.Contains(lower, "| echo") || strings.Contains(lower, "|echo") {
		warnings = append(warnings, "piping to 'echo' will not work on Windows — echo does not read stdin")
	}

	if len(warnings) == 0 {
		return ""
	}
	return "[WINDOWS WARNING] " + strings.Join(warnings, "; ") + "\n\n"
}

type ExecOptions struct {
	CWD            string
	TimeoutSeconds int
}

type DirEntry struct {
	Name    string
	IsDir   bool
	IsLink  bool
	Size    int64
	ModTime time.Time
	Mode    string
}

type GrepMatch struct {
	Path    string
	LineNum int
	Line    string
}

type Executer interface {
	Execute(cmd string, opts ExecOptions) (string, error)
	ListDir(path string) ([]DirEntry, error)
	GlobFiles(baseDir, pattern string, includeHidden bool) ([]string, error)
	GrepFiles(pattern, baseDir string, recursive, includeHidden bool) ([]GrepMatch, error)
}

type LocalExec struct {
	logger *logger.Logger
}

func NewLocalExec() *LocalExec {
	return &LocalExec{}
}

func NewLocalExecWithLogger(lg *logger.Logger) *LocalExec {
	return &LocalExec{logger: lg}
}

func (l *LocalExec) Execute(cmd string, opts ExecOptions) (string, error) {
	var c *exec.Cmd

	ctx := context.Background()
	if opts.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	var warning string
	if runtime.GOOS == "windows" {
		warning = windowsCommandWarning(cmd)
		// On Windows we write the command to a temporary .cmd batch file and
		// execute that instead of passing the string directly to cmd /C.
		// This avoids cmd's first-and-last-quote-stripping behaviour which
		// mangles arguments that contain quotes (e.g. curl URLs, python -c
		// code strings, curl -w format specifiers).
		tmp, err := os.CreateTemp("", "crude_*.cmd")
		if err != nil {
			return "", fmt.Errorf("prepare windows command: %w", err)
		}
		batPath := tmp.Name()
		if _, err := tmp.WriteString("@echo off\r\n" + cmd + "\r\n"); err != nil {
			tmp.Close()
			os.Remove(batPath)
			return "", fmt.Errorf("prepare windows command: %w", err)
		}
		tmp.Close()
		defer os.Remove(batPath)
		c = exec.CommandContext(ctx, "cmd", "/C", batPath)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", cmd)
	}

	if opts.CWD != "" {
		c.Dir = opts.CWD
	}

	start := time.Now()
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	out := buf.String()
	dur := time.Since(start).Milliseconds()
	if warning != "" {
		out = warning + out
	}
	if l.logger != nil {
		if err != nil {
			exitCode := -1
			if exitErr, ok := err.(*exec.ExitError); ok {
				exitCode = exitErr.ExitCode()
			}
			l.logger.Warn("COMMAND", "exec_failed", "cmd_len", len(cmd), "timeout", opts.TimeoutSeconds, "latency_ms", dur, "out_len", len(out), "exit_code", exitCode, "cwd", opts.CWD)
		} else {
			l.logger.Info("COMMAND", "exec_ok", "cmd_len", len(cmd), "timeout", opts.TimeoutSeconds, "latency_ms", dur, "out_len", len(out), "cwd", opts.CWD)
		}
	}
	return out, err
}

func (l *LocalExec) ListDir(path string) ([]DirEntry, error) {
	if l.logger != nil {
		l.logger.Debug("COMMAND", "list_dir", "path", path)
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		if l.logger != nil {
			l.logger.Error("COMMAND", "list_dir_failed", "path", path, "error", err.Error())
		}
		return nil, err
	}
	result := make([]DirEntry, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		de := DirEntry{
			Name:    e.Name(),
			IsDir:   e.IsDir(),
			IsLink:  e.Type()&fs.ModeSymlink != 0,
			Size:    info.Size(),
			ModTime: info.ModTime(),
			Mode:    info.Mode().String(),
		}
		result = append(result, de)
	}
	if l.logger != nil {
		l.logger.Debug("COMMAND", "list_dir_ok", "path", path, "entries", len(result))
	}
	return result, nil
}

// maxGrepLineLen caps how much of a single matching line is returned.
// Minified bundles/source-maps can have megabyte-long lines.
const maxGrepLineLen = 500

// maxGrepResults caps total matches to avoid flooding context.
const maxGrepResults = 500

// skipDirs are directories we never recurse into during glob/grep.
var skipDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"vendor":        true,
	"__pycache__":   true,
	".venv":         true,
	"venv":          true,
	"dist":          true,
	"build":         true,
	".next":         true,
	"out":           true,
	"target":        true,
	".pytest_cache": true,
	".mypy_cache":   true,
	".tox":          true,
	".egg-info":     true,
	".idea":         true,
	".vs":           true,
}

// allowedHiddenDirs are hidden directories that ARE useful to search.
var allowedHiddenDirs = map[string]bool{
	".cursor": true,
	".claude": true,
	".vscode": true,
	".github": true,
	".kilo":   true,
}

func shouldSkipDir(name string, includeHidden bool) bool {
	if skipDirs[name] {
		return true
	}
	if !includeHidden && len(name) > 0 && name[0] == '.' && !allowedHiddenDirs[name] {
		return true
	}
	return false
}

func truncateLine(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}

func (l *LocalExec) GlobFiles(baseDir, pattern string, includeHidden bool) ([]string, error) {
	if baseDir == "" {
		baseDir = "."
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}

	if l.logger != nil {
		l.logger.Debug("COMMAND", "glob_start", "base_dir", baseDir, "pattern", pattern, "include_hidden", includeHidden)
	}

	start := time.Now()
	var matches []string
	err = filepath.WalkDir(absBase, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		rel, err := filepath.Rel(absBase, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		// Never skip the root directory itself — if the user points
		// directly at e.g. node_modules, they mean to search it.
		if d.IsDir() && path != absBase && shouldSkipDir(d.Name(), includeHidden) {
			return fs.SkipDir
		}
		if matchGlobPattern(pattern, filepath.ToSlash(rel)) {
			matches = append(matches, filepath.ToSlash(path))
		}
		return nil
	})
	dur := time.Since(start).Milliseconds()
	if l.logger != nil {
		if err != nil {
			l.logger.Error("COMMAND", "glob_failed", "pattern", pattern, "base_dir", baseDir, "latency_ms", dur, "error", err.Error())
		} else {
			l.logger.Info("COMMAND", "glob_ok", "pattern", pattern, "base_dir", baseDir, "matches", len(matches), "latency_ms", dur)
		}
	}
	return matches, err
}

func (l *LocalExec) GrepFiles(pattern, baseDir string, recursive, includeHidden bool) ([]GrepMatch, error) {
	if baseDir == "" {
		baseDir = "."
	}
	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}

	if l.logger != nil {
		l.logger.Debug("COMMAND", "grep_start", "pattern_len", len(pattern), "base_dir", baseDir, "recursive", recursive, "include_hidden", includeHidden)
	}

	start := time.Now()
	var results []GrepMatch

	walkFn := func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if !recursive && path != absBase {
				return fs.SkipDir
			}
			// Never skip the root directory itself.
			if path != absBase && shouldSkipDir(d.Name(), includeHidden) {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if len(results) >= maxGrepResults {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		probe := make([]byte, 512)
		n, _ := f.Read(probe)
		if bytes.IndexByte(probe[:n], 0) >= 0 {
			return nil
		}
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil
		}

		scanner := bufio.NewScanner(f)
		const maxScanTokenSize = 1024 * 1024 // 1MB
		buf := make([]byte, maxScanTokenSize)
		scanner.Buffer(buf, maxScanTokenSize)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				results = append(results, GrepMatch{
					Path:    filepath.ToSlash(path),
					LineNum: lineNum,
					Line:    truncateLine(line, maxGrepLineLen),
				})
				if len(results) >= maxGrepResults {
					break
				}
			}
		}
		return nil
	}

	if err := filepath.WalkDir(absBase, walkFn); err != nil {
		return nil, err
	}
	dur := time.Since(start).Milliseconds()
	if l.logger != nil {
		if len(results) == maxGrepResults {
			l.logger.Warn("COMMAND", "grep_truncated", "pattern_len", len(pattern), "base_dir", baseDir, "matches", len(results), "limit", maxGrepResults, "latency_ms", dur)
		} else {
			l.logger.Info("COMMAND", "grep_ok", "pattern_len", len(pattern), "base_dir", baseDir, "matches", len(results), "latency_ms", dur)
		}
	}
	return results, err
}

func matchGlobPattern(pattern, relPath string) bool {
	patParts := strings.Split(filepath.ToSlash(pattern), "/")
	relParts := strings.Split(filepath.ToSlash(relPath), "/")
	return matchSegments(patParts, relParts)
}

func matchSegments(pat, rel []string) bool {
	for len(pat) > 0 && len(rel) > 0 {
		p := pat[0]
		if p == "**" {
			for i := 0; i <= len(rel); i++ {
				if matchSegments(pat[1:], rel[i:]) {
					return true
				}
			}
			return false
		}
		ok, err := filepath.Match(p, rel[0])
		if err != nil || !ok {
			return false
		}
		pat = pat[1:]
		rel = rel[1:]
	}
	for len(pat) > 0 && pat[0] == "**" {
		pat = pat[1:]
	}
	return len(pat) == 0 && len(rel) == 0
}
