package toolstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// ErrPreviewStale reports that a removal preview no longer matches the store.
var ErrPreviewStale = errors.New("the tools changed after the removal preview; preview the removal again")

// RemovalPreview is the exact set that Remove deletes. Token binds the tool
// IDs, each manifest, a fingerprint of each tree and the usage of each tool.
type RemovalPreview struct {
	Tools []Tool
	// Paths are the tool folders that Remove deletes.
	Paths []string
	// Bytes is the measured size of every regular file in Paths.
	Bytes int64
	Token string
}

// PreviewRemove previews the removal of the given tools.
func (s *Store) PreviewRemove(ctx context.Context, ids []string) (RemovalPreview, error) {
	if len(ids) == 0 {
		return RemovalPreview{}, errors.New("select at least one tool to remove")
	}
	if err := s.mu.LockContext(ctx); err != nil {
		return RemovalPreview{}, err
	}
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx); err != nil {
		return RemovalPreview{}, err
	}
	return s.previewLocked(ctx, ids)
}

// PreviewRemoveUnused previews the removal of every tool that no workspace
// uses. The preview is empty when every tool is in use.
func (s *Store) PreviewRemoveUnused(ctx context.Context) (RemovalPreview, error) {
	if err := s.mu.LockContext(ctx); err != nil {
		return RemovalPreview{}, err
	}
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx); err != nil {
		return RemovalPreview{}, err
	}
	var ids []string
	for id := range s.index {
		if len(s.usage.Tools[id].Workspaces) == 0 {
			ids = append(ids, id)
		}
	}
	if len(ids) == 0 {
		return RemovalPreview{}, nil
	}
	return s.previewLocked(ctx, ids)
}

func (s *Store) previewLocked(ctx context.Context, ids []string) (RemovalPreview, error) {
	unique := append([]string(nil), ids...)
	sort.Strings(unique)
	token := sha256.New()
	_, _ = token.Write([]byte("toolstore-removal-v1\n"))
	var preview RemovalPreview
	for i, id := range unique {
		if err := ctx.Err(); err != nil {
			return RemovalPreview{}, err
		}
		if i > 0 && unique[i-1] == id {
			continue
		}
		tool, ok := s.index[id]
		if !ok {
			return RemovalPreview{}, fmt.Errorf("no installed tool has the ID %q", id)
		}
		manifestData, err := readBoundedContext(ctx, filepath.Join(tool.Dir, manifestName), maxManifestBytes)
		if err != nil {
			return RemovalPreview{}, fmt.Errorf("read the manifest of %s: %w", id, err)
		}
		manifestDigest := sha256.Sum256(manifestData)
		usage, err := json.Marshal(s.usage.Tools[id])
		if err != nil {
			return RemovalPreview{}, err
		}
		_, _ = fmt.Fprintf(token, "tool %q\nmanifest %x\nusage %s\n", id, manifestDigest, usage)
		size, err := fingerprintTree(ctx, token, tool.Dir)
		if err != nil {
			return RemovalPreview{}, fmt.Errorf("measure %s: %w", id, err)
		}
		preview.Tools = append(preview.Tools, s.withUsageLocked(tool))
		preview.Paths = append(preview.Paths, tool.Dir)
		preview.Bytes += size
	}
	if err := ctx.Err(); err != nil {
		return RemovalPreview{}, err
	}
	preview.Token = hex.EncodeToString(token.Sum(nil))
	return preview, nil
}

// fingerprintTree writes the path, type, permissions, size, modification time
// and link target of every entry below dir into h. It returns the size of the
// regular files. Links are not followed.
func fingerprintTree(ctx context.Context, h hash.Hash, dir string) (int64, error) {
	var size int64
	err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
		}
		target := ""
		if info.Mode()&fs.ModeSymlink != 0 {
			if target, err = os.Readlink(name); err != nil {
				return err
			}
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
		_, _ = fmt.Fprintf(h, "entry %q %s %d %d %q\n", filepath.ToSlash(rel), info.Mode(), info.Size(), info.ModTime().UnixNano(), target)
		return nil
	})
	if err == nil {
		err = ctx.Err()
	}
	return size, err
}

