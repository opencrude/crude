package viewmodel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/screens/shellchat"
	"crude/tui/theme"
	"crude/tui/ui"
	ctypes "crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

type structuredProjectionCase struct {
	name, input, result, preview, detail string
}

func structuredProjectionCases(t *testing.T) []structuredProjectionCase {
	t.Helper()
	encode := func(value any) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	message := collaboration.Event{ID: "message-1", WorkspaceID: "/project", Sequence: 1,
		SenderName: "Patrick", SenderID: "patrick", Origin: collaboration.OriginPeer, Kind: collaboration.EventWorkRequest,
		Recipients: []collaboration.Recipient{{ID: "olivia", Name: "Olivia", State: collaboration.AgentAvailable, Presence: collaboration.RuntimeAttached}},
		Body:       "The full regression suite passed; all Board previews preserve peer attribution.",
		ThreadID:   "message-1", RequestID: "review-1", RequestRevision: 2,
		References: []collaboration.Reference{{Kind: "file", ID: "board_test.go"}}}
	return []structuredProjectionCase{
		{"agent_send", `{"recipient_ids":["olivia"],"kind":"request","body":"The full regression suite passed; all Board previews preserve peer attribution."}`, encode(collaboration.PublishResult{Event: message, Deliveries: []collaboration.Delivery{{ID: "delivery-1", RecipientID: "olivia", State: collaboration.DeliveryPending, Version: 1}}}), "regression suite passed", "board_test.go"},
		{"board_read", `{"since":0}`, encode(collaboration.Page{Events: []collaboration.Event{message}, Cursor: 1, Snapshot: 2, LatestSequence: 2, HasMore: true}), "Patrick → Olivia", "Request revision"},
		{"remember", `{"title":"Keep exact provenance","kind":"lesson","content":"Keep complete assertions with exact validation provenance.","evidence":[{"kind":"validation","session_id":"s1","tool_call_id":"validated-99","outcome":"succeeded","summary":"Suite passed"}]}`, `{"id":"memory-1","revision":1,"kind":"lesson","title":"Keep exact provenance","status":"remembered"}`, "complete assertions", "validated-99"},
		{"recall_memory", `{"query":"provenance"}`, `{"total":1,"results":[{"id":"memory-1","revision":2,"title":"Keep exact provenance","kind":"lesson","content":"Retain exact source and outcome evidence in memory.","evidence_count":1,"evidence":[{"kind":"validation","session_id":"s1","tool_call_id":"validated-99","outcome":"succeeded","summary":"Suite passed"}]}]}`, "exact source and outcome", "validated-99"},
		{"update_memory", `{"id":"memory-1","expected_revision":2,"content":"The corrected assertion keeps full validation evidence."}`, `{"id":"memory-1","revision":3,"previous_revision":2,"status":"updated"}`, "corrected assertion", "Previous revision"},
		{"forget_memory", `{"id":"memory-1","expected_revision":3}`, `{"id":"memory-1","revision":4,"status":"forgotten"}`, "history retained", "Expected revision"},
		{"memory_feedback", `{"items":[{"id":"memory-1","revision":2,"kind":"useful","evidence":[{"kind":"validation","session_id":"s1","outcome":"succeeded","summary":"accepted-evidence-97"}]}]}`, `{"status":"recorded","count":1}`, "useful", "accepted-evidence-97"},
		{"search_history", `{"query":"projection"}`, `{"sessions":[{"session_id":"s1","title":"Projection work","summary":"Kept Board evidence readable.","key_files":["projection.go"],"outcome":"completed"}],"turns":[{"session_id":"s1","turn":2,"user":"Improve shared Board chat","assistant":"Delivered the shared result renderer","tools":["board_read"],"files":["board.go"]}]}`, "Board evidence readable", "Delivered the shared result renderer"},
		{"capability_search", `{"query":"source review"}`, encode([]captypes.Entry{{ID: "review-skill", Name: "Source Review", Kind: captypes.KindSkill, Description: "Review exact source evidence before proposing changes.", Installed: true, Version: "revision-abc", Source: "local", State: "ready"}}), "exact source evidence", "revision-abc"},
		{"capability_activate", `{"id":"review-skill"}`, "<skill source=\"review-skill\" revision=\"revision-abc\" digest=\"digest-99\">\nReview source evidence before proposing changes.\n</skill>", "Review source evidence", "digest-99"},
		{"mcp_request_access", `{"connection_id":"docs","grants":[{"tool":"lookup","fingerprint":"observed-schema-99","effect":"read","scope":"public documentation"}]}`, encode(captypes.ActivationResult{ConnectionID: "docs", State: "connected", Next: "No usable tools until exact access is approved.", Tools: []captypes.Tool{{Name: "mcp__docs__lookup", OriginalName: "lookup", Fingerprint: "observed-schema-99", Description: "Read exact documentation", Reason: "Needs explicit effect and scope grant"}}}), "No usable tools", "observed-schema-99"},
		{"mcp_read_resource", `{"connection_id":"docs","uri":"docs://guide"}`, "The guide keeps external attribution and full document text.", "external attribution", "docs://guide"},
		{"mcp_get_prompt", `{"connection_id":"docs","name":"review","arguments":{"topic":"Board"}}`, "assistant: Review the Board evidence, not claimed permissions.", "Board evidence", "topic"},
		{"mcp__docs__lookup", `{"query":"Board"}`, `{"answer":"Read all attributed Board findings","items":[{"path":"guide.md","revision":7}]}`, "attributed Board findings", "guide.md"},
	}
}

