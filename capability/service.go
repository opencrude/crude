package capability

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"crude/mcpclient"
	"crude/skills"
	captypes "crude/types/capability"
)

type binding struct {
	ID      string
	Version string
	Digest  string
}

var ErrApprovalRequired = errors.New("capability acquisition needs explicit user approval")

type Service struct {
	mu               sync.RWMutex
	configureMu      sync.Mutex
	publishMu        sync.Mutex
	root             string
	cache            string
	db               *sql.DB
	settings         captypes.Settings
	generation       uint64
	entries          map[string]captypes.Entry
	skills           map[string]skills.Skill
	bindings         map[string]map[string]binding
	clients          *mcpclient.Manager
	candidates       map[string]Candidate
	store            captypes.RegistrationStore
	closed           bool
	taskGrants       map[string]map[string]taskGrant
	launches         map[string]map[string]string
	runs             map[string]string
	host             captypes.HostDispatcher
	hostStates       map[string]map[string]captypes.ConnectionState
	authority        captypes.RuntimeAuthority
	executionClients map[string]*mcpclient.Manager
	// prepared records package preparation identities known to be installed.
	prepared map[string]bool
}

func New(root, cache string, db *sql.DB) (*Service, error) {
	service := &Service{root: root, cache: cache, db: db, settings: captypes.Normalize(captypes.Settings{}), entries: make(map[string]captypes.Entry), skills: make(map[string]skills.Skill), bindings: make(map[string]map[string]binding), clients: mcpclient.New(root)}
	service.clients.SetCredentialDirectory(filepath.Join(filepath.Dir(cache), "credentials"))
	service.hostStates = make(map[string]map[string]captypes.ConnectionState)
	service.taskGrants = make(map[string]map[string]taskGrant)
	service.launches = make(map[string]map[string]string)
	service.runs = make(map[string]string)
	service.hostStates = make(map[string]map[string]captypes.ConnectionState)
	service.executionClients = make(map[string]*mcpclient.Manager)
	if db != nil {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS capability_authorizations (owner_id TEXT NOT NULL, session_id TEXT NOT NULL, run_id TEXT NOT NULL, origin TEXT NOT NULL, phase TEXT NOT NULL, token TEXT NOT NULL, evidence TEXT NOT NULL, created_at INTEGER NOT NULL)`); err != nil {
			return nil, err
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS capability_bindings (
			agent_id TEXT NOT NULL, capability_id TEXT NOT NULL, version TEXT NOT NULL,
			digest TEXT NOT NULL, PRIMARY KEY(agent_id, capability_id)
		)`); err != nil {
			return nil, err
		}
	}
	return service, nil
}

// SetRegistrationStore binds publication to the authoritative settings owner.
// Installations must not create a second settings registry inside this service.
func (service *Service) SetRegistrationStore(store captypes.RegistrationStore) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.store = store
}

func (service *Service) Configure(ctx context.Context, settings captypes.Settings) error {
	service.configureMu.Lock()
	defer service.configureMu.Unlock()
	return service.configure(ctx, settings)
}

