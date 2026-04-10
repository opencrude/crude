package fileio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

type Editor struct {
	sessionID string
	msgID     string
	journal   *Journal
	policy    *pathPolicy
}

func NewEditor(sessionID string) (*Editor, error) {
	j, err := OpenJournal(sessionID)
	if err != nil {
		return nil, fmt.Errorf("editor: open journal: %w", err)
	}
	return &Editor{
		sessionID: sessionID,
		journal:   j,
		policy:    defaultPathPolicy(),
	}, nil
}

func GenerateSessionID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("fileio: entropy source failed: %v", err))
	}
	wd, _ := os.Getwd()
	h := hex.EncodeToString(b)[:12]
	if wd != "" {
		abs, _ := filepath.Abs(wd)
		return fmt.Sprintf("s-%s-%s", filepath.Base(abs), h)
	}
	return "s-" + h
}

func (e *Editor) WithMessage(msgID string) *Editor {
	return &Editor{
		sessionID: e.sessionID,
		msgID:     msgID,
		journal:   e.journal,
		policy:    e.policy,
	}
}

func (e *Editor) Close() error {
	return e.journal.Close()
}

func (e *Editor) Read(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	data, _, err := readFileSecure(abs, e.policy)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if isBinary(data) {
		return "", &BinaryFileError{Path: abs, Size: int64(len(data))}
	}
	return EncodeHashlines(string(data)), nil
}

func (e *Editor) ReadRaw(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("read raw %s: %w", path, err)
	}
	data, _, err := readFileSecure(abs, e.policy)
	if err != nil {
		return "", fmt.Errorf("read raw %s: %w", path, err)
	}
	if isBinary(data) {
		return "", &BinaryFileError{Path: abs, Size: int64(len(data))}
	}
	return string(data), nil
}

func (e *Editor) Write(path string, content string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := e.policy.validatePath(abs); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	newBytes := []byte(content)
	opID := newOpID()

	existing, fi, statErr := readFile(abs)
	if statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("write %s: pre-read: %w", path, statErr)
		}
		if err := validateParentDir(abs); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		entry := buildCreateEntry(e.sessionID, e.msgID, opID, abs, newBytes)
		if err := e.journal.record(entry); err != nil {
			return fmt.Errorf("write %s: journal: %w", path, err)
		}
		return writeAtomic(abs, newBytes, 0644)
	}

	if isBinary(existing) {
		return &BinaryFileError{Path: abs, Size: fi.Size()}
	}

	entry, err := buildWriteEntry(e.sessionID, e.msgID, opID, abs, existing, newBytes, fi.Mode().Perm())
	if err != nil {
		return fmt.Errorf("write %s: build journal entry: %w", path, err)
	}
	if err := e.journal.record(entry); err != nil {
		return fmt.Errorf("write %s: journal: %w", path, err)
	}
	return writeAtomic(abs, newBytes, fi.Mode().Perm())
}

func (e *Editor) Patch(path string, ops []SearchReplace) error {
	if len(ops) == 0 {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("patch %s: %w", path, err)
	}

	oldBytes, fi, err := readFileForEdit(abs, e.policy)
	if err != nil {
		return fmt.Errorf("patch %s: %w", path, err)
	}

	newContent, err := ApplySearchReplace(string(oldBytes), ops)
	if err != nil {
		return fmt.Errorf("patch %s: %w", path, err)
	}

	newBytes := []byte(newContent)
	opID := newOpID()

	entry, err := buildWriteEntry(e.sessionID, e.msgID, opID, abs, oldBytes, newBytes, fi.Mode().Perm())
	if err != nil {
		return fmt.Errorf("patch %s: build journal entry: %w", path, err)
	}
	if err := e.journal.record(entry); err != nil {
		return fmt.Errorf("patch %s: journal: %w", path, err)
	}
	return writeAtomic(abs, newBytes, fi.Mode().Perm())
}

func (e *Editor) AnchorPatch(path string, edits []AnchorEdit) error {
	if len(edits) == 0 {
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("anchor patch %s: %w", path, err)
	}

	oldBytes, fi, err := readFileForEdit(abs, e.policy)
	if err != nil {
		return fmt.Errorf("anchor patch %s: %w", path, err)
	}

	newContent, err := ApplyAnchorEdits(string(oldBytes), edits)
	if err != nil {
		return fmt.Errorf("anchor patch %s: %w", path, err)
	}

	newBytes := []byte(newContent)
	opID := newOpID()

	entry, err := buildWriteEntry(e.sessionID, e.msgID, opID, abs, oldBytes, newBytes, fi.Mode().Perm())
	if err != nil {
		return fmt.Errorf("anchor patch %s: build journal entry: %w", path, err)
	}
	if err := e.journal.record(entry); err != nil {
		return fmt.Errorf("anchor patch %s: journal: %w", path, err)
	}
	return writeAtomic(abs, newBytes, fi.Mode().Perm())
}

