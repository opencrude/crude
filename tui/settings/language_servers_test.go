package settings

import (
	"reflect"
	"strings"
	"testing"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

func TestLSPSettingsRoundTripPreservesServersAndResources(t *testing.T) {
	cfg := types.DefaultLSPSettings()
	cfg.Enabled = false
	cfg.MaxServers = 1
	cfg.ServerThreads = 3
	cfg.ServerMemoryMB = 512
	cfg.MaxDocumentBytes = 8 * mebibyte
	goCfg := cfg.Languages["go"]
	goCfg.Enabled = false
	goCfg.Environment = map[string]string{"GOMEMLIMIT": "500MiB"}
	goCfg.Settings = map[string]any{"buildFlags": []any{"-tags=some tag"}}
	cfg.Languages["go"] = goCfg
	cfg.Languages["python"] = types.LSPLanguageSettings{Enabled: true, Command: "basedpyright-langserver", Args: []string{"--stdio", "a path with spaces"}}
	cfg.Languages["ruby"] = types.LSPLanguageSettings{Enabled: true, Command: "ruby-lsp", LanguageID: "ruby", Extensions: []string{".rb"}, RootMarkers: []string{"Gemfile"}}
	model := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
	got := model.AppSettings().LSP
	if got.Enabled || got.Languages["go"].Enabled || !reflect.DeepEqual(got.Languages["ruby"], cfg.Languages["ruby"]) {
		t.Fatalf("lost config: %+v", got)
	}
	if !reflect.DeepEqual(got.Languages["python"].Args, cfg.Languages["python"].Args) || !reflect.DeepEqual(got.Languages["go"].Settings, cfg.Languages["go"].Settings) {
		t.Fatal("server definitions lost")
	}
	if got.ServerThreads != 3 || got.ServerMemoryMB != 512 || got.MaxServers != 1 || got.MaxDocumentBytes != 8*mebibyte {
		t.Fatalf("resource values lost: %+v", got)
	}
	if model.hasUnsavedPreferences() {
		t.Fatal("opening settings must not create unsaved changes")
	}
}

func TestLSPInvalidResourceBlocksSave(t *testing.T) {
	for _, tc := range []struct{ id, value string }{{"max-servers", "33"}, {"server-threads", "-1"}, {"server-memory", "garbage"}} {
		t.Run(tc.id+tc.value, func(t *testing.T) {
			model := New(theme.DefaultDark(), types.AppSettings{LSP: types.DefaultLSPSettings()}, nil)
			root := NewSurface(theme.DefaultDark(), model)
			model.SetValueForTest(catLSP, tc.id, tc.value)
			result := root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
			if result.Cmd != nil || model.saving {
				t.Fatal("invalid LSP configuration emitted SaveMsg")
			}
			if !model.fieldAt(catLSP, tc.id).HasErrors() || model.SaveError() == "" {
				t.Fatal("missing actionable validation")
			}
		})
	}
}

func TestCodeIntelligencePaintsResourcesWithUnitsAndScopedSave(t *testing.T) {
	defaults := types.AppSettings{LSP: types.DefaultLSPSettings()}
	model := NewScoped(theme.DefaultDark(), defaults, nil, types.SettingsScopeWorkspace, defaults, types.WorkspaceOverrides{})
	model.SelectCategory(catLSP)
	root := NewSurface(theme.DefaultDark(), model)
	host := ui.NewHost(theme.DefaultDark(), root)
	host.Resize(160, 70)
	host.Layout()
	buf := surface.NewCellBuffer(160, 70)
	host.Paint(buf)
	plain := bufferPlain(buf)
	for _, label := range []string{"Running servers", "Retire idle after", "Skip documents over", "Threads per server", "Memory per server", "seconds", "MiB"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("missing painted %q", label)
		}
	}
	model.SetValueForTest(catLSP, "server-threads", "4")
	result := root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if result.Cmd == nil {
		t.Fatal("valid scoped LSP save blocked")
	}
	msg, ok := result.Cmd().(SaveMsg)
	if !ok || msg.Scope != types.SettingsScopeWorkspace {
		t.Fatalf("bad save %+v", msg)
	}
	overrides := types.WorkspaceOverrides{}
	if err := types.ApplySettingsEdits(&overrides, msg.Edits); err != nil {
		t.Fatal(err)
	}
	if string(overrides.LSP) != `{"server_threads":4}` {
		t.Fatalf("unrelated LSP leaves overridden: %s", overrides.LSP)
	}
}

