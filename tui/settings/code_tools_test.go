package settings

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

type codeToolsStub struct {
	inventory                types.CodeToolInventory
	preview                  types.CodeToolRemovalPreview
	reads, previews, removes int
	unused                   bool
	ids                      []string
	err                      error
	removeErr                error
}

func (s *codeToolsStub) ReadCodeToolInventory(_ context.Context, offset, limit int) (types.CodeToolInventory, error) {
	s.reads++
	out := s.inventory
	out.HistoryOffset = offset
	return out, s.err
}
func (s *codeToolsStub) PreviewCodeToolRemoval(_ context.Context, ids []string, unused bool) (types.CodeToolRemovalPreview, error) {
	s.previews++
	s.ids, s.unused = ids, unused
	return s.preview, s.err
}
func (s *codeToolsStub) RemoveCodeTools(_ context.Context, preview types.CodeToolRemovalPreview) error {
	s.removes++
	if preview.Token != s.preview.Token {
		return errors.New("wrong preview")
	}
	if s.err == nil {
		s.inventory.Tools = nil
	}
	if s.removeErr != nil {
		return s.removeErr
	}
	return s.err
}

func codeToolsFixture(t *testing.T) (*Model, *WorkspaceSettingsSurface, *codeToolsStub) {
	t.Helper()
	tool := types.CodeTool{ID: "fixture/v1-digest", Package: "fixture", Version: "v1", Size: 2048, Dir: "C:/managed/fixture", Executable: "C:/managed/fixture/files/server",
		Source: "test registry", URL: "https://example.test/tool", SHA256: "reviewed-sha256", DigestOrigin: "first-download",
		Licenses: []string{"MIT"}, InstalledBy: "automatic with user approval", Approval: "review-123",
		Workspaces: []string{"C:/project"}, InstalledAt: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC),
		FileEntries: []types.CodeToolFile{{Path: "files/server", Kind: "file", Size: 2048}, {Path: "manifest.json", Kind: "file", Size: 100}}}
	stub := &codeToolsStub{inventory: types.CodeToolInventory{Tools: []types.CodeTool{tool}, TotalBytes: 2048,
		Unmanaged: []types.UnmanagedCodeTool{{Name: "personal-server", Executable: "C:/user/bin/server", Source: "path"}},
		History:   []types.CodeToolHistoryEvent{{Kind: "install-failed", Package: "other", Detail: "download failed", By: "user"}}},
		preview: types.CodeToolRemovalPreview{Tools: []types.CodeTool{tool}, Paths: []string{tool.Dir}, Bytes: 2148, Token: "native-review"}}
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	m.SelectCategory(catLanguageServers)
	s := NewSurface(theme.DefaultDark(), m)
	cmd := s.SetCodeTools(stub)
	if cmd == nil || stub.reads != 0 {
		t.Fatal("binding must defer I/O")
	}
	m.Update(cmd())
	s.SetModel(m)
	return m, s, stub
}

func applyCodeToolsCommand(t *testing.T, m *Model, s *WorkspaceSettingsSurface, action string) {
	t.Helper()
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: action})
	if result.Cmd == nil {
		t.Fatalf("missing command: %s", action)
	}
	m.Update(result.Cmd())
	s.SetModel(m)
}

