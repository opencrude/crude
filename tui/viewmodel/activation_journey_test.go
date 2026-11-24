package viewmodel

import (
	"context"
	"crude/internal/terminalvt"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/agent/provider"
	"crude/agent/session"
	agenttools "crude/agent/tools"
	"crude/tui/rendering"
	captypes "crude/types/capability"
	"crude/types/command"
	"crude/types/conversation"
	"crude/types/setting"
	"crude/workspace"
	"crude/workspace/fileio"
)

// TestBrokenActivationHelper is a local server whose runner installed it but
// whose command line rejects the arguments it was given.
func TestBrokenActivationHelper(t *testing.T) {
	if os.Getenv("CRUDE_BROKEN_ACTIVATION_HELPER") != "1" {
		return
	}
	_, _ = fmt.Fprintln(os.Stderr, "Installed 84 packages in 2.1s")
	_, _ = fmt.Fprintln(os.Stderr, "Usage: windows-mcp [OPTIONS] COMMAND [ARGS]...")
	_, _ = fmt.Fprintln(os.Stderr, "Error: Missing command.")
	os.Exit(2)
}

// A real composer turn through the core starts a shell job and two
// activations. Activations are jobs from their first instant: the turn ends
// while the HTTP peer still withholds initialize, the exiting local server
// settles as a failed row that leads with its own reason, and every row keeps
// its exact output reader and stop control beside the others.
func TestConnectedActivationJobControls(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	waitPeer, releasePeer := context.WithCancel(t.Context())
	var first sync.Once
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body) // Allow net/http to observe a peer closing its socket.
		first.Do(func() {
			close(started)
			select {
			case <-r.Context().Done():
				close(stopped)
			case <-waitPeer.Done():
			}
		})
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(endpoint.Close)
	t.Cleanup(releasePeer)
	var requests atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"test-model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		step := requests.Add(1)
		if step == 1 {
			_, _ = fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"shell-fixture","type":"function","function":{"name":"async_execute_command","arguments":"{\"command\":\"go test ./...\",\"handle\":\"tests\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		if step == 2 {
			_, _ = fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"activate-fixture","type":"function","function":{"name":"capability_activate","arguments":"{\"id\":\"fixture\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		if step == 3 {
			_, _ = fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"activate-broken","type":"function","function":{"name":"capability_activate","arguments":"{\"id\":\"broken\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"content":"I can continue while the server starts."},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(provider.Close)
	settings := captypes.Normalize(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{
		"fixture": {ID: "fixture", Name: "Fixture server", Transport: "http", URL: endpoint.URL, TimeoutSeconds: 120},
		"broken": {ID: "broken", Name: "Windows computer use", Transport: "stdio", Command: os.Args[0], LaunchApproved: true,
			Args: []string{"-test.run=^TestBrokenActivationHelper$"}, Environment: map[string]string{"CRUDE_BROKEN_ACTIVATION_HELPER": "1"}},
	}})
	rt := remoteJourneyRuntimeConfig(t, provider.URL, func(cfg *session.Config) { cfg.Capabilities = settings }, func(ws *workspace.Workspace) {
		defaults := ws.AppDefaults()
		defaults.Capabilities = settings
		ws.SetAppDefaults(defaults)
	})
	root := t.TempDir()
	for name, body := range map[string]string{"go.mod": "module fixture\n\ngo 1.25\n", "work_test.go": "package fixture\nimport(\"testing\";\"time\")\nfunc TestWork(t *testing.T){time.Sleep(90*time.Second)}"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	shell := newRemoteJourneyShell(t, rt, root)
	id := remoteJourneyCreate(t, shell)
	app := shell.app
	app.Tree.Visible = false
	app.ws.SetYoloForAgent(id, true)
	submit := func(text string) {
		app.setFocusInput()
		app.Input.SetInput(text)
		_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
		runAppCommand(t, app, cmd)
	}
	submit("Run the tests and activate both servers.")
	// No steering: the turn finishes on its own while the HTTP handshake is
	// still held, because activation never waits inside the tool call.
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		view := rendering.StripAnsi(app.View().RenderContent())
		return strings.Contains(view, "Background jobs") && requests.Load() >= 4 && app.activeSessionProjection().backgroundJobs.VisibleCount() == 3 &&
			strings.Contains(view, "Server exited before it answered")
	})
	select {
	case <-started:
	default:
		t.Fatal("the HTTP activation never reached its server")
	}
	select {
	case <-stopped:
		t.Fatal("ending the turn stopped a background activation")
	default:
	}
	if app.nextTickDelay(time.UnixMilli(1600)) >= workspace.IdleTickInterval {
		t.Fatal("a visible job arc was left on the idle clock")
	}
	if os.Getenv("CRUDE_VISUAL_ACTIVATION") == "1" {
		app.onboarding.trust.askedOwner, app.onboarding.trust.askedGen = app.ws, app.workspaceSwitchGen.Load()
		model := &activationCapture{App: app, evidence: os.Getenv("CRUDE_CAPTURE_EVIDENCE")}
		program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
		app.SetProgram(program)
		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case msg := <-shell.fifo:
					program.Send(msg)
				case <-ctx.Done():
					return
				}
			}
		}()
		_, err := program.Run()
		cancel()
		<-done
		app.eventBus.SetSink(remoteJourneySink{shell.fifo})
		if err != nil {
			t.Fatal(err)
		}
	}
	clickCommandDeck(t, shell, "Show auto-1 in the conversation")
	var expanded, other bool
	for _, msg := range app.ws.AgentMgr().Get(id).State.Messages.Messages() {
		if job, ok := agenttools.ParseCommandResult(msg.ToolName, msg.Content); ok && msg.ToolName == "capability_activate" {
			open := app.subPaneProjections.Existing(id).renderer.DisclosureExpanded(msg.InternalID)
			if job.OutputDescriptor.Handle == "auto-1" {
				expanded = open
			} else {
				other = other || open
			}
		}
	}
	if !expanded || other {
		t.Fatalf("the auto-1 link did not reveal exactly its own call: revealed=%v other=%v", expanded, other)
	}
	clickCommandDeck(t, shell, "Show output of auto-1")
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		for _, v := range app.commandOutputs {
			if v.id != "" {
				page, err := app.ws.Terminals().Rows(v.id, terminalvt.RowRequest{Start: -1})
				if err == nil && strings.Contains(terminalVisualText(page), "Connecting") {
					return true
				}
			}
		}
		return false
	})
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool { return len(app.commandOutputs) == 0 })
	clickCommandDeck(t, shell, "Stop auto-1")
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "Stopped")
	})
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not cancel the original MCP handshake")
	}
	// The other row must still offer its exact stop control after this one settles.
	clickCommandDeck(t, shell, "Stop tests")

}

