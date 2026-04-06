package backend

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
)

// Local executes CRUDE operations directly on the host machine.
type Local struct{}

// NewLocal returns a Local backend.
func NewLocal() *Local { return &Local{} }

func (l *Local) Type() string { return "local" }

// Execute runs a shell command on the local host.
func (l *Local) Execute(cmd string, opts ExecOptions) (string, error) {
	var c *exec.Cmd

	ctx := context.Background()
	if opts.TimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(opts.TimeoutSeconds)*time.Second)
		defer cancel()
	}

	if runtime.GOOS == "windows" {
		c = exec.CommandContext(ctx, "cmd", "/C", cmd)
	} else {
		c = exec.CommandContext(ctx, "sh", "-c", cmd)
	}

	if opts.CWD != "" {
		c.Dir = opts.CWD
	}

	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	return buf.String(), err
}

// ReadFile reads the contents of a local file.
func (l *Local) ReadFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteFile creates or overwrites a local file, creating parent dirs as needed.
func (l *Local) WriteFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(content), 0644)
}

// EditFile performs an exact string replacement inside path.
// Returns the number of replacements made, or an error if oldStr was not found.
func (l *Local) EditFile(path, oldStr, newStr string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	original := string(data)
	count := strings.Count(original, oldStr)
	if count == 0 {
		return 0, fmt.Errorf("old_string not found in %s", path)
	}
	replaced := strings.ReplaceAll(original, oldStr, newStr)
	if err := os.WriteFile(path, []byte(replaced), 0644); err != nil {
		return 0, err
	}
	return count, nil
}

// DeleteFile removes a local file or empty directory.
func (l *Local) DeleteFile(path string) error {
	return os.Remove(path)
}

// MoveFile renames or moves src to dst, creating parent dirs as needed.
func (l *Local) MoveFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}
	return os.Rename(src, dst)
}

// CopyFile copies src to dst, creating parent dirs as needed.
func (l *Local) CopyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Close()
}

// ListDir returns metadata for entries inside a local directory.
func (l *Local) ListDir(path string) ([]DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
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
	return result, nil
}

// GlobFiles returns paths under baseDir matching the given glob pattern.
// Supports ** for recursive segment matching.
func (l *Local) GlobFiles(baseDir, pattern string) ([]string, error) {
	if baseDir == "" {
		baseDir = "."
	}
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return nil, err
	}

	var matches []string
	err = filepath.WalkDir(absBase, func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil // skip unreadable entries
		}
		rel, err := filepath.Rel(absBase, path)
		if err != nil {
			return nil
		}
		if rel == "." {
			return nil
		}
		if matchGlobPattern(pattern, filepath.ToSlash(rel)) {
			matches = append(matches, filepath.ToSlash(path))
		}
		return nil
	})
	return matches, err
}

// GrepFiles searches file contents for pattern (Go regexp) under baseDir.
// Skips binary files and unreadable entries.
func (l *Local) GrepFiles(pattern, baseDir string, recursive bool) ([]GrepMatch, error) {
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

	var results []GrepMatch

	walkFn := func(path string, d fs.DirEntry, werr error) error {
		if werr != nil {
			return nil
		}
		if d.IsDir() {
			if !recursive && path != absBase {
				return fs.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return nil
		}
		defer f.Close()

		// Binary check: read first 512 bytes.
		probe := make([]byte, 512)
		n, _ := f.Read(probe)
		if bytes.IndexByte(probe[:n], 0) >= 0 {
			return nil // binary file
		}
		// Seek back to start.
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return nil
		}

		scanner := bufio.NewScanner(f)
		lineNum := 0
		for scanner.Scan() {
			lineNum++
			line := scanner.Text()
			if re.MatchString(line) {
				results = append(results, GrepMatch{
					Path:    filepath.ToSlash(path),
					LineNum: lineNum,
					Line:    line,
				})
			}
		}
		return nil
	}

	if err := filepath.WalkDir(absBase, walkFn); err != nil {
		return nil, err
	}
	return results, nil
}