func (service *Service) configure(ctx context.Context, settings captypes.Settings) error {
	settings = captypes.Normalize(settings)
	entries := make(map[string]captypes.Entry)
	loaded := make(map[string]skills.Skill)
	for id, config := range settings.Skills {
		if err := ctx.Err(); err != nil {
			return err
		}
		entry := captypes.Entry{ID: id, Kind: captypes.KindSkill, Name: config.Name, Source: config.Source, Version: config.Version, Digest: config.Digest, Origin: config.Scope, Mode: config.Mode, Path: config.Path, Managed: config.Managed, Installed: true, State: "available"}
		if config.Disabled || config.Mode == "disabled" {
			entry.State = "disabled"
		}
		path := config.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(service.root, path)
		}
		skill, err := skills.Inspect(path)
		if err == nil {
			err = skills.CheckAvailability(skill)
		}
		if err != nil {
			entry.State, entry.Reason = "unavailable", err.Error()
		} else {
			loaded[id] = skill
			entry.Name, entry.Description, entry.Path = skill.Name, skill.Description, skill.Path
		}
		entries[id] = entry
	}
	for id, config := range settings.Connections {
		entry := captypes.Entry{
			ID: id, Kind: captypes.KindMCP, Name: config.Name, Source: config.Source,
			Version: config.Version, Origin: config.Scope, ConnectionID: id,
			Installed: true, State: "not started", Transport: config.Transport,
			PackageType: config.PackageType, Repository: config.RepositoryURL, Website: config.WebsiteURL,
		}
		if config.Disabled {
			entry.State = "disabled"
		} else if missing := missingConnectionEnvironment(config); len(missing) > 0 {
			entry.State, entry.Reason = "setup required", "Set required environment: "+strings.Join(missing, ", ")
		} else if config.Transport == "stdio" && !config.LaunchApproved {
			entry.State, entry.Reason = "approval required", "An agent's activation authorizes each launch under the session's Manual/Auto Review/YOLO policy; approve the local launch here to connect without asking"
		}
		if err := mcpclient.Validate(config); err != nil {
			entry.State, entry.Reason = "unavailable", err.Error()
		}
		entries[id] = entry
	}
	service.mu.Lock()
	if service.closed {
		service.mu.Unlock()
		return errors.New("capability service is closed")
	}
	service.clients.Configure(settings.Connections)
	for _, clients := range service.executionClients {
		clients.Configure(settings.Connections)
	}
	// Settings publication is a revocation boundary for task authority. Durable
	// installs/grants survive; in-memory review must use the new exact policy.
	service.hostStates = make(map[string]map[string]captypes.ConnectionState)
	service.taskGrants = make(map[string]map[string]taskGrant)
	service.launches = make(map[string]map[string]string)
	service.settings, service.entries, service.skills = settings, entries, loaded
	service.generation++
	service.mu.Unlock()
	return nil
}

func (service *Service) Snapshot() captypes.Snapshot {
	service.mu.RLock()
	defer service.mu.RUnlock()
	snapshot := captypes.Snapshot{Generation: service.generation, Settings: captypes.Clone(service.settings), Connections: service.clients.States()}
	states := make(map[string]captypes.ConnectionState)
	for _, state := range snapshot.Connections {
		states[state.ID] = state
	}
	for id, entry := range service.entries {
		if entry.Kind == captypes.KindMCP && entry.State == "not started" {
			if state, exists := states[id]; exists {
				entry.State, entry.Reason = state.State, state.Error
			}
		}
		snapshot.Entries = append(snapshot.Entries, entry)
	}
	for agentID, bindings := range service.bindings {
		for id, binding := range bindings {
			snapshot.Bindings = append(snapshot.Bindings, captypes.AgentBinding{AgentID: agentID, ID: id, Kind: service.entries[id].Kind, Version: binding.Version, Digest: binding.Digest})
		}
	}
	sort.Slice(snapshot.Bindings, func(first, second int) bool {
		if snapshot.Bindings[first].AgentID == snapshot.Bindings[second].AgentID {
			return snapshot.Bindings[first].ID < snapshot.Bindings[second].ID
		}
		return snapshot.Bindings[first].AgentID < snapshot.Bindings[second].AgentID
	})
	sort.Slice(snapshot.Entries, func(first, second int) bool { return snapshot.Entries[first].ID < snapshot.Entries[second].ID })
	return snapshot
}