func TestCodeToolsSettingsDetailsAndRemovalNeedExactNativeDecision(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	for index, row := range s.lsp.table.Rows() {
		if row.ID == "managed/fixture/v1-digest" {
			s.lsp.table.SelectIndex(index)
			break
		}
	}
	s.lsp.openTool()
	for _, want := range []string{"test registry", "https://example.test/tool", "reviewed-sha256", "MIT", "review-123", "C:/project", "Installed:"} {
		if !strings.Contains(s.lsp.form.Field("tool-overview").FieldState().Value.String(), want) {
			t.Fatalf("accountability missing %q", want)
		}
	}
	s.lsp.detailPage = "files"
	s.lsp.showDetail()
	if !strings.Contains(s.lsp.form.Field("tool-files").FieldState().Value.String(), "manifest.json") {
		t.Fatal("exact files absent")
	}
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 80, H: 24}} {
		host := ui.NewHost(theme.DefaultDark(), s)
		host.Resize(size.W, size.H)
		host.Layout()
		if !s.lsp.form.FocusField("tool-files") {
			t.Fatal("managed file list is not keyboard reachable")
		}
		plain := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(plain, "manifest.json") || !strings.Contains(plain, "files/server") {
			t.Fatalf("exact managed files were not rendered at %+v:\n%s", size, plain)
		}
	}
	if result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.confirm-remove"}); result.Cmd != nil {
		t.Fatal("unreviewed action removed tools")
	}
	applyCodeToolsCommand(t, m, s, "settings.code-tools.remove")
	if stub.removes != 0 || m.codeTools.preview == nil || stub.previews != 1 {
		t.Fatal("preview changed files or missing review")
	}
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 80, H: 24}} {
		plain := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(plain, "Review removal") || !strings.Contains(plain, "Will remove") || !strings.Contains(plain, "Remove") || !strings.Contains(plain, "Close") {
			t.Fatalf("review not rendered at %+v:\n%s", size, plain)
		}
	}
	result := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"})
	prompt := result.Cmd().(PromptMsg)
	if !s.PromptRelevant(prompt) || prompt.Kind != PromptRemoveCodeTools || stub.removes != 0 {
		t.Fatal("native confirmation missing")
	}
	applyCodeToolsCommand(t, m, s, "settings.code-tools.confirm-remove")
	if stub.removes != 1 || s.PromptRelevant(prompt) || len(m.codeTools.inventory.Tools) != 0 || m.codeTools.preview != nil {
		t.Fatal("remove outcome did not refresh/invalidate review")
	}
}

func TestCodeToolsSettingsUnusedCancellationAndStaleResults(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	applyCodeToolsCommand(t, m, s, "settings.code-tools.remove-unused")
	if !stub.unused || len(stub.ids) != 0 {
		t.Fatal("Remove unused selected a different operation")
	}
	prompt := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"}).Cmd().(PromptMsg)
	m.SelectCategory(catEditor)
	if s.PromptRelevant(prompt) || m.codeTools.preview != nil {
		t.Fatal("page switch retained consent")
	}
	m.SelectCategory(catLanguageServers)
	s.SetModel(m)
	cmd := m.beginCodeTools("refresh", nil, false, 0)
	s.CancelCodeTools()
	before := stub.reads
	m.Update(cmd())
	if stub.reads != before || m.codeTools.busy {
		t.Fatal("closed Settings started queued I/O")
	}
	m.Update(CodeToolsResultMsg{Target: m, Generation: m.codeTools.generation - 1, Operation: "preview", Preview: stub.preview})
	if m.codeTools.preview != nil {
		t.Fatal("old result reopened removal")
	}
}

func TestCodeToolsSettingsMountedInventoryAndFailure(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	for index, row := range s.lsp.table.Rows() {
		if row.ID == "external/C:/user/bin/server" {
			s.lsp.table.SelectIndex(index)
			break
		}
	}
	s.lsp.openTool()
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 80, H: 24}} {
		host := ui.NewHost(theme.DefaultDark(), s)
		host.Resize(size.W, size.H)
		host.Layout()
		if !s.lsp.form.FocusField("tool-overview") {
			t.Fatal("unmanaged tool not keyboard reachable")
		}
		plain := bufferPlain(paintAccountSurface(t, s, size.W, size.H))
		if !strings.Contains(plain, "outside Crude") || !strings.Contains(plain, "cannot remove") {
			t.Fatalf("unmanaged protection not rendered:\n%s", plain)
		}
		if strings.Contains(plain, "[ Save ]") {
			t.Fatal("inventory offered preference Save")
		}
	}
	if s.lsp.bar.ActionEnabled("settings.code-tools.remove") {
		t.Fatal("unmanaged tool offered removal")
	}
	stub.err = errors.New("disk unavailable")
	applyCodeToolsCommand(t, m, s, "settings.code-tools.refresh")
	if !strings.Contains(m.codeTools.notice, "disk unavailable") || m.codeTools.busy {
		t.Fatal("read failure missing")
	}
}

