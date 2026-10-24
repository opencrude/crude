package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"crude/coreprotocol"
	"crude/provider"
	"crude/shellport"
	"crude/types"
	"crude/workspace"
)

// StoredSecret stands in for a credential the core holds but never sends.
// Forms show it masked; submitting it unchanged means "keep", so a shell can
// edit an account without ever receiving, or accidentally clearing, a secret.
const StoredSecret = "\u2063stored\u2063"

// storedKey marks a write-only map (headers, extra body) the core retains.
const storedKey = "\u2063stored\u2063"

// settingsSnapshot is the replicated, redacted settings state.
type settingsSnapshot struct {
	fetch    coreprotocol.SettingsFetchResult
	defaults types.AgentsSettings
}

type settingsReplica struct {
	w       *Workspace
	current settingsSnapshot
	ready   bool
}

func (s *settingsReplica) load(ctx context.Context) (settingsSnapshot, error) {
	var out settingsSnapshot
	if err := s.w.call(ctx, coreprotocol.SettingsFetch, &coreprotocol.Empty{}, &out.fetch); err != nil {
		return out, err
	}
	var overrides coreprotocol.OverridesResult
	if err := s.w.call(ctx, coreprotocol.WorkspaceOverrides, &coreprotocol.Empty{}, &overrides); err != nil {
		return out, err
	}
	out.defaults = overrides.AgentDefaults
	return out, nil
}

func (s *settingsReplica) install(snapshot settingsSnapshot) bool {
	s.w.settingsMu.Lock()
	defer s.w.settingsMu.Unlock()
	changed := !s.ready || !reflect.DeepEqual(s.current, snapshot)
	s.current, s.ready = snapshot, true
	return changed
}

// accounts restores replica accounts with stored-secret markers.
func (s *settingsReplica) accounts() map[string]provider.Account {
	out := make(map[string]provider.Account, len(s.current.fetch.Accounts))
	for id, view := range s.current.fetch.Accounts {
		account := provider.Account{ID: view.ID, ProviderID: view.ProviderID, Label: view.Label, Connection: provider.CloneTemplate(view.Connection)}
		if view.APIKeyPresent {
			account.APIKey = StoredSecret
		}
		if view.OAuthPresent {
			account.OAuth = &provider.OAuthCredentials{AccessToken: StoredSecret, RefreshToken: StoredSecret}
		}
		if view.HeadersPresent {
			account.Connection.Headers = map[string]string{storedKey: StoredSecret}
		}
		if view.ExtraBodyPresent {
			account.Connection.ExtraBody = map[string]any{storedKey: StoredSecret}
		}
		out[id] = account
	}
	return out
}

func (w *Workspace) AllAccounts() map[string]provider.Account {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	return w.settings.accounts()
}

func (w *Workspace) AppDefaults() types.AppSettings {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	value := types.MergeAppWorkspace(w.settings.current.fetch.Application, types.WorkspaceOverrides{})
	value.Accounts = w.settings.accounts()
	value.Capabilities = markStoredCapabilities(value.Capabilities, w.settings.current.fetch.ConnectionSecretFields)
	value.TerminalShells = slices.Clone(w.settings.current.fetch.TerminalShells)
	return value
}

func (w *Workspace) LoadAppSettings() types.AppSettings {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	value := types.MergeAppWorkspace(w.settings.current.fetch.Effective, types.WorkspaceOverrides{})
	value.Accounts = w.settings.accounts()
	value.Capabilities = markStoredCapabilities(value.Capabilities, w.settings.current.fetch.ConnectionSecretFields)
	value.TerminalShells = slices.Clone(w.settings.current.fetch.TerminalShells)
	return value
}

func (w *Workspace) LoadWorkspaceOverrides() types.WorkspaceOverrides {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	return w.settings.current.fetch.Overrides
}

