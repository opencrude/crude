package fileio

import (
	"fmt"
	"hash/fnv"
	"strings"
)

func EncodeHashlines(content string) string {
	lines := splitLines(content)
	lineCount := len(lines)
	width := len(fmt.Sprintf("%d", lineCount))

	var b strings.Builder
	b.Grow(len(content) + lineCount*16)

	for i, line := range lines {
		fmt.Fprintf(&b, "%*d:%08x| %s\n", width, i+1, lineHash(line), line)
	}
	return b.String()
}

func lineHash(line string) uint32 {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return 0x00000000
	}
	h := fnv.New32a()
	h.Write([]byte(trimmed))
	return h.Sum32()
}

type Anchor struct {
	Line    int
	Hash    uint32
	HashLen int
}

func ParseAnchor(s string) (Anchor, error) {
	s = strings.TrimSpace(s)
	idx := strings.IndexByte(s, ':')
	if idx < 1 {
		return Anchor{}, fmt.Errorf("parse anchor: invalid format %q (want N:HHHHHHHH)", s)
	}
	lineStr := s[:idx]
	hashStr := s[idx+1:]

	var line int
	if _, err := fmt.Sscanf(lineStr, "%d", &line); err != nil || line < 1 {
		return Anchor{}, fmt.Errorf("parse anchor: invalid line %q", lineStr)
	}

	var hash uint64
	if _, err := fmt.Sscanf(hashStr, "%x", &hash); err != nil {
		return Anchor{}, fmt.Errorf("parse anchor: invalid hash %q", hashStr)
	}
	return Anchor{Line: line, Hash: uint32(hash), HashLen: len(hashStr)}, nil
}

func ValidateAnchor(a Anchor, lines []string) error {
	if a.Line < 1 || a.Line > len(lines) {
		return fmt.Errorf(
			"anchor %d:%s: line out of range (file has %d lines)",
			a.Line, formatHash(a.Hash, a.HashLen), len(lines),
		)
	}
	actual := lineHash(lines[a.Line-1])
	if !hashMatch(a.Hash, actual, a.HashLen) {
		return fmt.Errorf(
			"anchor %d:%s: hash mismatch (got %08x) — file modified since last read, re-read the file before editing",
			a.Line, formatHash(a.Hash, a.HashLen), actual,
		)
	}
	return nil
}

func hashMatch(anchorHash, actualHash uint32, hashLen int) bool {
	if anchorHash == actualHash {
		return true
	}
	switch hashLen {
	case 2:
		return (anchorHash & 0xff) == (actualHash & 0xff)
	case 4:
		return (anchorHash & 0xffff) == (actualHash & 0xffff)
	default:
		return false
	}
}

func formatHash(h uint32, hashLen int) string {
	switch hashLen {
	case 2:
		return fmt.Sprintf("%02x", h&0xff)
	case 4:
		return fmt.Sprintf("%04x", h&0xffff)
	default:
		return fmt.Sprintf("%08x", h)
	}
}

func splitLines(content string) []string {
	if content == "" {
		return nil
	}
	lines := strings.Split(content, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		return lines[:len(lines)-1]
	}
	return lines
}

func joinLines(lines []string, trailingNL bool) string {
	s := strings.Join(lines, "\n")
	if trailingNL {
		s += "\n"
	}
	return s
}
