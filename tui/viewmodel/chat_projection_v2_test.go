package viewmodel

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"crude/agent"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/types"
	"crude/types/command"
	"crude/types/recovery"
	"crude/workspace"
)

// Use the actual v2 codec and publication installer rather than constructing
// frontend rows: retained renderers must paint a replacement core's facts even
// when both that core's message IDs and revisions collide with its predecessor.
func TestV2PublicationResetRefreshesRetainedChatAndNestedProjection(t *testing.T) {
	for _, newDomain := range []bool{false, true} {
		t.Run(fmt.Sprint(newDomain), func(t *testing.T) {
			oldRecovery := recovery.Status{ID: "old-incident", State: recovery.NeedsAttention, UpdatedAt: time.Unix(100, 0)}
			snapshot := coreprotocol.WorkspaceSnapshot{ID: "w", Revision: 1,
				Sessions: []coreprotocol.SessionSnapshot{{ID: "s", AgentID: "root"}},
				Agents: []workspace.AgentSnapshot{{ID: "root", SessionID: "s", ConversationRevision: 1,
					Messages: []workspace.Message{{InternalID: "m", Role: types.RoleAssistant, Content: "OLD CORE BODY"}, workspace.RecoveryMessage(oldRecovery)}}}}
			decode := func(s coreprotocol.WorkspaceSnapshot) coreprotocol.WorkspaceSnapshot {
				t.Helper()
				b, err := coreprotocol.Marshal(&s)
				if err != nil {
					t.Fatal(err)
				}
				var out coreprotocol.WorkspaceSnapshot
				if err := coreprotocol.Decode(b, &out); err != nil {
					t.Fatal(err)
				}
				return out
			}
			view, err := coreclient.NewWorkspaceView(nil, decode(snapshot))
			if err != nil {
				t.Fatal(err)
			}
			conversation := view.AgentMgr.Get("root").State.Messages
			registry := newSubPaneProjectionRegistry(theme.DefaultDark())
			projection := registry.Get("root", workspace.PaneAgent)
			projection.renderer.SetSize(80, 24)
			projection.renderer.SetChatAreaBounds(0, 20)
			registry.SyncFromWorkspace("root", conversation, false)
			rows, _, _ := projection.renderer.ExportMessageRows(80)
			if !strings.Contains(strings.Join(rows[0].Lines, "\n"), "OLD CORE BODY") {
				t.Fatalf("old fixture not painted: %+v", rows)
			}
			oldScroll, generation := projection.scroll, conversation.SourceGeneration()
			// A malformed candidate must not change either canonical or cache clocks.
			bad := decode(snapshot)
			bad.Agents[0].Messages[0].InternalID = ""
			if err := view.InstallPublicationSnapshot(bad, newDomain); err == nil {
				t.Fatal("invalid reset accepted")
			}
			if conversation.SourceGeneration() != generation {
				t.Fatal("failed preflight advanced source generation")
			}
			snapshot.Agents[0].Messages = []workspace.Message{{InternalID: "m", Role: types.RoleAssistant, Content: "REPLACEMENT CORE BODY"}}
			if err := view.InstallPublicationSnapshot(decode(snapshot), newDomain); err != nil {
				t.Fatal(err)
			}
			registry.SyncFromWorkspace("root", conversation, false)
			rows, _, _ = projection.renderer.ExportMessageRows(80)
			text := strings.Join(rows[0].Lines, "\n")
			if !strings.Contains(text, "REPLACEMENT CORE BODY") || strings.Contains(text, "OLD CORE") || len(rows) != 1 {
				t.Fatalf("reset left stale painted facts: %+v", rows)
			}
			if conversation != view.AgentMgr.Get("root").State.Messages || conversation.Version() != 1 || conversation.SourceGeneration() <= generation || projection.scroll == oldScroll {
				t.Fatal("reset lost identity/cursor or retained the old source interaction lifetime")
			}
			if got := view.AgentMgr.Get("root").State.RecoveryStatus(); got.ID != "" {
				t.Fatalf("removed recovery survived replacement: %+v", got)
			}
			// Ordinary duplicate reconciliation retains caches and scroll lifetime.
			version, scroll := projection.renderer.Version(), projection.scroll
			registry.SyncFromWorkspace("root", conversation, false)
			if projection.renderer.Version() != version || projection.scroll != scroll {
				t.Fatal("unchanged source invalidated presentation")
			}
		})
	}
}

