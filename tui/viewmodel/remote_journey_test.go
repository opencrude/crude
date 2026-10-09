package viewmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/clientstate"
	"crude/core"
	"crude/coreclient"
	"crude/history"
	"crude/provider"
	"crude/session"
	"crude/shellport"
	"crude/shellport/remote"
	questionscreen "crude/tui/screens/question"
	"crude/types"
	"crude/workspace"
)

type remoteJourneySink struct{ messages chan any }

func (s remoteJourneySink) Send(msg any) { s.messages <- msg }

type remoteJourneyShell struct {
	app  *App
	fifo chan any
}

// Each shell has a real remote port and a private FIFO. Only Update installs
// subscription batches; neither the fixture nor the provider fabricates nodes.
func newRemoteJourneyShell(t *testing.T, runtime *core.Runtime, root string) remoteJourneyShell {
	t.Helper()
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		server, client := net.Pipe()
		go func() { _ = core.ServeConnection(t.Context(), runtime, server) }()
		return coreclient.Dial(ctx, client, id)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	port, err := remote.Attach(ctx, remote.Options{Root: root, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	app := NewApp(port, nil)
	app.SetPromptHistory(history.New())
	fifo := make(chan any, 1024)
	app.eventBus.SetSink(remoteJourneySink{fifo})
	port.StartReplica()
	app.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	t.Cleanup(func() {
		app.StopAsyncIO()
		if err := app.CloseWorkspaceAttachment(); err != nil {
			t.Error(err)
		}
	})
	return remoteJourneyShell{app, fifo}
}

func remoteJourneyUntil(t *testing.T, shells []remoteJourneyShell, condition func() bool) {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	tick := time.NewTicker(time.Millisecond)
	defer tick.Stop()
	for {
		for _, shell := range shells {
			for draining := true; draining; {
				select {
				case msg := <-shell.fifo:
					_, cmd := shell.app.Update(msg)
					runAppCommand(t, shell.app, cmd)
				default:
					draining = false
				}
			}
		}
		if condition() {
			return
		}
		select {
		case <-deadline.C:
			for _, shell := range shells {
				t.Logf("active=%s draft=%q pending=%d", shell.app.AgentView.ActiveID(), shell.app.Input.Value(), len(shell.app.pendingInputAdmissions))
				for _, node := range shell.app.ws.AgentMgr().AllNodes() {
					t.Logf("node=%s phase=%+v status=%s", node.ID, shell.app.ws.AgentActivity(node.ID), node.State.Status)
					for _, message := range node.State.Messages.Messages() {
						t.Logf("message=%+v", message)
					}
				}
				t.Logf("paint=%s", ansi.Strip(shell.app.View().Content))
			}
			t.Fatal("remote App journey did not reach its user-visible state")
		case <-tick.C:
		}
	}
}

func remoteJourneyRuntime(t *testing.T, endpoint string) *core.Runtime {
	t.Helper()
	home := t.TempDir()
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		if info, err := os.Stat(root); err != nil || !info.IsDir() {
			return nil, fmt.Errorf("invalid destination %s", root)
		}
		ws := workspace.NewWorkspace(root, "test-model", "tester", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}})
		agents := session.DefaultAgentsConfig()
		off := false
		profile := agents.Profiles["default"]
		profile.MultiAgent = &off
		profile.Categories["default"] = session.CategoryConfig{AccountID: "test", Model: "test-model", ModelSupportsTools: true, ModelContextWindow: 200_000}
		profile.Categories[session.CategoryAeron] = session.CategoryConfig{AccountID: "test", Model: "test-model", ModelContextWindow: 200_000, ModelMaxOutput: 2_048}
		agents.Profiles["default"] = profile
		ws.SetBackendConfig(&session.Config{Agents: agents, Memory: types.MemorySettings{AutoRecall: "off", AutoExtract: "off", Consolidation: "off"}, Accounts: map[string]provider.Account{"test": {ID: "test", ProviderID: "openai", APIKey: "test", Connection: provider.Template{Name: "test", ProviderURL: endpoint, APIStandard: "openai", AuthType: provider.AuthProtocol}}}}, home)
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.SetPromptHistory(history.New(), filepath.Join(home, "history.json")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	})
	return runtime
}