func TestCodeToolsSettingsRemoveFailureObservesInventoryWithoutReplay(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	applyCodeToolsCommand(t, m, s, "settings.code-tools.remove-unused")
	s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"})
	stub.removeErr = errors.New("could not clean the removal folder")
	applyCodeToolsCommand(t, m, s, "settings.code-tools.confirm-remove")
	if stub.removes != 1 || stub.reads != 2 || len(m.codeTools.inventory.Tools) != 0 ||
		m.codeTools.preview != nil || !strings.Contains(m.codeTools.notice, "could not clean") {
		t.Fatal("removal error did not observe the actual store without replay")
	}
}

func TestLanguageToolsCombinesConfigurationAndInstallOwnership(t *testing.T) {
	managed := types.CodeTool{ID: "fixture/v1-digest", Package: "fixture", Version: "v1",
		Command: "fixture-server", Executable: "/crude/fixture/bin/fixture-server", Size: 2048,
		FileEntries: []types.CodeToolFile{{Path: "bin/fixture-server", Kind: "file", Size: 2048}}}
	stub := &codeToolsStub{inventory: types.CodeToolInventory{
		Tools:     []types.CodeTool{managed},
		Unmanaged: []types.UnmanagedCodeTool{{Name: "personal", Command: "personal-server", Executable: "/user/bin/personal-server", Source: "path"}},
	}}
	cfg := types.DefaultLSPSettings()
	cfg.Languages["fixture"] = types.LSPLanguageSettings{Enabled: true, Command: "fixture-server", Extensions: []string{".fx"}, LanguageID: "fixture"}
	cfg.Languages["personal"] = types.LSPLanguageSettings{Enabled: true, Command: "personal-server", Extensions: []string{".ps"}, LanguageID: "personal"}
	m := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
	m.SelectCategory(catLanguageServers)
	s := NewSurface(theme.DefaultDark(), m)
	cmd := s.SetCodeTools(stub)
	m.Update(cmd())
	s.SetModel(m)

	var names []string
	for _, index := range m.visibleCategoryIndices() {
		names = append(names, m.categories[index].Name)
	}
	for _, obsolete := range []string{"Language Servers", "Code intelligence tools"} {
		if strings.Contains(strings.Join(names, "\n"), obsolete) {
			t.Fatalf("separate page %q remains in the Settings rail: %v", obsolete, names)
		}
	}
	if !strings.Contains(strings.Join(names, "\n"), "Language Tools") {
		t.Fatalf("combined page is missing: %v", names)
	}
	var rows strings.Builder
	for _, row := range s.lsp.table.Rows() {
		for _, cell := range row.Cells {
			rows.WriteString(cell.Text)
			rows.WriteByte(' ')
		}
		rows.WriteByte('\n')
	}
	for _, want := range []string{"fixture", "Crude", "personal", "Outside Crude"} {
		if !strings.Contains(rows.String(), want) {
			t.Fatalf("combined table is missing %q:\n%s", want, rows.String())
		}
	}
}

