package settings

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/types/control"
)

// This fixture mounts the new rows on the native settings surface without
// pretending to test the parent's not-yet-integrated collector/category hooks.
func predictionRowsModel(scope types.SettingsScope, cfg types.WorkspacePredictionSettings) *Model {
	m := NewScoped(theme.DefaultDark(), types.AppSettings{}, nil, scope, types.AppSettings{}, types.WorkspaceOverrides{})
	m.layerBase.Prediction = cfg
	m.categories[catLSP].Form = formModel(m.predictionFields(cfg)...)
	m.collectedBase.Prediction = m.collectPrediction()
	var baseline types.AppSettings
	_ = json.Unmarshal(m.preferencesBase, &baseline)
	baseline.Prediction = m.collectPrediction()
	m.preferencesBase, _ = json.Marshal(baseline)
	m.SelectCategory(catLSP)
	return m
}

func TestPredictionNativeRowsAndKeyboard(t *testing.T) {
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := predictionRowsModel(types.SettingsScopeWorkspace, types.DefaultWorkspacePredictionSettings())
			form := m.categories[catLSP].Form
			mode := form.Field("prediction.mode")
			if mode.Type != ui.FieldCycle || mode.Style != ui.StyleSegmented || mode.ReadOnly {
				t.Fatalf("not native segments: %+v", mode)
			}
			if got := []string{mode.Options[0].Label, mode.Options[1].Label, mode.Options[2].Label}; !slices.Equal(got, []string{"Off", "Light", "Full"}) {
				t.Fatal("mode choices", got)
			}
			limit := form.Field("prediction.hourly_token_limit")
			if limit.Type != ui.FieldText || !limit.Numeric || limit.Unit != "tokens" || limit.Value.String() != "0" {
				t.Fatalf("cap is not an explicit native number: %+v", limit)
			}
			protocol := form.Field("prediction.protocol")
			if protocol.Type != ui.FieldSelect {
				t.Fatal("protocol is not a shared selector")
			}
			var protocols []string
			for _, option := range protocol.Options {
				protocols = append(protocols, option.ID)
			}
			if !slices.Equal(protocols, []string{"chat", "openai-completions", "deepseek-fim", "codestral-fim"}) ||
				form.Field("prediction.endpoint_path").Type != ui.FieldText {
				t.Fatal("protocol or endpoint dialect changed", protocols)
			}
			help := ""
			for _, field := range form.Fields() {
				help += field.Description + "\n"
				if field.ReadOnly || !field.Value.Equal(field.Initial) || field.Dirty {
					t.Fatalf("opening row changed its draft: %+v", field)
				}
			}
			for _, text := range []string{"on demand", "while typing", "analysis Trust", "positive cap", "estimated", "not a price", "selected Aeron account", "sensitive and external files", "mount-relative", "hostname"} {
				if !strings.Contains(help, text) {
					t.Fatalf("help omitted %q: %s", text, help)
				}
			}
			s := NewSurface(theme.DefaultDark(), m)
			frame := ui.NewWindowFrameWidget(theme.DefaultDark(), "prediction-settings", ui.FrameWindow, "Settings", s, nil, true, size.W, size.H)
			s.SetFocusHandler(frame.Focus)
			host := frame.Host()
			host.Resize(size.W, size.H)
			host.Layout()
			s.form.FocusFirst()
			s.FocusForm()
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			for _, text := range []string{"AI predictions", "Off", "Hourly cap", "Prediction protocol", "Prediction endpoint"} {
				if !strings.Contains(bufferPlain(buf), text) {
					t.Fatalf("native frame omitted %q:\n%s", text, bufferPlain(buf))
				}
			}
			if size.W == 160 {
				for _, text := range []string{"Light", "Full", "Light: on demand"} {
					if !strings.Contains(bufferPlain(buf), text) {
						t.Fatalf("desktop frame omitted mode choices/help %q:\n%s", text, bufferPlain(buf))
					}
				}
			}
			if result := host.Handle(tea.KeyPressMsg{Code: tea.KeyRight}); !result.Handled {
				t.Fatal("native mode key not handled")
			}
			if got := m.collectPrediction(); got.Mode != types.PredictionLight || got.HourlyTokenLimit != 0 {
				t.Fatal("mode invented a cap", got)
			}
			form.ValidateAll()
			if !form.AnyErrors() {
				t.Fatal("enabled zero cap was accepted")
			}
			// Native text input, not a collector-only mutation, supplies the cap.
			if !s.form.FocusField("prediction.hourly_token_limit") {
				t.Fatal("cap cannot receive focus")
			}
			host.MarkLayout()
			host.Layout()
			host.Handle(tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl})
			host.Handle(tea.PasteMsg{Content: "4200"})
			host.Handle(tea.KeyPressMsg{Code: tea.KeyTab})
			form.ValidateAll()
			if form.AnyErrors() {
				t.Fatal("explicit cap rejected", limit.Errors, limit.Value.String())
			}
			if got := m.collectPrediction(); got != (types.WorkspacePredictionSettings{
				Mode: types.PredictionLight, HourlyTokenLimit: 4200, Protocol: types.PredictionChat,
			}) {
				t.Fatal("native edit was not collected exactly", got)
			}
			s.form.FocusField("prediction.mode")
			host.MarkLayout()
			host.Layout()
			host.Handle(tea.KeyPressMsg{Code: tea.KeyRight})
			host.Layout()
			host.Paint(buf)
			if m.collectPrediction().Mode != types.PredictionFull || !strings.Contains(bufferPlain(buf), "Full") {
				t.Fatal("Full selection was not collected/painted", bufferPlain(buf))
			}
		})
	}
}

