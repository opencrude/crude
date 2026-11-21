package session

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/agent/worktree"
	"crude/logger"
	"crude/types"
	"crude/types/command"
)

func investigationEvidenceCheckout(t *testing.T, s *Session, service ExecutionService, source string) (worktree.Context, string) {
	t.Helper()
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	cycle, err := s.planInvestigationCycle(t.Context())
	if err != nil || cycle == "" {
		t.Fatalf("planning cycle: %q, %v", cycle, err)
	}
	c, err := service.Create(t.Context(), worktree.Request{
		Owner: worktree.Owner{AgentID: "planner", RunID: "experiment-run"},
		Mode:  worktree.Isolated, SourceContextID: source,
		Investigation: worktree.Investigation{SessionID: s.sessionID, Cycle: cycle},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.savePlanInvestigations(t.Context(), []planInvestigationBinding{{Cycle: cycle, SourceID: source, ID: c.ID}}); err != nil {
		t.Fatal(err)
	}
	// Tool routing receives the compact checkout, not a copy of every
	// baseline file. Its measured baseline must still be the accepted bytes.
	c.Baseline, c.DocumentRevisions = nil, nil
	return c, cycle
}

func startEvidenceFixtureCommand(t *testing.T, s *Session, c worktree.Context, generation uint64, argv ...string) func(command.Result) error {
	t.Helper()
	ctx := withPlanInvestigationCycle(t.Context(), c.Investigation.Cycle)
	finish, err := s.beginInvestigationEvidence(ctx, generation, c, c.Root, argv, strings.Join(argv, " "))
	if err != nil {
		t.Fatal(err)
	}
	return finish
}

func readEvidenceFixture(t *testing.T, s *Session, revision int, ref types.PlanEvidenceRef) string {
	t.Helper()
	text, err := s.ReadPlanEvidence(revision, ref.Digest)
	if err != nil || evidenceDigest([]byte(text)) != ref.Digest {
		t.Fatalf("read verified evidence: %v, digest %q for %d bytes", err, ref.Digest, len(text))
	}
	return text
}

func TestInvestigationEvidenceBindsExactRecordAndSurvivesFork(t *testing.T) {
	s, service, selected := executionAdmissionFixture(t)
	s.Retain()
	const plan = "# Repair baseline"
	var source strings.Builder
	for i := range 3200 {
		fmt.Fprintf(&source, "case %07d: return fmt.Errorf(\"baseline case %d\")\n", i, i)
	}
	baseline := source.String()
	changed := strings.Replace(baseline, `return fmt.Errorf("baseline case 2400")`, `return fmt.Errorf("investigation proof")`, 1)
	if err := os.WriteFile(filepath.Join(selected.Root, "baseline.txt"), []byte(baseline), 0600); err != nil {
		t.Fatal(err)
	}
	c, cycle := investigationEvidenceCheckout(t, s, service, selected.ID)
	if err := os.WriteFile(filepath.Join(c.Root, "baseline.txt"), []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	process := exec.Command("git", "--version")
	process.Dir = c.Root
	finish := startEvidenceFixtureCommand(t, s, c, 1, process.Args...)
	if err := s.recordProposedPlan(1, planReply(plan), cycle, selected.ID); err != nil {
		t.Fatal(err)
	}
	pending := latestReview(t, s).Evidence
	if len(pending) != 1 || pending[0].Status != types.PlanEvidenceInconclusive {
		t.Fatalf("prepared command without exit fact: %+v", pending)
	}
	first := readEvidenceFixture(t, s, 1, pending[0])
	if !strings.Contains(first, "No terminal process result") || strings.Contains(first, "Exit code:") {
		t.Fatal("pending evidence claimed a process exit")
	}
	output, err := process.CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if err := finish(command.Result{ExitCode: new(process.ProcessState.ExitCode()), Output: string(output),
		OutputDescriptor: command.OutputDescriptor{Status: command.Completed, TotalBytes: len(output)}}); err != nil {
		t.Fatal(err)
	}
	if got := readEvidenceFixture(t, s, 1, pending[0]); got != first {
		t.Fatal("settlement changed evidence already attached to revision 1")
	}
	if err := s.recordProposedPlan(2, planReply(plan), cycle, "another-source"); err != nil || len(latestReview(t, s).Evidence) != 0 {
		t.Fatalf("unrelated sources inherited evidence: %v", err)
	}
	if err := s.recordProposedPlan(3, planReply(plan), cycle, selected.ID); err != nil {
		t.Fatal(err)
	}
	revision := latestReview(t, s)
	if len(revision.Evidence) != 1 || revision.Evidence[0].Status != types.PlanEvidencePassed {
		t.Fatalf("verified exit and unchanged measured input: %+v", revision.Evidence)
	}
	ref := revision.Evidence[0]
	revision.Evidence[0].Digest = "not a digest"
	text := readEvidenceFixture(t, s, 3, ref)
	for _, expected := range []string{c.BaselineDigest, c.BaseCommit, `Argv: "git" "--version"`, "git version ", "investigation proof", "match the before snapshot"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("verified evidence lacks %q", expected)
		}
	}
	verifyInvestigationArchive(t, text, baseline, changed)
	assertPlanFile(t, filepath.Join(selected.Root, "baseline.txt"), baseline)
	if _, err := s.ReadPlanEvidence(1, ref.Digest); err == nil {
		t.Fatal("revision 1 accepted a reference from a different revision")
	}
	path, err := s.OpenPlanEvidence(3, ref.Digest)
	if err != nil || filepath.Base(path) != ref.Digest+".md" {
		t.Fatalf("materialize exact evidence: %q, %v", path, err)
	}
	assertPlanFile(t, path, text)

	forkID := logger.NewSessionID()
	dir := filepath.Dir(s.dbPath)
	if err := s.Fork(dir, forkID); err != nil {
		t.Fatal(err)
	}
	fork, err := ResumeSessionWithDir(filepath.Join(dir, forkID+".db"), testConfig(), s.nameHome, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fork.Close)
	fork.Retain()
	if got := readEvidenceFixture(t, fork, 3, ref); got != text {
		t.Fatal("fork did not keep the immutable evidence")
	}
	forkPath, err := fork.OpenPlanEvidence(3, ref.Digest)
	if err != nil || forkPath == path {
		t.Fatalf("fork materialization did not use its own folder: %q, %v", forkPath, err)
	}
	assertPlanFile(t, forkPath, text)
	if err := fork.recordProposedPlan(4, planReply(plan), cycle, selected.ID); err != nil || len(latestReview(t, fork).Evidence) != 0 {
		t.Fatalf("fork borrowed the source's unapproved evidence journal: %v", err)
	}

	destination, sender := reviewSessionFixture(t)
	settle := types.PlanReviewSettle{Revision: 3, Text: plan + "\n\nApproved edits.", Action: types.PlanReviewNewSession,
		DestinationSessionID: destination.sessionID}
	send := func() (types.PlanReviewSettlement, error) {
		return s.SettlePlanReviewForAgent(t.Context(), "", settle, provider.UserInput{}, destination)
	}
	destinationPath := filepath.Join(destination.SessionDir(), "plan-evidence", ref.Digest+".md")
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destinationPath, []byte("changed evidence"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(destinationPath, 0400); err != nil {
		t.Fatal(err)
	}
	if _, err := send(); err != errPlanEvidenceChanged || !latestReview(t, s).Open() {
		t.Fatalf("changed handoff evidence should keep revision open: %v", err)
	}
	if got := readEvidenceFixture(t, s, 3, ref); got != text {
		t.Fatal("viewer used the modified handoff file instead of the record")
	}
	if err := os.Chmod(destinationPath, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(destinationPath); err != nil {
		t.Fatal(err)
	}
	if _, err := destination.DB().Exec(`CREATE TRIGGER reject_plan_input BEFORE INSERT ON session_runtime_state
		WHEN NEW.key = 'plan_handoff_receipt' BEGIN SELECT RAISE(ABORT, 'destination storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := send(); err != errPlanHandoffSend {
		t.Fatalf("destination refusal: %v", err)
	}
	stillOpen := latestReview(t, s)
	if unchanged, err := s.planInvestigationCycle(t.Context()); err != nil || unchanged != cycle || !s.PlanMode() ||
		!stillOpen.Open() || stillOpen.NewSessionID != "" || len(stillOpen.Evidence) != 1 || stillOpen.Evidence[0] != ref {
		t.Fatalf("failed input changed the source review or cycle: %+v, %q, %v", stillOpen, unchanged, err)
	}
	if raw, err := sessiondb.LoadRuntimeState(t.Context(), destination.DB(), queuedInputsStateKey); err != nil || len(raw) != 0 {
		t.Fatalf("failed receipt left a queued first input: %s, %v", raw, err)
	}
	if _, err := destination.DB().Exec(`DROP TRIGGER reject_plan_input`); err != nil {
		t.Fatal(err)
	}
	handoff, err := send()
	if err != nil || strings.Contains(handoff.Text, pending[0].Digest) || strings.Contains(handoff.Text, c.Root) ||
		!strings.Contains(handoff.Text, destinationPath) || strings.Contains(handoff.Text, path) ||
		!strings.Contains(handoff.Text, "Approved edits.") || strings.Contains(handoff.Display, destinationPath) {
		t.Fatalf("wrong exact evidence handoff: %+v, %v", handoff, err)
	}
	assertPlanFile(t, destinationPath, text)
	nextCycle, err := s.planInvestigationCycle(t.Context())
	if err != nil || !s.PlanMode() || nextCycle == "" || nextCycle == cycle || latestReview(t, s).Outcome != types.PlanOutcomeNewSession {
		t.Fatalf("fresh handoff did not close just the approved cycle: %q, %v", nextCycle, err)
	}
	destAgent := destination.liveOrchestrator().ID
	waitTurnDone(t, sender, destAgent)
	destination.msgsMu.Lock()
	defer destination.msgsMu.Unlock()
	var users int
	for i, message := range destination.msgs {
		if message.IsGenuineUserInput() {
			users++
			if message.ContentString() != handoff.Text || destination.displayMetas[i].DisplayContent != handoff.Display || destination.PlanMode() {
				t.Fatalf("destination did not receive the approved evidence and its separate display: %+v", message)
			}
		}
	}
	if users != 1 {
		t.Fatalf("destination received %d user messages, want exactly one", users)
	}
}

func verifyInvestigationArchive(t *testing.T, text, before, after string) {
	t.Helper()
	_, block, found := strings.Cut(text, "```text\n")
	if !found {
		t.Fatal("large edit has no exact archived data")
	}
	encoded, _, _ := strings.Cut(block, "\n```")
	zipped, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	reader, err := gzip.NewReader(bytes.NewReader(zipped))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	var changes []worktree.Change
	if err := json.NewDecoder(reader).Decode(&changes); err != nil || len(changes) != 1 {
		t.Fatalf("decode exact changes: %d, %v", len(changes), err)
	}
	if change := changes[0]; change.Path != "baseline.txt" || change.Before == nil || change.After == nil ||
		string(change.Before.Data) != before || string(change.After.Data) != after {
		t.Fatal("archive lost measured baseline or exact edited bytes")
	}
}

func TestInvestigationEvidenceBoundsAndDistinguishesInconclusiveResults(t *testing.T) {
	s, service, _ := executionAdmissionFixture(t)
	c, cycle := investigationEvidenceCheckout(t, s, service, "")
	const plan = "# Evaluate fixture"
	for i := range maxEvidenceCommands + 1 {
		finish := startEvidenceFixtureCommand(t, s, c, uint64(i+1), "fixture", fmt.Sprint(i))
		if err := finish(command.Result{Cancelled: true, OutputDescriptor: command.OutputDescriptor{Status: command.Killed}}); err != nil {
			t.Fatal(err)
		}
	}
	fail := startEvidenceFixtureCommand(t, s, c, 30, "fixture", "exit 7")
	const noisy = "First useful output.\n"
	if err := fail(command.Result{ExitCode: new(7), Output: noisy + strings.Repeat("!", 100<<10),
		OutputDescriptor: command.OutputDescriptor{Status: command.Failed, TotalBytes: 100<<10 + len(noisy)}}); err != nil {
		t.Fatal(err)
	}
	changed := startEvidenceFixtureCommand(t, s, c, 30, "fixture", "mutating")
	if err := os.WriteFile(filepath.Join(c.Root, "baseline.txt"), []byte("changed during command"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := changed(command.Result{ExitCode: new(7), OutputDescriptor: command.OutputDescriptor{Status: command.Failed}}); err == nil {
		t.Fatal("nonzero exit established a result despite changed files")
	}
	if err := s.recordProposedPlan(30, planReply(plan), cycle, ""); err != nil {
		t.Fatal(err)
	}
	revision := latestReview(t, s)
	refs := revision.Evidence
	if len(refs) != maxEvidenceCommands || revision.EvidenceOmitted != 3 {
		t.Fatalf("unbounded projection or missing omission: %+v", revision)
	}
	if refs[0].Status != types.PlanEvidenceInterrupted ||
		refs[len(refs)-2].Status != types.PlanEvidenceFailed ||
		refs[len(refs)-1].Status != types.PlanEvidenceInconclusive {
		t.Fatalf("command statuses: %+v", refs)
	}
	failed := readEvidenceFixture(t, s, 1, refs[len(refs)-2])
	if !strings.Contains(failed, noisy[:len(noisy)-1]) || !strings.Contains(failed, "Evidence excerpt") || len(failed) > 14<<10 {
		t.Fatalf("failed command output excerpt missing or unbounded: %d bytes", len(failed))
	}
	text := readEvidenceFixture(t, s, 1, refs[len(refs)-1])
	if !strings.Contains(text, "Exit code: 7") || !strings.Contains(text, `Bytes: "changed during command"`) {
		t.Fatal("inconclusive evidence lost actual exit or verified changed bytes")
	}
}

func TestInvestigationEvidenceReachesInSessionImplementationInput(t *testing.T) {
	const plan = "# Repair in this session"
	requests := make(chan []provider.ChatMessage, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct{ Messages []provider.ChatMessage }
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests <- body.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	s, err := NewWithSessionDir("", mediaAttributionConfig(server.URL, "review-test-model", "openai"), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	source, service, _ := executionAdmissionFixture(t)
	s.SetWorkDir(source.WorkDir())
	s.SetExecutionService(service)
	sender := newMockEventSender()
	root, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	c, cycle := investigationEvidenceCheckout(t, s, service, "")
	finish := startEvidenceFixtureCommand(t, s, c, 1, "fixture", "check")
	if err := finish(command.Result{ExitCode: new(0), OutputDescriptor: command.OutputDescriptor{Status: command.Completed}}); err != nil {
		t.Fatal(err)
	}
	if err := s.recordProposedPlan(1, planReply(plan), cycle, ""); err != nil {
		t.Fatal(err)
	}
	revision := latestReview(t, s)
	if len(revision.Evidence) != 1 {
		t.Fatalf("approval revision evidence: %+v", revision)
	}
	settle := types.PlanReviewSettle{Revision: 1, Text: plan, Action: types.PlanReviewImplement}
	if !s.planInvestigationToolMu.TryLock() {
		t.Fatal("investigation operation unexpectedly active")
	}
	_, busyErr := s.SettlePlanReview(settle)
	s.planInvestigationToolMu.Unlock()
	if busyErr == nil || !latestReview(t, s).Open() || !s.PlanMode() {
		t.Fatalf("approval overtook command settlement: %v", busyErr)
	}
	out, err := s.SettlePlanReview(settle)
	if err != nil || s.PlanMode() || latestReview(t, s).Outcome != types.PlanOutcomeImplemented {
		t.Fatalf("implementation did not close exact approval: %v", err)
	}
	if !strings.Contains(out.Text, revision.Evidence[0].Digest) || strings.Contains(out.Text, c.Root) {
		t.Fatalf("implementation received the wrong handoff: %q", out.Text)
	}
	waitTurnDone(t, sender, root.ID)
	select {
	case messages := <-requests:
		var sawHandoff bool
		for _, msg := range messages {
			if msg.Role == "user" && strings.Contains(msg.ContentString(), out.Text) {
				sawHandoff = true
			}
		}
		if !sawHandoff {
			t.Fatal("implementation provider request omitted approved evidence")
		}
	default:
		t.Fatal("implementation did not reach its provider")
	}
	rows, err := sessiondb.LoadMessagesWithMeta(s.db)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Msg.Role == "user" && strings.Contains(row.Msg.ContentString(), out.Text) {
			if len(row.Meta.Snapshots) != 1 || row.Meta.Snapshots[0].Content != out.Display {
				t.Fatalf("transcript exposed raw references instead of approval summary: %+v", row.Meta.Snapshots)
			}
			return
		}
	}
	t.Fatal("in-session approval did not dispatch the durable handoff text")
}
