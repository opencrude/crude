package core

import (
	"context"
	"html"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/agent/sessiondb"
	"crude/core/coreclient"
	"crude/core/shellport"
	"crude/core/shellport/remote"
	"crude/internal/providersim"
	"crude/text/anchor"
	"crude/types/conversation"
	"crude/types/setting"
	"crude/workspace"
	"crude/workspace/document"
)

type planReviewShellSink chan any

func (s planReviewShellSink) Send(value any) { s <- value }

func TestCorePlanReviewMirrorsCompletionAndCanonicalEditorFeedback(t *testing.T) {
	const proposal = "# Check output\n\nCheck the exit status."
	const revised = "# Revised plan\n\nCheck exit status and error output."
	type wirePlanInput struct {
		users    []string
		planMode bool
	}
	modelRequests := make(chan wirePlanInput, 4)
	server := providersim.New(t)
	server.Respond(func(input providersim.Request) providersim.Turn {
		var captured wirePlanInput
		for _, part := range input.System {
			captured.planMode = captured.planMode || strings.Contains(part.Text, "<plan_mode>")
		}
		for _, message := range input.Messages {
			text := simMessageText(message)
			if message.Role == "user" {
				captured.users = append(captured.users, text)
			}
			// The root prompt stays fixed until compaction; a later settings
			// update replaces its planning section for the model.
			if _, section, ok := strings.Cut(text, `<section name="planning">`); ok {
				section, _, _ = strings.Cut(section, "</section>")
				captured.planMode = strings.Contains(section, "<plan_mode>")
			}
		}
		modelRequests <- captured
		text := proposal
		if input.Index == 2 {
			text = revised
		}
		return providersim.Text("<proposed_plan>\n" + text + "\n</proposed_plan>")
	})
	home := t.TempDir()
	setCoreTestHome(t, home)
	r, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "model", "tester", setting.Settings{}, coreTestLogger{})
		w.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "off"}, Memory: setting.Memory{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		off := false
		cfg := &session.Config{Agents: session.AgentsConfig{ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{"default": {
			MultiAgent: &off, Categories: map[string]session.CategoryConfig{
				session.CategoryDefault: {AccountID: "test", Model: "model", ModelSupportsTools: true},
				session.CategoryErrand:  {AccountID: "test", Model: "model"},
			},
		}}}, Accounts: map[string]provider.Account{"test": {
			ID: "test", ProviderID: "openai", APIKey: "test",
			Connection: provider.Template{Name: "Test", ProviderURL: server.BaseURL(providersim.Chat), APIStandard: "openai", AuthType: provider.AuthProtocol},
		}}}
		w.SetBackendConfig(cfg, home)
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		one, two := net.Pipe()
		go func() { _ = ServeConnection(t.Context(), r, one) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(two), id)
	}
	port, err := remote.Attach(t.Context(), remote.Options{Root: t.TempDir(), Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = port.Close() })
	pubs := make(planReviewShellSink, 128)
	bus := workspace.NewEventBus()
	bus.SetSink(pubs)
	port.SetEventBus(bus)
	port.StartReplica()
	// Advance the real shell's acknowledged mirror, not an ad-hoc snapshot
	// poll. A Done reduction must observe the revision in this same apply.
	pump := func(phase string, accept func(shellport.ReplicaApplied) bool) {
		t.Helper()
		timer := time.NewTimer(8 * time.Second)
		defer timer.Stop()
		for {
			select {
			case msg := <-pubs:
				switch value := msg.(type) {
				case shellport.ReplicaChangesMsg:
					if accept(port.ApplyReplicaChanges(value)) {
						return
					}
				case shellport.LiveDocumentsChangedMsg:
					value.Done(nil)
				case shellport.DocumentInsightMsg:
					value.Done(nil)
				}
			case <-timer.C:
				t.Fatalf("no shell publication for %s", phase)
			}
		}
	}

	created := port.NewAgentSession()
	if created.Error != "" || created.OrchID == "" {
		t.Fatalf("could not create session: %+v", created)
	}
	agentID := created.OrchID
	pump("new session", func(_ shellport.ReplicaApplied) bool { return port.SessionForAgent(agentID) != nil })
	if err := port.SetPlanModeForAgent(agentID, true); err != nil {
		t.Fatal(err)
	}
	submit := func(text, display string) uint64 {
		t.Helper()
		intent, err := port.PrepareAgentInputAdmission(agentID, provider.UserInput{Text: text, DisplayText: display}, 0)
		if err != nil {
			t.Fatal(err)
		}
		result := port.CommitAgentInputAdmission(t.Context(), intent)
		if result.Err != nil || result.Receipt.TurnGeneration == 0 {
			t.Fatalf("submit input: %+v", result)
		}
		return result.Receipt.TurnGeneration
	}
	waitReview := func(number int, generation uint64) conversation.PlanRevision {
		t.Helper()
		var revision conversation.PlanRevision
		pump("turn completion", func(applied shellport.ReplicaApplied) bool {
			for _, reduction := range applied.Reductions {
				if done := reduction.Effects.Done; done != nil && done.AgentID == agentID && done.Reason == "completed" {
					review, _ := port.PlanReviewForAgent(agentID)
					revision, _ = review.Latest()
					if revision.Number != number || revision.Generation != generation {
						t.Fatalf("Done reducer did not see revision %d/generation %d in its mirror: %+v", number, generation, review)
					}
					return true
				}
			}
			return false
		})
		return revision
	}
	localRead := func() conversation.PlanReview {
		t.Helper()
		a := deletionActor(t, r, port.WorkspaceID())
		var review conversation.PlanReview
		var ok bool
		inspectDeletionActor(t, a, func() { review, ok = shellport.Local(a.ws).PlanReviewForAgent(agentID) })
		if !ok {
			t.Fatal("local port does not show the canonical review")
		}
		return review
	}

	first := waitReview(1, submit("Propose a plan.", ""))
	<-modelRequests
	if got, _ := localRead().Latest(); !reflect.DeepEqual(got, first) {
		t.Fatalf("local and remote differ at completion: local=%+v remote=%+v", got, first)
	}
	path, err := port.OpenPlanRevision(agentID, first.Number)
	if err != nil || path != first.Path || !filepath.IsAbs(path) {
		t.Fatalf("remote plan open: path=%q, %v", path, err)
	}
	doc, err := port.OpenDocument(t.Context(), path)
	if err != nil || string(doc.Text) != proposal+"\n" {
		t.Fatalf("open plan in editor: text=%q, %v", doc.Text, err)
	}
	quoteStart := strings.Index(string(doc.Text), "Check the exit status.")
	edit := conversation.PlanCommentEdit{Revision: first.Number, Op: conversation.PlanCommentAdd, Comment: conversation.PlanComment{
		ID: "check-errors", Body: "Also check errors.", Anchor: anchor.Capture(string(doc.Text), quoteStart, len(doc.Text)-1),
	}}
	if err := port.EditPlanComment(agentID, edit); err != nil {
		t.Fatal(err)
	}
	pump("new plan comment", func(_ shellport.ReplicaApplied) bool {
		review, _ := port.PlanReviewForAgent(agentID)
		revision, _ := review.Latest()
		return len(revision.Comments) == 1 && revision.Comments[0].Body == edit.Comment.Body
	})
	if local, _ := localRead().Latest(); local.PendingComments() != 1 {
		t.Fatalf("local port lost the remote comment: %+v", local)
	}
	if _, err := port.SettlePlanReview(agentID, conversation.PlanReviewSettle{Revision: 1, Action: conversation.PlanReviewImplement}); err == nil ||
		err.Error() != "Send or delete the comments first." {
		t.Fatalf("implementation did not refuse a pending comment: %v", err)
	}

	const unsaved = "Do the test first.\n"
	receipt, err := port.EditDocument(t.Context(), document.Change{
		DocumentID: doc.ID, Owner: port.DocumentOwner(), BaseRevision: doc.Revision,
		RequestID: "edit-plan-document", GroupID: "insert-a-line", Edits: []document.Edit{{Start: quoteStart, End: quoteStart, Text: unsaved}},
	})
	if err != nil || receipt.Error != "" || receipt.Revision != doc.Revision+1 {
		t.Fatalf("could not edit the canonical plan: %+v, %v", receipt, err)
	}
	// Fail the actual input-admission write after the review update in the
	// transaction. The shell must get a refusal and durable state must stay open.
	a := deletionActor(t, r, port.WorkspaceID())
	var backend *session.Session
	inspectDeletionActor(t, a, func() { backend = a.ws.BackendSession(created.SessionID) })
	if _, err := backend.DB().Exec(`CREATE TRIGGER block_review_input BEFORE INSERT ON session_runtime_state
		WHEN NEW.key = 'queued_inputs' BEGIN SELECT RAISE(ABORT, 'test admission failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := port.SettlePlanReview(agentID, conversation.PlanReviewSettle{Revision: 1, Action: conversation.PlanReviewFeedback}); err == nil {
		t.Fatal("shell accepted a review whose input could not be admitted")
	}
	failed, _ := localRead().Latest()
	if durable, err := sessiondb.LoadRuntimeState(t.Context(), backend.DB(), "queued_inputs"); err != nil || len(durable) != 0 ||
		!failed.Open() || failed.PendingComments() != 1 || !backend.PlanMode() || len(server.Requests()) != 1 {
		t.Fatalf("failed admission consumed a review or dispatched a message: %+v, %v", failed, err)
	}
	if _, err := backend.DB().Exec(`DROP TRIGGER block_review_input`); err != nil {
		t.Fatal(err)
	}
	out, err := port.SettlePlanReview(agentID, conversation.PlanReviewSettle{Revision: 1, Action: conversation.PlanReviewFeedback})
	if err != nil || !strings.Contains(html.UnescapeString(out.Text), "+"+unsaved) ||
		!strings.Contains(out.Display, `Line 4: "Check the exit status." Also check errors.`) {
		t.Fatalf("settlement did not use the canonical unsaved plan: %+v, %v", out, err)
	}
	if disk, err := os.ReadFile(path); err != nil || string(disk) != proposal+"\n" {
		t.Fatalf("settlement changed the plan file on disk: %q, %v", disk, err)
	}
	// Settlement itself submits; the shell does not send another request.
	waitReview(2, first.Generation+1)
	if actual := <-modelRequests; !actual.planMode || !slices.Contains(actual.users, out.Text) || slices.Contains(actual.users, out.Display) {
		t.Fatal("provider did not receive the model review separately from its transcript summary")
	}
	node := port.AgentMgr().Get(agentID)
	var sawDisplay bool
	for _, msg := range node.State.Messages.Messages() {
		sawDisplay = sawDisplay || msg.Content == out.Display
	}
	if !sawDisplay {
		t.Fatal("shell did not show the user-facing review summary")
	}

	approval, err := port.SettlePlanReview(agentID, conversation.PlanReviewSettle{Revision: 2, Action: conversation.PlanReviewImplement})
	if err != nil || approval.Text != "Implement the plan." || approval.Display != "" {
		t.Fatalf("unopened revision did not use its file: %+v, %v", approval, err)
	}
	select {
	case actual := <-modelRequests:
		if actual.planMode || !slices.Contains(actual.users, "Implement the plan.") {
			t.Fatal("approval did not dispatch a normal-mode implementation turn")
		}
	case <-time.After(8 * time.Second):
		t.Fatal("approval did not dispatch a turn")
	}
	pump("approved revision and Plan mode off", func(_ shellport.ReplicaApplied) bool {
		review, _ := port.PlanReviewForAgent(agentID)
		latest, _ := review.Latest()
		return latest.Outcome == conversation.PlanOutcomeImplemented && !port.PlanModeForAgent(agentID)
	})
	local := localRead()
	if len(local.Revisions) != 2 || local.Revisions[0].Outcome != conversation.PlanOutcomeReplaced || local.Revisions[0].PendingComments() != 0 ||
		local.Revisions[1].Outcome != conversation.PlanOutcomeImplemented {
		t.Fatalf("local review lost outcomes: %+v", local)
	}
}