func missingConnectionEnvironment(config captypes.ConnectionConfig) []string {
	var missing []string
	for _, name := range config.RequiredEnv {
		if strings.TrimSpace(config.Environment[name]) == "" && strings.TrimSpace(config.SecretRefs[name]) == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}

func (service *Service) Prepare(ctx context.Context, actor captypes.RuntimeActor) (captypes.RuntimeSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return captypes.RuntimeSnapshot{}, err
	}
	if err := service.validateRuntime(ctx, actor); err != nil {
		return captypes.RuntimeSnapshot{}, err
	}
	if err := service.prepareHosts(ctx, actor); err != nil {
		return captypes.RuntimeSnapshot{}, err
	}
	snapshot := service.actorSnapshot(actor)
	service.mu.Lock()
	var retired *mcpclient.Manager
	if prior := service.runs[actorKey(actor)]; prior != taskKey(actor) {
		delete(service.taskGrants, prior)
		delete(service.launches, prior)
		retired = service.executionClients[prior]
		delete(service.executionClients, prior)
	}
	service.runs[actorKey(actor)] = taskKey(actor)
	service.mu.Unlock()
	if retired != nil {
		retired.Close()
	}
	prepared := captypes.RuntimeSnapshot{Generation: snapshot.Generation, RunID: actor.RunID, TurnGeneration: actor.TurnGeneration}
	schemaBytes := 0
	for _, connection := range snapshot.Connections {
		if connection.State != "connected" || actor.DisabledForSession(connection.ID) {
			continue
		}
		if connection.Cached {
			if err := service.actorClients(actor).Refresh(ctx, connection.ID); err != nil {
				prepared.Notices = append(prepared.Notices, "MCP discovery needs attention: "+connection.ID+": "+err.Error())
				continue
			}
			for _, current := range service.actorClients(actor).States() {
				if current.ID == connection.ID {
					connection = current
					break
				}
			}
		}
		for _, tool := range connection.Tools {
			if grant, ok := service.taskAccess(actor, snapshot.Settings.Connections[connection.ID], tool.OriginalName); ok && !grant.Disabled && grant.Fingerprint == tool.Fingerprint {
				tool.Selected, tool.Effect, tool.Scope, tool.Reason = true, grant.Effect, grant.Scope, ""
			}
			if !tool.Selected || !actorAllows(actor, tool.Name, tool.Effect) {
				continue
			}
			if len(prepared.Tools) >= snapshot.Settings.Acquisition.MaxTools || schemaBytes+len(tool.InputSchema) > snapshot.Settings.Acquisition.MaxSchemaBytes {
				prepared.Notices = append(prepared.Notices, "Tool schema budget reached; use capability_search to select a bounded set of relevant tools.")
				break
			}
			prepared.Tools = append(prepared.Tools, tool)
			schemaBytes += len(tool.InputSchema)
		}
	}
	return prepared, nil
}

func actorAllows(actor captypes.RuntimeActor, name string, effect captypes.Effect) bool {
	if slices.Contains(actor.DeniedTools, name) || len(actor.AllowedTools) > 0 && !slices.Contains(actor.AllowedTools, name) {
		return false
	}
	return slices.Contains(actor.Effects, effect)
}

