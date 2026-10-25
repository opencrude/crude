package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"unicode/utf8"

	"crude/document"
	"crude/fileio"
)

const documentHistoryLimit = 1024
const documentHistoryBytes = 32 << 20
const documentCountLimit = 128
const documentRequestLimit = 16384

type documentOperation struct {
	revision       uint64
	inverseOf      uint64
	edits, inverse []document.Edit
	owner          document.Owner
	group          string
	change         document.Change
}
type documentUndoUnit []int
type liveDocument struct {
	snapshot     document.Snapshot
	owners       map[document.Owner]bool
	journal      []documentOperation
	undo, redo   map[document.Owner][]documentUndoUnit
	historyBytes int
	lspOpen      bool
	requests     map[string]documentRequest
	diskIntent   *documentDiskIntent
	relocation   *documentRelocation
}
type documentRequest struct {
	hash    [32]byte
	receipt document.Receipt
	err     error
}

// DocumentService owns live text, savepoints, revisions and per-owner undo.
// All methods serialize, including disk saves. Invoke I/O methods off any UI
// thread. The supplied save/LSP services are borrowed, not closed by this owner.
type DocumentService struct {
	mu          sync.Mutex
	ownerMu     sync.Mutex
	saveCancels map[document.Owner]context.CancelFunc
	stateDB     *sql.DB
	stateErr    error
	stateSizes  map[string]int
	root        string
	saves       *DocumentSaveService
	lsp         *LSPManager
	docs        map[string]*liveDocument
	epochs      map[string]uint64
	detached    map[document.Owner]bool
	closed      bool
	idleFenced  bool
	watch       *documentWatch
	bus         *EventBus
	subscribers map[string]map[chan struct{}]bool
}

func NewDocumentService(root string, saves *DocumentSaveService, lsp *LSPManager, state ...*sql.DB) *DocumentService {
	s := &DocumentService{root: root, saves: saves, lsp: lsp, docs: make(map[string]*liveDocument), epochs: make(map[string]uint64), detached: make(map[document.Owner]bool)}
	s.stateSizes = make(map[string]int)
	s.saveCancels = make(map[document.Owner]context.CancelFunc)
	if len(state) > 0 {
		s.stateErr = s.attachState(state[0])
	}
	s.watch = newDocumentWatch(s)
	if s.stateErr == nil {
		_ = s.recoverRelocationsLocked()
		for id, d := range s.docs {
			if d.relocation != nil {
				// An ambiguous move preserves a readable retained document even
				// when its old parent directory no longer exists.
				continue
			}
			// Inactive checkpoints outlive their disk paths. A lost directory or
			// unavailable watch is not a failure of the workspace's durable state;
			// reconcile every document and require its watch when a view opens.
			_ = s.watch.add(filepath.Dir(d.snapshot.Path))
			s.reconcile(id)
		}
	}
	return s
}

