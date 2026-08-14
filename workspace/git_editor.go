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

	"github.com/aymanbagabas/go-udiff"
)

// GitLineKind is the HEAD-vs-buffer status of one editor line.
type GitLineKind uint8

const (
	GitLineUnchanged GitLineKind = iota
	GitLineAdded
	GitLineModified
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
	Path  string
	Blame []GitBlameLine
	Edits []GitLineKind
}

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
	if headErr == nil {
		annotation.Edits = lineEdits(head, content)
	} else if isUntrackedGitError(headErr) {
		annotation.Edits = addedEdits(content)
	} else {
		return GitFileAnnotation{}, headErr
	}
	blame, blameErr := s.blameFile(ctx, root, rel, content)
	if blameErr == nil {
		annotation.Blame = blame
	}
	return annotation, nil
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

func addedEdits(content string) []GitLineKind {
	n := countLines(content)
	edits := make([]GitLineKind, n)
	for i := range edits {
		edits[i] = GitLineAdded
	}
	return edits
}

func lineEdits(before, after string) []GitLineKind {
	afterN := countLines(after)
	edits := make([]GitLineKind, afterN)
	if before == after {
		return edits
	}
	if strings.TrimRight(before, "\n") == "" {
		return addedEdits(after)
	}
	unified := udiff.Unified("a", "b", before, after)
	afterIdx := 0
	pendingDeletes := 0
	for _, line := range strings.Split(unified, "\n") {
		if strings.HasPrefix(line, "@@") {
			pendingDeletes = 0
			continue
		}
		if strings.HasPrefix(line, "---") || strings.HasPrefix(line, "+++") {
			continue
		}
		if line == "" {
			continue
		}
		switch line[0] {
		case '-':
			if !strings.HasPrefix(line, "---") {
				pendingDeletes++
			}
		case '+':
			if strings.HasPrefix(line, "+++") {
				continue
			}
			if afterIdx >= afterN {
				continue
			}
			if pendingDeletes > 0 {
				edits[afterIdx] = GitLineModified
				pendingDeletes--
			} else {
				edits[afterIdx] = GitLineAdded
			}
			afterIdx++
		case ' ':
			afterIdx++
			pendingDeletes = 0
		}
	}
	return edits
}

func countLines(text string) int {
	if text == "" {
		return 0
	}
	n := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		n++
	}
	return n
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
