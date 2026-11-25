package capability

import (
	"context"
	"crude/types/control"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
)

type Kind string

const (
	KindSkill Kind = "skill"
	KindMCP   Kind = "mcp"
	KindTool  Kind = "tool"
)

type Scope string

const (
	ScopeApplication Scope = "application"
	ScopeWorkspace   Scope = "workspace"
)

type Effect string

const (
	// EffectUnknown is an external operation the user has not classified.
	EffectUnknown       Effect = "unknown"
	EffectRead          Effect = "read"
	EffectWrite         Effect = "write"
	EffectDelete        Effect = "delete"
	EffectExecute       Effect = "execute"
	EffectCommunication Effect = "communication"
)

// AllowedBy checks an operation against the caller's effect ceiling. An opaque
// server operation can do any of these things; its annotations are not authority.
func (effect Effect) AllowedBy(allows func(Effect) bool) bool {
	if effect != EffectUnknown {
		return allows(effect)
	}
	return allows(EffectRead) && allows(EffectWrite) && allows(EffectDelete) && allows(EffectExecute) && allows(EffectCommunication)
}

type Settings struct {
	Skills      map[string]SkillConfig      `json:"skills,omitempty"`
	Connections map[string]ConnectionConfig `json:"connections,omitempty"`
	Sources     map[string]SourceConfig     `json:"sources,omitempty"`
	Acquisition AcquisitionSettings         `json:"acquisition,omitempty"`
	// WebSearchRoutes are application-owned account search methods for
	// Crude's Web MCP. A workspace cannot add or redirect them.
	WebSearchRoutes []WebSearchRoute `json:"web_search_routes,omitempty"`
	// ProjectToolDecisions is workspace-only authority owned by the dedicated
	// project setup transaction, not by generic capability settings.
	ProjectToolDecisions json.RawMessage `json:"project_tool_decisions,omitempty"`
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
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Host        string            `json:"host,omitempty"`
	Transport   string            `json:"transport"`
	URL         string            `json:"url,omitempty"`
	Command     string            `json:"command,omitempty"`
	Args        []string          `json:"args,omitempty"`
	Directory   string            `json:"directory,omitempty"`
	Environment map[string]string `json:"environment,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	SecretRefs  map[string]string `json:"secret_refs,omitempty"`
	Auth        string            `json:"auth,omitempty"`
	// CredentialRef is the write-only vault reference for a registered OAuth client secret.
	CredentialRef    string               `json:"credential_ref,omitempty"`
	TimeoutSeconds   int                  `json:"timeout_seconds,omitempty"`
	Disabled         bool                 `json:"disabled,omitempty"`
	Scope            Scope                `json:"scope,omitempty"`
	Source           string               `json:"source,omitempty"`
	Version          string               `json:"version,omitempty"`
	LaunchApproved   bool                 `json:"launch_approved,omitempty"`
	OAuthClientID    string               `json:"oauth_client_id,omitempty"`
	OAuthMetadataURL string               `json:"oauth_metadata_url,omitempty"`
	OAuthIssuer      string               `json:"oauth_issuer,omitempty"`
	OAuthScopes      []string             `json:"oauth_scopes,omitempty"`
	Grants           map[string]ToolGrant `json:"grants,omitempty"`
	PackageType      string               `json:"package_type,omitempty"`
	PackageID        string               `json:"package_id,omitempty"`
	RequiredEnv      []string             `json:"required_environment,omitempty"`
	RepositoryURL    string               `json:"repository_url,omitempty"`
	WebsiteURL       string               `json:"website_url,omitempty"`
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
	// ServerRevision binds an in-flight call to the same launch or endpoint. It is not part of the model schema.
	ServerRevision string
	Name           string
	Description    string
	InputSchema    json.RawMessage
	OutputSchema   json.RawMessage
	ConnectionID   string
	OriginalName   string
	Fingerprint    string
	Effect         Effect
	Scope          string
	Selected       bool
	Reason         string
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
	// Code records locally qualified tool evidence, not remote text or metadata.
	Code string
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
	// Stderr is the redacted diagnostic tail of the current/last local launch.
	Stderr      string
	Cached      bool
	AuthURL     string
	AuthPending bool
}

// ToolCatalog returns the detached part of discovery used to prepare requests.
func (s ConnectionState) ToolCatalog() ConnectionState {
	return (ConnectionState{ID: s.ID, State: s.State, Cached: s.Cached, Tools: s.Tools}).Clone()
}

// Clone returns detached evidence without encoding and parsing the schemas.
func (s ConnectionState) Clone() ConnectionState {
	s.Tools = slices.Clone(s.Tools)
	for i := range s.Tools {
		s.Tools[i].InputSchema = slices.Clone(s.Tools[i].InputSchema)
		s.Tools[i].OutputSchema = slices.Clone(s.Tools[i].OutputSchema)
	}
	s.Resources = slices.Clone(s.Resources)
	s.Prompts = slices.Clone(s.Prompts)
	for i := range s.Prompts {
		s.Prompts[i].Arguments = slices.Clone(s.Prompts[i].Arguments)
	}
	s.Logs = slices.Clone(s.Logs)
	return s
}

type Snapshot struct {
	Generation  uint64
	Entries     []Entry
	Connections []ConnectionState
	Settings    Settings
	Error       string
	Bindings    []AgentBinding
	// StoredSecrets names the values held in Crude's secret store. Values never
	// leave the core.
	StoredSecrets []string
	// WebSearch holds the latest outcome of each search method, by route ID.
	// A route without an entry has not searched since the core started.
	WebSearch map[string]WebSearchObservation
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
	// WebSearchRoute is the route that Save, Remove and Test search method
	// operations act on. Remove uses only its ID.
	WebSearchRoute *WebSearchRoute
}

type RuntimeActor struct {
	// CatalogOnly reads connected tools without starting hosts or refreshing connections.
	CatalogOnly    bool
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
	// SessionDisabled lists skill/MCP IDs the owning session turned off for
	// itself and all its agents, read live at each request.
	SessionDisabled []string
}

// DisabledForSession reports whether the owning session turned id off.
func (actor RuntimeActor) DisabledForSession(id string) bool {
	return id != "" && slices.Contains(actor.SessionDisabled, id)
}

// ErrSessionDisabled explains a capability the owning session turned off.
func ErrSessionDisabled(id string) error {
	return fmt.Errorf("capability %q is turned off for this session; the user can turn it back on with /skills or /mcps", id)
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

// ActivationResult lists the tools offered to this agent, without repeating their schemas.
type ActivationResult struct {
	ConnectionID string `json:"connection_id"`
	State        string `json:"state"`
	// Usable tools are callable by Name from the next request.
	Usable  []UsableTool `json:"usable_tools,omitempty"`
	Notices []string     `json:"notices,omitempty"`
	Next    string       `json:"next"`
}

// UsableTool is one granted tool and the authority it was granted with.
type UsableTool struct {
	Name   string `json:"name"`
	Tool   string `json:"tool"`
	Effect Effect `json:"effect"`
	Scope  string `json:"scope"`
}

// SearchHit is one capability_search result for a model: what it is, whether
// it can be used now, and the exact ID to activate.
type SearchHit struct {
	ID          string `json:"id"`
	Kind        Kind   `json:"kind"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	Installed   bool   `json:"installed,omitempty"`
	State       string `json:"state"`
	Reason      string `json:"reason,omitempty"`
	Source      string `json:"source,omitempty"`
	Transport   string `json:"transport,omitempty"`
	Package     string `json:"package,omitempty"`
	Version     string `json:"version,omitempty"`
	Repository  string `json:"repository,omitempty"`
}

