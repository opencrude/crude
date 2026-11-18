package collaboration

import (
	"errors"
	"reflect"
	"testing"
)

func reportContractRun(t *testing.T, store *Store, agent Agent, id, requestID string) Run {
	t.Helper()
	run, err := store.StartRun(t.Context(), StartRunRequest{
		ID: id, AgentID: agent.ID, RuntimeEpoch: agent.RuntimeEpoch,
		RequestID: requestID, Origin: OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	return run
}

func TestReportContractProvenanceAndFinalPublication(t *testing.T) {
	store, database := openTestStore(t)
	root, child := registerTestAgents(t, store)
	sibling, err := store.RegisterAgent(t.Context(), AgentRegistration{
		ID: "report-sibling", Name: "Sibling", SessionID: "sibling-session",
		ParentID: root.ID, Category: "crafter", OwnerInstance: "process-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	run := reportContractRun(t, store, child, "report-child-run", "report-work-request")
	siblingRun := reportContractRun(t, store, sibling, "report-sibling-run", "sibling-work-request")
	if _, err := store.Publish(t.Context(), PublishRequest{
		SenderID: root.ID, RecipientIDs: []string{child.ID}, IdempotencyKey: "report-assignment-request",
		Origin: OriginPeer, Kind: EventWorkRequest, RequestID: "report-assignment", Body: "Gather report evidence.",
	}); err != nil {
		t.Fatal(err)
	}
	base := PublishReportRequest{
		ArtifactID: "report-child", AuthorID: child.ID, IdempotencyKey: "report-interim",
		RecipientIDs: []string{root.ID}, RunID: run.ID, RequestID: "report-assignment",
		Kind: ReportInterim, Summary: "Evidence gathered", Body: "Original immutable bytes",
	}
	first, err := store.PublishReport(t.Context(), base)
	if err != nil {
		t.Fatal(err)
	}
	revision := base
	revision.IdempotencyKey = "report-final"
	revision.Kind = ReportFinal
	revision.Summary = "Final evidence"
	revision.Body = "Final immutable bytes"
	revision.SupersedesRevision = 1

	for _, tc := range []struct {
		name   string
		change func(*PublishReportRequest)
		want   error
	}{
		{"foreign author", func(r *PublishReportRequest) {
			r.AuthorID, r.RunID, r.RequestID = sibling.ID, siblingRun.ID, ""
		}, ErrConflict},
		{"request rebound", func(r *PublishReportRequest) {
			r.RequestID = "other-request"
		}, ErrConflict},
		{"missing parent recipient", func(r *PublishReportRequest) {
			r.RecipientIDs = []string{sibling.ID}
		}, ErrInvalid},
		{"missing child run", func(r *PublishReportRequest) {
			r.RunID, r.RequestID = "", ""
		}, ErrInvalid},
		{"stale revision", func(r *PublishReportRequest) {
			r.SupersedesRevision = 0
		}, ErrStale},
	} {
		t.Run(tc.name, func(t *testing.T) {
			attempt := revision
			attempt.IdempotencyKey = "rejected-" + tc.name
			tc.change(&attempt)
			if _, err := store.PublishReport(t.Context(), attempt); !errors.Is(err, tc.want) {
				t.Fatalf("publication error = %v, want %v", err, tc.want)
			}
		})
	}

	final, err := store.PublishReport(t.Context(), revision)
	if err != nil || final.Artifact.Revision != 2 || final.Artifact.Kind != ReportFinal {
		t.Fatalf("interim to final = %+v, %v", final, err)
	}
	// A run's launch request and the optional report request are not the same
	// identity; the final deliverable may omit the latter entirely.
	siblingFinal, err := store.PublishReport(t.Context(), PublishReportRequest{
		ArtifactID: "sibling-final", AuthorID: sibling.ID, IdempotencyKey: "sibling-final-call",
		RecipientIDs: []string{root.ID, child.ID}, RunID: siblingRun.ID,
		Kind: ReportFinal, Summary: "Sibling final", Body: "Sibling evidence",
	})
	if err != nil || siblingFinal.Artifact.RequestID != "" || len(siblingFinal.Publish.Deliveries) != 2 {
		t.Fatalf("optional request and additional recipient = %+v, %v", siblingFinal, err)
	}
	duplicate := revision
	duplicate.ArtifactID, duplicate.SupersedesRevision = "another-child-final", 0
	duplicate.IdempotencyKey = "report-duplicate-final"
	if _, err := store.PublishReport(t.Context(), duplicate); !errors.Is(err, ErrConflict) {
		t.Fatalf("second child final artifact = %v", err)
	}
	downgrade := revision
	downgrade.IdempotencyKey, downgrade.SupersedesRevision, downgrade.Kind = "report-downgrade", 2, ReportInterim
	if _, err := store.PublishReport(t.Context(), downgrade); !errors.Is(err, ErrConflict) {
		t.Fatalf("final downgrade = %v", err)
	}
	correction := revision
	correction.IdempotencyKey, correction.SupersedesRevision, correction.Body = "report-correction", 2, "Corrected immutable bytes"
	corrected, err := store.PublishReport(t.Context(), correction)
	if err != nil || corrected.Artifact.Revision != 3 {
		t.Fatalf("same-artifact final correction = %+v, %v", corrected, err)
	}
	for _, prior := range []Artifact{first.Artifact, final.Artifact} {
		stored, err := store.Artifact(t.Context(), prior.ID, prior.Revision)
		if err != nil || !reflect.DeepEqual(stored, prior) {
			t.Fatalf("previous revision %d changed: %+v, %v", prior.Revision, stored, err)
		}
	}
	var count int
	if err := database.QueryRow(`SELECT COUNT(*) FROM collaboration_artifacts WHERE id=?`, first.Artifact.ID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("artifact count = %d, %v", count, err)
	}
	if err := database.QueryRow(`SELECT COUNT(*) FROM collaboration_events WHERE kind=? AND run_id=?`, EventReportPublished, run.ID).Scan(&count); err != nil || count != 3 {
		t.Fatalf("report event count = %d, %v", count, err)
	}

	finished, err := store.FinishRun(t.Context(), FinishRunRequest{
		RunID: run.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch,
		ExpectedVersion: run.Version, State: RunSucceeded,
	})
	if err != nil || !finished.State.Terminal() {
		t.Fatalf("finish run = %+v, %v", finished, err)
	}
	retry, err := store.PublishReport(t.Context(), revision)
	if err != nil || !retry.Publish.Duplicate || retry.Artifact.Revision != 2 ||
		retry.Publish.Event.ID != final.Publish.Event.ID {
		t.Fatalf("identical retry after terminal run = %+v, %v", retry, err)
	}
	if _, err := store.PublishReport(t.Context(), PublishReportRequest{
		ArtifactID: "late-artifact", AuthorID: child.ID, IdempotencyKey: "late-new",
		RecipientIDs: []string{root.ID}, RunID: run.ID, RequestID: run.RequestID,
		Kind: ReportFinal, Summary: "Late", Body: "Late",
	}); !errors.Is(err, ErrStale) {
		t.Fatalf("new report for finished run = %v", err)
	}
}

func TestReportContractCannotRebindRunOrPublishFromStaleEpoch(t *testing.T) {
	store, _ := openTestStore(t)
	root, child := registerTestAgents(t, store)
	firstRun := reportContractRun(t, store, child, "report-first-run", "first-request")
	if _, err := store.Publish(t.Context(), PublishRequest{
		SenderID: root.ID, RecipientIDs: []string{child.ID}, IdempotencyKey: "first-assignment-request",
		Origin: OriginPeer, Kind: EventWorkRequest, RequestID: "assignment-first", Body: "Gather the first report.",
	}); err != nil {
		t.Fatal(err)
	}
	initial := PublishReportRequest{
		ArtifactID: "report-run-bound", AuthorID: child.ID, IdempotencyKey: "run-first-report",
		RecipientIDs: []string{root.ID}, RunID: firstRun.ID, RequestID: "assignment-first",
		Kind: ReportInterim, Summary: "First run", Body: "Original",
	}
	if _, err := store.PublishReport(t.Context(), initial); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(t.Context(), FinishRunRequest{
		RunID: firstRun.ID, AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch,
		ExpectedVersion: firstRun.Version, State: RunFailed,
	}); err != nil {
		t.Fatal(err)
	}
	secondRun := reportContractRun(t, store, child, "report-second-run", "second-request")
	rebind := initial
	rebind.IdempotencyKey, rebind.SupersedesRevision = "run-rebind", 1
	rebind.RunID = secondRun.ID
	if _, err := store.PublishReport(t.Context(), rebind); !errors.Is(err, ErrConflict) {
		t.Fatalf("run rebound = %v", err)
	}
	if err := store.DetachAgent(t.Context(), child.ID, "process-a", child.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	newReport := rebind
	newReport.ArtifactID, newReport.IdempotencyKey, newReport.SupersedesRevision = "report-detached", "detached-call", 0
	if _, err := store.PublishReport(t.Context(), newReport); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("detached author publication = %v", err)
	}
	retry, err := store.PublishReport(t.Context(), initial)
	if err != nil || !retry.Publish.Duplicate {
		t.Fatalf("accepted report retry after detach = %+v, %v", retry, err)
	}
}
