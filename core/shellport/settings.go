package shellport

import (
	"crude/agent/provider"
	"crude/types/setting"
	"crude/workspace"
)

// Settings loads, previews and persists application and workspace settings.
type Settings interface {
	// LoadAppSettings returns the effective settings for this workspace:
	// application defaults overlaid with workspace overrides.
	LoadAppSettings() setting.App
	// These passive reads use the same effective settings owner, without
	// materializing an entire settings form on the UI clock.
	AnalysisSettings() setting.WorkspaceAnalysis
	DesktopPresencePreferences() (awayMinutes int, notificationsActive bool)
	// AppDefaults returns the application-layer settings (no workspace overlay).
	AppDefaults() setting.App
	// LoadWorkspaceOverrides reads the sparse workspace overlay from workspace.db.
	LoadWorkspaceOverrides() setting.WorkspaceOverrides
	// WorkspaceAgentDefaults returns the effective defaults shown by the
	// persistent settings dialog.
	WorkspaceAgentDefaults() setting.Agents
	// SaveAccountChange writes one explicit account operation through the app's
	// atomic config owner. An unwired workspace must fail, never claim success.
	SaveAccountChange(change setting.AccountChange) error
	// AllAccounts returns independent snapshots from the live credential owner.
	// Settings must read these rather than an app snapshot containing old tokens.
	AllAccounts() map[string]provider.Account
	// PreparePreferencesSave captures the exact persistence owners before
	// a command runs. A project switch cannot redirect a late save into its
	// replacement DB. The returned work performs no live workspace/session
	// mutation.
	PreparePreferencesSave(scope setting.Scope, edits []setting.Edit) func() workspace.PreferencesSaveResult
	// ApplyPreferencesSave publishes a successful command snapshot, with no file
	// or database reads. The frontend rejects stale workspace/overlay generations
	// first.
	ApplyPreferencesSave(result workspace.PreferencesSaveResult)
	// PrepareAccountModels captures the connection on the UI owner thread; only
	// the returned operation performs I/O. A settings save or tab switch cannot
	// race a background read of sessionCfg's maps.
	PrepareAccountModels(templateID string, accountID, baseURL, standardID string) func() ([]setting.ModelEntry, error)
	// PrepareDraftAccountModels lets a newly added/reconnected account discover
	// models before Save without activating or persisting that draft. Unchanged
	// saved accounts use their durable token manager, including refresh.
	PrepareDraftAccountModels(account provider.Account, baseURL, standardID string) func() ([]setting.ModelEntry, error)
}
