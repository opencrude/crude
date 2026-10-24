package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/capability"
	"crude/types"
	captypes "crude/types/capability"
)

func assertSettingsRowMatchesSQL(t *testing.T, db *WorkspaceDB) {
	t.Helper()
	var stored string
	if err := db.DB.QueryRow(`SELECT value FROM workspace_metadata WHERE key = ?`, workspaceSettingsKey).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if got := db.GetSetting(workspaceSettingsKey); got != stored {
		t.Fatalf("committed projection %q differs from SQL %q", got, stored)
	}
}

func TestSettingsProjectionReadsAndPreparationBypassHeldDatabase(t *testing.T) {
	db := openTestWorkspaceDB(t)
	w := &Workspace{WorkspaceDB: db, Logger: testLogger{}}
	w.SetAppDefaults(types.AppSettings{})
	if err := db.SetSetting(workspaceSettingsKey, `{"editor":{"tab_size":7},"capabilities":{"skills":{"x":null}}}`); err != nil {
		t.Fatal(err)
	}
	service, err := capability.New(t.TempDir(), t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	w.Capabilities = service
	t.Cleanup(w.Capabilities.Close)
	db.DB.SetMaxOpenConns(1)
	connection, err := db.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	// Even an in-flight writer must not stop committed-state actor reads.
	db.settingsWriteMu.Lock()
	defer db.settingsWriteMu.Unlock()
	done := make(chan error, 1)
	go func() {
		if got := w.LoadAppSettings().Editor.TabSize; got != 7 {
			done <- fmt.Errorf("effective tab size = %d", got)
			return
		}
		_ = w.EffectiveAppSettings()
		_ = w.AppDefaults()
		_ = w.WorkspaceAgentDefaults()
		_ = w.PreparePreferencesSave(types.SettingsScopeWorkspace, nil)
		_ = w.PrepareCapabilityOverlayCommit(captypes.Action{})
		_, err := w.PrepareCapabilities(nil)
		overrides := w.LoadWorkspaceOverrides()
		overrides.Editor[0] = '!'
		if string(w.LoadWorkspaceOverrides().Editor) != `{"tab_size":7}` {
			err = fmt.Errorf("read projection aliases committed bytes")
		}
		if db.GetSettingContext(context.Background(), workspaceSettingsKey) == "" {
			err = fmt.Errorf("committed row unavailable")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("ordinary settings read/preparation waited on SQL or a settings writer")
	}
}

func TestSettingsProjectionStartupAndReplacementDB(t *testing.T) {
	db := openTestWorkspaceDB(t)
	const original = `{"editor":{"tab_size":9}}`
	if err := db.SetSetting(workspaceSettingsKey, original); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := openWorkspaceDBAt(db.Path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	w := &Workspace{WorkspaceDB: reopened, Logger: testLogger{}}
	if got := w.LoadAppSettings().Editor.TabSize; got != 9 {
		t.Fatalf("startup projection tab size = %d", got)
	}
	replacement := openTestWorkspaceDB(t)
	w.WorkspaceDB = replacement
	if !w.LoadWorkspaceOverrides().IsEmpty() {
		t.Fatal("replacement DB inherited the previous owner's projection")
	}
	if reopened.GetSetting(workspaceSettingsKey) != original {
		t.Fatal("replacement changed original owner")
	}
}

func TestSettingsProjectionConcurrentCommitsPreserveSparseNeighbors(t *testing.T) {
	db := openTestWorkspaceDB(t)
	root := t.TempDir()
	w := &Workspace{WorkspaceDB: db, RootPath: root, Logger: testLogger{}}
	w.SetAppDefaults(types.AppSettings{Capabilities: captypes.Settings{
		Sources:     map[string]captypes.SourceConfig{"local": {ID: "local", Kind: "local", Location: root, Trusted: true}},
		Acquisition: captypes.AcquisitionSettings{Mode: "automatic"},
	}})
	base := w.LoadAppSettings()
	store := newCapabilityRegistrationStore(w)
	const writers = 24
	var wg sync.WaitGroup
	var readers sync.WaitGroup
	stopReading := make(chan struct{})
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stopReading:
				return
			default:
				_ = w.LoadAppSettings()
				overrides := w.LoadWorkspaceOverrides()
				if len(overrides.Editor) != 0 {
					overrides.Editor[0] = '!'
				}
			}
		}
	}()
	errs := make(chan error, writers)
	for i := range writers {
		id := fmt.Sprintf("entry-%d", i)
		commit := w.PrepareCapabilityOverlayCommit(captypes.Action{})
		wg.Add(1)
		go func() {
			defer wg.Done()
			var err error
			switch i % 3 {
			case 0:
				_, err = saveWorkspacePreferences(db, base, []types.SettingEdit{{Path: []string{"editor", "tab_size"}, After: json.RawMessage(fmt.Sprint(i + 1))}})
			case 1:
				after := types.CloneAppSettings(base)
				after.Capabilities.Sources[id] = captypes.SourceConfig{ID: id, Location: root, Scope: captypes.ScopeWorkspace}
				_, err = commit(t.Context(), base, after, captypes.ScopeWorkspace)
			case 2:
				after := captypes.Clone(base.Capabilities)
				after.Skills[id] = captypes.SkillConfig{ID: id, Name: id, Path: filepath.Join(root, id), Source: "local", Managed: true, Scope: captypes.ScopeWorkspace}
				_, err = store.CommitRegistration(t.Context(), id, base.Capabilities, after)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(stopReading)
	readers.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	assertSettingsRowMatchesSQL(t, db)
	effective := w.LoadAppSettings()
	for i := range writers {
		id := fmt.Sprintf("entry-%d", i)
		if i%3 == 1 && effective.Capabilities.Sources[id].ID != id || i%3 == 2 && effective.Capabilities.Skills[id].ID != id {
			t.Fatalf("concurrent sparse commit lost %s", id)
		}
	}
	// A later addressed explicit value wins, even when equal to the parent.
	if _, err := saveWorkspacePreferences(db, base, []types.SettingEdit{{Path: []string{"editor", "tab_size"}, After: json.RawMessage("4")}}); err != nil {
		t.Fatal(err)
	}
	if got := string(w.LoadWorkspaceOverrides().Editor); got != `{"tab_size":4}` {
		t.Fatalf("last explicit parent-equal write = %s", got)
	}
	// Direct writes participate in the same commit/publication ordering.
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := db.SetSetting(workspaceSettingsKey, fmt.Sprintf(`{"editor":{"tab_size":%d}}`, i+1)); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	assertSettingsRowMatchesSQL(t, db)
}

func TestSettingsProjectionFailedWritesNeverPublish(t *testing.T) {
	db := openTestWorkspaceDB(t)
	w := &Workspace{WorkspaceDB: db, RootPath: t.TempDir(), Logger: testLogger{}}
	w.SetAppDefaults(types.AppSettings{Capabilities: captypes.Settings{
		Connections: map[string]captypes.ConnectionConfig{"custom": {ID: "custom", Transport: "http", URL: "https://example.test", Scope: captypes.ScopeWorkspace}},
	}})
	const original = `{"editor":{"tab_size":6}}`
	if err := db.SetSetting(workspaceSettingsKey, original); err != nil {
		t.Fatal(err)
	}
	_, err := db.DB.Exec(`CREATE TRIGGER reject_settings_update BEFORE UPDATE ON workspace_metadata WHEN NEW.key = 'workspace_settings' BEGIN SELECT RAISE(ABORT, 'test rejection'); END`)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.SetSetting(workspaceSettingsKey, "{}"); err == nil || !strings.Contains(err.Error(), "test rejection") {
		t.Fatalf("direct write did not reach rejecting storage: %v", err)
	}
	base := w.LoadAppSettings()
	if _, err := saveWorkspacePreferences(db, base, []types.SettingEdit{{Path: []string{"editor", "tab_size"}, After: json.RawMessage("2")}}); err == nil || !strings.Contains(err.Error(), "test rejection") {
		t.Fatalf("preference write did not reach rejecting storage: %v", err)
	}
	after := types.CloneAppSettings(base)
	after.Capabilities.Sources["x"] = captypes.SourceConfig{ID: "x", Location: w.RootPath, Scope: captypes.ScopeWorkspace}
	if _, err := w.PrepareCapabilityOverlayCommit(captypes.Action{})(t.Context(), base, after, captypes.ScopeWorkspace); err == nil || !strings.Contains(err.Error(), "test rejection") {
		t.Fatalf("capability overlay write did not reach rejecting storage: %v", err)
	}
	// Existing custom connections do not need acquisition source approval.
	after = types.CloneAppSettings(base)
	connection := after.Capabilities.Connections["custom"]
	connection.Grants = map[string]captypes.ToolGrant{"read": {Effect: captypes.EffectRead, Scope: "public", Fingerprint: "schema"}}
	after.Capabilities.Connections["custom"] = connection
	if _, err := newCapabilityRegistrationStore(w).CommitRegistration(t.Context(), "custom", base.Capabilities, after.Capabilities); err == nil || !strings.Contains(err.Error(), "test rejection") {
		t.Fatalf("registration write did not reach rejecting storage: %v", err)
	}
	if db.GetSetting(workspaceSettingsKey) != original {
		t.Fatal("failed write published uncommitted bytes")
	}
	assertSettingsRowMatchesSQL(t, db)
}
