package viewmodel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/coreprotocol"
	"crude/remoteconn"
	"crude/shellport"
	"crude/tui/screens/connectionpopover"
	"crude/tui/screens/shellheader"
	settingsui "crude/tui/settings"
	"crude/tui/ui"
	"github.com/charmbracelet/x/ansi"
)

type integrationRemoteHost struct {
	shellport.Workspace
	local                 bool
	reads, pairs, revokes int
	view                  coreprotocol.RemoteAccessView
	requestContext        context.Context
}

func (h *integrationRemoteHost) LocalHost() bool { return h.local }
func (h *integrationRemoteHost) BackendStatus() shellport.RemoteStatus {
	return shellport.RemoteStatus{State: "connected", Carrier: "quic", RTT: 12 * time.Millisecond}
}
func (h *integrationRemoteHost) RemoteAccess(ctx context.Context) (coreprotocol.RemoteAccessView, error) {
	h.reads++
	h.requestContext = ctx
	return h.view, nil
}
func (h *integrationRemoteHost) CreateRemotePairing(context.Context) (coreprotocol.RemotePairingCode, error) {
	h.pairs++
	return coreprotocol.RemotePairingCode{Code: "secret-integration-code", ExpiresAt: time.Now().Add(100 * time.Millisecond)}, nil
}
func (h *integrationRemoteHost) RevokeRemoteDevice(_ context.Context, id string) error {
	h.revokes++
	h.view.Devices[0].RevokedAt = time.Now()
	return nil
}
func integrationRemoteSettings(t *testing.T, app *App) (*settingsui.WorkspaceSettingsSurface, int) {
	t.Helper()
	body := app.settingsSurface(app.overlayHost.Window(ui.OverlayWorkspaceSettings))
	if body == nil {
		t.Fatal("settings not mounted")
	}
	for i, c := range body.Model().Categories() {
		if c.Name == "Remote access" {
			body.Model().SelectCategory(i)
			body.SetModel(body.Model())
			return body, i
		}
	}
	t.Fatal("remote access page missing")
	return nil, 0
}
func integrationSettingsField(body *settingsui.WorkspaceSettingsSurface, index int, id string) *ui.FieldState {
	return body.Model().Categories()[index].Form.Field(id)
}

func TestRemoteUIWordmarkDetailsAndPaletteUseRetainedRemotes(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {60, 20}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			app.ws = &integrationRemoteHost{Workspace: app.ws}
			manager := &fakeRemoteManager{}
			app.SetRemoteManager(manager, "local")
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			app.paintShellFrame()
			r, _ := app.rootHost.ScreenRect(shellheader.ScreenID)
			row := rowText(app, r.Y)
			if !strings.Contains(row, "Crude") || strings.Contains(row, "Local") || strings.Contains(row, "quic") || strings.Contains(row, "12 ms") {
				t.Fatalf("header did not move link telemetry to the wordmark: %q", row)
			}
			anchor := app.connectionWordmarkRect()
			app.Update(tea.MouseMotionMsg{X: anchor.X + 2, Y: r.Y})
			if text := connectionPopoverText(app); !strings.Contains(text, "Local") || !strings.Contains(text, "QUIC") || !strings.Contains(text, "12 ms") {
				t.Fatalf("wordmark details lost live telemetry: %q", text)
			}
			fw := app.overlayHost.Window(ui.OverlayConnectionHealth)
			host := app.connectionHover.widget.Host()
			bar, ok := host.ScreenRect(connectionpopover.ActionsID)
			if !ok {
				t.Fatal("Connections action not mounted")
			}
			action, ok := host.Component(connectionpopover.ActionsID).(*ui.ActionBar).ButtonBounds("connections")
			if !ok {
				t.Fatal("Connections button not available")
			}
			x, y := fw.Position()
			_, cmd := app.Update(tea.MouseClickMsg{X: x + bar.X + action.X + 1, Y: y + bar.Y + action.Y, Button: tea.MouseLeft})
			runAppCommand(t, app, cmd)
			if app.activityView != activityRemotes {
				t.Fatal("wordmark's Connections action did not open retained Remotes")
			}
			for _, id := range []string{"remote.connect", "remote.switch"} {
				found := false
				for _, entry := range paletteEntriesFromRegistry(app.actionRegistry) {
					if entry.ID == id {
						found = true
					}
				}
				if !found {
					t.Fatalf("palette missing %s", id)
				}
				runAppCommand(t, app, app.executePaletteCommand(id))
				if app.activityView != activityRemotes {
					t.Fatal("palette used another navigation path")
				}
			}
			if paint := ansi.Strip(app.View().RenderContent()); !strings.Contains(paint, "Add connection") {
				t.Fatalf("retained rail missing: %s", paint)
			}
		})
	}
}

