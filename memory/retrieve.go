package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Retrieval scoring weights exclude exposure and uncalibrated feedback.
// Relevance dominates; knowledge recency, importance and path overlap follow.
const (
	weightRelevance  = 0.50
	weightRecency    = 0.20
	weightImportance = 0.15
	weightPath       = 0.15
	pinnedBoost      = 0.25

	recencyHalfLifeDays = 14.0
	candidateLimit      = 64
	defaultRecallLimit  = 12
)

// Recall returns scored records for a query: FTS5/BM25 candidates fused
// with knowledge recency decay, importance, and path overlap,
// greedily packed under the query's token budget.
func (st *Store) Recall(ctx context.Context, q RecallQuery) ([]ScoredRecord, error) {
	if q.Limit <= 0 || q.Limit > 50 {
		q.Limit = defaultRecallLimit
	}
	statuses := []string{StatusActive}
	if q.IncludeArchived {
		statuses = append(statuses, StatusArchived)
	}

	candidates, err := st.gatherCandidates(ctx, q, statuses)
	if err != nil {
		return nil, err
	}
	if len(candidates) == 0 {
		return nil, nil
	}

	scoreCandidates(candidates, q)
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].Score == candidates[j].Score {
			return candidates[i].ID < candidates[j].ID
		}
		return candidates[i].Score > candidates[j].Score
	})
	var applicable []ScoredRecord
	for _, candidate := range candidates {
		if q.IncludeArchived || scopeApplicable(candidate.Record, append(append([]string(nil), q.ActivePaths...), q.TouchedPaths...)) {
			applicable = append(applicable, candidate)
		}
	}
	return packBudget(applicable, q.Limit, q.BudgetTokens), nil
}

func scopeApplicable(record Record, paths []string) bool {
	if len(record.ScopePaths) == 0 {
		return true
	}
	for _, scope := range record.ScopePaths {
		scope = strings.TrimSuffix(filepath.ToSlash(scope), "/")
		for _, path := range paths {
			path = filepath.ToSlash(path)
			if path == scope || strings.HasPrefix(path, scope+"/") {
				return true
			}
			if matched, err := filepath.Match(scope, path); err == nil && matched {
				return true
			}
		}
	}
	return len(paths) == 0
}

// gatherCandidates merges FTS matches (or the scan fallback), pinned
// records, and — for empty queries — recent records, deduplicated by ID.
func (st *Store) gatherCandidates(
	ctx context.Context, q RecallQuery, statuses []string,
) ([]ScoredRecord, error) {
	byID := make(map[string]*ScoredRecord)
	merge := func(records []ScoredRecord) {
		for i := range records {
			record := records[i]
			if existing, ok := byID[record.ID]; ok {
				if record.Relevance > existing.Relevance {
					existing.Relevance = record.Relevance
				}
				continue
			}
			byID[record.ID] = &record
		}
	}

	queryText := strings.TrimSpace(q.Text)
	if queryText != "" {
		if st.ftsOK {
			match := BuildMatchQuery(queryText, q.ActivePaths...)
			if match != "" {
				matched, err := st.ftsCandidates(ctx, match, statuses)
				if err != nil {
					// FTS query failure degrades to scan — recall must
					// answer, never silently fail (observable via meta).
					_ = st.SetMeta(ctx, MetaFTSFault, "query: "+err.Error())
					matched = nil
				}
				merge(matched)
			}
		}
		if len(byID) == 0 {
			scanned, err := st.scanCandidates(ctx, queryText, statuses)
			if err != nil {
				return nil, err
			}
			merge(scanned)
		}
	} else {
		recent, err := st.recentCandidates(ctx, statuses)
		if err != nil {
			return nil, err
		}
		merge(recent)
	}

	pinned, err := st.pinnedCandidates(ctx)
	if err != nil {
		return nil, err
	}
	merge(pinned)

	out := make([]ScoredRecord, 0, len(byID))
	for _, record := range byID {
		out = append(out, *record)
	}
	return out, nil
}

func statusPlaceholders(statuses []string) (string, []any) {
	marks := make([]string, len(statuses))
	args := make([]any, len(statuses))
	for i, s := range statuses {
		marks[i] = "?"
		args[i] = s
	}
	return strings.Join(marks, ", "), args
}