func (w *Workspace) WorkspaceAgentDefaults() types.AgentsSettings {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	return types.MergeAppWorkspace(types.AppSettings{Agents: w.settings.current.defaults}, types.WorkspaceOverrides{}).Agents
}

func (w *Workspace) settingsBase() coreprotocol.SettingsFetchResult {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	return w.settings.current.fetch
}

func (w *Workspace) refreshSettings() { w.resetTopic(coreprotocol.TopicSettings) }

// draft converts an edited account into a write-only draft. Unchanged stored
// markers keep the core's credentials; anything else replaces or clears them.
func draft(after provider.Account, before coreprotocol.AccountView, exists bool) coreprotocol.AccountDraft {
	connection := provider.CloneTemplate(after.Connection)
	headers, extra := connection.Headers, connection.ExtraBody
	connection.Headers, connection.ExtraBody = nil, nil
	out := coreprotocol.AccountDraft{ID: after.ID, ProviderID: after.ProviderID, Label: after.Label, Connection: connection}
	if exists && after.Connection.ProviderURL == before.Connection.ProviderURL {
		out.Endpoint.Keep = true
	} else {
		url := after.Connection.ProviderURL
		out.Endpoint.Value = &url
	}
	if exists && after.APIKey == StoredSecret {
		out.APIKey.Keep = true
	} else if after.APIKey != "" {
		key := after.APIKey
		out.APIKey.Value = &key
	}
	switch {
	case after.OAuth == nil:
	case exists && after.OAuth.AccessToken == StoredSecret:
		out.OAuth.Keep = true
	default:
		oauth := *after.OAuth
		out.OAuth.Value = &oauth
	}
	switch {
	case exists && len(headers) == 1 && headers[storedKey] == StoredSecret:
		out.Headers.Keep = true
	case len(headers) != 0:
		out.Headers.Value = maps.Clone(headers)
	}
	switch {
	case exists && len(extra) == 1 && extra[storedKey] == StoredSecret:
		out.ExtraBody.Keep = true
	case len(extra) != 0:
		copy := maps.Clone(extra)
		out.ExtraBody.Value = &copy
	}
	return out
}

func (w *Workspace) SaveAccountChange(change types.AccountChange) error {
	before, exists := w.settingsBase().Accounts[change.ID]
	request := coreprotocol.AccountChangeRequest{ID: change.ID}
	if change.After != nil {
		value := draft(*change.After, before, exists)
		request.After = &value
	}
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	if _, err := w.rpc().ChangeAccount(ctx, request); err != nil {
		return err
	}
	w.refreshSettings()
	return nil
}

func (w *Workspace) PreparePreferencesSave(scope types.SettingsScope, edits []types.SettingEdit) func() workspace.PreferencesSaveResult {
	base := w.settingsBase()
	application := base.Application
	return func() workspace.PreferencesSaveResult {
		ctx, cancel := context.WithTimeout(w.ctx, callTimeout)
		defer cancel()
		result := workspace.PreferencesSaveResult{Scope: scope, AppSettings: application}
		if scope == types.SettingsScopeWorkspace {
			var out coreprotocol.WorkspacePreferencesResult
			if err := w.call(ctx, coreprotocol.WorkspaceOverridesSet, &coreprotocol.WorkspacePreferencesRequest{Edits: edits}, &out); err != nil {
				result.Err = err
				return result
			}
			result.Effective = out.Effective
			result.Overrides = out.Overrides
			if err := w.refreshTopicApplied(ctx, coreprotocol.TopicSettings); err != nil {
				result.Err = fmt.Errorf("settings saved, but their display could not be refreshed: %w", err)
			}
			return result
		}
		var out coreprotocol.ApplicationPreferencesResult
		proxy := coreprotocol.SecretString{Keep: true}
		for _, edit := range edits {
			if slices.Equal(edit.Path, []string{"network", "proxy_url"}) {
				var url string
				if json.Unmarshal(edit.After, &url) == nil {
					proxy = coreprotocol.SecretString{Value: &url}
				}
			}
		}
		err := w.call(ctx, coreprotocol.ApplicationPreferencesSave, &coreprotocol.ApplicationPreferencesRequest{Edits: edits, ProxyURL: proxy}, &out)
		if err != nil {
			result.Err = err
			return result
		}
		result.AppSettings = out.Value
		result.Effective = types.MergeAppWorkspace(out.Value, base.Overrides)
		if err := w.refreshTopicApplied(ctx, coreprotocol.TopicSettings); err != nil {
			result.Err = fmt.Errorf("settings saved, but their display could not be refreshed: %w", err)
		}
		return result
	}
}