func TestRemoteUISettingsAuthorityAndResultLifecycle(t *testing.T) {
	app := newFullTestApp(t)
	wsOf(app).Logger = stubLogger{}
	host := &integrationRemoteHost{Workspace: app.ws, local: true, view: coreprotocol.RemoteAccessView{Config: coreprotocol.RemoteAccessConfig{Enabled: true}, Devices: []coreprotocol.RemoteDevice{{ID: "device-1", Name: "Laptop"}}}}
	app.ws = host
	app.openApplicationSettings()
	body, index := integrationRemoteSettings(t, app)
	if host.reads != 0 {
		t.Fatal("opening Settings performed I/O")
	}
	_, cmd := app.Update(struct{}{})
	runAppCommand(t, app, cmd)
	if host.reads != 1 || !strings.Contains(integrationSettingsField(body, index, "remote-access-status").Columns[0], "1 paired") {
		t.Fatal("initial load not forwarded to mounted model")
	}
	result := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.pair"})
	message := result.Cmd().(settingsui.RemoteAccessResultMsg)
	_, expiry := app.Update(message)
	if expiry == nil || integrationSettingsField(body, index, "remote-pairing-code") == nil {
		t.Fatal("pair code or expiry command was lost")
	}
	runAppCommand(t, app, expiry)
	if integrationSettingsField(body, index, "remote-pairing-code") != nil {
		t.Fatal("expired pairing code remains visible")
	}
	result = body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.revoke/device-1"})
	runAppCommand(t, app, result.Cmd)
	if app.settingsPrompt == nil || host.revokes != 0 {
		t.Fatal("revoke bypassed confirmation")
	}
	_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	runAppCommand(t, app, cmd)
	if host.revokes != 0 {
		t.Fatal("Cancel default revoked device")
	}
	result = body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.revoke/device-1"})
	runAppCommand(t, app, result.Cmd)
	runAppCommand(t, app, app.activateSettingsPrompt(0))
	if host.revokes != 1 {
		t.Fatal("confirmed revoke not forwarded")
	}
	// Close fences a result even though it retains the same model pointer.
	result = body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.refresh"})
	late := result.Cmd()
	app.overlayHost.Close(ui.OverlayWorkspaceSettings)
	app.openApplicationSettings()
	replacement, _ := integrationRemoteSettings(t, app)
	runAppCommand(t, app, func() tea.Msg { return late })
	if replacement.Model() == body.Model() {
		t.Fatal("reopened form reused closed authority")
	}
	host.local = false
	app.openApplicationSettings()
	readonly, ri := integrationRemoteSettings(t, app)
	if !integrationSettingsField(readonly, ri, "remote-access-enabled").ReadOnly {
		t.Fatal("remote attachment gained host authority from local backend label")
	}
	app.cancelRemoteSettings()
}

func TestRemoteUISettingsSaveAndResetRebindExactReplacement(t *testing.T) {
	for _, reset := range []bool{false, true} {
		t.Run(fmt.Sprint(reset), func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			host := &integrationRemoteHost{Workspace: app.ws, local: true}
			app.ws = host
			app.openApplicationSettings()
			body, index := integrationRemoteSettings(t, app)
			runAppCommand(t, app, app.drainRemoteUICommands())
			old := body.Model()
			if !old.BeginSave() {
				t.Fatal("save did not begin")
			}
			if reset {
				app.applySettingsShortcut(settingsShortcutResultMsg{owner: host, generation: app.workspaceSwitchGen.Load(), reset: old, category: index})
			} else {
				app.applyPreferencesSaved(preferencesSavedMsg{target: old, owner: host, generation: app.workspaceSwitchGen.Load()})
			}
			if body.Model() == old {
				t.Fatal("save/reset did not replace model")
			}
			runAppCommand(t, app, app.drainRemoteUICommands())
			if host.reads != 2 {
				t.Fatalf("replacement admin was not rebound: reads=%d", host.reads)
			}
			app.Update(settingsui.RemoteAccessResultMsg{Target: old, Generation: 1, View: coreprotocol.RemoteAccessView{Devices: []coreprotocol.RemoteDevice{{ID: "stale"}}}})
			if strings.Contains(integrationSettingsField(body, index, "remote-access-status").Columns[0], "1 paired") {
				t.Fatal("stale old-model result changed replacement")
			}
		})
	}
}

