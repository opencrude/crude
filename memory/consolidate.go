package memory

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Consolidation limits: conservative on purpose — a background rewrite that
// goes wrong must not be able to gut the store.
const (
	maxConsolidationOps   = 12
	maxConsolidationInput = 60
)

// ConsolidationLockName is the cooperative lock for the background job.
const ConsolidationLockName = "consolidate"

// ConsolidationOp is one operation proposed by the consolidation model.
type ConsolidationOp struct {
	// Op is "merge" (combine duplicate/overlapping records into one),
	// "supersede" (a record contradicts a newer one), or "rewrite"
	// (same knowledge, tighter text).
	Op         string   `json:"op"`
	IDs        []string `json:"ids,omitempty"` // merge sources
	ID         string   `json:"id,omitempty"`  // supersede/rewrite target
	By         string   `json:"by,omitempty"`  // supersede: the winning record
	Kind       string   `json:"kind,omitempty"`
	Title      string   `json:"title,omitempty"`
	Content    string   `json:"content,omitempty"`
	Importance int      `json:"importance,omitempty"`
	Reason     string   `json:"reason,omitempty"`
}

// ConsolidationSystemPrompt instructs the background consolidation model.
const ConsolidationSystemPrompt = `You are the memory consolidator for a coding IDE agent. You receive the project's active memories and propose a SHORT list of cleanup operations.

Propose an operation ONLY when clearly justified:
- {"op":"merge","ids":["id1","id2"],"kind":"...","title":"...","content":"...","importance":50,"reason":"..."} — two or more records state the same thing; the merged record replaces them all.
- {"op":"supersede","id":"old","by":"new","reason":"..."} — record old is contradicted or made obsolete by record new (keep new).
- {"op":"rewrite","id":"x","content":"...","reason":"..."} — same knowledge, meaningfully tighter or clearer text.

Rules:
- NEVER invent knowledge that is not in the input records.
- NEVER touch pinned records.
- When records disagree, prefer the more recently updated one.
- No operation is the correct answer for a healthy store. Propose at most 12.

Respond with ONLY a JSON array (possibly empty): [ ... ]`

// BuildConsolidationUser renders active records for the consolidation call.
func BuildConsolidationUser(records []Record) string {
	var b strings.Builder
	b.WriteString("ACTIVE MEMORIES:\n")
	for _, r := range records {
		pinned := ""
		if r.Pinned {
			pinned = " PINNED"
		}
		fmt.Fprintf(&b, "[%s] kind=%s importance=%d updated=%s%s\n  %s: %s\n",
			r.ID, r.Kind, r.Importance,
			time.Unix(0, r.UpdatedAt).UTC().Format("2006-01-02"), pinned,
			r.Title, TruncateBytes(r.Content, 400))
	}
	return b.String()
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
	for i := range scored {
		records[i] = scored[i].Record
	}
	return records, nil
}

// ApplyConsolidation validates and applies model-proposed operations.
// Invalid operations are skipped (never fatal); every applied change is
// journaled. Returns the number of applied operations.
func (st *Store) ApplyConsolidation(
	ctx context.Context, ops []ConsolidationOp, sessionID string,
) (int, error) {
	applied := 0
	for _, op := range ops {
		switch op.Op {
		case "merge":
			if len(op.IDs) < 2 || strings.TrimSpace(op.Title) == "" ||
				strings.TrimSpace(op.Content) == "" {
				continue
			}
			sources, ok := st.loadActiveUnpinned(ctx, op.IDs)
			if !ok {
				continue
			}
			merged, err := st.Remember(ctx, Record{
				Kind: op.Kind, Title: op.Title, Content: op.Content,
				Importance:      op.Importance,
				Source:          SourceConsolidation,
				SourceSessionID: sessionID,
				Provenance:      "merge:" + strings.Join(op.IDs, ","),
			})
			if err != nil {
				continue
			}
			for _, source := range sources {
				if source.ID == merged.ID {
					continue
				}
				_ = st.Supersede(ctx, source.ID, merged.ID, ActorConsolidation, sessionID)
			}
			applied++
		case "supersede":
			if op.ID == "" || op.By == "" || op.ID == op.By {
				continue
			}
			if _, ok := st.loadActiveUnpinned(ctx, []string{op.ID}); !ok {
				continue
			}
			if _, err := st.Get(ctx, op.By); err != nil {
				continue
			}
			if err := st.Supersede(ctx, op.ID, op.By, ActorConsolidation, sessionID); err == nil {
				applied++
			}
		case "rewrite":
			if op.ID == "" || strings.TrimSpace(op.Content) == "" {
				continue
			}
			existing, ok := st.loadActiveUnpinned(ctx, []string{op.ID})
			if !ok {
				continue
			}
			record := existing[0]
			record.Content = op.Content
			record.Source = SourceConsolidation
			if _, err := st.Remember(ctx, record); err == nil {
				applied++
			}
		}
	}
	if applied > 0 {
		st.journal(ctx, ActorConsolidation, "consolidate_run", "", sessionID,
			fmt.Sprintf("applied %d ops", applied))
	}
	return applied, nil
}

