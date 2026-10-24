package settings

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/network"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

type remoteAdminStub struct {
	view         coreprotocol.RemoteAccessView
	err          error
	reads, pairs int
	revoked      string
}

func (a *remoteAdminStub) RemoteAccess(context.Context) (coreprotocol.RemoteAccessView, error) {
	a.reads++
	return a.view, a.err
}
func (a *remoteAdminStub) CreateRemotePairing(context.Context) (coreprotocol.RemotePairingCode, error) {
	a.pairs++
	return coreprotocol.RemotePairingCode{Code: "secret-pairing-code", ExpiresAt: time.Now().Add(time.Minute)}, a.err
}
func (a *remoteAdminStub) RevokeRemoteDevice(_ context.Context, id string) error {
	a.revoked = id
	a.view.Devices[0].RevokedAt = time.Now()
	return a.err
}

func remoteSettingsFixture(t *testing.T) (*Model, *WorkspaceSettingsSurface, *remoteAdminStub) {
	t.Helper()
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	m.SelectCategory(catNetworkRemoteAccess)
	s := NewSurface(theme.DefaultDark(), m)
	a := &remoteAdminStub{view: coreprotocol.RemoteAccessView{Config: coreprotocol.RemoteAccessConfig{Enabled: true}, Devices: []coreprotocol.RemoteDevice{{ID: "device-1", Name: "Laptop", LastSeen: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)}}}}
	cmd := s.SetRemoteAccessAdmin(a, true)
	if cmd == nil || a.reads != 0 || !m.remoteAccess.busy {
		t.Fatal("binding did I/O on Update or did not load")
	}
	m.Update(cmd())
	s.SetModel(m)
	return m, s, a
}

func TestRemoteAccessSettingsSparseRoundTripAndOtherPages(t *testing.T) {
	configured := network.DefaultSettings()
	configured.RemoteAccessEnabled = true
	configured.RemoteQUICAddress = "[::]:7440"
	configured.RemoteTLSAddress = "0.0.0.0:7441"
	configured.SessionGraceSeconds = 1200
	configured.ProxyMode = network.ProxyModeCustom
	configured.ProxyURL = "socks5h://user:password@proxy.invalid:1080"
	configured.KeepAliveIntervalSeconds = 42
	configured.HTTP2PingTimeoutSeconds = 33
	for _, tc := range []struct {
		category        int
		id, value, path string
	}{
		{catNetworkConnection, fieldNetworkConnectTimeout, "51", "network.connect_timeout_seconds"},
		{catNetworkProxy, fieldNetworkNoProxy, "localhost", "network.no_proxy"},
		{catNetworkCertificates, fieldNetworkTLSMinimumVersion, "1.3", "network.tls_minimum_version"},
		{catNetworkRemoteAccess, fieldRemoteQUIC, "[::1]:7442", "network.remote_quic_address"},
		{catNetworkRemoteAccess, fieldRemoteTLS, "127.0.0.1:7443", "network.remote_tls_address"},
		{catNetworkRemoteAccess, fieldRemoteGrace, "1800", "network.session_grace_seconds"},
		{catNetworkRemoteAccess, fieldRemoteEnabled, "off", "network.remote_access_enabled"},
	} {
		t.Run(tc.path, func(t *testing.T) {
			m := New(theme.DefaultDark(), types.AppSettings{Network: configured}, nil)
			s := NewSurface(theme.DefaultDark(), m)
			s.SetRemoteAccessAdmin(nil, true)
			if m.AppSettings().Network != configured {
				t.Fatalf("initial collector lost network fields: %+v", m.AppSettings().Network)
			}
			m.SetValueForTest(tc.category, tc.id, tc.value)
			edits := m.Edits()
			if len(edits) != 1 || strings.Join(edits[0].Path, ".") != tc.path {
				t.Fatalf("not one sparse leaf: %+v", edits)
			}
			result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
			if result.Cmd == nil {
				t.Fatal("valid preference save unavailable")
			}
			saved := types.AppSettings{Network: configured}
			if err := types.ApplySettingsEdits(&saved, result.Cmd().(SaveMsg).Edits); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(saved.Network, m.AppSettings().Network) {
				t.Fatalf("sparse save differs from complete collector: %+v %+v", saved.Network, m.AppSettings().Network)
			}
		})
	}
}

