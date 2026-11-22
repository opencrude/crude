package viewmodel

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/types/conversation"
	"crude/workspace"
)

func launcherApp(t *testing.T) *App {
	t.Helper()
	app := newTestApp(t)
	app.AgentView.ClearActive()
	app.ws.AgentMgr().Remove("agent-1")
	app.width, app.height = 120, 30
	app.openLauncher()
	app.paintShellFrame()
	if !app.shellSlotActive(shellLauncherSlot) {
		t.Fatal("precondition: launcher slot active")
	}
	return app
}

func notices(app *App) string {
	var out []string
	for _, record := range app.ws.Status().Notifications(true) {
		out = append(out, record.Summary)
	}
	return strings.Join(out, "\n")
}

// A slow core held a requested session for 8 s. The launcher looked idle and
// further clicks were dropped silently. While a requested session opens, the
// New Session action shows progress; another request says to wait; the action
// returns when the session settles. The same holds when the user came to the
// launcher by hiding every agent or by restarting the shell.
func TestLauncherShowsSessionStartUntilItSettles(t *testing.T) {
	app := launcherApp(t)
	text := bufferPlain(app.mainSurf.Buffer())
	if !strings.Contains(text, "New Session") || strings.Contains(text, "Starting session") {
		t.Fatalf("idle launcher:\n%s", text)
	}
	// A request is in flight: the core has not answered yet.
	app.sessionLifecyclePending = true
	app.sessionLifecycleSequence++
	app.noteSessionRequested()
	app.paintShellFrame()
	text = bufferPlain(app.mainSurf.Buffer())
	if !strings.Contains(text, "Starting session") || !strings.Contains(text, "Please wait") || strings.Contains(text, "enter \u21b5") && strings.Count(text, "enter \u21b5") > 1 {
		t.Fatalf("pending launcher does not show progress:\n%s", text)
	}
	if cmd := app.remoteSessionCommand(""); cmd != nil {
		t.Fatal("a second request started another session while one was opening")
	}
	if !strings.Contains(notices(app), "A session is already starting") {
		t.Fatalf("the second request was dropped silently; notices: %q", notices(app))
	}
	app.applyRemoteSession(remoteSessionReadyMsg{owner: app.ws, sequence: app.sessionLifecycleSequence,
		result: &workspace.NewSessionResult{Error: "core unavailable"}})
	app.paintShellFrame()
	text = bufferPlain(app.mainSurf.Buffer())
	if strings.Contains(text, "Starting session") || !strings.Contains(text, "New Session") {
		t.Fatalf("settled launcher still shows progress:\n%s", text)
	}
}

// A request that waits for the automatic Errand open is also visible: it is
// the user's request, not background work.
func TestLauncherShowsSessionRequestQueuedBehindErrand(t *testing.T) {
	app := launcherApp(t)
	app.sessionLifecyclePending, app.errandEnsureInFlight = true, true
	app.paintShellFrame()
	if text := bufferPlain(app.mainSurf.Buffer()); strings.Contains(text, "Starting session") {
		t.Fatalf("the automatic Errand open alone shows user progress:\n%s", text)
	}
	if cmd := app.remoteSessionCommand(""); cmd != nil {
		t.Fatal("request did not wait for the Errand open")
	}
	app.paintShellFrame()
	if text := bufferPlain(app.mainSurf.Buffer()); !strings.Contains(text, "Starting session") {
		t.Fatalf("queued request shows no progress:\n%s", text)
	}
}

// Away from the launcher, a session that takes longer than a second tells the
// user to wait, once.
func TestSlowSessionStartNoticeOutsideLauncher(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("first")
	app.paintShellFrame()
	if app.launcherHostVisible() {
		t.Fatal("precondition: an agent pane is visible")
	}
	app.sessionLifecyclePending = true
	app.sessionLifecycleSequence++
	app.noteSessionRequested()
	start := app.sessionRequestedAt
	app.noticeSlowSessionStart(start.Add(500 * 1e6))
	if strings.Contains(notices(app), "Starting the session") {
		t.Fatal("a fast start was announced")
	}
	app.noticeSlowSessionStart(start.Add(1500 * 1e6))
	app.noticeSlowSessionStart(start.Add(2500 * 1e6))
	if got := strings.Count(notices(app), "Starting the session. Please wait."); got != 1 {
		t.Fatalf("slow start notices = %d, want 1: %q", got, notices(app))
	}
}

