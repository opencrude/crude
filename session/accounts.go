package session

import (
	"errors"
	"fmt"

	"crude/provider"
	"crude/sessiondb"
)

// ErrAccountRequired marks a connection that needs explicit user selection or
// repair. Callers may offer account setup without parsing error text.
var ErrAccountRequired = errors.New("account required")

func (c *Config) account(id string) (provider.Account, bool) {
	if c == nil || id == "" {
		return provider.Account{}, false
	}
	if c.AccountManager != nil {
		return c.AccountManager.Account(id)
	}
	a, ok := c.Accounts[id]
	if !ok || a.ID != id {
		return provider.Account{}, false
	}
	return provider.CloneAccount(a), true
}

// resolveAccount returns only an explicitly selected account. Provider and
// template identity always come from that account, never the caller.
func (c *Config) resolveAccount(accountID string) (provider.Account, error) {
	account, ok := c.account(accountID)
	if !ok || account.ID != accountID {
		return provider.Account{}, fmt.Errorf("%w: account %q is unavailable", ErrAccountRequired, accountID)
	}
	if account.ProviderID == "" {
		return provider.Account{}, fmt.Errorf("%w: account %q has no provider attribution", ErrAccountRequired, accountID)
	}
	return account, nil
}

func (c *Config) connectionForAccountID(accountID string) (provider.Template, error) {
	account, err := c.resolveAccount(accountID)
	if err != nil {
		return provider.Template{}, err
	}
	return provider.CloneTemplate(account.Connection), nil
}

func (c *Config) providerIDForAccount(accountID string) string {
	account, ok := c.account(accountID)
	if !ok {
		return ""
	}
	return account.ProviderID
}

// bindAccount is the sole credential binding point for session-owned requests.
func (c *Config) bindAccount(p *provider.Provider, accountID string) error {
	account, err := c.resolveAccount(accountID)
	if err != nil {
		return err
	}
	if p == nil || p.ID != account.ProviderID {
		return fmt.Errorf("account %q provider attribution changed while configuring connection", accountID)
	}
	manager := c.AccountManager
	if manager == nil {
		manager = provider.NewAccountManager(c.Accounts, nil)
	}
	return manager.Bind(p, accountID)
}

// ConnectionProvider binds an account's connection with no model and no
// per-agent request settings; the caller owns the returned instance. It is for
// listing models and probing endpoints, never for serving an agent: every
// model-serving provider is built from a
// complete CategoryConfig (account, model, thinking effort, sampling, context
// window) so no request setting can be silently dropped.
func (c *Config) ConnectionProvider(accountID string) (*provider.Provider, error) {
	return c.providerForCategory(CategoryConfig{AccountID: accountID})
}

// pinAccounts captures effective role connections for a NEW session. A later
// default change only changes future sessions, not any of these IDs.
func (c *Config) pinAccounts() error {
	profile, name := activeProfileCfg(c)
	if profile.Categories == nil {
		profile.Categories = make(map[string]CategoryConfig)
	}
	if c.Agents.Profiles == nil {
		c.Agents.Profiles = make(map[string]ProfileConfig)
	}
	main := c.GetActiveDefaultCategory()
	account, err := c.resolveAccount(main.AccountID)
	if err != nil {
		return fmt.Errorf("main agent: %w", err)
	}
	main.AccountID = account.ID
	// Pin the workspace-backed main category without replacing its other settings.
	cat := profile.Categories[CategoryDefault]
	cat.AccountID = account.ID
	profile.Categories[CategoryDefault] = cat
	c.Agents.Profiles[name] = profile
	for role, cat := range profile.Categories {
		if role == CategoryDefault || cat.AccountID == "" {
			continue
		}
		selected, resolveErr := c.resolveAccount(cat.AccountID)
		if resolveErr != nil {
			return fmt.Errorf("%s account: %w", role, resolveErr)
		}
		cat.AccountID = selected.ID
		profile.Categories[role] = cat
	}
	c.Agents.Profiles[name] = profile
	return nil
}

// pinnedAccountOverrides records only connection pins; the remaining category
// fields keep their configured inheritance semantics in workspace settings.
func pinnedAccountOverrides(c *Config) map[string]sessiondb.AgentSettingOverride {
	profile, _ := activeProfileCfg(c)
	out := make(map[string]sessiondb.AgentSettingOverride)
	for role, cat := range profile.Categories {
		if cat.AccountID == "" {
			continue
		}
		out[role] = sessiondb.AgentSettingOverride{AccountID: cat.AccountID}
	}
	return out
}
