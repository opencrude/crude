package fileio

import (
	"fmt"
	"strings"
)

func anchorsStartAtLineBoundary(content string, prefix, suffix string) error {
	cLines := strings.Split(content, "\n")
	pLines := strings.Split(strings.ReplaceAll(prefix, "\r\n", "\n"), "\n")
	sLines := strings.Split(strings.ReplaceAll(suffix, "\r\n", "\n"), "\n")

	trimmedP := trimBlankEdges(pLines)
	trimmedS := trimBlankEdges(sLines)

	if len(trimmedP) > 0 {
		firstP := trimmedP[0]
		line, col := findAnchorPos(cLines, firstP)
		if col > 0 {
			return fmt.Errorf(
				"delete_lines/replace_lines are for LINE-LEVEL operations — "+
					"prefix MUST start at the beginning of a line.\n\n"+
					"Your prefix anchor starts mid-line at position:\n"+
					"  file line %d, column %d: %q\n\n"+
					"To fix this, copy the ENTIRE line from read_file output starting at column 0, "+
					"including all leading whitespace (spaces/tabs).\n\n"+
					"Alternatively, use edit_file for substring-level edits inside a line.",
				line+1, col, preview(firstP),
			)
		}
		if col == -1 {
			return fmt.Errorf(
				"prefix anchor %q not found anywhere on any line — re-read the file with read_file and copy exact text",
				preview(firstP),
			)
		}
	}
	if len(trimmedS) > 0 {
		firstS := trimmedS[0]
		line, col := findAnchorPos(cLines, firstS)
		if col > 0 {
			return fmt.Errorf(
				"delete_lines/replace_lines are for LINE-LEVEL operations — "+
					"suffix MUST start at the beginning of a line.\n\n"+
					"Your suffix anchor starts mid-line at position:\n"+
					"  file line %d, column %d: %q\n\n"+
					"To fix this, copy the ENTIRE line from read_file output starting at column 0, "+
					"including all leading whitespace (spaces/tabs).\n\n"+
					"Alternatively, use edit_file for substring-level edits inside a line.",
				line+1, col, preview(firstS),
			)
		}
		if col == -1 {
			return fmt.Errorf(
				"suffix anchor %q not found anywhere on any line — re-read the file with read_file and copy exact text",
				preview(firstS),
			)
		}
	}
	return nil
}

func findAnchorPos(cLines []string, searchLine string) (line, col int) {
	for i, l := range cLines {
		idx := strings.Index(l, searchLine)
		if idx != -1 {
			return i, idx
		}
	}
	return -1, -1
}

type anchorMatch struct {
	startLine int
	endLine   int
}

func findAnchor(contentLines, searchLines []string, label string) (anchorMatch, error) {
	trimmedSearch := trimBlankEdges(searchLines)

	if len(trimmedSearch) == 0 {
		return anchorMatch{}, fmt.Errorf("%s is only whitespace after trimming blank edges", label)
	}

	// Phase 1: Exact match
	if hits := findMatches(contentLines, trimmedSearch, func(a, b string) bool { return a == b }); len(hits) == 1 {
		return anchorMatch{startLine: hits[0], endLine: hits[0] + len(trimmedSearch)}, nil
	} else if len(hits) > 1 {
		return anchorMatch{}, fmt.Errorf(
			"%s matches %d locations exactly — add more surrounding lines to make it unique:\n  %s",
			label, len(hits), preview(strings.Join(trimmedSearch, "\n")),
		)
	}

	// Phase 2: WS-normalized match
	if hits := findMatches(contentLines, trimmedSearch, func(a, b string) bool {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}); len(hits) == 1 {
		return anchorMatch{startLine: hits[0], endLine: hits[0] + len(trimmedSearch)}, nil
	} else if len(hits) > 1 {
		return anchorMatch{}, fmt.Errorf(
			"%s matches %d locations after whitespace normalisation — add more surrounding lines to make it unique:\n  %s",
			label, len(hits), preview(strings.Join(trimmedSearch, "\n")),
		)
	}

	// Phase 3: Full search (untrimmed) exact
	if len(searchLines) != len(trimmedSearch) {
		if hits := findMatches(contentLines, searchLines, func(a, b string) bool { return a == b }); len(hits) == 1 {
			return anchorMatch{startLine: hits[0], endLine: hits[0] + len(searchLines)}, nil
		} else if len(hits) > 1 {
			return anchorMatch{}, fmt.Errorf(
				"%s matches %d locations exactly (untrimmed) — add more surrounding lines to make it unique:\n  %s",
				label, len(hits), preview(strings.Join(searchLines, "\n")),
			)
		}

		// Phase 4: WS-normalized full search
		if hits := findMatches(contentLines, searchLines, func(a, b string) bool {
			return strings.TrimSpace(a) == strings.TrimSpace(b)
		}); len(hits) == 1 {
			return anchorMatch{startLine: hits[0], endLine: hits[0] + len(searchLines)}, nil
		} else if len(hits) > 1 {
			return anchorMatch{}, fmt.Errorf(
				"%s matches %d locations after whitespace normalisation (untrimmed) — add more surrounding lines:\n  %s",
				label, len(hits), preview(strings.Join(searchLines, "\n")),
			)
		}
	}

	// Phase 5: Substring within-line matching
	subHits := findSubstringMatches(contentLines, trimmedSearch)
	if len(subHits) == 1 {
		return anchorMatch{startLine: subHits[0], endLine: subHits[0] + len(trimmedSearch)}, nil
	} else if len(subHits) > 1 {
		return anchorMatch{}, fmt.Errorf(
			"%s matches %d locations by substring — add more surrounding lines to make it unique:\n  %s",
			label, len(subHits), preview(strings.Join(trimmedSearch, "\n")),
		)
	}

	return anchorMatch{}, fmt.Errorf(
		"%s not found in file (tried exact match, whitespace-normalised match, and substring match):\n  %s\n"+
			"hint: re-read the file with read_file and copy the EXACT text including all whitespace. "+
			"Provide full lines from the start of each line — do NOT start from the middle of a line.",
		label, preview(strings.Join(trimmedSearch, "\n")),
	)
}