func TestLanguageToolsMountedManagedRemovalAndExternalProtection(t *testing.T) {
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 80, H: 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			tool := types.CodeTool{ID: "fixture/v1-digest", Package: "fixture", Version: "v1",
				Command: "fixture-server", Executable: "/crude/fixture/server", Dir: "/crude/fixture",
				FileEntries: []types.CodeToolFile{{Path: "files/server", Kind: "file", Size: 12}}}
			stub := &codeToolsStub{
				inventory: types.CodeToolInventory{Tools: []types.CodeTool{tool},
					Unmanaged: []types.UnmanagedCodeTool{{Name: "personal", Command: "personal-server", Executable: "/user/bin/server", Source: "path"}}},
				preview: types.CodeToolRemovalPreview{Token: "reviewed", Tools: []types.CodeTool{tool}, Paths: []string{tool.Dir}, Bytes: 12},
			}
			cfg := types.DefaultLSPSettings()
			cfg.Languages["fixture"] = types.LSPLanguageSettings{Enabled: true, Command: tool.Executable, Extensions: []string{".fx"}, LanguageID: "fixture"}
			cfg.Languages["personal"] = types.LSPLanguageSettings{Enabled: true, Command: "personal-server", Extensions: []string{".ps"}, LanguageID: "personal"}
			m := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
			m.SelectCategory(catLanguageServers)
			h := newEditorHarness(t, m, size.W, size.H)
			cmd := h.surface.SetCodeTools(stub)
			m.Update(cmd())
			h.surface.SetModel(m)
			h.host.MarkLayout()
			h.host.Layout()
			h.host.Focus(h.surface.lsp.filter.ID())
			for _, r := range "fixture" {
				h.press(tea.KeyPressMsg{Code: r, Text: string(r)})
			}
			h.press(tea.KeyPressMsg{Code: tea.KeyDown})
			h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
			if h.surface.lsp.detail == nil || m.codeTools.detail != tool.ID {
				t.Fatal("keyboard did not open the exact managed tool")
			}
			h.host.MarkLayout()
			h.host.Layout()
			h.host.Focus(h.surface.lsp.tabs.ID())
			h.press(tea.KeyPressMsg{Code: tea.KeyRight})
			if h.surface.lsp.form.Field("configure/fixture") == nil {
				t.Fatal("launch tab did not contain the tool's configuration")
			}
			h.host.Focus(h.surface.lsp.form.ID())
			if !h.surface.lsp.form.FocusField("configure/fixture") {
				t.Fatal("configuration control is not reachable")
			}
			h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
			if h.surface.lsp.draft == nil || h.surface.lsp.editing != "fixture" {
				t.Fatal("configuration did not open the server editor")
			}
			h.press(tea.KeyPressMsg{Code: tea.KeyEsc})
			if h.surface.lsp.draft != nil || h.surface.lsp.detail == nil {
				t.Fatal("back from configuration did not return to the tool")
			}
			if !h.surface.lsp.bar.ActionEnabled("settings.code-tools.remove") {
				t.Fatal("managed tool has no removal review")
			}
			h.host.Focus(h.surface.lsp.bar.ID())
			h.surface.lsp.bar.FocusFirst()
			result := h.host.Handle(tea.KeyPressMsg{Code: tea.KeyEnter})
			if result.Cmd == nil {
				t.Fatal("removal did not request a preview")
			}
			m.Update(result.Cmd())
			h.surface.SetModel(m)
			if stub.previews != 1 || stub.removes != 0 || len(stub.ids) != 1 || stub.ids[0] != tool.ID || m.codeTools.preview == nil {
				t.Fatalf("wrong removal preview: ids=%v previews=%d removes=%d", stub.ids, stub.previews, stub.removes)
			}
			if h.surface.lsp.form.Field("code-tools-delete-0") == nil {
				t.Fatal("exact deletion review is not in the combined page")
			}
			h.surface.lsp.backTool()
			h.surface.lsp.backTool()
			h.surface.lsp.filter.SetValue("personal")
			h.surface.lsp.refreshRows()
			if len(h.surface.lsp.table.Rows()) != 1 {
				t.Fatalf("external tool was duplicated: %v", h.surface.lsp.table.Rows())
			}
			h.surface.lsp.openTool()
			if h.surface.lsp.bar.ActionEnabled("settings.code-tools.remove") || m.codeTools.detail != "" {
				t.Fatal("external tool was given a destructive action")
			}
		})
	}
}

