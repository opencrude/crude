package settings

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/coreprotocol"
	"crude/network"
	"crude/shellport"
	"crude/tui/ui"
	"crude/types"
)

const (
	fieldRemoteEnabled = "remote-access-enabled"
	fieldRemoteQUIC    = "remote-quic-address"
	fieldRemoteTLS     = "remote-tls-address"
	fieldRemoteGrace   = "remote-session-grace"
	fieldRemoteStatus  = "remote-access-status"
	fieldRemoteCode    = "remote-pairing-code"
	remoteRevokePrefix = "settings.remote.revoke/"
)

type remoteAccessState struct {
	admin      shellport.RemoteAccessAdmin
	localHost  bool
	generation uint64
	cancel     context.CancelFunc
	busy       bool
	loaded     bool
	notice     string
	view       coreprotocol.RemoteAccessView
	pairing    coreprotocol.RemotePairingCode
	pending    *PromptMsg
}

// RemoteAccessResultMsg is private administrative evidence for one exact form.
// Never format the contained pairing code into logs or ordinary status text.
type RemoteAccessResultMsg struct {
	Target       *Model
	Generation   uint64
	Operation    string
	View         coreprotocol.RemoteAccessView
	Pairing      coreprotocol.RemotePairingCode
	Err          error
	RefreshError error
}

func (RemoteAccessResultMsg) String() string     { return "[remote access result redacted]" }
func (v RemoteAccessResultMsg) GoString() string { return v.String() }

type RemotePairingExpiredMsg struct {
	Target     *Model
	Generation uint64
}

func remoteInfo(id, label, text, help string) *ui.FieldState {
	// A non-editable nav presentation has no text editor or clipboard endpoint.
	return &ui.FieldState{ID: id, Label: label, Type: ui.FieldNav, Columns: []string{text}, ReadOnly: true, Description: help}
}

func remoteOperation(id, label, text, help string, disabled bool) *ui.FieldState {
	return &ui.FieldState{ID: id, Label: label, Type: ui.FieldNav, Target: id, Columns: []string{text}, Description: help, Disabled: disabled}
}

func (m *Model) buildRemoteAccessForm(settings network.Settings) *ui.FormModel {
	settings = m.networkDisplaySettings(settings)
	fields := []*ui.FieldState{
		ui.SeparatorFieldState("remote-listeners", "Direct connections"),
		ui.BoolFieldState(fieldRemoteEnabled, "Allow remote access", settings.RemoteAccessEnabled, "Opt in to paired-device connections; Save applies listener changes on this host."),
		ui.TextFieldState(fieldRemoteQUIC, "QUIC listen address", settings.RemoteQUICAddress, "UDP host:port; loopback is the safe default. Use a reachable interface for other devices."),
		ui.TextFieldState(fieldRemoteTLS, "TLS listen address", settings.RemoteTLSAddress, "TCP host:port for explicit TLS fallback, using the same pinned host identity."),
		networkPositiveIntField(fieldRemoteGrace, "Session grace", "seconds", settings.SessionGraceSeconds, "Keep disconnected sessions resumable for this long; default 900 seconds (15 minutes)."),
	}
	if !m.remoteAccess.localHost {
		for _, field := range fields {
			if field.Type != ui.FieldSeparator {
				field.ReadOnly = true
			}
		}
	}
	return m.remoteAccessForm(fields)
}

