package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Consolidation limits: conservative on purpose, because a background rewrite that
// goes wrong must not be able to gut the store.
const (
	maxConsolidationOps   = 12
	maxConsolidationInput = 12
)

// ConsolidationLockName is the cooperative lock for the background job.
const ConsolidationLockName = "consolidate"

// ConsolidationOp is one operation proposed by the consolidation model.
type ConsolidationOp struct {
	// Op is "merge" (combine duplicate/overlapping records into one),
	// "supersede" (a record contradicts a newer one), or "rewrite"
	// (same knowledge, tighter text).
	Op                string           `json:"op"`
	IDs               []string         `json:"ids,omitempty"` // merge sources
	ID                string           `json:"id,omitempty"`  // supersede/rewrite target
	By                string           `json:"by,omitempty"`  // supersede: the winning record
	Kind              string           `json:"kind,omitempty"`
	Title             string           `json:"title,omitempty"`
	Content           string           `json:"content,omitempty"`
	Importance        int              `json:"importance,omitempty"`
	Reason            string           `json:"reason,omitempty"`
	ExpectedRevisions map[string]int64 `json:"expected_revisions"`
}

func PrepareConsolidationInput(records []Record) []string {
	var payloads []string
	spent := 0
	for _, record := range records {
		payload, err := json.Marshal(record)
		if err != nil || spent+len(payload) > 48<<10 {
			continue
		}
		spent += len(payload)
		payloads = append(payloads, string(payload))
	}
	return payloads
}

// ParseConsolidation parses the model's operation list.
func ParseConsolidation(raw string) ([]ConsolidationOp, error) {
	raw = strings.TrimSpace(raw)
	start := strings.IndexByte(raw, '[')
	end := strings.LastIndexByte(raw, ']')
	if start < 0 || end <= start {
		return nil, fmt.Errorf("memory: response contains no JSON array")
	}
	var ops []ConsolidationOp
	if err := json.Unmarshal([]byte(raw[start:end+1]), &ops); err != nil {
		return nil, fmt.Errorf("memory: parse consolidation: %w", err)
	}
	if len(ops) > maxConsolidationOps {
		ops = ops[:maxConsolidationOps]
	}
	return ops, nil
}

// ConsolidationCandidates returns the active records handed to the model,
// most recently updated first, bounded to keep the prompt small.
func (st *Store) ConsolidationCandidates(ctx context.Context) ([]Record, error) {
	scored, err := st.List(ctx, ListQuery{Status: StatusActive, Limit: maxConsolidationInput})
	if err != nil {
		return nil, err
	}
	records := make([]Record, len(scored))
	for index := range scored {
		records[index] = scored[index].Record
	}
	var complete []Record
	spent := 0
	for _, record := range records {
		payload, err := json.Marshal(record)
		if err != nil || spent+len(payload) > 48<<10 {
			continue
		}
		spent += len(payload)
		complete = append(complete, record)
	}
	return complete, nil
}

// ApplyConsolidation validates and applies model-proposed operations.
// Invalid operations are skipped (never fatal); every applied change is
// journaled. Returns the number of applied operations.
func (st *Store) ApplyConsolidation(
	ctx context.Context, ops []ConsolidationOp, sessionID string,
) (int, error) {
	applied := 0
	if len(ops) > maxConsolidationOps {
		ops = ops[:maxConsolidationOps]
	}
	for _, op := range ops {
		changed, err := st.applyConsolidationOp(ctx, op, sessionID)
		if err != nil {
			return applied, err
		}
		if changed {
			applied++
		}
	}
	if applied > 0 {
		st.journal(ctx, ActorConsolidation, "consolidate_run", "", sessionID,
			fmt.Sprintf("applied %d ops", applied))
	}
	return applied, nil
}

func sameApplicability(first, second Record) bool {
	firstPaths, _ := json.Marshal(first.ScopePaths)
	secondPaths, _ := json.Marshal(second.ScopePaths)
	return first.Kind == second.Kind && first.AppliesWhen == second.AppliesWhen && string(firstPaths) == string(secondPaths)
}

func assertionText(text string) string {
	return strings.TrimRight(strings.ToLower(strings.Join(strings.Fields(text), " ")), ".")
}

