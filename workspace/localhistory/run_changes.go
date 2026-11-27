package localhistory

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	crudediff "crude/text/unidiff"
)

type RunChangeQuery struct {
	RunID       string `json:"run_id"`
	After       int64  `json:"after,omitempty"`
	OperationID string `json:"operation_id,omitempty"`
	Path        string `json:"path,omitempty"`
}

type RunChangePage struct {
	RunID    string      `json:"run_id"`
	Changes  []RunChange `json:"changes"`
	After    int64       `json:"after,omitempty"`
	Coverage string      `json:"coverage"`
}

type RunChange struct {
	OperationID string `json:"operation_id"`
	Path        string `json:"path"`
	Source      string `json:"source"`
	State       string `json:"state"`
	CreatedAt   int64  `json:"created_unix_ns"`
	Diff        string `json:"diff,omitempty"`
	DiffNote    string `json:"diff_note,omitempty"`
}

// InspectRunChanges observes receipts, not current working files. The stable
// row cursor is local to the requested run and the authorised history store.
func (s *Store) InspectRunChanges(ctx context.Context, agentID string, q RunChangeQuery) (RunChangePage, error) {
	out := RunChangePage{RunID: q.RunID, Changes: []RunChange{}, Coverage: "Only retained direct file operation receipts for this run in this checkout. Shell and MCP writes may not be recorded."}
	if q.RunID == "" || agentID == "" || q.After < 0 || (q.OperationID == "") != (q.Path == "") || (q.OperationID != "" && q.After != 0) {
		return out, errors.New("select a run. For a diff, give its exact operation ID and path instead of a cursor")
	}
	const joins = ` FROM history_agent_operation_runs r JOIN history_agent_operations o USING(operation_id)
 JOIN history_agent_operation_paths p USING(operation_id) WHERE r.run_id=? AND o.agent_id=?`
	return s.receiptPage(ctx, joins, q.RunID, agentID, q, out)
}

// AgentReceiptQuery selects retained changes by author, independent of the last turn or undo eligibility.
type AgentReceiptQuery struct {
	SessionID, AgentID string
	After              int64
	OperationID, Path  string
}

// AgentReceipts reads committed before/after evidence, not an inverse of the current working tree.
func (s *Store) AgentReceipts(ctx context.Context, q AgentReceiptQuery) (RunChangePage, error) {
	out := RunChangePage{Coverage: "Direct file edits in this checkout. Shell commands and external tools are not attributed."}
	if q.SessionID == "" || q.AgentID == "" || q.After < 0 || (q.OperationID == "") != (q.Path == "") || (q.After != 0 && q.Path != "") {
		return out, errors.New("select an agent and a session")
	}
	const joins = ` FROM history_agent_operations o JOIN history_agent_operation_paths p USING(operation_id) WHERE o.session_id=? AND o.agent_id=? AND o.status='committed'`
	return s.receiptPage(ctx, joins, q.SessionID, q.AgentID, RunChangeQuery{After: q.After, OperationID: q.OperationID, Path: q.Path}, out)
}

func (s *Store) receiptPage(ctx context.Context, joins, scope, agentID string, q RunChangeQuery, out RunChangePage) (RunChangePage, error) {
	if err := s.beginOperation(); err != nil {
		return out, err
	}
	defer s.endOperation()
	if q.OperationID != "" {
		var c RunChange
		var before, after string
		err := s.db.QueryRowContext(ctx, `SELECT o.operation_id,p.path,o.source,o.status,o.created_at,p.before_entry,p.after_entry`+joins+` AND o.operation_id=? AND p.path=?`, scope, agentID, q.OperationID, q.Path).
			Scan(&c.OperationID, &c.Path, &c.Source, &c.State, &c.CreatedAt, &before, &after)
		if errors.Is(err, sql.ErrNoRows) {
			return out, errors.New("this file receipt is unavailable for the selected run")
		}
		if err != nil {
			return out, err
		}
		if c.State == "committed" {
			c.Diff, c.DiffNote = s.runChangeDiff(c.Path, before, after)
		} else {
			c.DiffNote = "This receipt does not confirm a file change."
		}
		out.Changes = append(out.Changes, c)
		return out, nil
	}
	const limit = 8
	rows, err := s.db.QueryContext(ctx, `SELECT p.rowid,o.operation_id,p.path,o.source,o.status,o.created_at`+joins+` AND p.rowid>? ORDER BY p.rowid LIMIT ?`, scope, agentID, q.After, limit+1)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var last int64
	for rows.Next() {
		if len(out.Changes) == limit {
			out.After = last
			break
		}
		var c RunChange
		if err := rows.Scan(&last, &c.OperationID, &c.Path, &c.Source, &c.State, &c.CreatedAt); err != nil {
			return out, err
		}
		out.Changes = append(out.Changes, c)
	}
	return out, rows.Err()
}

func (s *Store) runChangeDiff(path, before, after string) (string, string) {
	// The diff work and encoded response are bounded independently of the
	// retained file size; a binary or metadata-only change still has a receipt.
	const maxText = 48 << 10
	load := func(raw string) ([]byte, error) {
		if raw == "" {
			return nil, nil
		}
		var entry Entry
		if err := json.Unmarshal([]byte(raw), &entry); err != nil {
			return nil, err
		}
		if entry.Type != EntryFile || entry.Size > maxText {
			return nil, errors.New("no text preview for this file type or size")
		}
		data, err := s.readFileBytes(entry.Hash)
		if err != nil {
			return nil, errors.New("the retained file content is unavailable")
		}
		if len(data) > maxText || !utf8.Valid(data) || bytes.IndexByte(data, 0) >= 0 {
			return nil, errors.New("no text preview for binary or large files")
		}
		return data, nil
	}
	old, err := load(before)
	if err != nil {
		return "", err.Error()
	}
	new, err := load(after)
	if err != nil {
		return "", err.Error()
	}
	preview, _, _ := crudediff.GenerateDiff(string(old), string(new), path)
	const maxDiff = 8 << 10
	if len(preview) <= maxDiff {
		return preview, ""
	}
	end := maxDiff
	for !utf8.RuneStart(preview[end]) {
		end--
	}
	return preview[:end], fmt.Sprintf("%d diff bytes were omitted.", len(preview)-end)
}
