package memory

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const (
	MaxEvidence             = 16
	MaxEvidenceSummaryBytes = 1024
	MaxObservationsPerTurn  = 24
	MaxObservationBytes     = 24 << 10
	MaxFeedbackItems        = 32
)

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)-----BEGIN [^-]*PRIVATE KEY-----.*?-----END [^-]*PRIVATE KEY-----`),
	regexp.MustCompile(`(?i)(?:["']?(?:api[_-]?key|access[_-]?token|refresh[_-]?token|authorization|password|passwd|secret|credential)["']?\s*[:=]\s*)(?:"[^"]*"|'[^']*'|[^\s,;]+)`),
	regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`),
	regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{12,}|gh[pousr]_[A-Za-z0-9]{16,}|AKIA[A-Z0-9]{16})\b`),
	regexp.MustCompile(`\beyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\b`),
	regexp.MustCompile(`(?i)https?://[^\s/@]+:[^\s/@]+@`),
}

func Redact(text string) string {
	lower := strings.ToLower(text)
	if strings.Contains(lower, "<confidential>") || strings.Contains(lower, "<secret>") || strings.Contains(lower, "-----begin private key-----") {
		return "[redacted sensitive observation]"
	}
	for _, pattern := range secretPatterns {
		text = pattern.ReplaceAllString(text, "[redacted]")
	}
	return text
}

func validOutcome(outcome string) bool {
	switch outcome {
	case OutcomeSucceeded, OutcomeFailed, OutcomeUnmeasured, OutcomeCancelled:
		return true
	}
	return false
}

func validateEvidence(evidence []Evidence) error {
	if len(evidence) > MaxEvidence {
		return fmt.Errorf("memory: at most %d evidence references", MaxEvidence)
	}
	for index, item := range evidence {
		var missing []string
		if item.SessionID == "" {
			missing = append(missing, "session_id")
		}
		if item.MessageID == "" && item.ToolCallID == "" {
			missing = append(missing, "tool_call_id or message_id")
		}
		if item.Kind == "" {
			missing = append(missing, "kind")
		}
		if strings.TrimSpace(item.Summary) == "" {
			missing = append(missing, "summary")
		}
		if !validOutcome(item.Outcome) {
			missing = append(missing, "outcome (succeeded|failed|unmeasured|cancelled)")
		}
		if len(missing) > 0 {
			return fmt.Errorf("memory: evidence[%d] is missing %s", index, strings.Join(missing, ", "))
		}
		if len(item.Summary) > MaxEvidenceSummaryBytes || len(item.SessionID) > 256 || len(item.MessageID) > 256 || len(item.ToolCallID) > 256 || len(item.Kind) > 64 {
			return fmt.Errorf("memory: evidence[%d] exceeds bounds (summary at most %d bytes)", index, MaxEvidenceSummaryBytes)
		}
		if Redact(item.Summary) != item.Summary {
			return fmt.Errorf("memory: evidence[%d] summary contains a secret", index)
		}
	}
	return nil
}

func SupportsLesson(evidence []Evidence) bool {
	for _, validation := range evidence {
		if validation.Kind != EvidenceValidation || validation.Outcome != OutcomeSucceeded || validation.ToolCallID == "" {
			continue
		}
		for _, failure := range evidence {
			if failure.SessionID == validation.SessionID && failure.Outcome == OutcomeFailed &&
				failure.ToolCallID != "" && failure.ToolCallID != validation.ToolCallID &&
				(failure.Kind == EvidenceTool || failure.Kind == EvidenceValidation) {
				for _, change := range evidence {
					if change.SessionID == validation.SessionID && change.Kind == EvidenceChange && change.Outcome == OutcomeSucceeded && change.ToolCallID != "" {
						return true
					}
				}
			}
		}
	}
	return false
}

func supportsKnowledge(kind string, evidence []Evidence) bool {
	if kind == KindLesson {
		return SupportsLesson(evidence)
	}
	for _, item := range evidence {
		if item.Kind == EvidenceUser {
			return true
		}
		if kind != KindPreference && (item.Kind == EvidenceTool || item.Kind == EvidenceValidation || item.Kind == EvidenceChange) && item.Outcome == OutcomeSucceeded {
			return true
		}
	}
	return false
}

type evidenceObservation struct {
	Observation
	Position int
}

