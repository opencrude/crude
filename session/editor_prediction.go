package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"crude/provider"
)

// EditorPredictionCompleter binds Aeron on-owner for off-owner requests.
// It never rereads Config, builds prompts or grants consent/budget.
//
// ConnectionIdentity hashes private canonical account/credential/transport/role
// JSON, not request/source content; label/name/help cosmetics are ignored.
// A nil AccountManager gets a private snapshot manager, unable to observe later
// config edits. Live revocation requires a caller-owned shared manager.
type EditorPredictionCompleter struct {
	Model              string
	AccountID          string
	ConnectionIdentity string

	provider *provider.Provider
	manager  *provider.AccountManager
	role     CategoryConfig
	identity string
}

// AeronPredictionCompleter preserves role settings; unsupported settings cause
// explicit adapter errors, never repairs or fallback.
func (c *Config) AeronPredictionCompleter() (EditorPredictionCompleter, error) {
	if c == nil {
		return EditorPredictionCompleter{}, fmt.Errorf("model configuration is unavailable")
	}
	// Inheritance must not hide explicitly disabled Aeron.
	profile, _ := activeProfileCfg(c)
	stored := profile.Categories[CategoryAeron]
	role := c.GetCategoryConfig(CategoryAeron)
	if (stored.Enabled != nil && !*stored.Enabled) || (role.Enabled != nil && !*role.Enabled) {
		return EditorPredictionCompleter{}, fmt.Errorf("the Aeron agent is disabled in settings")
	}
	if strings.TrimSpace(role.Model) == "" {
		return EditorPredictionCompleter{}, fmt.Errorf("select a model for the Aeron agent")
	}
	// Detach all role pointers, slices and maps.
	raw, err := json.Marshal(role)
	if err != nil {
		return EditorPredictionCompleter{}, fmt.Errorf("invalid Aeron request settings")
	}
	var detached CategoryConfig
	if err := json.Unmarshal(raw, &detached); err != nil {
		return EditorPredictionCompleter{}, fmt.Errorf("invalid Aeron request settings")
	}
	role = detached
	account, err := c.resolveAccount(role.AccountID)
	if err != nil {
		return EditorPredictionCompleter{}, err
	}
	// Adapters refuse OAuth; Current relies on this memory-only auth invariant.
	if account.OAuth != nil || account.Connection.AuthType == provider.AuthChatGPT {
		return EditorPredictionCompleter{}, fmt.Errorf("editor prediction does not support OAuth accounts")
	}
	accounts := map[string]provider.Account{account.ID: provider.CloneAccount(account)}
	manager := c.AccountManager
	if manager == nil {
		manager = provider.NewAccountManager(accounts, nil)
	}
	// Avoid mixed live generations: build detached, then bind the exact manager.
	snapshot := &Config{Accounts: accounts}
	p, err := snapshot.providerForCategory(role)
	if err != nil {
		return EditorPredictionCompleter{}, err
	}
	if err := manager.Bind(p, account.ID); err != nil {
		return EditorPredictionCompleter{}, err
	}
	identity, err := predictionConnectionIdentity(account, role)
	if err != nil {
		return EditorPredictionCompleter{}, err
	}
	t := EditorPredictionCompleter{
		Model: role.Model, AccountID: account.ID, ConnectionIdentity: identity,
		provider: p, manager: manager, role: role, identity: identity,
	}
	if err := t.Current(); err != nil {
		return EditorPredictionCompleter{}, err
	}
	return t, nil
}

func predictionConnectionIdentity(account provider.Account, role CategoryConfig) (string, error) {
	account = provider.CloneAccount(account)
	account.Label, account.Connection.Name, account.Connection.HelpText = "", "", ""
	// encoding/json sorts keys; retain ALL transport settings, not only URL/auth.
	raw, err := json.Marshal(struct {
		AccountID  string                     `json:"account_id"`
		ProviderID string                     `json:"provider_id"`
		Connection provider.Template          `json:"connection"`
		APIKey     string                     `json:"api_key"`
		OAuth      *provider.OAuthCredentials `json:"oauth"`
		Model      string                     `json:"model"`
		Role       CategoryConfig             `json:"role_request_settings"`
	}{account.ID, account.ProviderID, account.Connection, account.APIKey, account.OAuth, role.Model, role})
	if err != nil {
		// Never expose private hash input.
		return "", fmt.Errorf("invalid editor prediction connection settings")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Current checks the live account without I/O. Role edits require actor-owned
// re-preparation and coordinator policy fingerprinting. This preflight is not
// atomic with concurrent transport-settings saves.
func (t EditorPredictionCompleter) Current() error {
	if t.provider == nil || t.manager == nil || t.identity == "" ||
		t.Model != t.role.Model || t.AccountID != t.role.AccountID || t.ConnectionIdentity != t.identity {
		return fmt.Errorf("editor prediction binding is unavailable or changed")
	}
	account, ok := t.manager.Account(t.AccountID)
	if !ok {
		return fmt.Errorf("editor prediction account is unavailable")
	}
	identity, err := predictionConnectionIdentity(account, t.role)
	if err != nil {
		return err
	}
	if identity != t.identity {
		return fmt.Errorf("editor prediction account connection changed; prepare a new binding")
	}
	// Detect identical remove/re-add via the original binding slot. EnsureAuth
	// cannot do I/O: this slot was non-OAuth, and adding OAuth revokes it before
	// auth. Never rebind this provider or allow OAuth without revisiting this.
	return t.provider.EnsureAuth(context.Background())
}

func (t EditorPredictionCompleter) checkRequest(ctx context.Context, model string, tokens int) error {
	if ctx == nil {
		return fmt.Errorf("editor prediction context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := t.Current(); err != nil {
		return err
	}
	if model != t.role.Model {
		return fmt.Errorf("editor prediction request model does not match its binding")
	}
	if t.role.ModelMaxOutput > 0 && tokens > t.role.ModelMaxOutput {
		return fmt.Errorf("editor prediction output limit exceeds the configured model limit")
	}
	return nil
}

// CompleteText preserves every adapter observation, including on error.
func (t EditorPredictionCompleter) CompleteText(ctx context.Context, request provider.TextOnceRequest) (provider.TextOnceResult, error) {
	if err := t.checkRequest(ctx, request.Model, request.MaxOutputTokens); err != nil {
		return provider.TextOnceResult{}, err
	}
	return t.provider.CompleteTextOnce(ctx, request)
}

// CompleteFIM requires explicit dialect/endpoint; no routing guesses or fallback.
func (t EditorPredictionCompleter) CompleteFIM(ctx context.Context, request provider.FIMOnceRequest) (provider.FIMOnceResult, error) {
	if err := t.checkRequest(ctx, request.Model, request.MaxOutputTokens); err != nil {
		return provider.FIMOnceResult{}, err
	}
	return t.provider.CompleteFIMOnce(ctx, request)
}