func (s *DocumentService) Open(owner document.Owner, path string) (document.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.idleFenced {
		return document.Snapshot{}, context.Canceled
	}
	if s.stateErr != nil {
		return document.Snapshot{}, s.stateErr
	}
	if !s.admitOwner(owner) {
		return document.Snapshot{}, document.ErrOwner
	}
	if path == "" || len(path) > 4096 {
		return document.Snapshot{}, document.ErrLimit
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.root, path)
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return document.Snapshot{}, err
	}
	d := s.documentAtPath(path)
	// Refuse symlink aliases, matching the checked disk writer's path contract.
	// A retained document whose parent was removed remains readable for recovery;
	// this never authorizes creating the missing path or following a new alias.
	err = fileio.CheckDirectoryNoSymlinks(filepath.Dir(path))
	missingParent := errors.Is(err, os.ErrNotExist)
	if err != nil && (d == nil || !missingParent) {
		return document.Snapshot{}, errors.New("document parent must be an existing non-symlink directory")
	}
	if d == nil {
		if s.docs[path] != nil {
			return document.Snapshot{}, errors.New("document identity is retained at another path")
		}
		if len(s.docs) >= documentCountLimit {
			return document.Snapshot{}, document.ErrLimit
		}
		text, exists, err := readLiveDocument(path)
		if err != nil {
			return document.Snapshot{}, err
		}
		d = &liveDocument{snapshot: document.Snapshot{ID: path, Path: path, Revision: 1, Text: text, Saved: bytes.Clone(text), DiskHash: sha256.Sum256(text), DiskExists: exists}, owners: make(map[document.Owner]bool), undo: make(map[document.Owner][]documentUndoUnit), redo: make(map[document.Owner][]documentUndoUnit), requests: make(map[string]documentRequest)}
		if err := s.persistDocument(d); err != nil {
			return document.Snapshot{}, err
		}
		s.docs[path] = d
	}
	if missingParent {
		trial := stageDocument(d)
		trial.snapshot.Conflict = true
		if err := s.persistDocument(trial); err != nil {
			return document.Snapshot{}, err
		}
		*d = *trial
	} else if err := s.watch.add(filepath.Dir(path)); err != nil {
		return document.Snapshot{}, err
	}
	if owner.Epoch > s.ownerEpoch(owner.ClientID) {
		for _, live := range s.docs {
			for previous := range live.owners {
				if previous.ClientID == owner.ClientID {
					delete(live.owners, previous)
				}
			}
			s.closeLanguageView(live)
		}
	}
	s.ownerMu.Lock()
	if s.detached[owner] || owner.Epoch < s.epochs[owner.ClientID] {
		s.ownerMu.Unlock()
		return document.Snapshot{}, document.ErrOwner
	}
	s.epochs[owner.ClientID] = owner.Epoch
	d.owners[owner] = true
	s.ownerMu.Unlock()
	if !d.lspOpen && s.lsp != nil {
		s.lsp.OpenFile(context.Background(), path, string(d.snapshot.Text))
		d.lspOpen = true
	}
	return cloneDocument(d.snapshot), nil
}

func readLiveDocument(path string) ([]byte, bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !info.Mode().IsRegular() {
		return nil, true, errors.New("document is not a regular file")
	}
	if info.Size() > document.MaxBytes {
		return nil, true, document.ErrLimit
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, true, err
	}
	defer func() { _ = f.Close() }()
	text, err := io.ReadAll(io.LimitReader(f, document.MaxBytes+1))
	if err != nil {
		return nil, true, err
	}
	if len(text) > document.MaxBytes {
		return nil, true, document.ErrLimit
	}
	if !utf8.Valid(text) {
		return nil, true, errors.New("document is not UTF-8")
	}
	return text, true, nil
}

func cloneDocument(s document.Snapshot) document.Snapshot {
	s.Text = bytes.Clone(s.Text)
	s.Saved = bytes.Clone(s.Saved)
	return s
}
func (s *DocumentService) Snapshot(id string) (document.Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return document.Snapshot{}, context.Canceled
	}
	d := s.docs[id]
	if d == nil {
		return document.Snapshot{}, os.ErrNotExist
	}
	return cloneDocument(d.snapshot), nil
}

// SnapshotIfOpen observes canonical retained work without acquiring a view or
// starting language services. Dirty zero-view documents count as open here.
func (s *DocumentService) SnapshotIfOpen(path string) (document.Snapshot, bool) {
	if !filepath.IsAbs(path) {
		path = filepath.Join(s.root, path)
	}
	path = filepath.Clean(path)
	s.mu.Lock()
	defer s.mu.Unlock()
	d := s.documentAtPath(path)
	if s.closed || d == nil {
		return document.Snapshot{}, false
	}
	return cloneDocument(d.snapshot), true
}
func (s *DocumentService) owned(owner document.Owner, id string) (*liveDocument, error) {
	if s.closed {
		return nil, context.Canceled
	}
	d := s.docs[id]
	if d == nil {
		return nil, os.ErrNotExist
	}
	s.ownerMu.Lock()
	valid := !s.detached[owner] && s.epochs[owner.ClientID] == owner.Epoch
	s.ownerMu.Unlock()
	if !d.owners[owner] || !valid {
		return nil, document.ErrOwner
	}
	return d, nil
}

