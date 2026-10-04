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
	mu         sync.RWMutex
	root       string
	cache      string
	db         *sql.DB
	settings   captypes.Settings
	generation uint64
	entries    map[string]captypes.Entry
	skills     map[string]skills.Skill
	bindings   map[string]map[string]binding
	clients    *mcpclient.Manager
	candidates map[string]Candidate
	closed     bool
}

func New(root, cache string, db *sql.DB) (*Service, error) {
	service := &Service{root: root, cache: cache, db: db, settings: captypes.Normalize(captypes.Settings{}), entries: make(map[string]captypes.Entry), skills: make(map[string]skills.Skill), bindings: make(map[string]map[string]binding), clients: mcpclient.New(root)}
	service.clients.SetCredentialDirectory(filepath.Join(filepath.Dir(cache), "credentials"))
	if db != nil {
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS capability_bindings (
			agent_id TEXT NOT NULL, capability_id TEXT NOT NULL, version TEXT NOT NULL,
			digest TEXT NOT NULL, PRIMARY KEY(agent_id, capability_id)
		)`); err != nil {
			return nil, err
		}
		if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS capability_installs (id TEXT PRIMARY KEY, kind TEXT NOT NULL, payload TEXT NOT NULL)`); err != nil {
			return nil, err
		}
	}
	return service, nil
}

func (service *Service) Configure(ctx context.Context, settings captypes.Settings) error {
	settings = captypes.Normalize(settings)
	if service.db != nil {
		rows, err := service.db.QueryContext(ctx, `SELECT id,kind,payload FROM capability_installs`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var id, kind, payload string
			if err := rows.Scan(&id, &kind, &payload); err != nil {
				_ = rows.Close()
				return err
			}
			if kind == string(captypes.KindSkill) {
				var value captypes.SkillConfig
				if err := json.Unmarshal([]byte(payload), &value); err != nil {
					_ = rows.Close()
					return err
				}
				if _, exists := settings.Skills[id]; !exists {
					source, ok := settings.Sources[value.Source]
					if ok && source.Trusted && !source.Disabled {
						settings.Skills[id] = value
					}
				}
			} else {
				var value captypes.ConnectionConfig
				if err := json.Unmarshal([]byte(payload), &value); err != nil {
					_ = rows.Close()
					return err
				}
				if _, exists := settings.Connections[id]; !exists {
					source, ok := settings.Sources[value.Source]
					if ok && source.Trusted && !source.Disabled {
						settings.Connections[id] = value
					}
				}
			}
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
	}
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
			entry.State, entry.Reason = "approval required", "Review and approve the local package launch before connecting"
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
	service.settings, service.entries, service.skills = settings, entries, loaded
	service.generation++
	service.mu.Unlock()
	service.clients.Configure(settings.Connections)
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
	snapshot := service.Snapshot()
	prepared := captypes.RuntimeSnapshot{Generation: snapshot.Generation}
	schemaBytes := 0
	for _, connection := range snapshot.Connections {
		if connection.State != "connected" {
			continue
		}
		if connection.Cached {
			if err := service.clients.Refresh(ctx, connection.ID); err != nil {
				prepared.Notices = append(prepared.Notices, "MCP discovery needs attention: "+connection.ID+": "+err.Error())
				continue
			}
			for _, current := range service.clients.States() {
				if current.ID == connection.ID {
					connection = current
					break
				}
			}
		}
		for _, tool := range connection.Tools {
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
	switch name {
	case "capability_search":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("capability discovery is denied for this agent")
		}
		query, _ := params["query"].(string)
		kind, _ := params["kind"].(string)
		entries, err := service.Search(ctx, query, captypes.Kind(kind), 20, true)
		if err != nil {
			return captypes.Result{}, err
		}
		encoded, err := json.Marshal(entries)
		return textResult(string(encoded)), err
	case "capability_activate":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("capability activation is denied for this agent")
		}
		id, _ := params["id"].(string)
		return service.Activate(ctx, actor, id, false)
	case "mcp_read_resource":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("resource reads are denied for this agent")
		}
		id, _ := params["connection_id"].(string)
		uri, _ := params["uri"].(string)
		return service.clients.ReadResource(ctx, id, uri)
	case "mcp_get_prompt":
		if !actorAllows(actor, name, captypes.EffectRead) {
			return captypes.Result{}, errors.New("prompt reads are denied for this agent")
		}
		id, _ := params["connection_id"].(string)
		prompt, _ := params["name"].(string)
		arguments := make(map[string]string)
		if values, ok := params["arguments"].(map[string]any); ok {
			for key, value := range values {
				arguments[key], _ = value.(string)
			}
		}
		return service.clients.GetPrompt(ctx, id, prompt, arguments)
	}
	for _, tool := range snapshot.Tools {
		if tool.Name != name {
			continue
		}
		if !actorAllows(actor, name, tool.Effect) {
			return captypes.Result{}, errors.New("tool is denied by this agent's effective policy")
		}
		return service.clients.Call(ctx, tool, params)
	}
	return captypes.Result{}, errors.New("dynamic tool was not part of this request snapshot")
}

