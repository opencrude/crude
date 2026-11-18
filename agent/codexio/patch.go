// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
// Copyright 2025 OpenAI
// Copyright 2026 Fredrik Gustafsson
// SPDX-License-Identifier: Apache-2.0
//
// The patch format of apply_patch in OpenAI Codex
// (https://github.com/openai/codex, codex-rs/apply-patch: parser.rs and
// streaming_parser.rs), ported to Go by Fredrik Gustafsson. Changed to keep
// update bodies as text so that either Mode can apply them. See
// ../LICENSE-APACHE and NOTICE.

package codexio

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	beginPatch    = "*** Begin Patch"
	endPatch      = "*** End Patch"
	addFile       = "*** Add File: "
	deleteFile    = "*** Delete File: "
	updateFile    = "*** Update File: "
	moveTo        = "*** Move to: "
	environmentID = "*** Environment ID:"
	endFile       = "*** End of File"
)

// OperationKind says what an Operation does to its file.
type OperationKind int

const (
	// AddFile creates a file, replacing any file at the same path.
	AddFile OperationKind = iota + 1
	// DeleteFile removes an existing file.
	DeleteFile
	// UpdateFile applies an update diff to an existing file and can move it.
	UpdateFile
)

// String returns "add", "delete", or "update".
func (k OperationKind) String() string {
	switch k {
	case AddFile:
		return "add"
	case DeleteFile:
		return "delete"
	case UpdateFile:
		return "update"
	}
	return fmt.Sprintf("OperationKind(%d)", int(k))
}

// Operation is one file operation of a patch.
type Operation struct {
	Kind OperationKind
	Path string
	// MoveTo is the new path of an updated file, or empty.
	MoveTo string
	// Contents is the text of an added file: each "+" line of the patch
	// followed by a newline.
	Contents string
	// Diff is the update diff of an updated file: each line of the patch
	// followed by "\n". Mode.Apply(contents, Diff) applies it exactly as
	// ApplyFiles would.
	Diff string
}

// Patch is a parsed apply_patch patch.
type Patch struct {
	// EnvironmentID is the value of an optional "*** Environment ID:" line.
	EnvironmentID string
	Operations    []Operation
}

type parseMode int

const (
	notStarted parseMode = iota
	startedPatch
	inAddFile
	inDeleteFile
	inUpdateFile
	endedPatch
)

type patchParser struct {
	patch *Patch
	mode  parseMode
	// lines collects the "+" lines of an add or the diff lines of an update,
	// joined into Contents or Diff when the operation ends.
	lines []string
	// hasChunk is set once an update has a diff line other than
	// "*** End of File", which Codex ignores before the first section.
	hasChunk   bool
	lineNumber int
	hunkLine   int
	bodyLines  []int
}

// Parse parses an apply_patch patch the way Codex does: it accepts
// whitespace around the markers and a patch wrapped in a <<EOF heredoc.
// All update bodies are validated before any operation can mutate files.
func Parse(patch string) (*Patch, error) {
	if !utf8.ValidString(patch) {
		return nil, errors.New("invalid patch: not valid UTF-8")
	}
	lines, err := patchLines(patch)
	if err != nil {
		return nil, err
	}
	parser := &patchParser{patch: &Patch{}}
	for i, line := range lines {
		parser.lineNumber = i + 1
		if i == len(lines)-1 {
			// The boundary check guarantees the last line is "*** End Patch".
			err = parser.header(endPatch)
			break
		}
		err = parser.line(strings.TrimSuffix(line, "\r"))
		if err != nil {
			break
		}
	}
	if err != nil {
		if strings.HasPrefix(err.Error(), "invalid patch hunk:") {
			return nil, hunkError(parser.lineNumber, strings.TrimPrefix(err.Error(), "invalid patch hunk: "))
		}
		return nil, err
	}
	return parser.patch, nil
}

