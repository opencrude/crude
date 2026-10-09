package provider

import (
	"context"
	"fmt"
	"sync"
)

// Account is an application-global connection and credential identity. It owns
// its complete request configuration; presets only seed new accounts and are
// never a live inheritance source. ProviderID is attribution, not a reference
// to another saved configuration. OAuth.AccountID is the remote identity.
type Account struct {
	ID         string            `json:"id"`
	ProviderID string            `json:"provider_id"`
	Label      string            `json:"label"`
	APIKey     string            `json:"api_key,omitempty"`
	OAuth      *OAuthCredentials `json:"oauth,omitempty"`
	Connection Template          `json:"connection"`
	// ObservationRevision is a client-only opaque preimage supplied by a core
	// projection. It is never a credential, routing input or persisted field.
	ObservationRevision string `json:"-"`
}

// NewAccountID produces an unpredictable stable identity for a new account.
func NewAccountID() (string, error) { return randomHex(16) }

func cloneAccount(a Account) Account {
	return CloneAccount(a)
}

type accountSlot struct {
	// Serializes refreshes for this account without blocking other accounts.
	refreshMu sync.Mutex
	account   Account // guarded by AccountManager.mu
}

// AccountManager owns the current account set, and coordinates refreshes across
// independently constructed providers and session config copies. The persist
// callback must durably store the supplied rotated account (and must not call
// back into this manager); an unsuccessful write never exposes the new token.
type AccountManager struct {
	mu       sync.RWMutex
	accounts map[string]*accountSlot
	persist  func(Account) error
}

func NewAccountManager(accounts map[string]Account, persist func(Account) error) *AccountManager {
	m := &AccountManager{persist: persist}
	m.Replace(accounts)
	return m
}

// Snapshot returns detached copies: modifying a credential in the result never
// changes the manager's authority.
func (m *AccountManager) Snapshot() map[string]Account {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Account, len(m.accounts))
	for id, slot := range m.accounts {
		out[id] = cloneAccount(slot.account)
	}
	return out
}

func (m *AccountManager) Account(id string) (Account, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	slot, ok := m.accounts[id]
	if !ok {
		return Account{}, false
	}
	return cloneAccount(slot.account), true
}

// Replace installs an application-config snapshot. A changed credential or
// removed account invalidates prior bindings and any in-flight refresh. Label
// edits preserve the binding and its refresh coordination.
func (m *AccountManager) Replace(accounts map[string]Account) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.replaceLocked(accounts)
}

