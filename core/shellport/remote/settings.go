package remote

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"

	"crude/agent/provider"
	"crude/core/coreprotocol"
	"crude/types/setting"
	"crude/workspace"

	"golang.org/x/sync/errgroup"
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
	defaults setting.Agents
}

type settingsReplica struct {
	w       *Workspace
	current settingsSnapshot
	ready   bool
}

func (s *settingsReplica) load(ctx context.Context) (settingsSnapshot, error) {
	var out settingsSnapshot
	var overrides coreprotocol.OverridesResult
	// Independent reads: one round trip, not two.
	reads, readCtx := errgroup.WithContext(ctx)
	reads.Go(func() error {
		return s.w.call(readCtx, coreprotocol.SettingsFetch, &coreprotocol.Empty{}, &out.fetch)
	})
	reads.Go(func() error {
		return s.w.call(readCtx, coreprotocol.WorkspaceOverrides, &coreprotocol.Empty{}, &overrides)
	})
	if err := reads.Wait(); err != nil {
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

// AppDefaults returns the application settings without the workspace layer.
func (w *Workspace) AppDefaults() setting.App {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	value := setting.MergeAppWorkspace(w.settings.current.fetch.Application, setting.WorkspaceOverrides{})
	value.Accounts = w.settings.accounts()
	markStoredConnections(value.Capabilities, w.settings.current.fetch.ApplicationConnectionSecretFields)
	value.CompactionChecks = slices.Clone(w.settings.current.fetch.CompactionChecks)
	value.TerminalShells = setting.CloneTerminalShells(w.settings.current.fetch.TerminalShells)
	value.AgentShells = setting.CloneTerminalShells(w.settings.current.fetch.AgentShells)
	return value
}

// AnalysisSettings returns the effective workspace analysis settings, normalized.
func (w *Workspace) AnalysisSettings() setting.WorkspaceAnalysis {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	return setting.NormalizeWorkspaceAnalysis(w.settings.current.fetch.Effective.Analysis)
}

func (w *Workspace) DesktopPresencePreferences() (int, bool) {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	s := w.settings.current.fetch.Effective.UI.DesktopNotifications
	return s.AwayMinutesOrDefault(), s.Active()
}

// LoadAppSettings returns the effective application settings with defaults applied.
func (w *Workspace) LoadAppSettings() setting.App {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	// This is already resolved by the core's workspace settings owner. Resolving
	// it again as an application layer would erase workspace-only choices.
	value := setting.CloneApp(w.settings.current.fetch.Effective)
	value.Entry = w.settings.current.fetch.Entry
	// Entry describes this shell. The effective settings still belong to the
	// workspace: another shell may already use its language servers.
	value.Entry.FileOnly = w.FileOnly()
	setting.ApplyAppDefaults(&value)
	value.Accounts = w.settings.accounts()
	markStoredConnections(value.Capabilities, w.settings.current.fetch.ConnectionSecretFields)
	value.CompactionChecks = slices.Clone(w.settings.current.fetch.CompactionChecks)
	value.TerminalShells = setting.CloneTerminalShells(w.settings.current.fetch.TerminalShells)
	value.AgentShells = setting.CloneTerminalShells(w.settings.current.fetch.AgentShells)
	return value
}

// LoadWorkspaceOverrides returns the saved workspace overrides.
func (w *Workspace) LoadWorkspaceOverrides() setting.WorkspaceOverrides {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	return w.settings.current.fetch.Overrides
}

// WorkspaceAgentDefaults returns the agent defaults that the workspace uses.
func (w *Workspace) WorkspaceAgentDefaults() setting.Agents {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	return setting.MergeAppWorkspace(setting.App{Agents: w.settings.current.defaults}, setting.WorkspaceOverrides{}).Agents
}

func (w *Workspace) settingsBase() coreprotocol.SettingsFetchResult {
	w.settingsMu.RLock()
	defer w.settingsMu.RUnlock()
	return w.settings.current.fetch
}

func (w *Workspace) refreshSettings() { w.resetTopic(coreprotocol.TopicSettings) }

// RequestSettingsShellDiscovery asks the existing settings subscription for a
// fresh observation. This only signals the owned subscription; it performs no
// RPC on the caller and introduces no second replica writer.
func (w *Workspace) RequestSettingsShellDiscovery() { w.refreshSettings() }

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

// SaveAccountChange saves an account change and refreshes the settings.
func (w *Workspace) SaveAccountChange(change setting.AccountChange) error {
	before, exists := w.settingsBase().Accounts[change.ID]
	request := coreprotocol.AccountChangeRequest{ID: change.ID, RemovalPreview: change.RemovalPreview, ForgetCompaction: change.ForgetCompaction}
	if (change.After == nil || change.ForgetCompaction) && exists {
		request.BeforeRevision = before.Revision
	}
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

// PreparePreferencesSave returns a function that saves preference edits at the given scope.
func (w *Workspace) PreparePreferencesSave(scope setting.Scope, edits []setting.Edit) func() workspace.PreferencesSaveResult {
	base := w.settingsBase()
	application := base.Application
	return func() workspace.PreferencesSaveResult {
		ctx, cancel := context.WithTimeout(w.ctx, callTimeout)
		defer cancel()
		result := workspace.PreferencesSaveResult{Scope: scope, AppSettings: application}
		if scope == setting.ScopeWorkspace {
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
		result.Effective = setting.MergeAppWorkspace(out.Value, base.Overrides)
		if err := w.refreshTopicApplied(ctx, coreprotocol.TopicSettings); err != nil {
			result.Err = fmt.Errorf("settings saved, but their display could not be refreshed: %w", err)
		}
		return result
	}
}

// ApplyPreferencesSave does not install an unsequenced command result over a
// newer pushed settings value. The authoritative topic announces the save.
func (w *Workspace) ApplyPreferencesSave(workspace.PreferencesSaveResult) {}

func (w *Workspace) models(request coreprotocol.AccountModelsRequest) func() ([]setting.ModelEntry, error) {
	rpc := w.rpc()
	return func() ([]setting.ModelEntry, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 2*callTimeout)
		defer cancel()
		result, err := rpc.QueryAccountModels(ctx, request)
		return result.Models, err
	}
}

// PrepareAccountModels returns a function that fetches the models of a saved account.
func (w *Workspace) PrepareAccountModels(templateID string, accountID, baseURL, standardID string) func() ([]setting.ModelEntry, error) {
	before, exists := w.settingsBase().Accounts[accountID]
	if !exists {
		return func() ([]setting.ModelEntry, error) { return nil, errors.New("account is not configured") }
	}
	if templateID != "" && templateID != before.ProviderID {
		return func() ([]setting.ModelEntry, error) {
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

// PrepareDraftAccountModels returns a function that fetches models for an account draft.
func (w *Workspace) PrepareDraftAccountModels(account provider.Account, baseURL, standardID string) func() ([]setting.ModelEntry, error) {
	if account.ID == "" || account.ProviderID == "" {
		return func() ([]setting.ModelEntry, error) {
			return nil, errors.New("select an account before fetching models")
		}
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