// Metadata changes can arrive without new message text. Compare the live
// retained projection with a fresh hydrated one and assert actual attribution,
// not only equality between two copies of the same helper's output.
func TestV2PublicationMetadataOnlyUpdatesRepaintAttribution(t *testing.T) {
	message := workspace.Message{InternalID: "peer", Role: types.RoleUser, Origin: provider.OriginPeer, Content: "Review exact evidence", Time: time.Unix(2000, 0),
		Collaboration: &provider.CollaborationAttribution{EventID: "e", SenderName: "Asha", RecipientNames: []string{"Blake"}, Kind: "peer_note", ArtifactID: "report", ArtifactRevision: 1}}
	snapshot := coreprotocol.WorkspaceSnapshot{ID: "w", Revision: 1, Sessions: []coreprotocol.SessionSnapshot{{ID: "s", AgentID: "root"}}, Agents: []workspace.AgentSnapshot{{ID: "root", SessionID: "s", ConversationRevision: 1, Messages: []workspace.Message{message}}}}
	view, err := coreclient.NewWorkspaceView(nil, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	r := chatprojection.NewRenderer(theme.DefaultDark())
	r.SetSize(100, 24)
	r.SetChatAreaBounds(0, 20)
	sync := func() {
		messages, version := chatMessagesFromConversation(view.AgentMgr.Get("root").State.Messages)
		r.SyncMessages(messages, version, view.AgentMgr.Get("root").State.Messages.SourceGeneration())
	}
	sync()
	_, _, _ = r.ExportMessageRows(100)
	message.Collaboration = &provider.CollaborationAttribution{EventID: "e", SenderName: "Casey", RecipientNames: []string{"Drew"}, Kind: "work_request", ArtifactID: "report", ArtifactRevision: 2}
	update := coreprotocol.WorkspaceUpdate{Changes: coreprotocol.AgentChangesResult{Revision: 2}, Conversations: []coreprotocol.ConversationUpdate{{AgentID: "root", Delta: workspace.ConversationDelta{BaseRevision: 1, Revision: 2, Upserts: []workspace.Message{message}}}}}
	b, err := coreprotocol.Marshal(&update)
	if err != nil {
		t.Fatal(err)
	}
	var decoded coreprotocol.WorkspaceUpdate
	if err := coreprotocol.Decode(b, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, err := view.ApplyPublication(decoded); err != nil {
		t.Fatal(err)
	}
	sync()
	rows, _, _ := r.ExportMessageRows(100)
	text := strings.Join(rows[0].Lines, "\n")
	for _, want := range []string{"Casey", "Drew", "Work request"} {
		if !strings.Contains(text, want) {
			t.Fatalf("metadata-only update lost %q: %s", want, text)
		}
	}
	if strings.Contains(text, "Asha") || strings.Contains(text, "Blake") {
		t.Fatalf("stale attribution remains: %s", text)
	}
	fresh := chatprojection.NewRenderer(theme.DefaultDark())
	fresh.SetSize(100, 24)
	fresh.SetChatAreaBounds(0, 20)
	fresh.SyncMessages(ChatMessagesFromViews([]workspace.ChatMessageView{message}), 1, 0)
	hydrated, _, _ := fresh.ExportMessageRows(100)
	assertSameRows(t, "metadata-only v2 update", rows[0], hydrated[0])
	if r.Messages()[0].Collaboration.ArtifactRevision != 2 {
		t.Fatal("report revision remained stale")
	}
}

func TestV2ProducerEnvelopesAndSQLiteHydrationPaintIdentically(t *testing.T) {
	for _, tc := range []struct {
		name   string
		events []workspace.AgentEvent
		want   string
		absent string
	}{
		{"automatic compaction", []workspace.AgentEvent{
			{Type: "context_compaction_started", Data: map[string]any{"request_id": "compact", "trigger": "automatic", "before_tokens": 12000}},
			{Type: "context_compacted", Data: map[string]any{"request_id": "compact", "trigger": "automatic", "before_tokens": 12000, "after_tokens": 3000}},
		}, "Compacted context", "Context compaction operation"},
		{"authored report", []workspace.AgentEvent{
			{Type: "tool_call", Data: map[string]any{"tool_id": "report-call", "internal_id": "report-call", "name": "publish_report", "args": `{"recipient_ids":["parent"],"kind":"final","summary":"Verified publication"}`}},
			{Type: "tool_result", Data: map[string]any{"tool_id": "report-call", "internal_id": "report-call", "name": "publish_report", "result": `{"artifact":{"id":"report-1","revision":1,"author_id":"root","kind":"final","summary":"Verified publication","body":"Complete evidence retained"},"publish":{"event":{"id":"published","kind":"report_published"},"deliveries":[{"id":"delivery","state":"pending"}]}}`}},
		}, "Verified publication", "Run succeeded"},
		{"cancelled recovery", []workspace.AgentEvent{
			{Type: "recovery", Data: recovery.Status{ID: "incident", Generation: 1, State: recovery.NeedsAttention, Summary: "Provider fault", UpdatedAt: time.Unix(2000, 0)}},
			{Type: "recovery", Data: recovery.Status{ID: "incident", Generation: 1, State: recovery.Cancelled, Summary: "Cancelled", UpdatedAt: time.Unix(2001, 0)}},
			{Type: "stream_token", Data: "Accepted partial evidence"},
		}, "Accepted partial evidence", "Needs attention"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": {SessionID: "root", AgentID: "root"}}
			app.AddOrchestrator("root")
			events := append([]workspace.AgentEvent{{Type: "turn_started", Data: types.TurnIdentity{Generation: 1, ModelID: "model"}}}, tc.events...)
			for i, event := range events {
				_, _ = app.Update(agent.AgentEvent{Type: event.Type, Data: event.Data, AgentID: "root", Identity: types.RuntimeEventIdentity{EventID: fmt.Sprint("event-", i), OwnerID: "root", AgentID: "root", RunID: "run", RuntimeEpoch: 1, Generation: 1, Sequence: uint64(i + 1)}})
			}
			state := app.ws.AgentMgr().Get("root").State
			for i := range 8 {
				state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
			}
			canonical := state.Messages.Messages()
			if len(canonical) == 0 {
				t.Fatal("producer envelope did not commit canonical rows")
			}
			snapshot := coreprotocol.WorkspaceSnapshot{ID: "w", Revision: 1, Sessions: []coreprotocol.SessionSnapshot{{ID: "root", AgentID: "root"}}, Agents: wsOf(app).SnapshotAgents().Agents}
			encoded, err := coreprotocol.Marshal(&snapshot)
			if err != nil {
				t.Fatal(err)
			}
			var decoded coreprotocol.WorkspaceSnapshot
			if err := coreprotocol.Decode(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			view, err := coreclient.NewWorkspaceView(nil, decoded)
			if err != nil {
				t.Fatal(err)
			}
			db := sessiondb.OpenAndMigrateAt(t.TempDir())
			defer db.Close()
			if err := sessiondb.CreateSessionRow(db, "root"); err != nil {
				t.Fatal(err)
			}
			if err := sessiondb.AppendMessageWithMeta(db, "root", provider.NewTextMessage("assistant", "persisted envelope"), 1, sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: canonical}); err != nil {
				t.Fatal(err)
			}
			stored, err := sessiondb.LoadMessagesWithMeta(db)
			if err != nil {
				t.Fatal(err)
			}
			for _, width := range []int{48, 100} {
				live, hydrated := chatprojection.NewRenderer(theme.DefaultDark()), chatprojection.NewRenderer(theme.DefaultDark())
				for _, r := range []*chatprojection.Renderer{live, hydrated} {
					r.SetSize(width, 40)
					r.SetChatAreaBounds(0, 30)
				}
				messages, revision := chatMessagesFromConversation(view.AgentMgr.Get("root").State.Messages)
				live.SyncMessages(messages, revision, view.AgentMgr.Get("root").State.Messages.SourceGeneration())
				hydrated.SyncMessages(ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(stored)), 1, 0)
				for _, expanded := range []bool{false, true} {
					left, _, _ := live.ExportMessageRows(width)
					right, _, _ := hydrated.ExportMessageRows(width)
					if len(left) != len(right) || len(left) == 0 {
						t.Fatalf("missing producer rows: live=%d hydrated=%d", len(left), len(right))
					}
					var text strings.Builder
					for i := range left {
						assertSameRows(t, tc.name, left[i], right[i])
						text.WriteString(strings.Join(left[i].Lines, "\n"))
						if !expanded && left[i].Toggle {
							live.ToggleDisclosure(left[i].ID)
							hydrated.ToggleDisclosure(right[i].ID)
						}
					}
					if !strings.Contains(text.String(), tc.want) || strings.Contains(text.String(), tc.absent) {
						t.Fatalf("wrong producer projection: %s", text.String())
					}
				}
			}
		})
	}
}

