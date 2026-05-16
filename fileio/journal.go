package fileio

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"crude/logger"
)

const (
	compressThreshold      = 4096
	rawPrefix         byte = 0x00
	gzipPrefix        byte = 0x01
)

type OpType string

const (
	OpWrite  OpType = "write"
	OpCreate OpType = "create"
	OpDelete OpType = "delete"
	OpMove   OpType = "move"
	OpCopy   OpType = "copy"
)

type JournalEntry struct {
	OpID      string `json:"op_id"`
	SessionID string `json:"session_id"`
	MsgID     string `json:"msg_id"`
	Path      string `json:"path"`
	DestPath  string `json:"dest_path,omitempty"`
	Timestamp int64  `json:"ts"`
	Op        OpType `json:"op"`
	OldGzip   []byte `json:"old,omitempty"`
	OldPerm   uint32 `json:"perm"`
	ChecksumO string `json:"cs_old"`
	ChecksumN string `json:"cs_new"`
	Undone    bool   `json:"undone,omitempty"`
}

type Journal struct {
	sessionID string
	path      string
	f         *os.File
	entries   []JournalEntry
	logger    *logger.Logger
}

func OpenJournal(sessionID string, lg *logger.Logger) (*Journal, error) {
	dir, err := journalDir()
	if err != nil {
		return nil, fmt.Errorf("journal: data dir: %w", err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("journal: mkdir: %w", err)
	}

	path := filepath.Join(dir, sessionID+".jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0600)
	if err != nil {
		return nil, fmt.Errorf("journal: open %s: %w", path, err)
	}

	j := &Journal{sessionID: sessionID, path: path, f: f, logger: lg}

	if err := j.loadEntries(); err != nil {
		f.Close()
		return nil, fmt.Errorf("journal: load entries: %w", err)
	}
	lg.Debug("JOURNAL", "open", "session_id", sessionID, "path", path, "entries", len(j.entries))
	return j, nil
}

func (j *Journal) Close() error {
	if j.f == nil {
		j.logger.Debug("JOURNAL", "close_noop", "session_id", j.sessionID)
		return nil
	}
	j.logger.Debug("JOURNAL", "close", "session_id", j.sessionID, "entries", len(j.entries))
	if err := j.f.Sync(); err != nil {
		j.f.Close()
		j.logger.Error("JOURNAL", "close_sync_failed", "error", err.Error())
		return err
	}
	err := j.f.Close()
	j.f = nil
	if err != nil {
		j.logger.Error("JOURNAL", "close_failed", "error", err.Error())
	}
	return err
}

func (j *Journal) record(e JournalEntry) error {
	data, err := json.Marshal(e)
	if err != nil {
		j.logger.Error("JOURNAL", "record_marshal_failed", "error", err.Error())
		return fmt.Errorf("journal: marshal: %w", err)
	}
	data = append(data, '\n')
	if _, err := j.f.Write(data); err != nil {
		j.logger.Error("JOURNAL", "record_write_failed", "error", err.Error(), "path", e.Path)
		return fmt.Errorf("journal: write: %w", err)
	}
	if err := j.f.Sync(); err != nil {
		j.logger.Error("JOURNAL", "record_sync_failed", "error", err.Error())
		return fmt.Errorf("journal: sync: %w", err)
	}
	j.entries = append(j.entries, e)
	j.logger.Debug("JOURNAL", "recorded", "op", e.Op, "path", e.Path, "op_id", e.OpID, "total", len(j.entries))
	return nil
}

func (j *Journal) markUndone(opID string) error {
	if _, err := j.f.Seek(0, io.SeekStart); err != nil {
		return err
	}

	var newEntries []string
	dec := json.NewDecoder(j.f)
	for {
		var e JournalEntry
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if e.OpID == opID {
			e.Undone = true
		}
		data, err := json.Marshal(e)
		if err != nil {
			return err
		}
		newEntries = append(newEntries, string(data))
	}

	if err := j.f.Truncate(0); err != nil {
		return err
	}
	if _, err := j.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	for _, entry := range newEntries {
		if _, err := j.f.WriteString(entry + "\n"); err != nil {
			return err
		}
	}
	if err := j.f.Sync(); err != nil {
		return err
	}
	_, err := j.f.Seek(0, io.SeekEnd)
	return err
}

func (j *Journal) loadEntries() error {
	if _, err := j.f.Seek(0, io.SeekStart); err != nil {
		return err
	}
	j.entries = nil
	dec := json.NewDecoder(j.f)
	for {
		var e JournalEntry
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if !e.Undone {
			j.entries = append(j.entries, e)
		}
	}
	_, err := j.f.Seek(0, io.SeekEnd)
	return err
}

func (j *Journal) UndoLast() error {
	if len(j.entries) == 0 {
		j.logger.Warn("JOURNAL", "undo_empty")
		return fmt.Errorf("nothing to undo")
	}
	last := j.entries[len(j.entries)-1]
	j.logger.Info("JOURNAL", "undo_last", "op", last.Op, "path", last.Path, "op_id", last.OpID, "remaining", len(j.entries)-1)
	if err := revertEntry(last, j.logger); err != nil {
		j.logger.Error("JOURNAL", "undo_revert_failed", "op_id", last.OpID, "error", err.Error())
		return err
	}
	if err := j.markUndone(last.OpID); err != nil {
		j.logger.Error("JOURNAL", "undo_mark_failed", "op_id", last.OpID, "error", err.Error())
		return fmt.Errorf("undo succeeded but journal update failed: %w", err)
	}
	j.entries = j.entries[:len(j.entries)-1]
	j.logger.Info("JOURNAL", "undo_last_ok", "remaining", len(j.entries))
	return nil
}

func (j *Journal) UndoMessage(msgID string) error {
	j.logger.Info("JOURNAL", "undo_msg_start", "msg_id", msgID)
	var matching []JournalEntry
	for _, e := range j.entries {
		if e.MsgID == msgID {
			matching = append(matching, e)
		}
	}
	if len(matching) == 0 {
		j.logger.Warn("JOURNAL", "undo_msg_none", "msg_id", msgID)
		return fmt.Errorf("no operations found for message %q", msgID)
	}
	j.logger.Debug("JOURNAL", "undo_msg_matched", "msg_id", msgID, "count", len(matching))
	reverseEntries(matching)

	var undone []string
	for _, e := range matching {
		if err := revertEntry(e, j.logger); err != nil {
			j.logger.Error("JOURNAL", "undo_msg_revert_failed", "msg_id", msgID, "op_id", e.OpID, "error", err.Error())
			if len(undone) > 0 {
				return fmt.Errorf("partial undo: reverted %d of %d ops for message %q, then failed: %w", len(undone), len(matching), msgID, err)
			}
			return fmt.Errorf("undo message %q: %w", msgID, err)
		}
		if err := j.markUndone(e.OpID); err != nil {
			j.logger.Error("JOURNAL", "undo_msg_mark_failed", "op_id", e.OpID, "error", err.Error())
			return fmt.Errorf("undo succeeded but journal update failed for op %s: %w", e.OpID, err)
		}
		undone = append(undone, e.OpID)
	}

	j.entries = filterEntries(j.entries, func(e JournalEntry) bool {
		return e.MsgID != msgID
	})
	j.logger.Info("JOURNAL", "undo_msg_ok", "msg_id", msgID, "reverted", len(undone))
	return nil
}

func (j *Journal) UndoSession() error {
	if len(j.entries) == 0 {
		j.logger.Warn("JOURNAL", "undo_session_empty")
		return fmt.Errorf("session has no recorded operations")
	}
	total := len(j.entries)
	j.logger.Info("JOURNAL", "undo_session_start", "total", total)
	all := make([]JournalEntry, len(j.entries))
	copy(all, j.entries)
	reverseEntries(all)

	var undone []string
	for _, e := range all {
		if err := revertEntry(e, j.logger); err != nil {
			j.logger.Error("JOURNAL", "undo_session_revert_failed", "op_id", e.OpID, "error", err.Error())
			return fmt.Errorf("partial undo: reverted %d of %d ops, then failed: %w", len(undone), len(all), err)
		}
		if err := j.markUndone(e.OpID); err != nil {
			j.logger.Error("JOURNAL", "undo_session_mark_failed", "op_id", e.OpID, "error", err.Error())
			return fmt.Errorf("undo succeeded but journal update failed for op %s: %w", e.OpID, err)
		}
		undone = append(undone, e.OpID)
	}
	j.entries = j.entries[:0]
	j.logger.Info("JOURNAL", "undo_session_ok", "reverted", total)
	return nil
}

func (j *Journal) UndoN(n int) error {
	if n <= 0 {
		j.logger.Warn("JOURNAL", "undo_n_invalid", "n", n)
		return fmt.Errorf("n must be positive")
	}
	if n > len(j.entries) {
		n = len(j.entries)
	}
	j.logger.Info("JOURNAL", "undo_n_start", "n", n, "total", len(j.entries))
	tail := make([]JournalEntry, n)
	copy(tail, j.entries[len(j.entries)-n:])
	reverseEntries(tail)

	var undone []string
	for _, e := range tail {
		if err := revertEntry(e, j.logger); err != nil {
			j.logger.Error("JOURNAL", "undo_n_revert_failed", "n", n, "op_id", e.OpID, "error", err.Error())
			return fmt.Errorf("partial undo: reverted %d of %d ops, then failed: %w", len(undone), n, err)
		}
		if err := j.markUndone(e.OpID); err != nil {
			j.logger.Error("JOURNAL", "undo_n_mark_failed", "op_id", e.OpID, "error", err.Error())
			return fmt.Errorf("undo succeeded but journal update failed for op %s: %w", e.OpID, err)
		}
		undone = append(undone, e.OpID)
	}
	j.entries = j.entries[:len(j.entries)-n]
	j.logger.Info("JOURNAL", "undo_n_ok", "reverted", n, "remaining", len(j.entries))
	return nil
}

func revertEntry(e JournalEntry, lg *logger.Logger) error {
	switch e.Op {
	case OpCreate:
		cur, _, err := readFile(e.Path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read for checksum: %w", err)
		}
		if err == nil {
			curCS := checksum(cur)
			if curCS != e.ChecksumN {
				return fmt.Errorf(
					"file %s has been modified since CRUDE created it (expected %s, got %s); not deleting — resolve manually",
					e.Path, truncateHash(e.ChecksumN, 8), truncateHash(curCS, 8),
				)
			}
		}
		return os.Remove(e.Path)

	case OpWrite, OpDelete:
		old, err := decompressOldContent(e.OldGzip)
		if err != nil {
			return fmt.Errorf("decompress old content: %w", err)
		}
		cur, _, err := readFile(e.Path)
		if err == nil {
			curCS := checksum(cur)
			if curCS != e.ChecksumN {
				lg.Warn("JOURNAL", "modified_externally", "path", e.Path, "op_id", truncateHash(e.OpID, 8),
				"expected", truncateHash(e.ChecksumN, 8), "got", truncateHash(curCS, 8))
			}
		}
		return writeAtomic(e.Path, old, os.FileMode(e.OldPerm), lg)

	case OpMove:
		if e.DestPath == "" {
			return fmt.Errorf("move undo: missing destination path in journal entry %s", e.OpID)
		}
		return os.Rename(e.DestPath, e.Path)

	case OpCopy:
		if e.DestPath == "" {
			return fmt.Errorf("copy undo: missing destination path in journal entry %s", e.OpID)
		}
		cur, _, err := readFile(e.DestPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("read for checksum: %w", err)
		}
		if err == nil {
			curCS := checksum(cur)
			if curCS != e.ChecksumN {
				return fmt.Errorf(
					"copied file %s has been modified since CRUDE created it (expected %s, got %s); not deleting — resolve manually",
					e.DestPath, truncateHash(e.ChecksumN, 8), truncateHash(curCS, 8),
				)
			}
		}
		return os.Remove(e.DestPath)
	}
	return fmt.Errorf("unknown op type %q", e.Op)
}

func truncateHash(hash string, n int) string {
	if len(hash) <= n {
		return hash
	}
	return hash[:n]
}

func buildWriteEntry(sessionID, msgID, opID, path string, oldContent, newContent []byte, perm os.FileMode) (JournalEntry, error) {
	gz, err := compressContent(oldContent)
	if err != nil {
		return JournalEntry{}, err
	}
	csOld := checksum(oldContent)
	csNew := checksum(newContent)
	return JournalEntry{
		OpID:      opID,
		SessionID: sessionID,
		MsgID:     msgID,
		Path:      path,
		Timestamp: time.Now().UnixNano(),
		Op:        OpWrite,
		OldGzip:   gz,
		OldPerm:   uint32(perm),
		ChecksumO: csOld,
		ChecksumN: csNew,
	}, nil
}

func buildCreateEntry(sessionID, msgID, opID, path string, newContent []byte) JournalEntry {
	return JournalEntry{
		OpID:      opID,
		SessionID: sessionID,
		MsgID:     msgID,
		Path:      path,
		Timestamp: time.Now().UnixNano(),
		Op:        OpCreate,
		ChecksumN: checksum(newContent),
	}
}

func buildDeleteEntry(sessionID, msgID, opID, path string, oldContent []byte, perm os.FileMode) (JournalEntry, error) {
	gz, err := compressContent(oldContent)
	if err != nil {
		return JournalEntry{}, err
	}
	return JournalEntry{
		OpID:      opID,
		SessionID: sessionID,
		MsgID:     msgID,
		Path:      path,
		Timestamp: time.Now().UnixNano(),
		Op:        OpDelete,
		OldGzip:   gz,
		OldPerm:   uint32(perm),
		ChecksumO: checksum(oldContent),
		ChecksumN: "",
	}, nil
}

func buildMoveEntry(sessionID, msgID, opID, srcPath, dstPath string, oldContent []byte, perm os.FileMode) (JournalEntry, error) {
	gz, err := compressContent(oldContent)
	if err != nil {
		return JournalEntry{}, err
	}
	return JournalEntry{
		OpID:      opID,
		SessionID: sessionID,
		MsgID:     msgID,
		Path:      srcPath,
		DestPath:  dstPath,
		Timestamp: time.Now().UnixNano(),
		Op:        OpMove,
		OldGzip:   gz,
		OldPerm:   uint32(perm),
		ChecksumO: checksum(oldContent),
	}, nil
}

func buildCopyEntry(sessionID, msgID, opID, dstPath string, newContent []byte) JournalEntry {
	return JournalEntry{
		OpID:      opID,
		SessionID: sessionID,
		MsgID:     msgID,
		Path:      dstPath,
		Timestamp: time.Now().UnixNano(),
		Op:        OpCopy,
		ChecksumN: checksum(newContent),
	}
}

func compressContent(data []byte) ([]byte, error) {
	if len(data) < compressThreshold {
		out := make([]byte, len(data)+1)
		out[0] = rawPrefix
		copy(out[1:], data)
		return out, nil
	}
	var buf bytes.Buffer
	buf.WriteByte(gzipPrefix)
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		return nil, err
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func decompressOldContent(data []byte) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	switch data[0] {
	case rawPrefix:
		return data[1:], nil
	case gzipPrefix:
		r, err := gzip.NewReader(bytes.NewReader(data[1:]))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		return io.ReadAll(r)
	default:
		if len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b {
			r, err := gzip.NewReader(bytes.NewReader(data))
			if err != nil {
				return nil, err
			}
			defer r.Close()
			return io.ReadAll(r)
		}
		return data, nil
	}
}

func checksum(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func journalDir() (string, error) {
	switch runtime.GOOS {
	case "linux":
		if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
			return filepath.Join(xdg, "crude", "sessions"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".local", "share", "crude", "sessions"), nil

	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support", "crude", "sessions"), nil

	case "windows":
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			return filepath.Join(appdata, "crude", "sessions"), nil
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "AppData", "Roaming", "crude", "sessions"), nil

	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".crude", "sessions"), nil
	}
}

func reverseEntries(s []JournalEntry) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func filterEntries(s []JournalEntry, pred func(JournalEntry) bool) []JournalEntry {
	var out []JournalEntry
	for _, e := range s {
		if pred(e) {
			out = append(out, e)
		}
	}
	return out
}
