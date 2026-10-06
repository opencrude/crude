package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	ttypes "crude/types"
	captypes "crude/types/capability"
)

// capabilityRegistrationStore publishes service-initiated workspace
// registrations through the normal sparse workspace settings authority. It
// captures the exact project owner so an acquisition finishing after Close or
// SwitchProject cannot write through a replacement WorkspaceDB.
type capabilityRegistrationStore struct {
	workspace *Workspace
	db        *WorkspaceDB
	root      string
	mu        sync.Mutex
}

func newCapabilityRegistrationStore(workspace *Workspace) *capabilityRegistrationStore {
	return &capabilityRegistrationStore{
		workspace: workspace,
		db:        workspace.WorkspaceDB,
		root:      workspace.RootPath,
	}
}

func (store *capabilityRegistrationStore) CommitRegistration(
	ctx context.Context,
	id string,
	before captypes.Settings,
	after captypes.Settings,
) (captypes.Settings, error) {
	if err := ctx.Err(); err != nil {
		return captypes.Settings{}, err
	}
	if store == nil || store.workspace == nil || store.db == nil || store.db.DB == nil {
		return captypes.Settings{}, errors.New("capability registration persistence is unavailable")
	}

	// Serialize read/compare/write within this owner. The workspace capability
	// lifetime lock separately fences Close and SwitchProject.
	if err := store.lockOwner(ctx); err != nil {
		return captypes.Settings{}, err
	}
	defer store.workspace.capabilityMu.RUnlock()
	store.mu.Lock()
	defer store.mu.Unlock()

	if store.workspace.WorkspaceDB != store.db || store.workspace.RootPath != store.root {
		return captypes.Settings{}, errors.New("capability registration workspace owner changed")
	}
	defaults := store.workspace.AppDefaults()
	before = captypes.Normalize(before)
	after = captypes.Normalize(after)

	kind, skill, connection, err := selectedRegistration(id, after)
	if err != nil {
		return captypes.Settings{}, err
	}
	change := captypes.RegistrationChange{ID: id, Kind: kind, Before: before, Skill: skill, Connection: connection}
	if change.RegistrationScope() == captypes.ScopeApplication {
		return store.commitApplication(ctx, change)
	}

	tx, err := store.db.DB.BeginTx(ctx, nil)
	if err != nil {
		return captypes.Settings{}, fmt.Errorf("begin capability registration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	overrides, err := loadWorkspaceOverridesTx(ctx, tx, defaults)
	if err != nil {
		return captypes.Settings{}, err
	}
	current := ttypes.MergeAppWorkspace(defaults, overrides)
	current.Capabilities = captypes.Normalize(current.Capabilities)
	updatedCapabilities, err := captypes.ApplyRegistration(current.Capabilities, change, current.Capabilities)
	if err != nil {
		return captypes.Settings{}, err
	}
	current.Capabilities = updatedCapabilities
	merged := captypes.Clone(current.Capabilities)
	updated := ttypes.OverridesFromDiff(defaults, current)
	encoded, err := json.Marshal(updated)
	if err != nil {
		return captypes.Settings{}, fmt.Errorf("encode capability registration settings: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO workspace_metadata (key, value, updated_at)
		VALUES (?, ?, unixepoch() * 1000000000)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, workspaceSettingsKey, string(encoded)); err != nil {
		return captypes.Settings{}, fmt.Errorf("persist capability registration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return captypes.Settings{}, fmt.Errorf("commit capability registration: %w", err)
	}
	return merged, nil
}

// commitApplication publishes one application-bound registration through the
// app config owner. Application installs are the product default and are not
// owned by this project, so the write is delegated to the app layer, which
// applies the same shared registration owner under its own config lock.
func (store *capabilityRegistrationStore) commitApplication(ctx context.Context, change captypes.RegistrationChange) (captypes.Settings, error) {
	if err := ctx.Err(); err != nil {
		return captypes.Settings{}, err
	}
	persist := store.workspace.OnAppCapabilityRegistration
	if persist == nil {
		return captypes.Settings{}, errors.New("application capability persistence is unavailable")
	}
	published, err := persist(change)
	if err != nil {
		return captypes.Settings{}, err
	}
	if _, exists := published.Skills[change.ID]; change.Kind == captypes.KindSkill && !exists {
		return captypes.Settings{}, errors.New("application capability registration was not published")
	}
	if _, exists := published.Connections[change.ID]; change.Kind == captypes.KindMCP && !exists {
		return captypes.Settings{}, errors.New("application capability registration was not published")
	}
	// The runtime is configured with the effective layer, so a workspace's own
	// overlay must not disappear just because this install was application-wide.
	effective := ttypes.MergeAppWorkspace(store.workspace.AppDefaults(), store.workspace.LoadWorkspaceOverrides())
	return captypes.Normalize(effective.Capabilities), nil
}

// Close holds the lifetime write lease while cancelling and joining sessions.
// A tool completing an acquisition must be able to abandon a pending read lease,
// otherwise teardown and that session's tool worker wait on each other.
func (store *capabilityRegistrationStore) lockOwner(ctx context.Context) error {
	if store.workspace.capabilityMu.TryRLock() {
		return nil
	}
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
			if store.workspace.capabilityMu.TryRLock() {
				return nil
			}
		}
	}
}

func selectedRegistration(id string, settings captypes.Settings) (captypes.Kind, captypes.SkillConfig, captypes.ConnectionConfig, error) {
	skill, hasSkill := settings.Skills[id]
	connection, hasConnection := settings.Connections[id]
	if id == "" || hasSkill == hasConnection {
		return "", captypes.SkillConfig{}, captypes.ConnectionConfig{}, errors.New("acquisition did not produce exactly one selected identity")
	}
	if hasSkill {
		if skill.ID != id {
			return "", captypes.SkillConfig{}, captypes.ConnectionConfig{}, errors.New("acquired skill identity does not match its selected ID")
		}
		return captypes.KindSkill, skill, captypes.ConnectionConfig{}, nil
	}
	if connection.ID != id {
		return "", captypes.SkillConfig{}, captypes.ConnectionConfig{}, errors.New("acquired connection identity does not match its selected ID")
	}
	return captypes.KindMCP, captypes.SkillConfig{}, connection, nil
}

func loadWorkspaceOverridesTx(ctx context.Context, tx *sql.Tx, defaults ttypes.AppSettings) (ttypes.WorkspaceOverrides, error) {
	raw, err := settingValueTx(ctx, tx, workspaceSettingsKey)
	if err != nil {
		return ttypes.WorkspaceOverrides{}, fmt.Errorf("load workspace capability settings: %w", err)
	}
	if raw != "" {
		var overrides ttypes.WorkspaceOverrides
		if err := json.Unmarshal([]byte(raw), &overrides); err != nil {
			return ttypes.WorkspaceOverrides{}, fmt.Errorf("decode workspace capability settings: %w", err)
		}
		effective := ttypes.MergeAppWorkspace(defaults, overrides)
		return ttypes.OverridesFromDiff(defaults, effective), nil
	}
	return ttypes.WorkspaceOverrides{}, nil
}

func settingValueTx(ctx context.Context, tx *sql.Tx, key string) (string, error) {
	var value string
	err := tx.QueryRowContext(ctx, `SELECT value FROM workspace_metadata WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return value, err
}
