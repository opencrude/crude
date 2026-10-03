package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	rootprompt "crude/prompt"
)

// ExtractedMemory is one candidate memory proposed by the extraction model.
type ExtractedMemory struct {
	Kind        string   `json:"kind"`
	Title       string   `json:"title"`
	Content     string   `json:"content"`
	Importance  int      `json:"importance"`
	Paths       []string `json:"paths"`
	AppliesWhen string   `json:"applies_when"`
	Evidence    []int    `json:"evidence"`
}

// ExtractedSummary is the rolling session summary proposed by the
// extraction model.
type ExtractedSummary struct {
	Title    string   `json:"title"`
	Summary  string   `json:"summary"`
	Outcome  string   `json:"outcome"`
	KeyFiles []string `json:"key_files"`
}

// ExtractionResult is the parsed output of one extraction call.
type ExtractionResult struct {
	Memories []ExtractedMemory `json:"memories"`
	Summary  *ExtractedSummary `json:"summary"`
}

func PrepareExtractionInput(previousSummary, existingTitles, transcript string, observations []Observation) rootprompt.MemoryExtractionInput {
	input := rootprompt.MemoryExtractionInput{
		PreviousSummary: TruncateBytes(Redact(previousSummary), 4096),
		ExistingTitles:  TruncateBytes(Redact(existingTitles), 4096),
		Transcript:      TruncateBytes(Redact(transcript), 16<<10),
	}
	for _, observation := range observations {
		payload, err := json.Marshal(observation)
		if err != nil {
			continue
		}
		input.Observations = append(input.Observations, string(payload))
	}
	return input
}

func SelectExtractionObservations(observations []Observation) []Observation {
	var selected []Observation
	spent := 0
	for _, observation := range observations {
		payload, err := json.Marshal(observation)
		if err != nil || len(selected) >= 48 || spent+len(payload) > 24<<10 {
			continue
		}
		spent += len(payload)
		selected = append(selected, observation)
	}
	return selected
}

func GroundExtraction(result ExtractionResult, observations []Observation, sessionID, provenance string) []Record {
	var records []Record
	for _, candidate := range result.Memories {
		var evidence []Evidence
		valid := len(candidate.Evidence) > 0 && len(candidate.Evidence) <= MaxEvidence
		lastFailure := -1
		validatedAfterFailure := false
		changedAfterFailure := false
		seen := make(map[int]bool)
		for _, index := range candidate.Evidence {
			if index < 0 || index >= len(observations) || seen[index] {
				valid = false
				break
			}
			seen[index] = true
			observation := observations[index]
			if observation.SessionID != sessionID || !scopeSupported(candidate.Paths, observation.Paths, observation.Kind) {
				valid = false
				break
			}
			evidence = append(evidence, observation.Evidence)
			if observation.Outcome == OutcomeFailed && (observation.Kind == EvidenceValidation || observation.Kind == EvidenceTool) {
				lastFailure = index
			}
		}
		if candidate.Kind == KindLesson {
			for _, index := range candidate.Evidence {
				if index >= 0 && index < len(observations) && index > lastFailure && lastFailure >= 0 {
					observation := observations[index]
					failure := observations[lastFailure]
					if observation.Kind == EvidenceChange && observation.Outcome == OutcomeSucceeded {
						changedAfterFailure = true
					}
					if observation.Kind == EvidenceValidation && observation.Outcome == OutcomeSucceeded &&
						observation.Method == failure.Method && observation.Conditions == failure.Conditions {
						for _, changeIndex := range candidate.Evidence {
							if changeIndex > lastFailure && changeIndex < index && changeIndex >= 0 && changeIndex < len(observations) &&
								observations[changeIndex].Kind == EvidenceChange && observations[changeIndex].Outcome == OutcomeSucceeded {
								validatedAfterFailure = true
							}
						}
					}
				}
			}
			valid = valid && changedAfterFailure && validatedAfterFailure && strings.TrimSpace(candidate.AppliesWhen) != ""
		}
		if !valid || !supportsKnowledge(candidate.Kind, evidence) {
			continue
		}
		record := Record{Kind: candidate.Kind, Title: candidate.Title, Content: candidate.Content,
			Importance: candidate.Importance, ScopePaths: candidate.Paths, AppliesWhen: candidate.AppliesWhen,
			Evidence: evidence, Source: SourceAuto, SourceSessionID: sessionID, Provenance: provenance}
		if err := validateKindDefaults(&record); err != nil {
			continue
		}
		records = append(records, record)
	}
	return records
}