func (service *Service) Execute(ctx context.Context, actor captypes.RuntimeActor, snapshot captypes.RuntimeSnapshot, name string, params map[string]any) (captypes.Result, error) {
	if err := ctx.Err(); err != nil {
		return captypes.Result{}, err
	}
	if err := service.validateRuntime(ctx, actor); err != nil {
		return captypes.Result{}, err
	}
	switch name {
	case "capability_search":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("capability discovery is denied for this agent")
		}
		query, _ := params["query"].(string)
		kind, _ := params["kind"].(string)
		limit := 20
		if value, ok := params["limit"].(float64); ok {
			limit = int(value)
		} else if value, ok := params["limit"].(int); ok {
			limit = value
		}
		entries, err := service.Search(ctx, query, captypes.Kind(kind), limit, true)
		if err != nil && len(entries) == 0 {
			return captypes.Result{}, err
		}
		for index := range entries {
			if actor.DisabledForSession(entries[index].ID) {
				entries[index].State, entries[index].Reason = "disabled", "turned off for this session by the user"
			}
		}
		var payload any = entries
		if err != nil {
			payload = captypes.SearchResult{Entries: entries, Warning: err.Error()}
		}
		encoded, encodeErr := json.Marshal(payload)
		return textResult(string(encoded)), encodeErr
	case "capability_activate":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("capability activation is denied for this agent")
		}
		id, _ := params["id"].(string)
		scope := captypes.ScopeApplication
		if value, ok := params["scope"].(string); ok && value != "" {
			scope = captypes.Scope(value)
		}
		var environment map[string]string
		for key, value := range params {
			switch key {
			case "id", "scope":
			case "environment":
				decoded, err := decodeEnvironment(value)
				if err != nil {
					return captypes.Result{}, err
				}
				environment = decoded
			default:
				return captypes.Result{}, fmt.Errorf("capability_activate accepts id, an optional scope and an optional environment; %q is not recognized, and MCP grants belong to mcp_request_access", key)
			}
		}
		if id == "" {
			return captypes.Result{}, errors.New("capability_activate needs an exact capability id")
		}
		if scope != captypes.ScopeApplication && scope != captypes.ScopeWorkspace {
			return captypes.Result{}, errors.New("capability_activate scope must be application or workspace")
		}
		return service.activate(ctx, actor, id, false, scope, environment)
	case "mcp_request_access":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("MCP access requests are denied for this agent")
		}
		id, _ := params["connection_id"].(string)
		if id == "" || len(params) != 2 {
			return captypes.Result{}, errors.New("mcp_request_access requires only connection_id and exact grants")
		}
		if actor.DisabledForSession(id) {
			return captypes.Result{}, captypes.ErrSessionDisabled(id)
		}
		return service.activateAccess(ctx, actor, id, params["grants"])
	case "mcp_read_resource":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("resource reads are denied for this agent")
		}
		id, _ := params["connection_id"].(string)
		uri, _ := params["uri"].(string)
		if actor.DisabledForSession(id) {
			return captypes.Result{}, captypes.ErrSessionDisabled(id)
		}
		return service.actorClients(actor).ReadResource(ctx, id, uri)
	case "mcp_get_prompt":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("prompt reads are denied for this agent")
		}
		id, _ := params["connection_id"].(string)
		prompt, _ := params["name"].(string)
		if actor.DisabledForSession(id) {
			return captypes.Result{}, captypes.ErrSessionDisabled(id)
		}
		arguments := make(map[string]string)
		if values, ok := params["arguments"].(map[string]any); ok {
			for key, value := range values {
				arguments[key], _ = value.(string)
			}
		}
		return service.actorClients(actor).GetPrompt(ctx, id, prompt, arguments)
	}
	for _, tool := range snapshot.Tools {
		if tool.Name != name {
			continue
		}
		if !actorAllows(actor, name, tool.Effect) {
			return captypes.Result{}, errors.New("tool is denied by this agent's effective policy")
		}
		if actor.DisabledForSession(tool.ConnectionID) {
			return captypes.Result{}, captypes.ErrSessionDisabled(tool.ConnectionID)
		}
		if snapshot.RunID != actor.RunID || snapshot.TurnGeneration != actor.TurnGeneration {
			return captypes.Result{}, errors.New("dynamic request belongs to an expired run")
		}
		service.mu.RLock()
		config := captypes.Clone(service.settings).Connections[tool.ConnectionID]
		currentRun := service.runs[actorKey(actor)]
		service.mu.RUnlock()
		if currentRun != taskKey(actor) {
			return captypes.Result{}, errors.New("dynamic request run was revoked or replaced")
		}
		if config.Disabled {
			return captypes.Result{}, errors.New("MCP connection is disabled")
		}
		if config.Source != "" {
			service.mu.RLock()
			source, exists := service.settings.Sources[config.Source]
			service.mu.RUnlock()
			if !exists || source.Disabled || !source.Trusted {
				return captypes.Result{}, errors.New("MCP source is unavailable or untrusted")
			}
		}
		if grant, ok := service.taskAccess(actor, config, tool.OriginalName); ok {
			return service.actorClients(actor).CallAuthorized(ctx, tool, params, config, grant)
		}
		return service.actorClients(actor).Call(ctx, tool, params)
	}
	return captypes.Result{}, errors.New("dynamic tool was not part of this request snapshot")
}

// Activate binds a newly acquired capability to the application layer. That is
// the product default: an installation belongs to the user, not to one project,
// so deleting a workspace can never destroy it. Callers that genuinely want a
// workspace-owned binding use ActivateScoped with ScopeWorkspace.
func (service *Service) Activate(ctx context.Context, actor captypes.RuntimeActor, id string, manual bool) (captypes.Result, error) {
	return service.ActivateScoped(ctx, actor, id, manual, captypes.ScopeApplication)
}

