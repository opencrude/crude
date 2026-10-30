package workspace

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"path/filepath"

	"crude/document"
)

// Checkpoints are current-format only. Runtime connections and language views
// never survive a process boundary; author history is keyed by stable client ID.
const documentStateVersion = 2
const documentTotalBytes = 256 << 20

type durableDocumentOperation struct {
	Revision       uint64
	InverseOf      uint64
	Edits, Inverse []document.Edit
	Owner          document.Owner
	Group          string
	Change         document.Change
}
type durableDocumentRequest struct {
	Hash    [32]byte
	Receipt document.Receipt
}
type documentDiskIntent struct {
	Revision uint64
	Hash     [32]byte
	Exists   bool
}
type durableDocument struct {
	Version    int
	Snapshot   document.Snapshot
	Journal    []durableDocumentOperation
	Undo, Redo map[string][]documentUndoUnit
	DiskIntent *documentDiskIntent
	Relocation *documentRelocation
}

func author(owner document.Owner) document.Owner { return document.Owner{ClientID: owner.ClientID} }

func (s *DocumentService) ownerEpoch(id string) uint64 {
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	return s.epochs[id]
}
func (s *DocumentService) admitOwner(owner document.Owner) bool {
	s.ownerMu.Lock()
	defer s.ownerMu.Unlock()
	return owner.ClientID != "" && len(owner.ClientID) <= 256 && owner.Epoch != 0 && owner.Epoch >= s.epochs[owner.ClientID] && !s.detached[owner] && (s.epochs[owner.ClientID] != 0 || len(s.epochs) < 1024)
}

// stageDocument copies only mutable metadata for this document. Text, immutable
// operation payloads and saved bytes are shared until replaced, not deep-copied.
func stageDocument(d *liveDocument) *liveDocument {
	c := *d
	c.journal = append([]documentOperation(nil), d.journal...)
	c.undo = cloneHistory(d.undo)
	c.redo = cloneHistory(d.redo)
	c.requests = make(map[string]documentRequest, len(d.requests))
	for key, value := range d.requests {
		c.requests[key] = value
	}
	return &c
}
func cloneHistory(src map[document.Owner][]documentUndoUnit) map[document.Owner][]documentUndoUnit {
	out := make(map[document.Owner][]documentUndoUnit, len(src))
	for key, stack := range src {
		units := make([]documentUndoUnit, len(stack))
		for i, unit := range stack {
			units[i] = append(documentUndoUnit(nil), unit...)
		}
		out[key] = units
	}
	return out
}

