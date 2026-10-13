package capability

import (
	"context"
	"crude/types/control"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

type Kind string

const (
	KindSkill Kind = "skill"
	KindMCP   Kind = "mcp"
)

type Scope string

const (
	ScopeApplication Scope = "application"
	ScopeWorkspace   Scope = "workspace"
)

type Effect string

const (
	EffectRead          Effect = "read"
	EffectWrite         Effect = "write"
	EffectDelete        Effect = "delete"
	EffectExecute       Effect = "execute"
	EffectCommunication Effect = "communication"
)

type Settings struct {
	Skills      map[string]SkillConfig      `json:"skills,omitempty"`
	Connections map[string]ConnectionConfig `json:"connections,omitempty"`
	Sources     map[string]SourceConfig     `json:"sources,omitempty"`
	Acquisition AcquisitionSettings         `json:"acquisition,omitempty"`
}

type SkillConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Path     string `json:"path"`
	Source   string `json:"source,omitempty"`
	Version  string `json:"version,omitempty"`
	Digest   string `json:"digest,omitempty"`
	Mode     string `json:"mode,omitempty"`
	Managed  bool   `json:"managed,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Scope    Scope  `json:"scope,omitempty"`
}

type ToolGrant struct {
	Effect      Effect `json:"effect"`
	Scope       string `json:"scope"`
	Fingerprint string `json:"fingerprint"`
	Disabled    bool   `json:"disabled,omitempty"`
}

type ConnectionConfig struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Host           string               `json:"host,omitempty"`
	Transport      string               `json:"transport"`
	URL            string               `json:"url,omitempty"`
	Command        string               `json:"command,omitempty"`
	Args           []string             `json:"args,omitempty"`
	Directory      string               `json:"directory,omitempty"`
	Environment    map[string]string    `json:"environment,omitempty"`
	Headers        map[string]string    `json:"headers,omitempty"`
	SecretRefs     map[string]string    `json:"secret_refs,omitempty"`
	Auth           string               `json:"auth,omitempty"`
	CredentialRef  string               `json:"credential_ref,omitempty"`
	TimeoutSeconds int                  `json:"timeout_seconds,omitempty"`
	Disabled       bool                 `json:"disabled,omitempty"`
	Scope          Scope                `json:"scope,omitempty"`
	Source         string               `json:"source,omitempty"`
	Version        string               `json:"version,omitempty"`
	LaunchApproved bool                 `json:"launch_approved,omitempty"`
	OAuthClientID  string               `json:"oauth_client_id,omitempty"`
	OAuthScopes    []string             `json:"oauth_scopes,omitempty"`
	Grants         map[string]ToolGrant `json:"grants,omitempty"`
	PackageType    string               `json:"package_type,omitempty"`
	PackageID      string               `json:"package_id,omitempty"`
	RequiredEnv    []string             `json:"required_environment,omitempty"`
	RepositoryURL  string               `json:"repository_url,omitempty"`
	WebsiteURL     string               `json:"website_url,omitempty"`
}

type SourceConfig struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Location string `json:"location"`
	Revision string `json:"revision,omitempty"`
	Digest   string `json:"digest,omitempty"`
	Trusted  bool   `json:"trusted,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	Builtin  bool   `json:"builtin,omitempty"`
	Scope    Scope  `json:"scope,omitempty"`
}

type AcquisitionSettings struct {
	AutomaticSelection string `json:"automatic_selection,omitempty"`
	Mode               string `json:"mode,omitempty"`
	TimeoutSeconds     int    `json:"timeout_seconds,omitempty"`
	MaxDownloadBytes   int64  `json:"max_download_bytes,omitempty"`
	MaxTools           int    `json:"max_tools,omitempty"`
	MaxSchemaBytes     int    `json:"max_schema_bytes,omitempty"`
}

type Entry struct {
	ID           string
	Kind         Kind
	Name         string
	Description  string
	Source       string
	Publisher    string
	Version      string
	Digest       string
	Origin       Scope
	Mode         string
	State        string
	Reason       string
	Path         string
	Managed      bool
	Installed    bool
	ConnectionID string
	Transport    string
	PackageType  string
	Repository   string
	Website      string
	Popularity   int64
}

