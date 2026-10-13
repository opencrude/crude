package workspace

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"crude/document"
	"crude/fileio"
	"crude/localhistory"
	"crude/worktree"
)

func (s *DocumentService) executionDocumentsLocked() map[string]worktree.Document {
	result := map[string]worktree.Document{}
	for _, d := range s.docs {
		p, err := filepath.Rel(s.root, d.snapshot.Path)
		if err != nil || p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
			continue
		}
		mode := uint32(0644)
		if info, err := os.Lstat(d.snapshot.Path); err == nil {
			mode = uint32(info.Mode().Perm())
		}
		result[filepath.ToSlash(p)] = worktree.Document{Revision: d.snapshot.Revision, Exists: true, Data: bytes.Clone(d.snapshot.Text), Mode: mode}
	}
	return result
}

// IntegrationStatus is passive durable evidence. Prepared means an interrupted
// effect, not a retryable request. Only explicit recovery may roll it back.
type IntegrationStatus struct {
	Version     int                `json:"version"`
	Plan        worktree.PlanDelta `json:"plan"`
	Receipt     worktree.Receipt   `json:"receipt"`
	State       string             `json:"state"`
	Disk        []worktree.Change  `json:"disk"`
	Directories []string           `json:"directories"`
	Documents   map[string][]byte  `json:"documents"`
}