func (service *Service) ActivateScoped(ctx context.Context, actor captypes.RuntimeActor, id string, manual bool, scope captypes.Scope) (captypes.Result, error) {
	return service.activate(ctx, actor, id, manual, scope, nil)
}

func (service *Service) activate(ctx context.Context, actor captypes.RuntimeActor, id string, manual bool, scope captypes.Scope, environment map[string]string) (captypes.Result, error) {
	if err := ctx.Err(); err != nil {
		return captypes.Result{}, err
	}
	if !manual {
		if err := service.validateRuntime(ctx, actor); err != nil {
			return captypes.Result{}, err
		}
	}
	if actor.DisabledForSession(id) {
		return captypes.Result{}, captypes.ErrSessionDisabled(id)
	}
	if !actorAllows(actor, "capability_activate", captypes.EffectRead) {
		return captypes.Result{}, errors.New("capability activation is denied for this agent")
	}
	if scope != captypes.ScopeApplication && scope != captypes.ScopeWorkspace {
		return captypes.Result{}, errors.New("capability binding must be application or workspace")
	}
	service.mu.RLock()
	entry, exists := service.entries[id]
	skill, isSkill := service.skills[id]
	settings := captypes.Clone(service.settings)
	service.mu.RUnlock()
	if !exists {
		service.mu.RLock()
		_, discovered := service.candidates[id]
		service.mu.RUnlock()
		if !discovered {
			return captypes.Result{}, fmt.Errorf("capability %q is not an installed or discovered exact ID; use capability_search and pass the returned ID unchanged, not its name or path", id)
		}
		approved, hasApproval := ctx.Value(captypes.AcquisitionApprovedKey{}).(captypes.AcquisitionPreview)
		if !manual && (settings.Acquisition.Mode == "manual" || settings.Acquisition.Mode == "disabled") {
			return captypes.Result{}, fmt.Errorf("%w: automatic acquisition is disabled; install explicitly in Settings", ErrApprovalRequired)
		}
		preview, err := service.PreviewAcquisition(ctx, id, scope, "install")
		if err != nil {
			return captypes.Result{}, err
		}
		if !manual && !hasApproval && settings.Acquisition.Mode != "automatic" {
			return captypes.Result{}, &captypes.ApprovalRequired{ID: id, Preview: preview, Actor: actor, Reason: fmt.Sprintf("Source %s · revision %s · digest %s · scope %s. Source instructions do not expand tool authority.", preview.Source.Name, preview.Candidate.Version, preview.Candidate.Digest, preview.Scope)}
		}
		if hasApproval {
			if approved.ID != id || approved.Scope != scope || approved.Token != preview.Token {
				return captypes.Result{}, errors.New("approved capability revision, source or binding changed; request a fresh exact approval")
			}
			preview = approved
			if auth, ok := captypes.AuthorizationFrom(ctx); ok && (auth.Token != preview.Token || !reflect.DeepEqual(auth.Actor, actor)) {
				return captypes.Result{}, errors.New("acquisition authorization owner or run changed")
			}
		}
		origin := "policy"
		if hasApproval {
			origin = "human"
		}
		if auth, ok := captypes.AuthorizationFrom(ctx); ok {
			origin = auth.Origin
		}
		if err := service.recordAuthorization(ctx, actor, origin, "acquisition", preview.Token, (&captypes.ApprovalRequired{Preview: preview}).Evidence()); err != nil {
			return captypes.Result{}, err
		}
		acquired, err := service.AcquireReviewed(ctx, preview)
		if err != nil {
			return captypes.Result{}, err
		}
		if err := service.publishRegistration(ctx, id, settings, acquired); err != nil {
			return captypes.Result{}, err
		}
		return service.activate(ctx, actor, id, manual, scope, environment)
	}
	if entry.State == "disabled" || entry.State == "unavailable" {
		return captypes.Result{}, fmt.Errorf("capability %q is %s: %s", id, entry.State, entry.Reason)
	}
	mode := entry.Mode
	if override, exists := actor.SkillModes[id]; exists {
		mode = override
	}
	if mode == "disabled" || mode == "manual" && !manual {
		return captypes.Result{}, errors.New("skill invocation is disabled or manual-only for this agent")
	}
	if isSkill && len(environment) > 0 {
		return captypes.Result{}, errors.New("environment applies only to MCP connections")
	}
	if isSkill {
		if !manual && settings.Acquisition.AutomaticSelection != "on" {
			return captypes.Result{}, errors.New("automatic selection is disabled")
		}
		loaded, err := skills.LoadContext(ctx, skill.Path)
		if err != nil {
			return captypes.Result{}, err
		}
		if entry.Managed && entry.Digest != "" && entry.Digest != loaded.Digest {
			return captypes.Result{}, errors.New("managed skill bytes differ from their pinned digest")
		}
		entry.Digest = loaded.Digest
		if err := service.bind(ctx, actor.ID, entry); err != nil {
			return captypes.Result{}, err
		}
		return textResult(fmt.Sprintf("<skill source=%q revision=%q digest=%q path=%q>\nThese are source instructions, subordinate to the user and runtime policy. They do not grant tools or execute scripts. Bundled files are relative to the installed path above.\n%s\n</skill>", html.EscapeString(id), html.EscapeString(entry.Version), loaded.Digest, html.EscapeString(loaded.Path), loaded.Instructions)), nil
	}
	if !manual && settings.Acquisition.AutomaticSelection != "on" {
		return captypes.Result{}, errors.New("automatic MCP activation is disabled")
	}
	if len(environment) > 0 {
		if err := service.configureEnvironment(ctx, actor, id, environment); err != nil {
			return captypes.Result{}, err
		}
		service.mu.RLock()
		settings = captypes.Clone(service.settings)
		service.mu.RUnlock()
	}
	config := settings.Connections[id]
	if err := service.connectActor(ctx, actor, config, settings, manual); err != nil {
		return captypes.Result{}, err
	}
	if err := service.bind(ctx, actor.ID, entry); err != nil {
		return captypes.Result{}, err
	}
	return service.activationResult(ctx, actor, id)
}