// The typing caret follows the last streamed character in the real shell
// paint, also after a completed paragraph whose row is padded to the width.
func TestStreamingCaretPaintsAfterTheLastToken(t *testing.T) {
	app := newFullTestApp(t)
	app.width, app.height = 120, 30
	app.AddOrchestrator("first")
	app.ws.AgentMgr().Get("first").State.Messages.AppendMessage(conversation.Message{Role: conversation.RoleUser, Content: "check the prune"})
	app.handleAgentEvent(AgentEventMsg{AgentID: "first", Type: "stream_token", Data: "I am checking whether the history prune is the cause, then I will set it aside.\n\n"})
	app.paintShellFrame()
	buf := app.mainSurf.Buffer()
	for y := 0; y < buf.Height(); y++ {
		row := shellBufferRow(app, y)
		if i := strings.Index(row, "set it aside."); i >= 0 {
			if !strings.Contains(row, "set it aside.\u2588") {
				t.Fatalf("caret is not after the last token: %q", row)
			}
			return
		}
	}
	t.Fatalf("streamed text not painted:\n%s", bufferPlain(buf))
}

// TestSessionStartNativeCapture shows the same states in a dedicated terminal.
// F4 checks the launcher progress, F5 streams an answer, F6 checks the caret
// and F9 exits. Run the compiled test binary through scripts/tuishot.ps1 with
// CRUDE_VISUAL_SESSION_START set to the proof file.
func TestSessionStartNativeCapture(t *testing.T) {
	out := os.Getenv("CRUDE_VISUAL_SESSION_START")
	if out == "" {
		t.Skip("requires tuishot")
	}
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	app.AgentView.ClearActive()
	app.openLauncher()
	app.sessionLifecyclePending = true
	app.sessionLifecycleSequence++
	app.noteSessionRequested()
	v := &sessionStartCapture{App: app}
	p := tea.NewProgram(v)
	app.SetProgram(p)
	defer app.SetProgram(nil)
	if _, err := p.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
	if !v.launcher || !v.caret {
		t.Fatalf("native capture: launcher progress=%t caret=%t", v.launcher, v.caret)
	}
	if err := os.WriteFile(out, []byte("PASS\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

type sessionStartCapture struct {
	*App
	launcher, caret bool
}

func (v *sessionStartCapture) Init() tea.Cmd { return tickCmd() }
func (v *sessionStartCapture) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if k, ok := msg.(tea.KeyPressMsg); ok {
		switch k.Code {
		case tea.KeyF4:
			text := bufferPlain(v.mainSurf.Buffer())
			v.launcher = strings.Contains(text, "Starting session") && strings.Contains(text, "Please wait")
			return v, nil
		case tea.KeyF5:
			v.sessionLifecyclePending = false
			v.AddOrchestrator("capture")
			v.ws.AgentMgr().Get("capture").State.Messages.AppendMessage(conversation.Message{Role: conversation.RoleUser, Content: "Why did the agent button freeze?"})
			v.handleAgentEvent(AgentEventMsg{AgentID: "capture", Type: "stream_token", Data: "Local History retention held its only database connection, so the workspace waited. I will set it aside.\n\n"})
			return v, nil
		case tea.KeyF6:
			v.caret = strings.Contains(bufferPlain(v.mainSurf.Buffer()), "set it aside.\u2588")
			return v, nil
		case tea.KeyF9:
			return v, tea.Quit
		}
	}
	_, cmd := v.App.Update(msg)
	return v, cmd
}
