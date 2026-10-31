package types

import (
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestInlayHintsDefaultAndScalarPreservation(t *testing.T) {
	field, ok := reflect.TypeFor[LSPSettings]().FieldByName("InlayHints")
	if !ok || field.Type.Kind() != reflect.Bool || field.Tag.Get("json") != "inlay_hints" {
		t.Fatalf("inlay hints must be an exact persisted Boolean leaf: %+v", field)
	}
	if DefaultLSPSettings().InlayHints {
		t.Fatal("inlay hints must be opt-in")
	}
	var fresh AppSettings
	ApplyAppDefaults(&fresh)
	if fresh.LSP.InlayHints || !fresh.LSP.Enabled {
		t.Fatal("fresh defaults must leave inlay hints off and existing services on")
	}
	onlyHints := AppSettings{LSP: LSPSettings{InlayHints: true}}
	if lspUnset(onlyHints.LSP) {
		t.Fatal("an explicit inlay preference is not an unset LSP configuration")
	}
	ApplyAppDefaults(&onlyHints)
	if !onlyHints.LSP.InlayHints || onlyHints.LSP.Enabled {
		t.Fatal("defaulting replaced the explicit configuration")
	}
	for _, enabled := range []bool{false, true} {
		cfg := DefaultLSPSettings()
		cfg.InlayHints = enabled
		if NormalizeLSP(cfg).InlayHints != enabled || CloneLSPSettings(cfg).InlayHints != enabled ||
			CloneAppSettings(AppSettings{LSP: cfg}).LSP.InlayHints != enabled {
			t.Fatalf("scalar preference lost while normalizing/cloning %v", enabled)
		}
	}
}

func TestInlayHintsSparseBooleanEdits(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(strconv.FormatBool(enabled), func(t *testing.T) {
			before := AppSettings{LSP: DefaultLSPSettings()}
			before.LSP.InlayHints = !enabled
			after := CloneAppSettings(before)
			after.LSP.InlayHints = enabled
			edits := SettingsEdits(before, after)
			if len(edits) != 1 || strings.Join(edits[0].Path, ".") != "lsp.inlay_hints" ||
				string(edits[0].After) != strconv.FormatBool(enabled) {
				t.Fatalf("toggle must patch only its exact boolean leaf: %+v", edits)
			}
			if err := ValidateSettingEdits(AppSettings{}, edits); err != nil {
				t.Fatal(err)
			}
			stored := json.RawMessage(`{"lsp":{"hover":false,"server_threads":3},"editor":{"tab_size":8},"unknown":"keep"}`)
			patched, err := PatchStoredSettings(stored, edits)
			if err != nil {
				t.Fatal(err)
			}
			// The application owner decodes sparse LSP JSON over its baseline.
			loaded := AppSettings{LSP: DefaultLSPSettings()}
			if err := DecodeStoredSettings(patched, &loaded); err != nil {
				t.Fatal(err)
			}
			ApplyAppDefaults(&loaded)
			if loaded.LSP.InlayHints != enabled || loaded.LSP.Hover || loaded.LSP.ServerThreads != 3 || loaded.Editor.TabSize != 8 {
				t.Fatalf("boolean or neighboring application settings lost: inlay=%v hover=%v threads=%d tab=%d",
					loaded.LSP.InlayHints, loaded.LSP.Hover, loaded.LSP.ServerThreads, loaded.Editor.TabSize)
			}
			reset, err := PatchStoredSettings(patched, []SettingEdit{{Path: []string{"lsp", "inlay_hints"}}})
			if err != nil {
				t.Fatal(err)
			}
			var want, got any
			if err := json.Unmarshal(stored, &want); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(reset, &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(want, got) {
				t.Fatalf("application leaf reset changed neighbors: %s", reset)
			}
		})
	}
	for _, value := range []string{`"true"`, `1`, `{}`} {
		edit := SettingEdit{Path: []string{"lsp", "inlay_hints"}, After: json.RawMessage(value)}
		if err := ValidateSettingEdits(AppSettings{}, []SettingEdit{edit}); err == nil {
			t.Fatalf("non-Boolean preference admitted: %s", value)
		}
	}
}

func TestInlayHintsExplicitFalseAndLiveWorkspaceReset(t *testing.T) {
	app := AppSettings{LSP: DefaultLSPSettings()}
	stored := WorkspaceOverrides{LSP: json.RawMessage(`{"hover":false}`), Editor: json.RawMessage(`{"tab_size":8}`)}
	// Store a parent-equal false as an explicit choice, not a value diff.
	if err := ApplySettingsEdits(&stored, []SettingEdit{{Path: []string{"lsp", "inlay_hints"}, After: json.RawMessage(`false`)}}); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded WorkspaceOverrides
	if err := json.Unmarshal(raw, &reloaded); err != nil {
		t.Fatal(err)
	}
	app.LSP.InlayHints = true
	app.LSP.ServerThreads = 4
	got := MergeAppWorkspace(app, reloaded)
	if got.LSP.InlayHints || got.LSP.Hover || got.LSP.ServerThreads != 4 || got.Editor.TabSize != 8 {
		t.Fatalf("explicit false or live siblings lost: %+v", got.LSP)
	}
	if err := ApplySettingsEdits(&reloaded, []SettingEdit{{Path: []string{"lsp", "inlay_hints"}}}); err != nil {
		t.Fatal(err)
	}
	if !MergeAppWorkspace(app, reloaded).LSP.InlayHints || string(reloaded.LSP) != `{"hover":false}` ||
		string(reloaded.Editor) != `{"tab_size":8}` {
		t.Fatalf("reset failed to inherit latest parent without changing neighbors: %+v", reloaded)
	}
	app.LSP.InlayHints = false
	if MergeAppWorkspace(app, reloaded).LSP.InlayHints {
		t.Fatal("reset pinned the previous parent value")
	}
}
