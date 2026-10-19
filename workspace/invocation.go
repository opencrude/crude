package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"

	"crude/provider"
	"crude/session"
	"crude/types"
)

// InvocationLifecycleOperation holds one captured selection, never workspace
// defaults. Only Run uses its private configuration and backend.
type InvocationLifecycleOperation struct {
	op        *SessionLifecycleOperation
	selection InvocationSelection
	once      sync.Once
	result    SessionLifecycleResult
}

// InvocationSelection is write-only lifecycle input, not a settings read model.
type InvocationSelection struct {
	AccountID    string `json:"account_id,omitempty"`
	Model        string `json:"model,omitempty"`
	ProviderID   string `json:"provider_id,omitempty"`
	APIKey       string `json:"api_key,omitempty"`
	Instructions string `json:"instructions,omitempty"`
}

func (s InvocationSelection) Empty() bool {
	return s.AccountID == "" && s.Model == "" && s.ProviderID == "" && s.APIKey == "" && s.Instructions == ""
}

func invocationConfig(base *session.Config, s InvocationSelection) (*session.Config, error) {
	if base == nil {
		return nil, errors.New("session configuration is unavailable")
	}
	if len(s.Instructions) > 64*1024 || len(s.APIKey) > 64*1024 || len(s.AccountID) > 256 || len(s.Model) > 1024 || len(s.ProviderID) > 256 {
		return nil, errors.New("invocation selection exceeds its input bounds")
	}
	copy := *base
	copy.AccountManager = nil
	raw, err := json.Marshal(copy)
	if err != nil {
		return nil, errors.New("cannot capture session configuration")
	}
	var cfg session.Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return nil, errors.New("cannot capture session configuration")
	}
	cfg.AccountManager = base.AccountManager
	if s.Empty() {
		return &cfg, nil
	}
	main := cfg.GetActiveDefaultCategory()
	if s.APIKey != "" && s.AccountID == "" {
		return nil, errors.New("--key requires an explicit --account")
	}
	id := s.AccountID
	if id == "" {
		id = main.AccountID
	}
	accounts := cfg.Accounts
	if cfg.AccountManager != nil {
		accounts = cfg.AccountManager.Snapshot()
	}
	a, ok := accounts[id]
	if !ok || a.ID != id {
		if s.AccountID == "" || s.APIKey == "" {
			return nil, errors.New("selected invocation account is unavailable; select an account in Accounts")
		}
		preset, known := provider.DefaultTemplates()[s.ProviderID]
		if !known {
			return nil, errors.New("a new invocation account requires --provider naming a known preset")
		}
		a = provider.Account{ID: id, Label: id, ProviderID: s.ProviderID, Connection: provider.CloneTemplate(preset)}
	}
	if s.ProviderID != "" && a.ProviderID != s.ProviderID {
		return nil, errors.New("selected account does not match --provider")
	}
	if err := a.Connection.Validate(); err != nil {
		return nil, errors.New("selected invocation account connection is invalid; repair it in Accounts")
	}
	if s.APIKey != "" {
		// Isolate overridden credentials from refresh/persistence and later global
		// account updates. Neither the key nor a key-derived ID is published.
		ephemeralID, err := provider.NewAccountID()
		if err != nil {
			return nil, errors.New("cannot allocate ephemeral account identity")
		}
		a.ID, a.APIKey, a.OAuth = "invocation-"+ephemeralID, s.APIKey, nil
		cfg.Accounts = provider.CloneAccounts(accounts)
		if cfg.Accounts == nil {
			cfg.Accounts = make(map[string]provider.Account)
		}
		cfg.Accounts[a.ID] = a
		cfg.AccountManager = provider.NewAccountManager(cfg.Accounts, nil)
	}
	if a.Connection.AuthType != provider.AuthNone && a.APIKey == "" && (a.OAuth == nil || a.OAuth.AccessToken == "" && a.OAuth.RefreshToken == "") {
		return nil, errors.New("selected invocation account has no credentials; configure Accounts or pass --key with --account")
	}
	main.AccountID, main.Inherit = a.ID, ""
	if s.Model != "" {
		main.Model = s.Model
	}
	if s.Instructions != "" {
		if main.Instructions != "" {
			main.Instructions += "\n\n"
		}
		main.Instructions += s.Instructions
	}
	name := cfg.Agents.ActiveProfile
	if name == "" {
		name = "default"
		cfg.Agents.ActiveProfile = name
	}
	prof := cfg.Agents.Profiles[name]
	if prof.Categories == nil {
		prof.Categories = make(map[string]session.CategoryConfig)
	}
	prof.Categories[session.CategoryDefault] = main
	if cfg.Agents.Profiles == nil {
		cfg.Agents.Profiles = make(map[string]session.ProfileConfig)
	}
	cfg.Agents.Profiles[name] = prof
	return &cfg, nil
}

