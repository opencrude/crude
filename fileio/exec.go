package fileio

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"crude/logger"

	"github.com/charmbracelet/x/ansi"
)

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

// MaxGrepResults is the match cap a grep result reports when it stops early.
const MaxGrepResults = maxGrepResults

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
	if ansi.StringWidth(s) <= maxLen {
		return s
	}
	return ansi.Truncate(s, maxLen, "…")
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
		// Never skip the root directory itself: if the user points
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

// SplitGlobPath separates a path containing "*" or "?" into its longest
// literal directory prefix and the remaining slash-separated glob. A path that
// exists literally is never treated as a glob. "[" is left literal because it is
// common in real file names.
func SplitGlobPath(path string) (base, pattern string, isGlob bool) {
	if !strings.ContainsAny(path, "*?") {
		return path, "", false
	}
	if _, err := os.Lstat(path); err == nil {
		return path, "", false
	}
	segments := strings.Split(filepath.ToSlash(path), "/")
	literal := 0
	for literal < len(segments) && !strings.ContainsAny(segments[literal], "*?") {
		literal++
	}
	base = strings.Join(segments[:literal], "/")
	switch {
	case base == "" && strings.HasPrefix(filepath.ToSlash(path), "/"):
		base = "/"
	case base == "":
		base = "."
	case filepath.VolumeName(filepath.FromSlash(base)) == filepath.FromSlash(base):
		// "C:" alone is drive-relative on Windows; the glob was rooted.
		base += "/"
	}
	return filepath.FromSlash(base), strings.Join(segments[literal:], "/"), true
}

// normalizeUnicodeEscapes rewrites the `\uXXXX` and `\UXXXXXXXX` escapes that
// PCRE, ripgrep and JSON users write into RE2's `\x{...}` form. An escaped
// backslash (`\\u`) is left alone.
func normalizeUnicodeEscapes(pattern string) string {
	if !strings.Contains(pattern, `\u`) && !strings.Contains(pattern, `\U`) {
		return pattern
	}
	var b strings.Builder
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		if c != '\\' || i+1 >= len(pattern) {
			b.WriteByte(c)
			continue
		}
		next := pattern[i+1]
		width := 0
		switch next {
		case 'u':
			width = 4
		case 'U':
			width = 8
		}
		if width > 0 && i+2+width <= len(pattern) && isHexDigits(pattern[i+2:i+2+width]) {
			b.WriteString(`\x{` + pattern[i+2:i+2+width] + `}`)
			i += 1 + width
			continue
		}
		b.WriteByte(c)
		b.WriteByte(next)
		i++
	}
	return b.String()
}

func isHexDigits(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// GrepFiles searches regular files under baseDir. baseDir may be a directory, a
// file, or a glob such as "workspace/lsp*.go" or "tui/**/*_test.go"; a glob
// searches only the files it matches.
func (l *LocalExec) GrepFiles(pattern, baseDir string, recursive, includeHidden bool) ([]GrepMatch, error) {
	if baseDir == "" {
		baseDir = "."
	}
	re, err := regexp.Compile(normalizeUnicodeEscapes(pattern))
	if err != nil {
		return nil, fmt.Errorf("invalid pattern: %w", err)
	}

	baseDir, fileGlob, isGlob := SplitGlobPath(baseDir)
	globDepth := -1
	if isGlob && !strings.Contains(fileGlob, "**") {
		globDepth = strings.Count(fileGlob, "/")
		recursive = true
	}
	if isGlob && strings.Contains(fileGlob, "**") {
		recursive = true
	}

	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(absBase); err != nil {
		return nil, fmt.Errorf("search path %s: %w", baseDir, err)
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
			if globDepth >= 0 && path != absBase {
				if rel, err := filepath.Rel(absBase, path); err == nil && strings.Count(filepath.ToSlash(rel), "/") >= globDepth {
					return fs.SkipDir
				}
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		if isGlob {
			rel, err := filepath.Rel(absBase, path)
			if err != nil || !matchGlobPattern(fileGlob, filepath.ToSlash(rel)) {
				return nil
			}
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
