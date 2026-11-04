package workspace

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"crude/textdiff"
)

// appendReviewedDiff uses the shared exact-byte line diff. CR and other control
// characters stay visible, including mixed line-ending changes; presentation
// never normalizes bytes that the immutable plan will change.
func appendReviewedDiff(path, before, after string, appendText func(string) error) error {
	hunks := textdiff.Hunks(before, after)
	if len(hunks) == 0 {
		return errors.New("the change has no reviewable lines")
	}
	label := path
	if strings.TrimSpace(path) != path || strings.ContainsFunc(path, reviewControl) {
		label = strconv.Quote(path)
	}
	added, removed := 0, 0
	for _, hunk := range hunks {
		added += len(hunk.NewLines)
		removed += len(hunk.OldLines)
	}
	if added > 0 {
		label += fmt.Sprintf("  +%d", added)
	}
	if removed > 0 {
		label += fmt.Sprintf("  -%d", removed)
	}
	if err := appendText(label + "\n"); err != nil {
		return err
	}
	for _, hunk := range hunks {
		line := hunk.OldStart + 1
		if len(hunk.OldLines) == 0 {
			line = hunk.NewStart + 1
		}
		if err := appendText(fmt.Sprintf("Line %d\n", line)); err != nil {
			return err
		}
		for _, side := range []struct {
			prefix string
			lines  []string
		}{{"- ", hunk.OldLines}, {"+ ", hunk.NewLines}} {
			for _, line := range side.lines {
				if len(line) > reviewedEditTextBytes {
					return errors.New("this change is too large to review here; make the change in smaller steps")
				}
				if err := appendText(side.prefix + reviewLine(strings.TrimSuffix(line, "\n")) + "\n"); err != nil {
					return err
				}
				if !strings.HasSuffix(line, "\n") {
					if err := appendText("  (no final newline)\n"); err != nil {
						return err
					}
				}
			}
		}
	}
	return appendText("\n")
}

func reviewControl(r rune) bool {
	return unicode.IsControl(r) || r == '\u2028' || r == '\u2029' ||
		r >= '\u202a' && r <= '\u202e' || r >= '\u2066' && r <= '\u2069'
}

func reviewLine(line string) string {
	var out strings.Builder
	for _, r := range line {
		if r != '\t' && reviewControl(r) {
			escaped := strconv.QuoteRune(r)
			out.WriteString(escaped[1 : len(escaped)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
