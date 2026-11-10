// Package document defines presentation-neutral shared text commands.
package document

import (
	"bytes"
	"crude/textencoding"
	"errors"
	"strings"
	"unicode/utf8"
)

const MaxBytes = 16 << 20
const MaxEdits = 1024

var (
	ErrRevision = errors.New("document revision is stale")
	ErrOwner    = errors.New("document owner is detached or invalid")
	ErrRequest  = errors.New("document request ID reused with different payload")
	ErrOverlap  = errors.New("document undo overlaps later work")
	ErrLimit    = errors.New("document limit exceeded")
	ErrNotFound = errors.New("document ID is not loaded. Open the file and use the returned document ID")
)

type Owner struct {
	ClientID string
	Epoch    uint64
}
type Snapshot struct {
	ID, Path string
	// Name is the display name of a document without a disk path.
	Name          string
	Encoding      textencoding.Encoding
	SavedEncoding textencoding.Encoding
	Revision      uint64
	// Runtime analysis identity is published with canonical text, never used as
	// an edit revision or permission token and never reused after a core restart.
	AnalysisGeneration, AnalysisLifetime string
	Text, Saved                          []byte
	DiskHash                             [32]byte
	DiskExists, Dirty, Conflict          bool
}

// ContentDirty compares the canonical savepoint, including its disk format.
// Shell paint uses the retained Buffer projection instead of exporting text.
func (s Snapshot) ContentDirty() bool {
	return s.Encoding != s.SavedEncoding || !bytes.Equal(s.Text, s.Saved)
}

// IsUntitledID identifies the core-generated identity of an untitled document.
// The identity remains stable after its first save; Path determines whether it
// currently has a disk destination.
func IsUntitledID(id string) bool {
	const prefix = "untitled:"
	if !strings.HasPrefix(id, prefix) || len(id) < len(prefix)+26 || len(id) > len(prefix)+256 {
		return false
	}
	for _, ch := range id[len(prefix):] {
		if (ch < 'A' || ch > 'Z') && (ch < '2' || ch > '7') {
			return false
		}
	}
	return true
}

// Edit offsets are UTF-8 byte boundaries in the same pre-change snapshot.
// Edits must be ordered and non-overlapping; touching edits are rejected to
// keep insertion and inverse ordering unambiguous.
type Edit struct {
	Start, End int
	Text       string
}
type Change struct {
	DocumentID, RequestID string
	Owner                 Owner
	BaseRevision          uint64
	GroupID               string
	Edits                 []Edit
}
type Receipt struct {
	RequestID, DocumentID string
	Before, Revision      uint64
	Accepted              bool
	Error                 string
}

// Apply validates a complete command before constructing text or inverse edits.
func Apply(text []byte, edits []Edit) ([]byte, []Edit, error) {
	if len(text) > MaxBytes || len(edits) == 0 || len(edits) > MaxEdits {
		return nil, nil, ErrLimit
	}
	if !utf8.Valid(text) {
		return nil, nil, errors.New("document text is not UTF-8")
	}
	size := len(text)
	for i, e := range edits {
		if e.Start < 0 || e.End < e.Start || e.End > len(text) || (i > 0 && e.Start <= edits[i-1].End) || !boundary(text, e.Start) || !boundary(text, e.End) || !utf8.ValidString(e.Text) {
			return nil, nil, errors.New("invalid document edit or UTF-8 byte boundary")
		}
		if len(e.Text) > MaxBytes {
			return nil, nil, ErrLimit
		}
		size += len(e.Text) - (e.End - e.Start)
		if size > MaxBytes {
			return nil, nil, ErrLimit
		}
	}
	out := make([]byte, 0, size)
	inverse := make([]Edit, 0, len(edits))
	pos := 0
	for _, e := range edits {
		out = append(out, text[pos:e.Start]...)
		start := len(out)
		out = append(out, e.Text...)
		inverse = append(inverse, Edit{Start: start, End: len(out), Text: string(text[e.Start:e.End])})
		pos = e.End
	}
	out = append(out, text[pos:]...)
	return out, inverse, nil
}

func boundary(text []byte, pos int) bool { return pos == len(text) || utf8.RuneStart(text[pos]) }

// Rebase moves edits across a later committed command. Ambiguous boundaries
// and overlapping replacements fail closed; no collaborator's work is removed.
func Rebase(edits, later []Edit) ([]Edit, error) {
	out := append([]Edit(nil), edits...)
	for i, e := range edits {
		delta := 0
		for _, l := range later {
			if l.End < e.Start {
				delta += len(l.Text) - (l.End - l.Start)
				continue
			}
			if l.Start > e.End {
				continue
			}
			return nil, ErrOverlap
		}
		out[i].Start += delta
		out[i].End += delta
	}
	return out, nil
}
