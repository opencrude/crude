package shellport

import (
	"crude/provider"
	"crude/types"
	"crude/workspace"
)

// Settings loads, previews and persists application and workspace settings.
type Settings interface {
	// BindToolLayout returns a layout store pinned to the current workspace database so work
	// queued before a workspace switch can never write into the replacement workspace.
	BindToolLayout() ToolLayoutStore
	// LoadAppSettings returns the effective settings for this workspace:
	// application defaults overlaid with workspace overrides.
	LoadAppSettings() types.AppSettings
	// AppDefaults returns the application-layer settings (no workspace overlay).
	AppDefaults() types.AppSettings
	// LoadWorkspaceOverrides reads the sparse workspace overlay from workspace.db.
	// Legacy `app_settings` rows are treated as overrides so existing projects
	// keep their per-workspace theme/editor/LSP values.
	// Stored overlays are re-diffed against the current application layer before
	LoadWorkspaceOverrides() types.WorkspaceOverrides
	// WorkspaceAgentDefaults returns the effective defaults shown by the
	// persistent settings dialog.
	WorkspaceAgentDefaults() types.AgentsSettings
	// SaveAppSettings persists application-layer settings to config.json via
	// OnAppSettingsSave. It does not write workspace.db — workspace overrides stay
	// on SaveWorkspaceSettings.
	SaveAppSettings(s types.AppSettings) error
	// SaveWorkspaceSettings persists sparse workspace overrides and reapplies the
	// effective layer to live sessions and the LSP manager.
	SaveWorkspaceSettings(overrides types.WorkspaceOverrides) error
	// SaveAccountChange writes one explicit account operation through the app's
	// atomic config owner. An unwired workspace must fail, never claim success.
	SaveAccountChange(change types.AccountChange) error
	// AllAccounts returns independent snapshots from the live credential owner.
	// Settings must read these rather than an app snapshot containing old tokens.
	AllAccounts() map[string]provider.Account
	// PreparePreferencesSave captures the exact persistence owners before
	// a command runs. A project switch cannot redirect a late save into its
	// replacement DB. The returned work performs no live workspace/session
	// mutation.
	PreparePreferencesSave(scope types.SettingsScope, settings types.AppSettings, overrides types.WorkspaceOverrides) func() workspace.PreferencesSaveResult
	// ApplyPreferencesSave publishes a successful command snapshot, with no file
	// or database reads. The frontend rejects stale workspace/overlay generations
	// first.
	ApplyPreferencesSave(result workspace.PreferencesSaveResult)
	// PrepareAccountModels captures the connection on the UI owner thread; only
	// the returned operation performs I/O. A settings save or tab switch cannot
	// race a background read of sessionCfg's maps.
	PrepareAccountModels(templateID string, accountID, baseURL, standardID string) func() ([]types.ModelEntry, error)
	// PrepareDraftAccountModels lets a newly added/reconnected account discover
	// models before Save without activating or persisting that draft. Unchanged
	// saved accounts use their durable token manager, including refresh.
	PrepareDraftAccountModels(account provider.Account, baseURL, standardID string) func() ([]types.ModelEntry, error)
}

// ToolLayoutStore persists rail and panel layout for one workspace database.
type ToolLayoutStore interface {
	LoadToolLayout() (workspace.ToolLayout, error)
	SaveToolLayout(layout workspace.ToolLayout) error
}
