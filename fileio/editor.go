package fileio

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"crude/logger"
)

// Editor performs exact, receipt-prepared file mutations for one session.
// Recovery and undo belong to Local History operation receipts.
type Editor struct {
	sessionID string
	msgID     string
	policy    *pathPolicy
	logger    *logger.Logger
}

// MutationStateKind identifies the kind of an exact path image.
type MutationStateKind string

const (
	MutationAbsent    MutationStateKind = "absent"
	MutationRegular   MutationStateKind = "regular_file"
	MutationDirectory MutationStateKind = "directory"
)

// MutationState is an exact, in-memory image of a path. Bytes is populated
// for regular files. Mode includes permission and special bits.
type MutationState struct {
	Kind  MutationStateKind
	Bytes []byte
	Mode  os.FileMode
}

// MutationPathState describes one path before and after a mutation.
type MutationPathState struct {
	Path   string
	Before MutationState
	After  MutationState
}

// MutationReceipt is prepared synchronously immediately before a mutation.
type MutationReceipt struct {
	OperationID   string
	OperationName string
	Paths         []MutationPathState
}

type mutationParentGuard struct {
	anchorPath     string
	anchorInfo     os.FileInfo
	resolvedParent string
}

func captureMutationParentGuard(path string) (mutationParentGuard, error) {
	parent := filepath.Dir(path)
	current := parent
	var missing []string
	for {
		info, err := os.Stat(current)
		if err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return mutationParentGuard{}, err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return mutationParentGuard{anchorPath: current, anchorInfo: info, resolvedParent: filepath.Clean(resolved)}, nil
		}
		if !os.IsNotExist(err) {
			return mutationParentGuard{}, err
		}
		next := filepath.Dir(current)
		if next == current {
			return mutationParentGuard{}, err
		}
		missing = append(missing, filepath.Base(current))
		current = next
	}
}

func (g mutationParentGuard) validate(path string) error {
	anchor, err := os.Stat(g.anchorPath)
	if err != nil || !os.SameFile(g.anchorInfo, anchor) {
		return fmt.Errorf("mutation parent changed for %s", path)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || !sameMutationPath(resolved, g.resolvedParent) {
		return fmt.Errorf("mutation parent was replaced for %s", path)
	}
	return nil
}

func sameMutationPath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	return left == right || strings.EqualFold(left, right)
}

func (r MutationReceipt) HasChanges() bool {
	for _, path := range r.Paths {
		if path.Before.Kind != path.After.Kind || path.Before.Mode != path.After.Mode || !bytes.Equal(path.Before.Bytes, path.After.Bytes) {
			return true
		}
	}
	return false
}

// MutationComplete durably records whether the prepared operation committed.
type MutationComplete func(operationID string, committed bool) error

// MutationPrepare durably records or otherwise approves a mutation receipt.
// Returning an error prevents the mutation and its journal entry. The returned
// completion callback is invoked exactly once after preparation.
type MutationPrepare func(MutationReceipt) (MutationComplete, error)

func NewEditor(sessionID string, lg *logger.Logger) *Editor {
	return &Editor{sessionID: sessionID, policy: defaultPathPolicy(), logger: lg}
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
	return &Editor{sessionID: e.sessionID, msgID: msgID, policy: e.policy, logger: e.logger}
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
	return e.WriteTracked(path, content, nil)
}

func (e *Editor) WriteTracked(path string, content string, prepare MutationPrepare) error {
	unlock := LockFileMutation(path)
	defer unlock()
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	if err := e.policy.validatePath(abs); err != nil {
		return err
	}
	existing, info, err := readFile(abs)
	if err == nil && IsBinary(existing) {
		return &BinaryFileError{Path: path, Size: info.Size()}
	}
	return e.writeBytesTracked(path, []byte(content), prepare)
}

