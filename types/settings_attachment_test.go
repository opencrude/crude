package types

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestAttachmentOpenDefaultsAndChoices(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{"", "system"}, {"system", "system"}, {"file-manager", "file-manager"},
		{"crude", "crude"}, {"unknown", "system"}, {"Crude", "system"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			ui := UISettings{AttachmentOpen: tc.input}
			if got := ui.AttachmentOpenOrDefault(); got != tc.want {
				t.Fatalf("destination = %q, want %q", got, tc.want)
			}
			app := AppSettings{UI: ui}
			ApplyAppDefaults(&app)
			if app.UI.AttachmentOpen != tc.want {
				t.Fatalf("normalized destination = %q, want %q", app.UI.AttachmentOpen, tc.want)
			}
		})
	}
	if AttachmentOpenSystem != "system" || AttachmentOpenFileManager != "file-manager" || AttachmentOpenCrude != "crude" {
		t.Fatal("attachment destination values changed")
	}
	data, err := json.Marshal(UISettings{})
	if err != nil || string(data) != "{}" {
		t.Fatalf("unset UI should stay sparse: %s (%v)", data, err)
	}
}

func TestAttachmentOpenApplicationSparseRoundTripAndReset(t *testing.T) {
	stored := json.RawMessage(`{"ui":{"sidebar_width":41,"show_icons":true},"editor":{"tab_size":8},"unknown":"keep"}`)
	for _, destination := range []string{"system", "file-manager", "crude"} {
		t.Run(destination, func(t *testing.T) {
			edit := SettingEdit{Path: []string{"ui", "attachment_open"}, After: json.RawMessage(`"` + destination + `"`)}
			if err := ValidateSettingEdits(AppSettings{}, []SettingEdit{edit}); err != nil {
				t.Fatal(err)
			}
			patched, err := PatchStoredSettings(stored, []SettingEdit{edit})
			if err != nil {
				t.Fatal(err)
			}
			var app AppSettings
			if err := DecodeStoredSettings(patched, &app); err != nil {
				t.Fatal(err)
			}
			ApplyAppDefaults(&app)
			if app.UI.AttachmentOpen != destination || app.UI.SidebarWidth != 41 || !app.UI.ShowIcons || app.Editor.TabSize != 8 {
				t.Fatalf("sparse application save changed neighbors: %+v", app)
			}
			reset, err := PatchStoredSettings(patched, []SettingEdit{{Path: []string{"ui", "attachment_open"}}})
			if err != nil {
				t.Fatal(err)
			}
			var before, after any
			if err := json.Unmarshal(stored, &before); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(reset, &after); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("reset changed untouched stored leaves: %s", reset)
			}
			var defaults AppSettings
			if err := DecodeStoredSettings(reset, &defaults); err != nil {
				t.Fatal(err)
			}
			ApplyAppDefaults(&defaults)
			if defaults.UI.AttachmentOpen != "system" {
				t.Fatal("application reset did not restore System app")
			}
		})
	}
}

func TestAttachmentOpenWorkspaceInheritanceExplicitDefaultAndReset(t *testing.T) {
	app := AppSettings{UI: UISettings{AttachmentOpen: "system", SidebarWidth: 30}}
	stored := WorkspaceOverrides{UI: json.RawMessage(`{"show_icons":true}`), Editor: json.RawMessage(`{"tab_size":8}`)}
	if got := MergeAppWorkspace(app, stored).UI; got.AttachmentOpen != "system" || !got.ShowIcons {
		t.Fatalf("untouched field did not inherit: %+v", got)
	}
	// An explicit parent-equal System app choice must remain a choice, not a diff.
	if err := ApplySettingsEdits(&stored, []SettingEdit{{Path: []string{"ui", "attachment_open"}, After: json.RawMessage(`"system"`)}}); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(stored)
	if err != nil {
		t.Fatal(err)
	}
	var reloaded WorkspaceOverrides
	if err := json.Unmarshal(encoded, &reloaded); err != nil {
		t.Fatal(err)
	}
	app.UI.AttachmentOpen, app.UI.SidebarWidth = "file-manager", 45
	if got := MergeAppWorkspace(app, reloaded).UI; got.AttachmentOpen != "system" || got.SidebarWidth != 45 || !got.ShowIcons {
		t.Fatalf("explicit default or live sibling inheritance lost: %+v", got)
	}
	if err := ApplySettingsEdits(&reloaded, []SettingEdit{{Path: []string{"ui", "attachment_open"}}}); err != nil {
		t.Fatal(err)
	}
	if got := MergeAppWorkspace(app, reloaded).UI; got.AttachmentOpen != "file-manager" || !got.ShowIcons {
		t.Fatalf("reset did not resume live inheritance: %+v", got)
	}
	if string(reloaded.UI) != `{"show_icons":true}` || string(reloaded.Editor) != `{"tab_size":8}` {
		t.Fatalf("reset changed workspace neighbors: %+v", reloaded)
	}
	app.UI.AttachmentOpen = "crude"
	if got := MergeAppWorkspace(app, reloaded).UI.AttachmentOpen; got != "crude" {
		t.Fatalf("reset pinned the old application choice: %q", got)
	}
}