func TestLanguageToolsInstalledFilterAndNarrowOwnership(t *testing.T) {
	cfg := types.DefaultLSPSettings()
	cfg.Languages["personal"] = types.LSPLanguageSettings{
		Enabled: true, Command: "personal-server", Extensions: []string{".ps"}, LanguageID: "personal",
	}
	m := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
	m.SelectCategory(catLanguageServers)
	h := newEditorHarness(t, m, 80, 24)
	stub := &codeToolsStub{inventory: types.CodeToolInventory{
		Unmanaged: []types.UnmanagedCodeTool{{Name: "personal", Command: "personal-server", Executable: "/user/bin/server", Source: "path"}},
	}}
	cmd := h.surface.SetCodeTools(stub)
	m.Update(cmd())
	h.surface.SetModel(m)
	h.host.MarkLayout()
	h.host.Layout()
	h.host.Focus(h.surface.lsp.installed.ID())
	h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !h.surface.lsp.installed.Checked() || len(h.surface.lsp.table.Rows()) != 1 {
		t.Fatal("Installed filter did not select the observed installation")
	}
	painted := bufferPlain(paintAccountSurface(t, h.surface, 80, 24))
	for _, text := range []string{"Installed by", "Outside Crude"} {
		if !strings.Contains(painted, text) {
			t.Fatalf("narrow table hides %q:\n%s", text, painted)
		}
	}
}

func TestLanguageToolsModelReplacementCancelsOldReview(t *testing.T) {
	m, s, stub := codeToolsFixture(t)
	for index, row := range s.lsp.table.Rows() {
		if row.ID == "managed/fixture/v1-digest" {
			s.lsp.table.SelectIndex(index)
		}
	}
	s.lsp.openTool()
	applyCodeToolsCommand(t, m, s, "settings.code-tools.remove")
	prompt := s.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.code-tools.remove"}).Cmd().(PromptMsg)
	replacement := New(theme.DefaultDark(), types.AppSettings{}, nil)
	replacement.SelectCategory(catLanguageServers)
	s.SetModel(replacement)
	if s.PromptRelevant(prompt) || s.lsp.detail != nil || m.codeTools.preview != nil || stub.removes != 0 {
		t.Fatal("old tool review survived replacement of its Settings owner")
	}
}

func TestLanguageToolsDetailTabsMountedKeyboardJourney(t *testing.T) {
	for _, size := range []ui.Size{{W: 124, H: 30}, {W: 80, H: 24}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			tool := types.CodeTool{
				ID: "fixture/v1-digest", Package: "fixture", Version: "v1",
				Executable:  "/managed/fixture/server",
				FileEntries: []types.CodeToolFile{{Path: "manifest.json", Kind: "file"}},
			}
			cfg := types.DefaultLSPSettings()
			cfg.Languages["fixture"] = types.LSPLanguageSettings{
				Enabled: true, Command: tool.Executable, Extensions: []string{".fx"}, LanguageID: "fixture",
			}
			m := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
			m.SelectCategory(catLanguageServers)
			h := newEditorHarness(t, m, size.W, size.H)
			stub := &codeToolsStub{inventory: types.CodeToolInventory{
				Tools:   []types.CodeTool{tool},
				History: []types.CodeToolHistoryEvent{{ToolID: tool.ID, Package: tool.Package, Kind: "install", By: "user"}},
			}}
			m.Update(h.surface.SetCodeTools(stub)())
			h.surface.SetModel(m)
			h.surface.lsp.filter.SetValue("fixture")
			h.surface.lsp.refreshRows()
			h.surface.lsp.openTool()
			h.host.MarkLayout()
			h.host.Layout()

			tabsID := ui.ComponentID("settings/language-tools/detail-tabs")
			if !h.host.Focus(tabsID) {
				t.Fatal("detail tabs are not mounted")
			}
			plain := bufferPlain(paintAccountSurface(t, h.surface, size.W, size.H))
			for _, label := range []string{"Overview", "Launch", "Files", "History"} {
				if !strings.Contains(plain, label) {
					t.Fatalf("%s tab is not visible at %v:\n%s", label, size, plain)
				}
			}
			h.press(tea.KeyPressMsg{Code: tea.KeyRight})
			if h.surface.lsp.form.Field("configure/fixture") == nil {
				t.Fatal("Launch tab did not expose the language configuration")
			}
			h.press(tea.KeyPressMsg{Code: tea.KeyRight})
			if field := h.surface.lsp.form.Field("tool-files"); field == nil || !strings.Contains(field.FieldState().Value.String(), "manifest.json") {
				t.Fatal("Files tab did not expose the exact managed files")
			}
			h.press(tea.KeyPressMsg{Code: tea.KeyRight})
			if field := h.surface.lsp.form.Field("tool-history"); field == nil || !strings.Contains(field.FieldState().Value.String(), "user") {
				t.Fatal("History tab did not expose the tool's history")
			}
			rect, ok := h.host.ScreenRect(tabsID)
			if !ok {
				t.Fatal("detail tabs have no bounds")
			}
			clickX := -1
			for x := 0; x < rect.W; x++ {
				index, _ := h.surface.lsp.tabs.HitTest(x)
				if index == 2 {
					clickX = x
					break
				}
			}
			if clickX < 0 {
				t.Fatal("Files tab has no pointer target")
			}
			h.host.Handle(tea.MouseClickMsg{X: rect.X + clickX, Y: rect.Y, Button: tea.MouseLeft})
			h.host.Handle(tea.MouseReleaseMsg{X: rect.X + clickX, Y: rect.Y, Button: tea.MouseLeft})
			if h.surface.lsp.form.Field("tool-files") == nil {
				t.Fatal("pointer did not switch to the Files tab")
			}
		})
	}
}