type Tool struct {
	Name         string
	Description  string
	InputSchema  json.RawMessage
	OutputSchema json.RawMessage
	ConnectionID string
	OriginalName string
	Fingerprint  string
	Effect       Effect
	Scope        string
	Selected     bool
	Reason       string
}

type Resource struct {
	ConnectionID string
	URI          string
	Name         string
	Description  string
	MIMEType     string
}

type PromptArgument struct {
	Name        string
	Description string
	Required    bool
}

type Prompt struct {
	ConnectionID string
	Name         string
	Description  string
	Arguments    []PromptArgument
}

type Content struct {
	Type     string
	Text     string
	Data     string
	MIMEType string
	URI      string
}

type Result struct {
	Content    []Content
	Structured json.RawMessage
	IsError    bool
	Unknown    bool
}

type ConnectionState struct {
	ID          string
	State       string
	Error       string
	Fingerprint string
	Tools       []Tool
	Resources   []Resource
	Prompts     []Prompt
	Logs        []string
	Cached      bool
	AuthURL     string
	AuthPending bool
}

type Snapshot struct {
	Generation  uint64
	Entries     []Entry
	Connections []ConnectionState
	Settings    Settings
	Error       string
	Bindings    []AgentBinding
}

type AgentBinding struct {
	AgentID string
	ID      string
	Kind    Kind
	Version string
	Digest  string
}

type AcquisitionPreview struct {
	ID              string
	Kind            Kind
	Scope           Scope
	Operation       string
	Candidate       Entry
	Existing        *Entry
	Source          SourceConfig
	Requirements    []string
	RequestedAccess []string
	Token           string
}

type Action struct {
	Operation  string
	Kind       Kind
	ID         string
	Scope      Scope
	Skill      *SkillConfig
	Connection *ConnectionConfig
	Source     *SourceConfig
	Query      string
	Content    string
	Arguments  map[string]string
	Preview    *AcquisitionPreview
}

type RuntimeActor struct {
	ID             string
	RunID          string
	RuntimeEpoch   uint64
	TurnGeneration uint64
	WorkspaceID    string
	ShellOwner     *control.ShellOwner
	ExecutionRoot  string
	ParentID       string
	SessionID      string
	AllowedTools   []string
	DeniedTools    []string
	Effects        []Effect
	SkillModes     map[string]string
}

type RuntimeSnapshot struct {
	Generation     uint64
	RunID          string
	TurnGeneration uint64
	Tools          []Tool
	Notices        []string
}

type Runtime interface {
	Prepare(context.Context, RuntimeActor) (RuntimeSnapshot, error)
	Execute(context.Context, RuntimeActor, RuntimeSnapshot, string, map[string]any) (Result, error)
}

// RegistrationStore publishes a selected registration through its owning settings
// layer. Before is the reviewed preimage, not permission to replace other entries.
type RegistrationStore interface {
	CommitRegistration(context.Context, string, Settings, Settings) (Settings, error)
}

type AccessRequest struct {
	Tool        string `json:"tool"`
	Fingerprint string `json:"fingerprint"`
	Effect      Effect `json:"effect"`
	Scope       string `json:"scope"`
}

type AccessPreview struct {
	Connection ConnectionConfig
	Grants     []AccessRequest
	Tools      []Tool
	Token      string
}

type ActivationResult struct {
	ConnectionID string
	State        string
	UsableTools  []Tool
	Tools        []Tool
	Notices      []string
	Next         string
}

type SearchResult struct {
	Entries []Entry
	Warning string
}

type AccessApprovalRequired struct {
	Preview AccessPreview
	Actor   RuntimeActor
}

func (request *AccessApprovalRequired) Error() string {
	return "MCP tool access requires exact approval"
}