// loadActiveUnpinned loads records and verifies every one is active and not
// pinned (the consolidator must not touch pinned records).
func (st *Store) loadActiveUnpinned(ctx context.Context, ids []string) ([]Record, bool) {
	records := make([]Record, 0, len(ids))
	for _, id := range ids {
		record, err := st.Get(ctx, id)
		if err != nil || record.Status != StatusActive || record.Pinned {
			return nil, false
		}
		records = append(records, record)
	}
	return records, true
}

// DedupeExact supersedes exact-content duplicates (normalized text hash),
// keeping the most recently updated record of each group.
func (st *Store) DedupeExact(ctx context.Context) (int, error) {
	scored, err := st.List(ctx, ListQuery{Status: StatusActive, Limit: 500})
	if err != nil {
		return 0, err
	}
	groups := make(map[string][]Record)
	for _, s := range scored {
		sum := sha256.Sum256([]byte(NormalizeText(s.Content)))
		key := hex.EncodeToString(sum[:])
		groups[key] = append(groups[key], s.Record)
	}
	superseded := 0
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		sort.Slice(group, func(i, j int) bool {
			if group[i].Pinned != group[j].Pinned {
				return group[i].Pinned
			}
			return group[i].UpdatedAt > group[j].UpdatedAt
		})
		keeper := group[0]
		for _, dup := range group[1:] {
			if dup.Pinned {
				continue
			}
			if err := st.Supersede(ctx, dup.ID, keeper.ID, ActorConsolidation, ""); err == nil {
				superseded++
			}
		}
	}
	return superseded, nil
}

// DecayArchive demotes long-unused auto-extracted records out of the
// auto-injection set. Pinned records and records the agent or user wrote
// explicitly never decay. Archived records still match explicit searches.
func (st *Store) DecayArchive(ctx context.Context, unusedFor time.Duration) (int, error) {
	cutoff := time.Now().Add(-unusedFor).UnixNano()
	rows, err := st.db.QueryContext(ctx, `
		SELECT id FROM workspace_memories
		WHERE status = ? AND pinned = 0 AND source = ?
		  AND use_count = 0 AND created_at < ?
		  AND updated_at < ? AND last_used_at < ?
		LIMIT 100
	`, StatusActive, SourceAuto, cutoff, cutoff, cutoff)
	if err != nil {
		return 0, fmt.Errorf("memory: decay query: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("memory: decay scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := st.Archive(ctx, ids, ActorConsolidation, "decayed: unused"); err != nil {
		return 0, err
	}
	return len(ids), nil
}

// EnforceCap archives the lowest-value unpinned actives when the active set
// exceeds cap, keeping the auto-injection candidate pool bounded.
func (st *Store) EnforceCap(ctx context.Context, cap int) (int, error) {
	if cap <= 0 {
		return 0, nil
	}
	active, err := st.CountByStatus(ctx, StatusActive)
	if err != nil {
		return 0, err
	}
	excess := active - cap
	if excess <= 0 {
		return 0, nil
	}
	rows, err := st.db.QueryContext(ctx, `
		SELECT id FROM workspace_memories
		WHERE status = ? AND pinned = 0
		ORDER BY importance ASC, last_used_at ASC, updated_at ASC
		LIMIT ?
	`, StatusActive, excess)
	if err != nil {
		return 0, fmt.Errorf("memory: cap query: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return 0, fmt.Errorf("memory: cap scan: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return 0, nil
	}
	if err := st.Archive(ctx, ids, ActorConsolidation, "capped: active set over limit"); err != nil {
		return 0, err
	}
	return len(ids), nil
}