func scopeSupported(paths, observedPaths []string, kind string) bool {
	if len(paths) == 0 || kind == EvidenceUser {
		return true
	}
	for _, path := range paths {
		matched := false
		for _, observed := range observedPaths {
			if observed == path || strings.HasPrefix(observed, strings.TrimSuffix(path, "/")+"/") {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

type ExtractionBatch struct {
	SessionID          string
	ExpectedWatermark  int
	ThroughTurn        int
	ExpectedGeneration int64
	Records            []Record
	Summary            *SessionSummary
}

func (st *Store) KnowledgeGeneration(ctx context.Context) (int64, error) {
	raw, err := st.GetMeta(ctx, "knowledge_generation")
	if err != nil || raw == "" {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

func (st *Store) CommitExtraction(ctx context.Context, batch ExtractionBatch) ([]Record, error) {
	if batch.SessionID == "" || batch.ThroughTurn <= batch.ExpectedWatermark || len(batch.Records) > 5 {
		return nil, fmt.Errorf("memory: invalid extraction batch")
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	for key, expected := range map[string]int64{"extract:" + batch.SessionID: int64(batch.ExpectedWatermark), "knowledge_generation": batch.ExpectedGeneration} {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT value FROM memory_meta WHERE key=?`, key).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return nil, err
		}
		current := int64(0)
		if raw != "" {
			current, err = strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return nil, err
			}
		}
		if current != expected {
			return nil, fmt.Errorf("%w: extraction %s changed", ErrRevisionConflict, key)
		}
	}
	var saved []Record
	for _, record := range batch.Records {
		if record.Source != SourceAuto || record.SourceSessionID != batch.SessionID {
			return nil, fmt.Errorf("memory: extraction source mismatch")
		}
		if err := validateKindDefaults(&record); err != nil {
			return nil, err
		}
		var conflicts int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM workspace_memories WHERE kind=? AND lower(trim(title))=lower(trim(?))`, record.Kind, record.Title).Scan(&conflicts); err != nil {
			return nil, err
		}
		if conflicts > 0 {
			if err := journalTx(ctx, tx, ActorAuto, "merge_suggested", "", batch.SessionID, record.Title); err != nil {
				return nil, err
			}
			continue
		}
		record, err := rememberTx(ctx, tx, record)
		if err != nil {
			return nil, err
		}
		saved = append(saved, record)
	}
	if batch.Summary != nil {
		if batch.Summary.SessionID != batch.SessionID {
			return nil, fmt.Errorf("memory: extraction summary source mismatch")
		}
		if err := saveSessionSummaryTx(ctx, tx, *batch.Summary); err != nil {
			return nil, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_meta(key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		"extract:"+batch.SessionID, strconv.Itoa(batch.ThroughTurn), time.Now().UnixNano()); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return saved, nil
}

// ParseExtraction parses a (possibly noisy) model response into an
// ExtractionResult. It tolerates markdown fences and leading/trailing prose
// but rejects structurally invalid output.
func ParseExtraction(raw string) (ExtractionResult, error) {
	payload, err := extractJSONObject(raw)
	if err != nil {
		return ExtractionResult{}, err
	}
	var result ExtractionResult
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		return ExtractionResult{}, fmt.Errorf("memory: parse extraction: %w", err)
	}
	valid := result.Memories[:0]
	for _, m := range result.Memories {
		m.Title = strings.TrimSpace(m.Title)
		m.Content = strings.TrimSpace(m.Content)
		if m.Title == "" || m.Content == "" {
			continue
		}
		switch m.Kind {
		case KindFact, KindDecision, KindConvention, KindPreference, KindLesson, KindReference:
		default:
			m.Kind = KindFact
		}
		if m.Importance <= 0 || m.Importance > 100 {
			m.Importance = DefaultImportance
		}
		valid = append(valid, m)
	}
	if len(valid) > 5 {
		valid = valid[:5]
	}
	result.Memories = valid
	if result.Summary != nil {
		result.Summary.Summary = strings.TrimSpace(result.Summary.Summary)
		if result.Summary.Summary == "" {
			result.Summary = nil
		}
	}
	return result, nil
}

// extractJSONObject isolates the outermost JSON object in a model response.
func extractJSONObject(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	start := strings.IndexByte(raw, '{')
	end := strings.LastIndexByte(raw, '}')
	if start < 0 || end <= start {
		return "", fmt.Errorf("memory: response contains no JSON object")
	}
	return raw[start : end+1], nil
}