func TestLanguageServerEditorAppliesToDraft(t *testing.T) {
	model := New(theme.DefaultDark(), types.AppSettings{LSP: types.DefaultLSPSettings()}, nil)
	root := NewSurface(theme.DefaultDark(), model)
	model.SelectCategory(catLanguageServers)
	root.syncModel()
	root.lsp.begin("python")
	if root.lsp.draft == nil || root.PaneTitle() != "Language Servers ▸ Python (pyright)" {
		t.Fatalf("editor not open: %q", root.PaneTitle())
	}
	root.lsp.draft.SetValue(lspFieldArgs, ui.StringValue(`--stdio "a path with spaces"`))
	if got := model.AppSettings().LSP.Languages["python"].Args; !reflect.DeepEqual(got, []string{"--stdio", "a path with spaces"}) {
		t.Fatalf("args = %#v", got)
	}
	if !model.categoryDirty(catLanguageServers) || !root.hasUnsavedDrafts() {
		t.Fatal("server edit is not reported as unsaved")
	}
	root.lsp.back()
	if root.lsp.draft != nil {
		t.Fatal("back did not return to the server list")
	}
}

func TestLanguageServerCustomServerLifecycle(t *testing.T) {
	model := New(theme.DefaultDark(), types.AppSettings{LSP: types.DefaultLSPSettings()}, nil)
	root := NewSurface(theme.DefaultDark(), model)
	model.SelectCategory(catLanguageServers)
	root.syncModel()
	before := cloneLSPLanguages(model.AppSettings().LSP.Languages)

	// Back abandons an Add: nothing is left behind and nothing is unsaved.
	root.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.add"})
	if root.lsp.editing != "custom" {
		t.Fatalf("editing %q", root.lsp.editing)
	}
	root.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.cancel"})
	if root.lsp.draft != nil || !reflect.DeepEqual(model.AppSettings().LSP.Languages, before) || root.hasUnsavedDrafts() {
		t.Fatal("Back left an abandoned server behind")
	}

	root.lsp.HandleAction(ui.EventContext{}, ui.Action{ID: "lsp.add"})
	if result := root.lsp.save(); root.lsp.draft == nil || root.lsp.notice == "" || result.Action != nil {
		t.Fatal("an incomplete custom server must stay open with guidance and not be saved")
	}
	draft := root.lsp.draft
	draft.SetValue(lspFieldID, ui.StringValue("Ruby!"))
	if len(draft.Field(lspFieldID).Errors) == 0 || root.lsp.editing != "custom" {
		t.Fatal("invalid identifier renamed the server")
	}
	draft.SetValue(lspFieldID, ui.StringValue("ruby"))
	draft.SetValue(lspFieldCommand, ui.StringValue("ruby-lsp"))
	draft.SetValue(lspFieldExtensions, ui.StringValue(".rb, .rake"))
	draft.SetValue(lspFieldLanguageID, ui.StringValue("ruby"))
	result := root.lsp.save()
	if result.Action == nil || result.Action.ID != "settings.save" {
		t.Fatal("Save did not persist Settings")
	}
	ruby, ok := model.AppSettings().LSP.Languages["ruby"]
	if root.lsp.draft != nil || !ok || ruby.Command != "ruby-lsp" || !reflect.DeepEqual(ruby.Extensions, []string{".rb", ".rake"}) {
		t.Fatalf("custom server not applied: %+v open=%v", ruby, root.lsp.draft != nil)
	}
	if _, stale := model.AppSettings().LSP.Languages["custom"]; stale {
		t.Fatal("rename left the placeholder behind")
	}
}

func TestShellWordsRoundTrip(t *testing.T) {
	for _, words := range [][]string{{"--stdio"}, {"a b", `quote"d`, `back\slash`, ""}, nil} {
		got, err := splitShellWords(joinShellWords(words))
		if err != nil || !reflect.DeepEqual(got, words) {
			t.Fatalf("%#v -> %#v (%v)", words, got, err)
		}
	}
	if _, err := splitShellWords(`"open`); err == nil {
		t.Fatal("unterminated quote accepted")
	}
}