func TestLanguageToolsProjectStatusDoesNotGuess(t *testing.T) {
	server := lspServer{id: "fixture", cfg: types.LSPLanguageSettings{Command: "fixture-server"}}
	first := types.CodeTool{ID: "one", Executable: "/tools/one"}
	second := types.CodeTool{ID: "two", Executable: "/tools/two"}
	row := languageToolRow{managed: &second, servers: []lspServer{server}}
	plan := types.AssemblyPlan{State: types.AssemblyReady, Languages: []types.AssemblyLanguage{{
		ID: "fixture", Command: "fixture-server", Executable: first.Executable, Status: types.ToolReady,
	}}}
	if status, included := row.projectState(plan); status != "Not used" || included {
		t.Fatalf("unused installed version was marked ready: %q %v", status, included)
	}
	plan.State = types.AssemblyScanning
	if status, included := row.projectState(plan); status != "Checking" || included {
		t.Fatalf("an incomplete project produced a final state: %q %v", status, included)
	}
	plan.State = types.AssemblyReady
	plan.Languages[0].Status, plan.Languages[0].Executable = types.ToolMissing, ""
	if status, included := row.projectState(plan); status != "Not resolved" || !included {
		t.Fatalf("installed artifact was falsely marked absent: %q %v", status, included)
	}
}

func TestLanguageToolsProjectShowsEffectiveWorkspaceOverride(t *testing.T) {
	cfg := types.DefaultLSPSettings()
	cfg.Languages["fixture"] = types.LSPLanguageSettings{
		Enabled: true, Command: "default-server", Extensions: []string{".fx"}, LanguageID: "fixture",
	}
	m := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
	m.SelectCategory(catLanguageServers)
	s := NewSurface(theme.DefaultDark(), m)
	stub := &codeToolsStub{inventory: types.CodeToolInventory{
		Unmanaged: []types.UnmanagedCodeTool{{Name: "fixture", Command: "workspace-server", Executable: "/user/bin/workspace-server", Source: types.ToolSourceSettings}},
		Project: types.AssemblyPlan{State: types.AssemblyReady, Languages: []types.AssemblyLanguage{{
			ID: "fixture", Command: "workspace-server", Executable: "/user/bin/workspace-server",
			Source: types.ToolSourceSettings, Status: types.ToolReady,
		}}},
	}}
	m.Update(s.SetCodeTools(stub)())
	s.SetModel(m)
	rows := s.lsp.table.Rows()
	if len(rows) != 1 || rows[0].ID != "external//user/bin/workspace-server" ||
		len(rows[0].Cells) < 4 || rows[0].Cells[3].Text != "Ready" {
		t.Fatalf("application settings hid the project's workspace override: %+v", rows)
	}
	s.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.filter-all"})
	found := false
	for _, row := range s.lsp.table.Rows() {
		if row.ID == "configured/default-server" {
			found = true
			if row.Cells[2].Text != "Not checked" || row.Cells[3].Text != "Not used" {
				t.Fatalf("unresolved application default was misrepresented: %+v", row.Cells)
			}
		}
	}
	if !found {
		t.Fatal("workspace override hid the application configuration")
	}
}

