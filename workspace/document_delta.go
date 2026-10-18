package workspace

import (
	"bytes"
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"slices"

	"crude/document"
)

// The checkpoint owns structural state. Ordinary commands append only newly
// accepted operations and the affected author's history, with an exact receipt
// in the same revision-fenced transaction. Loading applies data, never commands.
type durableDocumentDelta struct {
	Before, Revision uint64
	Operations       []durableDocumentOperation
	Author           string
	Undo, Redo       durableHistoryDelta
}

type durableHistoryDelta struct {
	Pop  int
	Push []documentUndoUnit
}

func historyDelta(before, after []documentUndoUnit) durableHistoryDelta {
	i := 0
	for i < len(before) && i < len(after) && slices.Equal(before[i], after[i]) {
		i++
	}
	return durableHistoryDelta{Pop: len(before) - i, Push: after[i:]}
}

func applyHistoryDelta(before []documentUndoUnit, delta durableHistoryDelta, journalSize int) ([]documentUndoUnit, error) {
	if delta.Pop < 0 || delta.Pop > len(before) {
		return nil, errors.New("invalid document history delta")
	}
	for _, unit := range delta.Push {
		for _, index := range unit {
			if index < 0 || index >= journalSize {
				return nil, errors.New("invalid document delta history")
			}
		}
	}
	return append(before[:len(before)-delta.Pop], delta.Push...), nil
}

func (s *DocumentService) createDocumentDeltaTables() error {
	for _, query := range []string{
		`CREATE TABLE IF NOT EXISTS workspace_document_heads (id TEXT PRIMARY KEY, revision INTEGER NOT NULL)`,
		`CREATE TABLE IF NOT EXISTS workspace_document_deltas (sequence INTEGER PRIMARY KEY, id TEXT NOT NULL, payload BLOB NOT NULL)`,
		`CREATE INDEX IF NOT EXISTS workspace_document_deltas_id ON workspace_document_deltas(id,sequence)`,
		`CREATE TABLE IF NOT EXISTS workspace_document_receipts (request_key TEXT PRIMARY KEY, payload BLOB NOT NULL)`,
	} {
		if _, err := s.stateDB.Exec(query); err != nil {
			return err
		}
	}
	return nil
}

func stageDocumentCommand(d *liveDocument, owner document.Owner) *liveDocument {
	if len(d.journal) >= documentHistoryLimit || d.historyBytes >= documentHistoryBytes/2 {
		return stageDocument(d)
	}
	c := *d
	c.journal = append([]documentOperation(nil), d.journal...)
	c.undo = make(map[document.Owner][]documentUndoUnit, len(d.undo))
	c.redo = make(map[document.Owner][]documentUndoUnit, len(d.redo))
	for _, pair := range []struct {
		src, dst map[document.Owner][]documentUndoUnit
	}{{d.undo, c.undo}, {d.redo, c.redo}} {
		for key, stack := range pair.src {
			if key == author(owner) {
				stack = append([]documentUndoUnit(nil), stack...)
				if len(stack) > 0 {
					stack[len(stack)-1] = append(documentUndoUnit(nil), stack[len(stack)-1]...)
				}
			}
			pair.dst[key] = stack
		}
	}
	return &c
}

func documentEncode(value any) ([]byte, error) {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(value)
	return buf.Bytes(), err
}

func findDocumentRevision(docs []*liveDocument, id string) uint64 {
	for _, d := range docs {
		if d.snapshot.ID == id {
			return d.snapshot.Revision
		}
	}
	return 0
}

func (s *DocumentService) lookupDocumentRequest(key string) (documentRequest, bool, error) {
	for _, d := range s.docs {
		if r, ok := d.requests[key]; ok {
			return r, true, nil
		}
	}
	if s.stateErr != nil {
		return documentRequest{}, false, s.stateErr
	}
	if s.stateDB == nil {
		return documentRequest{}, false, nil
	}
	var data []byte
	err := s.stateDB.QueryRow(`SELECT payload FROM workspace_document_receipts WHERE request_key=?`, key).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return documentRequest{}, false, nil
	}
	if err != nil {
		return documentRequest{}, false, err
	}
	var r durableDocumentRequest
	if err = gob.NewDecoder(bytes.NewReader(data)).Decode(&r); err != nil {
		return documentRequest{}, false, err
	}
	return documentRequest{hash: r.Hash, receipt: r.Receipt, err: documentReceiptError(r.Receipt.Error)}, true, nil
}

func (s *DocumentService) cacheDocumentRequest(d *liveDocument, key string, r documentRequest) {
	// Durable SQL is authoritative; cache capacity cannot prevent further typing.
	if s.stateDB != nil && len(d.requests) >= 256 {
		for old := range d.requests {
			delete(d.requests, old)
			break
		}
	}
	d.requests[key] = r
}

