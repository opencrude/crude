package fileio

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"crude/logger"
)

type Editor struct {
	sessionID     string
	msgID         string
	journal       *Journal
	policy        *pathPolicy
	logger        *logger.Logger
	journalWrites bool
}

func NewEditorWithLogger(sessionID string, lg *logger.Logger) (*Editor, error) {
	j, err := OpenJournal(sessionID, lg)
	if err != nil {
		return nil, fmt.Errorf("editor: open journal: %w", err)
	}
	return &Editor{
		sessionID:     sessionID,
		journal:       j,
		policy:        defaultPathPolicy(),
		logger:        lg,
		journalWrites: true,
	}, nil
}

func NewEditorWithSessionDir(sessionID, sessionsRoot string, lg *logger.Logger) (*Editor, error) {
	j, err := OpenJournalAtDir(sessionID, sessionsRoot, lg)
	if err != nil {
		return nil, fmt.Errorf("editor: open journal: %w", err)
	}
	return &Editor{
		sessionID:     sessionID,
		journal:       j,
		policy:        defaultPathPolicy(),
		logger:        lg,
		journalWrites: true,
	}, nil
}

func GenerateSessionID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("fileio: entropy source failed: %v", err))
	}
	short := hex.EncodeToString(b)
	wd, _ := os.Getwd()
	now := time.Now().Format("2006-01-02_150405")
	if wd != "" {
		abs, _ := filepath.Abs(wd)
		return fmt.Sprintf("%s_%s_%s", now, filepath.Base(abs), short)
	}
	return fmt.Sprintf("%s_%s", now, short)
}

func (e *Editor) WithMessage(msgID string) *Editor {
	e.logger.Debug("EDITOR", "with_message", "msg_id", msgID)
	return &Editor{
		sessionID:     e.sessionID,
		msgID:         msgID,
		journal:       e.journal,
		policy:        e.policy,
		logger:        e.logger,
		journalWrites: e.journalWrites,
	}
}

func (e *Editor) SetLegacyJournalWrites(enabled bool) { e.journalWrites = enabled }
func (e *Editor) recordJournal(entry JournalEntry) error {
	if !e.journalWrites {
		return nil
	}
	return e.journal.record(entry)
}

func (e *Editor) Close() error {
	return e.journal.Close()
}

func (e *Editor) Read(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	start := time.Now()
	data, _, err := readFileSecure(abs, e.policy)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		e.logger.Error("FILEIO", "read", "path", path, "latency_ms", dur, "error", err.Error())
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	if IsBinary(data) {
		e.logger.Info("FILEIO", "read_binary", "path", path, "latency_ms", dur, "size", int64(len(data)))
		return "", &BinaryFileError{Path: abs, Size: int64(len(data))}
	}
	encoded := EncodeHashlines(string(data))
	e.logger.Info("FILEIO", "read", "path", path, "latency_ms", dur, "size", len(data), "encoded_size", len(encoded))
	return encoded, nil
}

func (e *Editor) ReadRaw(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("read raw %s: %w", path, err)
	}
	start := time.Now()
	data, _, err := readFileSecure(abs, e.policy)
	dur := time.Since(start).Milliseconds()
	if err != nil {
		e.logger.Error("FILEIO", "read_raw", "path", path, "latency_ms", dur, "error", err.Error())
		return "", fmt.Errorf("read raw %s: %w", path, err)
	}
	if IsBinary(data) {
		e.logger.Warn("FILEIO", "read_raw_binary", "path", path, "size", int64(len(data)))
		return "", &BinaryFileError{Path: abs, Size: int64(len(data))}
	}
	e.logger.Debug("FILEIO", "read_raw_ok", "path", abs, "latency_ms", dur, "size", len(data))
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
	e.logger.Debug("FILEIO", "write_begin", "path", abs, "content_len", len(newBytes), "op_id", opID)

	existing, fi, statErr := readFile(abs)
	if statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("write %s: pre-read: %w", path, statErr)
		}
		// File doesn't exist — creating new file
		e.logger.Debug("FILEIO", "write_create", "path", abs, "op_id", opID)
		if err := validateParentDir(abs); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		entry := buildCreateEntry(e.sessionID, e.msgID, opID, abs, newBytes)
		if err := e.recordJournal(entry); err != nil {
			return fmt.Errorf("write %s: journal: %w", path, err)
		}
		start := time.Now()
		writeErr := writeAtomic(abs, newBytes, 0644, e.logger)
		dur := time.Since(start).Milliseconds()
		e.logger.Info("FILEIO", "create", "path", abs, "latency_ms", dur, "size", len(newBytes))
		return writeErr
	}

	if IsBinary(existing) {
		e.logger.Warn("FILEIO", "write_binary_skip", "path", abs, "size", fi.Size())
		return &BinaryFileError{Path: abs, Size: fi.Size()}
	}

	// Existing file — recording before/after content details
	oldSize := len(existing)
	newSize := len(newBytes)
	e.logger.Debug("FILEIO", "write_overwrite", "path", abs, "old_size", oldSize, "new_size", newSize, "delta", newSize-oldSize, "op_id", opID)

	entry, err := buildWriteEntry(e.sessionID, e.msgID, opID, abs, existing, newBytes, fi.Mode().Perm())
	if err != nil {
		return fmt.Errorf("write %s: build journal entry: %w", path, err)
	}
	if err := e.recordJournal(entry); err != nil {
		return fmt.Errorf("write %s: journal: %w", path, err)
	}
	start := time.Now()
	writeErr := writeAtomic(abs, newBytes, fi.Mode().Perm(), e.logger)
	dur := time.Since(start).Milliseconds()
	if writeErr != nil {
		e.logger.Error("FILEIO", "write_failed", "path", abs, "latency_ms", dur, "error", writeErr.Error())
	} else {
		e.logger.Info("FILEIO", "write_ok", "path", abs, "latency_ms", dur, "old_size", oldSize, "new_size", newSize)
	}
	return writeErr
}