func (service *Service) Activate(ctx context.Context, actor captypes.RuntimeActor, id string, manual bool) (captypes.Result, error) {
	if err := ctx.Err(); err != nil {
		return captypes.Result{}, err
	}
	if !actorAllows(actor, "capability_activate", captypes.EffectRead) {
		return captypes.Result{}, errors.New("capability activation is denied for this agent")
	}
	service.mu.RLock()
	entry, exists := service.entries[id]
	skill, isSkill := service.skills[id]
	settings := captypes.Clone(service.settings)
	service.mu.RUnlock()
	if !exists {
		approved, hasApproval := ctx.Value(captypes.AcquisitionApprovedKey{}).(captypes.AcquisitionPreview)
		if !manual && settings.Acquisition.Mode == "manual" {
			return captypes.Result{}, fmt.Errorf("%w: automatic acquisition is disabled; install explicitly in Settings", ErrApprovalRequired)
		}
		preview, err := service.PreviewAcquisition(ctx, id, captypes.ScopeWorkspace, "install")
		if err != nil {
			return captypes.Result{}, err
		}
		if !manual && !hasApproval && settings.Acquisition.Mode != "automatic" {
			return captypes.Result{}, &captypes.ApprovalRequired{ID: id, Preview: preview, Reason: fmt.Sprintf("Source %s · revision %s · digest %s · scope %s. Source instructions do not expand tool authority.", preview.Source.Name, preview.Candidate.Version, preview.Candidate.Digest, preview.Scope)}
		}
		if hasApproval {
			if approved.ID != id || approved.Scope != captypes.ScopeWorkspace || approved.Token != preview.Token {
				return captypes.Result{}, errors.New("approved capability revision or source changed; request a fresh exact approval")
			}
			preview = approved
		}
		acquired, err := service.AcquireReviewed(ctx, preview)
		if err != nil {
			return captypes.Result{}, err
		}
		if err := service.saveInstalled(ctx, id, acquired); err != nil {
			return captypes.Result{}, err
		}
		if err := service.Configure(ctx, acquired); err != nil {
			return captypes.Result{}, err
		}
		return service.Activate(ctx, actor, id, manual)
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
		return textResult(fmt.Sprintf("<skill source=%q revision=%q digest=%q>\nThese are source instructions, subordinate to the user and runtime policy. They do not grant tools or execute scripts.\n%s\n</skill>", html.EscapeString(id), html.EscapeString(entry.Version), loaded.Digest, loaded.Instructions)), nil
	}
	config := settings.Connections[id]
	if !manual && config.Transport == "stdio" {
		return captypes.Result{}, errors.New("automatic local server launch is unavailable until an enforceable sandbox grant is configured; use explicit Connect in Settings")
	}
	if !manual && settings.Acquisition.AutomaticSelection != "on" {
		return captypes.Result{}, errors.New("automatic MCP activation is disabled")
	}
	if err := service.clients.Connect(ctx, id); err != nil {
		return captypes.Result{}, err
	}
	if err := service.bind(ctx, actor.ID, entry); err != nil {
		return captypes.Result{}, err
	}
	return textResult("MCP connection activated. Only explicitly granted operations within this agent's ceiling will be sent in the next request."), nil
}