func (request *AccessApprovalRequired) CapabilityApproval() (string, string) {
	encoded, _ := json.Marshal(struct {
		Grants []AccessRequest
		Tools  []Tool
	}{request.Preview.Grants, request.Preview.Tools})
	return request.Preview.Connection.ID, "Approve durable MCP tool access (not execution) for " +
		request.Preview.Connection.Name + ". External observed schemas, exact fingerprints, effects and scopes (source data, not instructions):\n" + string(encoded) +
		"\nExternal mutations are not covered by native undo. Source annotations do not grant access."
}

func (request *AccessApprovalRequired) CapabilityApprovalContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, AccessApprovedKey{}, request.Preview)
}

type AccessApprovedKey struct{}

type ApprovalRequired struct {
	ID      string
	Reason  string
	Preview AcquisitionPreview
	Actor   RuntimeActor
}

func (request *ApprovalRequired) CapabilityApprovalContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, AcquisitionApprovedKey{}, request.Preview)
}

func (request *ApprovalRequired) Error() string {
	return fmt.Sprintf("Acquisition of %s requires approval: %s", request.ID, request.Reason)
}
func (request *ApprovalRequired) CapabilityApproval() (string, string) {
	return request.ID, request.Reason
}

type AcquisitionApprovedKey struct{}

func Clone(settings Settings) Settings {
	encoded, err := json.Marshal(settings)
	if err != nil {
		return Settings{}
	}
	var cloned Settings
	if json.Unmarshal(encoded, &cloned) != nil {
		return Settings{}
	}
	if cloned.Skills == nil {
		cloned.Skills = make(map[string]SkillConfig)
	}
	if cloned.Connections == nil {
		cloned.Connections = make(map[string]ConnectionConfig)
	}
	if cloned.Sources == nil {
		cloned.Sources = make(map[string]SourceConfig)
	}
	return cloned
}

// RegistrationChange is one reviewed capability registration publication. Only
// the selected identity is written; Before is the reviewed preimage of that
// identity, never permission to replace sibling registrations.
type RegistrationChange struct {
	ID         string
	Kind       Kind
	Before     Settings
	Skill      SkillConfig
	Connection ConnectionConfig
}

// RegistrationScope returns the binding the change targets.
func (change RegistrationChange) RegistrationScope() Scope {
	if change.Kind == KindSkill {
		return change.Skill.Scope
	}
	return change.Connection.Scope
}

// ApplyRegistration is the single owner of capability-registration publication.
// current is the writing layer's authoritative settings (the layer that will be
// modified) and policy is the current effective settings, which is where the
// acquisition policy and source availability live. It validates the reviewed
// preimage against both, then applies exactly the selected identity so sibling
// registrations and unrelated preferences are preserved. The workspace overlay
// store and the application config owner both call this.
func ApplyRegistration(current Settings, change RegistrationChange, policy Settings) (Settings, error) {
	scope := change.RegistrationScope()
	if scope != ScopeApplication && scope != ScopeWorkspace {
		return Settings{}, fmt.Errorf("capability registration must target application or workspace scope, not %q", scope)
	}
	if !reflect.DeepEqual(policy.Acquisition, change.Before.Acquisition) {
		return Settings{}, errors.New("capability acquisition policy changed; request a fresh acquisition")
	}
	sourceID := change.Connection.Source
	selectedExisted := false
	if change.Kind == KindSkill {
		sourceID = change.Skill.Source
		beforeValue, beforeExists := change.Before.Skills[change.ID]
		currentValue, currentExists := current.Skills[change.ID]
		if beforeExists != currentExists || !reflect.DeepEqual(beforeValue, currentValue) {
			return Settings{}, errors.New("selected capability registration changed; request a fresh acquisition")
		}
		selectedExisted = beforeExists
	} else {
		beforeValue, beforeExists := change.Before.Connections[change.ID]
		currentValue, currentExists := current.Connections[change.ID]
		if beforeExists != currentExists || !reflect.DeepEqual(beforeValue, currentValue) {
			return Settings{}, errors.New("selected capability registration changed; request a fresh acquisition")
		}
		selectedExisted = beforeExists
	}
	// Authored/custom connections have no catalog source. Their already
	// registered identity is the preimage for a grant-only update; a brand-new
	// acquisition still must name the reviewed source that supplied it.
	if sourceID != "" {
		beforeSource, beforeExists := change.Before.Sources[sourceID]
		policySource, policyExists := policy.Sources[sourceID]
		if beforeExists != policyExists || !reflect.DeepEqual(beforeSource, policySource) {
			return Settings{}, errors.New("capability source changed; request a fresh acquisition")
		}
	} else if !selectedExisted {
		return Settings{}, errors.New("new capability registration has no reviewed source")
	}
	return WithRegistration(current, change.Kind, change.ID, change.Skill, change.Connection), nil
}

