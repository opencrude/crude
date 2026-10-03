package sessiondb

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCurrentSchemaReopenPreservesAgentSettings(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO sessions(id, project_path, project_name, created_at) VALUES ('existing', '/project', 'project', 100);
		INSERT INTO session_agent_settings(session_id, category, provider, account_id, model, thinking_effort, context_compaction_percent, inherit_source)
		VALUES ('existing', 'default', 'openai', 'work', 'existing-model', 'high', 65, 'workspace');
		INSERT INTO messages(role, origin, visibility, content, sequence_num, turn_number, created_at) VALUES ('user', 'human', 'visible', 'Original transcript', 1, 1, 101)`); err != nil {
		t.Fatal(err)
	}
	compact := 65
	want := map[string]AgentSettingOverride{"default": {Provider: "openai", AccountID: "work", Model: "existing-model", ThinkingEffort: "high", ContextCompactionPercent: &compact, Inherit: "workspace"}}
	for attempt := 0; attempt < 2; attempt++ {
		if err := Migrate(db); err != nil {
			t.Fatal(err)
		}
		got, err := LoadAgentSettingOverrides(db, "existing")
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("existing role pins unavailable or changed: %+v, %v", got, err)
		}
		var content string
		if err := db.QueryRow(`SELECT content FROM messages WHERE sequence_num=1`).Scan(&content); err != nil || content != "Original transcript" {
			t.Fatalf("migration changed transcript: %q, %v", content, err)
		}
	}
	want["default"] = AgentSettingOverride{AccountID: "work", SkillModes: map[string]string{"team:review": "manual"}}
	if err := SaveAgentSettingOverrides(db, "existing", want); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err != nil {
		t.Fatal(err)
	}
	got, err := LoadAgentSettingOverrides(db, "existing")
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("reopen erased capability selections: %+v, %v", got, err)
	}
}

func TestOutdatedCapabilitySelectionIsNotMigrated(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`ALTER TABLE session_agent_settings DROP COLUMN capability_selection`); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(db); err == nil || !strings.Contains(err.Error(), "align local data manually") {
		t.Fatalf("outdated session schema must require manual alignment: %v", err)
	}
	var columns int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('session_agent_settings') WHERE name='capability_selection'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatalf("session schema was silently upgraded: %d, %v", columns, err)
	}
}

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
			SkillModes:               map[string]string{"team:review": "manual"}, DeniedTools: []string{"mcp__fixture__write"},
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

func TestSessionSettingsSaveRollsBackPinsAndChoiceOnConnectionFailure(t *testing.T) {
	db, err := OpenAndMigrate(filepath.Join(t.TempDir(), "session.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO sessions (id, project_path, project_name, created_at) VALUES (?, ?, ?, ?)`, "session-1", "/tmp/project", "project", time.Now().UnixNano()); err != nil {
		t.Fatal(err)
	}
	first := map[string]AgentSettingOverride{"default": {AccountID: "first"}}
	if err := SaveSessionSettings(db, "session-1", "template", "first", first, SessionFlags{MainAgent: "researcher"}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TRIGGER reject_connection BEFORE UPDATE OF account_id ON sessions BEGIN SELECT RAISE(ABORT, 'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	if err := SaveSessionSettings(db, "session-1", "template", "second", map[string]AgentSettingOverride{"default": {AccountID: "second"}}, SessionFlags{MainAgent: "crafter"}); err == nil {
		t.Fatal("injected connection failure did not abort the save")
	}
	got, err := LoadAgentSettingOverrides(db, "session-1")
	if err != nil || !reflect.DeepEqual(got, first) {
		t.Fatalf("agent pins changed after failed commit: %+v, %v", got, err)
	}
	flags, err := LoadSessionFlags(db, "session-1")
	if err != nil || flags.MainAgent != "researcher" {
		t.Fatalf("root choice changed after failed commit: %+v, %v", flags, err)
	}
	var account string
	if err := db.QueryRow(`SELECT account_id FROM sessions WHERE id = ?`, "session-1").Scan(&account); err != nil || account != "first" {
		t.Fatalf("connection changed after failed commit: %q, %v", account, err)
	}
}
