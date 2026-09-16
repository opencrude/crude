package fileio

import (
	"fmt"
	"strings"

	"crude/logger"
)

type SearchReplace struct {
	Search  string
	Replace string
}

func ApplySearchReplace(content string, ops []SearchReplace, lg *logger.Logger) (string, error) {
	bom := ""
	if strings.HasPrefix(content, "\uFEFF") {
		bom = "\uFEFF"
		content = content[len(bom):]
	}
	useCRLF := strings.Contains(content, "\r\n")
	lg.Debug("DIFF", "apply_enter", "use_crlf", useCRLF, "has_bom", bom != "", "ops", len(ops), "content_len", len(content))

	result := strings.ReplaceAll(content, "\r\n", "\n")
	if result != content {
		lg.Debug("DIFF", "normalized_to_lf", "original_len", len(content), "normalized_len", len(result))
	}
	for i, op := range ops {
		if op.Search == "" {
			lg.Error("DIFF", "empty_search", "op_idx", i)
			return "", fmt.Errorf("op[%d]: empty search string", i)
		}
		if op.Search == op.Replace {
			lg.Debug("DIFF", "skip_noop", "op_idx", i, "search_len", len(op.Search))
			continue
		}
		lg.Debug("DIFF", "apply_op", "op_idx", i, "search_len", len(op.Search), "replace_len", len(op.Replace), "content_len_before", len(result))
		applied, err := applySingleOp(result, op, lg)
		if err != nil {
			lg.Error("DIFF", "op_failed", "op_idx", i, "error", err.Error())
			return "", fmt.Errorf("op[%d]: %w", i, err)
		}
		lg.Debug("DIFF", "op_success", "op_idx", i, "result_len", len(applied))
		result = applied
	}
	if useCRLF {
		result = strings.ReplaceAll(result, "\n", "\r\n")
		lg.Debug("DIFF", "restored_crlf", "len", len(result))
	}
	return bom + result, nil
}

