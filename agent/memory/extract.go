package memory

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	rootprompt "crude/agent/prompt"
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

// Restatement cites new turns that state an existing memory again.
type Restatement struct {
	ID       string `json:"id"`
	Evidence []int  `json:"evidence"`
}

// ExtractionResult is the parsed output of one extraction call.
type ExtractionResult struct {
	Memories []ExtractedMemory `json:"memories"`
	Restated []Restatement     `json:"restated"`
	Summary  *ExtractedSummary `json:"summary"`
}

// Reinforcement is grounded support for knowledge the store already holds.
type Reinforcement struct {
	ID       string
	Evidence []Evidence
}

// extractionInputBytes bounds the turns and evidence of one curator request,
// as the earlier fixed transcript and evidence limits did together.
const extractionInputBytes = 40 << 10

// ExtractionWindow is the part of a session's pending turns that one curator
// request covers: their rendered transcript, their evidence and the last turn.
type ExtractionWindow struct {
	Transcript   string
	Observations []Observation
	ThroughTurn  int
}

// PlanExtractionWindow takes pending turns, oldest first, while their
// transcript and evidence fit one request, and always at least one turn. The
// other turns wait for the next request, so a full window never cuts its
// newest turn. A first turn that does not fit alone keeps its transcript and
// the evidence SelectObservations prefers.
func PlanExtractionWindow(turns []HistoryHit, observations []Observation) ExtractionWindow {
	byTurn := make(map[int][]Observation)
	for _, observation := range observations {
		byTurn[observation.Turn] = append(byTurn[observation.Turn], observation)
	}
	var window ExtractionWindow
	var transcript strings.Builder
	spent := 0
	for index, turn := range turns {
		text := TruncateMiddle(rootprompt.MemoryExtractionTurn(rootprompt.MemoryTurn{SessionID: turn.SessionID, Turn: turn.Turn,
			User: Redact(turn.UserText), Tools: Redact(turn.ToolNames), Files: Redact(turn.Files), Report: Redact(turn.AssistantText)}), extractionInputBytes/2)
		evidence := byTurn[turn.Turn]
		cost := len(text) + observationBytes(evidence)
		if index > 0 && spent+cost > extractionInputBytes {
			break
		}
		if cost > extractionInputBytes {
			evidence = SelectObservations(evidence, len(evidence), extractionInputBytes-len(text))
			cost = len(text) + observationBytes(evidence)
		}
		spent += cost
		transcript.WriteString(text)
		window.Observations = append(window.Observations, evidence...)
		window.ThroughTurn = turn.Turn
	}
	window.Transcript = transcript.String()
	return window
}

func observationBytes(observations []Observation) int {
	total := 0
	for _, observation := range observations {
		if payload, err := json.Marshal(observation); err == nil {
			total += len(payload)
		}
	}
	return total
}