func DeleteLines(content string, prefix, suffix string) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("prefix snippet cannot be empty")
	}
	if suffix == "" {
		return "", fmt.Errorf("suffix snippet cannot be empty")
	}

	useCRLF := strings.Contains(content, "\r\n")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	prefix = strings.ReplaceAll(prefix, "\r\n", "\n")
	suffix = strings.ReplaceAll(suffix, "\r\n", "\n")

	if err := anchorsStartAtLineBoundary(content, prefix, suffix); err != nil {
		return "", err
	}

	contentLines := strings.Split(content, "\n")
	prefixLines := strings.Split(prefix, "\n")
	suffixLines := strings.Split(suffix, "\n")

	pMatch, err := findAnchor(contentLines, prefixLines, "prefix")
	if err != nil {
		return "", fmt.Errorf("delete_lines: %w", err)
	}

	afterPrefix := contentLines[pMatch.endLine:]
	sMatch, err := findAnchor(afterPrefix, suffixLines, "suffix")
	if err != nil {
		return "", fmt.Errorf("delete_lines: %w", err)
	}

	suffixStart := pMatch.endLine + sMatch.startLine

	if suffixStart < pMatch.endLine {
		return "", fmt.Errorf(
			"delete_lines: suffix anchor overlaps with or appears before prefix anchor — "+
				"ensure suffix comes AFTER prefix in the file",
		)
	}

	var result strings.Builder
	for i, line := range contentLines {
		if i < pMatch.endLine || i >= suffixStart {
			if i > 0 {
				result.WriteByte('\n')
			}
			result.WriteString(line)
		}
	}

	out := result.String()
	if useCRLF {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, nil
}

func ReplaceLines(content string, prefix, suffix, newText string) (string, error) {
	if prefix == "" {
		return "", fmt.Errorf("prefix snippet cannot be empty")
	}
	if suffix == "" {
		return "", fmt.Errorf("suffix snippet cannot be empty")
	}

	useCRLF := strings.Contains(content, "\r\n")
	content = strings.ReplaceAll(content, "\r\n", "\n")
	prefix = strings.ReplaceAll(prefix, "\r\n", "\n")
	suffix = strings.ReplaceAll(suffix, "\r\n", "\n")
	newText = strings.ReplaceAll(newText, "\r\n", "\n")

	if err := anchorsStartAtLineBoundary(content, prefix, suffix); err != nil {
		return "", err
	}

	contentLines := strings.Split(content, "\n")
	prefixLines := strings.Split(prefix, "\n")
	suffixLines := strings.Split(suffix, "\n")

	pMatch, err := findAnchor(contentLines, prefixLines, "prefix")
	if err != nil {
		return "", fmt.Errorf("replace_lines: %w", err)
	}

	afterPrefix := contentLines[pMatch.endLine:]
	sMatch, err := findAnchor(afterPrefix, suffixLines, "suffix")
	if err != nil {
		return "", fmt.Errorf("replace_lines: %w", err)
	}

	suffixStart := pMatch.endLine + sMatch.startLine

	if suffixStart < pMatch.endLine {
		return "", fmt.Errorf(
			"replace_lines: suffix anchor overlaps with or appears before prefix anchor — "+
				"ensure suffix comes AFTER prefix in the file",
		)
	}

	replaceSplit := strings.Split(newText, "\n")
	hasNewText := newText != ""

	var result strings.Builder
	for i, line := range contentLines {
		if i < pMatch.endLine {
			if i > 0 {
				result.WriteByte('\n')
			}
			result.WriteString(line)
			continue
		}
		if i == pMatch.endLine && hasNewText {
			for j, rl := range replaceSplit {
				if j > 0 || i > 0 {
					result.WriteByte('\n')
				}
				result.WriteString(rl)
			}
		}
		if i >= suffixStart {
			if i > 0 {
				result.WriteByte('\n')
			}
			result.WriteString(line)
		}
	}

	out := result.String()
	if useCRLF {
		out = strings.ReplaceAll(out, "\n", "\r\n")
	}
	return out, nil
}
