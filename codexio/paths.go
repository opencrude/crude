package codexio

import (
	"path/filepath"
	"strings"
)

func IsGPT(model string) bool { return strings.Contains(strings.ToLower(model), "gpt") }

// Paths returns every mutation operand, including move sources and destinations.
func (p *Patch) Paths(root string) []string {
	seen := map[string]bool{}
	var paths []string
	for _, op := range p.Operations {
		for _, path := range []string{op.Path, op.MoveTo} {
			if path == "" {
				continue
			}
			if !filepath.IsAbs(path) && root != "" {
				path = filepath.Join(root, path)
			}
			path = filepath.Clean(path)
			if !seen[path] {
				paths = append(paths, path)
				seen[path] = true
			}
		}
	}
	return paths
}

// Preview reports complete header lines only; it never authorizes execution.
func Preview(input string) (paths []string, added, removed int) {
	lines := strings.Split(input, "\n")
	for _, line := range lines[:len(lines)-1] {
		line = strings.TrimSuffix(line, "\r")
		for _, prefix := range []string{addFile, deleteFile, updateFile, moveTo} {
			if path, ok := strings.CutPrefix(line, prefix); ok {
				paths = append(paths, strings.TrimSpace(path))
			}
		}
		if strings.HasPrefix(line, "+") {
			added++
		}
		if strings.HasPrefix(line, "-") {
			removed++
		}
	}
	return
}