// Exercise the actual App event ingress, canonical SQLite storage, passive
// hydration, native rows and compact child projections. Parity alone is not
// enough: every case independently asserts useful content and full evidence.
func TestStructuredToolFamilyLiveSQLiteHydrationParity(t *testing.T) {
	for _, themeCase := range []struct {
		name string
		th   *theme.Theme
	}{{"dark", theme.DefaultDark()}, {"light", theme.DefaultLight()}} {
		for _, width := range []int{48, 80, 160} {
			t.Run(fmt.Sprintf("%s/%d", themeCase.name, width), func(t *testing.T) {
				app := newFullTestApp(t)
				app.AddOrchestrator("structured-owner")
				state := app.ws.AgentMgr().Get("structured-owner").State
				database := sessiondb.OpenAndMigrateAt(t.TempDir())
				defer database.Close()
				if err := sessiondb.CreateSessionRow(database, state.ID); err != nil {
					t.Fatal(err)
				}
				for caseIndex, c := range structuredProjectionCases(t) {
					t.Run(c.name, func(t *testing.T) {
						id := fmt.Sprintf("call-%s-%d", c.name, caseIndex)
						for _, evt := range []ctypes.AgentEventMsg{
							{AgentID: state.ID, Type: "tool_call", Data: map[string]any{"tool_id": id, "internal_id": id, "name": c.name, "args": c.input}},
							{AgentID: state.ID, Type: "tool_result", Data: map[string]any{"tool_id": id, "internal_id": id, "name": c.name, "result": c.result}},
						} {
							app.handleAgentEvent(evt)
						}
						for i := range 8 {
							state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Second))
						}
						messages := state.Messages.Messages()
						snapshot := messages[len(messages)-1]
						snapshot.ToolDuration = 0
						if snapshot.ToolInput != c.input {
							t.Fatalf("live event lost exact input: %q", snapshot.ToolInput)
						}
						wire := provider.NewTextMessage("tool", c.result)
						wire.ToolCallID = id
						if err := sessiondb.AppendMessageWithMeta(database, state.ID, wire, 1, sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{snapshot}}); err != nil {
							t.Fatal(err)
						}
						loaded, err := sessiondb.LoadMessagesWithMeta(database)
						if err != nil {
							t.Fatal(err)
						}
						live := ChatMessagesFromViews([]workspace.ChatMessageView{snapshot})
						hydrated := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(loaded[len(loaded)-1:]))
						render := func(input []ctypes.ChatMessage) *chatprojection.Renderer {
							r := chatprojection.NewRenderer(themeCase.th)
							r.SetSize(width, 40)
							r.SetChatAreaBounds(0, 30)
							r.SyncMessages(input, 1)
							return r
						}
						left, right := render(live), render(hydrated)
						for _, expanded := range []bool{false, true} {
							if expanded {
								left.ToggleDisclosure(id)
								right.ToggleDisclosure(id)
							}
							lrows, _, _ := left.ExportMessageRows(width)
							rrows, _, _ := right.ExportMessageRows(width)
							if len(lrows) != 1 || len(rrows) != 1 || !lrows[0].Toggle || !rrows[0].Toggle {
								t.Fatalf("missing rows/disclosure: live=%+v hydrated=%+v", lrows, rrows)
							}
							assertSameRows(t, fmt.Sprint(expanded), lrows[0], rrows[0])
							text := strings.Join(lrows[0].Lines, "\n")
							// Wrapping inserts chrome/newlines, not ellipses or lost words.
							plain := strings.Join(strings.Fields(strings.ReplaceAll(text, "│", " ")), " ")
							want := c.preview
							if expanded {
								want = c.detail
								if !strings.Contains(text, "Request") {
									t.Fatalf("request evidence inaccessible: %s", text)
								}
							}
							if !strings.Contains(plain, want) {
								t.Fatalf("missing useful %q: %s", want, text)
							}
							if strings.Contains(text, `{"`) || strings.Contains(text, `"recipient_availability"`) || strings.Contains(text, "<skill ") || strings.Contains(text, "…") {
								t.Fatalf("bare envelope or arbitrary elision: %s", text)
							}
						}
						lp, _ := chatprojection.BuildSubPaneToolLines([]*ctypes.ChatMessage{&live[0]}, width, 0)
						rp, _ := chatprojection.BuildSubPaneToolLines([]*ctypes.ChatMessage{&hydrated[0]}, width, 0)
						if len(lp) != len(rp) {
							t.Fatal("compact child projection changed on resume")
						}
						for i := range lp {
							if lp[i].PlainText() != rp[i].PlainText() {
								t.Fatal("compact child rows differ")
							}
						}
					})
				}
			})
		}
	}
}