type activationCapture struct {
	*App
	evidence string
	phase    string
	arc      int
	changes  int
	since    time.Time
	ready    bool
	header   bool
}

// Inspect the last painted job row without consuming a frame. Require more
// than one full rotation in order, with no step left on the idle clock.
func (m *activationCapture) motionReady() bool {
	if m.ready || m.evidence == "" {
		return false
	}
	phase := m.phase
	if phase == "" {
		phase = "Connecting"
	}
	for _, g := range m.mainPanes.groups {
		if g.view == nil || g.deck.Empty() || g.view.deck.surf == nil {
			continue
		}
		buf := g.view.deck.surf.Buffer()
		for y := 0; y < buf.Height(); y++ {
			var text strings.Builder
			for _, c := range buf.Row(y) {
				text.WriteRune(c.Char)
			}
			row := text.String()
			if !strings.Contains(row, phase) || !strings.Contains(row, "Output") {
				continue
			}
			for i, glyph := range []rune("◜◝◞◟") {
				if !strings.ContainsRune(row, glyph) {
					continue
				}
				now := time.Now()
				if m.since.IsZero() || i != m.arc {
					if !m.since.IsZero() && (m.arc+1)%4 == i && now.Sub(m.since) < 300*time.Millisecond {
						m.changes++
					} else {
						m.changes = 0
					}
					m.arc, m.since = i, now
				}
				m.ready = m.changes >= 5
				return m.ready
			}
		}
	}
	return false
}