// writeBytesTracked requires the shared mutation lease. Text-only callers
// enforce their own binary policy before reaching this shared byte writer.
func (e *Editor) writeBytesTracked(path string, newBytes []byte, prepare MutationPrepare) (resultErr error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	if err := e.policy.validatePath(abs); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	parentGuard, err := captureMutationParentGuard(abs)
	if err != nil {
		return fmt.Errorf("write %s: parent guard: %w", path, err)
	}

	if int64(len(newBytes)) > maxTrackedMutationBytes {
		return fmt.Errorf("write %s: content exceeds the %d MiB tracked-mutation limit", path, maxTrackedMutationBytes>>20)
	}
	opID := newOpID()
	e.logger.Debug("FILEIO", "write_begin", "path", abs, "content_len", len(newBytes), "op_id", opID)

	existing, fi, statErr := readFile(abs)
	if statErr != nil {
		if !errors.Is(statErr, os.ErrNotExist) {
			return fmt.Errorf("write %s: pre-read: %w", path, statErr)
		}
		// File doesn't exist: create a new file
		e.logger.Debug("FILEIO", "write_create", "path", abs, "op_id", opID)
		parentPaths, err := missingParentMutationPaths(abs)
		if err != nil {
			return fmt.Errorf("write %s: inspect parents: %w", path, err)
		}
		receiptPaths := append(parentPaths, MutationPathState{Path: abs, Before: absentState(), After: regularState(newBytes, 0644)})
		complete, err := prepareMutation(prepare, MutationReceipt{OperationID: opID, OperationName: "write_file", Paths: receiptPaths})
		if err != nil {
			return fmt.Errorf("write %s: prepare: %w", path, err)
		}
		defer finishPreparedMutation(&resultErr, complete, opID)
		defer cleanupPreparedParents(parentPaths, &resultErr)
		if err := validateMutationPreconditions(receiptPaths, nil); err != nil {
			return fmt.Errorf("write %s: precondition: %w", path, err)
		}
		if err := validateParentDir(abs); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		start := time.Now()
		writeErr := writeMutationFileChecked(abs, newBytes, 0644, e.logger, absentState(), parentGuard)
		dur := time.Since(start).Milliseconds()
		e.logger.Info("FILEIO", "create", "path", abs, "latency_ms", dur, "size", len(newBytes))
		return writeErr
	}

	if err := rejectHardlinkMutation(abs, fi); err != nil {
		return err
	}

	// Existing file: record before/after content details
	oldSize := len(existing)
	newSize := len(newBytes)
	e.logger.Debug("FILEIO", "write_overwrite", "path", abs, "old_size", oldSize, "new_size", newSize, "delta", newSize-oldSize, "op_id", opID)

	complete, err := prepareMutation(prepare, MutationReceipt{OperationID: opID, OperationName: "write_file", Paths: []MutationPathState{{Path: abs, Before: regularState(existing, fi.Mode()), After: regularState(newBytes, fi.Mode())}}})
	if err != nil {
		return fmt.Errorf("write %s: prepare: %w", path, err)
	}
	defer finishPreparedMutation(&resultErr, complete, opID)
	before := regularState(existing, fi.Mode())
	if err := validateMutationState(abs, before); err != nil {
		return fmt.Errorf("write %s: precondition: %w", path, err)
	}
	start := time.Now()
	writeErr := writeMutationFileChecked(abs, newBytes, fi.Mode(), e.logger, before, parentGuard)
	dur := time.Since(start).Milliseconds()
	if writeErr != nil {
		e.logger.Error("FILEIO", "write_failed", "path", abs, "latency_ms", dur, "error", writeErr.Error())
	} else {
		e.logger.Info("FILEIO", "write_ok", "path", abs, "latency_ms", dur, "old_size", oldSize, "new_size", newSize)
	}
	return writeErr
}

func (e *Editor) Patch(path string, ops []SearchReplace) error {
	return e.PatchTracked(path, ops, nil)
}