// SearchResult is the model-facing capability_search payload. Warning names
// sources that could not answer; the results from the others remain usable.
type SearchResult struct {
	Results []SearchHit `json:"results"`
	Warning string      `json:"warning,omitempty"`
}

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
	raw := slices.Clone(settings.ProjectToolDecisions)
	settings.ProjectToolDecisions = nil
	encoded, err := json.Marshal(settings)
	if err != nil {
		return Settings{}
	}
	var cloned Settings
	if json.Unmarshal(encoded, &cloned) != nil {
		return Settings{}
	}
	cloned.ProjectToolDecisions = raw
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
	if change.Kind != KindSkill && change.Kind != KindMCP {
		return Settings{}, fmt.Errorf("unsupported capability registration kind %q", change.Kind)
	}
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
	switch kind {
	case KindSkill:
		updated.Skills[id] = skill
	case KindMCP:
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

// WithDefaults adds missing curated sources and built-in connections while
// preserving explicit edits and disable choices on existing entries. Built-ins
// may be disabled but are not silently deleted and recreated with changed trust.
func WithDefaults(settings Settings) Settings {
	settings = Normalize(settings)
	for id, source := range DefaultSources() {
		if _, exists := settings.Sources[id]; !exists {
			settings.Sources[id] = source
		}
	}
	for id, connection := range DefaultConnections() {
		existing, exists := settings.Connections[id]
		if !exists {
			settings.Connections[id] = connection
			continue
		}
		// The built-in identity is Crude's own code; only the user's choices
		// (enabled state, name, timeout, per-tool grants) are theirs to keep.
		existing.Transport, existing.Command, existing.Host = connection.Transport, connection.Command, ""
		existing.URL, existing.Args, existing.Directory, existing.Source = "", nil, "", ""
		settings.Connections[id] = existing
	}
	return settings
}