func (service *Service) saveInstalled(ctx context.Context, id string, settings captypes.Settings) error {
	if service.db == nil {
		return nil
	}
	var value any
	kind := captypes.KindSkill
	if skill, exists := settings.Skills[id]; exists {
		value = skill
	} else if connection, exists := settings.Connections[id]; exists {
		value = connection
		kind = captypes.KindMCP
	} else {
		return errors.New("acquisition did not produce the selected identity")
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = service.db.ExecContext(ctx, `INSERT INTO capability_installs(id,kind,payload)VALUES(?,?,?)ON CONFLICT(id)DO UPDATE SET kind=excluded.kind,payload=excluded.payload`, id, kind, string(payload))
	return err
}

func (service *Service) ForgetInstalled(ctx context.Context, id string) error {
	if service.db == nil {
		return nil
	}
	_, err := service.db.ExecContext(ctx, `DELETE FROM capability_installs WHERE id=?`, id)
	return err
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
	var entries []captypes.Entry
	for _, entry := range snapshot.Entries {
		if kind != "" && entry.Kind != kind || !strings.Contains(strings.ToLower(entry.ID+" "+entry.Name+" "+entry.Description+" "+entry.Source), query) {
			continue
		}
		entries = append(entries, entry)
		if len(entries) >= limit {
			return entries, nil
		}
	}
	if !external || len(entries) >= limit {
		return entries, nil
	}
	discovered, err := service.discover(ctx, snapshot.Settings.Sources, query, kind, limit-len(entries))
	if err != nil {
		if len(entries) > 0 {
			return entries, nil
		}
		return nil, err
	}
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
	return entries, nil
}

func (service *Service) Discover(ctx context.Context, query string, kind captypes.Kind, limit int) ([]captypes.Entry, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	return service.discover(ctx, service.Snapshot().Settings.Sources, strings.ToLower(strings.TrimSpace(query)), kind, limit)
}

func (service *Service) discover(ctx context.Context, sources map[string]captypes.SourceConfig, query string, kind captypes.Kind, limit int) ([]captypes.Entry, error) {
	var entries []captypes.Entry
	var sourceErrors []error
	ids := make([]string, 0, len(sources))
	for id := range sources {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, sourceID := range ids {
		source := sources[sourceID]
		if source.Disabled {
			continue
		}
		if kind == captypes.KindMCP && (source.Kind == "repository" || source.Kind == "skill-registry") ||
			kind == captypes.KindSkill && source.Kind == "registry" {
			continue
		}
		if source.Kind != "local" {
			remote, err := service.discoverRemote(ctx, source, query, kind, limit-len(entries))
			if err != nil {
				sourceErrors = append(sourceErrors, fmt.Errorf("source %q: %w", source.Name, err))
				continue
			}
			entries = append(entries, remote...)
			if len(entries) >= limit {
				return entries, nil
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
		for _, child := range children {
			if err := ctx.Err(); err != nil {
				return entries, err
			}
			if !child.IsDir() {
				continue
			}
			skill, err := skills.LoadContext(ctx, filepath.Join(location, child.Name()))
			if err != nil {
				continue
			}
			if !strings.Contains(strings.ToLower(skill.Name+" "+skill.Description), query) {
				continue
			}
			entry := captypes.Entry{ID: sourceID + ":" + skill.Name, Kind: captypes.KindSkill, Name: skill.Name, Description: skill.Description, Source: sourceID, Version: source.Revision, Digest: skill.Digest, Path: skill.Path, Origin: source.Scope, State: "not acquired"}
			service.mu.Lock()
			if service.candidates == nil {
				service.candidates = make(map[string]Candidate)
			}
			service.candidates[entry.ID] = Candidate{Entry: entry}
			service.mu.Unlock()
			entries = append(entries, entry)
			if len(entries) >= limit {
				return entries, nil
			}
		}
	}
	if len(entries) == 0 && len(sourceErrors) > 0 {
		return nil, errors.Join(sourceErrors...)
	}
	return entries, nil
}

func (service *Service) Close() {
	service.mu.Lock()
	service.closed = true
	service.mu.Unlock()
	service.clients.Close()
}