func TestStructuredFamilyParallelLeavesSharePresentation(t *testing.T) {
	for _, c := range structuredProjectionCases(t) {
		t.Run(c.name, func(t *testing.T) {
			input := fmt.Sprintf(`{"tool_uses":[{"recipient_name":%q,"parameters":%s}]}`, "functions."+c.name, c.input)
			output, err := json.Marshal(map[string]any{"results": []map[string]any{{"recipient_name": c.name, "result": c.result, "error": false}}})
			if err != nil {
				t.Fatal(err)
			}
			r, resumed := newParityRenderer(), newParityRenderer()
			batch := ctypes.ChatMessage{InternalID: "batch", Role: ctypes.RoleTool, ToolName: "multi_tool_use.parallel", ToolInput: input, Content: string(output), ToolStatus: "success", ToolPreviewPhase: "done"}
			r.SyncMessages([]ctypes.ChatMessage{batch}, 1)
			views := workspace.ConvertMessagesForDisplay([]sessiondb.MessageWithMeta{{Meta: sessiondb.DisplayMeta{SnapshotVersion: sessiondb.ConversationSnapshotVersion, Snapshots: []ctypes.ConversationMessage{workspaceMessageFromChat(batch)}}}})
			resumed.SyncMessages(ChatMessagesFromViews(views), 1)
			rows, _, _ := r.ExportMessageRows(200)
			rrows, _, _ := resumed.ExportMessageRows(200)
			assertSameRows(t, "batch compact", rows[0], rrows[0])
			text := strings.Join(rows[0].Lines, "\n")
			if !strings.Contains(text, c.preview) || strings.Contains(text, `{"`) {
				t.Fatalf("batch lost useful semantic preview: %s", text)
			}
			r.ToggleDisclosure("batch:0")
			resumed.ToggleDisclosure("batch:0")
			rows, _, _ = r.ExportMessageRows(200)
			rrows, _, _ = resumed.ExportMessageRows(200)
			assertSameRows(t, "batch expanded", rows[0], rrows[0])
			if !strings.Contains(strings.Join(rows[0].Lines, "\n"), c.detail) {
				t.Fatalf("batch evidence inaccessible: %+v", rows)
			}
		})
	}
}

