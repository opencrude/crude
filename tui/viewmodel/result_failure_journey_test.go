package viewmodel

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	"crude/tui/chatprojection"
	"crude/tui/theme"
	"crude/types"
	"crude/workspace"
)

// These are actual failing tools, not hand-authored projection fixtures. The
// model request, live App ingress, SQLite snapshot and cold renderer must agree.
// In particular, a failed edit must not show its repair hint even on expansion.
func TestRealFileFailuresKeepHintsPrivateAndMatchAfterResume(t *testing.T) {
	var visualLive, visualResumed [][]types.ChatMessage
	for _, scenario := range []struct{ parallel, repeat bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		parallel, repeat := scenario.parallel, scenario.repeat
		t.Run(fmt.Sprintf("parallel=%v/repeat=%v", parallel, repeat), func(t *testing.T) {
			project := t.TempDir()
			path := filepath.Join(project, "target.go")
			const original = "package fixture\n\nvar Original = true\n"
			if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
				t.Fatal(err)
			}
			uses := []map[string]any{
				{"recipient_name": "functions.edit_file", "parameters": map[string]any{"path": path, "old_string": "var Missing = true", "new_string": "var Changed = true"}},
				{"recipient_name": "functions.read_file", "parameters": map[string]any{"path": filepath.Join(project, "absent.go")}},
			}
			if parallel {
				uses[0] = map[string]any{"recipient_name": "functions.apply_patch", "parameters": map[string]any{
					"input": "*** Begin Patch\n*** Update File: target.go\n@@\n-var Missing = true\n+var Changed = true\n*** End Patch\n",
				}}
			}
			var requests atomic.Int32
			nextRequest := make(chan string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				body, _ := io.ReadAll(request.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				requestIndex := requests.Add(1)
				if requestIndex > 1 {
					var requestBody struct {
						Messages json.RawMessage `json:"messages"`
					}
					_ = json.Unmarshal(body, &requestBody)
					select {
					case nextRequest <- string(requestBody.Messages):
					default:
					}
				}
				if requestIndex > 2 || requestIndex > 1 && !repeat {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"No file changed.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				var calls []map[string]any
				if parallel {
					args, _ := json.Marshal(map[string]any{"tool_uses": uses})
					calls = append(calls, map[string]any{"index": 0, "id": "batch", "type": "function", "function": map[string]any{"name": "multi_tool_use.parallel", "arguments": string(args)}})
				} else {
					for i, use := range uses {
						args, _ := json.Marshal(use["parameters"])
						calls = append(calls, map[string]any{"index": i, "id": fmt.Sprintf("file-%d", i), "type": "function", "function": map[string]any{"name": strings.TrimPrefix(use["recipient_name"].(string), "functions."), "arguments": string(args)}})
					}
				}
				encoded, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}}})
				_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", encoded)
			}))
			defer server.Close()
			app := newFullTestApp(t)
			wsOf(app).RootPath, wsOf(app).WorkspaceDir = project, t.TempDir()
			wsOf(app).SessionsByID = make(map[string]*workspace.Session)
			wsOf(app).SessionsByAgent = make(map[string]*workspace.Session)
			wsOf(app).SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
			cfg := testAccountConfig()
			if parallel {
				profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
				category := profile.Categories[session.CategoryDefault]
				category.Model = "gpt-result-test"
				profile.Categories[session.CategoryDefault] = category
				cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
			}
			account := cfg.Accounts["test"]
			account.Connection.ProviderURL = server.URL
			cfg.Accounts["test"] = account
			cfg.Agents.Enabled = false
			owner, err := wsOf(app).CreateSession(cfg, t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer wsOf(app).CloseAllSessions()
			backend := app.ws.BackendSession(owner.SessionID)
			backend.SetYolo(true) // only the test's isolated workspace
			sender := &recoveryJourneySender{events: make(chan types.AgentMsg, 512)}
			orch, err := wsOf(app).StartOrchestrator(owner.SessionID, sender)
			if err != nil {
				t.Fatal(err)
			}
			app.AddOrchestrator(orch.ID)
			if !orch.SubmitInput(provider.UserInput{Text: "Try the requested edits and report the result."}) {
				t.Fatal("input was not admitted")
			}
			timer := time.NewTimer(10 * time.Second)
			defer timer.Stop()
		events:
			for {
				select {
				case event := <-sender.events:
					if event.AgentID == "" {
						event.AgentID = orch.ID
					}
					_, _ = app.Update(event)
					if event.Type == "error" || event.Type == "recovery" {
						t.Fatalf("ordinary file failures interrupted the run: %s %v", event.Type, event.Data)
					}
					if event.Type == "done" {
						break events
					}
				case <-timer.C:
					t.Fatalf("file failure journey did not settle; requests=%d", requests.Load())
				}
			}
			wantRequests := int32(2)
			if repeat {
				wantRequests++
			}
			if requests.Load() != wantRequests {
				t.Fatalf("ordinary failures retried or ended the run: requests=%d", requests.Load())
			}
			model := <-nextRequest
			for _, want := range []string{"Hint:", "verify the exact content", "closest first"} {
				if !strings.Contains(model, want) {
					t.Fatalf("model lost %q from repair guidance", want)
				}
			}
			if !parallel && strings.Contains(model, "media-bearing user message") {
				t.Fatal("text-only hints changed the tool result protocol")
			}
			if got, err := os.ReadFile(path); err != nil || string(got) != original {
				t.Fatalf("failed edit changed the file: %q %v", got, err)
			}
			state := app.ws.AgentMgr().Get(orch.ID).State
			for i := range 8 {
				state.Messages.AdvanceToolLifecycles(time.Now().Add(time.Duration(i+1) * time.Hour))
			}
			saved, err := sessiondb.LoadMessagesWithMeta(owner.DB)
			if err != nil {
				t.Fatal(err)
			}
			live := ChatMessagesFromViews(state.Messages.Messages())
			resumed := ChatMessagesFromViews(workspace.ConvertMessagesForDisplay(saved))
			onlyTools := func(messages []types.ChatMessage) []types.ChatMessage {
				var tools []types.ChatMessage
				for _, message := range messages {
					if message.Role == types.RoleTool {
						tools = append(tools, message)
					}
				}
				return tools
			}
			live, resumed = onlyTools(live), onlyTools(resumed)
			wantCount := 2
			if parallel {
				wantCount = 1
			}
			if repeat {
				wantCount *= 2
			}
			if len(live) != wantCount || len(resumed) != wantCount {
				t.Fatalf("missing or duplicate evidence: live=%+v resumed=%+v", live, resumed)
			}
			for i := range live {
				// A transient live status may become a tool in place. Its view
				// ID is conversation-local; Outcome.CallID is the exact operation.
				if live[i].ToolOutcome.CallID == "" || live[i].ToolOutcome != resumed[i].ToolOutcome || live[i].Content != resumed[i].Content || live[i].ToolDuration != resumed[i].ToolDuration || live[i].ToolStatus != resumed[i].ToolStatus || live[i].ToolPreviewPhase != resumed[i].ToolPreviewPhase {
					t.Fatalf("result facts changed after persistence:\nlive=%+v\nsaved=%+v", live[i], resumed[i])
				}
			}
			for _, width := range []int{80, 160} {
				render := func(messages []types.ChatMessage) *chatprojection.Renderer {
					r := chatprojection.NewRenderer(theme.DefaultDark())
					r.SetSize(width, 24)
					r.SetChatAreaBounds(0, 20)
					r.SyncMessages(messages, 1, 0)
					return r
				}
				left, right := render(live), render(resumed)
				for _, expanded := range []bool{false, true} {
					before, _, _ := left.ExportMessageRows(width)
					if expanded {
						rbefore, _, _ := right.ExportMessageRows(width)
						for i, row := range before {
							left.ToggleDisclosure(row.ID)
							right.ToggleDisclosure(rbefore[i].ID)
						}
					}
					lrows, _, _ := left.ExportMessageRows(width)
					rrows, _, _ := right.ExportMessageRows(width)
					wantRows := 2
					if repeat {
						wantRows *= 2
					}
					if len(lrows) != wantRows || len(rrows) != wantRows {
						t.Fatalf("each failed operation needs one row: live=%+v resumed=%+v", lrows, rrows)
					}
					for i, row := range lrows {
						assertSameRows(t, fmt.Sprintf("%d/%v", width, expanded), row, rrows[i])
						text := strings.Join(row.Lines, "\n")
						for _, forbidden := range []string{"hint:", "closest first", "verify the exact content", "copy full lines", "do not start your search"} {
							if strings.Contains(strings.ToLower(text), forbidden) {
								t.Fatalf("agent guidance entered display/copy text: %s", text)
							}
						}
						if !strings.Contains(text, "failed") {
							t.Fatalf("failure was presented as success: %s", text)
						}
					}
				}
			}
			if !repeat {
				visualLive = append(visualLive, live)
				visualResumed = append(visualResumed, resumed)
			}
		})
	}
	if os.Getenv("CRUDE_VISUAL_RESULTS") == "1" && !t.Failed() {
		runCompactResultVisual(t, visualLive, visualResumed)
	}
}
