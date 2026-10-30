package lsp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	maxProcessEnvironmentEntries = 256
	maxProcessEnvironmentBytes   = 128 << 10
	maxProcessEnvironmentKey     = 256
	maxProcessEnvironmentValue   = 32 << 10
	maxProcessPathEntries        = 64
	maxProcessPathBytes          = 4096
)

// ErrProcessEnvironment covers invalid, unprepared, oversized environments and
// failed executable resolution. Errors deliberately contain no supplied data or
// filesystem error details: those can include credentials or private paths.
var ErrProcessEnvironment = errors.New("invalid process environment or executable")

// ProcessEnvironment is an immutable COMPLETE explicit launch environment.
// Its zero value is unprepared; NewProcessEnvironment(nil) is prepared and empty.
// Formatting and JSON redact all contents. Env is privileged launch data, not
// logging, facts, receipts, authorization, or an executable-identity attestation.
type ProcessEnvironment struct {
	table *processEnvironmentTable
}

type processEnvironmentTable struct {
	env        []string
	dirs       []string
	extensions []string
}

// NewProcessEnvironment validates and detaches a complete environment. It never
// reads the ambient environment or filesystem. Keys are ASCII identifiers (first
// letter/underscore, then letters/digits/underscore/dot/hyphen/parentheses).
// Values must be UTF-8 without NUL. Bounds count KEY=value plus one terminator,
// before PATH deduplication; they are not OS launchability or heap guarantees.
//
// If present, PATH must contain 1..64 absolute native directories, each at most
// 4096 bytes, without control characters or dot components. Empty/relative
// entries are rejected, never silently expanded to the working directory.
// Exact duplicate directories are removed, preserving first occurrence.
func NewProcessEnvironment(values map[string]string) (ProcessEnvironment, error) {
	if len(values) > maxProcessEnvironmentEntries {
		return ProcessEnvironment{}, ErrProcessEnvironment
	}
	table := &processEnvironmentTable{env: make([]string, 0, len(values))}
	seen := make(map[string]bool, len(values))
	total := 0
	for key, value := range values {
		if !validProcessEnvironmentKey(key) || len(value) > maxProcessEnvironmentValue ||
			!utf8.ValidString(value) || strings.ContainsRune(value, 0) {
			return ProcessEnvironment{}, ErrProcessEnvironment
		}
		total += len(key) + len(value) + 2
		if total > maxProcessEnvironmentBytes {
			return ProcessEnvironment{}, ErrProcessEnvironment
		}
		canonical := processEnvironmentKey(key)
		if seen[canonical] {
			return ProcessEnvironment{}, ErrProcessEnvironment
		}
		seen[canonical] = true
		switch canonical {
		case "PATH":
			dirs, err := prepareProcessPath(value)
			if err != nil {
				return ProcessEnvironment{}, err
			}
			table.dirs = dirs
			value = strings.Join(dirs, string(os.PathListSeparator))
		case "PATHEXT":
			extensions, err := prepareProcessExtensions(value)
			if err != nil {
				return ProcessEnvironment{}, err
			}
			table.extensions = extensions
		}
		table.env = append(table.env, key+"="+value)
	}
	sort.Strings(table.env)
	return ProcessEnvironment{table: table}, nil
}

func validProcessEnvironmentKey(key string) bool {
	if len(key) == 0 || len(key) > maxProcessEnvironmentKey {
		return false
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c == '_' {
			continue
		}
		if i > 0 && (c >= '0' && c <= '9' || c == '.' || c == '-' || c == '(' || c == ')') {
			continue
		}
		return false
	}
	return true
}

func prepareProcessPath(value string) ([]string, error) {
	if strings.Count(value, string(os.PathListSeparator)) >= maxProcessPathEntries {
		return nil, ErrProcessEnvironment
	}
	dirs := strings.Split(value, string(os.PathListSeparator))
	out := make([]string, 0, len(dirs))
	seen := make(map[string]bool, len(dirs))
	for _, dir := range dirs {
		if !validProcessAbsolutePath(dir) {
			return nil, ErrProcessEnvironment
		}
		if !seen[dir] {
			out = append(out, dir)
			seen[dir] = true
		}
	}
	return out, nil
}

func validProcessPathText(value string) bool {
	if len(value) == 0 || len(value) > maxProcessPathBytes || !utf8.ValidString(value) {
		return false
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (e ProcessEnvironment) prepared() bool { return e.table != nil }

// Env returns a detached, sorted KEY=value list for exec.Cmd.Env. A prepared
// empty environment returns a nonnil empty slice, preventing ambient inheritance.
// The unprepared zero value returns nil and must be rejected before launch.
func (e ProcessEnvironment) Env() []string {
	if !e.prepared() {
		return nil
	}
	out := make([]string, len(e.table.env))
	copy(out, e.table.env)
	return out
}

// resolve performs bounded read/stat-only lookup in this table, never LookPath,
// ambient PATH/PATHEXT, a shell, or the working directory. Explicit native
// absolute executable paths do not require PATH. os.Stat follows symlinks:
// success describes the observed regular file, not immutable bytes, authority,
// containment, execute access/noexec mounts, or freedom from later TOCTOU races.
func (e ProcessEnvironment) resolve(command string) (string, error) {
	if !e.prepared() || !validProcessPathText(command) {
		return "", ErrProcessEnvironment
	}
	if filepath.IsAbs(command) {
		if !validProcessAbsolutePath(command) || !processExplicitExecutable(command) {
			return "", ErrProcessEnvironment
		}
		if processExecutableFile(command) {
			return command, nil
		}
		return "", ErrProcessEnvironment
	}
	if command == "." || command == ".." || strings.ContainsAny(command, `/\:`) {
		return "", ErrProcessEnvironment
	}
	names := processExecutableNames(command, e.table.extensions)
	for _, dir := range e.table.dirs {
		for _, name := range names {
			candidate := filepath.Join(dir, name)
			if len(candidate) <= maxProcessPathBytes && processExecutableFile(candidate) {
				return candidate, nil
			}
		}
	}
	return "", ErrProcessEnvironment
}

func processExecutableFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular() && processExecutableMode(info.Mode())
}

// Format redacts even %+v and %#v, including when embedded in other structures.
func (ProcessEnvironment) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "ProcessEnvironment([redacted])")
}

// MarshalJSON deliberately exports no keys, values, or environment fingerprint.
func (ProcessEnvironment) MarshalJSON() ([]byte, error) {
	return []byte(`"ProcessEnvironment([redacted])"`), nil
}
