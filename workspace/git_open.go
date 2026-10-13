package workspace

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/storage/filesystem"
	"github.com/go-git/go-git/v5/storage/filesystem/dotgit"
)

// openGitRepository is go-git's PlainOpenWithOptions(DetectDotGit,
// EnableDotGitCommonDir) without its leaked commondir handle: go-git v5 opens
// "commondir" and never closes it, so every snapshot of a linked worktree kept
// a handle that, on Windows, blocks git worktree remove/prune and deletion.
func openGitRepository(path string) (*git.Repository, error) {
	plain, err := git.PlainOpenWithOptions(path, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, err
	}
	storage, ok := plain.Storer.(*filesystem.Storage)
	if !ok {
		return plain, nil
	}
	dot := storage.Filesystem()
	raw, err := os.ReadFile(filepath.Join(dot.Root(), "commondir"))
	if errors.Is(err, os.ErrNotExist) {
		return plain, nil
	}
	closeGitRepository(plain)
	if err != nil {
		return nil, err
	}
	common := strings.TrimSpace(string(raw))
	if common == "" {
		return openGitStorage(dot, nil, plain)
	}
	if !filepath.IsAbs(common) {
		common = filepath.Join(dot.Root(), common)
	}
	if _, err := os.Stat(common); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, git.ErrRepositoryIncomplete
		}
		return nil, err
	}
	return openGitStorage(dot, osfs.New(common), plain)
}

func openGitStorage(dot, common billy.Filesystem, plain *git.Repository) (*git.Repository, error) {
	worktree, err := plain.Worktree()
	if err != nil {
		return nil, err
	}
	repository := dot
	if common != nil {
		repository = dotgit.NewRepositoryFilesystem(dot, common)
	}
	return git.Open(filesystem.NewStorage(repository, cache.NewObjectLRUDefault()), worktree.Filesystem)
}
