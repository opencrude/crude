package core

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"crude/third_party/pathwalk"
	"crude/workspace"
)

// startupCatalog reads directory batches with a total visited-entry budget.
// The ordinary picker walker sorts complete directories before its callback;
// that is not suitable for a giant home/Downloads directory before attachment.
func startupCatalog(ctx context.Context, root string, hidden bool) (workspace.BrowserCatalog, error) {
	const maxEntries = 10_000
	const maxPathBytes = 2 << 20
	var out workspace.BrowserCatalog
	queue := []string{root}
	pathBytes := len(root)
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return out, err
		}
		directory := queue[0]
		queue[0] = ""
		queue = queue[1:]
		f, err := os.Open(directory)
		if err != nil {
			if directory == root {
				return out, err
			}
			out.Errors++
			continue
		}
		err = func() error {
			defer func() { _ = f.Close() }()
			for {
				if err := ctx.Err(); err != nil {
					return err
				}
				entries, readErr := f.ReadDir(min(128, maxEntries-out.Visited+1))
				for _, entry := range entries {
					if out.Visited == maxEntries {
						out.Truncated = true
						return nil
					}
					out.Visited++
					if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
						continue
					}
					name := entry.Name()
					if !hidden && strings.HasPrefix(name, ".") || skipStartupDirectory(name) {
						out.Skipped++
						continue
					}
					path := filepath.Join(directory, name)
					rel, relErr := filepath.Rel(root, path)
					if relErr != nil {
						return relErr
					}
					pathBytes += len(path)*2 + len(rel) + len(name)
					if pathBytes > maxPathBytes {
						out.Truncated = true
						return nil
					}
					out.Entries = append(out.Entries, pathwalk.Entry{Path: path, Relative: rel, Name: name, IsDir: true, Depth: strings.Count(rel, string(filepath.Separator)) + 1})
					queue = append(queue, path)
				}
				if errors.Is(readErr, io.EOF) {
					return nil
				}
				if readErr != nil {
					return readErr
				}
			}
		}()
		if err != nil {
			if ctx.Err() != nil || directory == root {
				return out, err
			}
			out.Errors++
		}
		if out.Truncated {
			break
		}
	}
	sort.Slice(out.Entries, func(i, j int) bool { return out.Entries[i].Path < out.Entries[j].Path })
	return out, nil
}

func skipStartupDirectory(name string) bool {
	switch name {
	case ".git", ".crude", ".crude-beta", "node_modules", "vendor", "target", "dist", "build", "out", "coverage", ".venv", "__pycache__":
		return true
	}
	return false
}