func remoteJourneyCreate(t *testing.T, shell remoteJourneyShell) string {
	t.Helper()
	cmd := shell.app.launcherNewSession()
	if cmd == nil {
		t.Fatal("launcher did not schedule remote session creation")
	}
	msg := cmd().(remoteSessionReadyMsg)
	if msg.result == nil || msg.result.Error != "" {
		t.Fatalf("create: %+v", msg.result)
	}
	// Deliberately apply the receipt before reading the replica FIFO.
	if shell.app.ws.AgentMgr().Get(msg.result.OrchID) != nil {
		t.Fatal("command installed inventory outside Update")
	}
	shell.app.Update(msg)
	if !shell.app.pendingOrchestrators[msg.result.OrchID] {
		t.Fatal("early acknowledgement was not retained")
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return shell.app.AgentView.ActiveID() == msg.result.OrchID })
	assertAgentIsPaintedView(t, shell.app, msg.result.OrchID)
	return msg.result.OrchID
}

func TestRemoteAppJourneySharedStreamPrivateDraftsAndDetach(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"shared prefix \"}}]}\n\n")
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"retained answer\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return two.app.AgentView.ActiveID() == id })
	assertAgentIsPaintedView(t, two.app, id)
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "drafts.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Store closes after both App teardown barriers (registered below).
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	for i, shell := range []remoteJourneyShell{one, two} {
		attachment, err := store.Acquire(t.Context(), fmt.Sprintf("shell-%d", i), clientstate.AcquireOptions{})
		if err != nil {
			t.Fatal(err)
		}
		if err := shell.app.ConfigureComposerDrafts(store, attachment, shell.app.ws.(shellport.Connection).WorkspaceID()); err != nil {
			t.Fatal(err)
		}
		runAppCommand(t, shell.app, shell.app.hydrateComposerDraftCmd())
		shell.app.Input.SetInput(fmt.Sprintf("private draft %d", i))
		shell.app.flushComposerDraft()
		if shell.app.composerDraftErr != nil {
			t.Fatal(shell.app.composerDraftErr)
		}
		t.Cleanup(func() { shell.app.StopAsyncIO(); shell.app.composerDraftClient = nil })
	}
	if one.app.ActiveSession.SessionID != two.app.ActiveSession.SessionID {
		t.Fatal("same root has different sessions")
	}
	one.app.Input.SetInput("say retained answer")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool {
		return strings.Contains(ansi.Strip(two.app.View().Content), "shared prefix") && strings.Contains(ansi.Strip(one.app.View().Content), "shared prefix")
	})
	if two.app.Input.Value() != "private draft 1" {
		t.Fatal("submission consumed the peer draft")
	}
	if err := one.app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	close(release)
	remoteJourneyUntil(t, []remoteJourneyShell{two}, func() bool { return strings.Contains(ansi.Strip(two.app.View().Content), "retained answer") })
	if calls.Load() != 1 {
		t.Fatalf("provider executed %d requests, want exactly one", calls.Load())
	}
	for i, shell := range []remoteJourneyShell{one, two} {
		c := shell.app.composerDraftClient
		record, err := store.Load(c.attachment.ClientID, c.attachment.Epoch, clientstate.Key{ClientID: c.attachment.ClientID, WorkspaceID: c.workspaceID, SessionID: shell.app.ActiveSession.SessionID})
		if err != nil {
			t.Fatal(err)
		}
		if i == 1 && record.Draft.Text != "private draft 1" {
			t.Fatalf("peer persisted draft: %+v", record)
		}
	}
}

func TestRemoteAppJourneyPeerRootFocusAndWorkspaceSwitch(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	one, two := newRemoteJourneyShell(t, runtime, root), newRemoteJourneyShell(t, runtime, root)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return len(one.fifo) == 0 && len(two.fifo) == 0 })
	path := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(path, []byte("editor must retain focus"), 0600); err != nil {
		t.Fatal(err)
	}
	cmd, err := two.app.openEditorTab(path)
	if err != nil {
		t.Fatal(err)
	}
	runAppCommand(t, two.app, cmd)
	// Connected opens are scheduled through Update's editor queue. Complete the
	// real asynchronous open before measuring whether a later peer root steals it.
	runAppCommand(t, two.app, two.app.drainLiveEditorCommands())
	if two.app.EditorState.Tabs[path] == nil {
		t.Fatal("asynchronous editor open did not complete")
	}
	two.app.setFocusEditor()
	before := two.app.focusTarget()
	id := remoteJourneyCreate(t, one)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return two.app.ws.AgentMgr().Get(id) != nil })
	assertEditorStillPainted(t, two.app, path)
	if two.app.focusTarget() != before {
		t.Fatal("peer-created root stole editor focus")
	}
	old := one.app.ws
	runAppCommand(t, one.app, one.app.launcherOpenFolder(filepath.Join(t.TempDir(), "missing")))
	if one.app.ws != old {
		t.Fatal("failed destination replaced old attachment")
	}
	// Dismiss the operational failure before the next explicit action.
	one.app.closeWorkspaceBoundOverlays()
	next := t.TempDir()
	preparedSwitch := one.app.launcherOpenFolder(next)
	result := preparedSwitch().(remoteWorkspaceReadyMsg)
	one.app.Input.SetInput("asynchronous newer draft")
	_, closePrepared := one.app.Update(result)
	runAppCommand(t, one.app, closePrepared)
	if one.app.ws != old || one.app.Input.Value() != "asynchronous newer draft" {
		t.Fatal("late attach consumed newer draft")
	}
	one.app.closeWorkspaceBoundOverlays()
	runAppCommand(t, one.app, one.app.launcherOpenFolder(next))
	if one.app.ws == old || one.app.ws.RootPath() != next {
		t.Fatal("switch did not attach destination")
	}
	if two.app.ws.RootPath() != root || two.app.ws.AgentMgr().Get(id) == nil {
		t.Fatal("switch destroyed peer workspace/root")
	}
	assertEditorStillPainted(t, two.app, path)
	runAppCommand(t, one.app, one.app.launcherOpenFolder(root))
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return one.app.AgentView.ActiveID() == id })
	assertAgentIsPaintedView(t, one.app, id)
}