// ftsCandidates runs the BM25 query. Column weights: id(unindexed)=0,
// title=4, content=1, entities=2, norm=2 — titles and code entities are
// the highest-signal fields.
func (st *Store) ftsCandidates(
	ctx context.Context, match string, statuses []string,
) ([]ScoredRecord, error) {
	marks, statusArgs := statusPlaceholders(statuses)
	query := `
		SELECT ` + prefixedRecordColumns("m") + `,
		       bm25(workspace_memories_fts, 0, 4.0, 1.0, 2.0, 2.0) AS rank
		FROM workspace_memories_fts
		JOIN workspace_memories m ON m.id = workspace_memories_fts.id
		WHERE workspace_memories_fts MATCH ? AND m.status IN (` + marks + `)
		ORDER BY rank LIMIT ?`
	args := append([]any{match}, statusArgs...)
	args = append(args, candidateLimit)
	rows, err := st.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []ScoredRecord
	for rows.Next() {
		record, rank, err := scanRecordWithRank(rows)
		if err != nil {
			return nil, err
		}
		// bm25() is smaller-is-better (negative); flip to positive.
		out = append(out, ScoredRecord{Record: record, Relevance: -rank})
	}
	return out, rows.Err()
}

// scanCandidates is the FTS-less fallback: substring term scoring over the
// most recent rows (the v0 behavior, kept as the degraded path).
func (st *Store) scanCandidates(
	ctx context.Context, queryText string, statuses []string,
) ([]ScoredRecord, error) {
	marks, statusArgs := statusPlaceholders(statuses)
	rows, err := st.db.QueryContext(ctx, `
		SELECT `+recordSelectColumns+`
		FROM workspace_memories
		WHERE status IN (`+marks+`)
		ORDER BY pinned DESC, updated_at DESC
		LIMIT 512
	`, statusArgs...)
	if err != nil {
		return nil, fmt.Errorf("recall workspace memories: %w", err)
	}
	defer func() { _ = rows.Close() }()

	terms := strings.Fields(strings.ToLower(queryText))
	var out []ScoredRecord
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace memory: %w", err)
		}
		score := 0.0
		title := strings.ToLower(record.Title)
		content := strings.ToLower(record.Content)
		entities := strings.ToLower(record.Entities)
		for _, term := range terms {
			if strings.Contains(title, term) {
				score += 8
			}
			if strings.Contains(entities, term) {
				score += 4
			}
			if strings.Contains(content, term) {
				score += 2
			}
		}
		if score > 0 {
			out = append(out, ScoredRecord{Record: record, Relevance: score})
		}
	}
	return out, rows.Err()
}

func (st *Store) recentCandidates(ctx context.Context, statuses []string) ([]ScoredRecord, error) {
	marks, statusArgs := statusPlaceholders(statuses)
	rows, err := st.db.QueryContext(ctx, `
		SELECT `+recordSelectColumns+`
		FROM workspace_memories
		WHERE status IN (`+marks+`)
		ORDER BY pinned DESC, updated_at DESC
		LIMIT 32
	`, statusArgs...)
	if err != nil {
		return nil, fmt.Errorf("recall recent memories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ScoredRecord
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace memory: %w", err)
		}
		out = append(out, ScoredRecord{Record: record})
	}
	return out, rows.Err()
}

func (st *Store) pinnedCandidates(ctx context.Context) ([]ScoredRecord, error) {
	rows, err := st.db.QueryContext(ctx, `
		SELECT `+recordSelectColumns+`
		FROM workspace_memories
		WHERE status = ? AND pinned = 1
		ORDER BY updated_at DESC
		LIMIT 16
	`, StatusActive)
	if err != nil {
		return nil, fmt.Errorf("recall pinned memories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ScoredRecord
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace memory: %w", err)
		}
		out = append(out, ScoredRecord{Record: record})
	}
	return out, rows.Err()
}

func prefixedRecordColumns(alias string) string {
	cols := strings.Split(recordSelectColumns, ",")
	for i, col := range cols {
		cols[i] = alias + "." + strings.TrimSpace(col)
	}
	return strings.Join(cols, ", ")
}

func scanRecordWithRank(scanner interface{ Scan(...any) error }) (Record, float64, error) {
	var record Record
	var scopeJSON, evidenceJSON string
	var rank float64
	err := scanner.Scan(
		&record.ID, &record.Kind, &record.Title, &record.Content,
		&record.SourceSessionID, &record.Pinned, &record.CreatedAt, &record.UpdatedAt,
		&record.Entities, &scopeJSON, &record.Importance, &record.Status,
		&record.SupersededBy, &record.Source, &record.Provenance,
		&record.UseCount, &record.LastUsedAt,
		&record.Revision, &record.AppliesWhen, &evidenceJSON, &record.Actor, &rank,
	)
	if err != nil {
		return Record{}, 0, err
	}
	if scopeJSON != "" && scopeJSON != "[]" {
		if err := json.Unmarshal([]byte(scopeJSON), &record.ScopePaths); err != nil {
			return Record{}, 0, err
		}
	}
	if err := json.Unmarshal([]byte(evidenceJSON), &record.Evidence); err != nil {
		return Record{}, 0, err
	}
	return record, rank, nil
}

