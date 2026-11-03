package workspace

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
)

// mutatePaths sends one exact selection through stdin, not the process command
// line, whose size is limited on Windows. NUL framing preserves whitespace and
// newlines; literal pathspecs prevent filenames from becoming Git patterns.
func (s *GitService) mutatePaths(ctx context.Context, command string, paths []string) error {
	for _, path := range paths {
		if strings.ContainsRune(path, '\x00') {
			return errors.New("cannot change the Git index: a file path contains a null character. Refresh the file list and try again")
		}
	}
	clean := cleanGitPaths(paths)
	if len(clean) == 0 {
		return nil
	}
	if err := s.lock(ctx); err != nil {
		return err
	}
	defer s.unlock()
	input := strings.NewReader(strings.Join(clean, "\x00") + "\x00")
	return s.runInput(ctx, input, "--literal-pathspecs", command, "--pathspec-from-file=-", "--pathspec-file-nul")
}

func cleanGitPaths(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, path := range paths {
		path = filepath.ToSlash(filepath.Clean(path))
		if path == "" || path == "." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		out = append(out, path)
	}
	return out
}
