package viewmodel

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent/session"
	"crude/core/shellport"
	"crude/internal/terminalvt"
	"crude/tui/rendering"
	"crude/tui/screens/backgroundjobs"
	"crude/tui/ui"
	"crude/workspace"
)

// A real owned process with deterministic output gates, not sleeps for ordering.
func TestCommandOutputHelper(t *testing.T) {
	addr := os.Getenv("CRUDE_TEST_OUTPUT_ADDR")
	if addr == "" {
		return
	}
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		os.Exit(2)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(90 * time.Second))
	_, _ = fmt.Print("\x1b[32mREADY\x1b[0m\n")
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		switch scanner.Text() {
		case "history":
			for i := range 80 {
				_, _ = fmt.Printf("output-line-%03d\n", i)
			}
		case "live":
			_, _ = fmt.Print("progress 10%\rprogress 90%\nLIVE_MARKER\n")
		case "exit":
			_, _ = fmt.Print("FINAL_MARKER\n")
			return
		}
	}
}

type outputCloseRecorder struct {
	shellport.TerminalService
	closed chan string
}

func (r *outputCloseRecorder) Close(id string) { r.closed <- id }

func TestCommandOutputLateOpenReleasesOnlyItsOriginalService(t *testing.T) {
	app := newFullTestApp(t)
	original := &outputCloseRecorder{closed: make(chan string, 1)}
	replacement := &outputCloseRecorder{closed: make(chan string, 1)}
	old := &commandOutputWindow{key: "same-command", ws: app.ws, service: original, generation: app.workspaceSwitchGen.Load(), cancel: func() {}}
	newer := &commandOutputWindow{key: old.key, ws: app.ws, service: replacement, generation: old.generation, cancel: func() {}}
	app.commandOutputs = map[string]*commandOutputWindow{old.key: newer}
	app.applyCommandOutputOpened(commandOutputOpenedMsg{view: old, info: workspace.TerminalInfo{ID: "old-view"}})
	select {
	case id := <-original.closed:
		if id != "old-view" {
			t.Fatal("late open closed a different reader")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("late open leaked its resource")
	}
	select {
	case <-replacement.closed:
		t.Fatal("late open was retargeted to a replacement service")
	default:
	}
	if app.commandOutputs[old.key] != newer {
		t.Fatal("late open replaced the newer UI intent")
	}
}

func commandOutputJourney(t *testing.T) (remoteJourneyShell, net.Conn, string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv("CRUDE_TEST_OUTPUT_ADDR", listener.Addr().String())
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"test-model"}]}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			args, _ := json.Marshal(map[string]any{"command": fmt.Sprintf("%q -test.run=^TestCommandOutputHelper$ -test.timeout=90s", os.Args[0]), "handle": "build"})
			chunk, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "build-call", "type": "function", "function": map[string]any{"name": "async_execute_command", "arguments": string(args)}}}}, "finish_reason": "tool_calls"}}})
			_, _ = fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", chunk)
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"The build is running.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)
	rt := remoteJourneyRuntimeConfig(t, server.URL, func(config *session.Config) {
		if runtime.GOOS == "windows" {
			config.Agents.Shell = "cmd"
		}
	})
	shell := newRemoteJourneyShell(t, rt, t.TempDir())
	id := remoteJourneyCreate(t, shell)
	app := shell.app
	app.Tree.Visible = false
	app.ws.SetYoloForAgent(id, true)
	app.Input.SetInput("Start the build.")
	app.setFocusInput()
	_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		app.View()
		p := app.activeSessionProjection()
		return p != nil && p.backgroundJobs.VisibleCount() == 1
	})
	select {
	case conn := <-accepted:
		t.Cleanup(func() { _ = conn.Close() })
		return shell, conn, id
	case <-time.After(8 * time.Second):
		t.Fatal("the real background command did not connect")
	}
	return shell, nil, ""
}