// scoreCandidates computes the fused score in place. Relevance is min-max
// normalized across the candidate set so BM25 and scan scores land in 0..1.
func scoreCandidates(candidates []ScoredRecord, q RecallQuery) {
	minRel, maxRel := math.Inf(1), math.Inf(-1)
	for i := range candidates {
		rel := candidates[i].Relevance
		if rel < minRel {
			minRel = rel
		}
		if rel > maxRel {
			maxRel = rel
		}
	}
	relSpan := maxRel - minRel

	contextPaths := append(append([]string(nil), q.ActivePaths...), q.TouchedPaths...)
	now := time.Now().UnixNano()
	for i := range candidates {
		c := &candidates[i]
		rel := 0.0
		if relSpan > 0 {
			rel = (c.Relevance - minRel) / relSpan
		} else if c.Relevance > 0 {
			rel = 1.0
		}

		ageDays := float64(now-c.UpdatedAt) / float64(24*time.Hour)
		if ageDays < 0 {
			ageDays = 0
		}
		recency := math.Exp(-ageDays / recencyHalfLifeDays)

		importance := float64(c.Importance) / 100.0

		pathScore := 0.0
		if len(contextPaths) > 0 {
			recordTokens := tokenSet(c.Title + " " + c.Entities + " " + strings.Join(c.ScopePaths, " "))
			pathScore = PathOverlap(recordTokens, contextPaths)
		}

		c.Score = weightRelevance*rel + weightRecency*recency +
			weightImportance*importance + weightPath*pathScore
		if c.Pinned {
			c.Score += pinnedBoost
		}
	}
}

// packBudget greedily takes candidates in score order until either the
// count limit or the token budget is exhausted.
func packBudget(candidates []ScoredRecord, limit, budgetTokens int) []ScoredRecord {
	var out []ScoredRecord
	spent := 0
	for _, c := range candidates {
		if len(out) >= limit {
			break
		}
		cost := EstimateTokens(c.Title) + EstimateTokens(c.Content) + EstimateTokens(c.AppliesWhen) +
			EstimateTokens(c.SourceSessionID) + 40 + len(c.Evidence)*12
		if budgetTokens > 0 && spent+cost > budgetTokens {
			continue
		}
		spent += cost
		out = append(out, c)
	}
	return out
}

// List serves the memory browser: filtered, optionally text-matched rows.
func (st *Store) List(ctx context.Context, q ListQuery) ([]ScoredRecord, error) {
	if q.Limit <= 0 || q.Limit > 500 {
		q.Limit = 100
	}
	statuses := []string{StatusActive, StatusArchived}
	switch q.Status {
	case "":
	case "all":
		statuses = []string{StatusActive, StatusArchived, StatusSuperseded}
	default:
		statuses = []string{q.Status}
	}

	text := strings.TrimSpace(q.Text)
	if text != "" {
		recalled, err := st.Recall(ctx, RecallQuery{
			Text:            text,
			Limit:           q.Limit,
			IncludeArchived: true,
		})
		if err != nil {
			return nil, err
		}
		if q.Kind == "" {
			return recalled, nil
		}
		var filtered []ScoredRecord
		for _, r := range recalled {
			if r.Kind == q.Kind {
				filtered = append(filtered, r)
			}
		}
		return filtered, nil
	}

	marks, args := statusPlaceholders(statuses)
	query := `SELECT ` + recordSelectColumns + `
		FROM workspace_memories WHERE status IN (` + marks + `)`
	if q.Kind != "" {
		query += ` AND kind = ?`
		args = append(args, q.Kind)
	}
	query += ` ORDER BY pinned DESC, updated_at DESC LIMIT ?`
	args = append(args, q.Limit)
	rows, err := st.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list workspace memories: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []ScoredRecord
	for rows.Next() {
		record, err := scanRecord(rows)
		if err != nil {
			return nil, fmt.Errorf("scan workspace memory: %w", err)
		}
		out = append(out, ScoredRecord{Record: record})
	}
	return out, rows.Err()
}