// groundedEvidenceTx resolves each cited reference to the durable observation
// with the same origin. The observation, not the caller's synopsis, is the
// authority: kind and outcome must agree, and the returned observations carry
// the recorded summary so stored evidence never repeats an unverified claim.
func groundedEvidenceTx(ctx context.Context, tx *sql.Tx, evidence []Evidence) ([]evidenceObservation, error) {
	grounded := make([]evidenceObservation, 0, len(evidence))
	for index, item := range evidence {
		candidates, err := observationsByOriginTx(ctx, tx, item)
		if err != nil {
			return nil, err
		}
		if len(candidates) == 0 {
			return nil, fmt.Errorf("%w: evidence[%d] (%s) has no recorded observation; observations are recorded when a turn completes, so cite tool calls or messages from an earlier turn",
				ErrUnsupported, index, evidenceOrigin(item))
		}
		matched := false
		for _, candidate := range candidates {
			if candidate.Kind == item.Kind && candidate.Outcome == item.Outcome {
				grounded = append(grounded, candidate)
				matched = true
				break
			}
		}
		if !matched {
			observed := candidates[0]
			return nil, fmt.Errorf("%w: evidence[%d] (%s) claims kind=%s outcome=%s but the recorded observation is kind=%s outcome=%s",
				ErrUnsupported, index, evidenceOrigin(item), item.Kind, item.Outcome, observed.Kind, observed.Outcome)
		}
	}
	return grounded, nil
}

func observationsByOriginTx(ctx context.Context, tx *sql.Tx, item Evidence) ([]evidenceObservation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT observation, position FROM memory_observations
		WHERE session_id=? AND tool_call_id=? AND (?='' OR message_id=?) ORDER BY turn, position LIMIT 4`,
		item.SessionID, item.ToolCallID, item.MessageID, item.MessageID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var observations []evidenceObservation
	for rows.Next() {
		var payload string
		var observation evidenceObservation
		if err := rows.Scan(&payload, &observation.Position); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &observation.Observation); err != nil {
			return nil, err
		}
		observations = append(observations, observation)
	}
	return observations, rows.Err()
}

func evidenceOrigin(item Evidence) string {
	if item.ToolCallID != "" {
		return "tool_call_id=" + item.ToolCallID
	}
	return "message_id=" + item.MessageID
}

func supportedLessonObservations(observations []evidenceObservation) bool {
	before := func(first, second evidenceObservation) bool {
		return first.Turn < second.Turn || (first.Turn == second.Turn && first.Position < second.Position)
	}
	for _, failure := range observations {
		if failure.Kind != EvidenceValidation || failure.Outcome != OutcomeFailed || failure.Method == "" {
			continue
		}
		for _, validation := range observations {
			if validation.Kind != EvidenceValidation || validation.Outcome != OutcomeSucceeded ||
				validation.SessionID != failure.SessionID || validation.Method != failure.Method || validation.Conditions != failure.Conditions || !before(failure, validation) {
				continue
			}
			for _, change := range observations {
				if change.Kind == EvidenceChange && change.Outcome == OutcomeSucceeded && change.SessionID == failure.SessionID &&
					before(failure, change) && before(change, validation) {
					return true
				}
			}
		}
	}
	return false
}

func SanitizeObservations(observations []Observation) []Observation {
	var result []Observation
	spent := 0
	for _, observation := range observations {
		if len(result) >= MaxObservationsPerTurn {
			break
		}
		observation.Summary = TruncateBytes(Redact(strings.TrimSpace(observation.Summary)), MaxEvidenceSummaryBytes)
		observation.Method = TruncateBytes(Redact(observation.Method), 512)
		observation.Conditions = TruncateBytes(Redact(observation.Conditions), 1024)
		if err := validateEvidence([]Evidence{observation.Evidence}); err != nil || observation.Turn <= 0 {
			continue
		}
		if len(observation.Paths) > 16 {
			observation.Paths = observation.Paths[:16]
		}
		for index, path := range observation.Paths {
			observation.Paths[index] = TruncateBytes(Redact(path), 512)
		}
		payload, err := json.Marshal(observation)
		if err != nil || spent+len(payload) > MaxObservationBytes {
			continue
		}
		spent += len(payload)
		result = append(result, observation)
	}
	return result
}

func episodeForObservation(situation string, observation Observation) Episode {
	identity := observation.SessionID + "\x00" + observation.ToolCallID + "\x00" + observation.MessageID
	sum := sha256.Sum256([]byte(identity))
	return Episode{
		ID: "episode-" + hex.EncodeToString(sum[:16]), SessionID: observation.SessionID,
		Turn: observation.Turn, Situation: TruncateBytes(Redact(situation), 1024),
		Method: observation.Method, Outcome: observation.Outcome, Conditions: observation.Conditions,
		Evidence: []Evidence{observation.Evidence}, CreatedAt: time.Now().UnixNano(),
	}
}

func recordEpisodeTx(ctx context.Context, tx *sql.Tx, episode Episode) error {
	if episode.ID == "" || episode.SessionID == "" || episode.Turn <= 0 || episode.Method == "" || !validOutcome(episode.Outcome) {
		return fmt.Errorf("memory: episode requires identity, attempted method and outcome")
	}
	if err := validateEvidence(episode.Evidence); err != nil || len(episode.Evidence) == 0 {
		return fmt.Errorf("memory: episode requires bounded evidence")
	}
	var existing string
	err := tx.QueryRowContext(ctx, `SELECT episode FROM memory_episodes WHERE id=?`, episode.ID).Scan(&existing)
	if err == nil {
		var previous Episode
		if err := json.Unmarshal([]byte(existing), &previous); err != nil {
			return err
		}
		episode.CreatedAt = previous.CreatedAt
		payload, err := json.Marshal(episode)
		if err != nil {
			return err
		}
		if string(payload) != existing {
			return fmt.Errorf("%w: episode identity reused with different observation", ErrExists)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	payload, err := json.Marshal(episode)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO memory_episodes(id, session_id, turn, episode, created_at) VALUES (?, ?, ?, ?, ?)`,
		episode.ID, episode.SessionID, episode.Turn, string(payload), episode.CreatedAt)
	return err
}