func TestPredictionValidationRetainsInvalidDrafts(t *testing.T) {
	for _, tc := range []struct {
		name, field, value string
	}{
		{"zero enabled", "prediction.hourly_token_limit", "0"},
		{"negative", "prediction.hourly_token_limit", "-1"},
		{"too large", "prediction.hourly_token_limit", "1099511627777"},
		{"overflow", "prediction.hourly_token_limit", "9223372036854775808"},
		{"fraction", "prediction.hourly_token_limit", "4.2"},
		{"exponent", "prediction.hourly_token_limit", "1e3"},
		{"text", "prediction.hourly_token_limit", "many"},
		{"empty", "prediction.hourly_token_limit", ""},
		{"null", "prediction.hourly_token_limit", "null"},
		{"mode", "prediction.mode", "automatic"},
		{"protocol", "prediction.protocol", "guess"},
		{"chat path", "prediction.endpoint_path", "v1/completions"},
		{"FIM empty", "prediction.protocol", "deepseek-fim"},
		{"absolute URL", "prediction.endpoint_path", "https://example.invalid/completions"},
		{"traversal", "prediction.endpoint_path", "../completions"},
		{"query", "prediction.endpoint_path", "completions?secret=1"},
		{"escaped traversal", "prediction.endpoint_path", "%2e%2e/completions"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := predictionRowsModel(types.SettingsScopeWorkspace, types.WorkspacePredictionSettings{
				Mode: types.PredictionLight, HourlyTokenLimit: 4200, Protocol: types.PredictionChat,
			})
			form := m.categories[catLSP].Form
			form.SetValue(tc.field, ui.StringValue(tc.value))
			form.ValidateAll()
			field := form.Field(tc.field)
			if !form.AnyErrors() || len(field.Errors) == 0 {
				t.Fatal("invalid preference was not rejected")
			}
			_ = m.collectPrediction()
			if field.Value.String() != tc.value || !field.Dirty {
				t.Fatalf("collection repaired/lost invalid input: %+v", field)
			}
		})
	}
}

