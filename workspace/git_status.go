package workspace

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// Git stores text in repository form and checks it out in working-tree form;
// core.autocrlf, eol/text attributes and clean/smudge filters translate between
// them per path and per machine. The Git executable is the only authority for
// that translation, so every path that decides whether a file changed, reads
// a baseline to compare with disk, or writes disk bytes into the object store
// goes through it. go-git's worktree Status hashes raw disk bytes and reports
// every converted (for example CRLF-checked-out) file as modified.

// ErrGitNotInstalled means the Git executable is not on PATH. Source-control
// features report it and stay unavailable; nothing else depends on Git.
var ErrGitNotInstalled = errors.New("git is not installed or not on PATH; install Git to use source control")

// ReadGitStatus reports exactly what `git status` reports for the repository
// at root, keyed by repository-relative slash path. It takes no optional
// index lock, so it never races a user's concurrent Git command.
func ReadGitStatus(ctx context.Context, root string) (git.Status, error) {
	out, err := gitStdout(ctx, root, nil, "--no-optional-locks", "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	return parseGitStatusZ(out)
}

// parseGitStatusZ decodes `status --porcelain=v1 -z`: "XY path\0", with the
// rename/copy source in the following field.
func parseGitStatusZ(out []byte) (git.Status, error) {
	status := make(git.Status)
	fields := bytes.Split(out, []byte{0})
	for i := 0; i < len(fields); i++ {
		entry := fields[i]
		if len(entry) == 0 {
			continue
		}
		if len(entry) < 4 || entry[2] != ' ' {
			return nil, fmt.Errorf("read Git status: unexpected entry %q", entry)
		}
		x, y := entry[0], entry[1]
		file := &git.FileStatus{Staging: gitStatusCode(x), Worktree: gitStatusCode(y)}
		if x == 'R' || x == 'C' || y == 'R' || y == 'C' {
			i++
			if i >= len(fields) || len(fields[i]) == 0 {
				return nil, fmt.Errorf("read Git status: rename of %q has no source", entry[3:])
			}
			file.Extra = string(fields[i])
		}
		if x == '!' {
			continue
		}
		if gitStatusUnmerged(x, y) {
			file.Staging, file.Worktree = git.UpdatedButUnmerged, git.UpdatedButUnmerged
		}
		status[string(entry[3:])] = file
	}
	return status, nil
}

func gitStatusCode(code byte) git.StatusCode {
	switch code {
	case ' ':
		return git.Unmodified
	case '?':
		return git.Untracked
	case 'A':
		return git.Added
	case 'D':
		return git.Deleted
	case 'R':
		return git.Renamed
	case 'C':
		return git.Copied
	case 'U':
		return git.UpdatedButUnmerged
	default: // M, T (type change), and any future modification code.
		return git.Modified
	}
}

// gitStatusUnmerged recognizes every unmerged XY pair Git documents.
func gitStatusUnmerged(x, y byte) bool {
	return x == 'U' || y == 'U' || x == 'A' && y == 'A' || x == 'D' && y == 'D'
}

// gitCheckoutText returns blob in the working-tree form Git would check out
// at rel, applying the eol conversion and smudge filters configured for it.
func gitCheckoutText(ctx context.Context, root, rel, object string) (string, error) {
	out, err := gitStdout(ctx, root, nil, "cat-file", "--filters", "--path="+rel, object)
	if err != nil {
		return "", err
	}
	if int64(len(out)) > gitComparisonLimit {
		return "", fmt.Errorf("git comparison exceeds the 64 MiB review limit")
	}
	return string(out), nil
}

// gitWriteBlob stores text as a blob. Working-tree text passes through the
// clean conversion `git add` applies at rel; repository-form text is stored
// byte for byte.
func gitWriteBlob(ctx context.Context, root, rel, text string, worktreeForm bool) (plumbing.Hash, error) {
	args := []string{"hash-object", "-w", "--stdin"}
	if worktreeForm {
		args = append(args, "--path="+rel)
	} else {
		args = append(args, "--no-filters")
	}
	out, err := gitStdout(ctx, root, []byte(text), args...)
	if err != nil {
		return plumbing.ZeroHash, err
	}
	hash := strings.TrimSpace(string(out))
	if raw, decodeErr := hex.DecodeString(hash); decodeErr != nil || len(raw) != len(plumbing.ZeroHash) {
		return plumbing.ZeroHash, fmt.Errorf("git hash-object returned %q", hash)
	}
	return plumbing.NewHash(hash), nil
}

// gitStdout runs Git at root and returns stdout only; warnings on stderr never
// corrupt content and are kept for the error text.
func gitStdout(ctx context.Context, root string, stdin []byte, args ...string) ([]byte, error) {
	defer observeGit(args, time.Now())
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root}, args...)...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, ErrGitNotInstalled
		}
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return stdout.Bytes(), nil
}
