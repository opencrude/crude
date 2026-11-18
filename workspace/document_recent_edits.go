package workspace

import (
	"bytes"
	"path/filepath"
	"strings"
	"time"

	"crude/document"
	"crude/types"
)

const (
	recentEditEntries = 64
	recentEditBytes   = 2 << 10
)

// recentEdit is one committed change, kept in a bounded ring owned by the
// DocumentService. Typing runs merge into one entry, so the ring holds edits
// as a person would describe them, not keystrokes.
type recentEdit struct {
	path       string
	document   string
	author     string // owner client ID; empty for a change from disk
	start, end int    // span of Inserted in the text after the edit
	line       int    // 1-based line of start
	removed    string
	inserted   string
	unix       int64
}

// noteRecentEditsLocked records a commit made directly on a live document
// (a merge from disk). Commands commit on a staged copy; command records
// their operations once the copy is accepted, so a refused or rolled-back
// command never appears here.
func (s *DocumentService) noteRecentEditsLocked(d *liveDocument, c document.Change, inverse []document.Edit) {
	if d == nil || s.docs[d.snapshot.ID] != d {
		return
	}
	s.recordRecentEditsLocked(d, c.Owner.ClientID, c.Edits, inverse)
}

// noteAcceptedCommandLocked records the operations an accepted command added.
func (s *DocumentService) noteAcceptedCommandLocked(d *liveDocument, before uint64) {
	for _, op := range d.journal {
		if op.revision > before {
			s.recordRecentEditsLocked(d, op.owner.ClientID, op.edits, op.inverse)
		}
	}
}

func (s *DocumentService) recordRecentEditsLocked(d *liveDocument, author string, edits, inverse []document.Edit) {
	if d.snapshot.Path == "" {
		return
	}
	now := time.Now().Unix()
	text := d.snapshot.Text
	c := document.Change{Owner: document.Owner{ClientID: author}, Edits: edits}
	for i, e := range c.Edits {
		if i >= len(inverse) {
			break
		}
		at := inverse[i]
		removed := at.Text
		if removed == e.Text {
			continue
		}
		if s.mergeRecentEditLocked(d, c.Owner.ClientID, at.Start, e, removed) {
			continue
		}
		entry := recentEdit{
			path: d.snapshot.Path, document: d.snapshot.ID, author: c.Owner.ClientID,
			start: at.Start, end: at.End, line: 1 + bytes.Count(text[:min(max(0, at.Start), len(text))], []byte{'\n'}),
			removed: clipRecentEdit(removed), inserted: clipRecentEdit(e.Text), unix: now,
		}
		if len(s.recentEdits) >= recentEditEntries {
			copy(s.recentEdits, s.recentEdits[1:])
			s.recentEdits = s.recentEdits[:len(s.recentEdits)-1]
		}
		s.recentEdits = append(s.recentEdits, entry)
	}
}

// mergeRecentEditLocked extends the newest entry with a typed character or a
// backspace at its end, by the same author in the same document.
func (s *DocumentService) mergeRecentEditLocked(d *liveDocument, author string, start int, e document.Edit, removed string) bool {
	if len(s.recentEdits) == 0 {
		return false
	}
	last := &s.recentEdits[len(s.recentEdits)-1]
	if last.document != d.snapshot.ID || last.author != author || len(last.inserted)+len(e.Text) > recentEditBytes {
		return false
	}
	switch {
	case removed == "" && start == last.end && !strings.Contains(e.Text, "\n"):
		last.inserted += e.Text
		last.end += len(e.Text)
	case e.Text == "" && start+len(removed) == last.end && strings.HasSuffix(last.inserted, removed) && len(removed) < len(last.inserted):
		last.inserted = last.inserted[:len(last.inserted)-len(removed)]
		last.end -= len(removed)
	default:
		return false
	}
	last.unix = time.Now().Unix()
	return true
}

func clipRecentEdit(text string) string {
	if len(text) <= recentEditBytes {
		return text
	}
	cut := recentEditBytes
	for cut > 0 && !utf8Start(text[cut]) {
		cut--
	}
	return text[:cut] + "…"
}

func utf8Start(b byte) bool { return b&0xC0 != 0x80 }

// RecentEdits returns up to limit recent edits, newest last. Author is
// relative to owner: the owner's own edits are "you". Paths are relative to
// the workspace root with forward slashes.
func (s *DocumentService) RecentEdits(owner document.Owner, limit int) []types.RecentEdit {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.recentEditsLocked(owner, limit, "")
}

func (s *DocumentService) recentEditsLocked(owner document.Owner, limit int, path string) []types.RecentEdit {
	if limit <= 0 {
		return nil
	}
	out := make([]types.RecentEdit, 0, min(limit, len(s.recentEdits)))
	for i := len(s.recentEdits) - 1; i >= 0 && len(out) < limit; i-- {
		e := s.recentEdits[i]
		if path != "" && e.path != path {
			continue
		}
		author := types.RecentEditOther
		switch e.author {
		case "":
			author = types.RecentEditDisk
		case owner.ClientID:
			author = types.RecentEditYou
		}
		rel := e.path
		if r, err := filepath.Rel(s.root, e.path); err == nil && !strings.HasPrefix(r, "..") {
			rel = filepath.ToSlash(r)
		}
		out = append(out, types.RecentEdit{Path: rel, Line: e.line, Removed: e.removed, Inserted: e.inserted, Author: author, Unix: e.unix})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}
