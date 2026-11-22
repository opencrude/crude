package viewmodel

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/remoteconn"
	"crude/core/shellport"
	"crude/internal/buildinfo"
	"crude/tui/screens/connectionpopover"
	"crude/tui/screens/shellheader"
	"crude/tui/ui"
	"crude/tui/wm"
)

// This is a responsiveness cue, not a claim about packet loss, bandwidth or the
// model provider. RTT belongs to the existing transport heartbeat observation.
const (
	fastCoreRoundTrip = 80 * time.Millisecond
	slowCoreRoundTrip = 250 * time.Millisecond
)

type connectionHealth struct {
	name       string
	status     shellport.RemoteStatus
	brand      shellheader.ConnectionState
	remote     bool
	tuiBuildID string
	tuiVersion string
	tuiStarted time.Time
	now        time.Time
}

func (m *App) connectionHealth() connectionHealth {
	h := connectionHealth{
		name: m.remoteBackendName(), status: shellport.RemoteStatus{State: "connected", Carrier: "local"},
		tuiBuildID: m.tuiBuildID, tuiVersion: buildinfo.Version, tuiStarted: m.tuiStartedAt, now: time.Now(),
	}
	if link, ok := m.ws.(shellport.BackendLink); ok {
		h.status = link.BackendStatus()
	}
	switch h.status.Carrier {
	case "ssh", "tcp", "quic":
		h.remote = true
	}
	// The selection stays attached through reconnect, even when the carrier has
	// no current sample. A pending workspace switch does not select a new link.
	if m.remotes != nil && m.remotes.current != "" && m.remotes.current != remoteconn.Local {
		h.remote = true
	}
	switch h.status.State {
	case "connected":
		if h.status.RTT >= slowCoreRoundTrip {
			h.brand = shellheader.ConnectionSlow
		}
	case "connecting":
		h.brand = shellheader.ConnectionConnecting
	case "reconnecting":
		h.brand = shellheader.ConnectionReconnecting
	default:
		h.brand = shellheader.ConnectionOffline
	}
	if m.workspaceAttachPending {
		h.brand = shellheader.ConnectionConnecting
	}
	return h
}

func (h connectionHealth) signal(nerdFont bool) shellheader.Segment {
	level, role := 0, ui.ColorMuted
	switch {
	case h.brand == shellheader.ConnectionOffline:
		role = ui.ColorError
	case h.brand == shellheader.ConnectionReconnecting:
		role = ui.ColorWarning
	case h.brand == shellheader.ConnectionConnecting || h.status.RTT <= 0:
		// No current round trip. Do not show a strong signal yet.
	case h.status.RTT >= slowCoreRoundTrip:
		level, role = 1, ui.ColorWarning
	case h.status.RTT >= fastCoreRoundTrip:
		level = 2
	default:
		level, role = 3, ui.ColorSuccess
	}
	glyphs := [...]string{"···", "▂··", "▂▄·", "▂▄▆"}
	if nerdFont {
		glyphs = [...]string{"\U000f08bf", "\U000f08bc", "\U000f08bd", "\U000f08be"}
	}
	return shellheader.Segment{Text: glyphs[level], Role: role, Action: shellheader.ConnectionSignalAction, Priority: -1}
}

