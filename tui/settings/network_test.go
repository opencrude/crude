package settings

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"crude/network"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
)

func TestSettingsNetworkGroupOrderAndSearch(t *testing.T) {
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		model := NewScoped(theme.DefaultDark(), types.AppSettings{}, nil, scope, types.AppSettings{}, types.WorkspaceOverrides{})
		var headers []string
		var networkPages []string
		group := ""
		for _, item := range model.CategoryItems() {
			switch {
			case item.Header:
				headers = append(headers, item.Label)
				group = item.Label
			case !item.Spacer && group == "Network":
				networkPages = append(networkPages, item.Label)
			}
		}
		if want := []string{"AI", "Security", "IDE", "Network"}; !reflect.DeepEqual(headers, want) {
			t.Fatalf("scope %s group order = %v, want %v", scope, headers, want)
		}
		if want := []string{"Connection", "Proxy", "Certificates"}; !reflect.DeepEqual(networkPages, want) {
			t.Fatalf("scope %s Network pages = %v, want %v", scope, networkPages, want)
		}

		for query, want := range map[string]string{
			"remote DNS":  "Proxy",
			"self-signed": "Certificates",
			"keep-alive":  "Connection",
		} {
			model.SetSearchQuery(query)
			items := model.CategoryItems()
			if len(items) != 2 || !items[0].Header || items[0].Label != "Network" || items[1].Label != want {
				t.Fatalf("scope %s query %q items = %#v, want Network/%s", scope, query, items, want)
			}
		}
	}
}

func TestNetworkSettingsRoundTripAndValidation(t *testing.T) {
	pin := strings.Repeat("ab", 32)
	configured := network.Settings{
		ProxyMode:                  network.ProxyModeCustom,
		ProxyURL:                   "socks5h://user:password@proxy.example:1080",
		NoProxy:                    "localhost,.internal.example,10.0.0.0/8",
		ConnectTimeoutSeconds:      45,
		TLSHandshakeTimeoutSeconds: 12,
		KeepAliveSeconds:           40,
		IdleConnTimeoutSeconds:     120,
		MaxIdleConnsPerHost:        24,
		DisableHTTP2:               true,
		TLSMinimumVersion:          network.TLSMinimumVersion13,
		CAFile:                     "/secure/roots.pem",
		ClientCertificateFile:      "/secure/client.pem",
		ClientKeyFile:              "/secure/client.key",
		ClientCertificateHost:      "api.example",
		TrustedCertificateHost:     "lab.example",
		TrustedCertificateSHA256:   pin,
	}
	model := New(theme.DefaultDark(), types.AppSettings{Network: configured}, nil)
	if got, want := model.AppSettings().Network, network.Normalize(configured); !reflect.DeepEqual(got, want) {
		t.Fatalf("network settings round trip = %+v, want %+v", got, want)
	}

	root := NewSurface(theme.DefaultDark(), model)
	model.SetValueForTest(catNetworkConnection, fieldNetworkMaxIdleConnsPerHost, "1001")
	result := root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if result.Cmd != nil {
		t.Fatal("out-of-range network setting emitted SaveMsg")
	}
	if model.SelectedCategory() != catNetworkConnection {
		t.Fatalf("invalid network save selected category %d, want Connection", model.SelectedCategory())
	}
	if field := model.fieldAt(catNetworkConnection, fieldNetworkMaxIdleConnsPerHost); field == nil || !field.HasErrors() {
		t.Fatal("network.Settings.Validate error was not attached to max idle connections")
	}

	model.SetValueForTest(catNetworkConnection, fieldNetworkMaxIdleConnsPerHost, "25")
	model.SetValueForTest(catNetworkCertificates, fieldNetworkClientKeyFile, "")
	result = root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if result.Cmd != nil {
		t.Fatal("incomplete mTLS identity emitted SaveMsg")
	}
	if model.SelectedCategory() != catNetworkCertificates {
		t.Fatalf("incomplete mTLS identity selected category %d, want Certificates", model.SelectedCategory())
	}
	if field := model.fieldAt(catNetworkCertificates, fieldNetworkClientKeyFile); field == nil || !field.HasErrors() {
		t.Fatal("mTLS relationship error was not attached to the client key field")
	}
}

