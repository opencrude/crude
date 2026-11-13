package app

import (
	"encoding/json"
	"testing"

	"crude/types"
)

// Settings › Agents › Longest wait is stored once and reaches every session;
// Auto is stored as absent.
func TestAgentMaxWaitEditPersistsAndReachesSessions(t *testing.T) {
	current := FileConfig{}
	desired := FileConfigToAppSettings(current)
	desired.Agents.MaxWaitMinutes = 25
	if err := applyPreferenceEdits(&current, desired, []types.SettingEdit{{Path: []string{"agents", "max_wait_minutes"}, After: json.RawMessage(`25`)}}); err != nil {
		t.Fatal(err)
	}
	if got := FileConfigToAppSettings(current).Agents.MaxWaitMinutes; got != 25 {
		t.Fatalf("application settings wait bound = %d", got)
	}
	if got := FileConfigToSession(current).Agents.MaxWaitMinutes; got != 25 {
		t.Fatalf("session wait bound = %d", got)
	}
	raw, _ := json.Marshal(current)
	reloaded := FileConfig{}
	if err := json.Unmarshal(raw, &reloaded); err != nil || FileConfigToSession(reloaded).Agents.MaxWaitMinutes != 25 {
		t.Fatalf("reloaded wait bound = %+v, %v", reloaded.Agents, err)
	}
	desired.Agents.MaxWaitMinutes = 0
	if err := applyPreferenceEdits(&current, desired, []types.SettingEdit{{Path: []string{"agents", "max_wait_minutes"}}}); err != nil {
		t.Fatal(err)
	}
	if got := FileConfigToSession(current).Agents.MaxWaitMinutes; got != 0 {
		t.Fatalf("Auto stored as %d; want absent", got)
	}
}