func (s *DocumentService) integrationTable() error {
	if s.stateDB == nil {
		return errors.New("execution integration requires durable workspace document storage")
	}
	_, err := s.stateDB.Exec(`CREATE TABLE IF NOT EXISTS workspace_integrations (id TEXT PRIMARY KEY, payload BLOB NOT NULL)`)
	return err
}
func (s *DocumentService) loadIntegration(id string) (IntegrationStatus, error) {
	var j IntegrationStatus
	if err := s.integrationTable(); err != nil {
		return j, err
	}
	var data []byte
	if err := s.stateDB.QueryRow(`SELECT payload FROM workspace_integrations WHERE id=?`, id).Scan(&data); err != nil {
		return j, err
	}
	if err := json.Unmarshal(data, &j); err != nil {
		return j, err
	}
	if j.Version != 1 || j.Receipt.OperationID != id {
		return j, errors.New("invalid integration journal")
	}
	return j, nil
}
func putIntegration(tx *sql.Tx, j IntegrationStatus) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO workspace_integrations(id,payload) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET payload=excluded.payload`, j.Receipt.OperationID, data)
	return err
}
func (s *DocumentService) saveIntegration(j IntegrationStatus) error {
	tx, err := s.stateDB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if err = putIntegration(tx, j); err != nil {
		return err
	}
	return tx.Commit()
}
func integrationID(p worktree.PlanDelta) string { return "execution-" + p.ContextID + "-" + p.Digest }

// commitIntegration checkpoints every staged document and the operation receipt
// atomically. Disk intent was durable before any filesystem effect. Live text is
// installed only after this commit, so failed commits never publish partial text.
func (s *DocumentService) commitIntegration(j IntegrationStatus, docs []*liveDocument, additional ...IntegrationStatus) error {
	payloads := map[string][]byte{}
	total := 0
	sizes := map[string]int{}
	for id, size := range s.stateSizes {
		sizes[id] = size
	}
	for _, d := range docs {
		b, err := encodeDocument(d)
		if err != nil {
			return err
		}
		payloads[d.snapshot.ID] = b
		sizes[d.snapshot.ID] = len(b)
	}
	for _, n := range sizes {
		total += n
	}
	if total > documentTotalBytes {
		return document.ErrLimit
	}
	tx, err := s.stateDB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, d := range docs {
		id := d.snapshot.ID
		if _, err = tx.Exec(`INSERT INTO workspace_documents(id,checkpoint) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET checkpoint=excluded.checkpoint`, id, payloads[id]); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO workspace_document_heads(id,revision) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision`, id, d.snapshot.Revision); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM workspace_document_deltas WHERE id=?`, id); err != nil {
			return err
		}
	}
	if err = putIntegration(tx, j); err != nil {
		return err
	}
	for _, other := range additional {
		if err = putIntegration(tx, other); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.stateSizes = sizes
	return nil
}

// integrationPath rejects symlinks in every component and captures an existing
// ancestor identity. This bounds same-process races; it is not hostile same-user
// universal atomicity against rename races between syscalls.
func integrationPath(root, rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) || filepath.ToSlash(filepath.Clean(rel)) != rel || strings.Contains(rel, "\\") {
		return "", errors.New("unsafe integration path")
	}
	p := root
	for _, part := range strings.Split(rel, "/") {
		if part == ".." || strings.EqualFold(part, ".git") {
			return "", errors.New("unsafe integration path")
		}
		p = filepath.Join(p, part)
		fi, err := os.Lstat(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return "", err
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("integration symlink conflict")
		}
	}
	return p, nil
}
func sameExecutionFile(a, b *worktree.File) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Type == b.Type && a.Mode == b.Mode && bytes.Equal(a.Data, b.Data)
}
func writeExecutionFile(root string, service *worktree.Service, ch worktree.Change) error {
	path, err := integrationPath(root, ch.Path)
	if err != nil {
		return err
	}
	parent := filepath.Dir(path)
	anchor, err := os.Stat(parent)
	if err != nil {
		return err
	}
	check := func() error {
		if _, err := integrationPath(root, ch.Path); err != nil {
			return err
		}
		now, err := os.Stat(parent)
		if err != nil || !os.SameFile(anchor, now) {
			return errors.New("integration parent identity changed")
		}
		f, err := service.DestinationFile(ch.Path)
		if err != nil {
			return err
		}
		if !sameExecutionFile(f, ch.Before) {
			return fmt.Errorf("disk changed: %s", ch.Path)
		}
		return nil
	}
	if ch.After == nil {
		if err = check(); err != nil {
			return err
		}
		return os.Remove(path)
	}
	// Stage the exact mode instead of WriteAtomicChecked's intentional existing
	// mode preservation. Publication still uses the shared atomic owner.
	tmp, err := os.CreateTemp(parent, ".crude-integration-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = tmp.Close(); _ = os.Remove(name) }()
	if _, err = tmp.Write(ch.After.Data); err != nil {
		return err
	}
	if err = tmp.Chmod(os.FileMode(ch.After.Mode)); err != nil {
		return err
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	if err = check(); err != nil {
		return err
	}
	return fileio.CommitAtomicFile(name, path)
}
func (s *DocumentService) rollbackIntegration(service *worktree.Service, j IntegrationStatus) error {
	// Validate the complete recovery set first. An external third state is never
	// overwritten; journal/preimages remain for explicit comparison.
	for _, ch := range j.Disk {
		f, err := service.DestinationFile(ch.Path)
		if err != nil {
			return err
		}
		if !sameExecutionFile(f, ch.Before) && !sameExecutionFile(f, ch.After) {
			return fmt.Errorf("recovery comparison required: %s", ch.Path)
		}
	}
	for i := len(j.Disk) - 1; i >= 0; i-- {
		ch := j.Disk[i]
		f, err := service.DestinationFile(ch.Path)
		if err != nil {
			return err
		}
		if sameExecutionFile(f, ch.Before) {
			continue
		}
		if err = writeExecutionFile(s.root, service, worktree.Change{Path: ch.Path, Before: f, After: ch.Before}); err != nil {
			return err
		}
	}
	for i := len(j.Directories) - 1; i >= 0; i-- {
		p, err := integrationPath(s.root, j.Directories[i])
		if err != nil {
			return err
		}
		if err = os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	j.State = "rolled_back"
	return s.saveIntegration(j)
}
func (s *DocumentService) applyExecution(ctx context.Context, service *worktree.Service, history *LocalHistoryService, plan worktree.PlanDelta) (worktree.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.stateErr != nil {
		return worktree.Receipt{}, errors.New("document authority unavailable")
	}
	if err := s.integrationTable(); err != nil {
		return worktree.Receipt{}, err
	}
	id := integrationID(plan)
	if existing, err := s.loadIntegration(id); err == nil {
		original, _ := json.Marshal(existing.Plan)
		provided, _ := json.Marshal(plan)
		if !bytes.Equal(original, provided) {
			return worktree.Receipt{}, errors.New("integration request identity mismatch")
		}
		if existing.State == "committed" {
			return existing.Receipt, nil
		}
		return worktree.Receipt{}, fmt.Errorf("integration %s needs explicit recovery (%s)", id, existing.State)
	} else if !errors.Is(err, sql.ErrNoRows) {
		return worktree.Receipt{}, err
	}
	var pending int
	if err := s.stateDB.QueryRow(`SELECT count(*) FROM workspace_integrations WHERE json_extract(payload,'$.state')='prepared'`).Scan(&pending); err != nil {
		return worktree.Receipt{}, err
	}
	if pending != 0 {
		return worktree.Receipt{}, errors.New("unfinished integration requires explicit recovery before another apply")
	}
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return worktree.Receipt{}, err
	}
	defer unlock()
	c, err := service.Inspect(plan.ContextID)
	if err != nil {
		return worktree.Receipt{}, err
	}
	if c.Mode != worktree.Isolated {
		return worktree.Receipt{}, errors.New("shared execution has no separate integration destination")
	}
	if err = service.ValidateDestination(ctx, plan, s.executionDocumentsLocked()); err != nil {
		return worktree.Receipt{}, err
	}
	if len(plan.Changes) == 0 {
		return worktree.Receipt{}, errors.New("execution has no changes")
	}
	j := IntegrationStatus{Version: 1, Plan: plan, State: "prepared", Receipt: worktree.Receipt{PlanDigest: plan.Digest, OperationID: id}, Documents: map[string][]byte{}}
	owner := document.Owner{ClientID: id, Epoch: 1}
	beforeDocs, afterDocs := []*liveDocument{}, []*liveDocument{}
	paths := []localhistory.OperationPath{}
	include := []string{}
	parents := map[string]bool{}
	state := func(f *worktree.File) *localhistory.OperationState {
		if f == nil {
			return nil
		}
		return &localhistory.OperationState{Type: localhistory.EntryFile, Mode: f.Mode, Data: bytes.Clone(f.Data)}
	}
	for _, ch := range plan.Changes {
		path, err := integrationPath(s.root, ch.Path)
		if err != nil {
			return worktree.Receipt{}, err
		}
		include = append(include, ch.Path)
		if d := s.documentAtPath(path); d != nil {
			if d.snapshot.Conflict || d.diskIntent != nil || d.relocation != nil {
				return worktree.Receipt{}, fmt.Errorf("document needs reconciliation: %s", ch.Path)
			}
			actual, e := service.DestinationFile(ch.Path)
			if e != nil {
				return worktree.Receipt{}, e
			}
			if (actual != nil) != d.snapshot.DiskExists || (actual != nil && sha256.Sum256(actual.Data) != d.snapshot.DiskHash) {
				return worktree.Receipt{}, fmt.Errorf("accepted document disk changed: %s", ch.Path)
			}
			if ch.After == nil || !utf8.Valid(ch.After.Data) || ch.Before == nil || ch.Before.Mode != ch.After.Mode {
				return worktree.Receipt{}, fmt.Errorf("open document deletion/mode/nontext needs comparison: %s", ch.Path)
			}
			edits, err := worktree.TextEdits(d.snapshot.Text, ch.After.Data)
			if err != nil {
				return worktree.Receipt{}, err
			}
			trial := stageDocument(d)
			index, err := s.commit(trial, document.Change{DocumentID: d.snapshot.ID, RequestID: id, Owner: owner, BaseRevision: d.snapshot.Revision, GroupID: id, Edits: edits})
			if err != nil {
				return worktree.Receipt{}, err
			}
			trial.undo[author(owner)] = append(trial.undo[author(owner)], documentUndoUnit{index})
			delete(trial.redo, author(owner))
			b, err := encodeDocument(d)
			if err != nil {
				return worktree.Receipt{}, err
			}
			j.Documents[d.snapshot.ID] = b
			beforeDocs = append(beforeDocs, d)
			afterDocs = append(afterDocs, trial)
			// Live text operation WAL is retained separately from the actual disk safety snapshot.
			paths = append(paths, localhistory.OperationPath{Path: ch.Path, Before: state(ch.Before), After: state(ch.After)})
		} else {
			actual, err := service.DestinationFile(ch.Path)
			if err != nil {
				return worktree.Receipt{}, err
			}
			if !sameExecutionFile(actual, ch.Before) {
				return worktree.Receipt{}, fmt.Errorf("disk baseline mismatch: %s", ch.Path)
			}
			j.Disk = append(j.Disk, worktree.Change{Path: ch.Path, Before: actual, After: ch.After})
			paths = append(paths, localhistory.OperationPath{Path: ch.Path, Before: state(actual), After: state(ch.After)})
			if ch.After != nil {
				for p := filepath.Dir(ch.Path); p != "."; p = filepath.Dir(p) {
					abs, err := integrationPath(s.root, filepath.ToSlash(p))
					if err != nil {
						return worktree.Receipt{}, err
					}
					info, err := os.Lstat(abs)
					if errors.Is(err, os.ErrNotExist) {
						parents[filepath.ToSlash(p)] = true
					} else if err != nil || !info.IsDir() {
						return worktree.Receipt{}, fmt.Errorf("invalid parent: %s", p)
					}
				}
			}
		}
	}
	for p := range parents {
		j.Directories = append(j.Directories, p)
	}
	sort.Slice(j.Directories, func(i, k int) bool { return len(j.Directories[i]) < len(j.Directories[k]) })
	for _, p := range j.Directories {
		paths = append(paths, localhistory.OperationPath{Path: p, After: &localhistory.OperationState{Type: localhistory.EntryDirectory, Mode: 0755}})
	}
	safety, err := history.store.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "safety", Label: "Before integration " + plan.ContextID, DisableGitIgnore: true, IncludePaths: include})
	if err != nil {
		return worktree.Receipt{}, err
	}
	j.Receipt.SafetySnapshotID = safety.ID
	sessionID := "execution/" + plan.Owner.AgentID
	if err = history.store.BeginMessageScope(sessionID, id, safety.ID, time.Now()); err != nil {
		return worktree.Receipt{}, err
	}
	if err = history.store.RecordAgentOperation(ctx, localhistory.AgentOperation{ID: id, SessionID: sessionID, MessageID: id, AgentID: plan.Owner.AgentID, Source: "execution_apply", Paths: paths}); err != nil {
		return worktree.Receipt{}, err
	}
	if err = service.ValidateDestination(ctx, plan, s.executionDocumentsLocked()); err != nil {
		_ = history.store.CompleteAgentOperation(id, false)
		return worktree.Receipt{}, err
	}
	for _, d := range beforeDocs {
		rel, e := filepath.Rel(s.root, d.snapshot.Path)
		if e != nil {
			return worktree.Receipt{}, e
		}
		actual, e := service.DestinationFile(filepath.ToSlash(rel))
		if e != nil {
			return worktree.Receipt{}, e
		}
		if (actual != nil) != d.snapshot.DiskExists || (actual != nil && sha256.Sum256(actual.Data) != d.snapshot.DiskHash) {
			_ = history.store.CompleteAgentOperation(id, false)
			return worktree.Receipt{}, fmt.Errorf("accepted document disk changed during backup: %s", rel)
		}
	}
	if err = s.saveIntegration(j); err != nil {
		_ = history.store.CompleteAgentOperation(id, false)
		return worktree.Receipt{}, err
	}
	rollback := func(cause error) (worktree.Receipt, error) {
		rerr := s.rollbackIntegration(service, j)
		if rerr == nil {
			rerr = history.store.CompleteAgentOperation(id, false)
		}
		return worktree.Receipt{}, errors.Join(cause, rerr)
	}
	for _, p := range j.Directories {
		abs, e := integrationPath(s.root, p)
		if e != nil {
			return rollback(e)
		}
		if e = os.Mkdir(abs, 0755); e != nil {
			return rollback(e)
		}
	}
	for _, ch := range j.Disk {
		if err = ctx.Err(); err != nil {
			return rollback(err)
		}
		if err = writeExecutionFile(s.root, service, ch); err != nil {
			return rollback(err)
		}
	}
	j.State = "committed"
	if err = s.commitIntegration(j, afterDocs); err != nil {
		return rollback(err)
	}
	for i, d := range afterDocs {
		*beforeDocs[i] = *d
		if s.lsp != nil && d.lspOpen {
			s.lsp.UpdateFile(context.Background(), d.snapshot.Path, string(d.snapshot.Text))
		}
		s.changed(d.snapshot.ID)
	}
	// Everything below is metadata publication, never grounds to roll back an
	// already accepted group. The durable receipt recovers lost replies exactly.
	err = history.store.CompleteAgentOperation(id, true)
	err = errors.Join(err, service.RecordIntegration(ctx, plan, j.Receipt, s.executionDocumentsLocked()))
	err = errors.Join(err, history.store.CloseMessageScope(sessionID, id, safety.ID, include, nil, time.Now()))
	return j.Receipt, err
}

// recoverExecution never replays Apply. Prepared intents roll back only when all
// current disk states match an exact recorded pre/post image. Committed outcomes
// remain receipts, even when the original response was lost.
func (s *DocumentService) recoverExecution(ctx context.Context, service *worktree.Service, id string) (worktree.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.loadIntegration(id)
	if err != nil {
		return worktree.Receipt{}, err
	}
	if j.State == "committed" {
		return j.Receipt, nil
	}
	if j.State == "rolled_back" {
		return j.Receipt, nil
	}
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return worktree.Receipt{}, err
	}
	defer unlock()
	return j.Receipt, s.rollbackIntegration(service, j)
}

// undoExecution derives exact inverse deltas against current accepted bytes.
// Nonoverlapping later text is preserved; overlap/binary changes fail closed.
func (s *DocumentService) undoExecution(ctx context.Context, service *worktree.Service, history *LocalHistoryService, id string) (worktree.Receipt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	j, err := s.loadIntegration(id)
	if err != nil {
		return worktree.Receipt{}, err
	}
	if j.State == "undone" {
		undo, err := s.loadIntegration(id + "-undo")
		return undo.Receipt, err
	}
	if j.State != "committed" {
		return worktree.Receipt{}, errors.New("only committed integration can be undone")
	}
	if pending, e := s.loadIntegration(id + "-undo"); e == nil {
		return worktree.Receipt{}, fmt.Errorf("undo requires explicit recovery (%s)", pending.State)
	} else if !errors.Is(e, sql.ErrNoRows) {
		return worktree.Receipt{}, e
	}
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return worktree.Receipt{}, err
	}
	defer unlock()
	staged := []*liveDocument{}
	live := []*liveDocument{}
	inverse := IntegrationStatus{Version: 1, State: "prepared", Plan: j.Plan, Receipt: worktree.Receipt{PlanDigest: j.Plan.Digest, OperationID: id + "-undo"}, Documents: map[string][]byte{}}
	paths := []localhistory.OperationPath{}
	toState := func(f *worktree.File) *localhistory.OperationState {
		if f == nil {
			return nil
		}
		return &localhistory.OperationState{Type: localhistory.EntryFile, Mode: f.Mode, Data: bytes.Clone(f.Data)}
	}
	for _, ch := range j.Plan.Changes {
		path, err := integrationPath(s.root, ch.Path)
		if err != nil {
			return worktree.Receipt{}, err
		}
		current, err := service.DestinationFile(ch.Path)
		if err != nil {
			return worktree.Receipt{}, err
		}
		d := s.documentAtPath(path)
		if d != nil {
			if _, wasLive := j.Documents[d.snapshot.ID]; !wasLive {
				return worktree.Receipt{}, fmt.Errorf("file opened since integration; explicit disk/document undo comparison required: %s", ch.Path)
			}
			if ch.After == nil || d.snapshot.Conflict || d.diskIntent != nil || d.relocation != nil {
				return worktree.Receipt{}, fmt.Errorf("undo document comparison required: %s", ch.Path)
			}
			current = &worktree.File{Type: "file", Mode: ch.After.Mode, Data: d.snapshot.Text}
		}
		target := ch.Before
		if !sameExecutionFile(current, ch.After) {
			if current == nil || ch.After == nil || ch.Before == nil || current.Mode != ch.After.Mode || ch.Before.Mode != ch.After.Mode {
				return worktree.Receipt{}, fmt.Errorf("undo comparison required: %s", ch.Path)
			}
			wanted, e := worktree.TextEdits(ch.After.Data, ch.Before.Data)
			if e != nil {
				return worktree.Receipt{}, e
			}
			later, e := worktree.TextEdits(ch.After.Data, current.Data)
			if e != nil {
				return worktree.Receipt{}, e
			}
			rebased, e := document.Rebase(wanted, later)
			if e != nil {
				return worktree.Receipt{}, e
			}
			text, _, e := document.Apply(current.Data, rebased)
			if e != nil {
				return worktree.Receipt{}, e
			}
			f := *ch.Before
			f.Data = text
			f.Hash = fmt.Sprintf("%x", sha256.Sum256(text))
			target = &f
		}
		if d != nil {
			if target == nil {
				return worktree.Receipt{}, errors.New("open document deletion cannot be undone here")
			}
			trial := stageDocument(d)
			edits, e := worktree.TextEdits(current.Data, target.Data)
			if e != nil {
				return worktree.Receipt{}, e
			}
			_, e = s.commit(trial, document.Change{DocumentID: d.snapshot.ID, Owner: document.Owner{ClientID: id, Epoch: 1}, RequestID: id + "-undo", BaseRevision: d.snapshot.Revision, Edits: edits})
			if e != nil {
				return worktree.Receipt{}, e
			}
			delete(trial.undo, document.Owner{ClientID: id})
			live = append(live, d)
			staged = append(staged, trial)
		} else {
			inverse.Disk = append(inverse.Disk, worktree.Change{Path: ch.Path, Before: current, After: target})
		}
		paths = append(paths, localhistory.OperationPath{Path: ch.Path, Before: toState(current), After: toState(target)})
	}
	safety, err := history.store.Snapshot(ctx, localhistory.SnapshotOptions{Kind: "safety", Label: "Before integration undo", DisableGitIgnore: true})
	if err != nil {
		return worktree.Receipt{}, err
	}
	inverse.Receipt.SafetySnapshotID = safety.ID
	sessionID := "execution/" + j.Plan.Owner.AgentID
	if err = history.store.BeginMessageScope(sessionID, inverse.Receipt.OperationID, safety.ID, time.Now()); err != nil {
		return worktree.Receipt{}, err
	}
	if err = history.store.RecordAgentOperation(ctx, localhistory.AgentOperation{ID: inverse.Receipt.OperationID, SessionID: sessionID, MessageID: inverse.Receipt.OperationID, AgentID: j.Plan.Owner.AgentID, Source: "execution_undo", Paths: paths}); err != nil {
		return worktree.Receipt{}, err
	}
	if err = s.saveIntegration(inverse); err != nil {
		return worktree.Receipt{}, err
	}
	for _, ch := range inverse.Disk {
		if err = writeExecutionFile(s.root, service, ch); err != nil {
			return worktree.Receipt{}, errors.Join(err, s.rollbackIntegration(service, inverse))
		}
	}
	// Original outcome and all text reversals are one authoritative commit.
	j.State = "undone"
	inverse.State = "committed"
	if err = s.commitIntegration(j, staged, inverse); err != nil {
		return worktree.Receipt{}, errors.Join(err, s.rollbackIntegration(service, inverse))
	}
	for i, d := range staged {
		*live[i] = *d
		if s.lsp != nil && d.lspOpen {
			s.lsp.UpdateFile(context.Background(), d.snapshot.Path, string(d.snapshot.Text))
		}
		s.changed(d.snapshot.ID)
	}
	err = history.store.CompleteAgentOperation(inverse.Receipt.OperationID, true)
	err = errors.Join(err, history.store.CloseMessageScope(sessionID, inverse.Receipt.OperationID, safety.ID, nil, nil, time.Now()))
	return inverse.Receipt, err
}