func clickCommandDeck(t *testing.T, shell remoteJourneyShell, label string) {
	t.Helper()
	app := shell.app
	app.View()
	projection := app.activeSessionProjection()
	for _, child := range projection.backgroundJobs.Children() {
		if child.Semantics().Label != label {
			continue
		}
		r, ok := projection.deckHost.ScreenRect(child.ID())
		if !ok || r.Empty() {
			t.Fatal("deck control is not mounted")
		}
		x, y := app.sessionDeckRect.X+r.X+1, app.sessionDeckRect.Y+r.Y
		_, cmd := app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
		runAppCommand(t, app, cmd)
		_, cmd = app.Update(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
		runAppCommand(t, app, cmd)
		return
	}
	t.Fatalf("missing deck control %q", label)
}

func TestConnectedCommandOutputAndChatReveal(t *testing.T) {
	shell, process, agentID := commandOutputJourney(t)
	app := shell.app
	clickCommandDeck(t, shell, "Show build in the conversation")
	renderer := app.subPaneProjections.Existing(agentID).renderer
	expanded := false
	for _, msg := range app.ws.AgentMgr().Get(agentID).State.Messages.Messages() {
		if msg.ToolName == "async_execute_command" {
			expanded = renderer.DisclosureExpanded(msg.InternalID)
		}
	}
	if !expanded {
		t.Fatal("the real deck link did not expand the originating call")
	}
	// An exact link action captured before a tab switch must restore its
	// owner, even if that owner's tab was minimized in the meantime.
	projection := app.activeSessionProjection()
	var reveal backgroundjobs.RevealRequest
	for _, child := range projection.backgroundJobs.Children() {
		if child.Semantics().Label == "Show build in the conversation" {
			result := projection.backgroundJobs.HandleAction(ui.EventContext{}, ui.Action{ID: string(child.ID())})
			reveal = result.Action.Payload.(backgroundjobs.RevealRequest)
		}
	}
	remoteJourneyCreate(t, shell)
	app.MinimizeAgent(agentID)
	app.syncAgentWindowTabs()
	app.revealBackgroundJob(reveal)
	assertAgentIsPaintedView(t, app, agentID)
	clickCommandDeck(t, shell, "Show output of build")
	defer func() {
		if t.Failed() {
			for _, view := range app.commandOutputs {
				p, err := app.ws.Terminals().Rows(view.id, terminalvt.RowRequest{Start: -1})
				t.Logf("output=%s core=%q err=%v", view.id, terminalVisualText(p), err)
				if screen := app.termScreens[view.id]; screen != nil {
					t.Logf("screen bounds=%+v viewport=%+v", screen.Bounds(), screen.ViewportRead())
				}
			}
		}
	}()
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "READY")
	})
	var terminalID string
	for _, view := range app.commandOutputs {
		terminalID = view.id
	}
	screen := app.termScreens[terminalID]
	if screen == nil || !screen.ReadOnly() || len(app.ws.TermMgr().List()) != 0 {
		t.Fatal("Output did not mount a read-only terminal without creating a process")
	}
	if _, err := fmt.Fprintln(process, "history"); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "output-line-079")
	})
	key := func(code rune, mod tea.KeyMod) {
		_, cmd := app.Update(tea.KeyPressMsg{Code: code, Mod: mod})
		runAppCommand(t, app, cmd)
	}
	key(tea.KeyHome, 0)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "READY")
	})
	anchor := screen.ViewportRead()
	if _, err := fmt.Fprintln(process, "live"); err != nil {
		t.Fatal(err)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		app.View()
		return screen.ViewportRead().Generation > anchor.Generation
	})
	if screen.ViewportRead().Start != anchor.Start ||
		!strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "READY") {
		t.Fatal("new output moved the historical viewport")
	}
	key(tea.KeyEnd, 0)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "LIVE_MARKER")
	})
	key(tea.KeyHome, 0)
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return screen.ViewportRead().Start == anchor.Start && strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "READY")
	})
	key(tea.KeyEsc, 0)
	if app.overlayHost.IsOpen(ui.TerminalOverlayID(terminalID)) || len(app.commandOutputs) != 0 {
		t.Fatal("Escape did not close the viewer")
	}
	// Closing a reader leaves the command alive; reopening uses a fresh stream.
	clickCommandDeck(t, shell, "Show output of build")
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "LIVE_MARKER")
	})
	if _, err := fmt.Fprintln(process, "exit"); err != nil {
		t.Fatal("closing the output window terminated the process")
	}
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "FINAL_MARKER")
	})
}
