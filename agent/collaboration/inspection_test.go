package collaboration

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestInspectAgentDistinguishesIdleHistoryAndCurrentRunWithoutEffects(t *testing.T) {
	store, database := openTestStore(t)
	root, child := registerTestAgents(t, store)
	start := func(id string) Run {
		t.Helper()
		run, err := store.StartRun(t.Context(), StartRunRequest{ID: id, AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch, Origin: OriginPeer})
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	finish := func(run Run) {
		t.Helper()
		if _, err := store.FinishRun(t.Context(), FinishRunRequest{RunID: run.ID, AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch, ExpectedVersion: run.Version, State: RunSucceeded, Outcome: "completed"}); err != nil {
			t.Fatal(err)
		}
	}
	old := start("old-run")
	if _, err := store.PublishReport(t.Context(), PublishReportRequest{AuthorID: root.ID, IdempotencyKey: "old-report", RecipientIDs: []string{child.ID}, RunID: old.ID, Kind: ReportAnswer, Summary: "stale summary", Body: strings.Repeat("stale large body", 20_000)}); err != nil {
		t.Fatal(err)
	}
	finish(old)
	latest := start("latest-run")
	report, err := store.PublishReport(t.Context(), PublishReportRequest{AuthorID: root.ID, IdempotencyKey: "latest-report", RecipientIDs: []string{child.ID}, RunID: latest.ID, Kind: ReportAnswer, Summary: "latest evidence", Body: "PRIVATE_FULL_REPORT_BODY"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.PublishReport(t.Context(), PublishReportRequest{ArtifactID: report.Artifact.ID, AuthorID: root.ID, IdempotencyKey: "latest-correction", RecipientIDs: []string{child.ID}, RunID: latest.ID, Kind: ReportAnswer, Summary: "corrected evidence", Body: "PRIVATE_FULL_REPORT_BODY", SupersedesRevision: 1}); err != nil {
		t.Fatal(err)
	}
	pending, err := store.Publish(t.Context(), PublishRequest{SenderID: child.ID, RecipientIDs: []string{root.ID}, Origin: OriginRuntime, Kind: EventPeerNote, IdempotencyKey: "still-pending", Body: "the exact notice"})
	if err != nil {
		t.Fatal(err)
	}
	finish(latest)
	before, err := store.Agent(t.Context(), root.ID)
	if err != nil {
		t.Fatal(err)
	}
	var sequence int64
	if err := database.QueryRow(`SELECT last_sequence FROM collaboration_state`).Scan(&sequence); err != nil {
		t.Fatal(err)
	}
	view, err := store.InspectAgent(t.Context(), root.ID)
	if err != nil || view.Run != nil || view.LatestRun == nil || view.LatestRun.ID != latest.ID || view.LatestRun.State != RunSucceeded {
		t.Fatalf("idle/history confused: %+v %v", view, err)
	}
	if len(view.Reports) != 1 || view.Reports[0].ID != report.Artifact.ID || view.Reports[0].Revision != 2 || view.Reports[0].Summary != "corrected evidence" {
		t.Fatalf("stale or duplicate report: %+v", view.Reports)
	}
	encoded, _ := json.Marshal(view)
	if strings.Contains(string(encoded), "PRIVATE_FULL_REPORT_BODY") || strings.Contains(string(encoded), "stale summary") || len(encoded) > 5000 {
		t.Fatal("inspection dumped unrelated/private report content", len(encoded))
	}
	event, err := store.InspectionEvent(t.Context(), root.ID, pending.Event.ID)
	if err != nil || event == nil || event.ID != pending.Event.ID || event.Origin != OriginRuntime || event.Body != "the exact notice" {
		t.Fatal("trigger evidence not inspectable", event, err)
	}
	foreign, err := store.InspectionEvent(t.Context(), child.ID, pending.Event.ID)
	if err != nil || foreign != nil {
		t.Fatal("unaddressed event misattributed as trigger", foreign, err)
	}
	after, err := store.Agent(t.Context(), root.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("inspection changed attachment/lifecycle", err)
	}
	var state string
	if err := database.QueryRow(`SELECT state FROM collaboration_deliveries WHERE id=?`, pending.Deliveries[0].ID).Scan(&state); err != nil || state != "pending" {
		t.Fatal("inspection consumed a notification", state, err)
	}
	var afterSequence int64
	if err := database.QueryRow(`SELECT last_sequence FROM collaboration_state`).Scan(&afterSequence); err != nil || sequence != afterSequence {
		t.Fatal("inspection published an event", err)
	}
	active := start("replacement-run")
	view, err = store.InspectAgent(t.Context(), root.ID)
	if err != nil || view.Run == nil || view.Run.ID != active.ID || view.Run.Version != active.Version || view.LatestRun.ID != active.ID || len(view.Reports) != 0 {
		t.Fatal("new attempt inherited historical status or reports", view, err)
	}
	finish(active)
}

func TestInspectAgentWithoutRunsDoesNotInventWork(t *testing.T) {
	store, _ := openTestStore(t)
	root, _ := registerTestAgents(t, store)
	view, err := store.InspectAgent(t.Context(), root.ID)
	if err != nil || view.Run != nil || view.LatestRun != nil || len(view.Reports) != 0 {
		t.Fatal("unstarted agent gained an execution", view, err)
	}
	encoded, _ := json.Marshal(view)
	if !strings.Contains(string(encoded), `"run":null`) || !strings.Contains(string(encoded), `"latest_run":null`) {
		t.Fatal("absence of work is not explicit", string(encoded))
	}
}