func TestV2ResetRepaintsMountedAppAndFilteredThinking(t *testing.T) {
	app := newFullTestApp(t)
	app.Tree.Visible = false
	app.width, app.height = 120, 40
	app.AddOrchestrator("root")
	state := app.ws.AgentMgr().Get("root").State
	install := func(text string) {
		t.Helper()
		if err := state.Messages.InstallDelta(workspace.ConversationDelta{Reset: true, Revision: 1, Order: []string{"same"}, Upserts: []workspace.Message{{InternalID: "same", Role: types.RoleAssistant, Content: text, Time: time.Unix(2000, 0)}}}); err != nil {
			t.Fatal(err)
		}
		app.presentAgentReduction(workspace.AgentReduction{Accepted: true, EvidenceOnly: true, ChangedAgents: []string{"root"}})
		app.paintShellFrame()
	}
	install("BEFORE SOURCE RESET")
	if !strings.Contains(chatAreaText(app), "BEFORE SOURCE RESET") {
		t.Fatal("mounted fixture did not paint old text")
	}
	install("AFTER SOURCE RESET")
	if text := chatAreaText(app); !strings.Contains(text, "AFTER SOURCE RESET") || strings.Contains(text, "BEFORE SOURCE RESET") {
		t.Fatalf("mounted App retained old source: %s", text)
	}

	// Filtered local thinking must observe the same source-generation fence,
	// independently from the complete-agent renderer's cursor.
	resetThinking := func(text string) {
		t.Helper()
		if err := state.Messages.InstallDelta(workspace.ConversationDelta{Reset: true, Revision: 1, Order: []string{"thought"}, Upserts: []workspace.Message{{InternalID: "thought", Role: types.RoleThinking, Content: text, ThinkingActive: true, Time: time.Unix(2000, 0)}}}); err != nil {
			t.Fatal(err)
		}
	}
	resetThinking("FIRST THOUGHT SOURCE")
	app.installThinkingPresentation(state)
	pane := app.Get(app.thinkingPaneID(state))
	app.syncLocalThinking(app.subPaneProjections, pane)
	resetThinking("SECOND THOUGHT SOURCE")
	app.syncLocalThinking(app.subPaneProjections, pane)
	projection := app.subPaneProjections.GetExisting(pane.ID)
	if got := projection.renderer.Messages(); len(got) != 1 || got[0].Content != "SECOND THOUGHT SOURCE" {
		t.Fatalf("filtered thought retained reset source: %+v", got)
	}
}

func TestV2CommandOutcomeProjectionOwnsNestedOutput(t *testing.T) {
	source := workspace.Message{Collaboration: &provider.CollaborationAttribution{Command: &command.Outcome{Handle: "build", Output: &command.Result{Output: "retained stderr"}}}}
	projected := chatMessageFromCanonical(source)
	projected.Collaboration.Command.Output.Output = "changed observer"
	if source.Collaboration.Command.Output.Output != "retained stderr" {
		t.Fatal("frontend projection aliases canonical command output")
	}
}
