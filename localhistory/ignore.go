package localhistory

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/go-git/go-git/v5/plumbing/format/gitignore"
)

var defaultIgnoredDirectories = []string{
	".git/", "node_modules/", "target/", "dist/", "build/", "out/",
	".next/", ".nuxt/", ".turbo/", ".cache/", "coverage/",
	"__pycache__/", ".pytest_cache/", ".mypy_cache/", ".venv/", "venv/",
	"bin/", "obj/",
}

type ignoreMatcher struct {
	matcher  gitignore.Matcher
	root     string
	gitAdmin []string
}

// Git metadata is never user history, including linked-worktree pointer files.
// Resolve the on-disk pointers without invoking Git, hooks or external programs.
func gitAdministration(root string) ([]string, error) {
	pointer := filepath.Join(root, ".git")
	info, err := os.Lstat(pointer)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	admin := pointer
	if info.Mode().IsRegular() {
		data, err := os.ReadFile(pointer)
		if err != nil {
			return nil, err
		}
		text := strings.TrimSpace(string(data))
		if !strings.HasPrefix(text, "gitdir: ") {
			return nil, fmt.Errorf("invalid Git administration pointer")
		}
		admin = strings.TrimSpace(strings.TrimPrefix(text, "gitdir: "))
		if !filepath.IsAbs(admin) {
			admin = filepath.Join(root, admin)
		}
	}
	admin, err = filepath.EvalSymlinks(admin)
	if err != nil {
		return nil, err
	}
	result := []string{admin}
	data, err := os.ReadFile(filepath.Join(admin, "commondir"))
	if os.IsNotExist(err) {
		return result, nil
	}
	if err != nil {
		return nil, err
	}
	common := strings.TrimSpace(string(data))
	if !filepath.IsAbs(common) {
		common = filepath.Join(admin, common)
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return nil, err
	}
	return append(result, common), nil
}

func (m *ignoreMatcher) hardExcluded(path string) bool {
	for _, part := range strings.Split(filepath.ToSlash(filepath.Clean(path)), "/") {
		if strings.EqualFold(part, ".git") {
			return true
		}
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(m.root, filepath.FromSlash(path))
	}
	for _, admin := range m.gitAdmin {
		rel, err := filepath.Rel(admin, path)
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

func newIgnoreMatcher(root string, opts SnapshotOptions) (*ignoreMatcher, error) {
	admin, err := gitAdministration(root)
	if err != nil {
		return nil, err
	}
	hard := &ignoreMatcher{root: root, gitAdmin: admin}
	defaults := make([]gitignore.Pattern, 0, len(defaultIgnoredDirectories))
	for _, pattern := range defaultIgnoredDirectories {
		defaults = append(defaults, gitignore.ParsePattern(pattern, nil))
	}
	overrides := make([]gitignore.Pattern, 0, len(opts.ExtraIgnorePatterns))
	for _, pattern := range opts.ExtraIgnorePatterns {
		overrides = append(overrides, gitignore.ParsePattern(pattern, nil))
	}
	if file, err := os.Open(filepath.Join(root, ".crudehistoryignore")); err == nil {
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			overrides = append(overrides, gitignore.ParsePattern(scanner.Text(), nil))
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
	var repository []gitignore.Pattern
	if !opts.DisableGitIgnore {
		repository, err = repositoryIgnorePatterns(root, hard, defaults, overrides)
		if err != nil {
			return nil, err
		}
	}
	hard.matcher = gitignore.NewMatcher(slices.Concat(defaults, repository, overrides))
	return hard, nil
}

// repositoryIgnorePatterns reads Git's exclude and .gitignore files in one
// walk, parent directory before child. Later patterns take priority, so a
// deeper file overrides its parents and Crude's own overrides stay last.
//
// Like Git, the walk does not enter an ignored directory. Patterns from a
// directory can match only paths inside it, so the skipped files could not
// change a result for any path that a scan visits. Reading them made each
// capture walk every ignored tree; on Windows that took seconds per turn.
func repositoryIgnorePatterns(root string, hard *ignoreMatcher, defaults, overrides []gitignore.Pattern) ([]gitignore.Pattern, error) {
	var patterns []gitignore.Pattern
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			return nil
		}
		var domain []string
		if path != root {
			if hard.hardExcluded(path) {
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			domain = strings.Split(filepath.ToSlash(rel), "/")
			if gitignore.NewMatcher(slices.Concat(defaults, patterns, overrides)).Match(domain, true) {
				return filepath.SkipDir
			}
		}
		for _, name := range repositoryIgnoreFiles {
			patterns = append(patterns, readIgnoreFile(filepath.Join(path, name), domain)...)
		}
		return nil
	})
	return patterns, err
}

// repositoryIgnoreFiles are read in each visited directory, in the order of
// go-git's gitignore.ReadPatterns.
var repositoryIgnoreFiles = []string{filepath.Join(".git", "info", "exclude"), ".gitignore"}

// readIgnoreFile reads one ignore file with the line rules of go-git's
// gitignore.ReadPatterns, which reads a whole tree and cannot read one file.
// As there, a file that cannot be read adds no patterns.
func readIgnoreFile(path string, domain []string) []gitignore.Pattern {
	file, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer file.Close()
	var patterns []gitignore.Pattern
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			patterns = append(patterns, gitignore.ParsePattern(line, domain))
		}
	}
	return patterns
}

func (m *ignoreMatcher) ignored(rel string, isDir bool) bool {
	if m == nil || rel == "." || rel == "" {
		return false
	}
	return m.hardExcluded(rel) || m.matcher.Match(strings.Split(filepath.ToSlash(rel), "/"), isDir)
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
