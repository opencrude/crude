package viewmodel

import (
	"fmt"
	"strings"
	"testing"

	"crude/provider"
	"crude/sessiondb"
	"crude/tui/cell"
	"crude/tui/ui"
	ctypes "crude/types"
	"crude/types/reasoning"
	"crude/workspace"
)

func specialChatBackgroundCases() []struct {
	name     string
	messages []workspace.Message
	expand   string
	want     []string
} {
	return []struct {
		name     string
		messages []workspace.Message
		expand   string
		want     []string
	}{
		{
			name: "peer message",
			messages: []workspace.Message{{
				InternalID: "peer", Role: ctypes.RoleUser, Origin: provider.OriginPeer,
				Content: "Parser evidence is ready.",
				Collaboration: &provider.CollaborationAttribution{
					SenderID: "child", SenderName: "Asha", RecipientIDs: []string{"root"},
					RecipientNames: []string{"Rebecca"}, Kind: "peer_reply",
				},
			}},
			want: []string{"Asha → Rebecca", "Parser evidence"},
		},
		{
			name: "send",
			messages: []workspace.Message{{
				InternalID: "send", Role: ctypes.RoleTool, ToolName: "agent_send", ToolStatus: ctypes.ToolStatusSuccess,
				ToolInput: `{"recipient_ids":["child"],"kind":"note","body":"Please review parser ownership."}`,
				Content:   `{"event":{"id":"note-1","sequence":1,"sender_id":"root","sender_name":"Rebecca","recipients":[{"id":"child","name":"Asha"}],"origin":"peer","kind":"peer_note","body":"Please review parser ownership."},"deliveries":[{"id":"delivery-1","recipient_id":"child","state":"pending","version":1}]}`,
			}},
			want: []string{"Sent workspace message", "review parser"},
		},
		{
			name: "board",
			messages: []workspace.Message{{
				InternalID: "board", Role: ctypes.RoleTool, ToolName: "board_read", ToolStatus: ctypes.ToolStatusSuccess,
				ToolInput: `{"since":0}`,
				Content:   `{"events":[{"sender_name":"Asha","recipients":[{"id":"root","name":"Rebecca"}],"kind":"peer_reply","body":"Board evidence is ready."}],"cursor":1,"snapshot":1}`,
			}},
			want: []string{"Read workspace history", "Board evidence"},
		},
		{
			name: "expanded thought",
			messages: []workspace.Message{{
				InternalID: "thought", Role: ctypes.RoleThinking, Content: "Thought evidence stays readable.",
			}},
			expand: "thought",
			want:   []string{"Thought", "Thought evidence"},
		},
		{
			name: "summary",
			messages: []workspace.Message{{
				InternalID: "summary", Role: ctypes.RoleThinking, Content: "Summary evidence stays readable.",
				ThinkingDisplay: reasoning.Display{Kind: reasoning.Summary},
			}},
			expand: "summary",
			want:   []string{"Thought", "Summary evidence"},
		},
		{
			name: "mixed activity tree",
			messages: []workspace.Message{
				{InternalID: "read", Role: ctypes.RoleTool, ToolName: "read_file", ToolArgs: "ordinary.go", ToolStatus: ctypes.ToolStatusSuccess, Content: "ordinary evidence"},
				{InternalID: "board", Role: ctypes.RoleTool, ToolName: "board_read", ToolStatus: ctypes.ToolStatusSuccess, ToolInput: `{"since":0}`, Content: `{"events":[{"sender_name":"Asha","kind":"peer_reply","body":"Grouped peer evidence."}]}`},
				{InternalID: "thought", Role: ctypes.RoleThinking, Content: "Grouped thought evidence."},
			},
			expand: "activity:read",
			want:   []string{"Read workspace history", "Grouped peer evidence", "└─ ● Thought"},
		},
	}
}