func TestRemoteAccessWorkspaceAndRemoteHostReadOnly(t *testing.T) {
	app := types.AppSettings{Network: network.DefaultSettings()}
	app.Network.RemoteAccessEnabled = true
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		m := NewScoped(theme.DefaultDark(), app, nil, scope, app, types.WorkspaceOverrides{})
		m.SelectCategory(catNetworkRemoteAccess)
		s := NewSurface(theme.DefaultDark(), m)
		a := &remoteAdminStub{}
		if cmd := s.SetRemoteAccessAdmin(a, false); cmd != nil {
			t.Fatal("remote host initiated administration")
		}
		for _, id := range []string{fieldRemoteEnabled, fieldRemoteQUIC, fieldRemoteTLS, fieldRemoteGrace} {
			if !m.fieldAt(catNetworkRemoteAccess, id).ReadOnly {
				t.Fatalf("%s %s editable", scope, id)
			}
		}
		m.SetValueForTest(catNetworkRemoteAccess, fieldRemoteEnabled, "off")
		if !m.AppSettings().Network.RemoteAccessEnabled || len(m.Edits()) != 0 {
			t.Fatal("read-only edit escaped scope")
		}
		if cmd := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.pair"}).Cmd; cmd != nil {
			t.Fatal("remote generated pairing code")
		}
		if scope == types.SettingsScopeWorkspace {
			if cmd := s.SetRemoteAccessAdmin(a, true); cmd != nil {
				t.Fatal("workspace initiated admin")
			}
		}
	}
}

func TestRemoteAccessDeviceActionsRequireConfirmationAndAreAsync(t *testing.T) {
	m, s, a := remoteSettingsFixture(t)
	if !s.form.FocusField(fieldRemoteGrace) {
		t.Fatal("cannot focus scalar control")
	}
	m.SetValueForTest(catNetworkRemoteAccess, fieldRemoteGrace, "1800")
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.refresh"})
	if a.reads != 1 || result.Cmd == nil {
		t.Fatal("refresh did I/O on Update")
	}
	m.Update(result.Cmd())
	s.SetModel(m)
	if s.form.HelpField().ID != fieldRemoteGrace || m.value(catNetworkRemoteAccess, fieldRemoteGrace) != "1800" || len(m.Edits()) != 1 {
		t.Fatal("refresh replaced focus/draft")
	}
	result = s.HandleAction(ui.EventContext{}, ui.Action{ID: remoteRevokePrefix + "device-1"})
	prompt := result.Cmd().(PromptMsg)
	if a.revoked != "" || prompt.Kind != PromptRevokeRemoteDevice || !s.PromptRelevant(prompt) {
		t.Fatal("revoke did not require exact prompt")
	}
	if cmd := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.confirm-revoke", Payload: "wrong"}).Cmd; cmd != nil {
		t.Fatal("wrong confirmation admitted")
	}
	result = s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.confirm-revoke", Payload: prompt.ID})
	if a.revoked != "" || result.Cmd == nil {
		t.Fatal("revoke did I/O on Update")
	}
	m.Update(result.Cmd())
	s.SetModel(m)
	if a.revoked != "device-1" || s.PromptRelevant(prompt) || !m.fieldAt(catNetworkRemoteAccess, remoteRevokePrefix+"device-1").Disabled {
		t.Fatal("confirmed revoke did not settle device")
	}
}

func TestRemoteAccessClosedFormNeverStartsQueuedAdministration(t *testing.T) {
	for _, operation := range []string{"refresh", "pair", "revoke"} {
		t.Run(operation, func(t *testing.T) {
			m, s, a := remoteSettingsFixture(t)
			cmd := m.beginRemoteAccess(operation, "device-1")
			if cmd == nil {
				t.Fatal("expected queued operation")
			}
			s.CancelRemoteAccess()
			result := cmd().(RemoteAccessResultMsg)
			if !errors.Is(result.Err, context.Canceled) {
				t.Fatalf("result error=%v", result.Err)
			}
			if a.reads != 1 || a.pairs != 0 || a.revoked != "" {
				t.Fatalf("closed form performed administration: %+v", a)
			}
		})
	}
}

