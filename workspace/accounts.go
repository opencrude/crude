package workspace

import (
	"context"
	"fmt"
	"reflect"

	"crude/provider"
	"crude/session"
	"crude/types"
)

// AllAccounts returns independent snapshots from the live credential owner.
// Settings must read these rather than an app snapshot containing old tokens.
func (w *Workspace) AllAccounts() map[string]provider.Account {
	if w.sessionCfg != nil {
		if w.sessionCfg.AccountManager != nil {
			return w.sessionCfg.AccountManager.Snapshot()
		}
		return cloneAccounts(w.sessionCfg.Accounts)
	}
	return cloneAccounts(w.appDefaults.Accounts)
}

// ActiveSessionConnectionIssue leaves a resumed transcript readable while the
// UI offers explicit repair of a missing or removed connection.
func (w *Workspace) ActiveSessionConnectionIssue() error {
	if w.ActiveSession == nil || w.ActiveSession.backend == nil {
		return nil
	}
	return w.ActiveSession.backend.ConnectionIssue()
}

func cloneAccounts(accounts map[string]provider.Account) map[string]provider.Account {
	if accounts == nil {
		return nil
	}
	out := make(map[string]provider.Account, len(accounts))
	for id, account := range accounts {
		out[id] = provider.CloneAccount(account)
	}
	return out
}

// PrepareAccountModels captures the connection on the UI owner thread; only
// the returned operation performs I/O. A settings save or tab switch cannot
// race a background read of sessionCfg's maps.
func (w *Workspace) PrepareAccountModels(templateID string, accountID, baseURL, standardID string) func() ([]types.ModelEntry, error) {
	cfg := w.sessionCfg
	if cfg == nil {
		return func() ([]types.ModelEntry, error) { return nil, fmt.Errorf("account configuration is unavailable") }
	}
	p, err := cfg.ProviderForAccount(accountID, "")
	if err != nil {
		return func() ([]types.ModelEntry, error) { return nil, err }
	}
	if templateID != "" && templateID != p.ID {
		return func() ([]types.ModelEntry, error) {
			return nil, fmt.Errorf("account %q does not belong to template %q", accountID, templateID)
		}
	}
	if baseURL != "" {
		p.BaseURL = baseURL
	}
	if standardID != "" {
		if err := p.SetProtocolID(standardID); err != nil {
			return func() ([]types.ModelEntry, error) { return nil, err }
		}
	}
	return accountModelOperation(p)
}

func accountModelOperation(p *provider.Provider) func() ([]types.ModelEntry, error) {
	return func() ([]types.ModelEntry, error) {
		client, err := p.Client()
		if err != nil {
			return nil, err
		}
		models, err := client.ListModels(context.Background())
		if err != nil {
			return nil, err
		}
		return types.ModelEntriesFromInfos(models), nil
	}
}

// PrepareDraftAccountModels lets a newly added/reconnected account discover
// models before Save without activating or persisting that draft. Unchanged
// saved accounts use their durable token manager, including refresh.
func (w *Workspace) PrepareDraftAccountModels(account provider.Account, baseURL, standardID string) func() ([]types.ModelEntry, error) {
	if account.ID == "" || account.ProviderID == "" {
		return func() ([]types.ModelEntry, error) { return nil, fmt.Errorf("select an account before fetching models") }
	}
	if w.sessionCfg != nil && w.sessionCfg.AccountManager != nil {
		if saved, ok := w.sessionCfg.AccountManager.Account(account.ID); ok && reflect.DeepEqual(saved, account) {
			return w.PrepareAccountModels(account.ProviderID, account.ID, baseURL, standardID)
		}
	}
	draftCfg := &session.Config{
		Accounts:  map[string]provider.Account{account.ID: provider.CloneAccount(account)},
		Providers: make(map[string]session.ProviderConfig),
	}
	if w.sessionCfg != nil {
		for id, template := range w.sessionCfg.Providers {
			draftCfg.Providers[id] = provider.CloneTemplate(template)
		}
	}
	p, err := draftCfg.ProviderForAccount(account.ID, "")
	if err != nil {
		return func() ([]types.ModelEntry, error) { return nil, err }
	}
	if baseURL != "" {
		p.BaseURL = baseURL
	}
	if standardID != "" {
		if err := p.SetProtocolID(standardID); err != nil {
			return func() ([]types.ModelEntry, error) { return nil, err }
		}
	}
	return accountModelOperation(p)
}
