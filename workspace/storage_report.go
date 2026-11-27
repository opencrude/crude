package workspace

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Storage categories, in the order the Storage tab shows them.
const (
	StorageSessions  = "sessions"
	StorageSubagents = "subagents"
	StorageTemporary = "temporary"
	StorageHistory   = "history"
	StorageWorktrees = "worktrees"
	StorageIndex     = "index"
	StorageOther     = "other"
)

// StorageReport is how much disk space Crude keeps for one workspace, by
// what the space holds. It measures the workspace folder under the Crude
// home, never the project itself.
type StorageReport struct {
	GeneratedAt int64             `json:"generated_unix_ns"`
	Bytes       int64             `json:"bytes"`
	Categories  []StorageCategory `json:"categories"`
}

// StorageCategory is one kind of saved data. Items counts sessions,
// sub-agents, worktrees or files, as the category names. Oldest and Newest
// are modification times. Reclaimable is what the category's cleanup can
// remove now; a category without a cleanup reports zero.
type StorageCategory struct {
	ID          string `json:"id"`
	Bytes       int64  `json:"bytes"`
	Items       int    `json:"items"`
	Oldest      int64  `json:"oldest_unix_ns,omitempty"`
	Newest      int64  `json:"newest_unix_ns,omitempty"`
	Reclaimable int64  `json:"reclaimable,omitempty"`
}

// SetItems replaces how many items a category holds, for counts that only
// the category's owner knows.
func (r *StorageReport) SetItems(id string, items int) {
	for i := range r.Categories {
		if r.Categories[i].ID == id {
			r.Categories[i].Items = items
		}
	}
}

// MeasureStorage walks workspaceDir. Sessions in open hold temporary files
// that a cleanup must keep, so their size is not reclaimable.
func MeasureStorage(ctx context.Context, workspaceDir string, open map[string]bool) (StorageReport, error) {
	categories := map[string]*StorageCategory{}
	for _, id := range []string{StorageSessions, StorageSubagents, StorageTemporary, StorageHistory, StorageWorktrees, StorageIndex, StorageOther} {
		categories[id] = &StorageCategory{ID: id}
	}
	add := func(id string, size int64, modTime time.Time) {
		category := categories[id]
		category.Bytes += size
		if at := modTime.UnixNano(); at > 0 {
			category.Oldest = earliest(category.Oldest, at)
			category.Newest = max(category.Newest, at)
		}
	}
	walk := func(id, path string, each func(size int64)) error {
		return filepath.WalkDir(path, func(_ string, entry fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
					return nil
				}
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if entry.IsDir() {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return nil
			}
			add(id, info.Size(), info.ModTime())
			if each != nil {
				each(info.Size())
			}
			return nil
		})
	}
	entries, err := os.ReadDir(workspaceDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return StorageReport{}, err
	}
	for _, entry := range entries {
		path := filepath.Join(workspaceDir, entry.Name())
		switch name := entry.Name(); {
		case name == "sessions" && entry.IsDir():
			if err := measureSessions(ctx, path, open, categories, add, walk); err != nil {
				return StorageReport{}, err
			}
		case name == "local-history" && entry.IsDir():
			if err := walk(StorageHistory, path, nil); err != nil {
				return StorageReport{}, err
			}
		case name == "executions" && entry.IsDir():
			// The execution owner counts the checkouts; see
			// StorageReport.SetItems.
			if err := walk(StorageWorktrees, path, nil); err != nil {
				return StorageReport{}, err
			}
		case strings.HasPrefix(name, "workspace.db"):
			if info, err := entry.Info(); err == nil {
				add(StorageIndex, info.Size(), info.ModTime())
			}
		default:
			if err := walk(StorageOther, path, nil); err != nil {
				return StorageReport{}, err
			}
		}
	}
	report := StorageReport{GeneratedAt: time.Now().UnixNano()}
	for _, id := range []string{StorageSessions, StorageSubagents, StorageTemporary, StorageHistory, StorageWorktrees, StorageIndex, StorageOther} {
		report.Bytes += categories[id].Bytes
		report.Categories = append(report.Categories, *categories[id])
	}
	return report, nil
}