func (e *Editor) Patch(path string, ops []SearchReplace) error {
	if len(ops) == 0 {
		e.logger.Warn("FILEIO", "patch_empty", "path", path)
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("patch %s: %w", path, err)
	}
	e.logger.Debug("FILEIO", "patch_enter", "path", abs, "ops", len(ops))

	oldBytes, fi, err := readFileForEdit(abs, e.policy)
	if err != nil {
		e.logger.Error("FILEIO", "patch_read_failed", "path", abs, "error", err.Error())
		return fmt.Errorf("patch %s: %w", path, err)
	}

	newContent, err := ApplySearchReplace(string(oldBytes), ops, e.logger)
	if err != nil {
		e.logger.Error("FILEIO", "patch_apply_failed", "path", abs, "error", err.Error())
		return fmt.Errorf("patch %s: %w", path, err)
	}

	newBytes := []byte(newContent)
	opID := newOpID()

	entry, err := buildWriteEntry(e.sessionID, e.msgID, opID, abs, oldBytes, newBytes, fi.Mode().Perm())
	if err != nil {
		e.logger.Error("FILEIO", "patch_journal_failed", "path", abs, "error", err.Error())
		return fmt.Errorf("patch %s: build journal entry: %w", path, err)
	}
	if err := e.recordJournal(entry); err != nil {
		e.logger.Error("FILEIO", "patch_journal_record_failed", "path", abs, "error", err.Error())
		return fmt.Errorf("patch %s: journal: %w", path, err)
	}
	start := time.Now()
	writeErr := writeAtomic(abs, newBytes, fi.Mode().Perm(), e.logger)
	dur := time.Since(start).Milliseconds()
	if writeErr != nil {
		e.logger.Error("FILEIO", "patch_write_failed", "path", abs, "latency_ms", dur, "error", writeErr.Error())
	} else {
		e.logger.Info("FILEIO", "patch_ok", "path", abs, "latency_ms", dur, "old_bytes", len(oldBytes), "new_bytes", len(newBytes))
	}
	return writeErr
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
	if err := e.recordJournal(entry); err != nil {
		return fmt.Errorf("delete %s: journal: %w", path, err)
	}
	start := time.Now()
	removeErr := os.Remove(abs)
	dur := time.Since(start).Milliseconds()
	if removeErr != nil {
		e.logger.Error("FILEIO", "delete_failed", "path", abs, "latency_ms", dur, "error", removeErr.Error())
	} else {
		e.logger.Info("FILEIO", "delete_ok", "path", abs, "latency_ms", dur)
	}
	return removeErr
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
	if err := e.recordJournal(entry); err != nil {
		return fmt.Errorf("move %s → %s: journal: %w", src, dst, err)
	}
	start := time.Now()
	renameErr := os.Rename(srcAbs, dstAbs)
	dur := time.Since(start).Milliseconds()
	if renameErr != nil {
		e.logger.Error("FILEIO", "rename_failed", "src", srcAbs, "dst", dstAbs, "latency_ms", dur, "error", renameErr.Error())
	} else {
		e.logger.Info("FILEIO", "rename_ok", "src", srcAbs, "dst", dstAbs, "latency_ms", dur)
	}
	return renameErr
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
	if err := e.recordJournal(entry); err != nil {
		return fmt.Errorf("copy %s → %s: journal: %w", src, dst, err)
	}
	return writeAtomic(dstAbs, data, 0644, e.logger)
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
