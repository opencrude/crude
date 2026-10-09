package core

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"

	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	captypes "crude/types/capability"
)

// A keyed entity digest is opaque even for low-entropy credentials. Revisions
// are process-local preconditions, not portable credential hashes.
var settingsRevisionKey = func() []byte {
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		panic(err)
	}
	return key
}()

func entityRevision(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	hash := hmac.New(sha256.New, settingsRevisionKey)
	_, _ = hash.Write(raw)
	return hex.EncodeToString(hash.Sum(nil))
}

func publicURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	u.User = nil
	// URLs may carry query/fragment tokens; they are not a public projection.
	u.RawQuery, u.Fragment = "", ""
	return u.String()
}

func publicCapabilities(value captypes.Settings) captypes.Settings {
	value = captypes.Clone(value)
	for id, conn := range value.Connections {
		conn.Environment, conn.Headers, conn.SecretRefs = nil, nil, nil
		conn.CredentialRef = ""
		conn.URL = publicURL(conn.URL)
		conn.RepositoryURL = publicURL(conn.RepositoryURL)
		conn.WebsiteURL = publicURL(conn.WebsiteURL)
		// Launch arguments can embed credentials, too.
		conn.Args = nil
		value.Connections[id] = conn
	}
	for id, source := range value.Sources {
		source.Location = publicURL(source.Location)
		value.Sources[id] = source
	}
	return value
}

func publicApp(value types.AppSettings) types.AppSettings {
	// Detach nested maps before any projection transformation.
	raw, _ := json.Marshal(value)
	var out types.AppSettings
	_ = json.Unmarshal(raw, &out)
	out.Accounts = nil
	out.Network.ProxyURL = publicURL(out.Network.ProxyURL)
	out.Capabilities = publicCapabilities(out.Capabilities)
	return out
}

func publicSession(value types.Settings) coreprotocol.SessionSettingsDTO {
	main, overrides, multi := value.SessionMainAgent, value.SessionOverrides, value.SessionMultiAgent
	raw, _ := json.Marshal(value)
	var out types.Settings
	_ = json.Unmarshal(raw, &out)
	out.APIKey, out.ProviderAPIKey = "", ""
	out.ProviderURL = publicURL(out.ProviderURL)
	out.Capabilities = publicCapabilities(out.Capabilities)
	// Category keys are excluded from JSON by the domain contract.
	raw, _ = json.Marshal(overrides)
	var detachedOverrides map[string]types.CategorySettings
	_ = json.Unmarshal(raw, &detachedOverrides)
	if multi != nil {
		copy := *multi
		multi = &copy
	}
	return coreprotocol.SessionSettingsDTO{Value: out, MainAgent: main, Overrides: detachedOverrides, MultiAgent: multi}
}

func publicSessionSet(value session.SettingsSnapshotSet) coreprotocol.SessionSettingsResult {
	return coreprotocol.SessionSettingsResult{Revision: value.Revision, Effective: publicSession(value.Effective), Base: publicSession(value.Base), Overrides: publicSession(types.Settings{SessionOverrides: value.Overrides}).Overrides}
}

func publicAccount(value provider.Account) coreprotocol.AccountView {
	connection := provider.CloneTemplate(value.Connection)
	connection.Headers, connection.ExtraBody = nil, nil
	connection.ProviderURL = publicURL(connection.ProviderURL)
	return coreprotocol.AccountView{ID: value.ID, ProviderID: value.ProviderID, Label: value.Label, Revision: entityRevision(value), Connection: connection, APIKeyPresent: value.APIKey != "", OAuthPresent: value.OAuth != nil, HeadersPresent: len(value.Connection.Headers) != 0, ExtraBodyPresent: len(value.Connection.ExtraBody) != 0}
}

func resolveSecret[T any](update coreprotocol.SecretUpdate[T], before T) (T, error) {
	var zero T
	if update.Keep {
		if update.Value != nil {
			return zero, errors.New("keep and replacement are mutually exclusive")
		}
		return before, nil
	}
	if update.Value != nil {
		return *update.Value, nil
	}
	return zero, nil
}