func TestPredictionSiblingsValidateTogetherWithoutNormalization(t *testing.T) {
	m := predictionRowsModel(types.SettingsScopeWorkspace, types.DefaultWorkspacePredictionSettings())
	form := m.categories[catLSP].Form
	form.SetValue("prediction.mode", ui.StringValue("full"))
	form.SetValue("prediction.hourly_token_limit", ui.StringValue("1099511627776"))
	for _, protocol := range []string{"openai-completions", "deepseek-fim", "codestral-fim"} {
		form.SetValue("prediction.endpoint_path", ui.StringValue(""))
		form.SetValue("prediction.protocol", ui.StringValue(protocol))
		form.ValidateAll()
		if !form.AnyErrors() {
			t.Fatal("FIM accepted without explicit path", protocol)
		}
		form.SetValue("prediction.endpoint_path", ui.StringValue("v1/completions"))
		form.ValidateAll()
		if form.AnyErrors() {
			t.Fatal("sibling correction left a stale error", protocol, form.Field("prediction.protocol").Errors)
		}
		if got := m.collectPrediction(); got.Mode != types.PredictionFull || got.HourlyTokenLimit != 1<<40 ||
			string(got.Protocol) != protocol || got.EndpointPath != "v1/completions" {
			t.Fatal("exact maximum/path/protocol lost", got)
		}
	}
	form.SetValue("prediction.protocol", ui.StringValue("chat"))
	form.ValidateAll()
	if !form.AnyErrors() || m.collectPrediction().EndpointPath != "v1/completions" {
		t.Fatal("chat silently cleared the FIM path")
	}
	form.SetValue("prediction.endpoint_path", ui.StringValue(""))
	form.ValidateAll()
	if form.AnyErrors() {
		t.Fatal("corrected chat remains invalid")
	}
	// Even the builder must leave an invalid enable visible for validation.
	invalid := types.WorkspacePredictionSettings{Mode: types.PredictionFull, Protocol: types.PredictionChat}
	fields := m.predictionFields(invalid)
	if fields[0].Value.String() != "full" || len(fields[0].Validator(fields[0].Value)) == 0 {
		t.Fatal("builder normalized an incomplete enable to Off")
	}
}

func TestPredictionApplicationRowsAndScopeBaselines(t *testing.T) {
	choice := types.WorkspacePredictionSettings{
		Mode: types.PredictionFull, HourlyTokenLimit: 4200, Protocol: types.PredictionDeepSeek, EndpointPath: "fim",
	}
	m := predictionRowsModel(types.SettingsScopeWorkspace, choice)
	// Pending workspace edits are not a new scope's authority.
	m.categories[catLSP].Form.SetValue("prediction.mode", ui.StringValue("off"))
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace, types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		// Exercise the new hook explicitly; SetScope integration belongs to
		// the parent and must have separate real-entry-point coverage.
		m.scope = scope
		m.syncPredictionScope()
		want := types.DefaultWorkspacePredictionSettings()
		if scope == types.SettingsScopeWorkspace {
			want = choice
		}
		if got := m.collectPrediction(); got != want || m.collectedBase.Prediction != want {
			t.Fatal("scope carried a draft or lost effective authority", scope, got, m.collectedBase.Prediction)
		}
		var baseline types.AppSettings
		if err := json.Unmarshal(m.preferencesBase, &baseline); err != nil || baseline.Prediction != want {
			t.Fatal("preference baseline did not follow scope", scope, err)
		}
		for _, field := range m.categories[catLSP].Form.Fields() {
			if field.ReadOnly != (scope == types.SettingsScopeApplication) || field.Dirty || !field.Initial.Equal(field.Value) ||
				field.Inherited || field.InheritedFrom != "" {
				t.Fatalf("scope row/baseline wrong: %s %+v", scope, field)
			}
			if scope == types.SettingsScopeApplication {
				if field.Value.String() != "Workspace only" || len(field.Options) != 0 || field.Validator != nil {
					t.Fatal("application has editable authority or choice metadata", field)
				}
				before := field.Value
				m.categories[catLSP].Form.SetValue(field.ID, ui.StringValue("full"))
				if !before.Equal(field.Value) {
					t.Fatal("read-only row accepted an edit")
				}
			}
		}
	}
	// After replacement the validators must read the mounted siblings, not
	// detached fields from the most recent predictionFields call.
	form := m.categories[catLSP].Form
	form.SetValue("prediction.hourly_token_limit", ui.StringValue("0"))
	form.ValidateAll()
	if len(form.Field("prediction.mode").Errors) == 0 {
		t.Fatal("scope replacement detached sibling validation")
	}
	form.SetValue("prediction.hourly_token_limit", ui.StringValue("123"))
	form.ValidateAll()
	if form.AnyErrors() {
		t.Fatal("scope replacement retained stale siblings")
	}
}