func (s *DocumentService) persistDocumentCommand(before, after *liveDocument, owner document.Owner, key string, r documentRequest) error {
	if s.stateErr != nil {
		return s.stateErr
	}
	if s.stateDB == nil {
		return nil
	}
	rolled := len(after.journal) < len(before.journal) || (len(before.journal) > 0 && len(after.journal) > 0 && after.journal[0].revision != before.journal[0].revision)
	checkpoint := rolled || s.stateSizes[after.snapshot.ID] == 0
	var data []byte
	var err error
	if checkpoint {
		data, err = encodeDocument(after)
	} else if after.snapshot.Revision != before.snapshot.Revision {
		delta := durableDocumentDelta{Before: before.snapshot.Revision, Revision: after.snapshot.Revision, Author: owner.ClientID, Undo: historyDelta(before.undo[owner], after.undo[owner]), Redo: historyDelta(before.redo[owner], after.redo[owner])}
		for _, op := range after.journal[len(before.journal):] {
			delta.Operations = append(delta.Operations, durableDocumentOperation{op.revision, op.inverseOf, op.edits, op.inverse, op.owner, op.group, op.change})
		}
		data, err = documentEncode(delta)
	}
	if err != nil {
		return err
	}
	if !checkpoint {
		total := len(data)
		for _, size := range s.stateSizes {
			total += size
		}
		if total > documentTotalBytes {
			return document.ErrLimit
		}
	}
	receipt, err := documentEncode(durableDocumentRequest{r.hash, r.receipt})
	if err != nil {
		return err
	}
	tx, err := s.stateDB.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	id := after.snapshot.ID
	if checkpoint {
		if _, err = tx.Exec(`INSERT INTO workspace_document_heads(id,revision) VALUES(?,?) ON CONFLICT(id) DO NOTHING`, id, before.snapshot.Revision); err != nil {
			return err
		}
	}
	result, err := tx.Exec(`UPDATE workspace_document_heads SET revision=? WHERE id=? AND revision=?`, after.snapshot.Revision, id, before.snapshot.Revision)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return document.ErrRevision
	}
	if checkpoint {
		total := len(data)
		for other, size := range s.stateSizes {
			if other != id {
				total += size
			}
		}
		if total > documentTotalBytes {
			return document.ErrLimit
		}
		if _, err = tx.Exec(`INSERT INTO workspace_documents(id,checkpoint) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET checkpoint=excluded.checkpoint`, id, data); err != nil {
			return err
		}
		if _, err = tx.Exec(`DELETE FROM workspace_document_deltas WHERE id=?`, id); err != nil {
			return err
		}
	}
	if !checkpoint && len(data) > 0 {
		if _, err = tx.Exec(`INSERT INTO workspace_document_deltas(id,payload) VALUES(?,?)`, id, data); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(`INSERT INTO workspace_document_receipts(request_key,payload) VALUES(?,?)`, key, receipt); err != nil {
		return err
	}
	if err = tx.Commit(); err != nil {
		return err
	}
	if checkpoint {
		s.stateSizes[id] = len(data)
	} else {
		s.stateSizes[id] += len(data)
	}
	return nil
}

func (s *DocumentService) loadDocumentDeltas() error {
	total := 0
	for _, size := range s.stateSizes {
		total += size
	}
	rows, err := s.stateDB.Query(`SELECT id,payload FROM workspace_document_deltas ORDER BY sequence`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id string
		var data []byte
		if err = rows.Scan(&id, &data); err != nil {
			return err
		}
		total += len(data)
		if total > documentTotalBytes {
			return document.ErrLimit
		}
		s.stateSizes[id] += len(data)
		var delta durableDocumentDelta
		if err = gob.NewDecoder(bytes.NewReader(data)).Decode(&delta); err != nil {
			return err
		}
		d := s.docs[id]
		if d == nil || delta.Before != d.snapshot.Revision || delta.Revision <= delta.Before || len(delta.Operations) == 0 {
			return fmt.Errorf("invalid document delta: %s", id)
		}
		for _, op := range delta.Operations {
			if op.Revision != d.snapshot.Revision+1 {
				return document.ErrRevision
			}
			text, inverse, applyErr := document.Apply(d.snapshot.Text, op.Edits)
			if applyErr != nil {
				return applyErr
			}
			// Inverse payload is derived from the canonical base, not trusted replay.
			d.snapshot.Text = text
			d.snapshot.Revision = op.Revision
			d.journal = append(d.journal, documentOperation{op.Revision, op.InverseOf, op.Edits, inverse, op.Owner, op.Group, op.Change})
			for _, e := range op.Edits {
				d.historyBytes += len(e.Text)
			}
			for _, e := range inverse {
				d.historyBytes += len(e.Text)
			}
		}
		if d.snapshot.Revision != delta.Revision || len(d.journal) > documentHistoryLimit || d.historyBytes > documentHistoryBytes {
			return document.ErrLimit
		}
		owner := document.Owner{ClientID: delta.Author}
		d.undo[owner], err = applyHistoryDelta(d.undo[owner], delta.Undo, len(d.journal))
		if err != nil {
			return err
		}
		d.redo[owner], err = applyHistoryDelta(d.redo[owner], delta.Redo, len(d.journal))
		if err != nil {
			return err
		}
		d.snapshot.Dirty = !bytes.Equal(d.snapshot.Text, d.snapshot.Saved)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	for id, d := range s.docs {
		if s.stateSizes[id] == 0 {
			// Tests/core infrastructure may attach storage to an ephemeral service;
			// those documents have not been durably admitted yet.
			continue
		}
		var revision uint64
		if err = s.stateDB.QueryRow(`SELECT revision FROM workspace_document_heads WHERE id=?`, id).Scan(&revision); err != nil {
			return err
		}
		if revision != d.snapshot.Revision {
			return document.ErrRevision
		}
	}
	return nil
}
