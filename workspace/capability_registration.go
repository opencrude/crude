package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	if kind == captypes.KindSkill && skill.Scope != captypes.ScopeWorkspace ||
		kind == captypes.KindMCP && connection.Scope != captypes.ScopeWorkspace {
		return captypes.Settings{}, errors.New("service-initiated capability registration must target workspace scope")
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
	if err := validateRegistrationPreimage(id, kind, before, current.Capabilities, skill.Source, connection.Source); err != nil {
		return captypes.Settings{}, err
	}

	if kind == captypes.KindSkill {
		current.Capabilities.Skills[id] = skill
	} else {
		current.Capabilities.Connections[id] = connection
	}
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

func validateRegistrationPreimage(
	id string,
	kind captypes.Kind,
	before captypes.Settings,
	current captypes.Settings,
	skillSource string,
	connectionSource string,
) error {
	if !reflect.DeepEqual(current.Acquisition, before.Acquisition) {
		return errors.New("capability acquisition policy changed; request a fresh acquisition")
	}
	sourceID := connectionSource
	selectedExisted := false
	if kind == captypes.KindSkill {
		sourceID = skillSource
		beforeValue, beforeExists := before.Skills[id]
		currentValue, currentExists := current.Skills[id]
		if beforeExists != currentExists || !reflect.DeepEqual(beforeValue, currentValue) {
			return errors.New("selected capability registration changed; request a fresh acquisition")
		}
		selectedExisted = beforeExists
	} else {
		beforeValue, beforeExists := before.Connections[id]
		currentValue, currentExists := current.Connections[id]
		if beforeExists != currentExists || !reflect.DeepEqual(beforeValue, currentValue) {
			return errors.New("selected capability registration changed; request a fresh acquisition")
		}
		selectedExisted = beforeExists
	}
	// Authored/custom connections have no catalog source. Their already
	// registered identity is the preimage for a grant-only update; a brand-new
	// acquisition still must name the reviewed source that supplied it.
	if sourceID == "" {
		if selectedExisted {
			return nil
		}
		return errors.New("new capability registration has no reviewed source")
	}
	beforeSource, beforeExists := before.Sources[sourceID]
	currentSource, currentExists := current.Sources[sourceID]
	if beforeExists != currentExists || !reflect.DeepEqual(beforeSource, currentSource) {
		return errors.New("capability source changed; request a fresh acquisition")
	}
	return nil
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
