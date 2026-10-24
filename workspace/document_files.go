package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"unicode/utf8"

	"crude/document"
)

// MutateFiles fences canonical edits, opens and saves through the disk operation.
// Invoke only off the actor/UI thread. FileOps owns the nested mutation lease;
// the lock order matches DocumentService.Save (document, then filesystem).
// Closing a view does not discard retained work or author history.
func (s *DocumentService) MutateFiles(ctx context.Context, op FileOperation, paths []string, edits []FileEdit, run func() FileOperationResultMsg) FileOperationResultMsg {
	s.mu.Lock()
	changed := make([]string, 0, len(edits))
	defer func() {
		s.mu.Unlock()
		for _, id := range changed {
			s.changed(id)
		}
	}()
	fail := func(err error) FileOperationResultMsg {
		return FileOperationResultMsg{Op: op, Err: err, Detail: "shared document conflict; no files changed"}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if s.closed {
		return fail(context.Canceled)
	}
	if s.stateErr != nil {
		return fail(s.stateErr)
	}
	if op == FileOpRename {
		return s.renameDocumentsLocked(paths, run, &changed)
	}
	for _, d := range s.docs {
		for _, path := range paths {
			if !documentUnder(path, d.snapshot.Path) {
				continue
			}
			if op != FileOpDelete || d.snapshot.Dirty || d.snapshot.Conflict || d.diskIntent != nil || d.relocation != nil {
				return fail(fmt.Errorf("cannot %s %s: retained shared work must be saved/reconciled first", op, path))
			}
			changed = append(changed, d.snapshot.ID)
		}
	}

	for _, edit := range edits {
		d := s.documentAtPath(filepath.Clean(edit.Path))
		if d == nil {
			continue
		}
		if d.snapshot.Dirty || d.snapshot.Conflict || d.diskIntent != nil || !bytes.Equal(d.snapshot.Text, []byte(edit.OldContent)) {
			return fail(fmt.Errorf("cannot edit %s: shared text has unsaved changes or a stale preimage; save/reconcile the shared document and review the edit again", edit.Path))
		}
		if len(edit.NewContent) > document.MaxBytes || !utf8.ValidString(edit.NewContent) {
			return fail(fmt.Errorf("cannot edit shared document %s: replacement must be UTF-8 within the document size limit", edit.Path))
		}
		// Prove reconciliation can retain a complete disk-change operation before
		// any effect, rather than discovering journal capacity after publication.
		trial := stageDocument(d)
		_, err := s.commit(trial, document.Change{DocumentID: d.snapshot.ID, RequestID: "disk", BaseRevision: d.snapshot.Revision, Edits: []document.Edit{{Start: 0, End: len(d.snapshot.Text), Text: edit.NewContent}}})
		if err != nil {
			return fail(err)
		}
		changed = append(changed, d.snapshot.ID)
	}
	result := run()
	// Reconcile even failed commits: rollback can fail and leave changed disk
	// bytes. The canonical state must reflect that rather than trust success.
	for _, id := range changed {
		if err := s.reconcileLocked(id); err != nil {
			result.Err = errors.Join(result.Err, err)
			result.Detail = "disk changed; shared publication requires reconciliation; do not replay"
		}
	}
	if result.Err == nil {
		for _, edit := range edits {
			if d := s.documentAtPath(filepath.Clean(edit.Path)); d != nil && (d.snapshot.Conflict || !bytes.Equal(d.snapshot.Text, []byte(edit.NewContent))) {
				result.Err = fmt.Errorf("disk edit committed but shared document %s requires reconciliation; do not replay the edit", edit.Path)
				result.Detail = "disk changed; shared publication uncertain"
				break
			}
		}
	}
	return result
}

// Identity is retained separately from the current disk path. No ID aliases are
// accepted by path consumers: a moved document is found only at Snapshot.Path.
func (s *DocumentService) documentAtPath(path string) *liveDocument {
	for _, d := range s.docs {
		if d.snapshot.Path == path {
			return d
		}
	}
	return nil
}
func documentUnder(root, path string) bool {
	rel, err := filepath.Rel(filepath.Clean(root), path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Every affected checkpoint holds the same immutable transaction evidence.
// Recovery never reexecutes a rename and never guesses across two extant paths.
type documentRelocation struct {
	Source, Target string
	Preimages      map[string]documentPathPreimage
}
type documentPathPreimage struct {
	Hash           [32]byte
	Mode           uint32
	Size, Modified int64
	Device, Inode  uint64
}

func documentPreimage(path string) (documentPathPreimage, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return documentPathPreimage{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return documentPathPreimage{}, fmt.Errorf("symlink relocation forbidden: %s", path)
	}
	p := documentPathPreimage{Mode: uint32(info.Mode()), Size: info.Size(), Modified: info.ModTime().UnixNano()}
	v := reflect.ValueOf(info.Sys())
	if v.Kind() == reflect.Pointer {
		v = v.Elem()
	}
	if v.Kind() == reflect.Struct {
		for name, dst := range map[string]*uint64{"Dev": &p.Device, "Ino": &p.Inode} {
			field := v.FieldByName(name)
			if field.IsValid() && field.CanUint() {
				*dst = field.Uint()
			}
		}
	}
	if info.Mode().IsRegular() {
		text, _, err := readLiveDocument(path)
		if err != nil {
			return p, err
		}
		p.Hash = sha256.Sum256(text)
	}
	return p, nil
}
func (s *DocumentService) renameDocumentsLocked(paths []string, run func() FileOperationResultMsg, changed *[]string) FileOperationResultMsg {
	fail := func(err error) FileOperationResultMsg {
		return FileOperationResultMsg{Op: FileOpRename, Err: err, Detail: "shared relocation refused; no files changed"}
	}
	if len(paths) != 2 || !filepath.IsAbs(paths[0]) || !filepath.IsAbs(paths[1]) || paths[0] == paths[1] || documentUnder(paths[0], paths[1]) {
		return fail(fmt.Errorf("invalid shared relocation paths"))
	}
	source, target := filepath.Clean(paths[0]), filepath.Clean(paths[1])
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		return fail(fmt.Errorf("relocation destination exists or is inaccessible"))
	}
	intent := &documentRelocation{Source: source, Target: target, Preimages: make(map[string]documentPathPreimage)}
	pre, err := documentPreimage(source)
	if err != nil {
		return fail(err)
	}
	intent.Preimages[source] = pre
	staged := []*liveDocument{}
	for _, d := range s.docs {
		if documentUnder(target, d.snapshot.Path) {
			return fail(fmt.Errorf("relocation destination has retained document %s", d.snapshot.Path))
		}
		if !documentUnder(source, d.snapshot.Path) {
			continue
		}
		if d.relocation != nil || d.diskIntent != nil || d.snapshot.Conflict {
			return fail(fmt.Errorf("document %s requires reconciliation before relocation", d.snapshot.Path))
		}
		pre, err = documentPreimage(d.snapshot.Path)
		if err != nil {
			return fail(err)
		}
		if pre.Hash != d.snapshot.DiskHash || !d.snapshot.DiskExists {
			return fail(fmt.Errorf("document disk preimage changed: %s", d.snapshot.Path))
		}
		intent.Preimages[d.snapshot.Path] = pre
		trial := stageDocument(d)
		trial.relocation = intent
		staged = append(staged, trial)
	}
	if len(staged) == 0 {
		return run()
	}
	if err = s.persistDocuments(staged); err != nil {
		return fail(err)
	}
	for _, trial := range staged {
		*s.docs[trial.snapshot.ID] = *trial
		*changed = append(*changed, trial.snapshot.ID)
	}
	// Recheck staged evidence immediately before handing disk ownership to FileOps.
	for path, expected := range intent.Preimages {
		got, e := documentPreimage(path)
		if e != nil || got != expected {
			return fail(fmt.Errorf("relocation preimage changed; reconciliation required"))
		}
	}
	result := run()
	if err := s.recoverRelocationsLocked(); err != nil {
		result.Err = fmt.Errorf("shared relocation result uncertain; do not replay; reconciliation required: %w", err)
		result.Detail = "durable relocation retained"
	}
	return result
}

func (s *DocumentService) recoverRelocationsLocked() error {
	groups := map[string]*documentRelocation{}
	for _, d := range s.docs {
		if d.relocation != nil {
			groups[d.relocation.Source] = d.relocation
		}
	}
	for _, intent := range groups {
		_, srcErr := os.Lstat(intent.Source)
		_, dstErr := os.Lstat(intent.Target)
		moved := os.IsNotExist(srcErr) && dstErr == nil
		original := srcErr == nil && os.IsNotExist(dstErr)
		valid := moved || original
		for path, pre := range intent.Preimages {
			check := path
			if moved {
				rel, _ := filepath.Rel(intent.Source, path)
				check = filepath.Join(intent.Target, rel)
			}
			got, err := documentPreimage(check)
			if err != nil || got != pre {
				valid = false
			}
		}
		staged := []*liveDocument{}
		for _, d := range s.docs {
			if d.relocation == nil || d.relocation.Source != intent.Source {
				continue
			}
			trial := stageDocument(d)
			if valid {
				if moved {
					rel, _ := filepath.Rel(intent.Source, trial.snapshot.Path)
					trial.snapshot.Path = filepath.Join(intent.Target, rel)
				}
				trial.relocation = nil
			}
			trial.snapshot.Conflict = !valid
			staged = append(staged, trial)
		}
		if err := s.persistDocuments(staged); err != nil {
			for _, trial := range staged {
				s.docs[trial.snapshot.ID].snapshot.Conflict = true
			}
			return err
		}
		for _, trial := range staged {
			d := s.docs[trial.snapshot.ID]
			old := d.snapshot.Path
			*d = *trial
			if old != d.snapshot.Path {
				if s.saves != nil {
					s.saves.Cancel(d.snapshot.ID)
				}
				if s.watch != nil {
					if err := s.watch.add(filepath.Dir(d.snapshot.Path)); err != nil {
						return err
					}
				}
				if s.lsp != nil && d.lspOpen {
					s.lsp.CloseFile(context.Background(), old)
					s.lsp.OpenFile(context.Background(), d.snapshot.Path, string(d.snapshot.Text))
				}
			}
		}
		if !valid {
			return fmt.Errorf("ambiguous relocation %s to %s; both disk paths and canonical work preserved", intent.Source, intent.Target)
		}
	}
	return nil
}