// request IDs are scoped to the exact owner epoch, across all its documents.
func (s *DocumentService) command(kind string, c document.Change, run func(*liveDocument) (uint64, error)) (document.Receipt, error) {
	r := document.Receipt{RequestID: c.RequestID, DocumentID: c.DocumentID}
	if c.RequestID == "" || len(c.RequestID) > 256 || len(c.GroupID) > 256 || len(c.Owner.ClientID) > 256 || len(c.DocumentID) > 4096 {
		r.Error = document.ErrLimit.Error()
		return r, document.ErrLimit
	}
	keyBytes, _ := json.Marshal(struct {
		Owner document.Owner
		ID    string
	}{c.Owner, c.RequestID})
	payload, _ := json.Marshal(struct {
		Kind   string
		Change document.Change
	}{kind, c})
	hash := sha256.Sum256(payload)
	key := string(keyBytes)
	d, err := s.owned(c.Owner, c.DocumentID)
	if err != nil {
		r.Error = err.Error()
		return r, err
	}
	r.Before = d.snapshot.Revision
	r.Revision = r.Before
	prior, found, lookupErr := s.lookupDocumentRequest(key)
	if lookupErr != nil {
		r.Error = lookupErr.Error()
		return r, lookupErr
	}
	if found {
		if prior.hash != hash {
			r.Error = document.ErrRequest.Error()
			return r, document.ErrRequest
		}
		return prior.receipt, prior.err
	}
	r.Before = d.snapshot.Revision
	r.Revision = r.Before
	trial := stageDocumentCommand(d, c.Owner)
	if c.BaseRevision != r.Before {
		err = document.ErrRevision
	} else {
		r.Revision, err = run(trial)
	}
	r.Accepted = err == nil
	if err != nil {
		r.Error = err.Error()
	}
	if err != nil {
		trial = stageDocumentCommand(d, c.Owner)
		r.Revision = r.Before
	}
	request := documentRequest{hash: hash, receipt: r, err: err}
	if storageErr := s.persistDocumentCommand(d, trial, author(c.Owner), key, request); storageErr != nil {
		r.Accepted = false
		r.Revision = r.Before
		r.Error = storageErr.Error()
		return r, storageErr
	}
	*d = *trial
	s.cacheDocumentRequest(d, key, request)
	if err == nil && s.lsp != nil && d.lspOpen {
		s.lsp.UpdateFile(context.Background(), d.snapshot.Path, string(d.snapshot.Text))
	}
	return r, err
}

func (s *DocumentService) commit(d *liveDocument, c document.Change) (int, error) {
	text, inverse, err := document.Apply(d.snapshot.Text, c.Edits)
	if err != nil {
		return 0, err
	}
	weight := 0
	for _, e := range c.Edits {
		weight += len(e.Text)
	}
	for _, e := range inverse {
		weight += len(e.Text)
	}
	rollDocumentHistory(d)
	if len(d.journal) >= documentHistoryLimit || d.historyBytes+weight > documentHistoryBytes {
		return 0, document.ErrLimit
	}
	d.snapshot.Text = text
	d.snapshot.Revision++
	d.snapshot.Dirty = !bytes.Equal(text, d.snapshot.Saved)
	d.journal = append(d.journal, documentOperation{revision: d.snapshot.Revision, edits: append([]document.Edit(nil), c.Edits...), inverse: inverse, owner: c.Owner, group: c.GroupID, change: cloneChange(c)})
	d.historyBytes += weight
	return len(d.journal) - 1, nil
}

func (s *DocumentService) Apply(c document.Change) (document.Receipt, error) {
	s.mu.Lock()
	defer func() { s.mu.Unlock(); s.changed(c.DocumentID) }()
	// Bound untrusted command serialization before request hashing.
	if len(c.Edits) > document.MaxEdits {
		return document.Receipt{}, document.ErrLimit
	}
	weight := 0
	for _, e := range c.Edits {
		weight += len(e.Text)
		if weight > document.MaxBytes {
			return document.Receipt{}, document.ErrLimit
		}
	}
	return s.command("edit", c, func(d *liveDocument) (uint64, error) {
		index, err := s.commit(d, c)
		if err != nil {
			return d.snapshot.Revision, err
		}
		stack := d.undo[author(c.Owner)]
		if c.GroupID != "" && len(stack) > 0 && len(d.redo[author(c.Owner)]) == 0 && d.journal[stack[len(stack)-1][len(stack[len(stack)-1])-1]].inverseOf == 0 && d.journal[stack[len(stack)-1][len(stack[len(stack)-1])-1]].group == c.GroupID {
			stack[len(stack)-1] = append(stack[len(stack)-1], index)
		} else {
			stack = append(stack, documentUndoUnit{index})
		}
		d.undo[author(c.Owner)] = stack
		delete(d.redo, author(c.Owner))
		return d.snapshot.Revision, nil
	})
}

