package projectpath

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	// PageSize caps returned directories, not merely the number of batches.
	PageSize = 512
	// ScanLimit caps raw entries read per request, including skipped offsets.
	ScanLimit = 100000
	readBatch = 128
)

type Entry struct {
	Name      string
	Path      string
	IsDir     bool
	IsSymlink bool
}

// Directory is an unsorted filesystem-order page. Offsets count raw entries,
// including files. They are best-effort cursors, not snapshots across mutations.
// NextOffset is -1 on EOF or the scan ceiling; Truncated is conservatively true
// when a page/scan ceiling is reached (even if the next read would be EOF).
type Directory struct {
	Path       string
	Entries    []Entry
	NextOffset int
	Truncated  bool
}

// Browse lists directories without sorting or loading the entire directory.
// Directory symlinks are navigable; their targets are validated again by Inspect
// when selected. Broken and inaccessible symlinks are omitted. Context is checked
// between bounded reads and entry lookups; it cannot interrupt an OS syscall.
func Browse(ctx context.Context, path string, offset int) (Directory, error) {
	if err := ctx.Err(); err != nil {
		return Directory{}, err
	}
	if offset < 0 || offset > ScanLimit {
		return Directory{}, fmt.Errorf("directory offset must be between 0 and %d", ScanLimit)
	}
	path, _, err := canonicalDirectory(path)
	if err != nil {
		return Directory{}, err
	}
	result := Directory{Path: path, NextOffset: -1}
	if offset == ScanLimit {
		result.Truncated = true
		return result, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return Directory{}, err
	}
	defer func() { _ = f.Close() }()
	position := 0
	for position < ScanLimit {
		if err := ctx.Err(); err != nil {
			return Directory{}, err
		}
		n := min(readBatch, ScanLimit-position)
		if position < offset {
			n = min(n, offset-position)
		}
		entries, readErr := f.ReadDir(n)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return Directory{}, readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return Directory{}, err
			}
			position++
			if position <= offset {
				continue
			}
			link := entry.Type()&os.ModeSymlink != 0
			isDir := entry.IsDir()
			child := filepath.Join(path, entry.Name())
			if link {
				info, err := os.Stat(child)
				isDir = err == nil && info.IsDir()
			}
			if !isDir {
				continue
			}
			result.Entries = append(result.Entries, Entry{Name: entry.Name(), Path: child, IsDir: true, IsSymlink: link})
			if len(result.Entries) == PageSize {
				result.Truncated = true
				if position < ScanLimit {
					result.NextOffset = position
				}
				return result, nil
			}
		}
		if errors.Is(readErr, io.EOF) {
			return result, nil
		}
	}
	result.Truncated = true
	return result, nil
}
