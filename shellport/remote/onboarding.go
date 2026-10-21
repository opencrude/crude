package remote

import (
	"context"

	"crude/coreprotocol"
	"crude/types"
)

// RefreshOnboarding reads the committed account and model configuration off the
// UI thread. Installation remains on Update, not in this request worker.
func (w *Workspace) RefreshOnboarding(ctx context.Context) (types.AppSettings, error) {
	var value coreprotocol.OnboardingResult
	err := w.call(ctx, coreprotocol.StartupOnboarding, struct{}{}, &value)
	if err != nil {
		return types.AppSettings{}, err
	}
	out := types.CloneAppSettings(value.Application)
	replica := settingsReplica{current: settingsSnapshot{fetch: coreprotocol.SettingsFetchResult{Accounts: value.Accounts}}}
	out.Accounts = replica.accounts()
	return out, nil
}

// ApplyOnboarding installs the explicit refresh on Update before a clean setup
// form is constructed. Stored markers remain write-only secret indicators.
func (w *Workspace) ApplyOnboarding(settings types.AppSettings) {
	w.settingsMu.Lock()
	defer w.settingsMu.Unlock()
	current := &w.settings.current.fetch
	accounts := make(map[string]coreprotocol.AccountView, len(settings.Accounts))
	for id, account := range settings.Accounts {
		view := current.Accounts[id]
		view.ID, view.ProviderID, view.Label = account.ID, account.ProviderID, account.Label
		view.Connection = account.Connection
		view.APIKeyPresent = account.APIKey != ""
		view.OAuthPresent = account.OAuth != nil
		view.HeadersPresent, view.ExtraBodyPresent = len(account.Connection.Headers) != 0, len(account.Connection.ExtraBody) != 0
		view.Connection.Headers, view.Connection.ExtraBody = nil, nil
		accounts[id] = view
	}
	settings.Accounts = nil
	current.Application, current.Accounts = settings, accounts
	current.Effective = types.MergeAppWorkspace(settings, current.Overrides)
}
