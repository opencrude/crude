package workspace

import (
	"os"
	"path/filepath"
	"strings"
)

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// resolveLSPRoot honors language-specific markers, including wildcard project
// files, without descending into source trees. Workspace folders are a boundary,
// not permission to walk up to an unrelated parent repository or home directory.
func resolveLSPRoot(path, workspaceRoot string, markers []string) string {
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
	for _, marker := range markers {
		if marker != "go.work" && marker != "*.sln" && marker != "*.slnx" {
			continue
		}
		for _, current := range dirs {
			if lspMarkerExists(current, marker) {
				return fileURI(current)
			}
		}
	}
	for _, current := range dirs {
		for _, marker := range markers {
			if lspMarkerExists(current, marker) {
				return fileURI(current)
			}
		}
	}
	return fileURI(boundary)
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