func TestRemoteUISettingsMountedRenderAndCancelledPairAtBothSizes(t *testing.T) {
	for _, size := range [][2]int{{160, 45}, {60, 20}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			host := &integrationRemoteHost{Workspace: app.ws, local: true, view: coreprotocol.RemoteAccessView{Config: coreprotocol.RemoteAccessConfig{Enabled: true}, Devices: []coreprotocol.RemoteDevice{{ID: "device-1", Name: "Laptop"}}}}
			app.ws = host
			app.Update(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
			app.openApplicationSettings()
			body, index := integrationRemoteSettings(t, app)
			runAppCommand(t, app, app.drainRemoteUICommands())
			paint := ansi.Strip(app.View().RenderContent())
			if !strings.Contains(paint, "Remote access") || !strings.Contains(paint, "Allow remot") || !strings.Contains(paint, "Save") {
				t.Fatalf("mounted remote Settings page is not usable: %s", paint)
			}
			result := body.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.pair"})
			late := result.Cmd().(settingsui.RemoteAccessResultMsg)
			app.cancelRemoteSettings()
			app.Update(late)
			if integrationSettingsField(body, index, "remote-pairing-code") != nil {
				t.Fatal("cancelled pairing result republished a secret")
			}
			app.openApplicationSettings()
			replacement, replacementIndex := integrationRemoteSettings(t, app)
			runAppCommand(t, app, app.drainRemoteUICommands())
			result = replacement.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.remote.pair"})
			fresh := result.Cmd().(settingsui.RemoteAccessResultMsg)
			_, expiry := app.Update(fresh)
			app.Update(settingsui.RemotePairingExpiredMsg{Target: body.Model(), Generation: fresh.Generation})
			if integrationSettingsField(replacement, replacementIndex, "remote-pairing-code") == nil {
				t.Fatal("stale old-form expiry hid the replacement's fresh pairing code")
			}
			runAppCommand(t, app, expiry)
		})
	}
}

type integrationInstallOffer struct {
	calls   int
	install func(context.Context) error
}

func (*integrationInstallOffer) Error() string { return "matching install required" }
func (*integrationInstallOffer) RemoteInstallSummary() string {
	return "Host build.example, linux/amd64, exact build abc123 in /private/.crude/remote-bin"
}
func (o *integrationInstallOffer) InstallRemote(ctx context.Context) error {
	o.calls++
	if o.install != nil {
		return o.install(ctx)
	}
	return nil
}

type integrationInstallManager struct {
	fakeRemoteManager
	offer *integrationInstallOffer
	tests int
}

func (f *integrationInstallManager) Test(context.Context, remoteconn.Connection) (shellport.RemoteStatus, error) {
	f.tests++
	if f.offer.calls == 0 {
		return shellport.RemoteStatus{}, f.offer
	}
	return shellport.RemoteStatus{State: "connected", Carrier: "ssh"}, nil
}

func TestRemoteUIInstallConsentCancelApproveAndStale(t *testing.T) {
	for _, action := range []string{"cancel", "narrow-cancel", "yes", "pointer-yes", "replacement", "changed", "stop", "queued-close", "manager", "generation"} {
		t.Run(action, func(t *testing.T) {
			app := newFullTestApp(t)
			wsOf(app).Logger = stubLogger{}
			offer := &integrationInstallOffer{}
			manager := &integrationInstallManager{offer: offer}
			manager.err = offer
			app.SetRemoteManager(manager, "local")
			width, height := 100, 35
			if action == "narrow-cancel" {
				width, height = 60, 20
			}
			app.Update(tea.WindowSizeMsg{Width: width, Height: height})
			app.setActivity(activityRemotes, true)
			remoteClickItem(t, app, "remote.add")
			p := app.remotes.editor
			p.Model.SetValue("name", ui.StringValue("Retain draft"))
			p.Model.SetValue("host", ui.StringValue("build.example"))
			remoteAct(t, app, p, "save")
			if offer.calls != 0 || app.remotes.install == nil {
				t.Fatal("missing explicit install consent or eager installer")
			}
			paint := ansi.Strip(app.View().RenderContent())
			if !strings.Contains(paint, "abc123") || !strings.Contains(paint, "Cancel") {
				t.Fatalf("exact immutable preview missing: %s", paint)
			}
			switch action {
			case "cancel", "narrow-cancel":
				_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				runAppCommand(t, app, cmd)
			case "pointer-yes":
				fw := app.overlayHost.Window(remoteInstallOverlay)
				px, py := fw.Position()
				w, h := fw.Size()
				clicked := false
				for y := 0; y < h && !clicked; y++ {
					for x := 0; x < w; x++ {
						if confirmFrameButtonAt(fw, x, y) == 0 {
							_, cmd := app.Update(tea.MouseClickMsg{X: px + x, Y: py + y, Button: tea.MouseLeft})
							runAppCommand(t, app, cmd)
							clicked = true
							break
						}
					}
				}
				if !clicked {
					t.Fatal("install button not clickable")
				}
			case "yes":
				_, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
				runAppCommand(t, app, cmd)
				_, cmd = app.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
				runAppCommand(t, app, cmd)
			case "replacement":
				app.openRemoteEditor(remoteconn.Connection{ID: "other", Name: "other", Host: "other", Kind: "ssh"}, false, "Replacement", "")
				runAppCommand(t, app, app.resolveRemoteInstall(true))
			case "changed":
				p.Model.SetValue("host", ui.StringValue("changed.example"))
				runAppCommand(t, app, app.resolveRemoteInstall(true))
			case "stop":
				app.StopRemotes()
				runAppCommand(t, app, app.resolveRemoteInstall(true))
			case "manager":
				app.SetRemoteManager(&fakeRemoteManager{}, "local")
				runAppCommand(t, app, app.resolveRemoteInstall(true))
			case "generation":
				app.workspaceSwitchGen.Add(1)
				runAppCommand(t, app, app.resolveRemoteInstall(true))
			case "queued-close":
				cmd := app.resolveRemoteInstall(true)
				app.cancelRemoteEditor()
				runAppCommand(t, app, cmd)
			}
			want := 0
			if action == "yes" || action == "pointer-yes" {
				want = 1
			}
			if offer.calls != want || manager.saves != 1 || manager.attaches != 0 || len(manager.snapshot.Connections) != 0 {
				t.Fatalf("unexpected effects install=%d save=%d attach=%d", offer.calls, manager.saves, manager.attaches)
			}
			if (action == "yes" || action == "pointer-yes") && (manager.tests != 1 || app.remotes.editor != p) {
				t.Fatal("approved install did not only retest retained draft")
			}
		})
	}
}

