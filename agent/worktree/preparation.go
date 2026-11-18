package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

// Failed setup can leave no checkout or a partially materialized checkout. It
// is not runnable, but its regular-file remnants are still reviewable evidence.
func (s *Service) preparationRemnants(ctx context.Context, c Context) (map[string]File, error) {
	if c.Mode != Isolated || c.Root != filepath.Join(s.store, c.ID, "checkout") {
		return nil, errors.New("invalid preparation artifact root")
	}
	if _, err := os.Lstat(c.Root); errors.Is(err, os.ErrNotExist) {
		return map[string]File{}, nil
	} else if err != nil {
		return nil, err
	}
	root, err := canonical(c.Root)
	if err != nil || root != c.Root {
		return nil, errors.New("preparation checkout replaced by symlink")
	}
	// Audit all regular files including ignored output, without relying on a
	// usable Git index. Git's own pointer/administration is never a source file.
	return s.capture(ctx, c.Root, c.CommonGitDir, nil, nil, false)
}

func (s *Service) cleanupPreparation(ctx context.Context, c Context) error {
	files, err := s.preparationRemnants(ctx, c)
	if err != nil {
		return err
	}
	c.ArchivedResult = files
	if err := s.save(c); err != nil {
		return err
	}
	current, err := s.preparationRemnants(ctx, c)
	if err != nil {
		return err
	}
	if !equalArchive(current, files) {
		return errors.New("preparation changed during archive; checkout retained")
	}
	if _, err := os.Lstat(filepath.Join(c.Root, ".git")); err == nil {
		if err := s.verifyCheckout(ctx, c); err != nil {
			return err
		}
		// Only failed, exact reviewed and durably archived setup reaches this.
		if _, err := s.git(ctx, s.root, "worktree", "remove", "--force", c.Root); err != nil {
			return err
		}
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.RemoveAll(c.Root); err != nil {
			return err
		}
	} else {
		return err
	}
	c.Removed = true
	return s.save(c)
}
