package core

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent/collaboration"
	"crude/agent/provider"
	"crude/agent/sessiondb"
	"crude/core/client"
	"crude/core/protocol"
	"crude/types"
	"crude/types/accounting"
	"crude/types/control"
	"crude/types/inspect"
	"crude/workspace"
)

func TestPromptHistoryGatewayFindsModelAndReadsOriginalHumanText(t *testing.T) {
	const original = "My first prompt to the reviewer."
	long := strings.Repeat("a", 4095) + "🌱\x00" + strings.Repeat("تعليق\n", 100)
	r, err := New(func(root string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	c := controlWireClient(t, r, "prompt-observer")
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: t.TempDir()}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read}}, nil); err != nil {
		t.Fatal(err)
	}
	w := r.actors()[0].ws
	if err := os.MkdirAll(filepath.Join(w.WorkspaceDir, "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}
	register := func(id, category string) collaboration.Agent {
		a, err := w.Collaboration.RegisterAgent(t.Context(), collaboration.AgentRegistration{ID: id, Name: id, Category: category, SessionID: id + "-session", OwnerInstance: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		return a
	}
	for i := range 24 {
		register(fmt.Sprintf("a-%02d", i), "default")
	}
	reviewer := register("z-reviewer", "default")
	other := register("z-other", "aeron")
	for _, a := range []collaboration.Agent{reviewer, other} {
		db, err := sessiondb.OpenAndMigrate(filepath.Join(w.WorkspaceDir, "sessions", a.SessionID+".db"))
		if err != nil {
			t.Fatal(err)
		}
		if err := sessiondb.CreateSessionRow(db, a.SessionID); err != nil {
			t.Fatal(err)
		}
		appendInput := func(id, text string, turn int, origin provider.MessageOrigin, visibility provider.MessageVisibility) {
			m := provider.NewTextMessage("user", "RAW_PROVIDER_ENRICHMENT")
			m.InternalID, m.Origin, m.Visibility = id, origin, visibility
			_, err := sessiondb.PublishContext(db, sessiondb.ContextPublication{SessionID: a.SessionID, AgentID: a.SessionID, RuntimeAgentID: a.ID,
				ExpectedRevision: uint64(turn - 1), Mutation: sessiondb.ContextMutation{Append: []sessiondb.ContextMessageDelta{{Message: m,
					Meta:    sessiondb.DisplayMeta{TurnGeneration: uint64(turn), ContextOnly: visibility != provider.VisibilityConversation, Snapshots: []types.ConversationMessage{{Role: "user", Content: text}}},
					AgentID: a.ID, Turn: turn, CreatedAt: time.Now().UnixNano()}}}})
			if err != nil {
				t.Fatal(err)
			}
		}
		appendInput("peer", "ASSIGNMENT_NOT_HUMAN", 1, provider.OriginAssignment, provider.VisibilityConversation)
		appendInput("private", "CONTEXT_ONLY_SECRET", 2, provider.OriginHuman, provider.VisibilityContextOnly)
		appendInput("first", original, 3, provider.OriginHuman, provider.VisibilityConversation)
		appendInput("long", long, 4, provider.OriginHuman, provider.VisibilityConversation)
		appendInput("steering", "A later instruction", 5, provider.OriginHumanSteering, provider.VisibilityConversation)
		usage := func(id string, generation uint64, model, kind string, outcome accounting.Outcome) {
			if err := sessiondb.UpsertUsageRecord(db, sessiondb.UsageRecord{RequestID: id, SessionID: a.SessionID, AgentID: a.ID,
				TurnGeneration: generation, ModelID: model, ProviderID: "fixture", RequestKind: kind, Outcome: outcome, CreatedAt: int64(generation), UpdatedAt: int64(generation)}); err != nil {
				t.Fatal(err)
			}
		}
		usage("first-answer", 3, "other-model", "normal", accounting.Succeeded)
		usage("failed-opus", 3, "opus", "normal", accounting.Failed)
		usage("background-opus", 3, "opus", "compaction", accounting.Succeeded)
		usage("later-answer", 4, "vendor/opus-fixture", "normal", accounting.Succeeded)
		usage("steering-unqualified", 5, "steering-only", "normal", accounting.Succeeded)
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
	run, err := w.Collaboration.StartRun(t.Context(), collaboration.StartRunRequest{AgentID: reviewer.ID, RuntimeEpoch: reviewer.RuntimeEpoch, Origin: collaboration.OriginHuman})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Collaboration.FinishRun(t.Context(), collaboration.FinishRunRequest{RunID: run.ID, AgentID: reviewer.ID, RuntimeEpoch: reviewer.RuntimeEpoch, ExpectedVersion: run.Version, State: collaboration.RunSucceeded, ContextCommitID: "long"}); err != nil {
		t.Fatal(err)
	}
	var descriptors coreprotocol.ControlDescriptors
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlDescribe, &coreprotocol.ControlDescribeRequest{Query: "history.prompts", Target: control.Target{WorkspaceID: attached.WorkspaceID}}, &descriptors); err != nil {
		t.Fatal(err)
	}
	if len(descriptors) == 0 || descriptors[0].ID != "history.prompts" || descriptors[0].Effect != control.Read {
		t.Fatalf("could not discover the read-only history tool: %+v", descriptors)
	}
	n := 0
	call := func(args any, wantError bool) inspect.Page {
		t.Helper()
		n++
		id := fmt.Sprintf("prompt-history-%d", n)
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		var result control.Result
		request := control.Request{ID: id, CommandID: "history.prompts", Target: control.Target{WorkspaceID: attached.WorkspaceID}, Input: raw}
		_, err = c.CallWith(ctx, attached.WorkspaceID, coreprotocol.ControlExecute, &request, &result, coreclient.CallOptions{ID: id})
		if err != nil {
			t.Fatal(err)
		}
		if wantError {
			if result.Fault == nil {
				t.Fatalf("invalid request accepted: %+v", result)
			}
			return inspect.Page{}
		}
		if result.State != control.Committed || result.Fault != nil {
			t.Fatalf("history read did not complete: %+v", result)
		}
		for _, excluded := range []string{"ASSIGNMENT_NOT_HUMAN", "CONTEXT_ONLY_SECRET", "RAW_PROVIDER_ENRICHMENT"} {
			if strings.Contains(string(result.Output), excluded) {
				t.Fatalf("private or non-human text escaped: %s", excluded)
			}
		}
		var page inspect.Page
		if err := json.Unmarshal(result.Output, &page); err != nil {
			t.Fatal(err)
		}
		return page
	}
	row := func(page inspect.Page) promptHistoryRecord {
		t.Helper()
		var got promptHistoryRecord
		if len(page.Records) != 1 {
			t.Fatalf("want one record: %+v", page)
		}
		if err := json.Unmarshal(page.Records[0].Value, &got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	empty := call(map[string]any{"model": "OPUS", "category": "default"}, false)
	var source promptHistorySource
	if err := json.Unmarshal(empty.Source, &source); err != nil || len(empty.Records) != 0 || source.NextScan == nil || source.AgentsChecked != 24 {
		t.Fatalf("empty batch hid continuation: %+v %v", source, err)
	}
	got := row(call(source.NextScan, false))
	if got.AgentID != "z-reviewer" || got.Category != "default" || got.ID != "long" || got.RunID == "" || len(got.Models) != 1 || got.Models[0].ID != "vendor/opus-fixture" || got.NextTextOffset == nil {
		t.Fatalf("wrong model or provenance: %+v", got)
	}
	text := row(call(map[string]any{"agent_id": got.AgentID, "prompt_id": got.ID}, false))
	if text.NextTextOffset == nil || *text.NextTextOffset != 4095 {
		t.Fatalf("UTF-8 boundary lost: %+v", text.NextTextOffset)
	}
	rest := row(call(map[string]any{"agent_id": got.AgentID, "prompt_id": got.ID, "text_offset": *text.NextTextOffset}, false))
	if rest.NextTextOffset != nil || text.Text+rest.Text != long || rest.TextBytes != int64(len(long)) {
		t.Fatal("exact original text did not round-trip across pages")
	}
	first := row(call(map[string]any{"agent_id": got.AgentID}, false))
	if first.ID != "first" || first.Text != original || len(first.Models) != 1 || first.Models[0].ID != "other-model" {
		t.Fatalf("wrong first human input: %+v", first)
	}
	if strict := call(map[string]any{"agent_id": got.AgentID, "mode": "first", "model": "opus"}, false); strict.Total != 0 {
		t.Fatal("later, failed, or background model was attributed to first prompt")
	}
	if steering := call(map[string]any{"agent_id": got.AgentID, "model": "steering-only"}, false); steering.Total != 0 {
		t.Fatal("steering inherited turn model attribution")
	}
	call(map[string]any{"agent_id": got.AgentID, "prompt_id": "private"}, true)
	call(map[string]any{"agent_id": got.AgentID, "prompt_id": got.ID, "text_offset": 4096}, true)
}
