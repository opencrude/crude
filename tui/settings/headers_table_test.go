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

	connection := provider.Template{
		Name:        "OpenRouter",
		ProviderURL: "https://openrouter.ai/api/v1",
		APIStandard: "openai",
		AuthType:    provider.AuthProtocol,
		Headers:     map[string]string{"X-Trace": "enabled", "X-Tenant": "acme"},
	}
	return types.AppSettings{

		Accounts: map[string]provider.Account{
			"home": {ID: "home", ProviderID: "openrouter", Label: "Home", APIKey: "home-secret", Connection: connection},
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
	m.SelectCategory(catAccounts)
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	h := newEditorHarness(t, m, 120, 46)
	h.surface.FocusForm()
	if !h.surface.form.FocusField(accountFieldHeaders) {
		t.Fatal("template editor could not focus the header table")
	}
	grid := h.grid(accountFieldHeaders)
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

	change, err := m.accountEditChange()
	if err != nil {
		t.Fatalf("editor change: %v", err)
	}
	if got := change.After.Connection.Headers["X-New"]; got != "yes" {
		t.Fatalf("stored headers=%v", change.After.Connection.Headers)
	}
	if change.After.Connection.Headers["X-Tenant"] != "acme" {
		t.Fatalf("stored headers lost the pre-existing pairs: %v", change.After.Connection.Headers)
	}
	if _, kept := change.After.Connection.Headers["X-Trace"]; kept {
		t.Fatalf("stored headers lost the pre-existing pairs: %v", change.After.Connection.Headers)
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
	m.SelectCategory(catAccounts)
	if err := m.beginAccountEdit(false, ""); err != nil {
		t.Fatal(err)
	}
	h := newEditorHarness(t, m, 120, 46)
	buf := newTestBuffer(120, 46)
	h.host.Paint(buf)
	painted := bufferPlain(buf)
	for _, want := range []string{"Accounts", "Extra headers", "Header", "Value", "X-Tenant", "acme", "X-Trace", "enabled", "Add header"} {
		if !strings.Contains(painted, want) {
			t.Fatalf("painted editor missing %q:\n%s", want, painted)
		}
	}
	if strings.Contains(painted, "Name: value") {
		t.Fatalf("editor still documents the raw line syntax:\n%s", painted)
	}
}
