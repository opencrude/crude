package backend

import "time"

// ExecOptions controls optional execution parameters.
type ExecOptions struct {
	CWD            string // working directory; empty = backend default
	TimeoutSeconds int    // 0 = no timeout
}

// DirEntry holds metadata for a single filesystem entry.
type DirEntry struct {
	Name    string
	IsDir   bool
	IsLink  bool
	Size    int64
	ModTime time.Time
	Mode    string // Unix mode string, e.g. "-rw-r--r--"
}

// GrepMatch is a single line match from a content search.
type GrepMatch struct {
	Path    string
	LineNum int
	Line    string
}

// Executor is the pluggable CRUDE backend interface.
// Every method maps to one or more of the five CRUDE primitives.
type Executor interface {
	// Type returns the backend identifier ("local" or "ssh").
	Type() string

	// Execute runs a shell command and returns combined stdout+stderr.
	// Maps to the Execute primitive.
	Execute(cmd string, opts ExecOptions) (string, error)

	// ReadFile returns the full contents of a file as a string.
	// Maps to the Read primitive.
	ReadFile(path string) (string, error)

	// WriteFile creates or overwrites a file with the given content.
	// Parent directories are created automatically.
	// Maps to the Create / Update primitive.
	WriteFile(path, content string) error

	// EditFile performs an exact string replacement inside a file.
	// Returns the number of replacements made, or an error if oldStr
	// was not found (file is left unchanged in that case).
	// Maps to the Update primitive.
	EditFile(path, oldStr, newStr string) (int, error)

	// DeleteFile removes a file or empty directory.
	// Maps to the Delete primitive.
	DeleteFile(path string) error

	// MoveFile renames or moves src to dst, creating parent dirs as needed.
	// Maps to the Update primitive.
	MoveFile(src, dst string) error

	// CopyFile copies src to dst, creating parent dirs as needed.
	// Maps to the Create primitive.
	CopyFile(src, dst string) error

	// ListDir returns metadata for entries inside a directory.
	// Maps to the Read primitive.
	ListDir(path string) ([]DirEntry, error)

	// GlobFiles returns paths matching the glob pattern under baseDir.
	// Supports ** for recursive matching.
	// Maps to the Read primitive.
	GlobFiles(baseDir, pattern string) ([]string, error)

	// GrepFiles searches file contents for pattern (Go regexp) under baseDir.
	// If recursive is true, descends into subdirectories.
	// Maps to the Read primitive.
	GrepFiles(pattern, baseDir string, recursive bool) ([]GrepMatch, error)
}
