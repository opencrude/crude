package coreprotocol

import (
	"crude/provider"
	"crude/types"
	captypes "crude/types/capability"
)

const (
	SettingsFetch                = "settings.fetch"
	SessionSettingsRead          = "settings.session.read"
	SessionSettingsSave          = "settings.session.save"
	SessionSettingsReset         = "settings.session.reset"
	SessionMultiAgentToggle      = "settings.session.toggle_multi_agent"
	SessionCapabilityToggle      = "settings.session.toggle_capability"
	AccountChange                = "settings.account.change"
	AccountModels                = "settings.account.models"
	CapabilityRuntime            = "settings.capability.runtime"
	CapabilitySnapshot           = "settings.capability.snapshot"
	CapabilityAction             = "settings.capability.action"
	CapabilityAcquisitionPreview = "settings.capability.acquisition_preview"
	SkillInput                   = "settings.capability.skill_input"
)

// SessionSettingsDTO explicitly carries the three fields omitted by Settings JSON.
// Value is a redacted projection, never an account credential container.
type SessionSettingsDTO struct {
	Value      types.Settings                    `json:"value"`
	MainAgent  string                            `json:"main_agent"`
	Overrides  map[string]types.CategorySettings `json:"overrides"`
	MultiAgent *bool                             `json:"multi_agent"`
	// DisabledCapabilities is the session-wide skill/MCP gate.
	DisabledCapabilities []string `json:"disabled_capabilities,omitempty"`
}

type SessionSettingsResult struct {
	Revision  uint64                            `json:"revision"`
	Effective SessionSettingsDTO                `json:"effective"`
	Base      SessionSettingsDTO                `json:"base"`
	Overrides map[string]types.CategorySettings `json:"overrides"`
}
type SessionSettingsRequest struct {
	SessionID string `json:"session_id"`
}
type SessionSettingsMutationRequest struct {
	SessionID        string             `json:"session_id"`
	ExpectedRevision uint64             `json:"expected_revision"`
	Settings         SessionSettingsDTO `json:"settings"`
	// Capability turns one installed skill/MCP server on or off for the
	// session and all its agents (SessionCapabilityToggle only).
	Capability *SessionCapabilityChange `json:"capability,omitempty"`
}

type SessionCapabilityChange struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// SecretUpdate is write-only. Keep must be explicit; empty Value means clear,
// never an implicit instruction to restore a server credential.
type SecretUpdate[T any] struct {
	Keep  bool `json:"keep"`
	Value *T   `json:"value,omitempty"`
}
type AccountView struct {
	ID               string            `json:"id"`
	ProviderID       string            `json:"provider_id"`
	Label            string            `json:"label"`
	Revision         string            `json:"revision"`
	Connection       provider.Template `json:"connection"`
	APIKeyPresent    bool              `json:"api_key_present"`
	OAuthPresent     bool              `json:"oauth_present"`
	HeadersPresent   bool              `json:"headers_present"`
	ExtraBodyPresent bool              `json:"extra_body_present"`
}
type AccountDraft struct {
	ID         string                                  `json:"id"`
	ProviderID string                                  `json:"provider_id"`
	Label      string                                  `json:"label"`
	Connection provider.Template                       `json:"connection"`
	Endpoint   SecretUpdate[string]                    `json:"endpoint"`
	APIKey     SecretUpdate[string]                    `json:"api_key"`
	OAuth      SecretUpdate[provider.OAuthCredentials] `json:"oauth"`
	Headers    SecretUpdate[map[string]string]         `json:"headers"`
	ExtraBody  SecretUpdate[map[string]any]            `json:"extra_body"`
}
type AccountChangeRequest struct {
	ID               string        `json:"id"`
	ExpectedRevision string        `json:"expected_revision"`
	After            *AccountDraft `json:"after"`
}
type AccountModelsRequest struct {
	AccountID        string        `json:"account_id"`
	ExpectedRevision string        `json:"expected_revision"`
	Draft            *AccountDraft `json:"draft,omitempty"`
}
type AccountModelsResult struct {
	Models []types.ModelEntry `json:"models"`
}
type SettingsFetchResult struct {
	ApplicationRevision      string                   `json:"application_revision"`
	WorkspaceRevision        string                   `json:"workspace_revision"`
	CapabilitiesRevision     string                   `json:"capabilities_revision"`
	Application              types.AppSettings        `json:"application"`
	Effective                types.AppSettings        `json:"effective"`
	Overrides                types.WorkspaceOverrides `json:"overrides"`
	Accounts                 map[string]AccountView   `json:"accounts"`
	ProxyCredentialsPresent  bool                     `json:"proxy_credentials_present"`
	ConnectionSecretsPresent map[string]bool          `json:"connection_secrets_present"`
	// ConnectionSecretFields reports exactly which write-only connection fields
	// hold a value, so shells can show and keep each one without receiving it.
	ConnectionSecretFields map[string]ConnectionSecretPresence `json:"connection_secret_fields,omitempty"`
	// TerminalShells are detected on the core's machine, which may be remote.
	TerminalShells []types.TerminalShell `json:"terminal_shells,omitempty"`
}

// ConnectionSecretPresence flags each redacted connection field.
type ConnectionSecretPresence struct {
	Args          bool `json:"args,omitempty"`
	Environment   bool `json:"environment,omitempty"`
	Headers       bool `json:"headers,omitempty"`
	SecretRefs    bool `json:"secret_refs,omitempty"`
	CredentialRef bool `json:"credential_ref,omitempty"`
}
type CapabilityActionRequest struct {
	Action            captypes.Action         `json:"action"`
	ConnectionSecrets ConnectionSecretUpdates `json:"connection_secrets"`
	SourceLocation    SecretUpdate[string]    `json:"source_location"`
	ExpectedRevision  string                  `json:"expected_revision"`
}
type ConnectionSecretUpdates struct {
	Endpoint      SecretUpdate[string]            `json:"endpoint"`
	Args          SecretUpdate[[]string]          `json:"args"`
	Environment   SecretUpdate[map[string]string] `json:"environment"`
	Headers       SecretUpdate[map[string]string] `json:"headers"`
	SecretRefs    SecretUpdate[map[string]string] `json:"secret_refs"`
	CredentialRef SecretUpdate[string]            `json:"credential_ref"`
}

type CapabilityResult struct {
	Revision string            `json:"revision"`
	Snapshot captypes.Snapshot `json:"snapshot"`
	Entries  []captypes.Entry  `json:"entries,omitempty"`
	Content  string            `json:"content,omitempty"`
}
type CapabilityPreviewResult struct {
	Preview captypes.AcquisitionPreview `json:"preview"`
}
type SkillInputRequest struct {
	AgentID   string `json:"agent_id"`
	ID        string `json:"id"`
	Arguments string `json:"arguments"`
}
type SkillInputResult struct {
	Input provider.UserInput `json:"input"`
}