func (s *DocumentService) attachState(db *sql.DB) error {
	if db == nil {
		return errors.New("document state storage is unavailable")
	}
	s.stateDB = db
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS workspace_documents (id TEXT PRIMARY KEY, checkpoint BLOB NOT NULL)`); err != nil {
		return err
	}
	if err := s.createDocumentDeltaTables(); err != nil {
		return err
	}
	rows, err := db.Query(`SELECT id, checkpoint FROM workspace_documents`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	total := 0
	for rows.Next() {
		var id string
		var data []byte
		if err = rows.Scan(&id, &data); err != nil {
			return err
		}
		total += len(data)
		if len(s.docs) >= documentCountLimit || total > documentTotalBytes {
			return document.ErrLimit
		}
		var saved durableDocument
		if err = gob.NewDecoder(bytes.NewReader(data)).Decode(&saved); err != nil {
			return err
		}
		if saved.Version != documentStateVersion || saved.Snapshot.ID != id || !filepath.IsAbs(id) || filepath.Clean(id) != id || !filepath.IsAbs(saved.Snapshot.Path) || filepath.Clean(saved.Snapshot.Path) != saved.Snapshot.Path || len(saved.Snapshot.Text) > document.MaxBytes || len(saved.Snapshot.Saved) > document.MaxBytes || len(saved.Journal) > documentHistoryLimit {
			return fmt.Errorf("invalid document checkpoint: %s", id)
		}
		if intent := saved.Relocation; intent != nil {
			if !filepath.IsAbs(intent.Source) || !filepath.IsAbs(intent.Target) || filepath.Clean(intent.Source) != intent.Source || filepath.Clean(intent.Target) != intent.Target || documentUnder(intent.Source, intent.Target) || !documentUnder(intent.Source, saved.Snapshot.Path) || len(intent.Preimages) == 0 || len(intent.Preimages) > documentCountLimit+1 {
				return fmt.Errorf("invalid document relocation checkpoint: %s", id)
			}
			if _, ok := intent.Preimages[intent.Source]; !ok {
				return fmt.Errorf("missing relocation source preimage: %s", id)
			}
			if _, ok := intent.Preimages[saved.Snapshot.Path]; !ok {
				return fmt.Errorf("missing relocation document preimage: %s", id)
			}
			for path := range intent.Preimages {
				if !documentUnder(intent.Source, path) || filepath.Clean(path) != path {
					return fmt.Errorf("invalid relocation preimage path: %s", path)
				}
			}
		}
		d := &liveDocument{snapshot: saved.Snapshot, owners: make(map[document.Owner]bool), undo: make(map[document.Owner][]documentUndoUnit), redo: make(map[document.Owner][]documentUndoUnit), requests: make(map[string]documentRequest), diskIntent: saved.DiskIntent, relocation: saved.Relocation}
		for _, op := range saved.Journal {
			d.journal = append(d.journal, documentOperation{revision: op.Revision, inverseOf: op.InverseOf, edits: op.Edits, inverse: op.Inverse, owner: op.Owner, group: op.Group, change: op.Change})
			for _, edit := range op.Edits {
				d.historyBytes += len(edit.Text)
			}
			for _, edit := range op.Inverse {
				d.historyBytes += len(edit.Text)
			}
		}
		if d.historyBytes > documentHistoryBytes {
			return document.ErrLimit
		}
		for key, units := range saved.Undo {
			d.undo[document.Owner{ClientID: key}] = units
		}
		for key, units := range saved.Redo {
			d.redo[document.Owner{ClientID: key}] = units
		}
		for _, history := range []map[document.Owner][]documentUndoUnit{d.undo, d.redo} {
			for _, stack := range history {
				for _, unit := range stack {
					for _, index := range unit {
						if index < 0 || index >= len(d.journal) {
							return errors.New("invalid document undo checkpoint")
						}
					}
				}
			}
		}
		if d.diskIntent != nil {
			d.snapshot.Conflict = true
		}
		if s.documentAtPath(d.snapshot.Path) != nil {
			return fmt.Errorf("duplicate document path: %s", d.snapshot.Path)
		}
		s.docs[id] = d
		s.stateSizes[id] = len(data)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	return s.loadDocumentDeltas()
}

func documentReceiptError(message string) error {
	if message == "" {
		return nil
	}
	for _, err := range []error{document.ErrRevision, document.ErrOwner, document.ErrRequest, document.ErrOverlap, document.ErrLimit} {
		if message == err.Error() {
			return err
		}
	}
	return errors.New(message)
}

func (s *DocumentService) persistDocument(d *liveDocument) error {
	return s.persistDocuments([]*liveDocument{d})
}

func encodeDocument(d *liveDocument) ([]byte, error) {
	state := durableDocument{Version: documentStateVersion, Snapshot: d.snapshot, Undo: make(map[string][]documentUndoUnit), Redo: make(map[string][]documentUndoUnit), DiskIntent: d.diskIntent, Relocation: d.relocation}
	state.Snapshot.AnalysisGeneration, state.Snapshot.AnalysisLifetime = "", ""
	for _, op := range d.journal {
		state.Journal = append(state.Journal, durableDocumentOperation{op.revision, op.inverseOf, op.edits, op.inverse, op.owner, op.group, op.change})
	}
	for key, stack := range d.undo {
		state.Undo[key.ClientID] = stack
	}
	for key, stack := range d.redo {
		state.Redo[key.ClientID] = stack
	}
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(state); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// Directory relocation publishes every checkpoint in one SQLite transaction.
func (s *DocumentService) persistDocuments(docs []*liveDocument) error {
	if s.stateErr != nil {
		return s.stateErr
	}
	if s.stateDB == nil {
		return nil
	}
	payloads := make(map[string][]byte, len(docs))
	sizes := make(map[string]int, len(s.stateSizes))
	for id, size := range s.stateSizes {
		sizes[id] = size
	}
	for _, d := range docs {
		data, err := encodeDocument(d)
		if err != nil {
			return err
		}
		payloads[d.snapshot.ID] = data
		sizes[d.snapshot.ID] = len(data)
	}
	total := 0
	for _, size := range sizes {
		total += size
	}
	if total > documentTotalBytes {
		return document.ErrLimit
	}
	tx, err := s.stateDB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for id, data := range payloads {
		if _, err = tx.Exec(`INSERT INTO workspace_documents(id, checkpoint) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET checkpoint=excluded.checkpoint`, id, data); err != nil {
			return err
		}
		if _, err = tx.Exec(`INSERT INTO workspace_document_heads(id, revision) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET revision=excluded.revision`, id, findDocumentRevision(docs, id)); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM workspace_document_deltas WHERE id=?`, id); err != nil {
			return err
		}
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	s.stateSizes = sizes
	return nil
}

// Rolling drops whole old undo units, never part of a group. Sync detects the
// missing revision prefix and returns Reset. Retired undo is intentionally lost,
// but canonical text and exact request receipts are never discarded.
func rollDocumentHistory(d *liveDocument) {
	if len(d.journal) < documentHistoryLimit && d.historyBytes < documentHistoryBytes/2 {
		return
	}
	cut := len(d.journal) / 2
	if cut == 0 && len(d.journal) > 0 {
		cut = 1
	}
	for cut < len(d.journal) && cut > 0 && d.journal[cut].group != "" && d.journal[cut].group == d.journal[cut-1].group && author(d.journal[cut].owner) == author(d.journal[cut-1].owner) {
		cut++
	}
	for _, history := range []map[document.Owner][]documentUndoUnit{d.undo, d.redo} {
		for key, stack := range history {
			retained := []documentUndoUnit{}
			for _, unit := range stack {
				keep := true
				for _, index := range unit {
					if index < cut {
						keep = false
					}
				}
				if keep {
					shifted := make(documentUndoUnit, len(unit))
					for i, index := range unit {
						shifted[i] = index - cut
					}
					retained = append(retained, shifted)
				}
			}
			history[key] = retained
		}
	}
	d.journal = append([]documentOperation(nil), d.journal[cut:]...)
	d.historyBytes = 0
	for _, op := range d.journal {
		for _, e := range op.edits {
			d.historyBytes += len(e.Text)
		}
		for _, e := range op.inverse {
			d.historyBytes += len(e.Text)
		}
	}
}