func TestNetworkWorkspaceScopeIsReadOnlyAndNeverOverridesApplication(t *testing.T) {
	app := types.AppSettings{Network: network.Settings{
		ProxyMode:                network.ProxyModeCustom,
		ProxyURL:                 "https://user:password@proxy.example:8443",
		NoProxy:                  ".internal.example",
		ConnectTimeoutSeconds:    44,
		TLSMinimumVersion:        network.TLSMinimumVersion13,
		TrustedCertificateHost:   "lab.example",
		TrustedCertificateSHA256: strings.Repeat("cd", 32),
	}}
	types.ApplyAppDefaults(&app)
	working := app
	working.Network.ProxyURL = "https://workspace-must-not-win.example"
	working.Network.ConnectTimeoutSeconds = 99

	model := NewScoped(theme.DefaultDark(), working, nil, types.SettingsScopeWorkspace, app, types.WorkspaceOverrides{})
	for _, category := range []int{catNetworkConnection, catNetworkProxy, catNetworkCertificates} {
		for _, field := range model.categories[category].Form.Fields() {
			if field.Type == ui.FieldSeparator || field.Type == ui.FieldSpacer {
				continue
			}
			if !field.ReadOnly {
				t.Fatalf("workspace network field %q on category %d is editable", field.ID, category)
			}
		}
	}
	if got := model.value(catNetworkProxy, fieldNetworkProxyURL); got != app.Network.ProxyURL {
		t.Fatalf("workspace proxy URL = %q, want inherited application value", got)
	}
	model.SetValueForTest(catNetworkConnection, fieldNetworkConnectTimeout, "88")
	if got := model.value(catNetworkConnection, fieldNetworkConnectTimeout); got != "44" {
		t.Fatalf("workspace edited application-owned timeout to %q", got)
	}
	if got := model.AppSettings().Network; !reflect.DeepEqual(got, app.Network) {
		t.Fatalf("workspace AppSettings network = %+v, want application baseline %+v", got, app.Network)
	}
	baseline := NewScoped(theme.DefaultDark(), app, nil, types.SettingsScopeWorkspace, app, types.WorkspaceOverrides{})
	if got, want := model.Overrides(), baseline.Overrides(); !reflect.DeepEqual(got, want) {
		t.Fatalf("workspace Network values changed the override payload: got=%+v want=%+v", got, want)
	}

	model.SelectCategory(catNetworkProxy)
	root := NewSurface(theme.DefaultDark(), model)
	plain := bufferPlain(paintAccountSurface(t, root, 100, 25))
	if !strings.Contains(plain, "Inherited from Application Settings") {
		t.Fatalf("workspace Network page omitted inheritance explanation:\n%s", plain)
	}
	if strings.Contains(plain, "Reset section") {
		t.Fatalf("workspace Network page exposed a reset action for a non-overridable section:\n%s", plain)
	}
	if strings.Contains(plain, app.Network.ProxyURL) {
		t.Fatal("masked proxy credentials were painted as plain text")
	}
	field := root.form.Field(fieldNetworkProxyURL)
	if field == nil || field.Control() == nil || !field.Control().TextSecret() {
		t.Fatal("proxy URL is not backed by a secret TextField")
	}
}

func TestNetworkScopeSwitchRebuildsApplicationOwnership(t *testing.T) {
	app := types.AppSettings{Network: network.Settings{
		ProxyMode:             network.ProxyModeCustom,
		ProxyURL:              "https://user:password@proxy.example:8443",
		ConnectTimeoutSeconds: 41,
	}}
	types.ApplyAppDefaults(&app)
	model := NewScoped(theme.DefaultDark(), app, nil, types.SettingsScopeApplication, app, types.WorkspaceOverrides{})
	root := NewSurface(theme.DefaultDark(), model)
	if field := model.fieldAt(catNetworkConnection, fieldNetworkConnectTimeout); field == nil || field.ReadOnly {
		t.Fatal("Application Network field is not editable")
	}

	model.SetScope(types.SettingsScopeWorkspace)
	root.SetModel(model)
	model.SelectCategory(catNetworkConnection)
	root.refreshForm()
	if field := model.fieldAt(catNetworkConnection, fieldNetworkConnectTimeout); field == nil || !field.ReadOnly || field.Value.String() != "41" {
		t.Fatalf("Workspace scope did not rebuild an inherited read-only Network field: %+v", field)
	}
	if root.showReset() {
		t.Fatal("Workspace Network scope exposed Reset section after switching")
	}

	model.SetScope(types.SettingsScopeApplication)
	root.SetModel(model)
	model.SelectCategory(catNetworkConnection)
	root.refreshForm()
	if field := model.fieldAt(catNetworkConnection, fieldNetworkConnectTimeout); field == nil || field.ReadOnly || field.Value.String() != "41" {
		t.Fatalf("Application scope did not restore editable Network fields: %+v", field)
	}
}

