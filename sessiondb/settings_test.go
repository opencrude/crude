package sessiondb

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestSessionFlagsRoundTripAndClear(t *testing.T) {
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

	on := true
	if err := SaveSessionFlags(db, "session-1", SessionFlags{MultiAgent: &on}); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSessionFlags(db, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MultiAgent == nil || !*got.MultiAgent {
		t.Fatalf("loaded flags = %#v, want multi-agent on", got)
	}

	if err := ClearSessionFlags(db, "session-1"); err != nil {
		t.Fatal(err)
	}
	got, err = LoadSessionFlags(db, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.MultiAgent != nil {
		t.Fatalf("flags after clear = %#v, want inherit", got)
	}
}

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

	compact := 2
	want := map[string]AgentSettingOverride{
		"default": {
			Provider: "openai", AccountID: "work", Model: "gpt-5",
			APIStandard: "openai-responses", ThinkingEffort: "high",
			ContextCompactionPercent: &compact,
		},
		"crafter": {Provider: "anthropic", AccountID: "personal", Model: "claude-sonnet-4", APIStandard: "anthropic"},
	}
	if err := SaveAgentSettingOverrides(db, "session-1", want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAgentSettingOverrides(db, "session-1")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
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