func (st *Store) applyConsolidationOp(ctx context.Context, op ConsolidationOp, sessionID string) (bool, error) {
	ids := []string{op.ID}
	switch op.Op {
	case "merge":
		if len(op.IDs) < 2 || len(op.IDs) > 4 || op.Title == "" || op.Content == "" {
			return false, nil
		}
		ids = op.IDs
	case "supersede":
		if op.ID == "" || op.By == "" || op.ID == op.By {
			return false, nil
		}
		ids = append(ids, op.By)
	case "rewrite":
		if op.ID == "" || op.Content == "" {
			return false, nil
		}
	default:
		return false, nil
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	var records []Record
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			return false, nil
		}
		seen[id] = true
		record, err := getExpectedTx(ctx, tx, id, op.ExpectedRevisions[id])
		if err != nil {
			if errors.Is(err, ErrRevisionConflict) || errors.Is(err, ErrRetired) || errors.Is(err, sql.ErrNoRows) {
				return false, nil
			}
			return false, err
		}
		if record.Status != StatusActive || record.Pinned {
			return false, nil
		}
		records = append(records, record)
	}
	switch op.Op {
	case "rewrite":
		record := records[0]
		if assertionText(record.Content) != assertionText(op.Content) {
			return false, nil
		}
		expected := record.Revision
		record.Content, record.Source, record.Actor = op.Content, SourceConsolidation, ActorConsolidation
		record.Provenance += fmt.Sprintf(" rewrite:%s@%d", record.ID, expected)
		record.Revision++
		if err := validateKindDefaults(&record); err != nil {
			return false, nil
		}
		deriveEntities(&record)
		if err := writeRecordTx(ctx, tx, record, expected, "rewrite", sessionID); err != nil {
			return false, err
		}
	case "merge":
		merged := records[0]
		var evidence []Evidence
		seenEvidence := make(map[Evidence]bool)
		for _, record := range records {
			for _, item := range record.Evidence {
				if !seenEvidence[item] {
					seenEvidence[item] = true
					evidence = append(evidence, item)
				}
			}
		}
		merged.Evidence = evidence
		for _, record := range records[1:] {
			if !sameApplicability(merged, record) || assertionText(merged.Content) != assertionText(record.Content) {
				return false, nil
			}
			merged.Provenance += " | " + record.Provenance
		}
		if assertionText(merged.Content) != assertionText(op.Content) {
			return false, nil
		}
		merged.ID, merged.Revision = "", 0
		merged.Title, merged.Content, merged.Source = op.Title, op.Content, SourceConsolidation
		merged.SourceSessionID = sessionID
		merged.Provenance += " merge:" + strings.Join(ids, ",")
		if op.Kind != "" && op.Kind != merged.Kind {
			return false, nil
		}
		if op.Importance > 0 {
			merged.Importance = op.Importance
		}
		if err := validateKindDefaults(&merged); err != nil {
			return false, nil
		}
		merged, err = rememberTx(ctx, tx, merged)
		if err != nil {
			return false, err
		}
		for _, record := range records {
			if err := retireTx(ctx, tx, record, merged.ID, ActorConsolidation, sessionID); err != nil {
				return false, err
			}
		}
	case "supersede":
		if !sameApplicability(records[0], records[1]) || !supportsKnowledge(records[1].Kind, records[1].Evidence) {
			return false, nil
		}
		var contradicted int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_feedback WHERE memory_id=? AND revision=? AND kind=?`,
			op.ID, records[0].Revision, FeedbackContradicted).Scan(&contradicted); err != nil {
			return false, err
		}
		if contradicted == 0 {
			return false, nil
		}
		if err := retireTx(ctx, tx, records[0], op.By, ActorConsolidation, sessionID); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (st *Store) DuplicateSuggestions(ctx context.Context) ([][]RevisionRef, error) {
	scored, err := st.List(ctx, ListQuery{Status: StatusActive, Limit: 500})
	if err != nil {
		return nil, err
	}
	groups := make(map[string][]RevisionRef)
	for _, record := range scored {
		paths, _ := json.Marshal(record.ScopePaths)
		key := assertionText(record.Content) + "\x00" + record.Kind + "\x00" + record.AppliesWhen + string(paths)
		groups[key] = append(groups[key], RevisionRef{ID: record.ID, Revision: record.Revision})
	}
	var suggestions [][]RevisionRef
	for _, group := range groups {
		if len(group) > 1 {
			suggestions = append(suggestions, group)
		}
	}
	return suggestions, nil
}