func TestLanguageToolsProjectFilterUsesAssemblyEvidence(t *testing.T) {
	cfg := types.DefaultLSPSettings()
	for id, command := range map[string]string{
		"personal": "personal-server",
		"missing":  "missing-server",
		"other":    "other-server",
	} {
		cfg.Languages[id] = types.LSPLanguageSettings{
			Enabled: true, Command: command, Extensions: []string{"." + id}, LanguageID: id,
		}
	}
	m := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
	m.SelectCategory(catLanguageServers)
	s := NewSurface(theme.DefaultDark(), m)
	stub := &codeToolsStub{inventory: types.CodeToolInventory{
		Unmanaged: []types.UnmanagedCodeTool{{Name: "personal", Command: "personal-server", Executable: "/user/bin/server", Source: types.ToolSourcePath}},
		Project: types.AssemblyPlan{State: types.AssemblyReady, Generation: 4, Languages: []types.AssemblyLanguage{
			{ID: "personal", Command: "personal-server", Executable: "/user/bin/server", Source: types.ToolSourcePath, Status: types.ToolReady},
			{ID: "missing", Command: "missing-server", Status: types.ToolMissing},
		}},
	}}
	m.Update(s.SetCodeTools(stub)())
	s.SetModel(m)
	s.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.filter-project"})
	rows := s.lsp.table.Rows()
	if len(rows) != 2 {
		t.Fatalf("project filter included unrelated tools: %+v", rows)
	}
	byID := make(map[string]ui.TableRow)
	for _, row := range rows {
		byID[row.ID] = row
	}
	for id, want := range map[string][2]string{
		"external//user/bin/server": {"Outside Crude", "Ready"},
		"configured/missing-server": {"Not installed", "Not installed"},
	} {
		row, ok := byID[id]
		if !ok || len(row.Cells) < 4 || row.Cells[2].Text != want[0] || row.Cells[3].Text != want[1] {
			t.Fatalf("project status for %s = %+v, want %v", id, row.Cells, want)
		}
	}
	for index, row := range s.lsp.table.Rows() {
		if row.ID == "configured/missing-server" {
			s.lsp.table.SelectIndex(index)
			break
		}
	}
	s.lsp.openTool()
	detail := s.lsp.form.Field("tool-overview").FieldState().Value.String()
	if !strings.Contains(detail, "Not installed") || strings.Contains(detail, "has not checked") {
		t.Fatalf("tool detail contradicts the project table: %q", detail)
	}
	action := s.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.setup"})
	if action.Cmd == nil {
		t.Fatal("missing tool did not offer setup")
	}
	setup, ok := action.Cmd().(OpenProjectToolsMsg)
	if !ok || !s.ProjectToolsRequestRelevant(setup) {
		t.Fatal("missing tool did not open a current native setup request")
	}
	if setup.Selection != (types.ProjectToolSetupRequest{Explicit: true, LanguageID: "missing", AssemblyGeneration: 4}) {
		t.Fatalf("setup did not select the exact project tool: %+v", setup.Selection)
	}
	stale := setup
	stale.Selection.AssemblyGeneration++
	if s.ProjectToolsRequestRelevant(stale) {
		t.Fatal("a changed project generation was accepted")
	}
	s.lsp.backTool()
	if s.ProjectToolsRequestRelevant(setup) {
		t.Fatal("setup request from the old detail survived navigation")
	}
}