func (h connectionHealth) projection() connectionpopover.Projection {
	p := connectionpopover.Projection{Summary: "Connected to core", Role: ui.ColorSuccess}
	guidance := "Your shell and core are communicating normally."
	switch h.brand {
	case shellheader.ConnectionConnecting:
		p.Summary, p.Role = "Connecting to core", ui.ColorAccent
		guidance = "Establishing a connection to the core. The current workspace stays open until the destination is ready."
	case shellheader.ConnectionReconnecting:
		p.Summary, p.Role = "Reconnecting to core", ui.ColorWarning
		guidance = "The link was interrupted. Crude is reconnecting automatically; displayed state may be out of date. Accepted core work is not cancelled by a lost link."
	case shellheader.ConnectionSlow:
		p.Summary, p.Role = "Core link is slow", ui.ColorWarning
		guidance = "Round-trip time is at least 250 ms. Input and live updates may feel delayed. This measures the shell-to-core link, not the model provider."
	case shellheader.ConnectionOffline:
		p.Summary, p.Role = "Core is unavailable", ui.ColorError
		guidance = "The shell cannot reach its core. Displayed state may be out of date. Open Connections to inspect or choose a connection."
	}
	state := h.status.State
	if state == "" {
		state = "unavailable"
	}
	carrier := "Not connected"
	switch h.status.Carrier {
	case "local":
		carrier = "Local IPC · this machine"
	case "ssh":
		carrier = "SSH · encrypted relay"
	case "tcp":
		carrier = "TCP · TLS 1.3"
	case "quic":
		carrier = "QUIC · TLS 1.3"
	}
	rtt := "Not measured yet"
	if h.status.RTT > 0 {
		if h.status.RTT < time.Millisecond {
			rtt = "<1 ms"
		} else {
			rtt = fmt.Sprintf("%d ms", h.status.RTT.Milliseconds())
		}
		if h.status.State != "connected" {
			rtt += " · last observation"
		}
	}
	coreBuild := formatConnectionBuild(h.status.CoreVersion, h.status.CoreBuildID)
	if coreBuild == "" {
		coreBuild = "Not observed"
	} else if h.status.State != "connected" {
		coreBuild += " · last connection"
	}
	coreUptime := "Not observed"
	if !h.status.CoreStartedAt.IsZero() {
		coreUptime = formatUptime(h.now.Sub(h.status.CoreStartedAt))
		if h.status.State != "connected" {
			coreUptime += " · last connection"
		}
	}
	tuiUptime := "Not available"
	if !h.tuiStarted.IsZero() {
		tuiUptime = formatUptime(h.now.Sub(h.tuiStarted))
	}
	tuiBuild := firstNonempty(formatConnectionBuild(h.tuiVersion, h.tuiBuildID), "Not available")
	p.Detail = fmt.Sprintf("Connection   %s\nState        %s\nTransport    %s\nRound trip   %s\nTUI build    %s\nTUI uptime   %s\nCore build   %s\nCore uptime  %s\n\n%s", h.name, state, carrier, rtt, tuiBuild, tuiUptime, coreBuild, coreUptime, guidance)
	if h.status.Notice != "" {
		p.Detail += "\n\n" + h.status.Notice
	}
	return p
}

func formatConnectionBuild(version, hash string) string {
	if version == "" && hash == "" {
		return ""
	}
	return firstNonempty(version, "Version not available") + " / " + firstNonempty(hash, "Hash not available")
}

type connectionHoverState struct {
	owner      shellport.Workspace
	generation uint64
	body       *connectionpopover.Screen
	widget     *ui.WindowFrameWidget
	action     string
	anchor     ui.Rect
	width      int
	height     int
}

func (m *App) connectionActionRect(action string) ui.Rect {
	if m.headerScreen == nil || m.rootHost == nil {
		return ui.Rect{}
	}
	r, ok := m.screenRectOf(m.headerScreen.ID())
	if !ok || r.Empty() {
		return ui.Rect{}
	}
	local := m.headerScreen.ActionBounds(action)
	if local.Empty() {
		return ui.Rect{}
	}
	local.X += r.X
	local.Y += r.Y
	return local
}

func (m *App) clearConnectionHover() {
	if m.overlayHost != nil {
		m.overlayHost.Close(ui.OverlayConnectionHealth)
	}
	m.connectionHover = nil
}

func (m *App) routeConnectionHoverMotion(msg tea.MouseMotionMsg) bool {
	if m.windowManager == nil || m.overlayHost == nil || m.rootHost == nil {
		return false
	}
	if h := m.connectionHover; h != nil && h.widget.Host().HasPointerCapture() {
		if fw := m.overlayHost.Window(ui.OverlayConnectionHealth); fw != nil && m.windowManager.TopWindow() == fw {
			m.forwardEventToWindowContent(fw, msg)
			return true
		}
	}
	if msg.Button != tea.MouseNone {
		m.clearConnectionHover()
		return false
	}
	if top := m.windowManager.TopWindow(); top != nil && top.ID != string(ui.OverlayConnectionHealth) {
		m.clearConnectionHover()
		return false
	}
	if m.connectionHover != nil {
		m.syncConnectionHover()
		if h := m.connectionHover; h != nil {
			if fw := m.overlayHost.Window(ui.OverlayConnectionHealth); fw != nil && fw.ContainsPoint(msg.X, msg.Y) {
				m.forwardEventToWindowContent(fw, msg)
				return true
			}
			if h.anchor.Contains(msg.X, msg.Y) {
				return false
			}
		}
		m.clearConnectionHover()
	}
	for _, action := range [...]string{shellheader.ConnectionAction, shellheader.ConnectionSignalAction} {
		if m.connectionActionRect(action).Contains(msg.X, msg.Y) {
			m.openConnectionHover(action)
			break
		}
	}
	return false
}

