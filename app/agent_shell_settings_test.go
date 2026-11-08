package app

import (
	"encoding/json"
	"testing"

	"crude/types"
)

// Settings › Agents › Command shell is stored once in the application config
// and reaches every session; Auto is stored as absent.
func TestAgentShellEditPersistsAndReachesSessions(t *testing.T) {
	current := FileConfig{}
	desired := FileConfigToAppSettings(current)
	desired.Agents.Shell = "git-bash"
	if err := applyPreferenceEdits(&current, desired, []types.SettingEdit{{Path: []string{"agents", "shell"}, After: json.RawMessage(`"git-bash"`)}}); err != nil {
		t.Fatal(err)
	}
	if got := FileConfigToAppSettings(current).Agents.Shell; got != "git-bash" {
		t.Fatalf("application settings shell = %q", got)
	}
	if got := FileConfigToSession(current).Agents.Shell; got != "git-bash" {
		t.Fatalf("session shell = %q", got)
	}
	raw, _ := json.Marshal(current)
	reloaded := FileConfig{}
	if err := json.Unmarshal(raw, &reloaded); err != nil || FileConfigToSession(reloaded).Agents.Shell != "git-bash" {
		t.Fatalf("reloaded shell = %+v, %v", reloaded.Agents, err)
	}
	desired.Agents.Shell = ""
	if err := applyPreferenceEdits(&current, desired, []types.SettingEdit{{Path: []string{"agents", "shell"}}}); err != nil {
		t.Fatal(err)
	}
	if got := FileConfigToSession(current).Agents.Shell; got != "" {
		t.Fatalf("Auto stored as %q; want absent", got)
	}
}
