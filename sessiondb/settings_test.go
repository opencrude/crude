package sessiondb

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAgentSettingOverridesRoundTripAndClear(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`
		INSERT INTO sessions (id, project_path, project_name, created_at)
		VALUES (?, ?, ?, ?)
	`, "session-1", "/tmp/project", "project", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}

	want := map[string]AgentSettingOverride{
		"default": {
			Provider: "openai", Model: "gpt-5",
			APIStandard: "openai-responses", ThinkingEffort: "high",
		},
		"crafter": {Provider: "anthropic", Model: "claude-sonnet-4", APIStandard: "anthropic"},
	}
	if err := SaveAgentSettingOverrides(db, "session-1", want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAgentSettingOverrides(db, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) || got["default"] != want["default"] || got["crafter"] != want["crafter"] {
		t.Fatalf("loaded overrides = %#v, want %#v", got, want)
	}

	if err := ClearAgentSettingOverrides(db, "session-1"); err != nil {
		t.Fatal(err)
	}
	got, err = LoadAgentSettingOverrides(db, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("overrides after clear = %#v, want empty", got)
	}
}