func resolveAccount(draft coreprotocol.AccountDraft, before provider.Account) (provider.Account, error) {
	out := provider.Account{ID: draft.ID, ProviderID: draft.ProviderID, Label: draft.Label, Connection: provider.CloneTemplate(draft.Connection)}
	if len(out.Connection.Headers) != 0 || len(out.Connection.ExtraBody) != 0 {
		return out, errors.New("use write-only header and extra-body updates")
	}
	var err error
	if out.Connection.ProviderURL, err = resolveSecret(draft.Endpoint, before.Connection.ProviderURL); err != nil {
		return out, err
	}
	if out.APIKey, err = resolveSecret(draft.APIKey, before.APIKey); err != nil {
		return out, err
	}
	var oldOAuth provider.OAuthCredentials
	if before.OAuth != nil {
		oldOAuth = *before.OAuth
	}
	if draft.OAuth.Keep {
		if draft.OAuth.Value != nil {
			return out, errors.New("keep and replacement are mutually exclusive")
		}
		if before.OAuth != nil {
			out.OAuth = &oldOAuth
		}
	} else {
		out.OAuth = draft.OAuth.Value
	}
	if out.Connection.Headers, err = resolveSecret(draft.Headers, before.Connection.Headers); err != nil {
		return out, err
	}
	if out.Connection.ExtraBody, err = resolveSecret(draft.ExtraBody, before.Connection.ExtraBody); err != nil {
		return out, err
	}
	return provider.CloneAccount(out), nil
}