// Remove deletes exactly the tools of a preview. It fails closed with
// ErrPreviewStale when a tool, its files or its usage changed after the
// preview. It moves every tool folder into staging first and moves them back
// when a move fails or ctx ends, so a removal applies to all tools or none.
// It restores write permission only inside the previewed folders.
func (s *Store) Remove(ctx context.Context, p RemovalPreview, actor string) error {
	if strings.TrimSpace(actor) == "" {
		return errors.New("removal requires an actor")
	}
	if len(p.Tools) == 0 || p.Token == "" {
		return errors.New("the removal preview is empty; there is nothing to remove")
	}
	ids := make([]string, 0, len(p.Tools))
	keys := make([]string, 0, len(p.Tools))
	for _, tool := range p.Tools {
		ids = append(ids, tool.ID)
		keys = append(keys, installKey(tool.Package, tool.Version))
	}
	unlock, err := s.lockKeys(ctx, keys)
	if err != nil {
		return fmt.Errorf("the removal was cancelled: %w", err)
	}
	defer unlock()
	if err := s.mu.LockContext(ctx); err != nil {
		return err
	}
	defer s.mu.Unlock()
	if err := s.refreshLocked(ctx); err != nil {
		return err
	}
	current, err := s.previewLocked(ctx, ids)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%w: %v", ErrPreviewStale, err)
	}
	if !reflect.DeepEqual(current, p) {
		return ErrPreviewStale
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("the removal was cancelled: %w", err)
	}

	trash, err := os.MkdirTemp(filepath.Join(s.root, stagingName), "remove-")
	if err != nil {
		return fmt.Errorf("create staging folder: %w", err)
	}
	type move struct{ from, to string }
	var moved []move
	rollback := func() error {
		var failed error
		for i := len(moved) - 1; i >= 0; i-- {
			if err := os.Rename(moved[i].to, moved[i].from); err != nil {
				failed = errors.Join(failed, err)
				continue
			}
			failed = errors.Join(failed, sealDir(moved[i].from))
		}
		if failed == nil {
			failed = os.Remove(trash)
		}
		return failed
	}
	for i, tool := range current.Tools {
		if err := ctx.Err(); err != nil {
			return errors.Join(fmt.Errorf("the removal was cancelled: %w", err), rollback())
		}
		// Moving a folder to another parent needs write permission on it.
		if err := unsealDir(tool.Dir); err != nil {
			return errors.Join(fmt.Errorf("remove %s: %w", tool.ID, err), rollback())
		}
		to := filepath.Join(trash, strconv.Itoa(i))
		if err := os.Rename(tool.Dir, to); err != nil {
			_ = sealDir(tool.Dir)
			return errors.Join(fmt.Errorf("remove %s: %w", tool.ID, err), rollback())
		}
		moved = append(moved, move{from: tool.Dir, to: to})
	}

	if err := ctx.Err(); err != nil {
		return errors.Join(err, rollback())
	}
	previousUsage := make(map[string]usageEntry)
	for _, id := range ids {
		if entry, ok := s.usage.Tools[id]; ok {
			previousUsage[id] = entry
			delete(s.usage.Tools, id)
		}
	}
	if err := s.writeUsageLocked(); err != nil {
		for id, entry := range previousUsage {
			s.usage.Tools[id] = entry
		}
		return errors.Join(err, rollback())
	}

	var failed error
	for _, tool := range current.Tools {
		delete(s.index, tool.ID)
		failed = errors.Join(failed, s.appendLocked(Event{
			Kind:    EventRemove,
			ToolID:  tool.ID,
			Package: tool.Package,
			Version: tool.Version,
			By:      actor,
			Detail:  fmt.Sprintf("Removed %s (%d bytes of content).", tool.Dir, tool.Size),
		}))
		_ = syncDir(filepath.Dir(tool.Dir))
		// Remove the package folder only when it is now empty.
		_ = os.Remove(filepath.Dir(tool.Dir))
	}
	if err := removeTree(trash); err != nil {
		failed = errors.Join(failed, fmt.Errorf("the tools are removed, but their files could not be deleted from %s. Inspect that folder: %w", trash, err))
	}
	return failed
}
