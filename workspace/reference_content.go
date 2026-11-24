package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"unicode/utf8"

	"crude/agent/prompthistory"
	"crude/agent/sessiondb"
	"crude/workspace/fileio"
)

// ReadReferenceMedia reads original bytes only for an exact, still-owned
// reference preimage. Shell viewers use it off Update, never a server path.
func (s *ReferenceStore) ReadReferenceMedia(ctx context.Context, expected sessiondb.Reference) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok := s.Reference(expected.ID)
	if !ok || !current.Active() || current != expected || current.SessionID != s.sessionID {
		return nil, fmt.Errorf("attachment changed or belongs to another session")
	}
	if current.FilePath == "" {
		return nil, fmt.Errorf("attachment has no captured media")
	}
	data, err := prompthistory.ReadAttachment(current.FilePath)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	current, ok = s.Reference(expected.ID)
	if !ok || current != expected {
		return nil, fmt.Errorf("attachment changed while reading")
	}
	return data, nil
}

// ReadReferenceContent returns the complete editable text for pasted text
// references. Other file-backed references are bounded previews and read-only.
// Callers must keep this filesystem and database work off the UI thread.
func (s *ReferenceStore) ReadReferenceContent(id string) (content string, readOnly bool, err error) {
	s.mu.RLock()
	ref, ok := s.refs[id]
	s.mu.RUnlock()
	if !ok || !ref.Active() {
		return "", false, fmt.Errorf("reference %q is missing or inactive", id)
	}

	switch ref.Kind {
	case sessiondb.KindPastedText:
		if !utf8.ValidString(ref.Content) {
			return "", false, fmt.Errorf("reference %q contains invalid UTF-8", id)
		}
		return ref.Content, false, nil
	case sessiondb.KindCodeSelection:
		// The captured lines are the reference, not the file as it is now.
		if !utf8.ValidString(ref.Content) {
			return "", true, fmt.Errorf("reference %q contains invalid UTF-8", id)
		}
		return ref.Content, true, nil
	case sessiondb.KindPastedTextFile:
		data, err := prompthistory.ReadAttachment(ref.FilePath)
		if err != nil {
			return "", false, fmt.Errorf("read reference %q: %w", id, err)
		}
		if !utf8.Valid(data) {
			return "", false, fmt.Errorf("reference %q contains invalid UTF-8", id)
		}
		return string(data), false, nil
	default:
		if ref.FilePath == "" {
			if !utf8.ValidString(ref.Content) {
				return "", true, fmt.Errorf("reference %q contains invalid UTF-8", id)
			}
			return ref.Content, true, nil
		}
		preview, err := fileio.InspectFile(ref.FilePath, fileio.DefaultPreviewBytes, fileio.DefaultHexHeadBytes)
		if err != nil {
			return "", true, fmt.Errorf("read reference %q: %w", id, err)
		}
		return preview.ReadableText(), true, nil
	}
}

// PreparePastedTextFile returns the session-owned path for an editable paste.
// A file-backed paste is reused. An inline paste gets a backing file only on
// this explicit open; its ID, label and all references in the draft survive.
func (s *ReferenceStore) PreparePastedTextFile(ctx context.Context, expected sessiondb.Reference) (sessiondb.Reference, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := ctx.Err(); err != nil {
		return sessiondb.Reference{}, err
	}
	ref, ok := s.Reference(expected.ID)
	if !ok || !ref.Active() || ref != expected || ref.SessionID != s.sessionID {
		return sessiondb.Reference{}, errors.New("attachment changed; open it again")
	}
	if ref.Kind == sessiondb.KindPastedTextFile && ref.FilePath != "" {
		return ref, nil
	}
	if ref.Kind != sessiondb.KindPastedText || !utf8.ValidString(ref.Content) {
		return sessiondb.Reference{}, errors.New("this attachment is not editable text")
	}
	path, err := s.writeReferenceFile("Pasted-Text", []byte(ref.Content), ".txt")
	if err != nil {
		return sessiondb.Reference{}, err
	}
	next := ref
	next.Kind, next.FilePath, next.Content = sessiondb.KindPastedTextFile, path, ""
	err = ctx.Err()
	if err == nil && s.db != nil {
		var ok bool
		ok, err = updateReferencePrecondition(s.db, ref, next)
		if err == nil && !ok {
			err = errors.New("attachment changed; open it again")
		}
	}
	if err != nil {
		_ = os.Remove(path)
		return sessiondb.Reference{}, err
	}
	s.mu.Lock()
	s.refs[ref.ID] = next
	s.mu.Unlock()
	return next, nil
}