func TestLanguageToolsSingleToolSelectionRejectsAmbiguousPackages(t *testing.T) {
	row := languageToolRow{servers: []lspServer{
		{id: "first", cfg: types.LSPLanguageSettings{Command: "shared"}},
		{id: "second", cfg: types.LSPLanguageSettings{Command: "shared"}},
	}}
	plan := types.AssemblyPlan{State: types.AssemblyReady, Generation: 5, Languages: []types.AssemblyLanguage{
		{ID: "first", Command: "shared", Package: "first-package", Status: types.ToolMissing},
		{ID: "second", Command: "shared", Package: "second-package", Status: types.ToolMissing},
	}}
	if _, ok := row.setupTarget(plan); ok {
		t.Fatal("the same command from different packages was guessed")
	}
	plan.Languages[1].Package = "first-package"
	if selected, ok := row.setupTarget(plan); !ok || selected.LanguageID != "first" || selected.AssemblyGeneration != 5 {
		t.Fatalf("a shared package was not selected: %+v %v", selected, ok)
	}
	plan.Limited = true
	if _, ok := row.setupTarget(plan); ok {
		t.Fatal("an incomplete project was used for a single-tool review")
	}
}

func TestLanguageToolsRefreshIsVisibleOnNarrowPage(t *testing.T) {
	m := New(theme.DefaultDark(), types.AppSettings{}, nil)
	m.SelectCategory(catLanguageServers)
	h := newEditorHarness(t, m, 80, 24)
	stub := &codeToolsStub{}
	m.Update(h.surface.SetCodeTools(stub)())
	h.surface.SetModel(m)
	h.host.MarkLayout()
	h.host.Layout()
	id := ui.ComponentID("settings/language-tools/more")
	bar, ok := h.host.Component(id).(*ui.ActionBar)
	if !ok {
		t.Fatal("refresh actions are not mounted")
	}
	rect, _ := h.host.ScreenRect(id)
	button, ok := bar.ButtonBounds("settings.code-tools.refresh")
	if !ok || button.Empty() {
		t.Fatal("Refresh is not visible")
	}
	stub.err = errors.New("cannot read fixture")
	result := h.host.Handle(tea.MouseClickMsg{X: rect.X + button.X + 1, Y: rect.Y + button.Y, Button: tea.MouseLeft})
	if result.Cmd == nil {
		t.Fatal("Refresh did not start an inventory read")
	}
	m.Update(result.Cmd())
	h.surface.SetModel(m)
	if stub.reads != 2 || !strings.Contains(m.codeTools.notice, "cannot read fixture") {
		t.Fatal("Refresh did not report the new read failure")
	}
}

func TestLanguageToolsRemovingCustomConfigurationReturnsToList(t *testing.T) {
	cfg := types.DefaultLSPSettings()
	cfg.Languages["custom-fixture"] = types.LSPLanguageSettings{
		Enabled: true, Command: "custom-server", Extensions: []string{".fixture"}, LanguageID: "fixture",
	}
	m := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
	m.SelectCategory(catLanguageServers)
	h := newEditorHarness(t, m, 80, 24)
	h.surface.lsp.filter.SetValue("custom-server")
	h.surface.lsp.refreshRows()
	h.surface.lsp.openTool()
	h.host.MarkLayout()
	h.host.Layout()
	h.host.Focus(h.surface.lsp.tabs.ID())
	h.press(tea.KeyPressMsg{Code: tea.KeyRight})
	h.host.Focus(h.surface.lsp.form.ID())
	if !h.surface.lsp.form.FocusField("configure/custom-fixture") {
		t.Fatal("custom configuration is not reachable")
	}
	h.press(tea.KeyPressMsg{Code: tea.KeyEnter})
	if h.surface.lsp.draft == nil {
		t.Fatal("custom configuration editor did not open")
	}
	result := h.surface.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.remove"})
	if !result.Handled || h.surface.lsp.draft != nil || h.surface.lsp.detail != nil {
		t.Fatal("removing a custom configuration left an empty detail open")
	}
	if _, ok := m.lspLanguages["custom-fixture"]; ok {
		t.Fatal("custom configuration was not removed from the draft")
	}
}