func TestNetworkDirtyDiscardAndAsyncSaveLifecycle(t *testing.T) {
	model := New(theme.DefaultDark(), types.AppSettings{}, nil)
	root := NewSurface(theme.DefaultDark(), model)
	model.SetValueForTest(catNetworkProxy, fieldNetworkProxyMode, network.ProxyModeCustom)
	model.SetValueForTest(catNetworkProxy, fieldNetworkProxyURL, "socks5h://user:password@proxy.example:1080")
	if !model.hasUnsavedPreferences() {
		t.Fatal("network edit did not mark settings dirty")
	}
	prompt := settingsPrompt(t, root.RequestClose())
	if prompt.Kind != PromptDiscardSettings || !root.PromptRelevant(prompt) {
		t.Fatalf("dirty Network close did not request a relevant discard decision: %+v", prompt)
	}

	result := root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if result.Cmd == nil || !model.Saving() {
		t.Fatal("valid Network save did not enter the retained saving state")
	}
	message, ok := result.Cmd().(SaveMsg)
	if !ok || message.Target != model || message.AppSettings.Network.ProxyURL != "socks5h://user:password@proxy.example:1080" {
		t.Fatalf("save message lost target or Network values: %#v", message)
	}
	before := model.value(catNetworkConnection, fieldNetworkConnectTimeout)
	model.SetValueForTest(catNetworkConnection, fieldNetworkConnectTimeout, "88")
	if got := model.value(catNetworkConnection, fieldNetworkConnectTimeout); got != before {
		t.Fatalf("pending save accepted an edit: got %q want %q", got, before)
	}
	if duplicate := root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"}); duplicate.Cmd != nil {
		t.Fatal("pending save emitted a duplicate SaveMsg")
	}
	if close := root.RequestClose(); close.Cmd != nil {
		t.Fatal("pending save allowed Settings to close")
	}
	other := New(theme.DefaultDark(), types.AppSettings{}, nil)
	if root.FinishSave(other, errors.New("wrong owner")) {
		t.Fatal("save result for a replacement model was accepted")
	}
	saveErr := errors.New("certificate file could not be read")
	if !root.FinishSave(model, saveErr) || model.Saving() || model.SaveError() != saveErr.Error() {
		t.Fatalf("failed save did not restore the retained draft: saving=%v error=%q", model.Saving(), model.SaveError())
	}
	if got := model.value(catNetworkProxy, fieldNetworkProxyURL); got != "socks5h://user:password@proxy.example:1080" || !model.hasUnsavedPreferences() {
		t.Fatalf("failed save lost the dirty proxy draft: value=%q dirty=%v", got, model.hasUnsavedPreferences())
	}

	retry := root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if retry.Cmd == nil || !model.Saving() {
		t.Fatal("failed Network save could not be retried")
	}
	if !root.FinishSave(model, nil) || model.Saving() || model.hasUnsavedPreferences() || model.SaveError() != "" {
		t.Fatalf("successful save did not commit the retained baseline: saving=%v dirty=%v error=%q", model.Saving(), model.hasUnsavedPreferences(), model.SaveError())
	}
}

func TestNetworkPagesPaintAtDesktopAndNarrowSizes(t *testing.T) {
	for _, page := range []struct {
		category int
		field    string
		label    string
	}{
		{catNetworkConnection, fieldNetworkDisableHTTP2, "Force HTTP/1.1"},
		{catNetworkProxy, fieldNetworkProxyURL, "Proxy URL"},
		{catNetworkCertificates, fieldNetworkTrustedCertificatePin, "Leaf SHA-256"},
	} {
		for _, size := range []ui.Size{{W: 124, H: 30}, {W: 70, H: 18}} {
			model := New(theme.DefaultDark(), types.AppSettings{}, nil)
			model.SelectCategory(page.category)
			root := NewSurface(theme.DefaultDark(), model)
			root.refreshForm()
			host := ui.NewHost(theme.DefaultDark(), root)
			host.Resize(size.W, size.H)
			host.Layout()
			if !root.form.FocusField(page.field) {
				t.Fatalf("category=%d cannot focus %q", page.category, page.field)
			}
			host.MarkLayout()
			host.Layout()
			buffer := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buffer)
			plain := bufferPlain(buffer)
			if !strings.Contains(plain, page.label) || !strings.Contains(plain, "Save") {
				t.Fatalf("category=%d size=%+v clipped Network control or footer:\n%s", page.category, size, plain)
			}
		}
	}
}
