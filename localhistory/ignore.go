package localhistory

import (
	"bufio"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

var defaultIgnoredDirectories = []string{
	".git/", "node_modules/", "target/", "dist/", "build/", "out/",
	".next/", ".nuxt/", ".turbo/", ".cache/", "coverage/",
	"__pycache__/", ".pytest_cache/", ".mypy_cache/", ".venv/", "venv/",
	"bin/", "obj/",
}

var defaultIgnoredDirectoryNames = func() map[string]bool {
	names := make(map[string]bool, len(defaultIgnoredDirectories))
	for _, pattern := range defaultIgnoredDirectories {
		names[strings.TrimSuffix(pattern, "/")] = true
	}
	return names
}()

type ignoreMatcher struct{ matcher gitignore.Matcher }

func newIgnoreMatcher(root string, opts SnapshotOptions) (*ignoreMatcher, error) {
	patterns := make([]gitignore.Pattern, 0, len(defaultIgnoredDirectories)+len(opts.ExtraIgnorePatterns))
	for _, pattern := range defaultIgnoredDirectories {
		patterns = append(patterns, gitignore.ParsePattern(pattern, nil))
	}
	if !opts.DisableGitIgnore {
		worktree := osfs.New(root)
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(root, path)
			if relErr != nil {
				return relErr
			}
			domain := []string(nil)
			if rel != "." {
				domain = strings.Split(filepath.ToSlash(rel), "/")
			}
			if path != root && defaultIgnoredDirectoryNames[entry.Name()] &&
				gitignore.NewMatcher(patterns).Match(domain, true) {
				return filepath.SkipDir
			}
			local, readErr := gitignore.ReadPatterns(worktree, domain)
			if readErr != nil && !os.IsNotExist(readErr) {
				return readErr
			}
			patterns = append(patterns, local...)
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for _, pattern := range opts.ExtraIgnorePatterns {
		patterns = append(patterns, gitignore.ParsePattern(pattern, nil))
	}
	if file, err := os.Open(filepath.Join(root, ".crudehistoryignore")); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			patterns = append(patterns, gitignore.ParsePattern(scanner.Text(), nil))
		}
		closeErr := file.Close()
		if err := scanner.Err(); err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	return &ignoreMatcher{matcher: gitignore.NewMatcher(patterns)}, nil
}

func (m *ignoreMatcher) ignored(rel string, isDir bool) bool {
	if m == nil || rel == "." || rel == "" {
		return false
	}
	return m.matcher.Match(strings.Split(filepath.ToSlash(rel), "/"), isDir)
}

func (s *Store) ShouldIgnore(rel string, isDir bool, opts SnapshotOptions) (bool, error) {
	matcher, err := newIgnoreMatcher(s.workspaceRoot, opts)
	if err != nil {
		return false, err
	}
	return matcher.ignored(filepath.ToSlash(rel), isDir), nil
}

func (s *Store) IgnoreFilter(opts SnapshotOptions) (func(string, bool) bool, error) {
	matcher, err := newIgnoreMatcher(s.workspaceRoot, opts)
	if err != nil {
		return nil, err
	}
	return func(rel string, isDir bool) bool { return matcher.ignored(filepath.ToSlash(rel), isDir) }, nil
}