// Commit serializes a durable account-store change with refresh persistence.
// The callback receives a detached live snapshot and must durably persist the
// returned account set before returning; it must not call this manager. Failed
// commits leave existing bindings and credentials untouched.
func (m *AccountManager) Commit(save func(map[string]Account) (map[string]Account, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := make(map[string]Account, len(m.accounts))
	for id, slot := range m.accounts {
		current[id] = cloneAccount(slot.account)
	}
	updated, err := save(current)
	if err != nil {
		return err
	}
	m.replaceLocked(updated)
	return nil
}

func (m *AccountManager) replaceLocked(accounts map[string]Account) {
	next := make(map[string]*accountSlot, len(accounts))
	for id, input := range accounts {
		if id == "" || input.ID != id || input.ProviderID == "" {
			continue
		}
		account := cloneAccount(input)
		if old := m.accounts[id]; old != nil && sameAccountCredentials(old.account, account) {
			old.account = account
			next[id] = old
		} else {
			next[id] = &accountSlot{account: account}
		}
	}
	m.accounts = next
}

func sameAccountCredentials(a, b Account) bool {
	if a.ProviderID != b.ProviderID || a.APIKey != b.APIKey || (a.OAuth == nil) != (b.OAuth == nil) || a.Connection.ProviderURL != b.Connection.ProviderURL || a.Connection.AuthType != b.Connection.AuthType || a.Connection.AuthHeader != b.Connection.AuthHeader || a.Connection.APIStandard != b.Connection.APIStandard {
		return false
	}
	return a.OAuth == nil || *a.OAuth == *b.OAuth
}

type accountBinding struct {
	manager *AccountManager
	id      string
	slot    *accountSlot
}

// Bind connects a provider instance to exactly one account. Replacing or
// removing that account revokes this binding; callers must explicitly rebind
// after reconnecting, never silently switch to another identity.
func (m *AccountManager) Bind(p *Provider, accountID string) error {
	if p == nil || accountID == "" {
		return fmt.Errorf("account: provider and account ID are required")
	}
	m.mu.RLock()
	slot, ok := m.accounts[accountID]
	if !ok {
		m.mu.RUnlock()
		return fmt.Errorf("account %q not found; reconnect or select an account", accountID)
	}
	if slot.account.ProviderID != p.ID {
		m.mu.RUnlock()
		return fmt.Errorf("account %q belongs to provider %q, not %q", accountID, slot.account.ProviderID, p.ID)
	}
	// Never lend credentials to a provider constructed for another endpoint or
	// authentication contract after the account connection changed.
	protocol, _ := canonicalProtocolID(slot.account.Connection.APIStandard)
	if slot.account.Connection.ProviderURL != "" && (p.BaseURL != slot.account.Connection.ProviderURL || p.authType != slot.account.Connection.AuthType || p.authHeader != slot.account.Connection.AuthHeader || p.ProtocolID() != protocol) {
		m.mu.RUnlock()
		return fmt.Errorf("account %q connection changed while binding; retry with the current connection", accountID)
	}
	p.authMu.Lock()
	p.binding = &accountBinding{manager: m, id: accountID, slot: slot}
	p.oauth = nil
	p.APIKey = ""
	p.AccountID = accountID
	p.authMu.Unlock()
	m.mu.RUnlock()
	return nil
}

func (b *accountBinding) snapshot() (Account, error) {
	m := b.manager
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.accounts[b.id] != b.slot {
		return Account{}, fmt.Errorf("account %q was removed or reconnected; reconnect this session", b.id)
	}
	return cloneAccount(b.slot.account), nil
}

func (b *accountBinding) auth(ctx context.Context) (AuthContext, error) {
	b.slot.refreshMu.Lock()
	defer b.slot.refreshMu.Unlock()
	account, err := b.snapshot()
	if err != nil {
		return AuthContext{}, err
	}
	if account.OAuth != nil && account.OAuth.RefreshToken != "" && account.OAuth.expiredSoon() {
		fresh, err := RefreshOpenAIToken(ctx, account.OAuth.RefreshToken)
		if err != nil {
			return AuthContext{}, err
		}
		rotated := cloneAccount(account)
		mergeOAuth(rotated.OAuth, fresh)
		m := b.manager
		m.mu.Lock()
		if m.accounts[b.id] != b.slot {
			m.mu.Unlock()
			return AuthContext{}, fmt.Errorf("account %q was removed or reconnected during refresh", b.id)
		}
		// Hold the commit lock over disk persistence, not the network request.
		// Replace cannot remove/reconnect this account between the durable
		// write and installation of the refreshed credentials.
		if m.persist == nil {
			m.mu.Unlock()
			return AuthContext{}, fmt.Errorf("account %q: no token persistence callback", b.id)
		}
		// Label edits can occur while refresh is in flight; preserve them.
		rotated.Label = b.slot.account.Label
		rotated.Connection = CloneTemplate(b.slot.account.Connection)
		if err := m.persist(cloneAccount(rotated)); err != nil {
			m.mu.Unlock()
			return AuthContext{}, fmt.Errorf("persist refreshed account %q: %w", b.id, err)
		}
		b.slot.account = rotated
		m.mu.Unlock()
		account = rotated
	}
	// Validate once more so a removal after refresh never lends old
	// credentials to a subsequent HTTP request.
	if _, err := b.snapshot(); err != nil {
		return AuthContext{}, err
	}
	return accountAuth(account), nil
}

func accountAuth(account Account) AuthContext {
	key := account.APIKey
	if account.OAuth != nil && account.OAuth.AccessToken != "" {
		key = account.OAuth.AccessToken
	}
	return AuthContext{APIKey: key, OAuth: account.OAuth}
}

func mergeOAuth(dst, fresh *OAuthCredentials) {
	dst.AccessToken = fresh.AccessToken
	if fresh.RefreshToken != "" {
		dst.RefreshToken = fresh.RefreshToken
	}
	if fresh.IDToken != "" {
		dst.IDToken = fresh.IDToken
	}
	if fresh.Email != "" {
		dst.Email = fresh.Email
	}
	dst.ExpiresAt = fresh.ExpiresAt
	if fresh.AccountID != "" {
		dst.AccountID = fresh.AccountID
	}
}