// ApplyPreferencesSave does not install an unsequenced command result over a
// newer pushed settings value. The authoritative topic announces the save.
func (w *Workspace) ApplyPreferencesSave(workspace.PreferencesSaveResult) {}

func (w *Workspace) models(request coreprotocol.AccountModelsRequest) func() ([]types.ModelEntry, error) {
	rpc := w.rpc()
	return func() ([]types.ModelEntry, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
		defer cancel()
		result, err := rpc.QueryAccountModels(ctx, request)
		return result.Models, err
	}
}

func (w *Workspace) PrepareAccountModels(templateID string, accountID, baseURL, standardID string) func() ([]types.ModelEntry, error) {
	before, exists := w.settingsBase().Accounts[accountID]
	if !exists {
		return func() ([]types.ModelEntry, error) { return nil, errors.New("account is not configured") }
	}
	if templateID != "" && templateID != before.ProviderID {
		return func() ([]types.ModelEntry, error) {
			return nil, errors.New("account does not belong to the selected connection")
		}
	}
	request := coreprotocol.AccountModelsRequest{AccountID: accountID}
	if baseURL != "" || standardID != "" {
		account := w.AllAccounts()[accountID]
		if baseURL != "" {
			account.Connection.ProviderURL = baseURL
		}
		if standardID != "" {
			account.Connection.APIStandard = standardID
		}
		value := draft(account, before, true)
		request.Draft = &value
	}
	return w.models(request)
}

func (w *Workspace) PrepareDraftAccountModels(account provider.Account, baseURL, standardID string) func() ([]types.ModelEntry, error) {
	if account.ID == "" || account.ProviderID == "" {
		return func() ([]types.ModelEntry, error) { return nil, errors.New("select an account before fetching models") }
	}
	before, exists := w.settingsBase().Accounts[account.ID]
	if baseURL != "" {
		account.Connection.ProviderURL = baseURL
	}
	if standardID != "" {
		account.Connection.APIStandard = standardID
	}
	value := draft(account, before, exists)
	request := coreprotocol.AccountModelsRequest{AccountID: account.ID, Draft: &value}
	return w.models(request)
}

// BindToolLayout pins the layout store to this attachment's workspace.
func (w *Workspace) BindToolLayout() shellport.ToolLayoutStore { return toolLayout{w: w} }

type toolLayout struct{ w *Workspace }

func (t toolLayout) LoadToolLayout() (workspace.ToolLayout, error) {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	var out coreprotocol.ToolLayoutRequest
	err := t.w.call(ctx, coreprotocol.ToolLayoutLoad, &coreprotocol.Empty{}, &out)
	return out.Layout, err
}

func (t toolLayout) SaveToolLayout(layout workspace.ToolLayout) error {
	ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
	defer cancel()
	return t.w.call(ctx, coreprotocol.ToolLayoutSave, &coreprotocol.ToolLayoutRequest{Layout: layout}, &coreprotocol.BoolResult{})
}

func (w *Workspace) LoadToolLayout() (workspace.ToolLayout, error) {
	return toolLayout{w: w}.LoadToolLayout()
}
func (w *Workspace) SaveToolLayout(layout workspace.ToolLayout) error {
	return toolLayout{w: w}.SaveToolLayout(layout)
}
