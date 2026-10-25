package viewmodel

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/remoteconn"
	"crude/shellport"
	"crude/tui/rendering"
	"crude/tui/screens/connectionpopover"
	"crude/tui/screens/shellchat"
	"crude/tui/screens/shellheader"
	"crude/tui/surface"
	"crude/tui/ui"
)

// Only the transport observation is injected. Header layout, pointer dispatch,
// focus, overlay lifecycle and painted output use the real shell.
type connectionHealthWorkspace struct {
	shellport.Workspace
	status shellport.RemoteStatus
}

func (w *connectionHealthWorkspace) BackendStatus() shellport.RemoteStatus { return w.status }

func TestConnectionHealthLivesOnWordmarkNotTelemetry(t *testing.T) {
	for _, width := range []int{160, 60, 32} {
		app := newFullTestApp(t)
		original := app.ws
		t.Cleanup(func() { app.ws = original })
		app.width, app.height = width, 32
		app.ws = &connectionHealthWorkspace{Workspace: app.ws, status: shellport.RemoteStatus{State: "connected", Carrier: "local", RTT: 200 * time.Microsecond}}
		app.paintShellFrame()
		line := shellBufferRow(app, app.frameTopRow())
		if !strings.Contains(line, "Crude") {
			t.Fatalf("width %d lost wordmark: %q", width, line)
		}
		for _, unwanted := range []string{"Local", "connected", "local", " ms", "⚠", "✕"} {
			if strings.Contains(line, unwanted) {
				t.Fatalf("healthy header still spends space on %q: %q", unwanted, line)
			}
		}
		if rendering.VisibleWidth(line) > width {
			t.Fatalf("header exceeds %d columns: %q", width, line)
		}
		left, _ := ui.ShellContentRail(app.rootHost.Tokens(), app.headerScreen.Bounds().W)
		r, _ := app.screenRectOf(app.headerScreen.ID())
		app.dispatchMouseMotion(tea.MouseMotionMsg{X: r.X + left + 2, Y: r.Y})
		if !app.overlayHost.IsOpen(ui.OverlayConnectionHealth) {
			t.Fatal("hovering the Crude wordmark did not reveal connection details")
		}
	}
}

func connectionPopoverText(app *App) string {
	fw := app.overlayHost.Window(ui.OverlayConnectionHealth)
	if fw == nil {
		return ""
	}
	w, h := fw.Size()
	surf := surface.NewSurface("connection-test", w, h, app.mainSurf.Buffer().Get(0, 0))
	fw.Content.Render(surf, app.renderCtx)
	var b strings.Builder
	for y := range h {
		b.WriteString(shellchat.PlainRow(surf.Buffer(), y, w))
		b.WriteByte('\n')
	}
	return b.String()
}

func newConnectionHealthApp(t *testing.T) (*App, *connectionHealthWorkspace) {
	t.Helper()
	app := newFullTestApp(t)
	app.AddOrchestrator("owner")
	original := app.ws
	t.Cleanup(func() { app.ws = original })
	w := &connectionHealthWorkspace{Workspace: original, status: shellport.RemoteStatus{State: "connected", Carrier: "quic", RTT: 38 * time.Millisecond}}
	app.ws = w
	app.width, app.height = 120, 36
	app.Input.SetInput("retained draft")
	app.paintShellFrame()
	return app, w
}

func TestConnectionHealthWordmarkStatesAndFastClock(t *testing.T) {
	app, w := newConnectionHealthApp(t)
	for _, tc := range []struct {
		state, marker string
		rtt           time.Duration
		fast          bool
	}{
		{"connected", "", 249 * time.Millisecond, false},
		{"connected", "⚠", 250 * time.Millisecond, false},
		{"connecting", "", 0, true},
		{"reconnecting", "⚠", 0, true},
		{"offline", "✕", 0, false},
		{"connected", "", 38 * time.Millisecond, false},
	} {
		w.status.State, w.status.RTT = tc.state, tc.rtt
		app.paintShellFrame()
		line := shellBufferRow(app, app.frameTopRow())
		if tc.marker != "" && !strings.Contains(line, "Crude "+tc.marker) {
			t.Fatalf("%s %s missing attention: %q", tc.state, tc.rtt, line)
		}
		if tc.marker == "" && (strings.Contains(line, "⚠") || strings.Contains(line, "✕")) {
			t.Fatalf("settled connection kept an obsolete marker: %q", line)
		}
		if app.wantsFastTick() != tc.fast || app.headerScreen.NeedsAnimation() != tc.fast {
			t.Fatalf("%s: wrong animation cadence fast=%v", tc.state, app.wantsFastTick())
		}
	}
	app.workspaceAttachPending = true
	app.paintShellFrame()
	if !app.headerScreen.NeedsAnimation() || !app.wantsFastTick() {
		t.Fatal("workspace attachment did not animate the brand")
	}
}

