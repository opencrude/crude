package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// GitConflictChoice is how one conflict region is resolved.
type GitConflictChoice string

const (
	GitChooseNone     GitConflictChoice = ""
	GitChooseCurrent  GitConflictChoice = "current"  // keep HEAD's side
	GitChooseIncoming GitConflictChoice = "incoming" // take the merged-in side
	GitChooseBoth     GitConflictChoice = "both"     // current, then incoming
)

// GitConflictRegion is one <<<<<<< … >>>>>>> block. Lines keep their own line
// endings so a resolution reproduces the file byte for byte outside the
// chosen text.
type GitConflictRegion struct {
	Current, Base, Incoming []string
	CurrentLabel            string
	IncomingLabel           string
	// StartLine is the 0-based line of the <<<<<<< marker in the file.
	StartLine int
}

// GitConflictSegment is either unchanged text or a conflict region.
type GitConflictSegment struct {
	Text     []string
	Conflict *GitConflictRegion
}

// GitConflictFile is a conflicted working file parsed for resolution. The
// fingerprint pins the exact bytes the resolution was decided against.
type GitConflictFile struct {
	Path         string
	AbsolutePath string
	Fingerprint  string
	Segments     []GitConflictSegment
}

// Conflicts lists the file's conflict regions in order.
func (f GitConflictFile) Conflicts() []*GitConflictRegion {
	var out []*GitConflictRegion
	for _, segment := range f.Segments {
		if segment.Conflict != nil {
			out = append(out, segment.Conflict)
		}
	}
	return out
}

func gitContentFingerprint(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// splitKeepingEndings splits text into lines that retain "\n" or "\r\n".
func splitKeepingEndings(text string) []string {
	var lines []string
	for len(text) > 0 {
		index := strings.IndexByte(text, '\n')
		if index < 0 {
			lines = append(lines, text)
			break
		}
		lines = append(lines, text[:index+1])
		text = text[index+1:]
	}
	return lines
}

func markerLine(line, marker string) (string, bool) {
	trimmed := strings.TrimRight(line, "\r\n")
	if trimmed == marker {
		return "", true
	}
	if strings.HasPrefix(trimmed, marker+" ") {
		return strings.TrimSpace(trimmed[len(marker):]), true
	}
	return "", false
}

// ParseGitConflicts splits text into unchanged segments and conflict regions,
// accepting both the merge and diff3/zdiff3 marker styles. Unbalanced markers
// are an error: guessing would write a half-resolved file.
func ParseGitConflicts(text string) ([]GitConflictSegment, error) {
	lines := splitKeepingEndings(text)
	var segments []GitConflictSegment
	var plain []string
	flush := func() {
		if len(plain) > 0 {
			segments = append(segments, GitConflictSegment{Text: plain})
			plain = nil
		}
	}
	for i := 0; i < len(lines); i++ {
		label, open := markerLine(lines[i], "<<<<<<<")
		if !open {
			plain = append(plain, lines[i])
			continue
		}
		region := &GitConflictRegion{CurrentLabel: label, StartLine: i}
		section := &region.Current
		closed := false
		for i++; i < len(lines); i++ {
			line := lines[i]
			if _, nested := markerLine(line, "<<<<<<<"); nested {
				return nil, fmt.Errorf("line %d: nested conflict marker", i+1)
			}
			if _, base := markerLine(line, "|||||||"); base && section == &region.Current {
				section = &region.Base
				continue
			}
			if _, mid := markerLine(line, "======="); mid && section != &region.Incoming {
				section = &region.Incoming
				continue
			}
			if label, end := markerLine(line, ">>>>>>>"); end && section == &region.Incoming {
				region.IncomingLabel = label
				closed = true
				break
			}
			*section = append(*section, line)
		}
		if !closed {
			return nil, fmt.Errorf("line %d: conflict is not closed with >>>>>>>", region.StartLine+1)
		}
		flush()
		segments = append(segments, GitConflictSegment{Conflict: region})
	}
	flush()
	return segments, nil
}

// ResolveGitConflicts renders the file with one choice per conflict region.
// Every region must be decided.
func ResolveGitConflicts(segments []GitConflictSegment, choices []GitConflictChoice) (string, error) {
	var b strings.Builder
	index := 0
	for _, segment := range segments {
		if segment.Conflict == nil {
			for _, line := range segment.Text {
				b.WriteString(line)
			}
			continue
		}
		if index >= len(choices) {
			return "", errors.New("every conflict needs a choice")
		}
		var chosen []string
		switch choices[index] {
		case GitChooseCurrent:
			chosen = segment.Conflict.Current
		case GitChooseIncoming:
			chosen = segment.Conflict.Incoming
		case GitChooseBoth:
			chosen = append(append([]string(nil), segment.Conflict.Current...), ensureTrailingNewline(segment.Conflict.Current, segment.Conflict.Incoming)...)
		default:
			return "", fmt.Errorf("conflict %d has no choice", index+1)
		}
		for _, line := range chosen {
			b.WriteString(line)
		}
		index++
	}
	if index != len(choices) {
		return "", fmt.Errorf("%d choices for %d conflicts", len(choices), index)
	}
	return b.String(), nil
}

// ensureTrailingNewline keeps "both" from gluing the last current line to the
// first incoming line when the current side ends without a newline.
func ensureTrailingNewline(current, incoming []string) []string {
	if len(current) == 0 || strings.HasSuffix(current[len(current)-1], "\n") {
		return incoming
	}
	ending := "\n"
	for _, line := range current {
		if strings.HasSuffix(line, "\r\n") {
			ending = "\r\n"
			break
		}
	}
	out := append([]string(nil), incoming...)
	if len(out) > 0 {
		out[0] = ending + out[0]
	}
	return out
}
