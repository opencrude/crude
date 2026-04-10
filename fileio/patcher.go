package fileio

import (
	"fmt"
	"sort"
	"strings"
)

type SearchReplace struct {
	Search  string
	Replace string
}

func ApplySearchReplace(content string, ops []SearchReplace) (string, error) {
	result := content
	for i, op := range ops {
		if op.Search == "" {
			return "", fmt.Errorf("op[%d]: empty search string", i)
		}
		if op.Search == op.Replace {
			continue
		}
		applied, err := applySingleOp(result, op)
		if err != nil {
			return "", fmt.Errorf("op[%d]: %w", i, err)
		}
		result = applied
	}
	return result, nil
}

func applySingleOp(content string, op SearchReplace) (string, error) {
	n := strings.Count(content, op.Search)
	if n == 1 {
		return strings.Replace(content, op.Search, op.Replace, 1), nil
	}
	if n > 1 {
		return "", fmt.Errorf(
			"search text matches %d locations — add more surrounding lines to make it unique:\n  %s\nhint: each match must be unique for safety",
			n, preview(op.Search),
		)
	}

	result, err := normalisedReplace(content, op.Search, op.Replace)
	if err != nil {
		return "", fmt.Errorf(
			"search text not found (exact or whitespace-normalised):\n  %s\nhint: re-read the file with read_file and copy the exact text",
			preview(op.Search),
		)
	}
	return result, nil
}

func normalisedReplace(content, search, replace string) (string, error) {
	contentLines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	searchLines := strings.Split(strings.TrimSpace(strings.ReplaceAll(search, "\r\n", "\n")), "\n")
	if len(searchLines) == 0 {
		return "", fmt.Errorf("search is empty after normalisation")
	}

	trimSearch := make([]string, len(searchLines))
	for i, l := range searchLines {
		trimSearch[i] = strings.TrimSpace(l)
	}

	matchAt := -1
	for i := 0; i <= len(contentLines)-len(searchLines); i++ {
		ok := true
		for j, s := range trimSearch {
			if strings.TrimSpace(contentLines[i+j]) != s {
				ok = false
				break
			}
		}
		if ok {
			if matchAt != -1 {
				return "", fmt.Errorf("normalised search matches multiple locations — add more context lines to make it unique")
			}
			matchAt = i
		}
	}
	if matchAt == -1 {
		return "", fmt.Errorf("not found after whitespace normalisation")
	}

	indent := leadingWS(contentLines[matchAt])

	replaceLines := strings.Split(replace, "\n")
	for k, rl := range replaceLines {
		trimmed := strings.TrimSpace(rl)
		if trimmed != "" {
			replaceLines[k] = indent + trimmed
		} else {
			replaceLines[k] = ""
		}
	}

	out := make([]string, 0, len(contentLines)-len(searchLines)+len(replaceLines))
	out = append(out, contentLines[:matchAt]...)
	out = append(out, replaceLines...)
	out = append(out, contentLines[matchAt+len(searchLines):]...)
	return strings.Join(out, "\n"), nil
}

type AnchorEdit struct {
	Anchor  string
	NewText string
	Delete  bool
}

func ApplyAnchorEdits(content string, edits []AnchorEdit) (string, error) {
	if len(edits) == 0 {
		return content, nil
	}

	trailingNL := strings.HasSuffix(content, "\n")
	lines := splitLines(content)

	type resolved struct {
		anchor Anchor
		edit   AnchorEdit
	}
	resolved_ := make([]resolved, len(edits))
	for i, e := range edits {
		a, err := ParseAnchor(e.Anchor)
		if err != nil {
			return "", fmt.Errorf("anchor edit[%d]: %w", i, err)
		}
		if err := ValidateAnchor(a, lines); err != nil {
			return "", fmt.Errorf("anchor edit[%d]: %w", i, err)
		}
		resolved_[i] = resolved{a, e}
	}

	seen := make(map[int]bool, len(edits))
	for i, r := range resolved_ {
		if seen[r.anchor.Line] {
			return "", fmt.Errorf("anchor edit[%d]: duplicate edit on line %d — each line can only be edited once per batch", i, r.anchor.Line)
		}
		seen[r.anchor.Line] = true
	}

	sort.Slice(resolved_, func(i, j int) bool {
		return resolved_[i].anchor.Line > resolved_[j].anchor.Line
	})

	for _, r := range resolved_ {
		idx := r.anchor.Line - 1
		if r.edit.Delete {
			lines = append(lines[:idx], lines[idx+1:]...)
		} else if strings.Contains(r.edit.NewText, "\n") {
			newLines := strings.Split(r.edit.NewText, "\n")
			lines = append(lines[:idx], append(newLines, lines[idx+1:]...)...)
		} else {
			lines[idx] = r.edit.NewText
		}
	}

	return joinLines(lines, trailingNL), nil
}

func leadingWS(s string) string {
	for i, r := range s {
		if r != ' ' && r != '\t' {
			return s[:i]
		}
	}
	return s
}

func preview(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.SplitN(s, "\n", 4)
	if len(lines) > 3 {
		lines = append(lines[:3], "…")
	}
	return strings.Join(lines, "\n  ")
}