func TestConnectionHealthHoverLiveDetailsAndFocus(t *testing.T) {
	app, w := newConnectionHealthApp(t)
	app.remotes = &remoteManagerState{current: "development", snapshot: remoteconn.Snapshot{Connections: []remoteconn.Connection{{ID: "development", Name: "Development"}}}}
	anchor := app.connectionWordmarkRect()
	focus, agent, group := app.rootHost.Focused(), app.AgentView.ActiveID(), app.Win.ActiveGroup()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 2, Y: anchor.Y})
	if app.connectionHover == nil {
		t.Fatal("wordmark hover did not open")
	}
	if app.windowManager.Focused() != nil || app.rootHost.Focused() != focus || app.AgentView.ActiveID() != agent || app.Win.ActiveGroup() != group {
		t.Fatal("hover stole focus or changed the active view")
	}
	for _, label := range []string{"Connection to core", "Connected to core", "Development", "QUIC", "38 ms", "Connections", "Close"} {
		if text := connectionPopoverText(app); !strings.Contains(text, label) {
			t.Fatalf("detail omitted %q: %s", label, text)
		}
	}
	fw := app.overlayHost.Window(ui.OverlayConnectionHealth)
	x, y := fw.Position()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: x + 4, Y: y + 2})
	if app.connectionHover == nil {
		t.Fatal("moving into the adjacent popover dismissed it")
	}
	w.status.RTT = 520 * time.Millisecond
	app.syncConnectionHover()
	if text := connectionPopoverText(app); !strings.Contains(text, "Core link is slow") || !strings.Contains(text, "520 ms") || !strings.Contains(text, "model provider") {
		t.Fatalf("open details did not follow slow link: %s", text)
	}
	w.status.State = "reconnecting"
	app.syncConnectionHover()
	if text := connectionPopoverText(app); !strings.Contains(text, "Reconnecting to core") || !strings.Contains(text, "last observation") {
		t.Fatalf("reconnecting details dishonest: %s", text)
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if app.connectionHover != nil || app.Input.Value() != "retained draftx" {
		t.Fatal("passive reader took typing or consumed the draft")
	}
}

func TestConnectionHealthDismissalAndLifetimeBoundaries(t *testing.T) {
	for _, scenario := range []string{"leave", "escape", "outside", "modal", "workspace", "replacement", "capture", "tiny"} {
		t.Run(scenario, func(t *testing.T) {
			app, _ := newConnectionHealthApp(t)
			anchor := app.connectionWordmarkRect()
			app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 2, Y: anchor.Y})
			old := app.connectionHover
			if old == nil {
				t.Fatal("hover did not open")
			}
			switch scenario {
			case "leave":
				app.dispatchMouseMotion(tea.MouseMotionMsg{X: app.width - 2, Y: app.height - 3})
			case "escape":
				app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
			case "outside":
				app.dispatchMouseClick(tea.MouseClickMsg{X: app.width - 2, Y: app.height - 3, Button: tea.MouseLeft})
			case "modal":
				openOverlayFixture(t, app, overlayOutsideFixture{id: ui.OverlayDirtyClose, policy: ui.ModalDialogPolicy()})
				app.syncConnectionHover()
			case "workspace":
				app.beginWorkspaceSwitch()
			case "replacement":
				app.workspaceSwitchGen.Add(1)
				app.syncConnectionHover()
			case "capture":
				app.rootHost.CapturePointer(app.chatScreen.ID(), tea.MouseLeft)
				app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X, Y: anchor.Y, Button: tea.MouseLeft})
				app.rootHost.CancelPointerCapture()
			case "tiny":
				app.width, app.height = 18, 8
				app.syncConnectionHover()
			}
			if app.connectionHover != nil || app.overlayHost.IsOpen(ui.OverlayConnectionHealth) || app.Input.Value() != "retained draft" {
				t.Fatal("popover outlived its owner/gesture or consumed the draft")
			}
			if cmd := app.handleConnectionHealthAction(connectionpopover.ActionMsg{Screen: old.body, Action: "connections"}); cmd != nil {
				t.Fatal("a stale panel action survived its lifetime")
			}
		})
	}
}

