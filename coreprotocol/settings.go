package coreprotocol

import (
	"crude/provider"
	"crude/types"
	captypes "crude/types/capability"
	"crude/types/jsondoc"
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
	// CodeIntelligence is the session's agent code-intelligence override;
	// null follows the workspace LSP switch.
	CodeIntelligence *bool `json:"code_intelligence"`
	// MaxSubAgents caps the session's running children; null follows the
	// workspace setting.
	MaxSubAgents *int  `json:"max_sub_agents"`
	TurnLogs     *bool `json:"turn_logs"`
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
	Edits     []types.SettingEdit `json:"edits"`
	SessionID string              `json:"session_id"`
	// Capability turns one installed skill/MCP server on or off for the
	// session and all its agents (SessionCapabilityToggle only).
	Capability *SessionCapabilityChange `json:"capability,omitempty"`
}

type SessionCapabilityChange struct {
	ID      string `json:"id"`
	Enabled bool   `json:"enabled"`
}

// Secret updates are write-only. Keep must be explicit; an empty Value means
// clear, never an implicit instruction to restore a server credential.
type SecretString struct {
	Keep  bool    `json:"keep"`
	Value *string `json:"value,omitempty"`
}
type SecretStrings struct {
	Keep bool `json:"keep"`
	// Value replaces the list when it has entries; empty means clear.
	Value []string `json:"value,omitempty"`
}
type SecretMap struct {
	Keep bool `json:"keep"`
	// Value replaces the map when it has entries; empty means clear.
	Value map[string]string `json:"value,omitempty"`
}
type SecretOAuth struct {
	Keep  bool                       `json:"keep"`
	Value *provider.OAuthCredentials `json:"value,omitempty"`
}
type SecretObject struct {
	Keep  bool            `json:"keep"`
	Value *jsondoc.Object `json:"value,omitempty"`
}

// Update exposes each secret update to one generic resolution rule.
func (u SecretString) Update() (bool, *string) { return u.Keep, u.Value }
func (u SecretStrings) Update() (bool, *[]string) {
	if len(u.Value) == 0 {
		return u.Keep, nil
	}
	return u.Keep, &u.Value
}
func (u SecretMap) Update() (bool, *map[string]string) {
	if len(u.Value) == 0 {
		return u.Keep, nil
	}
	return u.Keep, &u.Value
}
func (u SecretOAuth) Update() (bool, *provider.OAuthCredentials) { return u.Keep, u.Value }
func (u SecretObject) Update() (bool, *jsondoc.Object)           { return u.Keep, u.Value }

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
	ID         string            `json:"id"`
	ProviderID string            `json:"provider_id"`
	Label      string            `json:"label"`
	Connection provider.Template `json:"connection"`
	Endpoint   SecretString      `json:"endpoint"`
	APIKey     SecretString      `json:"api_key"`
	OAuth      SecretOAuth       `json:"oauth"`
	Headers    SecretMap         `json:"headers"`
	ExtraBody  SecretObject      `json:"extra_body"`
}
type AccountChangeRequest struct {
	ID    string        `json:"id"`
	After *AccountDraft `json:"after"`
}
type AccountModelsRequest struct {
	AccountID string        `json:"account_id"`
	Draft     *AccountDraft `json:"draft,omitempty"`
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
	SourceLocation    SecretString            `json:"source_location"`
}
type ConnectionSecretUpdates struct {
	Endpoint      SecretString  `json:"endpoint"`
	Args          SecretStrings `json:"args"`
	Environment   SecretMap     `json:"environment"`
	Headers       SecretMap     `json:"headers"`
	SecretRefs    SecretMap     `json:"secret_refs"`
	CredentialRef SecretString  `json:"credential_ref"`
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