func (s *DocumentService) Undo(owner document.Owner, id, requestID string, expected uint64) (document.Receipt, error) {
	return s.history(false, owner, id, requestID, expected)
}
func (s *DocumentService) Redo(owner document.Owner, id, requestID string, expected uint64) (document.Receipt, error) {
	return s.history(true, owner, id, requestID, expected)
}
func (s *DocumentService) history(redo bool, owner document.Owner, id, requestID string, expected uint64) (document.Receipt, error) {
	s.mu.Lock()
	defer func() { s.mu.Unlock(); s.changed(id) }()
	kind := "undo"
	if redo {
		kind = "redo"
	}
	return s.command(kind, document.Change{DocumentID: id, RequestID: requestID, Owner: owner, BaseRevision: expected}, func(d *liveDocument) (uint64, error) {
		from, to := d.undo, d.redo
		if redo {
			from, to = d.redo, d.undo
		}
		stack := from[author(owner)]
		if len(stack) == 0 {
			return d.snapshot.Revision, errors.New("document history is empty")
		}
		unit := stack[len(stack)-1]
		// Prepare the whole group on a detached document, so a later conflict or
		// capacity failure cannot partially undo a group.
		trial := &liveDocument{snapshot: d.snapshot, journal: append([]documentOperation(nil), d.journal...), historyBytes: d.historyBytes}
		result := documentUndoUnit{}
		for i := len(unit) - 1; i >= 0; i-- {
			index := unit[i]
			edits := trial.journal[index].inverse
			later, err := effectiveDocumentHistory(trial.journal[index+1:])
			if err != nil {
				return d.snapshot.Revision, err
			}
			for _, op := range later {
				edits, err = document.Rebase(edits, op.edits)
				if err != nil {
					return d.snapshot.Revision, err
				}
			}
			text, inverse, err := document.Apply(trial.snapshot.Text, edits)
			if err != nil {
				return d.snapshot.Revision, err
			}
			weight := 0
			for _, e := range edits {
				weight += len(e.Text)
			}
			for _, e := range inverse {
				weight += len(e.Text)
			}
			trial.snapshot.Text = text
			trial.snapshot.Revision++
			trial.historyBytes += weight
			group := trial.journal[index].group
			trial.journal = append(trial.journal, documentOperation{revision: trial.snapshot.Revision, inverseOf: trial.journal[index].revision, edits: edits, inverse: inverse, owner: owner, group: group, change: document.Change{DocumentID: id, RequestID: requestID, Owner: owner, BaseRevision: trial.snapshot.Revision - 1, GroupID: group, Edits: append([]document.Edit(nil), edits...)}})
			result = append(result, len(trial.journal)-1)
		}
		d.snapshot = trial.snapshot
		d.snapshot.Dirty = !bytes.Equal(d.snapshot.Text, d.snapshot.Saved)
		d.journal = trial.journal
		d.historyBytes = trial.historyBytes
		from[author(owner)] = stack[:len(stack)-1]
		to[author(owner)] = append(to[author(owner)], result)
		// Retire whole units only after their indices have been consumed. Undo
		// and redo must roll history too, rather than permanently filling it.
		for len(d.journal) >= documentHistoryLimit || d.historyBytes >= documentHistoryBytes/2 {
			rollDocumentHistory(d)
		}
		return d.snapshot.Revision, nil
	})
}

// effectiveDocumentHistory reduces explicit operation/inverse pairs in a suffix.
// Simply skipping their indices is wrong: intervening edits are expressed in the
// post-operation coordinates. Commute the inverse through those disjoint edits
// and move both their forward and inverse spans back to the pre-operation space.
// Revision provenance, not equal text or author identity, authorizes cancellation.
func effectiveDocumentHistory(journal []documentOperation) ([]documentOperation, error) {
	effective := make([]documentOperation, 0, len(journal))
	for _, op := range journal {
		index := -1
		if op.inverseOf != 0 {
			for i := len(effective) - 1; i >= 0; i-- {
				if effective[i].revision == op.inverseOf {
					index = i
					break
				}
			}
		}
		if index < 0 {
			effective = append(effective, op)
			continue
		}
		inverse := effective[index].inverse
		for i := index + 1; i < len(effective); i++ {
			later := &effective[i]
			movedInverse, err := document.Rebase(inverse, later.edits)
			if err != nil {
				return nil, err
			}
			edits, err := document.Rebase(later.edits, inverse)
			if err != nil {
				return nil, err
			}
			undo, err := document.Rebase(later.inverse, movedInverse)
			if err != nil {
				return nil, err
			}
			later.edits, later.inverse = edits, undo
			inverse = movedInverse
		}
		effective = append(effective[:index], effective[index+1:]...)
	}
	return effective, nil
}