func (st *Store) Episodes(ctx context.Context, sessionID string, limit int) ([]Episode, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := st.db.QueryContext(ctx, `SELECT episode FROM memory_episodes WHERE (?='' OR session_id=?) ORDER BY created_at DESC LIMIT ?`, sessionID, sessionID, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []Episode
	for rows.Next() {
		var payload string
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		var episode Episode
		if err := json.Unmarshal([]byte(payload), &episode); err != nil {
			return nil, err
		}
		result = append(result, episode)
	}
	return result, rows.Err()
}

func (st *Store) Expose(ctx context.Context, refs []RevisionRef, sessionID, kind string) error {
	if len(refs) > 50 || (kind != "automatic" && kind != "recall") {
		return fmt.Errorf("memory: invalid exposure batch")
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, ref := range refs {
		if err := revisionExistsTx(ctx, tx, ref); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_exposures(memory_id, revision, session_id, kind, at) VALUES (?, ?, ?, ?, ?)`,
			ref.ID, ref.Revision, sessionID, kind, time.Now().UnixNano()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func revisionExistsTx(ctx context.Context, tx *sql.Tx, ref RevisionRef) error {
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM memory_revisions WHERE memory_id=? AND revision=?`, ref.ID, ref.Revision).Scan(&exists); err != nil {
		return err
	}
	if exists != 1 {
		return fmt.Errorf("%w: unknown record revision", ErrRevisionConflict)
	}
	return nil
}

func (st *Store) Feedback(ctx context.Context, items []Feedback) error {
	if len(items) == 0 || len(items) > MaxFeedbackItems {
		return fmt.Errorf("memory: feedback requires 1-%d items", MaxFeedbackItems)
	}
	tx, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for index, item := range items {
		switch item.Kind {
		case FeedbackUseful, FeedbackMisleading, FeedbackContradicted, FeedbackNotApplicable:
		default:
			return fmt.Errorf("memory: items[%d] has unknown feedback kind %q (useful|misleading|contradicted|not_applicable)", index, item.Kind)
		}
		if len(item.Evidence) == 0 {
			return fmt.Errorf("memory: items[%d] requires at least one evidence reference", index)
		}
		if err := validateEvidence(item.Evidence); err != nil {
			return fmt.Errorf("items[%d]: %w", index, err)
		}
		if err := revisionExistsTx(ctx, tx, item.RevisionRef); err != nil {
			return err
		}
		payload, err := json.Marshal(item.Evidence)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO memory_feedback(memory_id, revision, session_id, actor, kind, evidence, at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
			item.ID, item.Revision, item.SessionID, item.Actor, item.Kind, string(payload), time.Now().UnixNano()); err != nil {
			return err
		}
		if err := journalTx(ctx, tx, item.Actor, "feedback", item.ID, item.SessionID, fmt.Sprintf("revision:%d %s", item.Revision, item.Kind)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (st *Store) FeedbackForMemory(ctx context.Context, id string, limit int) ([]Feedback, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := st.db.QueryContext(ctx, `SELECT revision, session_id, actor, kind, evidence, at FROM memory_feedback WHERE memory_id=? ORDER BY id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []Feedback
	for rows.Next() {
		item := Feedback{RevisionRef: RevisionRef{ID: id}}
		var payload string
		if err := rows.Scan(&item.Revision, &item.SessionID, &item.Actor, &item.Kind, &payload, &item.At); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(payload), &item.Evidence); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}

func (st *Store) ExposuresForMemory(ctx context.Context, id string, limit int) ([]Exposure, error) {
	if limit <= 0 || limit > 100 {
		limit = 30
	}
	rows, err := st.db.QueryContext(ctx, `SELECT revision, session_id, kind, at FROM memory_exposures WHERE memory_id=? ORDER BY id DESC LIMIT ?`, id, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var result []Exposure
	for rows.Next() {
		item := Exposure{RevisionRef: RevisionRef{ID: id}}
		if err := rows.Scan(&item.Revision, &item.SessionID, &item.Kind, &item.At); err != nil {
			return nil, err
		}
		result = append(result, item)
	}
	return result, rows.Err()
}
