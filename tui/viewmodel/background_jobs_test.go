package viewmodel

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/provider"
	"crude/session"
	"crude/tui/rendering"
	"crude/tui/screens/backgroundjobs"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

func TestBackgroundJobMouseTerminationKeepsOtherJobsAndSessionAlive(t *testing.T) {
	helper := fmt.Sprintf(`CRUDE_JOB_UI_HELPER=1 %q -test.run=^TestBackgroundJobCommandHelperProcess$`, os.Args[0])
	if runtime.GOOS == "windows" {
		helper = fmt.Sprintf(`set "CRUDE_JOB_UI_HELPER=1"&&"%s" -test.run=^TestBackgroundJobCommandHelperProcess$`, os.Args[0])
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodGet {
			_, _ = fmt.Fprint(writer, `{"data":[{"id":"test-model"}]}`)
			return
		}
		writer.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			var calls []map[string]any
			for index, handle := range []string{"selected-job", "neighbor-job"} {
				arguments, _ := json.Marshal(map[string]any{"command": helper, "handle": handle})
				calls = append(calls, map[string]any{"index": index, "id": handle, "type": "function", "function": map[string]any{"name": "async_execute_command", "arguments": string(arguments)}})
			}
			chunk, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]any{"tool_calls": calls}, "finish_reason": "tool_calls"}}})
			_, _ = fmt.Fprintf(writer, "data: %s\n\ndata: [DONE]\n\n", chunk)
			return
		}
		_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"Session remains usable\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	app := newFullTestApp(t)
	wsOf(app).Logger = &testLogger{}
	wsOf(app).RootPath, wsOf(app).WorkspaceDir = t.TempDir(), t.TempDir()
	wsOf(app).SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
	config := testAccountConfig()
	account := config.Accounts["test"]
	template := account.Connection
	template.ProviderURL = server.URL
	account.Connection = template
	config.Accounts["test"] = account
	profile := config.Agents.Profiles[config.Agents.ActiveProfile]
	main := profile.Categories[session.CategoryDefault]
	main.ModelSupportsTools = true
	profile.Categories[session.CategoryDefault] = main
	config.Agents.Profiles[config.Agents.ActiveProfile] = profile
	config.Memory = types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}
	wsOf(app).SessionsByID = make(map[string]*workspace.Session)
	wsOf(app).SessionsByAgent = make(map[string]*workspace.Session)
	owner, err := wsOf(app).CreateSession(config, home)
	if err != nil {
		t.Fatal(err)
	}
	defer wsOf(app).CloseAllSessions()
	sender := &latencyEventSender{ctx: t.Context(), events: make(chan types.AgentEventMsg, 4096)}
	orchestrator, err := wsOf(app).StartOrchestrator(owner.SessionID, sender)
	if err != nil {
		t.Fatal(err)
	}
	app.AddOrchestrator(orchestrator.ID)
	backend := app.ws.BackendSession(owner.SessionID)
	backend.SetYolo(true)
	if !backend.SubmitInput(orchestrator.ID, provider.PlainInput("start independent commands")) {
		t.Fatal("turn not admitted")
	}
	commands := make(map[string]fileio.AsyncCmdInfo)
	consume := func(event types.AgentEventMsg) {
		app.handleAgentEvent(event)
		if event.Type == "error" {
			t.Fatalf("backend error: %v", event.Data)
		}
		if event.Type == "async_command_status" {
			for _, command := range event.Data.([]fileio.AsyncCmdInfo) {
				commands[command.Handle] = command
			}
		}
	}
	deadline := time.After(10 * time.Second)
	for len(commands) != 2 {
		select {
		case event := <-sender.events:
			consume(event)
		case <-deadline:
			t.Fatal("background commands did not appear")
		}
	}
	app.width, app.height = 120, 35
	app.compositor.Resize(app.width, app.height)
	app.Tree.Visible = false
	projection := app.activeSessionProjection()
	app.View()
	var button ui.Component
	for _, child := range projection.backgroundJobs.Children() {
		if strings.Contains(child.Semantics().Label, "selected-job") {
			button = child
		}
	}
	if button == nil {
		t.Fatal("selected command has no terminate button")
	}
	bounds, _ := projection.deckHost.ScreenRect(button.ID())
	click := tea.MouseClickMsg{X: app.sessionDeckRect.X + bounds.X + 1, Y: app.sessionDeckRect.Y + bounds.Y, Button: tea.MouseLeft}
	_, command := app.dispatchMouseClick(click)
	if command == nil {
		t.Fatal("mouse activation dropped termination command")
	}
	if !projection.deckFocused {
		t.Fatal("button click failed to retain keyboard focus")
	}
	app.dispatchMouseRelease(tea.MouseReleaseMsg{X: click.X, Y: click.Y, Button: tea.MouseLeft})
	app.ActiveSession = &workspace.Session{SessionID: "other-session"}
	command()
	if app.ActiveSession == owner {
		t.Fatal("inactive termination changed the active session")
	}
	app.ActiveSession = owner
	for commands["selected-job"].Status != fileio.AsyncKilled {
		select {
		case event := <-sender.events:
			consume(event)
		case <-deadline:
			t.Fatal("selected command was not terminated")
		}
	}
	if commands["neighbor-job"].Status != fileio.AsyncRunning || app.ws.AgentMgr().Get(orchestrator.ID) == nil {
		t.Fatal("termination killed another command or its session")
	}
	app.forceFrame()
	view := app.View()
	painted := rendering.StripAnsi(view.Content)
	if !strings.Contains(painted, "KILLED") || !strings.Contains(painted, "neighbor-job") {
		t.Fatalf("owning panel did not show the individual terminal status:\n%s", painted)
	}
	if !backend.SubmitInput(orchestrator.ID, provider.PlainInput("continue this session")) {
		t.Fatal("terminated command made session unable to accept another turn")
	}
	for requests.Load() < 3 || orchestrator.TurnActive() {
		select {
		case event := <-sender.events:
			consume(event)
		case <-time.After(time.Millisecond):
		case <-deadline:
			t.Fatal("session follow-up turn did not finish")
		}
	}
}

func TestBackgroundJobCommandHelperProcess(t *testing.T) {
	if os.Getenv("CRUDE_JOB_UI_HELPER") == "1" {
		time.Sleep(30 * time.Second)
	}
}

func TestBackgroundJobRejectsWrongProjectionOrSettledRow(t *testing.T) {
	app := newFullTestApp(t)
	owner := &workspace.Session{SessionID: "session"}
	app.ws.AgentMgr().AddOrchestrator("root", &workspace.AgentState{})
	wsOf(app).SessionsByAgent = map[string]*workspace.Session{"root": owner}
	app.ActiveSession = owner
	projection := app.activeSessionProjection()
	job := backgroundjobs.Job{OwnerID: "root", Handle: "job", PID: 123, StartedAt: time.Now(), Status: "running"}
	if app.terminateBackgroundJob(backgroundjobs.TerminateRequest{Target: backgroundjobs.New(app.Theme), Job: job}) != nil {
		t.Fatal("wrong projection submitted termination")
	}
	job.Status = "completed"
	if app.terminateBackgroundJob(backgroundjobs.TerminateRequest{Target: projection.backgroundJobs, Job: job}) != nil {
		t.Fatal("settled row submitted termination")
	}
	job.Status, job.OwnerID = "running", "missing"
	if app.terminateBackgroundJob(backgroundjobs.TerminateRequest{Target: projection.backgroundJobs, Job: job}) != nil {
		t.Fatal("missing owner submitted termination")
	}
}
