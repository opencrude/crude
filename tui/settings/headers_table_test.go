package settings

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

// headersFixture is a provider template that already carries two extra request
// headers, plus one named account that can override them.
func headersFixture() types.AppSettings {
	templates := provider.DefaultTemplates()
	templates["openrouter"] = types.ProviderConfig{
		Name:        "OpenRouter",
		ProviderURL: "https://openrouter.ai/api/v1",
		APIStandard: "openai",
		AuthType:    provider.AuthProtocol,
		Headers:     map[string]string{"X-Trace": "enabled", "X-Tenant": "acme"},
	}
	return types.AppSettings{
		ActiveProvider: "openrouter",
		Providers:      templates,
		Accounts: map[string]provider.Account{
			"home": {ID: "home", ProviderID: "openrouter", Label: "Home", APIKey: "home-secret"},
		},
		Agents: types.AgentsSettings{Categories: map[string]types.CategorySettings{"default": {Provider: "openrouter", AccountID: "home", Model: "m"}}},
	}
}

// editorHarness mounts the real settings surface in a window frame so tests can
// drive the same key path a user hits.
type editorHarness struct {
	surface *WorkspaceSettingsSurface
	host    *ui.Host
	model   *Model
	press   func(tea.KeyPressMsg)
}

func newEditorHarness(t *testing.T, m *Model, w, h int) *editorHarness {
	t.Helper()
	surfaceRoot := NewSurface(theme.DefaultDark(), m)
	frame := ui.NewWindowFrameWidget(theme.DefaultDark(), "settings-frame", ui.FrameWindow, "Settings", surfaceRoot, nil, true, w, h)
	surfaceRoot.SetFocusHandler(frame.Focus)
	host := frame.Host()
	host.Resize(w, h)
	host.Layout()
	return &editorHarness{
		surface: surfaceRoot,
		host:    host,
		model:   m,
		press: func(key tea.KeyPressMsg) {
			res := host.Handle(key)
			if res.Action != nil {
				_ = surfaceRoot.HandleAction(ui.EventContext{Theme: theme.DefaultDark()}, *res.Action)
			}
			host.MarkLayout()
			host.Layout()
		},
	}
}

func (h *editorHarness) grid(fieldID string) *ui.KeyValueGrid {
	field := h.surface.form.Field(fieldID)
	if field == nil {
		return nil
	}
	return field.Grid()
}