func TestConnectionHealthClickAndKeyboardAccess(t *testing.T) {
	app, _ := newConnectionHealthApp(t)
	focus := app.rootHost.Focused()
	anchor := app.connectionWordmarkRect()
	_, cmd := app.dispatchMouseClick(tea.MouseClickMsg{X: anchor.X + 2, Y: anchor.Y, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	app.dispatchMouseRelease(tea.MouseReleaseMsg{X: anchor.X + 2, Y: anchor.Y, Button: tea.MouseLeft})
	if app.connectionHover == nil {
		t.Fatal("wordmark click did not open details")
	}
	if app.rootHost.Focused() != focus || app.FocusStack.Top() == FocusTree {
		t.Fatal("wordmark click sent keyboard focus to the sidebar below it")
	}
	app.clearConnectionHover()
	runAppCommand(t, app, app.executePaletteCommand("remote.health"))
	if fw := app.windowManager.Focused(); fw == nil || fw.ID != string(ui.OverlayConnectionHealth) {
		t.Fatal("palette details are not keyboard accessible")
	}
	h := app.connectionHover
	runAppCommand(t, app, app.forwardEventToWindowContent(app.windowManager.Focused(), tea.KeyPressMsg{Code: tea.KeyTab}))
	if h.widget.Host().Focused() != connectionpopover.ActionsID {
		t.Fatal("Tab did not reach native actions")
	}
	app.dispatchKeyPress(tea.KeyPressMsg{Code: tea.KeyEsc})
	if app.connectionHover != nil || app.Input.Value() != "retained draft" {
		t.Fatal("Escape failed to dismiss keyboard details")
	}
	// The wordmark uses painted hit geometry, not a second viewmodel width.
	if got := app.headerScreen.ActionBounds(shellheader.ConnectionAction).W; got != 5 {
		t.Fatalf("wordmark hit width=%d", got)
	}
}

func TestConnectionHealthRoundTripIsMeasuredNotInvented(t *testing.T) {
	app, w := newConnectionHealthApp(t)
	for _, tc := range []struct {
		rtt  time.Duration
		want string
	}{
		{0, "Not measured yet"},
		{200 * time.Microsecond, "<1 ms"},
		{38 * time.Millisecond, "38 ms"},
	} {
		w.status.RTT = tc.rtt
		p := app.connectionHealth().projection()
		if !strings.Contains(p.Detail, tc.want) || strings.Contains(p.Detail, " 0 ms") {
			t.Fatalf("RTT %s misrepresented: %s", tc.rtt, p.Detail)
		}
	}
}

func TestConnectionHealthSelectionKeepsOwnershipAndCopies(t *testing.T) {
	app, w := newConnectionHealthApp(t)
	anchor := app.connectionWordmarkRect()
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: anchor.X + 2, Y: anchor.Y})
	h := app.connectionHover
	if h == nil {
		t.Fatal("details did not open")
	}
	fw := app.overlayHost.Window(ui.OverlayConnectionHealth)
	_ = connectionPopoverText(app)
	r, _ := h.widget.Host().ScreenRect(connectionpopover.DetailID)
	x, y := fw.Position()
	x, y = x+r.X, y+r.Y
	var copied string
	reader := h.widget.Host().Component(connectionpopover.DetailID).(interface{ SetClipboardCopy(func(string) tea.Cmd) })
	reader.SetClipboardCopy(func(text string) tea.Cmd { copied = text; return nil })
	app.dispatchMouseClick(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	app.dispatchMouseMotion(tea.MouseMotionMsg{X: x + 10, Y: y, Button: tea.MouseLeft})
	w.status.RTT = 520 * time.Millisecond
	app.syncConnectionHover()
	if app.connectionHover != h || !h.widget.Host().HasPointerCapture() {
		t.Fatal("metric refresh interrupted the selection")
	}
	// The exact window Host keeps the selection until matching release, even
	// outside the popover; it must not become a composer or chat gesture.
	_, cmd := app.dispatchMouseRelease(tea.MouseReleaseMsg{X: app.width - 2, Y: app.height - 2, Button: tea.MouseLeft})
	runAppCommand(t, app, cmd)
	if !strings.Contains(copied, "Connection") || app.Input.Value() != "retained draft" || h.widget.Host().HasPointerCapture() {
		t.Fatalf("selection did not copy/settle: %q", copied)
	}
	app.syncConnectionHover()
	if text := connectionPopoverText(app); !strings.Contains(text, "520 ms") {
		t.Fatalf("live refresh did not resume after selection: %s", text)
	}
}