// Assert the real shell's painted margins, not just a background flag or a
// matching text snapshot. SQLite recovery must paint the very same cells.
func TestSpecialChatBlocksPaintFullWidthAndResumeIdentically(t *testing.T) {
	for _, preset := range []string{"dark", "light"} {
		for _, width := range []int{48, 160} {
			for _, c := range specialChatBackgroundCases() {
				t.Run(fmt.Sprintf("%s/%d/%s", preset, width, c.name), func(t *testing.T) {
					app := newFullTestApp(t)
					app.width, app.height = width, 60
					app.ApplyThemePreset(preset)
					app.AddOrchestrator("background-owner")
					state := app.ws.AgentMgr().Get("background-owner").State
					state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant, Content: "Regular chat before"})
					for _, message := range c.messages {
						state.Messages.AppendMessage(message)
					}
					state.Messages.AppendMessage(workspace.Message{Role: ctypes.RoleAssistant, Content: "Regular chat after"})
					app.syncAgentMessages(state.ID, false)
					renderer := app.subPaneProjections.GetExisting(state.ID).renderer
					if c.expand != "" {
						renderer.ToggleDisclosure(c.expand)
					}
					app.paintShellFrame()
					live := app.mainSurf.Buffer().Clone()
					tokens := ui.NewTokenSet(app.Theme)
					left := app.chatRect.X
					right := left + app.chatScreen.VirtualList().Bounds().W - 1
					for _, expected := range c.want {
						assertSpecialChatRowEdges(t, app, expected, tokens.Color(ui.ColorSubPaneBg), left, right)
					}
					for _, expected := range []string{"Regular chat before", "Regular chat after", "ordinary.go"} {
						if c.name != "mixed activity tree" && expected == "ordinary.go" {
							continue
						}
						assertSpecialChatRowEdges(t, app, expected, tokens.Color(ui.ColorBg), left, right)
					}

					database := sessiondb.OpenAndMigrateAt(t.TempDir())
					defer database.Close()
					if err := sessiondb.CreateSessionRow(database, state.ID); err != nil {
						t.Fatal(err)
					}
					for _, snapshot := range state.Messages.Messages() {
						wire := provider.NewTextMessage("assistant", snapshot.Content)
						if err := sessiondb.AppendMessageWithMeta(database, state.ID, wire, 1, sessiondb.DisplayMeta{
							SnapshotVersion: sessiondb.ConversationSnapshotVersion,
							Snapshots:       []ctypes.ConversationMessage{snapshot},
						}); err != nil {
							t.Fatal(err)
						}
					}
					stored, err := sessiondb.LoadMessagesWithMeta(database)
					if err != nil {
						t.Fatal(err)
					}
					recovered := newFullTestApp(t)
					recovered.width, recovered.height = width, 60
					recovered.ApplyThemePreset(preset)
					recovered.AddOrchestrator(state.ID)
					resumedState := recovered.ws.AgentMgr().Get(state.ID).State
					for _, snapshot := range workspace.ConvertMessagesForDisplay(stored) {
						resumedState.Messages.AppendMessage(snapshot)
					}
					recovered.syncAgentMessages(state.ID, false)
					if c.expand != "" {
						recovered.subPaneProjections.GetExisting(state.ID).renderer.ToggleDisclosure(c.expand)
					}
					recovered.paintShellFrame()
					resumed := recovered.mainSurf.Buffer()
					for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H-2; y++ {
						for x := left; x <= right; x++ {
							if got, want := resumed.Get(x, y), live.Get(x, y); got != want {
								t.Fatalf("resumed cell (%d,%d)=%+v, live=%+v", x, y, got, want)
							}
						}
					}
				})
			}
		}
	}
}

func assertSpecialChatRowEdges(t *testing.T, app *App, text string, bg cell.Color, left, right int) {
	t.Helper()
	for y := app.chatRect.Y; y < app.chatRect.Y+app.chatRect.H; y++ {
		if !strings.Contains(shellBufferRow(app, y), text) {
			continue
		}
		for _, x := range []int{left, right} {
			if got := app.mainSurf.Buffer().Get(x, y).BG; got != bg {
				t.Errorf("%q edge (%d,%d) background=%v, want %v", text, x, y, got, bg)
			}
		}
		return
	}
	t.Fatalf("missing painted %q:\n%s", text, bufferPlain(app.mainSurf.Buffer()))
}

func TestLiveThinkingPaneBackgroundIncludesMargins(t *testing.T) {
	for _, promoted := range []bool{false, true} {
		t.Run(fmt.Sprint(promoted), func(t *testing.T) {
			app := newFullTestApp(t)
			app.width, app.height = 80, 40
			app.AddOrchestrator("thinking-owner")
			trace := "Inspecting the current evidence."
			if promoted {
				trace = strings.Repeat(trace+"\n", 40)
			}
			app.handleAgentEvent(ctypes.AgentEventMsg{AgentID: "thinking-owner", Type: "thinking",
				Data: reasoning.Delta{Text: trace, Kind: reasoning.Raw}})
			app.paintShellFrame()
			state := app.ws.AgentMgr().Get("thinking-owner").State
			if promoted != (app.thinkingPaneID(state) != "") {
				t.Fatal("fixture did not exercise the expected thinking presentation")
			}
			bg := ui.NewTokenSet(app.Theme).Color(ui.ColorSubPaneBg)
			left := app.chatRect.X
			right := left + app.chatScreen.VirtualList().Bounds().W - 1
			assertSpecialChatRowEdges(t, app, "Thinking", bg, left, right)
			assertSpecialChatRowEdges(t, app, "Inspecting the current evidence", bg, left, right)
		})
	}
}