func (*activationCapture) Init() tea.Cmd { return tickCmd() }
func (m *activationCapture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok && key.Code == tea.KeyF9 {
		return m, tea.Quit
	}
	_, cmd := m.App.Update(msg)
	if !m.header && m.chatReflowInnerW > 0 {
		for _, row := range m.activeMessageList().ExportMessageRowsSnapshot(m.chatReflowInnerW).Rows {
			if len(row.Lines) >= 3 && strings.TrimSpace(row.Lines[2]) == "Keep the download visible." && len(row.Native) >= 3 {
				m.header = row.Native[1].HeaderRule && strings.HasPrefix(row.Lines[1], "Tester ")
				break
			}
		}
	}
	if m.motionReady() {
		return m, tea.Batch(cmd, func() tea.Msg { return os.WriteFile(m.evidence, []byte("motion\n"), 0600) })
	}
	return m, cmd
}

// TestMeasuredJobMotionVisualAcceptance uses the same shell/PanelDeck paint
// path with a held download snapshot. The real transfer counters and connected
// activation controls are exercised separately by their owning journeys.
func TestMeasuredJobMotionVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_JOB_MOTION") != "1" {
		t.Skip("set CRUDE_VISUAL_JOB_MOTION to drive the native capture")
	}
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	owner := &workspace.Session{SessionID: "job-motion", Settings: workspace.NewSessionSettingsState(setting.Settings{}, setting.Settings{}, nil)}
	wsOf(app).Username = "Tester"
	selectDeckSession(t, app, owner, "job-motion-agent")
	state := app.ws.AgentMgr().Get("job-motion-agent").State
	stamp := time.Now()
	state.Messages.AppendMessage(conversation.Message{Role: conversation.RoleAssistant, Content: "I am checking the jobs.", Time: stamp})
	state.Messages.AppendMessage(conversation.Message{Role: conversation.RoleUser, Origin: provider.OriginPeer,
		Collaboration: &provider.CollaborationAttribution{Kind: "command_outcome", Command: &command.Outcome{Handle: "checks", Status: "failed", ExitCode: 1, ExitKnown: true}}, Time: stamp})
	state.Messages.AppendMessage(conversation.Message{Role: conversation.RoleUser, Origin: provider.OriginHumanSteering,
		Collaboration: &provider.CollaborationAttribution{Kind: "human_steering", SenderID: "human", SenderName: "Tester"},
		Content:       "Keep the download visible.", Time: stamp})
	app.syncAgentMessages(state.ID, true)
	app.Tree.Visible = false
	app.onboarding.trust.askedOwner, app.onboarding.trust.askedGen = app.ws, app.workspaceSwitchGen.Load()
	now := time.Now()
	app.updateSessionBackgroundJobs("job-motion-agent", []fileio.AsyncCmdInfo{
		{Handle: "tests", Command: "go test ./...", Status: fileio.AsyncRunning, StartedAt: now.Add(-4 * time.Second)},
		{Handle: "auto-1", TaskID: "connect", Command: "Fixture server", Status: fileio.AsyncRunning, StartedAt: now.Add(-3 * time.Second), Progress: command.Progress{Phase: "Connecting"}},
		{Handle: "auto-2", TaskID: "download", Command: "Review guide", Status: fileio.AsyncRunning, StartedAt: now.Add(-2 * time.Second), Progress: command.Progress{Phase: "Downloading skill", Current: 42, Total: 100}},
		{Handle: "auto-3", TaskID: "failed", Command: "Local server", Status: fileio.AsyncFailed, StartedAt: now.Add(-time.Second), EndedAt: now, Error: "Missing command. Check the launch arguments."},
	}, now)
	model := &activationCapture{App: app, evidence: os.Getenv("CRUDE_CAPTURE_EVIDENCE"), phase: "Downloading"}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	if !model.ready {
		t.Fatal("native shell did not paint five consecutive arc steps")
	}
	if !model.header {
		t.Fatal("native shell did not paint the human author header above the admitted prompt")
	}
}