// patchLines splits a patch into lines like Rust's str::lines and removes a
// heredoc wrapper, then checks the first and last lines.
func patchLines(patch string) ([]string, error) {
	text := strings.TrimSpace(patch)
	if text == "" {
		return nil, errors.New("invalid patch: The first line of the patch must be '*** Begin Patch'")
	}
	lines := strings.Split(text, "\n")
	for i := range lines[:len(lines)-1] {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	err := checkBoundaries(lines)
	if err == nil {
		return lines, nil
	}
	first, last := lines[0], lines[len(lines)-1]
	if (first == "<<EOF" || first == "<<'EOF'" || first == "<<\"EOF\"") &&
		strings.HasSuffix(last, "EOF") && len(lines) >= 4 {
		inner := lines[1 : len(lines)-1]
		return inner, checkBoundaries(inner)
	}
	return nil, err
}

func checkBoundaries(lines []string) error {
	if strings.TrimSpace(lines[0]) != beginPatch {
		return errors.New("invalid patch: The first line of the patch must be '*** Begin Patch'")
	}
	if strings.TrimSpace(lines[len(lines)-1]) != endPatch {
		return errors.New("invalid patch: The last line of the patch must be '*** End Patch'")
	}
	return nil
}

func (p *patchParser) line(line string) error {
	trimmed := strings.TrimSpace(line)
	switch p.mode {
	case notStarted:
		// patchLines checked that this line is "*** Begin Patch".
		p.mode = startedPatch
		return nil
	case startedPatch:
		rest, ok := strings.CutPrefix(trimmed, environmentID)
		if ok {
			return p.environmentID(strings.TrimSpace(rest))
		}
	case inAddFile:
		if !isHeader(trimmed) {
			contents, ok := strings.CutPrefix(line, "+")
			if !ok {
				return invalidHeader(trimmed)
			}
			p.lines = append(p.lines, contents)
			return nil
		}
	case inUpdateFile:
		marker := strings.TrimRightFunc(line, unicode.IsSpace)
		if isHeader(marker) {
			return p.header(marker)
		}
		operation := &p.patch.Operations[len(p.patch.Operations)-1]
		destination, ok := strings.CutPrefix(marker, moveTo)
		if ok && !p.hasChunk && operation.MoveTo == "" {
			operation.MoveTo = destination
			return nil
		}
		p.hasChunk = p.hasChunk || marker != endFile
		p.bodyLines = append(p.bodyLines, p.lineNumber)
		p.lines = append(p.lines, line)
		return nil
	case endedPatch:
		if trimmed != "" {
			return errors.New("invalid patch: The last line of the patch must be '*** End Patch'")
		}
		return nil
	case inDeleteFile:
	}
	if isHeader(trimmed) {
		return p.header(trimmed)
	}
	return invalidHeader(trimmed)
}

func (p *patchParser) environmentID(value string) error {
	switch {
	case p.patch.EnvironmentID != "":
		return errors.New("invalid patch: apply_patch environment_id cannot be specified more than once")
	case value == "":
		return errors.New("invalid patch: apply_patch environment_id cannot be empty")
	}
	p.patch.EnvironmentID = value
	return nil
}

func isHeader(line string) bool {
	return line == endPatch || strings.HasPrefix(line, addFile) ||
		strings.HasPrefix(line, deleteFile) || strings.HasPrefix(line, updateFile)
}

// header ends the operation being parsed and starts the next one.
func (p *patchParser) header(line string) error {
	patchHeader := line
	if p.mode == inAddFile || p.mode == inUpdateFile {
		operation := &p.patch.Operations[len(p.patch.Operations)-1]
		if p.mode == inUpdateFile && len(p.lines) == 0 {
			return hunkError(p.hunkLine, fmt.Sprintf("Update file hunk for path '%s' is empty", operation.Path))
		}
		text := joinLines(p.lines)
		if p.mode == inAddFile {
			operation.Contents = text
		} else {
			if _, err := parseCodexChunks(operation.Path, p.lines); err != nil {
				if pe, ok := err.(*ParseError); ok {
					line := p.lineNumber
					if pe.Line > 0 && pe.Line <= len(p.bodyLines) {
						line = p.bodyLines[pe.Line-1]
					}
					if strings.Contains(pe.Message, "is empty") {
						line = p.hunkLine
					}
					if pe.Line > len(p.bodyLines) && pe.Message == emptyChunkMessage && strings.TrimSpace(patchHeader) != endPatch {
						return unexpectedCodexLine(patchHeader, line)
					}
					return hunkError(line, pe.Message)
				}
				return err
			}
			operation.Diff = text
		}
	}
	p.lines, p.hasChunk, p.bodyLines = nil, false, nil
	if line == endPatch {
		p.mode = endedPatch
		return nil
	}
	for _, kind := range []struct {
		prefix string
		kind   OperationKind
		mode   parseMode
	}{{addFile, AddFile, inAddFile}, {deleteFile, DeleteFile, inDeleteFile}, {updateFile, UpdateFile, inUpdateFile}} {
		path, ok := strings.CutPrefix(line, kind.prefix)
		if ok {
			p.patch.Operations = append(p.patch.Operations, Operation{Kind: kind.kind, Path: path})
			p.hunkLine = p.lineNumber
			p.mode = kind.mode
			break
		}
	}
	return nil
}

func invalidHeader(line string) error {
	return fmt.Errorf("invalid patch hunk: '%s' is not a valid hunk header. Valid hunk headers: "+
		"'*** Add File: {path}', '*** Delete File: {path}', '*** Update File: {path}'", line)
}

// joinLines joins lines, each followed by "\n".
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// diffLines splits text whose lines each end in a newline, like
// Operation.Diff, back into its lines.
func diffLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(text, "\n"), "\n")
}
