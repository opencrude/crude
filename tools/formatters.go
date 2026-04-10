package tools

import (
	"fmt"
	"strings"

	"crude/fileio"
)

func formatReadFile(path, content string, offset, limit int) string {
	lines := strings.Split(content, "\n")
	total := len(lines)

	if total == 1 && lines[0] == "" {
		return fmt.Sprintf("%s — (empty file)", path)
	}

	startIdx := 0
	if offset > 1 {
		startIdx = offset - 1
	}
	if startIdx >= total {
		return fmt.Sprintf("Error: offset %d exceeds file length (%d lines)", offset, total)
	}

	endIdx := total
	if limit > 0 && startIdx+limit < total {
		endIdx = startIdx + limit
	}

	slice := lines[startIdx:endIdx]
	var sb strings.Builder

	if offset > 1 || limit > 0 {
		fmt.Fprintf(&sb, "Lines %d–%d of %s (%d total):\n", startIdx+1, endIdx, path, total)
	}

	width := len(fmt.Sprintf("%d", endIdx))
	for i, line := range slice {
		fmt.Fprintf(&sb, "%*d  %s\n", width, startIdx+1+i, line)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func formatDirEntries(path string, entries []fileio.DirEntry) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d entries:\n", path, len(entries))
	for _, e := range entries {
		name := e.Name
		if e.IsDir {
			name += "/"
		} else if e.IsLink {
			name += "@"
		}
		modStr := ""
		if !e.ModTime.IsZero() {
			modStr = e.ModTime.Format("2006-01-02 15:04")
		}
		fmt.Fprintf(&sb, "%-12s  %8d  %s  %s\n", e.Mode, e.Size, modStr, name)
	}
	return strings.TrimRight(sb.String(), "\n")
}

func listDirectoryRecursive(path string, exec fileio.Executer) string {
	matches, err := exec.GlobFiles(path, "**")
	if err != nil {
		return "Error: " + err.Error()
	}
	if len(matches) == 0 {
		return fmt.Sprintf("%s — empty directory (no files found recursively)", path)
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "%s — %d entries (recursive):\n", path, len(matches))
	for _, m := range matches {
		sb.WriteString(m)
		sb.WriteByte('\n')
	}
	return strings.TrimRight(sb.String(), "\n")
}
