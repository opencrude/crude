package workspace

import (
	"crude/internal/fileuri"
	"os"
	"path/filepath"
	"strings"
)

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveLSPRoot honors language-specific markers, including wildcard project
// files, without descending into source trees. Workspace markers (from the
// catalog) win over nearer markers. Workspace folders are a boundary, not
// permission to walk up to an unrelated parent repository or home directory.
func resolveLSPRoot(path, workspaceRoot string, markers, workspaceMarkers []string) string {
	dir := filepath.Dir(path)
	boundary := workspaceRoot
	if boundary == "" || boundary == "." || !pathWithin(boundary, dir) {
		boundary = dir
	}
	var dirs []string
	for current := dir; ; current = filepath.Dir(current) {
		dirs = append(dirs, current)
		if current == boundary || filepath.Dir(current) == current {
			break
		}
	}
	// Workspace markers take precedence over nearer package/module markers.
	for _, marker := range workspaceMarkers {
		for _, current := range dirs {
			if lspMarkerExists(current, marker) {
				return fileuri.FromPath(current)
			}
		}
	}
	for _, current := range dirs {
		for _, marker := range markers {
			if lspMarkerExists(current, marker) {
				return fileuri.FromPath(current)
			}
		}
	}
	return fileuri.FromPath(boundary)
}

func lspMarkerExists(dir, marker string) bool {
	if marker == "" || filepath.IsAbs(marker) || strings.Contains(marker, "..") {
		return false
	}
	if strings.ContainsAny(marker, "*?[") {
		// ReadDir is bounded by one ancestor, never a recursive discovery scan.
		entries, err := os.ReadDir(dir)
		if err != nil {
			return false
		}
		for _, entry := range entries {
			if ok, _ := filepath.Match(marker, entry.Name()); ok {
				return true
			}
		}
		return false
	}
	_, err := os.Stat(filepath.Join(dir, marker))
	return err == nil
}
