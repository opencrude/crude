// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
// Copyright 2025 OpenAI
// Copyright 2026 Fredrik Gustafsson
// SPDX-License-Identifier: Apache-2.0
//
// The Codex mode: the update logic of apply_patch in OpenAI Codex
// (https://github.com/openai/codex, codex-rs/apply-patch:
// streaming_parser.rs, file_update.rs, seek_sequence.rs, and text_file.rs)
// in its PreserveLineEndings update mode, ported to Go by Fredrik Gustafsson
// and changed to patch strings instead of files. See ../LICENSE-APACHE and
// NOTICE.

package codexio

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

// codexChunk is one "@@" section of an update diff.
type codexChunk struct {
	context    string
	hasContext bool
	oldLines   []string
	newLines   []string
	// contextPairs are the indexes in oldLines and newLines of lines parsed
	// as context, so they can be kept as they are in the file.
	contextPairs [][2]int
	endOfFile    bool
}

func (c *codexChunk) empty() bool {
	return len(c.oldLines) == 0 && len(c.newLines) == 0
}

func (c *codexChunk) pushContext(line string) {
	c.contextPairs = append(c.contextPairs, [2]int{len(c.oldLines), len(c.newLines)})
	c.oldLines = append(c.oldLines, line)
	c.newLines = append(c.newLines, line)
}

const emptyChunkMessage = "Update hunk does not contain any lines"

func unexpectedCodexLine(line string, n int) error {
	return hunkError(n, fmt.Sprintf("Unexpected line found in update hunk: '%s'. Every line should start with ' ' (context line), '+' (added line), or '-' (removed line)", line))
}

func missingCodexMarker(line string, n int) error {
	return hunkError(n, fmt.Sprintf("Expected update hunk to start with a @@ context marker, got: '%s'", line))
}

// parseCodexChunks parses the diff lines of one update the way the Codex
// streaming parser does.
func parseCodexChunks(path string, lines []string) ([]*codexChunk, error) {
	var chunks []*codexChunk
	last := func() *codexChunk {
		if len(chunks) == 0 {
			return nil
		}
		return chunks[len(chunks)-1]
	}
	current := func() *codexChunk {
		if len(chunks) == 0 {
			chunks = append(chunks, &codexChunk{})
		}
		return last()
	}
	for i, line := range lines {
		marker := strings.TrimRightFunc(line, unicode.IsSpace)
		isSection := marker == "@@" || strings.HasPrefix(marker, "@@ ")
		previous := last()
		if previous != nil && previous.endOfFile {
			if marker == "" {
				continue
			}
			if !isSection {
				return nil, missingCodexMarker(line, i+1)
			}
		}
		switch {
		case isSection:
			if previous != nil && previous.empty() {
				return nil, unexpectedCodexLine(line, i+1)
			}
			context, hasContext := strings.CutPrefix(marker, "@@ ")
			chunks = append(chunks, &codexChunk{context: context, hasContext: hasContext})
		case marker == endFile:
			if previous != nil && previous.empty() {
				return nil, hunkError(i+1, emptyChunkMessage)
			}
			if previous != nil {
				previous.endOfFile = true
			}
		case line == "":
			current().pushContext("")
		case line[0] == ' ':
			current().pushContext(line[1:])
		case line[0] == '+':
			chunk := current()
			chunk.newLines = append(chunk.newLines, line[1:])
		case line[0] == '-':
			chunk := current()
			chunk.oldLines = append(chunk.oldLines, line[1:])
		case previous != nil && !previous.empty():
			return nil, missingCodexMarker(line, i+1)
		default:
			return nil, unexpectedCodexLine(line, i+1)
		}
	}
	switch {
	case len(chunks) == 0:
		return nil, hunkError(0, fmt.Sprintf("Update file hunk for path '%s' is empty", path))
	case last().empty():
		return nil, hunkError(len(lines)+1, emptyChunkMessage)
	}
	return chunks, nil
}

type replacement struct {
	start, oldLen int
	lines         []string
}

// sourceLine is one line of a file and its ending: "\n", "\r\n", "\r", or
// "" for a last line without one.
type sourceLine struct {
	text, ending string
}

// splitSource splits contents into lines, keeping each ending. The first
// ending is the preferred one for new lines; it is "\n" if there is none.
func splitSource(contents string) ([]sourceLine, string) {
	var lines []sourceLine
	preferred := ""
	start := 0
	for i := 0; i < len(contents); i++ {
		var ending string
		switch {
		case strings.HasPrefix(contents[i:], "\r\n"):
			ending = "\r\n"
		case contents[i] == '\r' || contents[i] == '\n':
			ending = contents[i : i+1]
		default:
			continue
		}
		if preferred == "" {
			preferred = ending
		}
		lines = append(lines, sourceLine{contents[start:i], ending})
		i += len(ending) - 1
		start = i + 1
	}
	if start < len(contents) {
		lines = append(lines, sourceLine{contents[start:], ""})
	}
	if preferred == "" {
		preferred = "\n"
	}
	return lines, preferred
}

