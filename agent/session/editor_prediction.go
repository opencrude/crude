package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"crude/agent/provider"
	ttypes "crude/types"
)

// EditorPredictionCompleter is the bound connection for Errand's editor
// suggestions: the Suggestions settings over the Errand role, with thinking
// "none". It is built on the workspace owner and used off it. It never builds
// prompts or grants consent or budget.
//
// ConnectionIdentity names where source text goes (account, provider, URL,
// model, format and endpoint). Credentials and cosmetic labels are not part of
// it, so a rotated key keeps the workspace consent and a new provider does not.
type EditorPredictionCompleter struct {
	Model              string
	AccountID          string
	ProviderName       string
	Format             string
	EndpointPath       string
	MaxOutputTokens    int
	TimeoutSeconds     int
	ConnectionIdentity string

	provider *provider.Provider
	client   *provider.Client
	manager  *provider.AccountManager
	oauth    bool
}

// ErrSuggestionsUnconfigured explains a missing suggestion connection.
var ErrSuggestionsUnconfigured = errors.New("choose a model for Errand in Settings, Agents")

// SuggestionCompleter binds the current Suggestions settings. Errors are
// written for the user and contain no endpoint or credential.
func (c *Config) SuggestionCompleter() (EditorPredictionCompleter, error) {
	if c == nil {
		return EditorPredictionCompleter{}, ErrSuggestionsUnconfigured
	}
	settings := c.Agents.Suggestions.Normalized()
	profile, _ := activeProfileCfg(c)
	stored := profile.Categories[CategoryErrand]
	errand := c.GetCategoryConfig(CategoryErrand)
	if (stored.Enabled != nil && !*stored.Enabled) || (errand.Enabled != nil && !*errand.Enabled) {
		return EditorPredictionCompleter{}, errors.New("the Errand agent is turned off in Settings, Agents")
	}
	role := CategoryConfig{AccountID: errand.AccountID, Model: errand.Model, ServiceTier: errand.ServiceTier,
		ModelMaxOutput: errand.ModelMaxOutput, ModelContextWindow: errand.ModelContextWindow}
	if settings.AccountID != "" {
		role.AccountID, role.Model, role.ModelMaxOutput, role.ModelContextWindow = settings.AccountID, settings.Model, 0, 0
	} else if settings.Model != "" {
		role.Model = settings.Model
	}
	if settings.ServiceTier != "" {
		role.ServiceTier = settings.ServiceTier
	}
	if settings.Format == ttypes.SuggestionFormatChat {
		// Chat suggestions never think: a reasoning pass costs more time
		// than the user waits between keystrokes.
		role.ThinkingEffort = "none"
	} else {
		// A fill-in-the-middle request has no reasoning or tier controls;
		// the adapter refuses chat overrides, so bind none.
		role.ThinkingEffort, role.ServiceTier = "", ""
	}
	if strings.TrimSpace(role.Model) == "" || role.AccountID == "" {
		return EditorPredictionCompleter{}, ErrSuggestionsUnconfigured
	}
	account, err := c.resolveAccount(role.AccountID)
	if err != nil {
		return EditorPredictionCompleter{}, errors.New("the account for Errand suggestions is not available")
	}
	oauth := account.OAuth != nil || account.Connection.AuthType == provider.AuthChatGPT
	if oauth && settings.Format != ttypes.SuggestionFormatChat {
		return EditorPredictionCompleter{}, errors.New("fill-in-the-middle formats need an API key account; choose Chat")
	}
	if settings.Format != ttypes.SuggestionFormatChat && settings.EndpointPath == "" {
		return EditorPredictionCompleter{}, errors.New("enter the endpoint path for the fill-in-the-middle format")
	}
	accounts := map[string]provider.Account{account.ID: provider.CloneAccount(account)}
	manager := c.AccountManager
	if manager == nil {
		manager = provider.NewAccountManager(accounts, nil)
	}
	// Build detached, then bind the exact live manager for revocation.
	snapshot := &Config{Accounts: accounts}
	p, err := snapshot.providerForCategory(role)
	if err != nil {
		return EditorPredictionCompleter{}, errors.New("the connection for Errand suggestions is not valid")
	}
	if err := manager.Bind(p, account.ID); err != nil {
		return EditorPredictionCompleter{}, errors.New("the account for Errand suggestions is not available")
	}
	timeout := settings.TimeoutSeconds
	p.SetDialSettings(provider.DialSettings{MaxAttempts: 1, RequestTimeoutSeconds: timeout, ConnectTimeoutSeconds: timeout})
	client, err := p.Client(provider.WithResponseLimit(256 << 10))
	if err != nil {
		return EditorPredictionCompleter{}, errors.New("the connection for Errand suggestions is not valid")
	}
	identity, err := suggestionConnectionIdentity(account, role.Model, settings)
	if err != nil {
		return EditorPredictionCompleter{}, err
	}
	output := settings.MaxOutputTokens
	if role.ModelMaxOutput > 0 {
		output = min(output, role.ModelMaxOutput)
	}
	name := account.Connection.Name
	if name == "" {
		name = account.ProviderID
	}
	return EditorPredictionCompleter{
		Model: role.Model, AccountID: account.ID, ProviderName: name, Format: settings.Format,
		EndpointPath: settings.EndpointPath, MaxOutputTokens: output, TimeoutSeconds: timeout,
		ConnectionIdentity: identity, provider: p, client: client, manager: manager, oauth: oauth,
	}, nil
}