// PrepareExtractionInput builds one curator request from the previous summary,
// the existing memory titles and a planned window.
func PrepareExtractionInput(previousSummary, existingTitles string, window ExtractionWindow) rootprompt.MemoryExtractionInput {
	input := rootprompt.MemoryExtractionInput{
		PreviousSummary: TruncateMiddle(Redact(previousSummary), maxTurnTextBytes),
		ExistingTitles:  TruncateBytes(Redact(existingTitles), 4096),
		Transcript:      window.Transcript,
	}
	for _, observation := range window.Observations {
		payload, err := json.Marshal(observation)
		if err != nil {
			continue
		}
		input.Observations = append(input.Observations, string(payload))
	}
	return input
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

// GroundRestatements keeps only restatements cited by this window's own
// observations. Repetition strengthens a memory; it never invents one.
func GroundRestatements(result ExtractionResult, observations []Observation, sessionID string) []Reinforcement {
	var out []Reinforcement
	for _, restated := range result.Restated {
		if restated.ID == "" || len(restated.Evidence) == 0 || len(restated.Evidence) > MaxEvidence {
			continue
		}
		reinforcement := Reinforcement{ID: restated.ID}
		for _, index := range restated.Evidence {
			if index < 0 || index >= len(observations) || observations[index].SessionID != sessionID {
				reinforcement.Evidence = nil
				break
			}
			reinforcement.Evidence = append(reinforcement.Evidence, observations[index].Evidence)
		}
		if len(reinforcement.Evidence) > 0 {
			out = append(out, reinforcement)
		}
	}
	return out
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
	Reinforcements     []Reinforcement
	Summary            *SessionSummary
}

// ExtractionCommit reports what one curator window changed.
type ExtractionCommit struct {
	Saved      []Record
	Reinforced []Record
}

func (st *Store) KnowledgeGeneration(ctx context.Context) (int64, error) {
	raw, err := st.Meta(ctx, "knowledge_generation")
	if err != nil || raw == "" {
		return 0, err
	}
	return strconv.ParseInt(raw, 10, 64)
}

// CommitExtraction applies one curator window and advances its watermark together.
func (st *Store) CommitExtraction(ctx context.Context, batch ExtractionBatch) (ExtractionCommit, error) {
	var commit ExtractionCommit
	if batch.SessionID == "" || batch.ThroughTurn <= batch.ExpectedWatermark || len(batch.Records) > 5 || len(batch.Reinforcements) > 5 {
		return commit, fmt.Errorf("memory: invalid extraction batch")
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return commit, err
	}
	defer func() { _ = tx.Rollback() }()
	for key, expected := range map[string]int64{"extract:" + batch.SessionID: int64(batch.ExpectedWatermark), "knowledge_generation": batch.ExpectedGeneration} {
		var raw string
		err := tx.QueryRowContext(ctx, `SELECT value FROM memory_meta WHERE key=?`, key).Scan(&raw)
		if err != nil && err != sql.ErrNoRows {
			return commit, err
		}
		current := int64(0)
		if raw != "" {
			current, err = strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return commit, err
			}
		}
		if current != expected {
			return commit, fmt.Errorf("%w: extraction %s changed", ErrRevisionConflict, key)
		}
	}
	reinforce := func(id string, evidence []Evidence) error {
		record, changed, err := reinforceTx(ctx, tx, id, evidence, batch.SessionID)
		if err == nil && changed {
			commit.Reinforced = append(commit.Reinforced, record)
		}
		return err
	}
	for _, record := range batch.Records {
		if record.Source != SourceAuto || record.SourceSessionID != batch.SessionID {
			return commit, fmt.Errorf("memory: extraction source mismatch")
		}
		if err := validateKindDefaults(&record); err != nil {
			return commit, err
		}
		// The same knowledge stated again strengthens the record that holds it.
		var existing string
		switch err := tx.QueryRowContext(ctx, `SELECT id FROM workspace_memories WHERE kind=? AND lower(trim(title))=lower(trim(?)) ORDER BY status = ? DESC LIMIT 1`, record.Kind, record.Title, StatusActive).Scan(&existing); {
		case err == nil:
			if err := reinforce(existing, record.Evidence); err != nil {
				return commit, err
			}
			continue
		case !errors.Is(err, sql.ErrNoRows):
			return commit, err
		}
		record, err := rememberTx(ctx, tx, record)
		if err != nil {
			return commit, err
		}
		commit.Saved = append(commit.Saved, record)
	}
	for _, reinforcement := range batch.Reinforcements {
		if err := reinforce(reinforcement.ID, reinforcement.Evidence); err != nil {
			return commit, err
		}
	}
	if batch.Summary != nil {
		if batch.Summary.SessionID != batch.SessionID {
			return commit, fmt.Errorf("memory: extraction summary source mismatch")
		}
		if err := saveSessionSummaryTx(ctx, tx, *batch.Summary); err != nil {
			return commit, err
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO memory_meta(key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		"extract:"+batch.SessionID, strconv.Itoa(batch.ThroughTurn), time.Now().UnixNano()); err != nil {
		return commit, err
	}
	if err := tx.Commit(); err != nil {
		return commit, err
	}
	return commit, nil
}

// reinforceTx adds new support to an active record stated again. It keeps the
// record's text and authority; only its evidence and freshness change, as an
// audited revision. Retired records and already cited evidence change nothing.
func reinforceTx(ctx context.Context, tx *sql.Tx, id string, evidence []Evidence, sessionID string) (Record, bool, error) {
	record, err := scanRecord(tx.QueryRowContext(ctx, `SELECT `+recordSelectColumns+` FROM workspace_memories WHERE id=? AND status=?`, id, StatusActive))
	if errors.Is(err, sql.ErrNoRows) {
		return Record{}, false, nil
	}
	if err != nil {
		return Record{}, false, err
	}
	added := false
	for _, item := range evidence {
		if !slices.ContainsFunc(record.Evidence, func(known Evidence) bool {
			return known.SessionID == item.SessionID && known.MessageID == item.MessageID && known.ToolCallID == item.ToolCallID
		}) {
			record.Evidence = append(record.Evidence, item)
			added = true
		}
	}
	if !added {
		return record, false, nil
	}
	if excess := len(record.Evidence) - MaxEvidence; excess > 0 {
		record.Evidence = record.Evidence[excess:]
	}
	expected := record.Revision
	record.Revision++
	record.UpdatedAt = time.Now().UnixNano()
	record.Actor = ActorAuto
	if err := writeRecordTx(ctx, tx, record, expected, "reinforce", sessionID); err != nil {
		return Record{}, false, err
	}
	return record, true, nil
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
	if len(result.Restated) > 5 {
		result.Restated = result.Restated[:5]
	}
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
