package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	rootprompt "crude/prompt"
)

func userEvidence(t *testing.T, store *Store, sessionID string) []Evidence {
	t.Helper()
	evidence := Evidence{SessionID: sessionID, MessageID: "human-1", Kind: EvidenceUser,
		Summary: "Use the focused package test before handoff.", Outcome: OutcomeUnmeasured}
	if err := store.RecordTurn(t.Context(), TurnRecord{SessionID: sessionID, Turn: 1,
		UserText: evidence.Summary, Observations: []Observation{{Evidence: evidence, Turn: 1}}}); err != nil {
		t.Fatal(err)
	}
	return []Evidence{evidence}
}

func lessonObservations() []Observation {
	return []Observation{
		{Evidence: Evidence{SessionID: "s1", ToolCallID: "fail", Kind: EvidenceValidation,
			Summary: "TestMoveWordLeft failed across blank lines", Outcome: OutcomeFailed}, Turn: 1,
			Method: "go test ./tui/editor -run TestMoveWordLeft", Conditions: "project", Paths: []string{"tui/editor/movement.go"}},
		{Evidence: Evidence{SessionID: "s1", ToolCallID: "fix", Kind: EvidenceChange,
			Summary: "Replaced early return with continue", Outcome: OutcomeSucceeded}, Turn: 1,
			Method: "edit_file tui/editor/movement.go", Conditions: "project", Paths: []string{"tui/editor/movement.go"}},
		{Evidence: Evidence{SessionID: "s1", ToolCallID: "pass", Kind: EvidenceValidation,
			Summary: "ok crude/tui/editor TestMoveWordLeft", Outcome: OutcomeSucceeded}, Turn: 1,
			Method: "go test ./tui/editor -run TestMoveWordLeft", Conditions: "project", Paths: []string{"tui/editor/movement.go"}},
	}
}

func lessonEvidence(t *testing.T, store *Store) []Evidence {
	t.Helper()
	observations := lessonObservations()
	if err := store.RecordTurn(t.Context(), TurnRecord{SessionID: "s1", Turn: 1,
		UserText: "Fix word-left movement", Observations: observations}); err != nil {
		t.Fatal(err)
	}
	var evidence []Evidence
	for _, observation := range observations {
		evidence = append(evidence, observation.Evidence)
	}
	return evidence
}