// prepareSettings captures values/backend identity on the actor. Mutation paths
// lacking an atomic owner API fail explicitly rather than execute unsafe worker
// callbacks. The parent owns startJob/journal integration.
func (a *actor) prepareSettings(request coreprotocol.Request) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
	result := func(value any) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
		return func(context.Context) coreprotocol.Response { return response(request, value) }, nil, true
	}
	bad := func(code, message string) (func(context.Context) coreprotocol.Response, func(coreprotocol.Response), bool) {
		return func(context.Context) coreprotocol.Response { return fault(request, code, message) }, nil, true
	}
	switch request.Method {
	case coreprotocol.SettingsFetch:
		if err := decode(request.Payload, &struct{}{}); err != nil {
			return bad("invalid_request", err.Error())
		}
		app := a.ws.AppDefaults()
		overrides := a.ws.LoadWorkspaceOverrides()
		// Sparse raw JSON must be sanitized independently; never send the raw DB overlay.
		publicOverrides := overrides
		if len(overrides.Capabilities) != 0 {
			var caps captypes.Settings
			if err := json.Unmarshal(overrides.Capabilities, &caps); err != nil {
				return bad("settings_invalid", "invalid capability overlay")
			}
			publicOverrides.Capabilities, _ = json.Marshal(publicCapabilities(caps))
		}
		out := coreprotocol.SettingsFetchResult{ApplicationRevision: entityRevision(app), WorkspaceRevision: entityRevision(overrides), CapabilitiesRevision: entityRevision(types.MergeAppWorkspace(app, overrides).Capabilities), Application: publicApp(app), Effective: publicApp(types.MergeAppWorkspace(app, overrides)), Overrides: publicOverrides, Accounts: make(map[string]coreprotocol.AccountView), ConnectionSecretsPresent: make(map[string]bool)}
		if u, err := url.Parse(app.Network.ProxyURL); err == nil {
			out.ProxyCredentialsPresent = u.User != nil || u.RawQuery != ""
		}
		for id, account := range a.ws.AllAccounts() {
			out.Accounts[id] = publicAccount(account)
		}
		out.ConnectionSecretFields = make(map[string]coreprotocol.ConnectionSecretPresence)
		// Effective connections include workspace-bound registrations.
		for id, conn := range types.MergeAppWorkspace(app, overrides).Capabilities.Connections {
			out.ConnectionSecretsPresent[id] = len(conn.Environment)+len(conn.Headers)+len(conn.SecretRefs) != 0 || conn.CredentialRef != ""
			out.ConnectionSecretFields[id] = coreprotocol.ConnectionSecretPresence{Args: len(conn.Args) != 0, Environment: len(conn.Environment) != 0,
				Headers: len(conn.Headers) != 0, SecretRefs: len(conn.SecretRefs) != 0, CredentialRef: conn.CredentialRef != ""}
		}
		return result(out)
	case coreprotocol.SessionSettingsRead:
		var in coreprotocol.SessionSettingsRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		backend := a.ws.BackendSession(in.SessionID)
		if backend == nil {
			return bad("session_missing", "exact session is not open")
		}
		return result(publicSessionSet(backend.SettingsSnapshots()))
	case coreprotocol.AccountModels:
		var in coreprotocol.AccountModelsRequest
		if err := decode(request.Payload, &in); err != nil {
			return bad("invalid_request", err.Error())
		}
		before, exists := a.ws.AllAccounts()[in.AccountID]
		if in.ExpectedRevision != "" && (!exists || in.ExpectedRevision != entityRevision(before)) {
			return bad("stale_preimage", "account changed; fetch its current revision")
		}
		var operation func() ([]types.ModelEntry, error)
		if in.Draft == nil {
			if !exists {
				return bad("account_missing", "selected account is unavailable")
			}
			operation = a.ws.PrepareAccountModels("", in.AccountID, "", "")
		} else {
			if exists && in.ExpectedRevision == "" {
				return bad("stale_preimage", "draft queries require the exact saved account revision")
			}
			if in.Draft.ID != in.AccountID {
				return bad("invalid_request", "draft account identity differs from query owner")
			}
			draft, err := resolveAccount(*in.Draft, before)
			if err != nil {
				return bad("invalid_request", err.Error())
			}
			operation = a.ws.PrepareDraftAccountModels(draft, "", "")
		}
		return func(ctx context.Context) coreprotocol.Response {
			if ctx.Err() != nil {
				return fault(request, "cancelled", "model query cancelled")
			}
			models, err := operation()
			if err != nil {
				return fault(request, "models_failed", "account model discovery failed; repair the selected connection")
			}
			return response(request, coreprotocol.AccountModelsResult{Models: models})
		}, nil, true
	case coreprotocol.CapabilityRuntime:
		if err := decode(request.Payload, &struct{}{}); err != nil {
			return bad("invalid_request", err.Error())
		}
		service := a.ws.Capabilities
		return func(ctx context.Context) coreprotocol.Response {
			if ctx.Err() != nil {
				return fault(request, "cancelled", "capability observation cancelled")
			}
			var snapshot captypes.Snapshot
			if service != nil {
				snapshot = service.Snapshot()
			}
			return response(request, publicCapabilityResult(snapshot, nil, ""))
		}, nil, true
	case coreprotocol.SessionSettingsSave, coreprotocol.SessionSettingsReset, coreprotocol.SessionMultiAgentToggle:
		return a.prepareSessionSettings(request)
	case coreprotocol.AccountChange, coreprotocol.ApplicationPreferencesSave, coreprotocol.CapabilitySnapshot, coreprotocol.CapabilityAction, coreprotocol.CapabilityAcquisitionPreview, coreprotocol.SkillInput:
		return a.prepareApplicationSettings(request)
	}
	return nil, nil, false
}

func settingsMutation(method string) bool {
	switch method {
	case coreprotocol.SessionSettingsSave, coreprotocol.SessionSettingsReset, coreprotocol.SessionMultiAgentToggle, coreprotocol.AccountChange, coreprotocol.CapabilityAction, coreprotocol.SkillInput, coreprotocol.ApplicationPreferencesSave:
		return true
	}
	return false
}