// Save requires an owner separately from the document ID and exact revision.
// No formatting or synthetic text change occurs. The mutex fences edits through
// the checked commit, so its savepoint cannot acknowledge newer text.
func (s *DocumentService) Save(ctx context.Context, owner document.Owner, id string, revision uint64, reason DocumentSaveReason) (document.Snapshot, error) {
	s.mu.Lock()
	defer func() { s.mu.Unlock(); s.changed(id) }()
	d, err := s.owned(owner, id)
	if err != nil {
		return document.Snapshot{}, err
	}
	if revision != d.snapshot.Revision {
		return cloneDocument(d.snapshot), document.ErrRevision
	}
	if s.saves == nil {
		return cloneDocument(d.snapshot), fmt.Errorf("document disk authority is not configured")
	}
	ctx, cancel := context.WithCancel(ctx)
	s.ownerMu.Lock()
	if s.detached[owner] || s.epochs[owner.ClientID] != owner.Epoch {
		s.ownerMu.Unlock()
		cancel()
		return cloneDocument(d.snapshot), document.ErrOwner
	}
	s.saveCancels[owner] = cancel
	s.ownerMu.Unlock()
	defer func() {
		cancel()
		s.ownerMu.Lock()
		delete(s.saveCancels, owner)
		s.ownerMu.Unlock()
	}()
	if d.snapshot.Conflict {
		return cloneDocument(d.snapshot), ErrDocumentConflict
	}
	if d.diskIntent != nil || d.relocation != nil {
		return cloneDocument(d.snapshot), errors.New("document disk commit requires reconciliation")
	}
	// Durable intent precedes disk effects, retaining exact evidence if the final
	// savepoint transaction fails or the core crashes between the two commits.
	intent := stageDocument(d)
	intent.diskIntent = &documentDiskIntent{Revision: revision, Hash: sha256.Sum256(d.snapshot.Text), Exists: true}
	if err := s.persistDocument(intent); err != nil {
		return cloneDocument(d.snapshot), err
	}
	*d = *intent
	if s.lsp != nil && d.lspOpen {
		s.lsp.NotifyFileSave(ctx, d.snapshot.Path, string(d.snapshot.Text), reason, true)
	}
	r := s.saves.Save(ctx, DocumentSaveRequest{BufferID: id, Path: d.snapshot.Path, Data: bytes.Clone(d.snapshot.Text), Revision: revision, ExpectedHash: d.snapshot.DiskHash, ExpectedExist: d.snapshot.DiskExists, Reason: reason})
	trial := stageDocument(d)
	if r.Committed || r.Unchanged {
		trial.snapshot.Saved = bytes.Clone(d.snapshot.Text)
		trial.snapshot.DiskHash = r.DiskHash
		trial.snapshot.DiskExists = true
		trial.snapshot.Dirty = false
		trial.snapshot.Conflict = false
	}
	trial.diskIntent = nil
	if errors.Is(r.Err, ErrDocumentConflict) {
		trial.snapshot.Conflict = true
	}
	if err := s.persistDocument(trial); err != nil {
		d.snapshot.Conflict = true
		return cloneDocument(d.snapshot), fmt.Errorf("document disk result uncertain; reconcile required: %w", err)
	}
	*d = *trial
	if (r.Committed || r.Unchanged) && s.lsp != nil && d.lspOpen {
		s.lsp.NotifyFileSave(ctx, d.snapshot.Path, string(d.snapshot.Text), reason, false)
	}

	return cloneDocument(d.snapshot), r.Err
}

// Detach is an immediate connection fence, with no filesystem/LSP or mutation
// lock acquisition. The core schedules ReleaseDetached off its actor in order.
func (s *DocumentService) Detach(owner document.Owner) {
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	if owner.ClientID == "" || owner.Epoch == 0 || s.epochs[owner.ClientID] > owner.Epoch {
		return
	}
	s.epochs[owner.ClientID] = owner.Epoch
	for previous := range s.detached {
		if previous.ClientID == owner.ClientID {
			delete(s.detached, previous)
		}
	}
	s.detached[owner] = true
	if cancel := s.saveCancels[owner]; cancel != nil {
		cancel()
	}
}

