package pathwalk

import (
	"context"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	// DefaultWorkers is deliberately smaller than the upstream command-line
	// default. Picker scans must remain a low-priority background workload.
	DefaultWorkers      = 2
	maxWorkers          = 4
	DefaultMaxEntries   = 250_000
	DefaultMaxPathBytes = 32 << 20
)

// ErrEntryLimit indicates that a catalog stopped at its configured bound.
var ErrEntryLimit = errors.New("pathwalk: entry limit reached")

// Request describes one bounded metadata-only recursive scan.
type Request struct {
	Root            string
	DirectoriesOnly bool
	IncludeHidden   bool
	FollowSymlinks  bool
	MaxDepth        int
	MaxWorkers      int
	MaxEntries      int
	MaxPathBytes    int64
	SkipDir         func(path, name string) bool
}

// Entry is the presentation-neutral metadata needed by file-tree and picker
// path search. It intentionally contains no file contents or expensive stat
// metadata.
type Entry struct {
	Path      string
	Relative  string
	Name      string
	Depth     int
	IsDir     bool
	IsSymlink bool
}

// Catalog is a deterministic path snapshot returned by Scan.
type Catalog struct {
	Entries   []Entry
	Visited   int
	Skipped   int
	Errors    int
	Truncated bool
}

// Scan walks Request.Root and returns bounded path metadata. The callback in
// the underlying walker is concurrent, so collection is protected here and
// sorting is performed before returning.
func Scan(ctx context.Context, req Request) (Catalog, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	root, err := filepath.Abs(req.Root)
	if err != nil {
		return Catalog{}, err
	}
	root = filepath.Clean(root)
	if req.MaxWorkers <= 0 {
		req.MaxWorkers = DefaultWorkers
	}
	if req.MaxWorkers > maxWorkers {
		req.MaxWorkers = maxWorkers
	}
	if req.MaxEntries <= 0 {
		req.MaxEntries = DefaultMaxEntries
	}
	if req.MaxPathBytes <= 0 {
		req.MaxPathBytes = DefaultMaxPathBytes
	}

	var mu sync.Mutex
	cat := Catalog{Entries: make([]Entry, 0, minInt(req.MaxEntries, 4096))}
	var pathBytes int64
	conf := &Config{
		Follow:     req.FollowSymlinks,
		NumWorkers: req.MaxWorkers,
		Sort:       SortDirsFirst,
		MaxDepth:   req.MaxDepth,
	}
	err = WalkContext(ctx, conf, root, func(path string, d fs.DirEntry, walkErr error) error {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if walkErr != nil {
			mu.Lock()
			cat.Errors++
			mu.Unlock()
			if filepath.Clean(path) == root {
				return walkErr
			}
			return nil
		}
		if d == nil {
			return nil
		}
		cleanPath := filepath.Clean(path)
		if cleanPath == root {
			return nil
		}
		name := d.Name()
		isDir := d.IsDir()
		isSymlink := d.Type()&fs.ModeSymlink != 0
		if !req.IncludeHidden && strings.HasPrefix(name, ".") {
			mu.Lock()
			cat.Skipped++
			mu.Unlock()
			if isDir {
				return SkipDir
			}
			return nil
		}
		if isDir && req.SkipDir != nil && req.SkipDir(cleanPath, name) {
			mu.Lock()
			cat.Skipped++
			mu.Unlock()
			return SkipDir
		}
		if req.DirectoriesOnly && !isDir {
			return nil
		}
		rel, relErr := filepath.Rel(root, cleanPath)
		if relErr != nil {
			mu.Lock()
			cat.Errors++
			mu.Unlock()
			return nil
		}
		rel = filepath.ToSlash(rel)
		mu.Lock()
		defer mu.Unlock()
		if len(cat.Entries) >= req.MaxEntries || pathBytes+int64(len(rel)) > req.MaxPathBytes {
			cat.Truncated = true
			return ErrEntryLimit
		}
		cat.Entries = append(cat.Entries, Entry{
			Path: cleanPath, Relative: rel, Name: name, Depth: DirEntryDepth(d),
			IsDir: isDir, IsSymlink: isSymlink,
		})
		cat.Visited++
		pathBytes += int64(len(rel))
		return nil
	})
	if errors.Is(err, ErrEntryLimit) {
		return cat, nil
	}
	if err != nil {
		return cat, err
	}
	sort.Slice(cat.Entries, func(i, j int) bool {
		if cat.Entries[i].Relative != cat.Entries[j].Relative {
			return cat.Entries[i].Relative < cat.Entries[j].Relative
		}
		return cat.Entries[i].Path < cat.Entries[j].Path
	})
	return cat, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