func measureSessions(ctx context.Context, root string, open map[string]bool, categories map[string]*StorageCategory,
	add func(string, int64, time.Time), walk func(string, string, func(int64)) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		name, path := entry.Name(), filepath.Join(root, entry.Name())
		if !entry.IsDir() {
			if info, err := entry.Info(); err == nil {
				add(StorageSessions, info.Size(), info.ModTime())
			}
			if strings.HasSuffix(name, ".db") {
				categories[StorageSessions].Items++
			}
			continue
		}
		parts, err := os.ReadDir(path)
		if err != nil {
			continue
		}
		for _, part := range parts {
			partPath := filepath.Join(path, part.Name())
			switch {
			case part.Name() == "tmp" && part.IsDir():
				reclaim := !open[name]
				if err := walk(StorageTemporary, partPath, func(size int64) {
					if reclaim {
						categories[StorageTemporary].Reclaimable += size
					}
				}); err != nil {
					return err
				}
				categories[StorageTemporary].Items++
			case part.Name() == "subagents" && part.IsDir():
				if err := walk(StorageSubagents, partPath, nil); err != nil {
					return err
				}
				children, _ := os.ReadDir(partPath)
				for _, child := range children {
					if strings.HasSuffix(child.Name(), ".db") {
						categories[StorageSubagents].Items++
					}
				}
			case part.IsDir():
				if err := walk(StorageSessions, partPath, nil); err != nil {
					return err
				}
			default:
				if info, err := part.Info(); err == nil {
					add(StorageSessions, info.Size(), info.ModTime())
				}
			}
		}
	}
	return nil
}

// CleanupResult is what one Storage cleanup removed and what it kept. Kept
// counts items an owner refused; Reasons holds up to five of their reasons.
type CleanupResult struct {
	Kind       string   `json:"kind"`
	FreedBytes int64    `json:"freed_bytes"`
	Removed    int      `json:"removed"`
	Kept       int      `json:"kept,omitempty"`
	Reasons    []string `json:"reasons,omitempty"`
}

// Keep records one refused item and keeps a few distinct reasons.
func (r *CleanupResult) Keep(reason string) {
	r.Kept++
	if len(r.Reasons) < 5 && !slices.Contains(r.Reasons, reason) {
		r.Reasons = append(r.Reasons, reason)
	}
}

// RemoveTemporaryFiles deletes the tmp folder of every session that is not in
// open. Agents keep transient downloads, builds and evidence there; a session
// that opens again starts with an empty folder. A folder that cannot be
// removed is reported, and the others are still removed.
func RemoveTemporaryFiles(ctx context.Context, workspaceDir string, open map[string]bool) (CleanupResult, error) {
	result := CleanupResult{Kind: StorageTemporary}
	root := filepath.Join(workspaceDir, "sessions")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return result, nil
	}
	if err != nil {
		return result, err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		if !entry.IsDir() || open[entry.Name()] {
			continue
		}
		dir := filepath.Join(root, entry.Name(), "tmp")
		info, err := os.Lstat(dir)
		if err != nil || !info.IsDir() {
			continue // A link is never followed out of the workspace folder.
		}
		size := DirectorySize(dir)
		if err := os.RemoveAll(dir); err != nil {
			result.Keep("A file is in use: " + err.Error())
			result.FreedBytes += size - DirectorySize(dir)
			continue
		}
		result.FreedBytes += size
		result.Removed++
	}
	return result, nil
}

// DirectorySize is the total size of the regular files below dir. Links are
// counted as links, never followed.
func DirectorySize(dir string) int64 {
	var size int64
	_ = filepath.WalkDir(dir, func(_ string, item fs.DirEntry, err error) error {
		if err == nil && !item.IsDir() {
			if info, err := item.Info(); err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	return size
}
