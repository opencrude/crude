package viewmodel

import (
	"context"
	"crude/internal/terminalvt"
	"crude/tui/cell"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/agent/session"
	"crude/tui/rendering"
	captypes "crude/types/capability"
	"crude/workspace"
)

// A real composer call through the core, real HTTP initialization, user
// steering, command inventory, terminal output reader and termination control.
// The HTTP peer deliberately withholds initialize until its request is stopped.
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
		_, _ = fmt.Fprint(w, "data: "+`{"choices":[{"delta":{"content":"I can continue while the server starts."},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
	}))
	t.Cleanup(provider.Close)
	settings := captypes.Normalize(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{
		"fixture": {ID: "fixture", Name: "Fixture server", Transport: "http", URL: endpoint.URL, TimeoutSeconds: 120},
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
	submit("Activate Fixture server.")
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		select {
		case <-started:
			return true
		default:
			return false
		}
	})
	submit("Continue while it starts.")
	remoteJourneyUntil(t, []remoteJourneyShell{shell}, func() bool {
		return strings.Contains(rendering.StripAnsi(app.View().RenderContent()), "Background jobs") && requests.Load() >= 3 && app.activeSessionProjection().backgroundJobs.VisibleCount() == 2
	})
	if !app.wantsFastTick() {
		t.Fatal("a visible background progress bar was left on the idle clock")
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
	var expanded bool
	for _, msg := range app.ws.AgentMgr().Get(id).State.Messages.Messages() {
		if msg.ToolName == "capability_activate" {
			expanded = app.subPaneProjections.Existing(id).renderer.DisclosureExpanded(msg.InternalID)
		}
	}
	if !expanded {
		t.Fatal("the activation link did not reveal its originating call")
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
	colors   []cell.Color
	changes  int
	since    time.Time
	ready    bool
}

// Inspect the last painted job row, not View(), which would consume a frame.
// Five distinct bar paints in one idle-tick interval distinguish actual motion
// from updating on either the 400 ms idle tick or the one-second inventory.
func (m *activationCapture) motionReady() bool {
	if m.ready || m.evidence == "" {
		return false
	}
	now := time.Now()
	if now.Sub(m.since) > workspace.IdleTickInterval {
		m.changes = 0
		m.since = now
	}
	for _, g := range m.mainPanes.groups {
		if g.view == nil || g.deck.Empty() || g.view.deck.surf == nil {
			continue
		}
		buf := g.view.deck.surf.Buffer()
		for y := 0; y < buf.Height(); y++ {
			var text strings.Builder
			var colors []cell.Color
			for _, c := range buf.Row(y) {
				text.WriteRune(c.Char)
				if c.Char == '─' {
					colors = append(colors, c.FG)
				}
			}
			if !strings.Contains(text.String(), "Output") || len(colors) < 2 {
				continue
			}
			if !slices.Equal(colors, m.colors) {
				m.colors = colors
				m.changes++
			}
			if m.changes >= 5 {
				m.ready = true
				return true
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
	if m.motionReady() {
		return m, tea.Batch(cmd, func() tea.Msg { return os.WriteFile(m.evidence, []byte("motion\n"), 0600) })
	}
	return m, cmd
}
