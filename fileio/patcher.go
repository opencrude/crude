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
	bom := ""
	if strings.HasPrefix(content, "\uFEFF") {
		bom = "\uFEFF"
		content = content[len(bom):]
	}
	useCRLF := strings.Contains(content, "\r\n")

	result := strings.ReplaceAll(content, "\r\n", "\n")
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
	if useCRLF {
		result = strings.ReplaceAll(result, "\n", "\r\n")
	}
	return bom + result, nil
}

func applySingleOp(content string, op SearchReplace) (string, error) {
	search := strings.ReplaceAll(op.Search, "\r\n", "\n")
	replace := strings.ReplaceAll(op.Replace, "\r\n", "\n")

	if search == "" {
		return "", fmt.Errorf("old_string cannot be empty")
	}

	contentLines := strings.Split(content, "\n")
	fullSearchLines := strings.Split(search, "\n")
	trimmedSearchLines := trimBlankEdges(fullSearchLines)
	replaceLines := strings.Split(replace, "\n")

	if len(trimmedSearchLines) == 0 {
		return "", fmt.Errorf("search is only whitespace after trimming blank edges")
	}

	// Phase 1: Exact match with trimmed search (handles accidental leading/trailing blank lines in search)
	if hits := findMatches(contentLines, trimmedSearchLines, func(a, b string) bool { return a == b }); len(hits) == 1 {
		return splice(contentLines, hits[0], len(trimmedSearchLines), replaceLines), nil
	} else if len(hits) > 1 {
		return "", multiMatchErr(hits, "exactly", op.Search)
	}

	// Phase 2: WS-normalized match with trimmed search + re-indentation
	if hits := findMatches(contentLines, trimmedSearchLines, func(a, b string) bool {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}); len(hits) == 1 {
		absCtx := hasAbsoluteSearchIndent(contentLines, trimmedSearchLines, hits[0])
		reIndented := reindent(contentLines[hits[0]], trimmedSearchLines[0], replaceLines, absCtx)
		return splice(contentLines, hits[0], len(trimmedSearchLines), reIndented), nil
	} else if len(hits) > 1 {
		return "", multiMatchErr(hits, "after whitespace normalisation", op.Search)
	}

	// Phase 3: Exact match with full search (for intentional blank lines in search)
	if len(fullSearchLines) != len(trimmedSearchLines) {
		if hits := findMatches(contentLines, fullSearchLines, func(a, b string) bool { return a == b }); len(hits) == 1 {
			return splice(contentLines, hits[0], len(fullSearchLines), replaceLines), nil
		} else if len(hits) > 1 {
			return "", multiMatchErr(hits, "exactly (untrimmed)", op.Search)
		}

		// Phase 4: WS-normalized match with full search + re-indentation
		if hits := findMatches(contentLines, fullSearchLines, func(a, b string) bool {
			return strings.TrimSpace(a) == strings.TrimSpace(b)
		}); len(hits) == 1 {
			absCtx := hasAbsoluteSearchIndent(contentLines, fullSearchLines, hits[0])
			reIndented := reindent(contentLines[hits[0]], fullSearchLines[0], replaceLines, absCtx)
			return splice(contentLines, hits[0], len(fullSearchLines), reIndented), nil
		} else if len(hits) > 1 {
			return "", multiMatchErr(hits, "after whitespace normalisation (untrimmed)", op.Search)
		}
	}

	// Phase 5: Substring within-line matching. Each trimmedSearchLines line
	// must occur as a substring inside the corresponding contentLines line.
	subHits := findSubstringMatches(contentLines, trimmedSearchLines)
	if len(subHits) == 1 {
		start := subHits[0]
		if len(trimmedSearchLines) == 1 {
			replaced, ok := substringReplaceInLine(contentLines[start], trimmedSearchLines[0], replaceLines)
			if ok {
				contentLines[start] = replaced
				return strings.Join(contentLines, "\n"), nil
			}
		}
		absCtx := hasAbsoluteSearchIndent(contentLines, trimmedSearchLines, start)
		reIndented := reindent(contentLines[start], trimmedSearchLines[0], replaceLines, absCtx)
		return splice(contentLines, start, len(trimmedSearchLines), reIndented), nil
	} else if len(subHits) > 1 {
		return "", multiMatchErr(subHits, "by substring", op.Search)
	}

	return "", fmt.Errorf(
		"search text not found (tried exact match, whitespace-normalised match, and substring match):\n  %s\n\n"+
			"ⓘ  HINT: Copy FULL lines from read_file output — from the START of each line including all indentation.\n"+
			"ⓘ  Do NOT start your search in the middle of a line.\n"+
			"ⓘ  Try delete_lines or replace_lines if you know the lines before/after but not the exact content.",
		preview(op.Search),
	)
}

func trimBlankEdges(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func findMatches(contentLines, searchLines []string, eq func(a, b string) bool) []int {
	n := len(searchLines)
	if n == 0 || n > len(contentLines) {
		return nil
	}
	var hits []int
	for i := 0; i <= len(contentLines)-n; i++ {
		ok := true
		for j := 0; j < n; j++ {
			if !eq(contentLines[i+j], searchLines[j]) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, i)
		}
	}
	return hits
}

// findSubstringMatches finds line-aligned positions where every search
// line is a (non-trimmed-space) substring of the corresponding content line.
func findSubstringMatches(contentLines, searchLines []string) []int {
	sn := len(searchLines)
	if sn == 0 || sn > len(contentLines) {
		return nil
	}
	var hits []int
	for i := 0; i <= len(contentLines)-sn; i++ {
		ok := true
		for j := 0; j < sn; j++ {
			jSearch := searchLines[j]
			jContent := contentLines[i+j]
			// If search part first/last chars are word-ish, use word-aware check
			if !safeSubstringMatch(jContent, jSearch) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, i)
		}
	}
	return hits
}

