// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
package codexio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// safeOraclePath matches the only paths the oracle may touch. Codex resolves
// patch paths against its working directory and accepts absolute paths, so
// anything else could write outside the temporary directory.
var safeOraclePath = regexp.MustCompile(`^[a-z0-9_]+(/[a-z0-9_]+)*(\.[a-z]+)?$`)

// maxStderr bounds the stderr kept from one oracle run.
const maxStderr = 4096

// boundedBuffer keeps the first maxStderr bytes written to it. It does not
// embed bytes.Buffer, whose ReadFrom would bypass the bound.
type boundedBuffer struct {
	buffer bytes.Buffer
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	room := max(0, maxStderr-b.buffer.Len())
	b.buffer.Write(p[:min(len(p), room)])
	return len(p), nil
}

func (b *boundedBuffer) String() string {
	return b.buffer.String()
}

type oracleResult struct {
	// files are the files after the run, also after a failure: Codex leaves
	// the operations before the failing one applied.
	files  map[string]string
	ok     bool
	stderr string
}

// runOracle runs the Codex apply_patch binary with line-ending preservation
// on files in a new temporary directory, until ctx is done. This is the only code in the module
// that touches processes or the filesystem. It refuses unsafe paths, and it
// returns an error when the binary crashes rather than rejecting the patch.
func runOracle(ctx context.Context, bin string, files map[string]string, patch string) (oracleResult, error) {
	return runOracleMode(ctx, bin, files, patch, PreserveLineEndings)
}

func runOracleMode(ctx context.Context, bin string, files map[string]string, patch string, mode Mode) (oracleResult, error) {
	err := checkOraclePaths(files, patch)
	if err != nil {
		return oracleResult{}, err
	}
	dir, err := os.MkdirTemp("", "applypatch-oracle")
	if err != nil {
		return oracleResult{}, err
	}
	defer os.RemoveAll(dir)
	for name, contents := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		err = os.MkdirAll(filepath.Dir(path), 0o755)
		if err != nil {
			return oracleResult{}, err
		}
		err = os.WriteFile(path, []byte(contents), 0o644)
		if err != nil {
			return oracleResult{}, err
		}
	}
	var stderr boundedBuffer
	cmd := exec.CommandContext(ctx, bin, patch)
	cmd.Dir = dir
	modeEnv := "0"
	if mode == PreserveLineEndings {
		modeEnv = "1"
	}
	cmd.Env = append(os.Environ(), "CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS="+modeEnv)
	cmd.Stderr = &stderr
	// Stop waiting for output once the process is killed, even if a child
	// still holds the pipe open.
	cmd.WaitDelay = time.Second
	err = cmd.Run()
	var exit *exec.ExitError
	switch {
	case err == nil:
	case !errors.As(err, &exit) || ctx.Err() != nil:
		return oracleResult{}, fmt.Errorf("running %s: %w", bin, err)
	case exit.ExitCode() != 1:
		// Codex exits with 1 when it rejects a patch; anything else, such as
		// a panic (101) or a signal, is a crash and not a result to record.
		return oracleResult{}, fmt.Errorf("%s crashed: %w: %s", bin, err, stderr.String())
	case strings.HasPrefix(stderr.String(), "Error:"):
		// Codex also exits with 1 when it cannot start, for example without
		// a working directory; patch rejections never start with "Error:".
		return oracleResult{}, fmt.Errorf("%s failed to start: %s", bin, stderr.String())
	}
	after := map[string]string{}
	err = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		name, err := filepath.Rel(dir, path)
		after[filepath.ToSlash(name)] = string(contents)
		return err
	})
	if err != nil {
		return oracleResult{}, err
	}
	return oracleResult{files: after, ok: exit == nil, stderr: stderr.String()}, nil
}

// checkOraclePaths refuses files and patch paths outside safeOraclePath.
func checkOraclePaths(files map[string]string, patch string) error {
	for name := range files {
		if !safeOraclePath.MatchString(name) {
			return fmt.Errorf("unsafe oracle file name %q", name)
		}
	}
	if strings.ContainsRune(patch, 0) {
		return errors.New("patch contains NUL, which cannot be passed as an argument")
	}
	for line := range strings.SplitSeq(patch, "\n") {
		for _, marker := range []string{addFile, deleteFile, updateFile, moveTo} {
			index := strings.Index(line, strings.TrimSpace(marker))
			if index < 0 {
				continue
			}
			path := strings.TrimSpace(line[index+len(strings.TrimSpace(marker)):])
			if path != "" && !safeOraclePath.MatchString(path) {
				return fmt.Errorf("unsafe oracle patch path %q", path)
			}
		}
	}
	return nil
}

// errorCategory maps a Codex or applypatch error message to a category, so
// that failures are compared by cause and not only by occurrence.
func errorCategory(message string) string {
	lower := strings.ToLower(message)
	for _, category := range []struct{ prefix, name string }{
		{"invalid patch", "parse"},
		{"failed to find expected lines", "lines"},
		{"failed to find context", "context"},
		{"failed to read file to update", "read"},
		{"failed to delete file", "delete"},
		{"no files were modified", "empty"},
	} {
		if strings.HasPrefix(lower, category.prefix) {
			return category.name
		}
	}
	first, _, _ := strings.Cut(message, "\n")
	return "other: " + first
}
