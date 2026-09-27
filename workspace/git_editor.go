package workspace

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"crude/textdiff"
)

// GitBlameLine is porcelain blame for one 1-based file line.
type GitBlameLine struct {
	Line        int
	Hash        string
	ShortHash   string
	Author      string
	When        time.Time
	Subject     string
	Uncommitted bool
}

// GitFileAnnotation is the editor-local Git projection for one path.
type GitFileAnnotation struct {
	HeadContent string
	Path        string
	Blame       []GitBlameLine
	Hunks       []GitHunk
	Conflicted  bool
}

// GitHunk is one exact HEAD-to-buffer change. Starts are zero-based line
// positions; line slices retain their original line endings for inspection.
type GitHunk = textdiff.Hunk

// AnnotateFile reads blame and HEAD-vs-content line edits for absPath.
// content is the editor buffer (unsaved text included). Results are
// presentation-neutral; the TUI owns gutter paint and hover.
func (s *GitService) AnnotateFile(ctx context.Context, absPath, content string) (GitFileAnnotation, error) {
	if s == nil {
		return GitFileAnnotation{}, errorsNewGitUnavailable()
	}
	if err := ctx.Err(); err != nil {
		return GitFileAnnotation{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	_, root, err := s.open()
	if err != nil {
		return GitFileAnnotation{}, err
	}
	rel, err := repoRelPath(root, absPath)
	if err != nil {
		return GitFileAnnotation{}, err
	}

	annotation := GitFileAnnotation{Path: absPath}
	head, headErr := s.showHEAD(ctx, root, rel)
	if err := ctx.Err(); err != nil {
		return GitFileAnnotation{}, err
	}
	if headErr == nil {
		annotation.HeadContent = head
		annotation.Hunks = DocumentHunks(head, content)
	} else if isUntrackedGitError(headErr) {
		annotation.Hunks = DocumentHunks("", content)
	} else if !s.hasHEAD(ctx, root) {
		annotation.Hunks = DocumentHunks("", content)
	} else {
		return GitFileAnnotation{}, headErr
	}
	annotation.Conflicted = s.isConflicted(ctx, root, rel)
	blame, blameErr := s.blameFile(ctx, root, rel, content)
	if blameErr == nil {
		annotation.Blame = blame
	}
	if err := ctx.Err(); err != nil {
		return GitFileAnnotation{}, err
	}
	return annotation, nil
}

func (s *GitService) hasHEAD(ctx context.Context, root string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "rev-parse", "--verify", "HEAD")
	return cmd.Run() == nil
}

func (s *GitService) isConflicted(ctx context.Context, root, rel string) bool {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "ls-files", "-u", "--", rel)
	out, err := cmd.Output()
	return err == nil && strings.TrimSpace(string(out)) != ""
}

func errorsNewGitUnavailable() error {
	return fmt.Errorf("git service is unavailable")
}

func repoRelPath(root, absPath string) (string, error) {
	abs, err := filepath.Abs(absPath)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", absPath, err)
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil {
		return "", fmt.Errorf("path %q is outside the repository: %w", absPath, err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q is outside the repository", absPath)
	}
	return filepath.ToSlash(rel), nil
}

func (s *GitService) showHEAD(ctx context.Context, root, rel string) (string, error) {
	out, err := s.output(ctx, root, "show", "HEAD:"+rel)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func (s *GitService) blameFile(ctx context.Context, root, rel, content string) ([]GitBlameLine, error) {
	out, err := s.outputStdin(ctx, root, []byte(content), "blame", "--line-porcelain", "--contents", "-", "--", rel)
	if err != nil {
		return nil, err
	}
	return parseBlamePorcelain(string(out))
}

func (s *GitService) output(ctx context.Context, root string, args ...string) ([]byte, error) {
	return s.outputStdin(ctx, root, nil, args...)
}

func (s *GitService) outputStdin(ctx context.Context, root string, stdin []byte, args ...string) ([]byte, error) {
	cmdArgs := append([]string{"-C", root}, args...)
	cmd := exec.CommandContext(ctx, "git", cmdArgs...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		detail := strings.TrimSpace(string(out))
		if detail == "" {
			detail = err.Error()
		}
		return nil, fmt.Errorf("git %s: %s", strings.Join(args, " "), detail)
	}
	return out, nil
}

func isUntrackedGitError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "exists on disk, but not in") ||
		strings.Contains(msg, "does not exist in") ||
		strings.Contains(msg, "bad revision") ||
		strings.Contains(msg, "unknown revision") ||
		strings.Contains(msg, "invalid object name") ||
		strings.Contains(msg, "ambiguous argument") ||
		strings.Contains(msg, "does not have any commits") ||
		strings.Contains(msg, "is outside repository")
}

// DocumentHunks compares exact immutable text snapshots without Git or disk I/O.
func DocumentHunks(before, after string) []GitHunk {
	return textdiff.Hunks(before, after)
}

func parseBlamePorcelain(text string) ([]GitBlameLine, error) {
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var (
		out      []GitBlameLine
		current  GitBlameLine
		haveMeta bool
	)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "\t") {
			if current.Line > 0 {
				out = append(out, current)
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && len(fields[0]) >= 8 && isHex(fields[0]) {
			final, err := strconv.Atoi(fields[2])
			if err != nil {
				continue
			}
			hash := fields[0]
			if !haveMeta || current.Hash != hash {
				current = GitBlameLine{Hash: hash, ShortHash: shortGitHash(hash)}
				haveMeta = true
			} else {
				current.Line = 0
			}
			current.Hash = hash
			current.ShortHash = shortGitHash(hash)
			current.Uncommitted = isZeroGitHash(hash)
			current.Line = final
			continue
		}
		switch {
		case strings.HasPrefix(line, "author "):
			current.Author = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "author-time "):
			sec, err := strconv.ParseInt(strings.TrimPrefix(line, "author-time "), 10, 64)
			if err == nil {
				current.When = time.Unix(sec, 0)
			}
		case strings.HasPrefix(line, "summary "):
			current.Subject = strings.TrimPrefix(line, "summary ")
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return len(s) > 0
}

func isZeroGitHash(hash string) bool {
	if hash == "" {
		return true
	}
	for i := 0; i < len(hash); i++ {
		if hash[i] != '0' {
			return false
		}
	}
	return true
}