func (m *Model) remoteAccessForm(fields []*ui.FieldState) *ui.FormModel {
	r := &m.remoteAccess
	status := r.notice
	if status == "" {
		switch {
		case m.Scope() == types.SettingsScopeWorkspace:
			status = "Application-owned; workspace access is read-only"
		case !r.localHost:
			status = "Unavailable: remote access administration requires the local host"
		case r.admin == nil:
			status = "Unavailable: this core has no remote access administrator"
		case r.busy:
			status = "Loading remote access…"
		case !r.loaded:
			status = "Refresh to load paired devices"
		default:
			status = fmt.Sprintf("%d paired devices", len(r.view.Devices))
		}
	}
	fields = append(fields, ui.SeparatorFieldState("remote-devices", "Paired devices"), remoteInfo(fieldRemoteStatus, "Status", status, "Only the local host can generate codes or revoke paired devices."))
	unavailable := m.Scope() != types.SettingsScopeApplication || !r.localHost || r.admin == nil || r.busy
	if r.view.Fingerprint != "" {
		fields = append(fields, remoteInfo("remote-fingerprint", "Host fingerprint", r.view.Fingerprint, "Verify this fingerprint on the device before pairing."))
	}
	fields = append(fields,
		remoteOperation("settings.remote.refresh", "Refresh devices", "Refresh", "Read current host admission and paired devices.", unavailable),
		remoteOperation("settings.remote.pair", "Generate pairing code", "Generate code", "Create a short-lived single-use secret; share it only with the device you intend to pair.", unavailable || !r.loaded || !r.view.Config.Enabled),
	)
	if r.pairing.Code != "" && time.Now().Before(r.pairing.ExpiresAt) {
		fields = append(fields, remoteInfo(fieldRemoteCode, "Pairing code", r.pairing.Code, "Sensitive, single-use code; no automatic clipboard copy."), remoteInfo("remote-code-expiry", "Code expires", r.pairing.ExpiresAt.Local().Format(time.DateTime), "Generate a new code if this one expires."), remoteOperation("settings.remote.hide-code", "Hide pairing code", "Hide", "Remove this secret from the current screen.", false))
	}
	if r.loaded && len(r.view.Devices) == 0 {
		fields = append(fields, remoteInfo("remote-no-devices", "Devices", "No paired devices", "Generate a pairing code to enroll a device."))
	}
	for _, device := range r.view.Devices {
		seen := "Never connected"
		if !device.LastSeen.IsZero() {
			seen = "Last seen " + device.LastSeen.Local().Format(time.DateTime)
		}
		if !device.RevokedAt.IsZero() {
			seen += " · Revoked"
		}
		fields = append(fields, remoteOperation(remoteRevokePrefix+device.ID, "Revoke "+device.Name, seen, "Revoke this device after confirmation; it will need a new pairing code to reconnect.", unavailable || !r.loaded || !device.RevokedAt.IsZero()))
	}
	return m.networkForm("remote-access", fields...)
}

// rebuildRemoteAccessStatus preserves scalar draft fields and their initial
// values when asynchronous administrative evidence changes.
func (m *Model) rebuildRemoteAccessStatus() {
	if len(m.categories) <= catNetworkRemoteAccess {
		return
	}
	old := m.categories[catNetworkRemoteAccess].Form
	var fields []*ui.FieldState
	for _, field := range old.Fields() {
		if field.ID == "remote-devices" {
			break
		}
		if strings.HasPrefix(field.ID, "network-remote-access-") {
			continue
		}
		fields = append(fields, field)
	}
	form := m.remoteAccessForm(fields)
	m.categories[catNetworkRemoteAccess].Form = form
	form.OnChanged(func(id string) { m.handleFieldChanged(catNetworkRemoteAccess, id) })
}

// SetRemoteAccessAdmin binds a host service once per retained Settings model.
// localHost must come from the selected core's authenticated attachment, never
// from a user-editable backend label. False always leaves admission read-only.
func (s *WorkspaceSettingsSurface) SetRemoteAccessAdmin(admin shellport.RemoteAccessAdmin, localHost bool) tea.Cmd {
	m := s.model
	if m == nil {
		return nil
	}
	m.cancelRemoteAccess()
	m.remoteAccess.admin, m.remoteAccess.localHost = admin, localHost
	for _, id := range []string{fieldRemoteEnabled, fieldRemoteQUIC, fieldRemoteTLS, fieldRemoteGrace} {
		if field := m.fieldAt(catNetworkRemoteAccess, id); field != nil {
			field.ReadOnly = !localHost || m.Scope() != types.SettingsScopeApplication
		}
	}
	m.rebuildRemoteAccessStatus()
	if admin == nil || !localHost || m.Scope() != types.SettingsScopeApplication {
		s.refreshForm()
		return nil
	}
	cmd := m.beginRemoteAccess("refresh", "")
	s.refreshForm()
	return cmd
}

// CancelRemoteAccess cancels the retained form's requests and removes secrets.
// The overlay host calls it when Settings closes or its workspace is replaced.
func (s *WorkspaceSettingsSurface) CancelRemoteAccess() {
	if s.model != nil {
		s.model.cancelRemoteAccess()
		s.model.rebuildRemoteAccessStatus()
	}
}

func (m *Model) cancelRemoteAccess() {
	r := &m.remoteAccess
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.generation++
	r.busy = false
	r.notice = ""
	r.pairing = coreprotocol.RemotePairingCode{}
	r.pending = nil
}