func applySingleOp(content string, op SearchReplace, lg *logger.Logger) (string, error) {
	search := strings.ReplaceAll(op.Search, "\r\n", "\n")
	replace := strings.ReplaceAll(op.Replace, "\r\n", "\n")

	if search == "" {
		if lg != nil {
			lg.Error("DIFF", "empty_search", "error", "old_string cannot be empty")
		}
		return "", fmt.Errorf("old_string cannot be empty")
	}

	contentLines := strings.Split(content, "\n")
	fullSearchLines := strings.Split(search, "\n")
	trimmedSearchLines := trimBlankEdges(fullSearchLines)
	replaceLines := trimMirroredEdges(fullSearchLines, strings.Split(replace, "\n"))

	lg.Debug("DIFF", "apply_single", "content_lines", len(contentLines), "search_lines", len(fullSearchLines), "trimmed_lines", len(trimmedSearchLines), "replace_lines", len(replaceLines))

	if len(trimmedSearchLines) == 0 {
		lg.Error("DIFF", "trimmed_to_empty", "error", "search is only whitespace after trimming blank edges")
		return "", fmt.Errorf("search is only whitespace after trimming blank edges")
	}

	if hits := findMatches(contentLines, trimmedSearchLines, func(a, b string) bool { return a == b }); len(hits) == 1 {
		lg.Debug("DIFF", "phase1_exact_trimmed", "match_line", hits[0]+1, "search_lines", len(trimmedSearchLines))
		return splice(contentLines, hits[0], len(trimmedSearchLines), replaceLines), nil
	} else if len(hits) > 1 {
		lg.Warn("DIFF", "phase1_ambiguous", "match_count", len(hits), "locations", multiMatchLocations(hits))
		return "", multiMatchErr(hits, "exactly", op.Search)
	} else {
		lg.Debug("DIFF", "phase1_no_match", "desc", "proceeding to phase2")
	}

	if hits := findMatches(contentLines, trimmedSearchLines, func(a, b string) bool {
		return strings.TrimSpace(a) == strings.TrimSpace(b)
	}); len(hits) == 1 {
		reIndented := reindentBlock(contentLines, trimmedSearchLines, hits[0], replaceLines)
		origLen := len(replaceLines)
		newLen := len(reIndented)
		changed := 0
		for i := 0; i < origLen && i < newLen; i++ {
			if reIndented[i] != replaceLines[i] {
				changed++
			}
		}
		if changed > 0 {
			lg.Debug("DIFF", "phase2_reindented", "match_line", hits[0]+1, "lines_changed", changed, "total_lines", newLen)
		}
		lg.Debug("DIFF", "phase2_ws_normalized", "match_line", hits[0]+1, "reindent_type", "trimmed")
		return splice(contentLines, hits[0], len(trimmedSearchLines), reIndented), nil
	} else if len(hits) > 1 {
		lg.Warn("DIFF", "phase2_ambiguous", "match_count", len(hits), "locations", multiMatchLocations(hits))
		return "", multiMatchErr(hits, "after whitespace normalisation", op.Search)
	} else {
		lg.Debug("DIFF", "phase2_no_match", "desc", "proceeding to phase3")
	}

	if len(fullSearchLines) != len(trimmedSearchLines) {
		lg.Debug("DIFF", "phase3_trying_full", "full_lines", len(fullSearchLines), "trimmed_lines", len(trimmedSearchLines))
		if hits := findMatches(contentLines, fullSearchLines, func(a, b string) bool { return a == b }); len(hits) == 1 {
			lg.Debug("DIFF", "phase3_exact_full", "match_line", hits[0]+1, "search_lines", len(fullSearchLines))
			return splice(contentLines, hits[0], len(fullSearchLines), replaceLines), nil
		} else if len(hits) > 1 {
			lg.Warn("DIFF", "phase3_ambiguous", "match_count", len(hits), "locations", multiMatchLocations(hits))
			return "", multiMatchErr(hits, "exactly (untrimmed)", op.Search)
		} else {
			lg.Debug("DIFF", "phase3_no_match", "desc", "proceeding to phase4")
		}

		if hits := findMatches(contentLines, fullSearchLines, func(a, b string) bool {
			return strings.TrimSpace(a) == strings.TrimSpace(b)
		}); len(hits) == 1 {
			reIndented := reindentBlock(contentLines, fullSearchLines, hits[0], replaceLines)
			changed := 0
			origLen := len(replaceLines)
			newLen := len(reIndented)
			for i := 0; i < origLen && i < newLen; i++ {
				if reIndented[i] != replaceLines[i] {
					changed++
				}
			}
			if changed > 0 {
				lg.Debug("DIFF", "phase4_reindented", "match_line", hits[0]+1, "lines_changed", changed)
			}
			lg.Debug("DIFF", "phase4_ws_normalized_full", "match_line", hits[0]+1)
			return splice(contentLines, hits[0], len(fullSearchLines), reIndented), nil
		} else if len(hits) > 1 {
			lg.Warn("DIFF", "phase4_ambiguous", "match_count", len(hits), "locations", multiMatchLocations(hits))
			return "", multiMatchErr(hits, "after whitespace normalisation (untrimmed)", op.Search)
		} else {
			lg.Debug("DIFF", "phase4_no_match", "desc", "proceeding to phase5")
		}
	} else {
		lg.Debug("DIFF", "phase3_skip", "desc", "full and trimmed have same line count")
	}

	subHits := findSubstringMatches(contentLines, trimmedSearchLines)
	if len(subHits) == 1 {
		start := subHits[0]
		lg.Debug("DIFF", "phase5_substring", "match_line", start+1, "search_lines", len(trimmedSearchLines))
		if len(trimmedSearchLines) == 1 {
			replaced, ok := substringReplaceInLine(contentLines[start], trimmedSearchLines[0], replaceLines)
			if ok {
				lg.Debug("DIFF", "phase5_inline_replace", "line", start+1)
				contentLines[start] = replaced
				return strings.Join(contentLines, "\n"), nil
			}
		}
		reIndented := reindentBlock(contentLines, trimmedSearchLines, start, replaceLines)
		lg.Debug("DIFF", "phase5_ws_adjusted", "match_line", start+1, "reindented", reIndented[0] != replaceLines[0])
		return splice(contentLines, start, len(trimmedSearchLines), reIndented), nil
	} else if len(subHits) > 1 {
		lg.Warn("DIFF", "phase5_ambiguous", "match_count", len(subHits), "locations", multiMatchLocations(subHits))
		return "", multiMatchErr(subHits, "by substring", op.Search)
	} else {
		lg.Debug("DIFF", "phase5_no_match", "desc", "all phases exhausted")
	}

	lg.Error("DIFF", "all_phases_failed", "search_preview", preview(op.Search))
	return "", fmt.Errorf(
		"search text not found (tried exact match, whitespace-normalised match, and substring match):\n  %s\n\n"+
			"ⓘ  HINT: Copy FULL lines from read_file output — from the START of each line including all indentation.\n"+
			"ⓘ  Do NOT start your search in the middle of a line",
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

// trimMirroredEdges drops from the replacement the same leading/trailing blank
// lines that trimBlankEdges removed from the search.
//
// Tool arguments carry a trailing newline, which strings.Split turns into a
// blank edge line. Trimming only the search would splice that extra blank line
// into the file. Mirroring the trim also keeps a deliberately added blank line
// (search "a\n" → replace "a\n\n") intact: one edge line is removed from each
// side, so the replacement still contributes exactly one blank line.
func trimMirroredEdges(search, replace []string) []string {
	leading, trailing := 0, 0
	for leading < len(search) && strings.TrimSpace(search[leading]) == "" {
		leading++
	}
	for trailing < len(search)-leading && strings.TrimSpace(search[len(search)-1-trailing]) == "" {
		trailing++
	}
	for leading > 0 && len(replace) > 0 && strings.TrimSpace(replace[0]) == "" {
		replace = replace[1:]
		leading--
	}
	for trailing > 0 && len(replace) > 0 && strings.TrimSpace(replace[len(replace)-1]) == "" {
		replace = replace[:len(replace)-1]
		trailing--
	}
	return replace
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
// reindentBlock adjusts replacement indentation by comparing each search line's
// leading whitespace against the corresponding content line's leading whitespace.
//
// When the model provides a search pattern starting mid-block (si == ""), some
// continuation lines may already carry absolute file indentation while others are
// at column 0 (relative). The per-line approach uses the content line's actual
// leading WS (cws) as the target indent for each position where we have that info.
// Lines where the search and content share the same leading WS (sws == cws) are
// "absolute" — the model already placed them at the correct file depth.
// Lines where sws != cws (search at col 0, content indented) are "relative" —
// the model placed them at column 0 and they need the content's indent applied.
func reindentBlock(contentLines, searchLines []string, matchStart int, replaceLines []string) []string {
	if len(replaceLines) == 0 {
		return replaceLines
	}
	mi := leadingWS(contentLines[matchStart])
	si := leadingWS(searchLines[0])

	if mi == si {
		return replaceLines
	}

	ri0 := leadingWS(replaceLines[0])

	if ri0 == mi {
		return replaceLines
	}

	crossType := detectCrossType(si, mi)

	mixedIndent := crossType
	if !mixedIndent {
		for i := 1; i < len(searchLines) && matchStart+i < len(contentLines); i++ {
			sws := leadingWS(searchLines[i])
			cws := leadingWS(contentLines[matchStart+i])
			if sws == cws {
				mixedIndent = true
				break
			}
		}
	}

	if !mixedIndent {
		if ri0 == si {
			return upgradeIndent(replaceLines, si, mi)
		}
		if ri0 == "" {
			return addIndent(replaceLines, mi)
		}
		return replaceLines
	}

	// Per-line reindent using content WS as the target.
	fileUnit := detectFileIndentUnit(contentLines, matchStart, len(searchLines))
	// For cross-type, compute the indent step: how many search-WS characters
	// correspond to one file indent level. Derived from the search→content
	// depth relationship across matched lines.
	indentStep := 0
	if crossType {
		indentStep = computeIndentStep(searchLines, contentLines, matchStart, fileUnit)
	}
	out := make([]string, len(replaceLines))
	for i, rl := range replaceLines {
		if rl == "" {
			out[i] = ""
			continue
		}
		rlws := leadingWS(rl)
		hasContentRef := i < len(searchLines) && matchStart+i < len(contentLines)
		if hasContentRef {
			sws := leadingWS(searchLines[i])
			cws := leadingWS(contentLines[matchStart+i])
			if crossType {
				if rlws == "" {
					out[i] = cws + rl
				} else {
					delta := len(rlws) - len(sws)
					if delta == 0 {
						out[i] = cws + rl[len(rlws):]
					} else if indentStep > 0 {
						depthAdj := delta / indentStep
						remainder := delta % indentStep
						if remainder != 0 {
							out[i] = cws + rl[len(rlws):]
						} else {
							target := adjustWS(cws, depthAdj, fileUnit)
							out[i] = target + rl[len(rlws):]
						}
					} else {
						out[i] = cws + rl[len(rlws):]
					}
				}
			} else if sws == cws {
				switch rlws {
				case cws:
					out[i] = rl
				case "":
					out[i] = cws + rl
				default:
					out[i] = rl
				}
			} else {
				if rlws == "" {
					out[i] = cws + rl
				} else if strings.HasPrefix(rlws, si) && si != "" {
					out[i] = cws + rl[len(si):]
				} else if rlws == cws {
					out[i] = rl
				} else {
					out[i] = rl
				}
			}
		} else if crossType {
			out[i] = reindentLineByContent(rl, contentLines, matchStart, len(searchLines), fileUnit, si, mi, indentStep)
		} else {
			if rlws == "" {
				out[i] = mi + rl
			} else {
				out[i] = rl
			}
		}
	}
	return out
}

// computeIndentStep determines how many characters of search-WS correspond
// to one file indent level. It scans matched search/content line pairs and
// finds the smallest positive indent difference in search-WS characters
// that corresponds to one fileUnit step.
func computeIndentStep(searchLines, contentLines []string, matchStart int, fileUnit string) int {
	best := 0
	for i := 1; i < len(searchLines) && matchStart+i < len(contentLines); i++ {
		swsPrev := leadingWS(searchLines[i-1])
		cwsPrev := leadingWS(contentLines[matchStart+i-1])
		swsCur := leadingWS(searchLines[i])
		cwsCur := leadingWS(contentLines[matchStart+i])
		sDelta := len(swsCur) - len(swsPrev)
		cDelta := len(cwsCur) - len(cwsPrev)
		if sDelta > 0 && cDelta > 0 && cDelta == len(fileUnit) {
			if best == 0 || sDelta < best {
				best = sDelta
			}
		}
	}
	if best > 0 {
		return best
	}
	// Fallback: infer from first line ratio. si and mi represent the
	// same logical depth in different WS types. If mi has N units and
	// si has S chars, then 1 file-level = S/N search-chars (rounded).
	si := leadingWS(searchLines[0])
	mi := leadingWS(contentLines[matchStart])
	_ = mi
	if len(si) > 0 && len(fileUnit) > 0 {
		fileDepth := len(leadingWS(contentLines[matchStart])) / len(fileUnit)
		if fileDepth > 0 {
			step := len(si) / fileDepth
			if step > 0 {
				return step
			}
		}
		return len(si)
	}
	return len(fileUnit) * 2
}

// adjustWS adds or removes file indent levels from a whitespace prefix.
func adjustWS(ws string, delta int, unit string) string {
	if delta == 0 {
		return ws
	}
	if delta > 0 {
		return ws + strings.Repeat(unit, delta)
	}
	// delta < 0: remove |delta| units from the end of ws
	removal := len(unit) * (-delta)
	if removal >= len(ws) {
		return ""
	}
	return ws[:len(ws)-removal]
}

func detectCrossType(si, mi string) bool {
	if si == "" || mi == "" {
		return false
	}
	siHasTabs := strings.Contains(si, "\t")
	miHasTabs := strings.Contains(mi, "\t")
	return siHasTabs != miHasTabs
}

// detectFileIndentUnit determines the file's indentation unit by computing
// the GCD of indent-level differences across the matched content lines.
// Returns "\t" for tab-indented files or the space string for space-indented
// files (e.g., "    " for 4-space indent).
func detectFileIndentUnit(contentLines []string, matchStart, matchLen int) string {
	var prevLen int
	var diffs []int
	useTabs := false
	useSpaces := false
	for i := matchStart; i < matchStart+matchLen && i < len(contentLines); i++ {
		ws := leadingWS(contentLines[i])
		if strings.Contains(ws, "\t") {
			useTabs = true
		}
		if len(ws) > 0 && !strings.Contains(ws, "\t") {
			useSpaces = true
		}
		if prevLen > 0 && len(ws) > prevLen {
			diffs = append(diffs, len(ws)-prevLen)
		}
		prevLen = len(ws)
	}
	if useTabs && !useSpaces {
		return "\t"
	}
	if len(diffs) == 0 {
		if useSpaces {
			return "    "
		}
		return "\t"
	}
	g := diffs[0]
	for _, d := range diffs[1:] {
		g = gcd(g, d)
	}
	if g < 2 {
		g = 2
	}
	return strings.Repeat(" ", g)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// reindentLineByContent reindents a replacement line that has no direct
// search/content reference. It looks for a content line with matching
// non-whitespace content in the vicinity; if found, uses that line's WS.
// Otherwise, computes the depth from the file's indent unit.
func reindentLineByContent(line string, contentLines []string, matchStart, matchLen int, fileUnit, si, mi string, indentStep int) string {
	rlws := leadingWS(line)
	body := strings.TrimSpace(line)
	if body == "" {
		return ""
	}
	if rlws == "" {
		return mi + line
	}
	for j := matchStart; j < matchStart+matchLen && j < len(contentLines); j++ {
		if strings.TrimSpace(contentLines[j]) == body {
			cws := leadingWS(contentLines[j])
			return cws + body
		}
	}
	if idx := matchStart + matchLen; idx < len(contentLines) {
		if strings.TrimSpace(contentLines[idx]) == body {
			cws := leadingWS(contentLines[idx])
			return cws + body
		}
	}
	if indentStep > 0 && len(si) > 0 {
		depthRel := (len(rlws) - len(si)) / indentStep
		baseDepth := len(mi) / len(fileUnit)
		if len(fileUnit) == 0 {
			return line
		}
		targetDepth := baseDepth + depthRel
		if targetDepth < 0 {
			targetDepth = 0
		}
		return strings.Repeat(fileUnit, targetDepth) + body
	}
	return mi + body
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

func multiMatchLocations(hits []int) string {
	locs := make([]string, len(hits))
	for i, h := range hits {
		locs[i] = fmt.Sprintf("%d", h+1)
	}
	return "lines " + strings.Join(locs, ",")
}

func preview(s string) string {
	s = strings.TrimSpace(s)
	lines := strings.SplitN(s, "\n", 4)
	if len(lines) > 3 {
		lines = append(lines[:3], "…")
	}
	return strings.Join(lines, "\n  ")
}