func TestRemoteAppJourneyQuestionResolvedFromPeerOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
			return
		}
		calls.Add(1)
		answered := false
		for _, message := range input.Messages {
			answered = answered || message.Role == "tool"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if !answered {
			args := `{"questions":[{"header":"Choice","question":"Choose a fixture answer","options":["Yes","No"]}]}`
			payload, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "fixture-question", "type": "function", "function": map[string]any{"name": "question", "arguments": args}}}}, "finish_reason": "tool_calls"}}})
			fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", payload)
		} else {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"PEER DECISION ACCEPTED\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return two.app.AgentView.ActiveID() == id })
	one.app.Input.SetInput("Ask a fixture question")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return len(one.app.agentPrompts) == 1 && len(two.app.agentPrompts) == 1 })
	left, right := one.app.agentPrompts[0].request, two.app.agentPrompts[0].request
	if left.blockID == "" || left.blockID != right.blockID || left.agentID != right.agentID {
		t.Fatal("clients received different pending blocks")
	}
	_, answerCmd := two.app.Update(questionscreen.ResultMsg{Target: two.app.agentPrompts[0].question, Result: questionscreen.Result{Answers: map[string]string{"Choice": "Yes"}, Questions: map[string]string{"Choice": "Choose a fixture answer"}}})
	runAppCommand(t, two.app, answerCmd)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool {
		return len(one.app.agentPrompts) == 0 && len(two.app.agentPrompts) == 0 && strings.Contains(ansi.Strip(one.app.View().Content), "PEER DECISION ACCEPTED") && strings.Contains(ansi.Strip(two.app.View().Content), "PEER DECISION ACCEPTED")
	})
	if calls.Load() != 2 {
		t.Fatalf("expected exactly one question request and one continuation, got %d", calls.Load())
	}
}

func TestRemoteAppJourneyPeerStopRetainsAcceptedContent(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ACCEPTED BEFORE PEER STOP\"}}]}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	runtime := remoteJourneyRuntime(t, server.URL)
	root := t.TempDir()
	one := newRemoteJourneyShell(t, runtime, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, runtime, root)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return two.app.AgentView.ActiveID() == id })
	one.app.Input.SetInput("Wait for peer stop")
	one.app.setFocusInput()
	_, cmd := one.app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, one.app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return strings.Contains(ansi.Strip(two.app.View().Content), "ACCEPTED BEFORE PEER STOP") })
	two.app.cancelAgentTurn(id)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool {
		return !one.app.ws.AgentTurnActive(id) && !two.app.ws.AgentTurnActive(id) && strings.Contains(ansi.Strip(one.app.View().Content), "Stopped") && strings.Contains(ansi.Strip(two.app.View().Content), "Stopped")
	})
	if !strings.Contains(ansi.Strip(one.app.View().Content), "ACCEPTED BEFORE PEER STOP") || calls.Load() != 1 {
		t.Fatal("stop lost accepted output or repeated provider execution")
	}
	one.app.Input.SetInput("new draft must survive refused steering")
	runAppCommand(t, one.app, one.app.submitSteering(id, one.app.Input.Value(), nil))
	if one.app.Input.Value() != "new draft must survive refused steering" || calls.Load() != 1 {
		t.Fatal("refused steering consumed the draft or started another provider request")
	}
}