func TestPredictionScopeDoesNotReviveBlockedStoredAuthority(t *testing.T) {
	m := predictionRowsModel(types.SettingsScopeWorkspace, types.DefaultWorkspacePredictionSettings())
	m.storedOverrides.Prediction = json.RawMessage(`{"mode":"full","hourly_token_limit":4200,"protocol":"chat"}`)
	for _, scope := range []types.SettingsScope{types.SettingsScopeApplication, types.SettingsScopeWorkspace} {
		m.scope = scope
		m.syncPredictionScope()
		if m.collectPrediction() != types.DefaultWorkspacePredictionSettings() {
			t.Fatal("scope reconstructed authority from unverified stored data")
		}
	}
}

func TestPredictionApplicationNativeRowsCannotGrant(t *testing.T) {
	choice := types.WorkspacePredictionSettings{
		Mode: types.PredictionFull, HourlyTokenLimit: 4200, Protocol: types.PredictionDeepSeek, EndpointPath: "private-fim",
	}
	for _, size := range []ui.Size{{W: 160, H: 40}, {W: 80, H: 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			m := predictionRowsModel(types.SettingsScopeApplication, choice)
			s := NewSurface(theme.DefaultDark(), m)
			host := ui.NewHost(theme.DefaultDark(), s)
			host.Resize(size.W, size.H)
			host.Layout()
			buf := surface.NewCellBuffer(size.W, size.H)
			host.Paint(buf)
			plain := bufferPlain(buf)
			if !strings.Contains(plain, "Workspace only") || strings.Contains(plain, "private-fim") ||
				strings.Contains(plain, "DeepSeek") || strings.Contains(plain, "4200") {
				t.Fatal("application rendering exposed workspace authority", plain)
			}
			if m.collectPrediction() != types.DefaultWorkspacePredictionSettings() {
				t.Fatal("application collection carried a workspace preference")
			}
			if got := strings.Count(plain, "Workspace only"); got != 4 {
				t.Fatalf("only %d of four rows are visibly workspace-only:\n%s", got, plain)
			}
		})
	}
}

func TestPredictionCanonicalPathsAreDirectHumanAuthority(t *testing.T) {
	// This checks the authoritative classifier using literal expected paths.
	// Surface mapping and off-page Save admission require the parent's wiring.
	for _, path := range []string{
		"prediction.mode", "prediction.hourly_token_limit", "prediction.protocol", "prediction.endpoint_path",
		"prediction_root_identity", "prediction_connection_identity", "prediction_grant_id",
	} {
		if !control.SettingsAuthority(path) {
			t.Fatal("ordinary controls can change human prediction authority", path)
		}
	}
	for _, path := range []string{"editor.tab_size", "editor.word_wrap", "lsp.inlay_hints"} {
		if control.SettingsAuthority(path) {
			t.Fatal("ordinary neighbor became authority", path)
		}
	}
	if fieldPredictionMode != "prediction.mode" || fieldPredictionLimit != "prediction.hourly_token_limit" ||
		fieldPredictionProtocol != "prediction.protocol" || fieldPredictionEndpoint != "prediction.endpoint_path" {
		t.Fatal("field IDs no longer name the strict authority leaves")
	}
}