func TestStructuredFamilyFailureKeepsNeutralEvidence(t *testing.T) {
	for _, c := range structuredProjectionCases(t) {
		p := projectionCase{tool: c.name, rawArgs: c.input, content: `{"error":"access_denied","message":"Exact denial reason"}`, isError: true}
		live, hydrated := renderLive(t, "failed", p, true)[0], renderHydrated(t, "failed", p, true)[0]
		assertSameRows(t, c.name, live, hydrated)
		text := strings.Join(live.Lines, "\n")
		if strings.Contains(text, `{"error"`) || !strings.Contains(text, "Exact denial reason") {
			t.Fatalf("failure result is raw or unavailable: %s", text)
		}
		for _, row := range live.Native {
			for _, span := range row.Spans {
				if strings.Contains(span.Text, "Exact denial reason") && span.Role != ui.ColorFg {
					t.Fatal("failure evidence inherited red action style")
				}
			}
		}
	}
}

func TestStructuredFamilyPaintClickAndCopyJourney(t *testing.T) {
	for _, c := range structuredProjectionCases(t) {
		t.Run(c.name, func(t *testing.T) {
			const width = 160
			th := theme.DefaultDark()
			renderer := chatprojection.NewRenderer(th)
			renderer.SetSize(width, 40)
			renderer.SetChatAreaBounds(0, 30)
			renderer.SyncMessages(liveToolMessages(t, "journey", projectionCase{tool: c.name, rawArgs: c.input, content: c.result}), 1)
			rows, _, h := renderer.ExportMessageRows(width)
			screen := shellchat.New(th)
			mount := func() {
				screen.SetMessages(rows, shellchat.PaintParams{Compose: shellchat.ComposeParams{InnerW: width - th.Spacing.PadLR*2, PadLR: th.Spacing.PadLR, Theme: th}})
				screen.SyncScrollMetrics(h, h)
				screen.Layout(ui.LayoutContext{}, ui.Rect{W: width, H: h})
			}
			mount()
			painted := bufferPlain(screen.PaintTestBuffer(width, h))
			if !strings.Contains(painted, c.preview) {
				t.Fatalf("useful compact evidence did not paint: %s", painted)
			}
			ctx := ui.EventContext{LocalX: th.Spacing.PadLR + 3, LocalY: rows[0].ToggleLine}
			_ = screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
			clicked := screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
			if clicked.Action == nil || clicked.Action.ID != chatprojection.ChatToggleActionID {
				t.Fatal("painted disclosure did not route a click")
			}
			renderer.ToggleDisclosure(clicked.Action.Payload.(chatprojection.ChatTogglePayload).MessageID)
			rows, _, h = renderer.ExportMessageRows(width)
			mount()
			painted = bufferPlain(screen.PaintTestBuffer(width, h))
			if !strings.Contains(painted, c.detail) {
				t.Fatalf("expanded evidence did not paint: %s", painted)
			}
			ctx.LocalX, ctx.LocalY = th.Spacing.PadLR, 1
			_ = screen.Handle(ctx, ui.MouseClickEvent(tea.MouseClickMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
			ctx.LocalX, ctx.LocalY = width-1, h-1
			_ = screen.Handle(ctx, ui.MouseMotionEvent(tea.MouseMotionMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
			selected := screen.SelectedText()
			if !strings.Contains(selected, c.detail) || strings.Contains(selected, `{"`) {
				t.Fatalf("selection lost readable evidence: %q", selected)
			}
			released := screen.Handle(ctx, ui.MouseReleaseEvent(tea.MouseReleaseMsg{X: ctx.LocalX, Y: ctx.LocalY, Button: tea.MouseLeft}))
			if released.Cmd == nil || released.Action != nil {
				t.Fatal("drag must copy selected text, not toggle disclosure")
			}
		})
	}
}