func (e *Editor) Delete(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	if err := e.policy.validatePath(abs); err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}

	oldBytes, fi, err := readFile(abs)
	if err != nil {
		return fmt.Errorf("delete %s: read: %w", path, err)
	}

	opID := newOpID()
	entry, err := buildDeleteEntry(e.sessionID, e.msgID, opID, abs, oldBytes, fi.Mode().Perm())
	if err != nil {
		return fmt.Errorf("delete %s: build journal entry: %w", path, err)
	}
	if err := e.journal.record(entry); err != nil {
		return fmt.Errorf("delete %s: journal: %w", path, err)
	}
	return os.Remove(abs)
}

func (e *Editor) Move(src, dst string) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("move %s: %w", src, err)
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("move dest %s: %w", dst, err)
	}
	if err := e.policy.validatePath(srcAbs); err != nil {
		return fmt.Errorf("move %s: %w", src, err)
	}
	if err := e.policy.validatePath(dstAbs); err != nil {
		return fmt.Errorf("move dest %s: %w", dst, err)
	}

	oldBytes, fi, err := readFile(srcAbs)
	if err != nil {
		return fmt.Errorf("move %s: read: %w", src, err)
	}

	if err := validateParentDir(dstAbs); err != nil {
		return fmt.Errorf("move %s: %w", dst, err)
	}

	opID := newOpID()
	entry, err := buildMoveEntry(e.sessionID, e.msgID, opID, srcAbs, dstAbs, oldBytes, fi.Mode().Perm())
	if err != nil {
		return fmt.Errorf("move %s → %s: build journal entry: %w", src, dst, err)
	}
	if err := e.journal.record(entry); err != nil {
		return fmt.Errorf("move %s → %s: journal: %w", src, dst, err)
	}
	return os.Rename(srcAbs, dstAbs)
}

func (e *Editor) Copy(src, dst string) error {
	srcAbs, err := filepath.Abs(src)
	if err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	dstAbs, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("copy dest %s: %w", dst, err)
	}
	if err := e.policy.validatePath(srcAbs); err != nil {
		return fmt.Errorf("copy %s: %w", src, err)
	}
	if err := e.policy.validatePath(dstAbs); err != nil {
		return fmt.Errorf("copy dest %s: %w", dst, err)
	}

	data, _, err := readFile(srcAbs)
	if err != nil {
		return fmt.Errorf("copy %s: read: %w", src, err)
	}

	if err := validateParentDir(dstAbs); err != nil {
		return fmt.Errorf("copy %s: %w", dst, err)
	}

	opID := newOpID()
	entry := buildCopyEntry(e.sessionID, e.msgID, opID, dstAbs, data)
	if err := e.journal.record(entry); err != nil {
		return fmt.Errorf("copy %s → %s: journal: %w", src, dst, err)
	}
	return writeAtomic(dstAbs, data, 0644)
}

func (e *Editor) UndoLast() error {
	return e.journal.UndoLast()
}

func (e *Editor) UndoMessage(msgID string) error {
	return e.journal.UndoMessage(msgID)
}

func (e *Editor) UndoN(n int) error {
	return e.journal.UndoN(n)
}

func (e *Editor) UndoSession() error {
	return e.journal.UndoSession()
}

func (e *Editor) Log() []JournalEntry {
	out := make([]JournalEntry, len(e.journal.entries))
	copy(out, e.journal.entries)
	return out
}

func (e *Editor) MessageLog(msgID string) []JournalEntry {
	var out []JournalEntry
	for _, entry := range e.journal.entries {
		if entry.MsgID == msgID {
			out = append(out, entry)
		}
	}
	return out
}

func (e *Editor) ChangedFiles() []string {
	seen := make(map[string]bool)
	var paths []string
	for _, entry := range e.journal.entries {
		if !seen[entry.Path] {
			seen[entry.Path] = true
			paths = append(paths, entry.Path)
		}
		if entry.DestPath != "" && !seen[entry.DestPath] {
			seen[entry.DestPath] = true
			paths = append(paths, entry.DestPath)
		}
	}
	return paths
}

func (e *Editor) HasChanges() bool {
	return len(e.journal.entries) > 0
}

func newOpID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("fileio: entropy source failed: %v", err))
	}
	return hex.EncodeToString(b)
}