// applyCodexChunks applies parsed chunks to the contents of path.
func applyCodexChunks(contents string, chunks []*codexChunk, path string, mode Mode) (string, error) {
	if !utf8.ValidString(contents) {
		//nolint:staticcheck // Upstream Codex diagnostic is intentionally byte-preserved.
		return "", fmt.Errorf("Failed to read file to update %s: stream did not contain valid UTF-8", path)
	}
	if mode == NormalizeToLf {
		return applyNormalized(contents, chunks, path)
	}
	source, preferred := splitSource(contents)
	texts := make([]string, len(source))
	for i, line := range source {
		texts[i] = line.text
	}
	replacements, err := codexReplacements(texts, chunks, path, mode)
	if err != nil {
		return "", err
	}
	var output strings.Builder
	index := 0
	write := func(line sourceLine) {
		output.WriteString(line.text)
		output.WriteString(cmp.Or(line.ending, preferred))
	}
	for _, r := range replacements {
		for _, line := range source[index:r.start] {
			write(line)
		}
		for _, text := range r.lines {
			write(sourceLine{text, preferred})
		}
		index = r.start + r.oldLen
	}
	for _, line := range source[index:] {
		write(line)
	}
	return output.String(), nil
}

// codexReplacements finds where each chunk applies. The replacements come
// back sorted by position and never overlap.
func codexReplacements(lines []string, chunks []*codexChunk, path string, mode Mode) ([]replacement, error) {
	var replacements []replacement
	cursor := 0
	for _, chunk := range chunks {
		if chunk.hasContext {
			index, ok := seekSequence(lines, []string{chunk.context}, cursor, false, mode)
			if !ok {
				//nolint:staticcheck // Upstream Codex diagnostic is intentionally byte-preserved.
				return nil, fmt.Errorf("Failed to find context '%s' in %s", chunk.context, path)
			}
			cursor = index + 1
		}
		if len(chunk.oldLines) == 0 {
			// Codex appends insertions without context to the end of the file,
			// even after an anchor.
			index := len(lines)
			if mode == NormalizeToLf && index > 0 && lines[index-1] == "" {
				index--
			}
			replacements = append(replacements, replacement{index, 0, chunk.newLines})
			continue
		}
		pattern, newLines := chunk.oldLines, chunk.newLines
		start, ok := seekSequence(lines, pattern, cursor, chunk.endOfFile, mode)
		if !ok && pattern[len(pattern)-1] == "" {
			// A trailing empty line in the diff stands for the final newline.
			pattern = pattern[:len(pattern)-1]
			if len(newLines) > 0 && newLines[len(newLines)-1] == "" {
				newLines = newLines[:len(newLines)-1]
			}
			start, ok = seekSequence(lines, pattern, cursor, chunk.endOfFile, mode)
		}
		if !ok {
			//nolint:staticcheck // Upstream Codex diagnostic is intentionally byte-preserved.
			return nil, fmt.Errorf("Failed to find expected lines in %s:\n%s", path, strings.Join(chunk.oldLines, "\n"))
		}
		if mode == NormalizeToLf {
			replacements = append(replacements, replacement{start, len(pattern), newLines})
			cursor = start + len(pattern)
			continue
		}
		// Keep context lines as they are in the file and replace only the
		// changed runs between them.
		oldStart, newStart := 0, 0
		for _, pair := range chunk.contextPairs {
			oldContext, newContext := pair[0], pair[1]
			if oldContext >= len(pattern) || newContext >= len(newLines) {
				break
			}
			if oldStart != oldContext || newStart != newContext {
				replacements = append(replacements,
					replacement{start + oldStart, oldContext - oldStart, newLines[newStart:newContext]})
			}
			oldStart, newStart = oldContext+1, newContext+1
		}
		if oldStart != len(pattern) || newStart != len(newLines) {
			replacements = append(replacements,
				replacement{start + oldStart, len(pattern) - oldStart, newLines[newStart:]})
		}
		cursor = start + len(pattern)
	}
	slices.SortStableFunc(replacements, func(a, b replacement) int { return cmp.Compare(a.start, b.start) })
	return replacements, nil
}

// lineMatchers are the comparisons seekSequence tries, from strictest to
// most lenient.
var lineMatchers = []func(a, b string) bool{
	func(a, b string) bool { return a == b },
	func(a, b string) bool {
		return strings.TrimRightFunc(a, unicode.IsSpace) == strings.TrimRightFunc(b, unicode.IsSpace)
	},
	func(a, b string) bool { return strings.TrimSpace(a) == strings.TrimSpace(b) },
	func(a, b string) bool { return normalizePunctuation(a) == normalizePunctuation(b) },
}

// seekSequence returns the first index at or after start where pattern
// matches lines, trying each matcher over the whole range before the next.
// An end-of-file pattern is only tried at the end.
func seekSequence(lines, pattern []string, start int, endOfFile bool, mode Mode) (int, bool) {
	if len(pattern) == 0 {
		return start, true
	}
	if len(pattern) > len(lines) {
		return 0, false
	}
	last := len(lines) - len(pattern)
	if endOfFile {
		if mode == NormalizeToLf {
			start = last
		} else {
			start = max(start, last)
		}
	}
	for _, same := range lineMatchers {
		for i := start; i <= last; i++ {
			if matchesAt(lines[i:], pattern, same) {
				return i, true
			}
		}
	}
	return 0, false
}

func matchesAt(lines, pattern []string, same func(a, b string) bool) bool {
	for i, line := range pattern {
		if !same(lines[i], line) {
			return false
		}
	}
	return true
}

// normalizePunctuation trims s and maps typographic dashes, quotes, and
// spaces to ASCII, like Codex's seek_sequence normalise.
func normalizePunctuation(s string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\u2010', '\u2011', '\u2012', '\u2013', '\u2014', '\u2015', '\u2212':
			return '-'
		case '\u2018', '\u2019', '\u201A', '\u201B':
			return '\''
		case '\u201C', '\u201D', '\u201E', '\u201F':
			return '"'
		case '\u00A0', '\u2002', '\u2003', '\u2004', '\u2005', '\u2006', '\u2007', '\u2008',
			'\u2009', '\u200A', '\u202F', '\u205F', '\u3000':
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
}