func suggestionConnectionIdentity(account provider.Account, model string, settings ttypes.SuggestionSettings) (string, error) {
	connection := provider.CloneTemplate(account.Connection)
	connection.Name, connection.HelpText = "", ""
	raw, err := json.Marshal(struct {
		AccountID  string            `json:"account_id"`
		ProviderID string            `json:"provider_id"`
		URL        string            `json:"url"`
		Standard   string            `json:"standard"`
		Headers    map[string]string `json:"headers"`
		Model      string            `json:"model"`
		Format     string            `json:"format"`
		Endpoint   string            `json:"endpoint"`
	}{account.ID, account.ProviderID, connection.ProviderURL, connection.APIStandard, connection.Headers,
		model, settings.Format, settings.EndpointPath})
	if err != nil {
		return "", errors.New("the connection for Errand suggestions is not valid")
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// Bound reports whether the completer holds a live connection.
func (t EditorPredictionCompleter) Bound() bool { return t.provider != nil && t.client != nil }

// Current checks without I/O that the bound account still exists with the
// same destination. Credential refresh belongs to the request path.
func (t EditorPredictionCompleter) Current() error {
	if !t.Bound() || t.manager == nil || t.ConnectionIdentity == "" {
		return errors.New("errand suggestions have no connection")
	}
	account, ok := t.manager.Account(t.AccountID)
	if !ok {
		return errors.New("the account for Errand suggestions was removed")
	}
	identity, err := suggestionConnectionIdentity(account, t.Model, ttypes.SuggestionSettings{Format: t.Format, EndpointPath: t.EndpointPath})
	if err != nil || identity != t.ConnectionIdentity {
		return errors.New("the connection for Errand suggestions changed")
	}
	return nil
}

func (t EditorPredictionCompleter) check(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("suggestion context is nil")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return t.Current()
}

// Stream sends one chat request with thinking "none" through the account's
// normal provider client, so every configured protocol works.
func (t EditorPredictionCompleter) Stream(ctx context.Context, system, input string) (*provider.ChatStream, error) {
	if err := t.check(ctx); err != nil {
		return nil, err
	}
	return t.client.Stream(ctx, &provider.ChatRequest{
		Model:           t.Model,
		MaxOutputTokens: t.MaxOutputTokens,
		Messages: []provider.ChatMessage{
			provider.NewTextMessage("system", system),
			provider.NewTextMessage("user", input),
		},
	})
}

// CompleteFIM sends one fill-in-the-middle request in the configured dialect
// and endpoint; there is no routing guess or fallback.
func (t EditorPredictionCompleter) CompleteFIM(ctx context.Context, prefix, suffix string, maxBytes int) (provider.FIMOnceResult, error) {
	if err := t.check(ctx); err != nil {
		return provider.FIMOnceResult{}, err
	}
	return t.provider.CompleteFIMOnce(ctx, provider.FIMOnceRequest{
		Dialect: provider.FIMDialect(t.Format), EndpointPath: t.EndpointPath, Model: t.Model,
		Prefix: prefix, Suffix: suffix, MaxOutputTokens: t.MaxOutputTokens, MaxOutputBytes: maxBytes,
	})
}
