package fileio

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// WalkProjectFiles visits bounded regular text files below root. It uses the
// same hidden/dependency exclusions as project search and never follows links.
// The callback owns the bytes only for the duration of the call.
func WalkProjectFiles(ctx context.Context, root string, limit int64, visit func(path string, data []byte) error) error {
	if limit <= 0 {
		return errors.New("project file byte limit must be positive")
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}
	return filepath.WalkDir(absRoot, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if entry.IsDir() {
			if path != absRoot && shouldSkipDir(entry.Name(), false) {
				return fs.SkipDir
			}
			return nil
		}
		if !entry.Type().IsRegular() || strings.HasPrefix(entry.Name(), ".") {
			return nil
		}
		info, err := entry.Info()
		if err != nil || info.Size() > limit {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil || len(data) > int(limit) || bytes.IndexByte(data[:min(len(data), 512)], 0) >= 0 {
			return nil
		}
		return visit(path, data)
	})
}
