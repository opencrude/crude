package workspace

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
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
	return readGitStatus(ctx, root, nil)
}

// Porcelain v2 returns branch metadata and all pathname records in one Git
// process. A plain status read does not request the upstream graph walk.
func readGitStatus(ctx context.Context, root string, branch *GitSnapshot) (git.Status, error) {
	args := []string{"--no-optional-locks", "status", "--porcelain=v2", "-z", "--untracked-files=all"}
	if branch != nil {
		args = append(args, "--branch", "--ahead-behind")
	}
	out, err := gitStdout(ctx, root, nil, args...)
	if err != nil {
		return nil, err
	}
	return parseGitStatusZ(out, branch)
}

// Paths may contain spaces, line breaks, and invalid UTF-8. Split only the
// documented metadata fields, never split the name on whitespace.
func parseGitStatusZ(out []byte, branch *GitSnapshot) (git.Status, error) {
	status := make(git.Status)
	for len(out) > 0 {
		var row []byte
		var ok bool
		row, out, ok = bytes.Cut(out, []byte{0})
		if !ok || len(row) < 3 || row[1] != ' ' {
			return nil, errors.New("incomplete Git status record")
		}
		switch row[0] {
		case '#':
			if branch != nil {
				key, value, _ := strings.Cut(string(row[2:]), " ")
				switch key {
				case "branch.oid":
					if value != "(initial)" {
						branch.Head = value
					}
				case "branch.head":
					branch.Branch = value
				case "branch.upstream":
					branch.Upstream = value
				case "branch.ab":
					a, b, ok := strings.Cut(value, " ")
					ahead, e1 := strconv.Atoi(a)
					behind, e2 := strconv.Atoi(b)
					if !ok || e1 != nil || e2 != nil || ahead < 0 || behind > 0 {
						return nil, errors.New("invalid Git tracking counts")
					}
					branch.Ahead, branch.Behind = ahead, -behind
				}
			}
			continue
		case '?':
			status[string(row[2:])] = &git.FileStatus{Staging: git.Untracked, Worktree: git.Untracked}
			continue
		case '!':
			continue
		}
		fields := 9
		switch row[0] {
		case '1':
		case '2':
			fields = 10
		case 'u':
			fields = 11
		default:
			return nil, fmt.Errorf("unknown Git status record %q", row[0])
		}
		parts := bytes.SplitN(row, []byte{' '}, fields)
		if len(parts) != fields || len(parts[1]) != 2 || len(parts[fields-1]) == 0 {
			return nil, errors.New("invalid Git status record")
		}
		file := &git.FileStatus{Staging: gitStatusCode(parts[1][0]), Worktree: gitStatusCode(parts[1][1])}
		if row[0] == 'u' {
			file.Staging, file.Worktree = git.UpdatedButUnmerged, git.UpdatedButUnmerged
		}
		if row[0] == '2' {
			var source []byte
			source, out, ok = bytes.Cut(out, []byte{0})
			if !ok || len(source) == 0 {
				return nil, errors.New("missing source in Git rename")
			}
			file.Extra = string(source)
		}
		status[string(parts[fields-1])] = file
	}
	if branch != nil {
		if branch.Head == "" {
			branch.Branch = "No commits"
		} else if branch.Branch == "(detached)" {
			branch.Branch = "detached@" + shortGitHash(branch.Head)
		}
	}
	return status, nil
}

func gitStatusCode(code byte) git.StatusCode {
	switch code {
	case '.':
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
	stdout := gitBoundedOutput{limit: int(gitComparisonLimit)}
	stderr := gitBoundedOutput{limit: 1 << 20}
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
	if stdout.overflow {
		return nil, errors.New("git output exceeds the 64 MiB review limit")
	}
	return stdout.Bytes(), nil
}

// Continue draining after the limit so a subprocess cannot block on a full
// output pipe. The caller receives an explicit overflow, never truncated success.
type gitBoundedOutput struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
}

func (b *gitBoundedOutput) Len() int       { return b.buffer.Len() }
func (b *gitBoundedOutput) Bytes() []byte  { return b.buffer.Bytes() }
func (b *gitBoundedOutput) String() string { return b.buffer.String() }

func (b *gitBoundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	available := max(0, b.limit-b.Len())
	if n > available {
		b.overflow = true
		p = p[:available]
	}
	_, _ = b.buffer.Write(p)
	return n, nil
}