func (service *Service) publishRegistration(ctx context.Context, id string, before, after captypes.Settings) error {
	// Do not hold the configuration lock while the settings owner obtains its
	// workspace lifetime lease: a concurrent manager command may already hold
	// that lease while waiting to configure.
	service.publishMu.Lock()
	defer service.publishMu.Unlock()
	service.mu.RLock()
	store, closed := service.store, service.closed
	service.mu.RUnlock()
	if store == nil || closed {
		return errors.New("capability registration persistence is unavailable; installation was not published")
	}
	persisted, err := store.CommitRegistration(ctx, id, before, after)
	if err != nil {
		return err
	}
	return service.Configure(ctx, persisted)
}

func (service *Service) InspectSkill(ctx context.Context, id string) (skills.Skill, error) {
	service.mu.RLock()
	skill, exists := service.skills[id]
	entry := service.entries[id]
	service.mu.RUnlock()
	if !exists {
		return skills.Skill{}, errors.New("skill is unavailable")
	}
	loaded, err := skills.LoadContext(ctx, skill.Path)
	if err == nil && entry.Managed && entry.Digest != "" && entry.Digest != loaded.Digest {
		return loaded, errors.New("managed artifact differs from its digest")
	}
	return loaded, err
}

func (service *Service) bind(ctx context.Context, actorID string, entry captypes.Entry) error {
	if actorID == "" {
		return errors.New("activation needs a stable agent identity")
	}
	if service.db != nil {
		if _, err := service.db.ExecContext(ctx, `INSERT INTO capability_bindings(agent_id, capability_id, version, digest) VALUES(?,?,?,?)
			ON CONFLICT(agent_id, capability_id) DO UPDATE SET version=excluded.version,digest=excluded.digest`, actorID, entry.ID, entry.Version, entry.Digest); err != nil {
			return err
		}
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.bindings[actorID] == nil {
		service.bindings[actorID] = make(map[string]binding)
	}
	service.bindings[actorID][entry.ID] = binding{ID: entry.ID, Version: entry.Version, Digest: entry.Digest}
	return nil
}

func textResult(text string) captypes.Result {
	return captypes.Result{Content: []captypes.Content{{Type: "text", Text: text}}}
}

func (service *Service) Connect(ctx context.Context, id string) error {
	return service.clients.Connect(ctx, id)
}
func (service *Service) Disconnect(id string) error { return service.clients.Disconnect(id) }
func (service *Service) Refresh(ctx context.Context, id string) error {
	return service.clients.Refresh(ctx, id)
}
func (service *Service) Test(ctx context.Context, config captypes.ConnectionConfig) (captypes.ConnectionState, error) {
	return service.clients.Test(ctx, config)
}
func (service *Service) SignIn(ctx context.Context, id string) error {
	return service.clients.SignIn(ctx, id)
}
func (service *Service) SignOut(id string) error { return service.clients.SignOut(id) }
func (service *Service) SubmitAuthorization(id, callback string) error {
	return service.clients.SubmitAuthorization(id, callback)
}
func (service *Service) ReadResource(ctx context.Context, id, uri string) (captypes.Result, error) {
	return service.clients.ReadResource(ctx, id, uri)
}
func (service *Service) GetPrompt(ctx context.Context, id, name string, arguments map[string]string) (captypes.Result, error) {
	return service.clients.GetPrompt(ctx, id, name, arguments)
}

func (service *Service) SkillFile(ctx context.Context, id, relative string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	skill, err := service.InspectSkill(ctx, id)
	if err != nil {
		return "", err
	}
	data, err := skills.ReadFile(skill, relative, 1<<20)
	return string(data), err
}

func (service *Service) SkillFiles(ctx context.Context, id string) ([]string, error) {
	skill, err := service.InspectSkill(ctx, id)
	return slices.Clone(skill.Files), err
}

func (service *Service) SkillPath(ctx context.Context, id, relative string) (string, error) {
	skill, err := service.InspectSkill(ctx, id)
	if err != nil {
		return "", err
	}
	service.mu.RLock()
	managed := service.entries[id].Managed
	service.mu.RUnlock()
	if managed {
		return "", errors.New("managed artifacts are immutable; make a local copy before opening in the editor")
	}
	if _, err := skills.ReadFile(skill, relative, 1<<20); err != nil {
		return "", err
	}
	return filepath.Join(skill.Path, filepath.FromSlash(relative)), nil
}

func (service *Service) Search(ctx context.Context, query string, kind captypes.Kind, limit int, external bool) ([]captypes.Entry, error) {
	query = strings.ToLower(strings.TrimSpace(query))
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	snapshot := service.Snapshot()
	entries := make([]captypes.Entry, 0)
	for _, entry := range snapshot.Entries {
		if kind != "" && entry.Kind != kind {
			continue
		}
		entries = append(entries, entry)
	}
	entries = rankCapabilityEntries(entries, newCapabilityQuery(query), limit)
	if !external || len(entries) > 0 {
		return entries, nil
	}
	discovered, err := service.discover(ctx, snapshot.Settings.Sources, query, kind, limit-len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		seen[entry.ID] = struct{}{}
	}
	for _, entry := range discovered {
		if _, exists := seen[entry.ID]; exists {
			continue
		}
		entries = append(entries, entry)
		if len(entries) >= limit {
			break
		}
	}
	return entries, err
}

func (service *Service) Discover(ctx context.Context, query string, kind captypes.Kind, limit int) ([]captypes.Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return service.discover(ctx, service.Snapshot().Settings.Sources, strings.ToLower(strings.TrimSpace(query)), kind, limit)
}

// DiscoverSource searches exactly one enabled source, without an all-source
// result budget hiding matches from the selected catalog.
func (service *Service) DiscoverSource(ctx context.Context, query string, kind captypes.Kind, limit int, sourceID string) ([]captypes.Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	source, exists := service.Snapshot().Settings.Sources[sourceID]
	if !exists {
		return nil, fmt.Errorf("capability source %q is unknown", sourceID)
	}
	if source.Disabled {
		return nil, fmt.Errorf("capability source %q is disabled", sourceID)
	}
	if kind == captypes.KindMCP && (source.Kind == "repository" || source.Kind == "skill-registry") ||
		kind == captypes.KindSkill && source.Kind == "registry" {
		return nil, fmt.Errorf("capability source %q does not provide %s entries", sourceID, kind)
	}
	return service.discover(ctx, map[string]captypes.SourceConfig{sourceID: source}, strings.ToLower(strings.TrimSpace(query)), kind, limit)
}

func (service *Service) discover(ctx context.Context, sources map[string]captypes.SourceConfig, query string, kind captypes.Kind, limit int) ([]captypes.Entry, error) {
	matcher := newCapabilityQuery(query)
	var groups [][]captypes.Entry
	var sourceErrors []error
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, sourceID := range ids {
		if err := ctx.Err(); err != nil {
			return interleaveDiscovery(groups, limit), err
		}
		source := sources[sourceID]
		if source.Disabled {
			continue
		}
		if kind == captypes.KindMCP && (source.Kind == "repository" || source.Kind == "skill-registry") ||
			kind == captypes.KindSkill && source.Kind == "registry" {
			continue
		}
		if source.Kind != "local" {
			remote, err := service.discoverRemote(ctx, source, query, kind, limit)
			if err != nil {
				sourceErrors = append(sourceErrors, fmt.Errorf("source %q: %w", source.Name, err))
			}
			if len(remote) > 0 {
				groups = append(groups, remote)
			}
			continue
		}
		if kind == captypes.KindMCP {
			continue
		}
		location := source.Location
		if !filepath.IsAbs(location) {
			location = filepath.Join(service.root, location)
		}
		children, err := os.ReadDir(location)
		if err != nil {
			sourceErrors = append(sourceErrors, fmt.Errorf("source %q: %w", source.Name, err))
			continue
		}
		var local []captypes.Entry
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return interleaveDiscovery(append(groups, local), limit), err
			}
			if !child.IsDir() {
				continue
			}
			skill, err := skills.LoadContext(ctx, filepath.Join(location, child.Name()))
			if err != nil {
				continue
			}
			entry := captypes.Entry{ID: sourceID + ":" + skill.Name, Kind: captypes.KindSkill, Name: skill.Name, Description: skill.Description, Source: sourceID, Version: source.Revision, Digest: skill.Digest, Path: skill.Path, Origin: source.Scope, State: "not acquired"}
			local = append(local, entry)
		}
		local = rankCapabilityEntries(local, matcher, limit)
		if len(local) > 0 {
			service.mu.Lock()
			if service.candidates == nil {
				service.candidates = make(map[string]Candidate)
			}
			for _, entry := range local {
				service.candidates[entry.ID] = Candidate{Entry: entry}
			}
			service.mu.Unlock()
			groups = append(groups, local)
		}
	}
	return interleaveDiscovery(groups, limit), errors.Join(sourceErrors...)
}

// Interleave source-ranked results so the first enabled source cannot exhaust
// the entire result budget before later sources contribute.
func interleaveDiscovery(groups [][]captypes.Entry, limit int) []captypes.Entry {
	entries := make([]captypes.Entry, 0)
	seen := make(map[string]bool)
	for offset := 0; len(entries) < limit; offset++ {
		available := false
		for _, group := range groups {
			if offset >= len(group) {
				continue
			}
			available = true
			entry := group[offset]
			if !seen[entry.ID] {
				entries = append(entries, entry)
				seen[entry.ID] = true
			}
			if len(entries) >= limit {
				break
			}
		}
		if !available {
			break
		}
	}
	return entries
}

func (service *Service) Close() {
	service.mu.Lock()
	service.closed = true
	clients := service.executionClients
	service.executionClients = make(map[string]*mcpclient.Manager)
	service.hostStates = make(map[string]map[string]captypes.ConnectionState)
	service.taskGrants = make(map[string]map[string]taskGrant)
	service.launches = make(map[string]map[string]string)
	service.runs = make(map[string]string)
	service.mu.Unlock()
	service.clients.Close()
	for _, client := range clients {
		client.Close()
	}
}
