// Package document defines presentation-neutral shared text commands.
package document

import (
	"bytes"
	"crude/text/indentation"
	"crude/text/textencoding"
	"errors"
	"slices"
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

// OpenConfiguration is an optional observation returned to an editor on open.
// It is not retained in shared text, savepoints or topic state.
type OpenConfiguration struct {
	Path    string
	Choices indentation.Choices
	Error   string
}

type Snapshot struct {
	OpenConfig *OpenConfiguration `json:",omitempty"`
	ID, Path   string
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
	// Removed reports that the file was deleted on disk after the document
	// read it. The text stays in conflict until the user saves or closes it.
	Removed bool
	// DiskSeen is, while Conflict, the hash of the file version the core
	// observed last, and zero otherwise. DiskHash stays the save baseline
	// during a conflict, so this is how a shell learns that the file changed
	// again while the decision waits.
	DiskSeen [32]byte
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

// DiskRequestID is the RequestID of a change the core commits because the file
// changed on disk: a reload of clean text, or external changes merged into
// unsaved text. Shells use it to tell the user about changes they did not make.
const DiskRequestID = "disk"

type EditGroup struct {
	ID    string
	Edits []Edit
}

// A command can carry the next undo groups against consecutive revisions.
// Topic publications and journal entries always describe one group each.
type Change struct {
	Following             []EditGroup `json:",omitempty"`
	DocumentID, RequestID string
	Owner                 Owner
	BaseRevision          uint64
	GroupID               string
	Edits                 []Edit
}

// Steps visits each undo group at its own revision, without retaining batch data
// in history. Even a multi-group command has one receipt and one precondition.
func (c Change) Steps(yield func(Change) bool) {
	following := c.Following
	c.Following = nil
	if !yield(c) {
		return
	}
	for _, g := range following {
		c.BaseRevision++
		c.GroupID, c.Edits = g.ID, g.Edits
		if !yield(c) {
			return
		}
	}
}

func (c Change) Clone() Change {
	c.Edits = slices.Clone(c.Edits)
	c.Following = slices.Clone(c.Following)
	for i := range c.Following {
		c.Following[i].Edits = slices.Clone(c.Following[i].Edits)
	}
	return c
}

func (c Change) ApplyTo(text []byte) ([]byte, error) {
	for step := range c.Steps {
		var err error
		text, _, err = Apply(text, step.Edits)
		if err != nil {
			return nil, err
		}
	}
	return text, nil
}

// Resolution settles a document whose file changed on disk while its text was
// unsaved. The current file becomes the save baseline. Replace sets the text to
// Text; otherwise the current text stays. A nonzero DiskHash must equal the
// file version the user reviewed, so an unseen version is never accepted.
type Resolution struct {
	Text     []byte
	Replace  bool
	DiskHash [32]byte
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