var _ shellport.LocalHostAuthority = (*integrationRemoteHost)(nil)
var _ shellport.RemoteAccessAdmin = (*integrationRemoteHost)(nil)
var _ shellport.RemoteInstallOffer = (*integrationInstallOffer)(nil)

func TestRemoteUIInstallCancellationJoinsBoundedJob(t *testing.T) {
	app := newFullTestApp(t)
	started := make(chan struct{})
	offer := &integrationInstallOffer{install: func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	}}
	manager := &integrationInstallManager{offer: offer}
	manager.err = offer
	app.SetRemoteManager(manager, "local")
	app.setActivity(activityRemotes, true)
	remoteClickItem(t, app, "remote.add")
	p := app.remotes.editor
	p.Model.SetValue("name", ui.StringValue("Keep draft"))
	p.Model.SetValue("host", ui.StringValue("build.example"))
	remoteAct(t, app, p, "save")
	cmd := app.resolveRemoteInstall(true)
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	<-started
	app.StopRemotes()
	if offer.calls != 1 || len(app.remotes.jobs) != 0 {
		t.Fatal("shutdown failed to join exact install owner")
	}
	runAppCommand(t, app, func() tea.Msg { return <-result })
	if manager.tests != 0 || manager.saves != 1 || manager.attaches != 0 {
		t.Fatal("cancelled installation retested or replayed an effect")
	}
}

func TestRemoteUISavedTestInstallOnlyRetestsSavedTarget(t *testing.T) {
	app := newFullTestApp(t)
	offer := &integrationInstallOffer{}
	manager := &integrationInstallManager{offer: offer}
	manager.snapshot.Connections = []remoteconn.Connection{{ID: "saved", Name: "Saved host", Kind: "ssh", Host: "build.example", Carrier: "auto"}}
	app.SetRemoteManager(manager, "local")
	runAppCommand(t, app, app.OpenRemotes())
	remoteClickItem(t, app, "remote:saved")
	remoteClickItem(t, app, "remote:saved:test:")
	if app.remotes.install == nil || app.remotes.editor == nil || offer.calls != 0 {
		t.Fatal("saved Test failed to request exact install consent")
	}
	runAppCommand(t, app, app.resolveRemoteInstall(true))
	if offer.calls != 1 || manager.tests != 2 || manager.saves != 0 || manager.attaches != 0 {
		t.Fatal("saved install replayed Save/Open or did not retest")
	}
}

func TestRemoteUIFailedAddPreservesDraftAndRedactsSecret(t *testing.T) {
	app := newFullTestApp(t)
	manager := &fakeRemoteManager{err: fmt.Errorf("pairing secret %s rejected", "private-code")}
	app.SetRemoteManager(manager, "local")
	app.setActivity(activityRemotes, true)
	remoteClickItem(t, app, "remote.add")
	p := app.remotes.editor
	p.Model.SetValue("name", ui.StringValue("Keep draft"))
	p.Model.SetValue("host", ui.StringValue("build.example"))
	p.Model.SetValue("code", ui.StringValue("private-code"))
	remoteAct(t, app, p, "save")
	if p.Model.Field("code").Value.String() != "private-code" {
		t.Fatal("failed add discarded unsaved draft")
	}
	if strings.Contains(app.remotes.errors[app.remotes.editorID], "private-code") {
		t.Fatal("secret leaked into ordinary remote status")
	}
	if paint := ansi.Strip(app.View().RenderContent()); strings.Contains(paint, "private-code") {
		t.Fatal("secret leaked into rendered error")
	}
}