func (e *Editor) PatchTracked(path string, ops []SearchReplace, prepare MutationPrepare) (resultErr error) {
	if len(ops) == 0 {
		e.logger.Warn("FILEIO", "patch_empty", "path", path)
		return nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("patch %s: %w", path, err)
	}
	unlock := LockFileMutation(abs)
	defer unlock()
	e.logger.Debug("FILEIO", "patch_enter", "path", abs, "ops", len(ops))
	parentGuard, err := captureMutationParentGuard(abs)
	if err != nil {
		return fmt.Errorf("patch %s: parent guard: %w", path, err)
	}

	oldBytes, fi, err := readFileForEdit(abs, e.policy)
	if err != nil {
		e.logger.Error("FILEIO", "patch_read_failed", "path", abs, "error", err.Error())
		return fmt.Errorf("patch %s: %w", path, err)
	}
	if err := rejectHardlinkMutation(abs, fi); err != nil {
		return err
	}

	newContent, err := ApplySearchReplace(string(oldBytes), ops, e.logger)
	if err != nil {
		e.logger.Error("FILEIO", "patch_apply_failed", "path", abs, "error", err.Error())
		return fmt.Errorf("patch %s: %w", path, err)
	}

	newBytes := []byte(newContent)
	opID := newOpID()

	complete, err := prepareMutation(prepare, MutationReceipt{OperationID: opID, OperationName: "edit_file", Paths: []MutationPathState{{Path: abs, Before: regularState(oldBytes, fi.Mode()), After: regularState(newBytes, fi.Mode())}}})
	if err != nil {
		return fmt.Errorf("patch %s: prepare: %w", path, err)
	}
	defer finishPreparedMutation(&resultErr, complete, opID)
	before := regularState(oldBytes, fi.Mode())
	if err := validateMutationState(abs, before); err != nil {
		return fmt.Errorf("patch %s: precondition: %w", path, err)
	}
	start := time.Now()
	writeErr := writeMutationFileChecked(abs, newBytes, fi.Mode(), e.logger, before, parentGuard)
	dur := time.Since(start).Milliseconds()
	if writeErr != nil {
		e.logger.Error("FILEIO", "patch_write_failed", "path", abs, "latency_ms", dur, "error", writeErr.Error())
	} else {
		e.logger.Info("FILEIO", "patch_ok", "path", abs, "latency_ms", dur, "old_bytes", len(oldBytes), "new_bytes", len(newBytes))
	}
	return writeErr
}

func (e *Editor) Delete(path string) error {
	return e.DeleteTracked(path, nil)
}

func (e *Editor) DeleteTracked(path string, prepare MutationPrepare) error {
	unlock := LockTreeMutation()
	defer unlock()
	return e.deleteTracked(path, prepare)
}

// deleteTracked requires the shared mutation lease.
func (e *Editor) deleteTracked(path string, prepare MutationPrepare) (resultErr error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	if err := e.policy.validatePath(abs); err != nil {
		return fmt.Errorf("delete %s: %w", path, err)
	}
	parentGuard, err := captureMutationParentGuard(abs)
	if err != nil {
		return fmt.Errorf("delete %s: parent guard: %w", path, err)
	}

	oldBytes, fi, err := readFile(abs)
	if err != nil {
		return fmt.Errorf("delete %s: read: %w", path, err)
	}
	if err := rejectHardlinkMutation(abs, fi); err != nil {
		return err
	}
	opID := newOpID()
	complete, err := prepareMutation(prepare, MutationReceipt{OperationID: opID, OperationName: "delete_file", Paths: []MutationPathState{{Path: abs, Before: regularState(oldBytes, fi.Mode()), After: absentState()}}})
	if err != nil {
		return fmt.Errorf("delete %s: prepare: %w", path, err)
	}
	defer finishPreparedMutation(&resultErr, complete, opID)
	if err := validateMutationState(abs, regularState(oldBytes, fi.Mode())); err != nil {
		return fmt.Errorf("delete %s: precondition: %w", path, err)
	}

	start := time.Now()
	if err := parentGuard.validate(abs); err != nil {
		return fmt.Errorf("delete %s: precommit: %w", path, err)
	}
	if err := validateMutationState(abs, regularState(oldBytes, fi.Mode())); err != nil {
		return fmt.Errorf("delete %s: precommit: %w", path, err)
	}
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
	return e.MoveTracked(src, dst, nil)
}