func (m *Model) beginRemoteAccess(operation, id string) tea.Cmd {
	r := &m.remoteAccess
	if r.busy || r.admin == nil || !r.localHost || m.Scope() != types.SettingsScopeApplication {
		return nil
	}
	m.cancelRemoteAccess()
	r.busy, r.notice = true, "Loading remote access…"
	if operation == "pair" {
		r.notice = "Generating pairing code…"
	}
	if operation == "revoke" {
		r.notice = "Revoking device…"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	r.cancel = cancel
	admin, generation := r.admin, r.generation
	m.rebuildRemoteAccessStatus()
	return func() tea.Msg {
		defer cancel()
		result := RemoteAccessResultMsg{Target: m, Generation: generation, Operation: operation}
		if err := ctx.Err(); err != nil {
			result.Err = err
			return result
		}
		switch operation {
		case "pair":
			result.Pairing, result.Err = admin.CreateRemotePairing(ctx)
		case "revoke":
			result.Err = admin.RevokeRemoteDevice(ctx, id)
		}
		if result.Err == nil {
			result.View, result.RefreshError = admin.RemoteAccess(ctx)
			if operation == "refresh" {
				result.Err = result.RefreshError
			}
		}
		return result
	}
}

func (m *Model) applyRemoteAccessResult(result RemoteAccessResultMsg) tea.Cmd {
	r := &m.remoteAccess
	if result.Target != m || result.Generation != r.generation || !r.busy || m.Scope() != types.SettingsScopeApplication {
		return nil
	}
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	r.busy = false
	r.notice = ""
	if result.Err != nil {
		r.notice = "Remote access unavailable: " + result.Err.Error()
		r.loaded = false
	} else {
		if result.RefreshError == nil {
			r.loaded = true
			r.view = result.View
			r.view.Devices = slices.Clone(result.View.Devices)
		} else {
			r.notice = "Operation completed; device refresh failed: " + result.RefreshError.Error()
		}
		if result.Operation == "pair" {
			r.pairing = result.Pairing
		}
		if result.Operation == "revoke" && result.RefreshError == nil {
			r.notice = "Device revoked"
		}
	}
	m.rebuildRemoteAccessStatus()
	if r.pairing.Code != "" {
		generation := r.generation
		return tea.Tick(max(0, time.Until(r.pairing.ExpiresAt)), func(time.Time) tea.Msg { return RemotePairingExpiredMsg{m, generation} })
	}
	return nil
}

func (s *WorkspaceSettingsSurface) handleRemoteAccessAction(action ui.Action) ui.EventResult {
	m := s.model
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	if m == nil || m.selected != catNetworkRemoteAccess || m.Scope() != types.SettingsScopeApplication {
		return result
	}
	r := &m.remoteAccess
	if action.ID == "settings.remote.hide-code" {
		r.pairing = coreprotocol.RemotePairingCode{}
		m.rebuildRemoteAccessStatus()
		s.refreshForm()
		return result
	}
	if !r.localHost || r.admin == nil || r.busy {
		return result
	}
	switch {
	case action.ID == "settings.remote.refresh":
		result.Cmd = m.beginRemoteAccess("refresh", "")
	case action.ID == "settings.remote.pair":
		if r.loaded && r.view.Config.Enabled {
			result.Cmd = m.beginRemoteAccess("pair", "")
		}
	case strings.HasPrefix(string(action.ID), remoteRevokePrefix):
		if !r.loaded {
			return result
		}
		id := strings.TrimPrefix(string(action.ID), remoteRevokePrefix)
		for _, device := range r.view.Devices {
			if device.ID == id && device.RevokedAt.IsZero() {
				copy := device
				prompt := PromptMsg{Target: m, Kind: PromptRevokeRemoteDevice, ID: id, Label: device.Name, remoteDevice: &copy, remoteGeneration: r.generation}
				r.pending = &prompt
				result.Cmd = func() tea.Msg { return prompt }
				break
			}
		}
	case action.ID == "settings.remote.confirm-revoke":
		id, _ := action.Payload.(string)
		if r.pending != nil && r.pending.ID == id && s.remoteRevokeRelevant(*r.pending) {
			result.Cmd = m.beginRemoteAccess("revoke", id)
		}
	}
	s.refreshForm()
	return result
}

func (s *WorkspaceSettingsSurface) remoteRevokeRelevant(prompt PromptMsg) bool {
	m := s.model
	if m == nil || prompt.Target != m || m.selected != catNetworkRemoteAccess || m.Scope() != types.SettingsScopeApplication || !m.remoteAccess.localHost || m.remoteAccess.busy || prompt.remoteDevice == nil || prompt.remoteGeneration != m.remoteAccess.generation {
		return false
	}
	return slices.Contains(m.remoteAccess.view.Devices, *prompt.remoteDevice) && prompt.remoteDevice.RevokedAt.IsZero()
}