func (m *App) openConnectionHover(action string) {
	anchor := m.connectionActionRect(action)
	if anchor.Empty() || m.width < 24 || m.height-anchor.Y < 9 {
		return
	}
	body := connectionpopover.New(m.Theme, m.connectionHealth().projection())
	size := body.PreferredSize(m.width-2, m.height-anchor.Y-1)
	widget := ui.NewWindowFrameWidget(m.Theme, "connection-health/frame", ui.FramePopover, "Connection to core", body, nil, false, size.W, size.H)
	// A passive hover has no text caret. An explicit reader click or palette
	// activation may focus the TextArea without changing the shell's draft.
	widget.Focus(connectionpopover.ScreenID)
	fw := wm.NewFloatingWindow(string(ui.OverlayConnectionHealth), "", widget, size.W, size.H,
		wm.WithDecoration(true), wm.WithFocusable(true), wm.WithMovable(false), wm.WithResizable(false), wm.WithClosable(false), wm.WithModal(false))
	x, y := clampPopupPosition(anchor.X, anchor.Y+1, size.W, size.H, m.width, m.height)
	fw.SetPosition(x, y)
	policy := ui.PopoverPolicy(true)
	policy.Passive = true
	m.overlayHost.Open(ui.OverlayConnectionHealth, fw, policy, func() { m.connectionHover = nil })
	m.connectionHover = &connectionHoverState{owner: m.ws, generation: m.workspaceSwitchGen.Load(), body: body, widget: widget, action: action, anchor: anchor, width: m.width, height: m.height}
}

func (m *App) syncConnectionHover() {
	h := m.connectionHover
	if h == nil {
		return
	}
	anchor := m.connectionActionRect(h.action)
	fw := m.overlayHost.Window(ui.OverlayConnectionHealth)
	if fw == nil || h.owner != m.ws || h.generation != m.workspaceSwitchGen.Load() || anchor.Empty() ||
		m.windowManager.TopWindow() != fw || m.width < 24 || m.height-anchor.Y < 9 {
		m.clearConnectionHover()
		return
	}
	// A metric sample must not interrupt a read-only selection drag.
	if h.widget.Host().HasPointerCapture() {
		return
	}
	changed := h.body.SetProjection(m.connectionHealth().projection())
	if !changed && h.anchor == anchor && h.width == m.width && h.height == m.height {
		return
	}
	h.body.SetTokens(m.rootHost.Tokens())
	size := h.body.PreferredSize(m.width-2, m.height-anchor.Y-1)
	fw.SetSize(size.W, size.H)
	x, y := clampPopupPosition(anchor.X, anchor.Y+1, size.W, size.H, m.width, m.height)
	fw.SetPosition(x, y)
	h.widget.Host().MarkLayout()
	fw.MarkContentDirty()
	h.anchor, h.width, h.height = anchor, m.width, m.height
}

func (m *App) handleConnectionHealthAction(msg connectionpopover.ActionMsg) tea.Cmd {
	if h := m.connectionHover; h == nil || msg.Screen != h.body || h.owner != m.ws || h.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	m.clearConnectionHover()
	if msg.Action == "connections" {
		return m.OpenRemotes()
	}
	return nil
}

// formatUptime keeps the two largest units: "45s", "12m 4s", "3h 20m", "2d 5h".
func formatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	seconds := int64(d / time.Second)
	days, hours, minutes := seconds/86400, seconds/3600%24, seconds/60%60
	switch {
	case days > 0:
		return fmt.Sprintf("%dd %dh", days, hours)
	case hours > 0:
		return fmt.Sprintf("%dh %dm", hours, minutes)
	case minutes > 0:
		return fmt.Sprintf("%dm %ds", minutes, seconds%60)
	default:
		return fmt.Sprintf("%ds", seconds)
	}
}