func (e *Editor) MoveTracked(src, dst string, prepare MutationPrepare) (resultErr error) {
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
	unlock := LockTreeMutation()
	defer unlock()
	srcParentGuard, err := captureMutationParentGuard(srcAbs)
	if err != nil {
		return fmt.Errorf("move %s: parent guard: %w", src, err)
	}
	dstParentGuard, err := captureMutationParentGuard(dstAbs)
	if err != nil {
		return fmt.Errorf("move %s: destination parent guard: %w", dst, err)
	}

	oldBytes, fi, err := readFile(srcAbs)
	if err != nil {
		return fmt.Errorf("move %s: read: %w", src, err)
	}
	if err := rejectHardlinkMutation(srcAbs, fi); err != nil {
		return err
	}

	opID := newOpID()
	dstState, err := snapshotMutationState(dstAbs)
	if err != nil {
		return fmt.Errorf("move dest %s: pre-read: %w", dst, err)
	}
	if dstInfo, statErr := os.Lstat(dstAbs); statErr == nil {
		if err := rejectHardlinkMutation(dstAbs, dstInfo); err != nil {
			return err
		}
	}
	if dstInfo, statErr := os.Lstat(dstAbs); statErr == nil && srcAbs != dstAbs && os.SameFile(fi, dstInfo) {
		// Case-only rename on a case-insensitive filesystem: the destination
		// spelling is logically absent even though both names resolve now.
		dstState = absentState()
	}
	parentPaths, err := missingParentMutationPaths(dstAbs)
	if err != nil {
		return fmt.Errorf("move dest %s: inspect parents: %w", dst, err)
	}
	receiptPaths := append(parentPaths,
		MutationPathState{Path: srcAbs, Before: regularState(oldBytes, fi.Mode()), After: absentState()},
		MutationPathState{Path: dstAbs, Before: dstState, After: regularState(oldBytes, fi.Mode())},
	)
	complete, err := prepareMutation(prepare, MutationReceipt{OperationID: opID, OperationName: "move_file", Paths: receiptPaths})
	if err != nil {
		return fmt.Errorf("move %s → %s: prepare: %w", src, dst, err)
	}
	defer finishPreparedMutation(&resultErr, complete, opID)
	defer cleanupPreparedParents(parentPaths, &resultErr)
	ignored := map[string]bool{}
	if srcAbs != dstAbs && strings.EqualFold(srcAbs, dstAbs) {
		ignored[dstAbs] = true
	}
	if err := validateMutationPreconditions(receiptPaths, ignored); err != nil {
		return fmt.Errorf("move %s → %s: precondition: %w", src, dst, err)
	}
	if err := validateParentDir(dstAbs); err != nil {
		return fmt.Errorf("move %s: %w", dst, err)
	}
	start := time.Now()
	if err := srcParentGuard.validate(srcAbs); err != nil {
		return fmt.Errorf("move %s → %s: source precommit: %w", src, dst, err)
	}
	if err := dstParentGuard.validate(dstAbs); err != nil {
		return fmt.Errorf("move %s → %s: destination precommit: %w", src, dst, err)
	}
	if err := validateMutationState(srcAbs, regularState(oldBytes, fi.Mode())); err != nil {
		return fmt.Errorf("move %s → %s: source precommit: %w", src, dst, err)
	}
	if !ignored[dstAbs] {
		if err := validateMutationState(dstAbs, dstState); err != nil {
			return fmt.Errorf("move %s → %s: destination precommit: %w", src, dst, err)
		}
	}
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
	return e.CopyTracked(src, dst, nil)
}

func (e *Editor) CopyTracked(src, dst string, prepare MutationPrepare) (resultErr error) {
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
	unlock := LockTreeMutation()
	defer unlock()
	dstParentGuard, err := captureMutationParentGuard(dstAbs)
	if err != nil {
		return fmt.Errorf("copy %s: destination parent guard: %w", dst, err)
	}

	data, _, err := readFile(srcAbs)
	if err != nil {
		return fmt.Errorf("copy %s: read: %w", src, err)
	}

	opID := newOpID()
	dstState, err := snapshotMutationState(dstAbs)
	if err != nil {
		return fmt.Errorf("copy dest %s: pre-read: %w", dst, err)
	}
	if dstInfo, statErr := os.Lstat(dstAbs); statErr == nil {
		if err := rejectHardlinkMutation(dstAbs, dstInfo); err != nil {
			return err
		}
	}
	afterMode := os.FileMode(0o644)
	if dstState.Kind == MutationRegular {
		afterMode = dstState.Mode
	}
	parentPaths, err := missingParentMutationPaths(dstAbs)
	if err != nil {
		return fmt.Errorf("copy dest %s: inspect parents: %w", dst, err)
	}
	receiptPaths := append(parentPaths, MutationPathState{Path: dstAbs, Before: dstState, After: regularState(data, afterMode)})
	complete, err := prepareMutation(prepare, MutationReceipt{OperationID: opID, OperationName: "copy_file", Paths: receiptPaths})
	if err != nil {
		return fmt.Errorf("copy %s → %s: prepare: %w", src, dst, err)
	}
	defer finishPreparedMutation(&resultErr, complete, opID)
	defer cleanupPreparedParents(parentPaths, &resultErr)
	if err := validateMutationPreconditions(receiptPaths, nil); err != nil {
		return fmt.Errorf("copy %s → %s: precondition: %w", src, dst, err)
	}
	if err := validateParentDir(dstAbs); err != nil {
		return fmt.Errorf("copy %s: %w", dst, err)
	}
	return writeMutationFileChecked(dstAbs, data, afterMode, e.logger, dstState, dstParentGuard)
}

