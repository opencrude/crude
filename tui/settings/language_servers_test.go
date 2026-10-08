package settings

import (
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"reflect"
	"strings"
	"testing"
)

func TestLSPFormPreservesAdvancedDisabledAndSpacedArguments(t *testing.T) {
	cfg := types.DefaultLSPSettings()
	cfg.Enabled = false
	cfg.MaxServers = 1
	cfg.ServerThreads = 3
	cfg.ServerMemoryMB = 512
	goCfg := cfg.Languages["go"]
	goCfg.Enabled = false
	goCfg.Command = ""
	goCfg.Environment = map[string]string{"GOMEMLIMIT": "500MiB"}
	goCfg.Settings = map[string]any{"buildFlags": []any{"-tags=some tag"}}
	cfg.Languages["go"] = goCfg
	cfg.Languages["python"] = types.LSPLanguageSettings{Enabled: true, Command: "basedpyright-langserver", Args: []string{"--stdio", "a path with spaces"}}
	cfg.Languages["ruby"] = types.LSPLanguageSettings{Enabled: true, Command: "ruby-lsp", LanguageID: "ruby", Extensions: []string{".rb"}, RootMarkers: []string{"Gemfile"}}
	cfg.Servers = map[string]string{".special": "external"}
	cfg.ServerArgs = map[string][]string{".special": {"a b"}}
	model := New(theme.DefaultDark(), types.AppSettings{LSP: cfg}, nil)
	got := model.AppSettings().LSP
	if got.Enabled || got.Languages["go"].Enabled || !reflect.DeepEqual(got.Servers, cfg.Servers) || !reflect.DeepEqual(got.ServerArgs, cfg.ServerArgs) || !reflect.DeepEqual(got.Languages["ruby"], cfg.Languages["ruby"]) {
		t.Fatalf("lost config: %+v", got)
	}
	if !reflect.DeepEqual(got.Languages["python"].Args, cfg.Languages["python"].Args) || !reflect.DeepEqual(got.Languages["go"].Settings, cfg.Languages["go"].Settings) || got.ServerThreads != 3 || got.ServerMemoryMB != 512 || got.MaxServers != 1 {
		t.Fatal("advanced/resource values lost")
	}
	if cfg.Languages["go"].Command != "" {
		t.Fatal("form mutated original")
	}
}

func TestLSPInvalidSaveThroughSettingsSurface(t *testing.T) {
	for _, tc := range []struct{ id, value string }{{"max-servers", "33"}, {"server-threads", "-1"}, {"server-memory", "garbage"}, {"lang-go-args", "--space separated"}, {"advanced", `{"languages":{"ruby":{"command":"ruby-lsp","extensions":[".go"],"language_id":"ruby"}}}`}, {"advanced", `{"languages":{"go":{"settings":[]}}}`}, {"advanced", `{"unrecognized":true}`}} {
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

func TestLSPResourceControlsPaintAndScopedSave(t *testing.T) {
	defaults := types.AppSettings{LSP: types.DefaultLSPSettings()}
	model := NewScoped(theme.DefaultDark(), defaults, nil, types.SettingsScopeWorkspace, defaults, types.WorkspaceOverrides{})
	model.SelectCategory(catLSP)
	root := NewSurface(theme.DefaultDark(), model)
	host := ui.NewHost(theme.DefaultDark(), root)
	host.Resize(160, 50)
	host.Layout()
	buf := surface.NewCellBuffer(160, 50)
	host.Paint(buf)
	for _, label := range []string{"Maximum servers", "Idle timeout", "Document limit", "Threads per server", "Memory guidance"} {
		if !strings.Contains(bufferPlain(buf), label) {
			t.Fatalf("missing painted %s", label)
		}
	}
	model.SetValueForTest(catLSP, "server-threads", "4")
	result := root.HandleAction(ui.EventContext{}, ui.Action{ID: "settings.save"})
	if result.Cmd == nil {
		t.Fatal("valid scoped LSP save blocked")
	}
	msg, ok := result.Cmd().(SaveMsg)
	if !ok || msg.Scope != types.SettingsScopeWorkspace || msg.AppSettings.LSP.ServerThreads != 4 {
		t.Fatalf("bad save %+v", msg)
	}
	if string(msg.Overrides.LSP) != `{"server_threads":4}` {
		t.Fatalf("unrelated LSP leaves overridden: %s", msg.Overrides.LSP)
	}
	if defaults.LSP.ServerThreads != 2 {
		t.Fatal("changed application defaults")
	}
}
