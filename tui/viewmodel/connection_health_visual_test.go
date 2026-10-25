package viewmodel

import (
	"fmt"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/remoteconn"
	"crude/shellport"
	"crude/tui/ui"
)

// Dedicated scripts/tuishot.sh fixture: only the transport observation is
// controlled. The production App, header, ticks, pointer/focus and overlay paths
// remain intact; no provider, user configuration or real backend is disturbed.
func TestConnectionHealthVisualAcceptance(t *testing.T) {
	if os.Getenv("CRUDE_VISUAL_CONNECTION_HEALTH") != "1" {
		t.Skip("dedicated sequential native capture")
	}
	app, _ := newRealShellApp(t)
	app.AddOrchestrator("connection-preview")
	app.Input.SetInput("This draft stays yours")
	w := &connectionHealthWorkspace{Workspace: app.ws, status: shellport.RemoteStatus{State: "connected", Carrier: "quic", RTT: 38 * time.Millisecond}}
	app.ws = w
	app.SetRemoteManager(&fakeRemoteManager{}, "development")
	app.remotes.snapshot = remoteconn.Snapshot{Connections: []remoteconn.Connection{{ID: "development", Name: "Development", Kind: "direct", Host: "dev.example"}}}
	model := &connectionHealthVisualModel{App: app, link: w, t: t}
	program := tea.NewProgram(model, tea.WithWidthMethod(ansi.GraphemeWidth))
	app.SetProgram(program)
	if _, err := program.Run(); err != nil {
		t.Fatal(err)
	}
	app.StopAsyncIO()
}

type connectionHealthVisualModel struct {
	*App
	link     *connectionHealthWorkspace
	t        *testing.T
	light    bool
	sawHover bool
}

func (*connectionHealthVisualModel) Init() tea.Cmd { return tickCmd() }

func (m *connectionHealthVisualModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		fixtureKey := true
		switch key.Code {
		case tea.KeyF2:
			m.link.status = shellport.RemoteStatus{State: "connecting"}
		case tea.KeyF3:
			m.link.status = shellport.RemoteStatus{State: "connected", Carrier: "quic", RTT: 520 * time.Millisecond}
		case tea.KeyF4:
			m.link.status = shellport.RemoteStatus{State: "reconnecting", Notice: "The previous link stopped responding. Restoring the connection automatically."}
		case tea.KeyF5:
			m.link.status = shellport.RemoteStatus{State: "offline", Notice: "The selected core is unavailable."}
		case tea.KeyF6:
			m.link.status = shellport.RemoteStatus{State: "connected", Carrier: "quic", RTT: 38 * time.Millisecond}
		case tea.KeyF7:
			m.light = !m.light
			preset := "dark"
			if m.light {
				preset = "light"
			}
			m.ApplyThemePreset(preset)
		case tea.KeyF9:
			if m.Input.Value() != "This draft stays yours" || m.rootHost.HasPointerCapture() {
				m.t.Error("capture lost the draft or left a gesture captured")
			}
			if !m.sawHover {
				m.t.Error("native journey never opened the wordmark details")
			}
			if path := os.Getenv("CRUDE_VISUAL_PROOF"); path != "" {
				if err := os.WriteFile(path, []byte(fmt.Sprintf("connection-health native journey: hover=%v draft=%q failed=%v\n", m.sawHover, m.Input.Value(), m.t.Failed())), 0600); err != nil {
					m.t.Error(err)
				}
			}
			return m, tea.Quit
		default:
			fixtureKey = false
		}
		if fixtureKey {
			m.forceFrame()
			return m, nil
		}
	}
	_, cmd := m.App.Update(msg)
	m.sawHover = m.sawHover || m.overlayHost.IsOpen(ui.OverlayConnectionHealth)
	return m, cmd
}