// TestTemplateEditorHeadersAreATable drives the header table through the real
// surface: open the add row, type a pair, and read what Save would store.
func TestTemplateEditorHeadersAreATable(t *testing.T) {
	m := New(theme.DefaultDark(), headersFixture(), nil)
	m.SelectCategory(catProviders)
	if err := m.beginProviderEdit(false, false); err != nil {
		t.Fatal(err)
	}
	h := newEditorHarness(t, m, 120, 46)
	h.surface.FocusForm()
	if !h.surface.form.FocusField(templateFieldHeaders) {
		t.Fatal("template editor could not focus the header table")
	}
	grid := h.grid(templateFieldHeaders)
	if grid == nil {
		t.Fatal("template editor kept a text input for extra headers")
	}
	if got := grid.Value(); got != "X-Tenant: acme\nX-Trace: enabled" {
		t.Fatalf("table did not load the stored header set: %q", got)
	}

	h.press(tea.KeyPressMsg{Code: tea.KeyDown})
	h.press(tea.KeyPressMsg{Code: tea.KeyDown})
	h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "X-New" {
		h.press(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "yes" {
		h.press(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	if grid.Count() != 3 {
		t.Fatalf("table rows=%d want 3", grid.Count())
	}
	// Removing the middle pair keeps both neighbours intact.
	h.press(tea.KeyPressMsg{Code: tea.KeyUp})
	h.press(tea.KeyPressMsg{Code: tea.KeyUp})
	h.press(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got, want := grid.Value(), "X-Tenant: acme\nX-New: yes"; got != want {
		t.Fatalf("row removal changed the set: %q want %q", got, want)
	}

	change, err := m.providerEditorChange()
	if err != nil {
		t.Fatalf("editor change: %v", err)
	}
	if got := change.After.Headers["X-New"]; got != "yes" {
		t.Fatalf("stored headers=%v", change.After.Headers)
	}
	if change.After.Headers["X-Tenant"] != "acme" {
		t.Fatalf("stored headers lost the pre-existing pairs: %v", change.After.Headers)
	}
	if _, kept := change.After.Headers["X-Trace"]; kept {
		t.Fatalf("stored headers lost the pre-existing pairs: %v", change.After.Headers)
	}
}

// TestAccountOverrideHeadersAreATable checks the inherited view and the
// override edit: the table is read-only until the override is switched on, then
// edits land in the account draft.
func TestAccountOverrideHeadersAreATable(t *testing.T) {
	m := New(theme.DefaultDark(), headersFixture(), nil)
	m.SelectCategory(catAccounts)
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	h := newEditorHarness(t, m, 120, 60)
	h.surface.FocusForm()
	if !h.surface.form.FocusField(accountOverrideHeaders) {
		t.Fatal("account editor could not focus the header table")
	}
	grid := h.grid(accountOverrideHeaders)
	if grid == nil {
		t.Fatal("account editor kept a text input for headers")
	}
	if !grid.ReadOnly() {
		t.Fatal("inherited header set must render read-only while the override is off")
	}

	// Switch the override on: the form rebuilds with an editable table.
	m.SetValueForTest(catAccounts, accountOverrideHeaders+"-enabled", "yes")
	h.surface.SetModel(m)
	h.surface.FocusForm()
	if !h.surface.form.FocusField(accountOverrideHeaders) {
		t.Fatal("account editor lost the header table after enabling the override")
	}
	grid = h.grid(accountOverrideHeaders)
	if grid == nil || grid.ReadOnly() {
		t.Fatal("enabled override must render an editable table")
	}
	// Two rows live in the table; Down twice reaches the add row.
	h.press(tea.KeyPressMsg{Code: tea.KeyDown})
	h.press(tea.KeyPressMsg{Code: tea.KeyDown})
	h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "X-Account" {
		h.press(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	for _, r := range "scoped" {
		h.press(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	if grid.Count() != 3 {
		t.Fatalf("override table rows=%d want 3", grid.Count())
	}

	change, err := m.accountEditChange()
	if err != nil {
		t.Fatalf("account change: %v", err)
	}
	if change.After == nil || change.After.Overrides.Headers == nil {
		t.Fatal("account draft has no header override")
	}
	if got := (*change.After.Overrides.Headers)["X-Account"]; got != "scoped" {
		t.Fatalf("override headers=%v", *change.After.Overrides.Headers)
	}
}

// TestSettingsWindowTitleStaysPutAndPaneCarriesBreadcrumb pins the title
// policy: the window frame names the scope only, and the right pane carries the
// page hierarchy while an editor is open.
func TestSettingsWindowTitleStaysPutAndPaneCarriesBreadcrumb(t *testing.T) {
	th := theme.DefaultDark()
	m := New(th, headersFixture(), nil)
	s := NewSurface(th, m)
	if got := s.WindowTitle(); got != "Application Settings" {
		t.Fatalf("window title=%q", got)
	}
	m.SelectCategory(catAccounts)
	if got := s.WindowTitle(); got != "Application Settings" {
		t.Fatalf("selecting a page changed the window title: %q", got)
	}
	m.SetScope(types.SettingsScopeWorkspace)
	if got := s.WindowTitle(); got != "Workspace Settings" {
		t.Fatalf("workspace window title=%q", got)
	}
	m.SetScope(types.SettingsScopeApplication)
	m.SelectCategory(catAccounts)
	s.syncModel()
	if got := s.PaneTitle(); !strings.Contains(got, "Accounts") {
		t.Fatalf("pane title=%q", got)
	}

	if err := m.beginProviderEdit(true, false); err != nil {
		t.Fatal(err)
	}
	if got := s.WindowTitle(); got != "Application Settings" {
		t.Fatalf("opening an editor changed the window title: %q", got)
	}
	if got, sep := s.PaneTitle(), th.Glyphs.BreadcrumbSep; !strings.Contains(got, "Providers "+sep+" Add template") {
		t.Fatalf("pane breadcrumb=%q", got)
	}
	m.cancelProviderEdit()
	if err := m.beginProviderEdit(false, false); err != nil {
		t.Fatal(err)
	}
	if got, sep := s.PaneTitle(), th.Glyphs.BreadcrumbSep; !strings.Contains(got, "Providers "+sep+" Edit template") {
		t.Fatalf("edit pane breadcrumb=%q", got)
	}
	m.cancelProviderEdit()
	if err := m.beginAccountEdit(true, "openrouter"); err != nil {
		t.Fatal(err)
	}
	if got, sep := s.PaneTitle(), th.Glyphs.BreadcrumbSep; !strings.Contains(got, "Accounts "+sep+" Add account") {
		t.Fatalf("account pane breadcrumb=%q", got)
	}
}

// TestSettingsHeaderTablePaintsInPane renders the template editor and asserts
// the pane paints the table header, both inherited pairs, and the add row.
func TestSettingsHeaderTablePaintsInPane(t *testing.T) {
	m := New(theme.DefaultDark(), headersFixture(), nil)
	m.SelectCategory(catProviders)
	if err := m.beginProviderEdit(false, false); err != nil {
		t.Fatal(err)
	}
	h := newEditorHarness(t, m, 120, 46)
	buf := newTestBuffer(120, 46)
	h.host.Paint(buf)
	painted := bufferPlain(buf)
	for _, want := range []string{"Providers", "Extra headers", "Name", "Value", "X-Tenant", "acme", "X-Trace", "enabled", "Add header"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("painted editor missing %q:\n%s", want, painted)
		}
	}
	if strings.Contains(painted, "Name: value") {
		t.Fatalf("editor still documents the raw line syntax:\n%s", painted)
	}
}