// ReleaseDetached performs ordered final LSP release off the core actor. It may
// wait for an already accepted save; Detach itself never does.
func (s *DocumentService) ReleaseDetached(owner document.Owner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, d := range s.docs {
		delete(d.owners, owner)
		s.closeLanguageView(d)
	}
}
func (s *DocumentService) Close() {
	s.watch.close()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	for id, d := range s.docs {
		if s.saves != nil {
			s.saves.Cancel(id)
		}
		if s.lsp != nil {
			s.lsp.CloseFile(context.Background(), d.snapshot.Path)
		}
	}
}

// CloseView releases only this owner; retained text and history are not discarded.
func (s *DocumentService) CloseView(owner document.Owner, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.owned(owner, id)
	if err != nil {
		return err
	}
	delete(d.owners, owner)
	s.closeLanguageView(d)
	return nil
}

func (s *DocumentService) closeLanguageView(d *liveDocument) {
	s.ownerMu.Lock()
	for owner := range d.owners {
		if s.detached[owner] || s.epochs[owner.ClientID] != owner.Epoch {
			delete(d.owners, owner)
		}
	}
	s.ownerMu.Unlock()
	if len(d.owners) == 0 && d.lspOpen && s.lsp != nil {
		s.lsp.CloseFile(context.Background(), d.snapshot.Path)
		d.lspOpen = false
	}
}

// DirtyCount is retained work, not a shell-view count. Durable dirty checkpoints
// can survive idle shutdown; callers must not interpret this as active execution.
func (s *DocumentService) DirtyCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, d := range s.docs {
		if d.snapshot.Dirty {
			n++
		}
	}
	return n
}
func cloneChange(c document.Change) document.Change {
	c.Edits = append([]document.Edit(nil), c.Edits...)
	return c
}
func (s *DocumentService) Sync(owner document.Owner, id string, after uint64) (document.SyncResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.owned(owner, id)
	if err != nil {
		return document.SyncResult{}, err
	}
	out := s.syncDocumentLocked(d, after)
	if !out.Reset {
		out.Snapshot = cloneDocument(out.Snapshot)
	}
	return out, nil
}

func (s *DocumentService) syncDocumentLocked(d *liveDocument, after uint64) document.SyncResult {
	out := document.SyncResult{Snapshot: d.snapshot}
	if after == 0 || after > d.snapshot.Revision {
		out.Reset = true
		out.Snapshot = cloneDocument(d.snapshot)
		return out
	}
	next := after
	for _, op := range d.journal {
		if op.revision > after {
			if op.change.BaseRevision != next {
				out.Reset = true
				out.Changes = nil
				out.Snapshot = cloneDocument(d.snapshot)
				return out
			}
			out.Changes = append(out.Changes, cloneChange(op.change))
			next = op.revision
		}
	}
	if next != d.snapshot.Revision {
		out.Reset = true
		out.Changes = nil
	}
	if out.Reset {
		out.Snapshot = cloneDocument(out.Snapshot)
	}
	return out
}

type DocumentChangedMsg struct{ ID string }

func (s *DocumentService) SetEventBus(bus *EventBus) { s.mu.Lock(); s.bus = bus; s.mu.Unlock() }
func (s *DocumentService) changed(id string)         { s.notifyDocument(id); s.watch.changed(id) }

func (s *DocumentService) Format(ctx context.Context, owner document.Owner, id, requestID string, revision uint64) (document.Snapshot, error) {
	s.mu.Lock()
	d, err := s.owned(owner, id)
	if err != nil {
		s.mu.Unlock()
		return document.Snapshot{}, err
	}
	snap := cloneDocument(d.snapshot)
	s.mu.Unlock()
	if snap.Revision != revision {
		return snap, document.ErrRevision
	}
	text, err := FormatDocument(ctx, snap.Path, snap.Text)
	if err != nil {
		return snap, err
	}
	if err = ctx.Err(); err != nil {
		return snap, err
	}
	if bytes.Equal(text, snap.Text) {
		return snap, nil
	}
	_, err = s.Apply(document.Change{Owner: owner, DocumentID: id, RequestID: requestID, BaseRevision: revision, GroupID: requestID, Edits: []document.Edit{{Start: 0, End: len(snap.Text), Text: string(text)}}})
	if err != nil {
		return snap, err
	}
	return s.Snapshot(id)
}