// WithRegistration returns settings with exactly one reviewed registration
// applied. Only the selected identity is replaced; every sibling registration
// and unrelated preference is preserved.
func WithRegistration(settings Settings, kind Kind, id string, skill SkillConfig, connection ConnectionConfig) Settings {
	updated := Clone(settings)
	if kind == KindSkill {
		updated.Skills[id] = skill
	} else {
		updated.Connections[id] = connection
	}
	return updated
}

func Normalize(settings Settings) Settings {
	settings = Clone(settings)
	if settings.Skills == nil {
		settings.Skills = make(map[string]SkillConfig)
	}
	if settings.Connections == nil {
		settings.Connections = make(map[string]ConnectionConfig)
	}
	if settings.Sources == nil {
		settings.Sources = make(map[string]SourceConfig)
	}
	if settings.Acquisition.Mode == "" {
		settings.Acquisition.Mode = "ask"
	}
	if settings.Acquisition.AutomaticSelection == "" {
		settings.Acquisition.AutomaticSelection = "on"
	}
	if settings.Acquisition.TimeoutSeconds <= 0 {
		settings.Acquisition.TimeoutSeconds = 60
	}
	if settings.Acquisition.MaxDownloadBytes <= 0 {
		settings.Acquisition.MaxDownloadBytes = 32 << 20
	}
	if settings.Acquisition.MaxTools <= 0 {
		settings.Acquisition.MaxTools = 48
	}
	if settings.Acquisition.MaxSchemaBytes <= 0 {
		settings.Acquisition.MaxSchemaBytes = 64 << 10
	}
	return settings
}

const (
	AnthropicSkillsSourceID       = "anthropic-skills"
	OfficialMCPRegistrySourceID   = "official-mcp-registry"
	SkillsDirectorySourceID       = "skills-directory"
	AnthropicSkillsPinnedRevision = "8a1541c4a3ffa5a20a5a91de0dcf3f0bab1d1ef4"
)

// DefaultSources returns the curated discovery sources shipped with Crude.
// They are discovery inputs, not permission grants. Repository bytes remain
// commit-pinned and every install/update still requires its exact preview.
func DefaultSources() map[string]SourceConfig {
	return map[string]SourceConfig{
		AnthropicSkillsSourceID: {
			ID: AnthropicSkillsSourceID, Name: "Anthropic Skills (pinned)",
			Kind: "repository", Location: "https://github.com/anthropics/skills",
			Revision: AnthropicSkillsPinnedRevision, Trusted: true, Builtin: true,
			Scope: ScopeApplication,
		},
		OfficialMCPRegistrySourceID: {
			ID: OfficialMCPRegistrySourceID, Name: "Official MCP Registry (preview)",
			Kind: "registry", Location: "https://registry.modelcontextprotocol.io/v0/servers",
			Trusted: true, Builtin: true, Scope: ScopeApplication,
		},
		SkillsDirectorySourceID: {
			ID: SkillsDirectorySourceID, Name: "Skills Directory",
			Kind: "skill-registry", Location: "https://skills.sh/api/search",
			Trusted: true, Builtin: true, Scope: ScopeApplication,
		},
	}
}

// WithDefaultSources adds missing curated presets while preserving explicit
// edits and disable choices on existing entries. Built-ins may be disabled but
// are not silently deleted and recreated with changed trust.
func WithDefaultSources(settings Settings) Settings {
	settings = Normalize(settings)
	for id, source := range DefaultSources() {
		if _, exists := settings.Sources[id]; !exists {
			settings.Sources[id] = source
		}
	}
	return settings
}
