package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"crude/collaboration"
	"crude/provider"
	"crude/session"
	"crude/sessiondb"
	ttypes "crude/types"
	"crude/workspace"
)

// Exercise the exact regression: start with delegation disabled, finish a
// normal turn, enable it in Settings, then launch children through model tool
// calls. Hand-crafted sub_agent_started events cannot catch missing parentage.
func TestEnableSubAgentsMidSessionSurfacesChildren(t *testing.T) {
	t.Run("automatic delivery", func(t *testing.T) {
		var parentRequests atomic.Int32
		var childRequests atomic.Int32
		var parentAgentID atomic.Value
		childFollowup := make(chan []provider.ChatMessage, 1)
		releaseChildren := make(chan struct{})
		var releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseChildren) }) }
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{"data":[{"id":"parent-model"},{"id":"child-model"}]}`)
				return
			}
			var request struct {
				Model    string                 `json:"model"`
				Messages []provider.ChatMessage `json:"messages"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				if r.Context().Err() == nil {
					t.Errorf("%s %s: %v", r.Method, r.URL.Path, err)
				}
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if request.Model == "child-model" {
				select {
				case <-releaseChildren:
				case <-r.Context().Done():
					return
				}
				if childRequests.Add(1) == 1 {
					reportArgs, _ := json.Marshal(map[string]any{
						"recipient_ids": []string{parentAgentID.Load().(string)},
						"kind":          "final",
						"summary":       "Child evidence",
						"body":          "child evidence",
					})
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"report-1\",\"type\":\"function\",\"function\":{\"name\":\"publish_report\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(reportArgs))
					return
				}
				childFollowup <- request.Messages
			} else if parentRequests.Add(1) == 2 {
				args, _ := json.Marshal(map[string]any{"name": "visible-child", "description": "Reply with child evidence.", "category": "researcher"})
				_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"spawn-1\",\"type\":\"function\",\"function\":{\"name\":\"agent_start\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(args))
				return
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"child evidence\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}))
		defer server.Close()
		// Release handlers before closing the server even when an assertion fails.
		defer release()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		off := false
		cfg := &session.Config{
			Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", Label: "Test", APIKey: "test", Connection: provider.Template{Name: "OpenAI", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthProtocol}}},
			Agents: session.AgentsConfig{Enabled: false, MaxConcurrent: 2, ActiveProfile: "default", Profiles: map[string]session.ProfileConfig{"default": {MultiAgent: &off, Categories: map[string]session.CategoryConfig{
				session.CategoryDefault:    {AccountID: "test", Model: "parent-model", ModelSupportsTools: true},
				session.CategoryResearcher: {AccountID: "test", Model: "child-model", ModelSupportsTools: true},
			}}}},
		}
		app := newFullTestApp(t)
		wsOf(app).RootPath, wsOf(app).WorkspaceDir = t.TempDir(), t.TempDir()
		// The shell fixture bypasses Workspace.New; install the real accepted
		// document owner before CreateSession constructs its execution service.
		wsOf(app).LiveDocuments = workspace.NewDocumentService(wsOf(app).RootPath, wsOf(app).Documents, wsOf(app).LSPMgr)
		defer wsOf(app).LiveDocuments.Close()
		wsOf(app).Logger = &testLogger{}
		wsOf(app).SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "off"}})
		wsOf(app).SessionsByID = make(map[string]*workspace.Session)
		wsOf(app).SessionsByAgent = make(map[string]*workspace.Session)
		owner, err := wsOf(app).CreateSession(cfg, home)
		if err != nil {
			t.Fatal(err)
		}
		defer wsOf(app).CloseAllSessions()
		sender := &latencyEventSender{ctx: t.Context(), events: make(chan ttypes.AgentEventMsg, 4096)}
		orch, err := wsOf(app).StartOrchestrator(owner.SessionID, sender)
		if err != nil {
			t.Fatal(err)
		}
		parentAgentID.Store(orch.ID)
		app.AddOrchestrator(orch.ID)
		backend := app.ws.BackendSession(owner.SessionID)
		if label := app.ws.AgentMgr().Get(orch.ID).Label; label == "" || app.Win.TabByTitle(app.agentTabTitle(orch.ID)).DisplayTitle != label {
			t.Fatal("root tab did not render the allocated first name")
		}
		backend.SetYolo(true)
		if !backend.SubmitInput(orch.ID, provider.PlainInput("initial turn without delegation")) {
			t.Fatal("initial turn not admitted")
		}
		deadline := time.After(10 * time.Second)
		for orch.TurnActive() {
			select {
			case evt := <-sender.events:
				app.handleAgentEvent(evt)
				if evt.Type == "error" {
					t.Fatalf("backend error: %v", evt.Data)
				}
			case <-time.After(time.Millisecond):
			case <-deadline:
				t.Fatal("initial turn timed out")
			}
		}
		settings := backend.SettingsSnapshot()
		on := true
		settings.SessionMultiAgent = &on
		settings.Agents.Enabled = true
		if err := wsOf(app).HandleSettingsSaveForSession(owner.SessionID, settings); err != nil {
			t.Fatal(err)
		}
		if !backend.SubmitInput(orch.ID, provider.PlainInput("launch the child now")) {
			t.Fatal("delegation turn not admitted")
		}
		childID := ""
		deadline = time.After(10 * time.Second)
		for childID == "" {
			select {
			case evt := <-sender.events:
				app.handleAgentEvent(evt)
				if evt.Type == "error" {
					t.Fatalf("backend error: %v", evt.Data)
				}
				if evt.Type == "sub_agent_started" {
					childID = evt.AgentID
					data, _ := evt.Data.(map[string]string)
					if data["parent_id"] != orch.ID {
						t.Fatalf("child start parent=%q, want %q", data["parent_id"], orch.ID)
					}
				}
			case <-deadline:
				t.Fatalf("child start event missing after enabling delegation; requests=%d; messages=%+v", parentRequests.Load(), backend.MessagesWithMeta())
			}
		}
		node := app.ws.AgentMgr().Get(childID)
		if node == nil || node.Parent == nil || node.Parent.ID != orch.ID {
			t.Fatal("running child missing from agent tree")
		}
		if !agentTabPresent(app, childID) {
			t.Fatal("running child missing from tabs")
		}
		rootLabel := app.ws.AgentMgr().Get(orch.ID).Label
		childLabel := node.Label
		if !strings.HasSuffix(node.Label, " sub "+rootLabel) || node.Label == " sub "+rootLabel || app.Win.TabByTitle(app.agentTabTitle(childID)).DisplayTitle != node.Label {
			t.Fatalf("child task hint replaced personal identity/parentage: %q", node.Label)
		}
		if app.ws.PaneMgr().Get(childID) == nil {
			t.Fatal("running child missing from conversation panes")
		}
		app.switchToAgentView(childID)
		assertAgentIsPaintedView(t, app, childID)
		// Disabling while the child is awaiting its provider must leave its
		// real tab, identity, and cancellation/review surface intact.
		settings = backend.SettingsSnapshot()
		settings.SessionMultiAgent = &off
		settings.Agents.Enabled = false
		if err := wsOf(app).HandleSettingsSaveForSession(owner.SessionID, settings); err != nil {
			t.Fatal(err)
		}
		if !agentTabPresent(app, childID) || app.ws.AgentMgr().Get(childID) != node {
			t.Fatal("disabling delegation hid or replaced the active child")
		}
		steering := provider.InputWithRefs("include the exact parser edge case", nil)
		steering.InternalID = "child-steering-1"
		if !app.ws.SteerAgent(childID, steering) {
			t.Fatal("human steering could not target the exact active child")
		}
		// Let the real child finish, then verify its retained conversation and DB.
		release()
		deadline = time.After(10 * time.Second)
		completed := false
		for !completed {
			select {
			case evt := <-sender.events:
				app.handleAgentEvent(evt)
				if evt.Type == "error" {
					t.Fatalf("backend error: %v", evt.Data)
				}
				completed = evt.Type == "sub_agent_completed" && evt.AgentID == childID
			case <-deadline:
				data, _ := os.ReadFile(filepath.Join(backend.SessionDir(), "session.log"))
				t.Fatalf("child completion event missing: %s", data)
			}
		}
		backend.SubAgentRunner().Wait()
		select {
		case messages := <-childFollowup:
			found := false
			for _, message := range messages {
				content := message.ContentString()
				if strings.Contains(content, `origin="human"`) &&
					strings.Contains(content, `kind="human_steering"`) &&
					strings.Contains(content, "include the exact parser edge case") {
					found = true
				}
			}
			if !found {
				t.Fatalf("child follow-up lacked attributed human steering: %+v", messages)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("child did not receive human steering at its safe boundary")
		}
		node = app.ws.AgentMgr().Get(childID)
		if node == nil || !node.State.Completed {
			t.Fatal("completed child missing from retained agent list")
		}
		found := false
		for _, message := range node.State.Messages.Messages() {
			if strings.Contains(message.Content, "child evidence") {
				found = true
			}
		}
		if !found {
			t.Fatal("child evidence missing from retained conversation")
		}
		rows, err := sessiondb.ListSubAgentResults(backend.DB(), backend.SessionID())
		if err != nil || len(rows) < 1 || rows[len(rows)-1].AgentID != childID || rows[len(rows)-1].Status != "completed" {
			t.Fatalf("persisted children=%+v, err=%v", rows, err)
		}
		snapshots := backend.CompletedSubAgentSnapshots()
		if len(snapshots) != 1 || len(snapshots[0].Messages) < 2 {
			t.Fatalf("child history snapshots=%+v", snapshots)
		}
		persistedSteering := false
		for _, message := range snapshots[0].Messages {
			if message.Msg.InternalID == steering.InternalID &&
				message.Msg.EffectiveOrigin() == provider.OriginHumanSteering &&
				message.Msg.Collaboration != nil &&
				message.Msg.Collaboration.Kind == string(collaboration.EventHumanSteering) {
				persistedSteering = true
			}
		}
		if !persistedSteering {
			t.Fatal("child transcript lost admitted human steering provenance")
		}
		if snapshots[0].Result.Name != childLabel || rows[len(rows)-1].AgentName != childLabel {
			t.Fatal("saved child history lost personal identity and parentage")
		}
		registered, err := app.ws.WorkspaceDB().ListSessions()
		if err != nil || len(registered) != 1 || registered[0].AgentName.String != rootLabel {
			t.Fatalf("previous-session registry lost root name: %+v err=%v", registered, err)
		}
	})
}