func TestRememberSameTitleNeverOverwritesOrRevives(t *testing.T) {
	store := openTestStore(t)
	first, err := store.Remember(t.Context(), Record{Title: "Parser behavior", Content: "Parser returns bytes."})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Remember(t.Context(), Record{Title: first.Title, Content: "Parser returns runes."})
	if err != nil || second.ID == first.ID {
		t.Fatalf("same-title assertion silently overwrote identity: %+v %v", second, err)
	}
	if err := store.Supersede(t.Context(), first.ID, first.Revision, "", ActorUser, "s2"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Remember(t.Context(), Record{ID: first.ID, Title: first.Title, Content: "Revive stale advice"}); !errors.Is(err, ErrExists) {
		t.Fatalf("existing ID was revived: %v", err)
	}
	if _, err := store.Update(t.Context(), first.ID, Record{Revision: 2, Content: "late correction"}, ActorAuto, "s3"); !errors.Is(err, ErrRetired) {
		t.Fatalf("retired record accepted update: %v", err)
	}
	revisions, err := store.Revisions(t.Context(), first.ID)
	if err != nil || len(revisions) != 2 || revisions[0].Content != first.Content || revisions[1].Status != StatusSuperseded {
		t.Fatalf("retirement lost provenance: %+v %v", revisions, err)
	}
}

func TestConcurrentUpdatesOnlyOneExpectedRevisionWins(t *testing.T) {
	store := openTestStore(t)
	record, err := store.Remember(t.Context(), Record{Title: "Concurrency", Content: "initial"})
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make(chan error, 2)
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func(content string) {
			defer group.Done()
			_, err := store.Update(context.Background(), record.ID, Record{Revision: 1, Content: content}, ActorTool, "worker")
			results <- err
		}(fmt.Sprintf("candidate-%d", index))
	}
	group.Wait()
	close(results)
	wins, conflicts := 0, 0
	for err := range results {
		if err == nil {
			wins++
		} else if errors.Is(err, ErrRevisionConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 1 {
		t.Fatalf("CAS wins=%d conflicts=%d", wins, conflicts)
	}
}

func TestJournalFailureRollsBackKnowledgeAndRevision(t *testing.T) {
	store := openTestStore(t)
	if _, err := store.db.Exec(`CREATE TRIGGER fail_journal BEFORE INSERT ON memory_journal BEGIN SELECT RAISE(ABORT, 'audit unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Remember(t.Context(), Record{Title: "Auditable", Content: "Must not survive failed audit"}); err == nil {
		t.Fatal("write reported success despite failed audit")
	}
	for _, table := range []string{"workspace_memories", "memory_revisions"} {
		var count int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("partial write in %s: %d %v", table, count, err)
		}
	}
}

func TestGroundExtractionRequiresObservedFixAndValidation(t *testing.T) {
	base := lessonObservations()
	proposal := ExtractionResult{Memories: []ExtractedMemory{{Kind: KindLesson, Title: "Word-left correction",
		Content: "Continue climbing blank lines instead of returning.", Paths: []string{"tui/editor"},
		AppliesWhen: "When the previous line is whitespace-only", Evidence: []int{0, 1, 2}}}}
	if records := GroundExtraction(proposal, base, "s1", "test"); len(records) != 1 {
		t.Fatalf("supported lesson rejected: %+v", records)
	}
	tests := []struct {
		name   string
		modify func([]Observation)
	}{
		{"assistant_claim", func(observations []Observation) { observations[2].Kind = EvidenceAssistant }},
		{"failed_fix", func(observations []Observation) { observations[1].Outcome = OutcomeFailed }},
		{"unmeasured", func(observations []Observation) { observations[2].Outcome = OutcomeUnmeasured }},
		{"cancelled", func(observations []Observation) { observations[2].Outcome = OutcomeCancelled }},
		{"different_test", func(observations []Observation) { observations[2].Method = "go test ./unrelated" }},
		{"different_conditions", func(observations []Observation) { observations[2].Conditions = "another workspace" }},
		{"wrong_origin", func(observations []Observation) { observations[2].SessionID = "someone-else" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			observations := append([]Observation(nil), base...)
			test.modify(observations)
			if records := GroundExtraction(proposal, observations, "s1", "test"); len(records) != 0 {
				t.Fatalf("unsupported lesson promoted: %+v", records)
			}
		})
	}
	proposal.Memories[0].Evidence = []int{0, 2}
	if records := GroundExtraction(proposal, base, "s1", "test"); len(records) != 0 {
		t.Fatal("passing test without observed change was promoted")
	}
}

func TestForgedEvidenceCannotEstablishTechnicalLesson(t *testing.T) {
	store := openTestStore(t)
	var evidence []Evidence
	for _, observation := range lessonObservations() {
		evidence = append(evidence, observation.Evidence)
	}
	if _, err := store.Remember(t.Context(), Record{Kind: KindLesson, Title: "Forged fix", Content: "Unsupported", AppliesWhen: "A test fails", Evidence: evidence}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("invented evidence accepted: %v", err)
	}
	if _, err := store.Remember(t.Context(), Record{Kind: KindLesson, Title: "Narrative fix", Content: "The assistant said it worked", AppliesWhen: "A test fails"}); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unsupported narrative accepted: %v", err)
	}
}

func TestFailedUnmeasuredCancelledAttemptsRemainEpisodes(t *testing.T) {
	store := openTestStore(t)
	var observations []Observation
	for index, outcome := range []string{OutcomeFailed, OutcomeUnmeasured, OutcomeCancelled} {
		observations = append(observations, Observation{Evidence: Evidence{SessionID: "attempts", ToolCallID: fmt.Sprintf("call-%d", index),
			Kind: EvidenceTool, Summary: "Attempt did not establish a fix", Outcome: outcome}, Turn: 1, Method: "measure resize", Conditions: "dense resumed session"})
	}
	turn := TurnRecord{SessionID: "attempts", Turn: 1, UserText: "Diagnose the slow resize", Observations: observations}
	if err := store.RecordTurn(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordTurn(t.Context(), turn); err != nil {
		t.Fatal(err)
	}
	episodes, err := store.Episodes(t.Context(), "attempts", 10)
	if err != nil || len(episodes) != 3 {
		t.Fatalf("attempt history = %+v %v", episodes, err)
	}
	for _, episode := range episodes {
		if episode.Conditions != "dense resumed session" || len(episode.Evidence) != 1 || episode.Evidence[0].ToolCallID == "" {
			t.Fatalf("attempt lost conditions or origin: %+v", episode)
		}
	}
	count, err := store.CountByStatus(t.Context(), StatusActive)
	if err != nil || count != 0 {
		t.Fatal("episodes unexpectedly became knowledge")
	}
}

func TestExtractionCommitAtomicityAndLateCorrection(t *testing.T) {
	store := openTestStore(t)
	evidence := userEvidence(t, store, "extract")
	batch := ExtractionBatch{SessionID: "extract", ThroughTurn: 1, Records: []Record{
		{Title: "First", Content: "First assertion", Source: SourceAuto, SourceSessionID: "extract", Evidence: evidence},
		{Title: "Second", Content: "Second assertion", Source: SourceAuto, SourceSessionID: "extract", Evidence: evidence}},
		Summary: &SessionSummary{SessionID: "extract", Title: "Extraction", Summary: "Both claims saved", TurnCount: 1}}
	if _, err := store.db.Exec(`CREATE TRIGGER fail_second BEFORE INSERT ON workspace_memories WHEN new.title='Second' BEGIN SELECT RAISE(ABORT, 'write fault'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitExtraction(t.Context(), batch); err == nil {
		t.Fatal("partial extraction was reported as committed")
	}
	count, _ := store.CountByStatus(t.Context(), StatusActive)
	watermark, _ := store.GetMeta(t.Context(), "extract:extract")
	_, summaryExists, _ := store.GetSessionSummary(t.Context(), "extract")
	if count != 0 || watermark != "" || summaryExists {
		t.Fatalf("failed extraction marked unsaved turns processed: count=%d watermark=%q summary=%v", count, watermark, summaryExists)
	}
	if _, err := store.db.Exec(`DROP TRIGGER fail_second`); err != nil {
		t.Fatal(err)
	}
	saved, err := store.CommitExtraction(t.Context(), batch)
	if err != nil || len(saved) != 2 {
		t.Fatalf("retry extraction: %+v %v", saved, err)
	}
	if _, err := store.CommitExtraction(t.Context(), batch); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("duplicate stale extraction committed: %v", err)
	}
	generation, err := store.KnowledgeGeneration(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	batch.ExpectedWatermark, batch.ThroughTurn, batch.ExpectedGeneration = 1, 2, generation
	if _, err := store.Update(t.Context(), saved[0].ID, Record{Revision: saved[0].Revision, Content: "User corrected this fact", Source: SourceUser}, ActorUser, "correction"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CommitExtraction(t.Context(), batch); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("late extraction ignored correction generation: %v", err)
	}
	got, _ := store.Get(t.Context(), saved[0].ID)
	if got.Content != "User corrected this fact" {
		t.Fatal("late extraction overwrote correction")
	}
}

func TestFeedbackIsRevisionSpecificAtomicAndNotRanking(t *testing.T) {
	store := openTestStore(t)
	record, err := store.Remember(t.Context(), Record{Title: "Feedback", Content: "Candidate advice"})
	if err != nil {
		t.Fatal(err)
	}
	evidence := userEvidence(t, store, "review")
	for _, kind := range []string{FeedbackUseful, FeedbackMisleading, FeedbackContradicted, FeedbackNotApplicable} {
		if err := store.Feedback(t.Context(), []Feedback{{RevisionRef: RevisionRef{ID: record.ID, Revision: 1}, Kind: kind, Evidence: evidence, Actor: ActorUser}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.Feedback(t.Context(), []Feedback{
		{RevisionRef: RevisionRef{ID: record.ID, Revision: 1}, Kind: FeedbackUseful, Evidence: evidence},
		{RevisionRef: RevisionRef{ID: record.ID, Revision: 99}, Kind: FeedbackUseful, Evidence: evidence}}); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("invalid batch accepted: %v", err)
	}
	feedback, err := store.FeedbackForMemory(t.Context(), record.ID, 20)
	if err != nil || len(feedback) != 4 {
		t.Fatalf("feedback batch partially saved: %+v %v", feedback, err)
	}
	got, _ := store.Get(t.Context(), record.ID)
	if got.UpdatedAt != record.UpdatedAt || got.Revision != 1 || got.UseCount != 0 {
		t.Fatalf("feedback implicitly changed knowledge ranking: %+v", got)
	}
	if _, err := store.Update(t.Context(), record.ID, Record{Revision: 1, Content: "Corrected advice"}, ActorUser, "correction"); err != nil {
		t.Fatal(err)
	}
	for _, item := range feedback {
		if item.Revision != 1 {
			t.Fatal("feedback followed corrected revision")
		}
	}
}

func TestExposureCannotImproveScoreOrPreventDecay(t *testing.T) {
	store := openTestStore(t)
	evidence := userEvidence(t, store, "observed")
	record, err := store.Remember(t.Context(), Record{Title: "Stale advice", Content: "Old scoped rule", Source: SourceAuto, Evidence: evidence})
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-90 * 24 * time.Hour).UnixNano()
	if _, err := store.db.Exec(`UPDATE workspace_memories SET created_at=?, updated_at=? WHERE id=?`, old, old, record.ID); err != nil {
		t.Fatal(err)
	}
	before, err := store.Recall(t.Context(), RecallQuery{Text: "Stale advice"})
	if err != nil || len(before) != 1 {
		t.Fatal(err)
	}
	for index := 0; index < 3; index++ {
		if err := store.Expose(t.Context(), []RevisionRef{{ID: record.ID, Revision: 1}}, "consumer", "automatic"); err != nil {
			t.Fatal(err)
		}
	}
	after, err := store.Recall(t.Context(), RecallQuery{Text: "Stale advice"})
	if err != nil || len(after) != 1 || after[0].Score > before[0].Score {
		t.Fatalf("exposure increased rank: before=%+v after=%+v err=%v", before, after, err)
	}
	archived, err := store.DecayArchive(t.Context(), 45*24*time.Hour)
	if err != nil || archived != 1 {
		t.Fatalf("exposure prevented inactivity archive: %d %v", archived, err)
	}
}

func TestConsolidationRejectsStaleAndScopedContradictions(t *testing.T) {
	store := openTestStore(t)
	first, err := store.Remember(t.Context(), Record{Title: "First", Content: "Original", AppliesWhen: "Linux"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Remember(t.Context(), Record{Title: "Second", Content: "Original", AppliesWhen: "Windows"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Update(t.Context(), first.ID, Record{Revision: 1, Content: "User correction"}, ActorUser, "s2"); err != nil {
		t.Fatal(err)
	}
	applied, err := store.ApplyConsolidation(t.Context(), []ConsolidationOp{
		{Op: "rewrite", ID: first.ID, Content: "late model rewrite", ExpectedRevisions: map[string]int64{first.ID: 1}},
		{Op: "merge", IDs: []string{first.ID, second.ID}, Title: "Merged", Content: "Original", ExpectedRevisions: map[string]int64{first.ID: 2, second.ID: 1}},
		{Op: "supersede", ID: first.ID, By: second.ID, ExpectedRevisions: map[string]int64{first.ID: 2, second.ID: 1}},
	}, "curator")
	if err != nil || applied != 0 {
		t.Fatalf("unsafe consolidation applied=%d err=%v", applied, err)
	}
	got, _ := store.Get(t.Context(), first.ID)
	if got.Content != "User correction" || got.Status != StatusActive {
		t.Fatalf("consolidation replaced corrected advice: %+v", got)
	}
	long := strings.Repeat("complete-evidence-context ", 200)
	complete, err := store.Remember(t.Context(), Record{Title: "Complete candidate", Content: long})
	if err != nil {
		t.Fatal(err)
	}
	if rendered := rootprompt.MemoryConsolidationUser(PrepareConsolidationInput([]Record{complete})); !strings.Contains(rendered, complete.Content) || !strings.Contains(rendered, `"Revision":1`) {
		t.Fatal("consolidator received truncated record or no revision")
	}
}

func TestRedactedBoundedObservationsRetainOrigin(t *testing.T) {
	store := openTestStore(t)
	observation := Observation{Evidence: Evidence{SessionID: "redacted", MessageID: "human", Kind: EvidenceUser,
		Summary: `Use API_KEY="do-not-store-me" and password=hunter2`, Outcome: OutcomeUnmeasured}, Turn: 1}
	if err := store.RecordTurn(t.Context(), TurnRecord{SessionID: "redacted", Turn: 1, UserText: observation.Summary, Observations: []Observation{observation}}); err != nil {
		t.Fatal(err)
	}
	observations, err := store.ObservationsForSession(t.Context(), "redacted", 0, 1)
	if err != nil || len(observations) != 1 || strings.Contains(observations[0].Summary, "hunter2") || strings.Contains(observations[0].Summary, "do-not-store-me") || observations[0].MessageID != "human" {
		t.Fatalf("unsafe or untraceable observation: %+v %v", observations, err)
	}
	rendered := rootprompt.MemoryExtractionUser(PrepareExtractionInput("", "", observation.Summary, observations))
	if strings.Contains(rendered, "hunter2") || !strings.Contains(rendered, `"message_id":"human"`) {
		t.Fatalf("unsafe extraction prompt: %s", rendered)
	}
	if _, err := store.Remember(t.Context(), Record{Title: "Credential", Content: "password=hunter2"}); err == nil {
		t.Fatal("secret persisted in knowledge")
	}
}