// SaveReferenceContent replaces editable pasted text only when the caller's
// complete reference preimage and original bytes still match. Callers must run
// this filesystem and database work off the UI thread.
func (s *ReferenceStore) SaveReferenceContent(expected sessiondb.Reference, original, content string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if !utf8.ValidString(original) || !utf8.ValidString(content) {
		return fmt.Errorf("reference content must be valid UTF-8")
	}

	s.mu.Lock()
	current, ok := s.refs[expected.ID]
	s.mu.Unlock()
	if !ok || !current.Active() {
		return fmt.Errorf("reference %q is missing or inactive", expected.ID)
	}
	if current != expected {
		return fmt.Errorf("reference %q changed since it was opened", expected.ID)
	}

	switch current.Kind {
	case sessiondb.KindPastedText:
		if current.Content != original {
			return fmt.Errorf("reference %q content changed since it was opened", current.ID)
		}
		if s.db != nil {
			next := expected
			next.Content = content
			updated, err := updateReferencePrecondition(s.db, expected, next)
			if err != nil {
				return err
			}
			if !updated {
				return fmt.Errorf("reference %q changed or was deleted", current.ID)
			}
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.refs[current.ID] != expected {
			return fmt.Errorf("reference %q changed during save", current.ID)
		}
		current.Content = content
		s.refs[current.ID] = current
		return nil

	case sessiondb.KindPastedTextFile:
		if current.FilePath == "" {
			return fmt.Errorf("reference %q has no backing file", current.ID)
		}
		unlock := fileio.LockFileMutation(current.FilePath)
		defer unlock()

		oldBytes, err := prompthistory.ReadAttachment(current.FilePath)
		if err != nil {
			return fmt.Errorf("read reference %q before save: %w", current.ID, err)
		}
		if !utf8.Valid(oldBytes) {
			return fmt.Errorf("reference %q contains invalid UTF-8", current.ID)
		}
		if string(oldBytes) != original {
			return fmt.Errorf("reference %q content changed since it was opened", current.ID)
		}
		info, err := os.Stat(current.FilePath)
		if err != nil {
			return fmt.Errorf("stat reference %q: %w", current.ID, err)
		}
		newBytes := []byte(content)
		if err := fileio.WriteAtomicChecked(current.FilePath, newBytes, info.Mode().Perm(), func() error {
			actual, readErr := prompthistory.ReadAttachment(current.FilePath)
			if readErr != nil {
				return readErr
			}
			if string(actual) != original {
				return errors.New("backing file changed since it was opened")
			}
			return nil
		}); err != nil {
			if rollbackErr := restoreReferenceFile(current.FilePath, oldBytes, newBytes, info.Mode().Perm()); rollbackErr != nil {
				return fmt.Errorf("save reference %q: %v; rollback failed: %w", current.ID, err, rollbackErr)
			}
			return fmt.Errorf("save reference %q: %w", current.ID, err)
		}

		if s.db != nil {
			updated, err := updateReferencePrecondition(s.db, expected, expected)
			if err == nil && !updated {
				err = fmt.Errorf("reference %q changed or was deleted", current.ID)
			}
			if err != nil {
				if rollbackErr := restoreReferenceFile(current.FilePath, oldBytes, newBytes, info.Mode().Perm()); rollbackErr != nil {
					return fmt.Errorf("%v; rollback failed: %w", err, rollbackErr)
				}
				return err
			}
		}
		return nil

	default:
		return fmt.Errorf("reference %q is read-only", current.ID)
	}
}

func updateReferencePrecondition(db *sql.DB, expected, next sessiondb.Reference) (bool, error) {
	inline := 0
	if expected.Inline {
		inline = 1
	}
	result, err := db.Exec(`
		UPDATE prompt_references
		SET content = ?, kind = ?, file_path = ?
		WHERE id = ?
		  AND session_id = ?
		  AND kind = ?
		  AND label = ?
		  AND COALESCE(title, '') = ?
		  AND COALESCE(mime, '') = ?
		  AND COALESCE(content, '') = ?
		  AND COALESCE(file_path, '') = ?
		  AND inline = ?
		  AND created_at = ?
		  AND COALESCE(deleted_at, 0) = ?
	`, next.Content, string(next.Kind), next.FilePath, expected.ID, expected.SessionID, string(expected.Kind), expected.Label,
		expected.Title, expected.Mime, expected.Content, expected.FilePath, inline,
		expected.CreatedAt, expected.DeletedAt)
	if err != nil {
		return false, fmt.Errorf("persist reference %q: %w", expected.ID, err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("verify persisted reference %q: %w", expected.ID, err)
	}
	return rows == 1, nil
}

func restoreReferenceFile(path string, original, replacement []byte, perm os.FileMode) error {
	actual, err := prompthistory.ReadAttachment(path)
	if err != nil {
		return err
	}
	if bytes.Equal(actual, original) {
		return nil
	}
	if !bytes.Equal(actual, replacement) {
		return errors.New("backing file changed again; refusing to overwrite it")
	}
	return fileio.WriteAtomicChecked(path, original, perm, func() error {
		current, readErr := prompthistory.ReadAttachment(path)
		if readErr != nil {
			return readErr
		}
		if !bytes.Equal(current, replacement) {
			return errors.New("backing file changed again; refusing to overwrite it")
		}
		return nil
	})
}