// safeSubstringMatch returns true iff search appears inside content, but
// rejects matches where the character before/after would produce a partial
// token (e.g. changing a to x inside bar).  This is a best-effort safety.
func safeSubstringMatch(content, search string) bool {
	if search == "" {
		return true
	}
	idx := strings.Index(content, search)
	if idx == -1 {
		return false
	}
	// check for char before and after
	before, after := false, false
	if idx > 0 {
		before = isCodeChar(content[idx-1])
	}
	end := idx + len(search)
	if end < len(content) {
		after = isCodeChar(content[end])
	}
	// only accept if not surrounded by code chars on one side and a
	// code char on the other (prevents splitting identifiers)
	if before && after {
		// both sides are code chars → could be inside a bigger identifier
		// but if search itself is a complete statement (has = or : or () → accept)
		if !strings.ContainsAny(search, "=:") && !strings.Contains(search, "(") {
			return false
		}
	}
	return true
}

func isCodeChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}

func substringReplaceInLine(contentLine, searchLine string, replaceLines []string) (string, bool) {
	// Only accept single-line replacement for in-line substring replacement.
	if len(replaceLines) != 1 {
		return "", false
	}
	idx := strings.Index(contentLine, searchLine)
	if idx == -1 {
		return "", false
	}
	return contentLine[:idx] + replaceLines[0] + contentLine[idx+len(searchLine):], true
}

// reindent adjusts replacement indentation based on the relationship between
// the matched content line's indent and the original search line's indent.
// absCtx indicates that some continuation search lines share the same leading
// whitespace as their corresponding content lines (absolute indentation mode).
// When absCtx is true and the first replacement line starts at column 0,
// only column-0 replacement lines get the matched indent added; lines that
// already carry indentation are left as-is.
func reindent(matchedLine, searchLine string, replaceLines []string, absCtx bool) []string {
	if len(replaceLines) == 0 {
		return replaceLines
	}
	mi, si := leadingWS(matchedLine), leadingWS(searchLine)
	if mi == si {
		return replaceLines
	}
	ri0 := leadingWS(replaceLines[0])

	// Replacement already absolutely indented to matched level → pass through.
	if ri0 == mi {
		return replaceLines
	}

	// Replacement indented to search level → upgrade whole block.
	if ri0 == si {
		// When si == "" (search starts at column 0) and absCtx is true,
		// continuation lines in the replacement already carry absolute file
		// indentation. Only add mi to column-0 lines, not to all lines.
		if si == "" && absCtx {
			return addIndentToBaseline(replaceLines, mi)
		}
		return upgradeIndent(replaceLines, si, mi)
	}

	// Replacement starts unindented at top level → add matched indent everywhere,
	// unless absCtx tells us continuation lines already have absolute indentation.
	if ri0 == "" {
		if absCtx {
			return addIndentToBaseline(replaceLines, mi)
		}
		return addIndent(replaceLines, mi)
	}

	// Replacement has its own indentation intent we don't understand → leave as-is.
	return replaceLines
}

// addIndentToBaseline adds indent only to replacement lines that have no leading
// whitespace (the column-0 baseline). Lines that already carry indentation are
// left untouched — they represent absolute file positions, not relative offsets.
func addIndentToBaseline(lines []string, indent string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = ""
		} else if leadingWS(l) == "" {
			out[i] = indent + l
		} else {
			out[i] = l
		}
	}
	return out
}

// hasAbsoluteSearchIndent returns true when any continuation search line (i>0)
// has the same leading whitespace as the corresponding content line. This
// indicates that the search pattern embeds absolute file indentation rather
// than relative indentation from column 0.
func hasAbsoluteSearchIndent(contentLines, searchLines []string, matchStart int) bool {
	for i := 1; i < len(searchLines); i++ {
		sws := leadingWS(searchLines[i])
		if sws != "" && matchStart+i < len(contentLines) {
			cws := leadingWS(contentLines[matchStart+i])
			if sws == cws {
				return true
			}
		}
	}
	return false
}

func upgradeIndent(lines []string, from, to string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = ""
			continue
		}
		if from != "" && strings.HasPrefix(l, from) {
			out[i] = to + l[len(from):]
		} else {
			out[i] = to + l
		}
	}
	return out
}

func addIndent(lines []string, indent string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		if l == "" {
			out[i] = ""
		} else {
			out[i] = indent + l
		}
	}
	return out
}

func leadingWS(s string) string {
	for i := 0; i < len(s); i++ {
		if s[i] != ' ' && s[i] != '\t' {
			return s[:i]
		}
	}
	return s
}

func splice(lines []string, start, count int, replacement []string) string {
	out := make([]string, 0, len(lines)-count+len(replacement))
	out = append(out, lines[:start]...)
	out = append(out, replacement...)
	out = append(out, lines[start+count:]...)
	return strings.Join(out, "\n")
}

func multiMatchErr(hits []int, mode, search string) error {
	locs := make([]string, len(hits))
	for i, h := range hits {
		locs[i] = fmt.Sprintf("line %d", h+1)
	}
	return fmt.Errorf(
		"search matches %d locations %s (%s) — add more surrounding FULL lines (from start of line) to make it unique:\n  %s",
		len(hits), mode, strings.Join(locs, ", "), preview(search),
	)
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
