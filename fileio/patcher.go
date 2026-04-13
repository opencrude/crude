package fileio

import (
	"fmt"
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
	contentLines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	searchLines := strings.Split(strings.TrimSpace(strings.ReplaceAll(op.Search, "\r\n", "\n")), "\n")
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
				return "", fmt.Errorf(
					"search matches multiple locations — add more surrounding lines to make it unique:\n  %s\nhint: each match must be unique for safety",  preview(op.Search),
				)
			}
			matchAt = i
		}
	}
	if matchAt == -1 {
		return "", fmt.Errorf(
			"search text not found:\n  %s\nhint: re-read the file with read_file and copy the exact text",
			preview(op.Search),
		)
	}


	replaceLines := strings.Split(op.Replace, "\n")
	for k, rl := range replaceLines {
		trimmed := strings.TrimSpace(rl)
		if trimmed != "" {
			replaceLines[k] = rl
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

func preview(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.SplitN(s, "\n", 4)
	if len(lines) > 3 {
		lines = append(lines[:3], "…")
	}
	return strings.Join(lines, "\n  ")
}