func TestRemoteAccessPairingSensitiveExpiryAndStaleResults(t *testing.T) {
	m, s, a := remoteSettingsFixture(t)
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.pair"})
	if result.Cmd == nil || a.pairs != 0 {
		t.Fatal("pair ran synchronously")
	}
	msg := result.Cmd().(RemoteAccessResultMsg)
	if strings.Contains(fmt.Sprintf("%+v %#v", msg, msg), "secret-pairing-code") {
		t.Fatal("result formats secret")
	}
	_, expiry := m.Update(msg)
	s.SetModel(m)
	if expiry == nil || a.pairs != 1 {
		t.Fatal("pair code has no expiry")
	}
	field := m.fieldAt(catNetworkRemoteAccess, fieldRemoteCode)
	if field == nil || field.Type != ui.FieldNav || !field.ReadOnly || s.form.Field(fieldRemoteCode).Control() != nil {
		t.Fatal("pairing secret exposed through clipboard editor")
	}
	id := ui.NewID(s.form.ID(), fieldRemoteCode)
	if s.ControlValueAllowed(id) {
		t.Fatal("pairing secret available through semantic read")
	}
	if effect, human, _ := s.ControlMetadata(id, "settings.remote.pair"); effect != control.Authority || !human {
		t.Fatal("pairing bypasses human authority")
	}
	m.Update(RemotePairingExpiredMsg{Target: m, Generation: msg.Generation})
	s.SetModel(m)
	if m.fieldAt(catNetworkRemoteAccess, fieldRemoteCode) != nil {
		t.Fatal("expired code remains visible")
	}
	s.CancelRemoteAccess()
	m.Update(msg)
	if m.fieldAt(catNetworkRemoteAccess, fieldRemoteCode) != nil {
		t.Fatal("stale result restored secret")
	}
	// A page switch also clears sensitive state and invalidates pending prompts.
	r := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.refresh"})
	m.SelectCategory(catEditor)
	m.Update(r.Cmd())
	if m.remoteAccess.busy || m.remoteAccess.pairing.Code != "" {
		t.Fatal("page switch retained administrative operation")
	}
}

func TestRemoteAccessErrorAndRenderedPage(t *testing.T) {
	m, s, a := remoteSettingsFixture(t)
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 70, H: 18}} {
		plain := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(plain, "Remote access") || !strings.Contains(plain, "Allow remote acce") || !strings.Contains(plain, "Save") {
			t.Fatalf("page missing shared form/footer at %+v:\n%s", size, plain)
		}
	}
	a.err = errors.New("forbidden: local host required")
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.refresh"})
	m.Update(result.Cmd())
	s.SetModel(m)
	if m.remoteAccess.busy || !strings.Contains(strings.Join(m.fieldAt(catNetworkRemoteAccess, fieldRemoteStatus).Columns, " "), "forbidden") || !m.fieldAt(catNetworkRemoteAccess, "settings.remote.pair").Disabled {
		t.Fatal("failed refresh did not expose forbidden state")
	}
}

func TestRemoteAccessValidationFocusAndStaleRevokePrompt(t *testing.T) {
	m, s, _ := remoteSettingsFixture(t)
	m.SetValueForTest(catNetworkRemoteAccess, fieldRemoteTLS, "host:65536")
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"}); result.Cmd != nil {
		t.Fatal("invalid listen address emitted Save")
	}
	if m.SelectedCategory() != catNetworkRemoteAccess || !m.fieldAt(catNetworkRemoteAccess, fieldRemoteTLS).HasErrors() || s.form.HelpField().ID != fieldRemoteTLS {
		t.Fatal("invalid remote setting did not retain page and focus offending field")
	}
	m.SetValueForTest(catNetworkRemoteAccess, fieldRemoteTLS, "127.0.0.1:7340")
	request := s.HandleAction(ui.EventContext{}, ui.Action{ID: remoteRevokePrefix + "device-1"})
	prompt := request.Cmd().(PromptMsg)
	refresh := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.refresh"})
	m.Update(refresh.Cmd())
	s.SetModel(m)
	if s.PromptRelevant(prompt) {
		t.Fatal("old revoke decision survived a device snapshot generation boundary")
	}
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 70, H: 18}} {
		host := ui.NewHost(theme.DefaultDark(), s)
		host.Resize(size.W, size.H)
		host.Layout()
		if !s.form.FocusField(remoteRevokePrefix + "device-1") {
			t.Fatal("device action is not keyboard reachable")
		}
		plain := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(plain, "Revoke Laptop") || !strings.Contains(plain, "Last seen") {
			t.Fatalf("device identity/last-seen not rendered at %+v:\n%s", size, plain)
		}
	}
}