func (w *Workspace) PrepareInvocationSession(id string, selection InvocationSelection) (*InvocationLifecycleOperation, error) {
	if !selection.Empty() && w.SessionsByID[id] != nil {
		return nil, errors.New("invocation selectors cannot change an already-live session; open without selectors or create a new session")
	}
	if w.sender == nil {
		return nil, errors.New("session event transport is unavailable")
	}
	preparedSelection := selection
	if id != "" && selection.AccountID == "" {
		// The retained account, not the workspace default, owns provider
		// validation on cold resume. Validate it after loading the snapshot.
		preparedSelection.ProviderID = ""
	}
	cfg, err := invocationConfig(w.sessionCfg, preparedSelection)
	if err != nil {
		return nil, err
	}
	op, err := w.prepareSessionLifecycle(id, cfg, w.homeDir, true)
	if err != nil {
		return nil, err
	}
	return &InvocationLifecycleOperation{op: op, selection: selection}, nil
}

func (op *InvocationLifecycleOperation) Run(ctx context.Context) SessionLifecycleResult {
	op.once.Do(func() { op.result = op.run(ctx) })
	return op.result
}

func (op *InvocationLifecycleOperation) run(ctx context.Context) SessionLifecycleResult {
	if op.op.sessionID == "" || op.selection.Empty() {
		result := op.op.Run(ctx)
		if result.Err == nil && result.owner != nil && op.op.sessionID == "" && (op.selection.AccountID != "" || op.selection.Model != "" || op.selection.APIKey != "") {
			backend := result.owner.backend
			settings := backend.SettingsSnapshot()
			main := op.op.cfg.GetActiveDefaultCategory()
			override := types.CategorySettings{}
			if op.selection.AccountID != "" || op.selection.APIKey != "" {
				override.AccountID = main.AccountID
			}
			if op.selection.Model != "" {
				override.Model = main.Model
			}
			settings.SessionOverrides = map[string]types.CategorySettings{session.CategoryDefault: override}
			_, result.Err = backend.SaveSettingsSnapshot(settings)
		}
		return result
	}
	// Resume first reads retained settings. Apply this explicit selection before
	// creating the orchestrator, never to an already-live backend.
	op.op.start = false
	result := op.op.Run(ctx)
	if result.Err != nil {
		return result
	}
	backend := result.owner.backend
	snapshot := backend.SettingsSnapshots()
	settings := snapshot.Effective
	settings.SessionOverrides = snapshot.Overrides
	main := op.op.cfg.GetActiveDefaultCategory()
	cat := settings.SessionOverrides[session.CategoryDefault]
	// Preserve the resumed root's exact model/account when only instructions or
	// provider validation were requested; workspace defaults are not resume truth.
	if op.selection.AccountID != "" || op.selection.APIKey != "" {
		cat.AccountID = main.AccountID
	}
	if op.selection.Model != "" {
		cat.Model = main.Model
	}
	account := cat.AccountID
	if account == "" {
		account = settings.AccountID
	}
	validation := InvocationSelection{AccountID: account, ProviderID: op.selection.ProviderID}
	if _, err := invocationConfig(op.op.cfg, validation); err != nil {
		backend.Close()
		result.Err = fmt.Errorf("invocation resume failed: %w", err)
		return result
	}
	cat.Inherit = ""
	if settings.SessionOverrides == nil {
		settings.SessionOverrides = make(map[string]types.CategorySettings)
	}
	settings.SessionMainAgent = ""
	settings.SessionOverrides[session.CategoryDefault] = cat
	_, result.Err = backend.SaveSettingsSnapshot(settings)
	if result.Err == nil {
		result.gate = &lifecycleEventGate{sender: op.op.sender}
		orch, err := backend.NewOrchestrator(result.gate)
		result.Err = err
		if err == nil {
			result.agentID = orch.ID
		}
	}
	if result.Err != nil {
		backend.Close()
		result.Err = fmt.Errorf("invocation resume failed: %w", result.Err)
		return result
	}
	if err := ctx.Err(); err != nil {
		backend.Close()
		result.Err = err
		return result
	}
	result.owner.installSettings(backend.SettingsSnapshots())
	reader := &Workspace{Logger: op.op.logger}
	result.views = reader.prepareSessionHydration(result.owner)
	children := backend.CompletedSubAgentSnapshots()
	children = append(children, backend.PendingSubAgentSnapshots()...)
	result.children = prepareSubAgentViews(children)
	return result
}
