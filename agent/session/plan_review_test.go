package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/logger"
	"crude/text/anchor"
	"crude/types"
	"crude/types/control"
)

func planReply(plan string) string {
	return "Here is the plan.\n\n<proposed_plan>\n" + plan + "\n</proposed_plan>"
}

func latestReview(t *testing.T, s *Session) types.PlanRevision {
	t.Helper()
	review, _ := s.PlanReview()
	revision, ok := review.Latest()
	if !ok {
		t.Fatal("plan review is missing")
	}
	return revision
}

func TestPlanReviewRootCompletionUsesStartingMode(t *testing.T) {
	requests := make(chan string, 4)
	replies := make(chan string)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body planModeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body.text(0)
		select {
		case answer := <-replies:
			frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{
				"delta": map[string]string{"content": answer}, "finish_reason": "stop",
			}}})
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(server.Close)
	s, err := NewWithSessionDir("", mediaAttributionConfig(server.URL, "plan-review-model", "openai"), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	sender := newMockEventSender()
	o, err := s.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	start := func(planning bool) {
		t.Helper()
		if !o.SubmitInput(provider.PlainInput("Tell me the next step.")) {
			t.Fatal("input rejected")
		}
		select {
		case system := <-requests:
			if strings.Contains(system, "<plan_mode>") != planning {
				t.Fatalf("provider did not get requested mode: plan mode want %v", planning)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no provider request")
		}
	}
	respond := func(reply string) types.TurnIdentity {
		t.Helper()
		select {
		case replies <- reply:
		case <-time.After(5 * time.Second):
			t.Fatal("provider did not accept reply")
		}
		return waitTurnDone(t, sender, o.ID)
	}

	start(false)
	respond(planReply("# Not a Plan mode proposal"))
	if _, ok := s.PlanReview(); ok {
		t.Fatal("a normal turn created a review")
	}
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	start(true)
	respond("I still need to inspect the work.")
	if _, ok := s.PlanReview(); ok {
		t.Fatal("a turn without a proposal created a review")
	}

	const proposal = "# Update UI + Tests 2\n\nUse the existing review."
	start(true)
	if err := s.SetPlanMode(false); err != nil {
		t.Fatal(err)
	}
	firstTurn := respond(planReply(proposal))
	first := latestReview(t, s) // Done already crossed the real runtime event boundary.
	if first.Generation != firstTurn.Generation || first.Number != 1 || !first.Open() ||
		first.Title != "Update UI + Tests 2" || first.CreatedAt.IsZero() ||
		first.Path != filepath.Join(s.SessionDir(), "plans", "update-ui-tests-2-v1.md") {
		t.Fatalf("wrong plan revision at Done: %+v, turn=%+v", first, firstTurn)
	}
	assertPlanFile(t, first.Path, proposal+"\n")

	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	start(true)
	if stopped, _ := o.CancelTurn(); !stopped {
		t.Fatal("could not stop the active Plan mode turn")
	}
	waitTurnDone(t, sender, o.ID)
	if got := latestReview(t, s); got.Number != 1 || !got.Open() {
		t.Fatalf("stopped turn changed review: %+v", got)
	}

	start(true)
	nextTurn := respond(planReply("# 第二"))
	review, _ := s.PlanReview()
	next, _ := review.Latest()
	prior, _ := review.Revision(1)
	if len(review.Revisions) != 2 || next.Number != 2 || next.Generation != nextTurn.Generation ||
		next.Path != filepath.Join(s.SessionDir(), "plans", "plan-v2.md") || prior.Outcome != types.PlanOutcomeReplaced {
		t.Fatalf("second proposal did not replace the open revision: %+v", review)
	}
	assertPlanFile(t, next.Path, "# 第二\n")
}

func TestPlanReviewChildProposalDoesNotCreateRootRevision(t *testing.T) {
	h := newChildWireHarness(t, CategoryConfig{AccountID: "account", Model: wireChildModel})
	child := h.launch(map[string]any{})
	call := h.next(h.children, "child ready")
	if err := h.session.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	call.reply <- map[string]any{"tool_calls": []any{wireTool("report", "publish_report", map[string]any{
		"recipient_ids": []string{h.root.ID}, "kind": "final", "summary": "Done.", "body": "Done.",
	})}}
	last := h.next(h.children, "child receives its report")
	last.reply <- map[string]any{"content": planReply("# Child work\n\nChange nothing.")}
	joined := make(chan struct{})
	go func() { h.session.SubAgentRunner().Wait(); close(joined) }()
	select {
	case <-joined:
	case <-time.After(5 * time.Second):
		t.Fatal("child did not settle")
	}
	result, ok := h.session.SubAgentRunner().Manager().GetCompleted(agent.AgentID(child))
	if !ok || result.Status != agent.SubAgentCompleted {
		t.Fatalf("child did not complete successfully: %+v", result)
	}
	if _, ok := h.session.PlanReview(); ok {
		t.Fatal("a child reply created a root plan revision")
	}
}

func TestPlanReviewPersistsRelativePathAndRecreatesInFork(t *testing.T) {
	home, dir := t.TempDir(), t.TempDir()
	cfg := testConfig()
	s, err := NewWithSessionDir("", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.Retain()
	const proposal = "# A source plan\n\nDo the work."
	if err := s.recordProposedPlan(1, planReply(proposal), "", ""); err != nil {
		t.Fatal(err)
	}
	edit := types.PlanCommentEdit{Revision: 1, Op: types.PlanCommentAdd, Comment: types.PlanComment{ID: "one", Body: "Keep this."}}
	if err := s.EditPlanComment(edit); err != nil {
		t.Fatal(err)
	}
	view, _ := s.PlanReview()
	revision := latestReview(t, s)
	raw, err := sessiondb.LoadRuntimeState(t.Context(), s.db, "plan_review")
	var persisted struct {
		Revisions []struct{ Path, Proposal string } `json:"revisions"`
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatal(err)
	}
	if len(persisted.Revisions) != 1 || persisted.Revisions[0].Path != "plans/a-source-plan-v1.md" ||
		persisted.Revisions[0].Proposal != proposal {
		t.Fatalf("wrong durable review: %s", raw)
	}
	if runtime.GOOS != "windows" {
		file, err := os.Stat(revision.Path)
		if err != nil || file.Mode().Perm() != 0600 {
			t.Fatalf("plan file permissions: %v, %v", file, err)
		}
		parent, err := os.Stat(filepath.Dir(revision.Path))
		if err != nil || parent.Mode().Perm() != 0700 {
			t.Fatalf("plan directory permissions: %v, %v", parent, err)
		}
	}
	if err := os.WriteFile(revision.Path, []byte("User edits.\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if path, err := s.OpenPlanRevision(1); err != nil || path != revision.Path {
		t.Fatalf("open existing file: %q, %v", path, err)
	}
	assertPlanFile(t, revision.Path, "User edits.\n")
	forkID := logger.NewSessionID()
	if err := s.Fork(dir, forkID); err != nil {
		t.Fatal(err)
	}
	fork, err := ResumeSessionWithDir(filepath.Join(dir, forkID+".db"), cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(fork.Close)
	fork.Retain()
	forkRev := latestReview(t, fork)
	if forkRev.Path == revision.Path || len(forkRev.Comments) != 1 || forkRev.Comments[0].Body != "Keep this." {
		t.Fatalf("wrong fork revision: %+v", forkRev)
	}
	if _, err := os.Stat(forkRev.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fork should initially have no copied file: %v", err)
	}
	if path, err := fork.OpenPlanRevision(1); err != nil || path != forkRev.Path {
		t.Fatalf("fork could not recreate its plan: %q, %v", path, err)
	}
	assertPlanFile(t, forkRev.Path, proposal+"\n")
	assertPlanFile(t, revision.Path, "User edits.\n")

	if err := os.Remove(revision.Path); err != nil {
		t.Fatal(err)
	}
	s.Close()
	resumed, err := ResumeSessionWithDir(s.dbPath, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(resumed.Close)
	resumed.Retain()
	if loaded, _ := resumed.PlanReview(); !reflect.DeepEqual(loaded, view) {
		t.Fatalf("review changed on resume: got %+v want %+v", loaded, view)
	}
	if _, err := resumed.OpenPlanRevision(1); err != nil {
		t.Fatal(err)
	}
	assertPlanFile(t, revision.Path, proposal+"\n")
}

func assertPlanFile(t *testing.T, path, want string) {
	t.Helper()
	if got, err := os.ReadFile(path); err != nil || string(got) != want {
		t.Fatalf("plan file %q: got %q, %v; want %q", path, got, err, want)
	}
}

func reviewFixture(t *testing.T, plan string) (*Session, *mockEventSender) {
	t.Helper()
	s, sender := reviewSessionFixture(t)
	if err := s.SetPlanMode(true); err != nil {
		t.Fatal(err)
	}
	if err := s.recordProposedPlan(1, planReply(plan), "", ""); err != nil {
		t.Fatal(err)
	}
	return s, sender
}

func reviewSessionFixture(t *testing.T) (*Session, *mockEventSender) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Acknowledged.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	s, err := NewWithSessionDir("", mediaAttributionConfig(server.URL, "review-test-model", "openai"), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	s.SetWorkDir(t.TempDir())
	sender := newMockEventSender()
	if _, err := s.NewOrchestrator(sender); err != nil {
		t.Fatal(err)
	}
	return s, sender
}

func TestPlanReviewCommentValidationAndSettlement(t *testing.T) {
	const quote = "Path A\nPath B"
	const proposal = "# Improve tests\n\n" + quote + "\nDelete this sentence."
	s, sender := reviewFixture(t, proposal)
	feedback := func(text string) (types.PlanReviewSettlement, error) {
		return s.SettlePlanReview(types.PlanReviewSettle{Revision: 1, Action: types.PlanReviewFeedback, Text: text})
	}
	if _, err := feedback(proposal + "\n\n"); err == nil || err.Error() != "Add a comment or edit the plan first." {
		t.Fatalf("trailing newlines were counted as edits: %v", err)
	}
	start := strings.Index(proposal, quote)
	edit := types.PlanCommentEdit{Revision: 1, Op: types.PlanCommentAdd, Comment: types.PlanComment{
		ID: "comment", Body: "  Cover this path.  ", Anchor: anchor.Capture(proposal, start, start+len(quote)),
		CreatedAt: time.Unix(1, 0), Sent: true,
	}}
	for _, change := range []func(*types.PlanComment){
		func(c *types.PlanComment) { c.ID = "" },
		func(c *types.PlanComment) { c.ID = strings.Repeat("1", 65) },
		func(c *types.PlanComment) { c.Body = " \t" },
		func(c *types.PlanComment) { c.Body = strings.Repeat("x", types.PlanCommentMaxBody+1) },
		func(c *types.PlanComment) { c.Anchor.Exact = strings.Repeat("x", types.PlanCommentMaxQuote+1) },
	} {
		bad := edit
		change(&bad.Comment)
		if err := s.EditPlanComment(bad); err == nil {
			t.Fatal("invalid new comment accepted")
		}
	}
	if err := s.EditPlanComment(edit); err != nil {
		t.Fatal(err)
	}
	if err := s.EditPlanComment(edit); err != nil {
		t.Fatalf("identical add retry failed: %v", err)
	}
	rev := latestReview(t, s)
	if len(rev.Comments) != 1 || rev.Comments[0].Body != "Cover this path." || rev.Comments[0].Sent ||
		rev.Comments[0].CreatedAt.Equal(edit.Comment.CreatedAt) {
		t.Fatalf("wrong accepted comment: %+v", rev.Comments)
	}
	rev.Comments[0].Body = "injected mutation"
	if got := latestReview(t, s).Comments[0].Body; got != "Cover this path." {
		t.Fatalf("read mutated the canonical comment: %q", got)
	}
	edit.Comment.Body = "Cover both paths."
	if err := s.EditPlanComment(edit); err == nil {
		t.Fatal("different add under same ID accepted")
	}
	edit.Op = types.PlanCommentUpdate
	if err := s.EditPlanComment(edit); err != nil {
		t.Fatal(err)
	}
	deleted := edit
	deleted.Op, deleted.Comment.ID = types.PlanCommentAdd, "delete-me"
	if err := s.EditPlanComment(deleted); err != nil {
		t.Fatal(err)
	}
	deleted.Op = types.PlanCommentDelete
	if err := s.EditPlanComment(deleted); err != nil {
		t.Fatal(err)
	}
	if err := s.EditPlanComment(deleted); err == nil {
		t.Fatal("missing comment deletion accepted")
	}
	for _, action := range []types.PlanReviewAction{types.PlanReviewImplement, types.PlanReviewNewSession} {
		if _, err := s.SettlePlanReview(types.PlanReviewSettle{Revision: 1, Action: action, Text: proposal}); err == nil || err.Error() != "Send or delete the comments first." {
			t.Fatalf("%s accepted pending comments: %v", action, err)
		}
	}

	edited := "# Improve tests\n\nA new paragraph.\n" + quote + "\n"
	out, err := feedback(edited)
	if err != nil {
		t.Fatal(err)
	}
	const display = "Plan feedback on revision 1\n- Lines 4-5: \"Path A Path B\" Cover both paths.\n- I edited the plan: 1 line added, 1 line removed."
	if out.Display != display {
		t.Fatalf("feedback display: got %q want %q", out.Display, display)
	}
	for _, evidence := range []string{`<comment lines="4-5">`, "<quoted_text>" + quote + "</quoted_text>", "<note>Cover both paths.</note>",
		"--- a/plan.md", "+++ b/plan.md", "+A new paragraph.", "-Delete this sentence.", "<plan>\n" + strings.TrimSuffix(edited, "\n")} {
		if !strings.Contains(html.UnescapeString(out.Text), evidence) {
			t.Fatalf("model feedback lacks %q: %s", evidence, out.Text)
		}
	}
	rev = latestReview(t, s)
	if !rev.Open() || !s.PlanMode() || rev.PendingComments() != 0 || !rev.Comments[0].Sent {
		t.Fatalf("feedback wrongly settled the plan or left pending comments: %+v", rev)
	}
	if err := s.EditPlanComment(edit); err == nil {
		t.Fatal("sent comment update accepted")
	}
	edit.Op = types.PlanCommentDelete
	if err := s.EditPlanComment(edit); err == nil {
		t.Fatal("sent comment deletion accepted")
	}
	waitTurnDone(t, sender, s.liveOrchestrator().ID)
	if out, err := s.SettlePlanReview(types.PlanReviewSettle{Revision: 1, Action: types.PlanReviewImplement, Text: edited}); err != nil ||
		out.Display != "Implement the plan (revision 1, with my edits)." ||
		!strings.Contains(out.Text, "<approved_plan>\n# Improve tests") {
		t.Fatalf("edited approval: %+v, %v", out, err)
	}
	if s.PlanMode() || latestReview(t, s).Outcome != types.PlanOutcomeImplemented {
		t.Fatal("implementation did not settle review and turn off Plan mode")
	}
	modeRows := s.StatusRows()
	if len(modeRows) != 2 || modeRows[1].Phase != "plan_mode_off" {
		t.Fatalf("expected a single Plan mode off row: %+v", modeRows)
	}
}

func TestPlanReviewActionsRespectBoundsAndStaleRevisions(t *testing.T) {
	const plan = "# A fresh plan"
	s, _ := reviewFixture(t, plan)
	for i := range types.PlanCommentMaxCount {
		edit := types.PlanCommentEdit{Revision: 1, Op: types.PlanCommentAdd, Comment: types.PlanComment{ID: fmt.Sprint(i), Body: "Note."}}
		if err := s.EditPlanComment(edit); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.EditPlanComment(types.PlanCommentEdit{Revision: 1, Op: types.PlanCommentAdd, Comment: types.PlanComment{ID: "extra", Body: "Extra."}}); err == nil {
		t.Fatal("accepted more than the allowed comments")
	}
	first := latestReview(t, s)
	if err := s.recordProposedPlan(2, planReply(plan), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePlanReview(types.PlanReviewSettle{Revision: 1, Text: plan, Action: types.PlanReviewFeedback}); err == nil {
		t.Fatal("stale plan revision was settled")
	}
	if err := s.EditPlanComment(types.PlanCommentEdit{Revision: 1, Op: types.PlanCommentAdd, Comment: types.PlanComment{ID: "late", Body: "Late."}}); err == nil {
		t.Fatal("replaced revision accepted new comments")
	}
	big := strings.Repeat("x", types.PlanReviewMaxPlanText)
	if err := s.recordProposedPlan(3, planReply(big+"x"), "", ""); err != nil || latestReview(t, s).Number != 2 {
		t.Fatalf("oversize proposal was recorded: %v", err)
	}
	if _, err := s.SettlePlanReview(types.PlanReviewSettle{Revision: 2, Text: big + "x", Action: types.PlanReviewNewSession}); err == nil {
		t.Fatal("oversize settlement was accepted")
	}
	destination, _ := reviewSessionFixture(t)
	out, err := s.SettlePlanReviewForAgent(t.Context(), s.liveOrchestrator().ID,
		types.PlanReviewSettle{Revision: 2, Text: plan + "\n", Action: types.PlanReviewNewSession, DestinationSessionID: destination.sessionID},
		provider.UserInput{}, destination)
	if err != nil || out.Display != "" || !strings.HasSuffix(out.Text, plan+"\n") ||
		!s.PlanMode() || latestReview(t, s).Outcome != types.PlanOutcomeNewSession {
		t.Fatalf("fresh context settlement: %+v, %v", out, err)
	}
	if err := s.recordProposedPlan(3, planReply(big), "", ""); err != nil {
		t.Fatal(err)
	}
	if out, err := s.SettlePlanReview(types.PlanReviewSettle{Revision: 3, Text: big + "\n", Action: types.PlanReviewImplement}); err != nil ||
		out.Text != "Implement the plan." || out.Display != "" {
		t.Fatalf("maximum length unchanged approval: len=%d, %q, %v", len(out.Text), out.Display, err)
	}
	for n := 4; n <= types.PlanRevisionMaxCount+2; n++ {
		if err := s.recordProposedPlan(uint64(n), planReply(plan), "", ""); err != nil {
			t.Fatal(err)
		}
	}
	review, _ := s.PlanReview()
	if len(review.Revisions) != types.PlanRevisionMaxCount || review.Revisions[0].Number != 3 ||
		review.Revisions[0].Outcome != types.PlanOutcomeImplemented || latestReview(t, s).Number != types.PlanRevisionMaxCount+2 {
		t.Fatal("review pruning lost numbering, settled outcome or count")
	}
	if _, err := os.Stat(first.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned file still exists: %v", err)
	}
	if _, err := s.OpenPlanRevision(1); err == nil {
		t.Fatal("pruned revision can still be reopened")
	}
}

func TestPlanReviewApprovalRollsBackWhenPlanModeCannotPersist(t *testing.T) {
	const plan = "# A plan"
	s, _ := reviewFixture(t, plan)
	if _, err := s.db.Exec(`CREATE TRIGGER block_mode_off BEFORE DELETE ON session_runtime_state
		WHEN OLD.key = 'plan_mode' BEGIN SELECT RAISE(ABORT, 'read-only plan mode'); END`); err != nil {
		t.Fatal(err)
	}
	input := types.PlanReviewSettle{Revision: 1, Text: plan, Action: types.PlanReviewImplement}
	if _, err := s.SettlePlanReview(input); err == nil || !s.PlanMode() || !latestReview(t, s).Open() {
		t.Fatalf("failed approval committed live changes: %v", err)
	}
	if err := s.loadPlanReview(); err != nil || !latestReview(t, s).Open() {
		t.Fatalf("failed approval committed durable changes: %v", err)
	}
	if len(s.StatusRows()) != 1 || len(s.QueuedInputs()) != 0 {
		t.Fatal("failed approval published a Plan mode off row")
	}
	if _, err := s.db.Exec(`DROP TRIGGER block_mode_off`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePlanReview(input); err != nil {
		t.Fatal(err)
	}
}

func TestPlanReviewFeedbackPreservesChangedQuoteAndTruncatesOnlyDisplay(t *testing.T) {
	quote := strings.Repeat("é", 61)
	plan := "# Review this\n" + quote
	s, _ := reviewFixture(t, plan)
	edit := types.PlanCommentEdit{
		Revision: 1, Op: types.PlanCommentAdd,
		Comment: types.PlanComment{ID: "changed", Body: "Keep the intent.",
			Anchor: anchor.Capture(plan, len("# Review this\n"), len(plan))},
	}
	if err := s.EditPlanComment(edit); err != nil {
		t.Fatal(err)
	}
	out, err := s.SettlePlanReview(types.PlanReviewSettle{Revision: 1, Action: types.PlanReviewFeedback, Text: "# Review this\nReplacement.\n"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.Text, `text_changed="true"`) || !strings.Contains(html.UnescapeString(out.Text), "<quoted_text>"+quote+"</quoted_text>") {
		t.Fatalf("changed quote was not preserved for the model: %+v", out)
	}
	if !strings.Contains(out.Display, `"`+strings.Repeat("é", 60)+`…" Keep the intent.`) || strings.Contains(out.Display, quote) {
		t.Fatalf("display did not truncate the quote at 60 runes: %s", out.Display)
	}
}

func genuineUserInputs(s *Session) []string {
	s.msgsMu.Lock()
	defer s.msgsMu.Unlock()
	var out []string
	for _, message := range s.msgs {
		if message.IsGenuineUserInput() {
			out = append(out, message.ContentString())
		}
	}
	return out
}

type planHandoffTestSender struct {
	*mockEventSender
	onQueued func()
}

func (s *planHandoffTestSender) SendAgentEventWithID(typ string, data any, agentID string) {
	if typ == "input_queued" {
		s.onQueued()
	}
	s.mockEventSender.SendAgentEventWithID(typ, data, agentID)
}

func TestPlanNewSessionHandoffFirstTurnRemainsImplementation(t *testing.T) {
	const plan = "# Fresh implementation\n\n- Use these exact steps."
	source, _ := reviewFixture(t, plan)
	requests := make(chan planModeRequest, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body planModeRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Acknowledged.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	destination, err := NewWithSessionDir("", mediaAttributionConfig(server.URL, "review-handoff-mode", "openai"), t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(destination.Close)
	destination.SetWorkDir(t.TempDir())
	target := control.Target{WorkspaceID: "original-workspace", ExecutionID: "source-only-checkout", SessionID: source.sessionID,
		Shell: &control.ShellOwner{ClientID: "original-shell", Epoch: 4}}
	var queued provider.UserInput
	var toggleErr error
	sender := &planHandoffTestSender{mockEventSender: newMockEventSender(), onQueued: func() {
		queued = destination.QueuedInputs()[0].Input
		toggleErr = destination.SetPlanMode(true)
	}}
	owner, err := destination.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	out, err := source.SettlePlanReviewForAgent(t.Context(), source.liveOrchestrator().ID,
		types.PlanReviewSettle{Revision: 1, Text: plan, Action: types.PlanReviewNewSession, DestinationSessionID: destination.sessionID},
		provider.UserInput{ControlTarget: &target}, destination)
	if err != nil || toggleErr != nil {
		t.Fatalf("handoff failed: %v; mode change: %v", err, toggleErr)
	}
	wantTarget := target
	wantTarget.SessionID, wantTarget.ExecutionID = destination.sessionID, ""
	if queued.Origin != provider.OriginHuman || queued.Text != out.Text || !strings.Contains(queued.Text, plan) ||
		!reflect.DeepEqual(queued.ControlTarget, &wantTarget) {
		t.Fatalf("handoff changed the approved input or submitting attachment: %+v", queued)
	}
	waitTurnDone(t, sender.mockEventSender, owner.ID)
	first := <-requests
	if !destination.PlanMode() || first.offers("open_plan_investigation") || strings.Contains(first.text(0), "<plan_mode>") {
		t.Fatal("first destination turn did not implement the plan in ordinary mode")
	}
	if !owner.SubmitInput(provider.PlainInput("Plan the follow-up.")) {
		t.Fatal("follow-up was not accepted")
	}
	waitTurnDone(t, sender.mockEventSender, owner.ID)
	followup := <-requests
	if !followup.offers("open_plan_investigation") || !strings.Contains(followup.text(0), "<plan_mode>") {
		t.Fatal("handoff policy overrode Plan mode for a later input")
	}
}

// New session spans two databases. When the source cannot record a delivered
// handoff, the review stays bound to that destination: only its exact receipt
// can close the review, and the plan is never sent a second time. A bound
// destination without that receipt definitely did not receive the plan, so
// the check reopens the choice instead of leaving the review stuck.
func TestPlanNewSessionHandoffIsCheckedNeverResent(t *testing.T) {
	const plan = "# Fresh context plan\n\n- Do the work"
	s, _ := reviewFixture(t, plan)
	agentID := s.liveOrchestrator().ID
	destination, sender := reviewSessionFixture(t)
	settle := types.PlanReviewSettle{Revision: 1, Text: plan, Action: types.PlanReviewNewSession, DestinationSessionID: destination.sessionID}
	if _, err := s.db.Exec(`CREATE TRIGGER block_handoff_close BEFORE INSERT ON session_runtime_state
		WHEN NEW.key = 'plan_review' AND instr(CAST(NEW.value AS TEXT), '"outcome":"new_session"') > 0
		BEGIN SELECT RAISE(ABORT, 'source storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePlanReviewForAgent(t.Context(), agentID, settle, provider.UserInput{}, destination); err != errPlanHandoffCheck {
		t.Fatalf("unrecorded delivery: %v", err)
	}
	bound := latestReview(t, s)
	if !bound.Open() || bound.NewSessionID != destination.sessionID || !s.PlanMode() {
		t.Fatalf("delivered handoff is not bound to its destination: %+v", bound)
	}
	waitTurnDone(t, sender, destination.liveOrchestrator().ID)
	if inputs := genuineUserInputs(destination); len(inputs) != 1 || !strings.Contains(inputs[0], "- Do the work") {
		t.Fatalf("destination inputs after delivery = %q", inputs)
	}
	for _, refused := range []types.PlanReviewSettle{
		{Revision: 1, Text: plan, Action: types.PlanReviewImplement},
		{Revision: 1, Text: plan, Action: types.PlanReviewNewSession},
	} {
		if _, err := s.SettlePlanReviewForAgent(t.Context(), agentID, refused, provider.UserInput{}, destination); err != errPlanHandoffOpen {
			t.Fatalf("%s while bound: %v", refused.Action, err)
		}
	}
	if err := s.EditPlanComment(types.PlanCommentEdit{Revision: 1, Op: types.PlanCommentAdd, Comment: types.PlanComment{ID: "late", Body: "Late."}}); err != errPlanHandoffOpen {
		t.Fatalf("comment on a bound handoff: %v", err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER block_handoff_close`); err != nil {
		t.Fatal(err)
	}
	check := types.PlanReviewSettle{Revision: 1, Action: types.PlanReviewNewSession, DestinationSessionID: destination.sessionID}
	exact, err := sessiondb.LoadRuntimeState(t.Context(), destination.db, planHandoffReceiptKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessiondb.SaveRuntimeState(t.Context(), destination.db, planHandoffReceiptKey, []byte(`{"InputID":"another-input"}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePlanReviewForAgent(t.Context(), agentID, check, provider.UserInput{}, destination); err != errPlanHandoffCheck ||
		latestReview(t, s).NewSessionID != destination.sessionID {
		t.Fatalf("a conflicting receipt unlocked an already delivered plan: %v", err)
	}
	if err := sessiondb.SaveRuntimeState(t.Context(), destination.db, planHandoffReceiptKey, exact); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SettlePlanReviewForAgent(t.Context(), agentID, check, provider.UserInput{}, destination); err != nil {
		t.Fatalf("check of the delivered handoff: %v", err)
	}
	if closed := latestReview(t, s); closed.Outcome != types.PlanOutcomeNewSession || closed.NewSessionID != destination.sessionID || !s.PlanMode() {
		t.Fatalf("checked handoff did not close the source review: %+v", closed)
	}
	if inputs := genuineUserInputs(destination); len(inputs) != 1 || len(destination.QueuedInputs()) != 0 {
		t.Fatalf("check resent the plan: %q, queued %d", inputs, len(destination.QueuedInputs()))
	}

	// A bound record whose destination has no receipt: the queue write
	// failed and so did restoring the source.
	s2, _ := reviewFixture(t, plan)
	empty, emptySender := reviewSessionFixture(t)
	if _, err := empty.DB().Exec(`CREATE TRIGGER reject_plan_input BEFORE INSERT ON session_runtime_state
		WHEN NEW.key = 'plan_handoff_receipt' BEGIN SELECT RAISE(ABORT, 'destination storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.db.Exec(`CREATE TRIGGER block_restore BEFORE INSERT ON session_runtime_state
		WHEN NEW.key = 'plan_review' AND instr(CAST(NEW.value AS TEXT), '"handoff_source"') = 0
		BEGIN SELECT RAISE(ABORT, 'source storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	lost := settle
	lost.DestinationSessionID = empty.sessionID
	if _, err := s2.SettlePlanReviewForAgent(t.Context(), s2.liveOrchestrator().ID, lost, provider.UserInput{}, empty); err != errPlanHandoffCheck {
		t.Fatalf("failed delivery with failed restore: %v", err)
	}
	if latestReview(t, s2).NewSessionID != empty.sessionID {
		t.Fatal("failed restore did not leave the binding for a check")
	}
	if _, err := empty.DB().Exec(`DROP TRIGGER reject_plan_input`); err != nil {
		t.Fatal(err)
	}
	if _, err := s2.db.Exec(`DROP TRIGGER block_restore`); err != nil {
		t.Fatal(err)
	}
	check.DestinationSessionID = empty.sessionID
	if _, err := s2.SettlePlanReviewForAgent(t.Context(), s2.liveOrchestrator().ID, check, provider.UserInput{}, empty); err != errPlanHandoffMissing {
		t.Fatalf("check without a receipt: %v", err)
	}
	if reopened := latestReview(t, s2); !reopened.Open() || reopened.NewSessionID != "" || len(genuineUserInputs(empty)) != 0 {
		t.Fatalf("missing receipt did not reopen the choice: %+v", reopened)
	}
	if _, err := s2.SettlePlanReviewForAgent(t.Context(), s2.liveOrchestrator().ID, lost, provider.UserInput{}, empty); err != nil ||
		latestReview(t, s2).Outcome != types.PlanOutcomeNewSession {
		t.Fatalf("New session after a reopened choice: %v", err)
	}
	waitTurnDone(t, emptySender, empty.liveOrchestrator().ID)
}