func prepareMutation(prepare MutationPrepare, receipt MutationReceipt) (MutationComplete, error) {
	if prepare == nil {
		return nil, nil
	}
	return prepare(receipt)
}

func finishPreparedMutation(result *error, complete MutationComplete, operationID string) {
	if complete == nil {
		return
	}
	completionErr := complete(operationID, *result == nil || WriteCommitted(*result))
	if *result == nil && completionErr != nil {
		*result = fmt.Errorf("complete mutation receipt: %w", completionErr)
	}
}

func absentState() MutationState { return MutationState{Kind: MutationAbsent} }

func regularState(data []byte, mode os.FileMode) MutationState {
	return MutationState{Kind: MutationRegular, Bytes: append([]byte(nil), data...), Mode: MutationFileMode(mode)}
}

func snapshotMutationState(path string) (MutationState, error) {
	fi, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return absentState(), nil
	}
	if err != nil {
		return MutationState{}, err
	}
	if fi.IsDir() {
		return MutationState{Kind: MutationDirectory, Mode: MutationDirectoryMode(fi.Mode())}, nil
	}
	if !fi.Mode().IsRegular() {
		return MutationState{}, fmt.Errorf("unsupported path type: %s", path)
	}
	if fi.Size() > maxTrackedMutationBytes {
		return MutationState{}, fmt.Errorf("%s exceeds the %d MiB tracked-mutation limit", path, maxTrackedMutationBytes>>20)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return MutationState{}, err
	}
	return regularState(data, fi.Mode()), nil
}

func missingParentMutationPaths(path string) ([]MutationPathState, error) {
	var missing []string
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		info, err := os.Lstat(dir)
		if err == nil {
			if !info.IsDir() {
				return nil, fmt.Errorf("parent path %s is not a directory", dir)
			}
			break
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		missing = append(missing, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, fmt.Errorf("no existing parent for %s", path)
		}
	}
	paths := make([]MutationPathState, 0, len(missing))
	for index := len(missing) - 1; index >= 0; index-- {
		paths = append(paths, MutationPathState{Path: missing[index], Before: absentState(), After: MutationState{Kind: MutationDirectory, Mode: MutationDirectoryMode(0o755)}})
	}
	return paths, nil
}

func writeMutationFileChecked(path string, data []byte, mode os.FileMode, lg *logger.Logger, before MutationState, guards ...mutationParentGuard) error {
	var precommit func() error
	if before.Kind != "" {
		precommit = func() error {
			for _, guard := range guards {
				if err := guard.validate(path); err != nil {
					return err
				}
			}
			return validateMutationState(path, before)
		}
	}
	if err := writeAtomicChecked(path, data, mode.Perm(), lg, precommit); err != nil {
		return err
	}
	// writeAtomic deliberately uses permission bits for its temporary file;
	// restore special bits on the committed image as part of the same
	// operation's postimage.
	special := mode & (os.ModeSetuid | os.ModeSetgid | os.ModeSticky)
	if special != 0 {
		if err := os.Chmod(path, mode.Perm()|special); err != nil {
			return fmt.Errorf("set mutation mode: %w", err)
		}
	}
	return nil
}

func validateMutationPreconditions(paths []MutationPathState, ignored map[string]bool) error {
	for _, path := range paths {
		if ignored[path.Path] {
			continue
		}
		if err := validateMutationState(path.Path, path.Before); err != nil {
			return err
		}
	}
	return nil
}

func validateMutationState(path string, want MutationState) error {
	got, err := snapshotMutationState(path)
	if err != nil {
		return err
	}
	if got.Kind != want.Kind || got.Mode != want.Mode || !bytes.Equal(got.Bytes, want.Bytes) {
		return fmt.Errorf("%s changed since the operation receipt was recorded", path)
	}
	return nil
}

func cleanupPreparedParents(parents []MutationPathState, result *error) {
	if result == nil || *result == nil {
		return
	}
	for index := len(parents) - 1; index >= 0; index-- {
		_ = os.Remove(parents[index].Path)
	}
}

func rejectHardlinkMutation(path string, info os.FileInfo) error {
	multiple, err := hasMultipleHardlinks(path, info)
	if err != nil {
		return fmt.Errorf("inspect hardlinks for %s: %w", path, err)
	}
	if multiple {
		return fmt.Errorf("refusing to mutate hard-linked file %s because selective undo cannot preserve link topology", path)
	}
	return nil
}

func newOpID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("fileio: entropy source failed: %v", err))
	}
	return hex.EncodeToString(b)
}
