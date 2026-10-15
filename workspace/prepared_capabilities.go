package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"crude/capability"
	"crude/fileio"
	"crude/mcpclient"
	"crude/provider"
	"crude/skills"
	"crude/types"
	captypes "crude/types/capability"
	"reflect"
	"sync"
)

// PreparedCapabilities is captured on the domain owner. Its workers never read
// workspace configuration or session maps. Commit must durably compare the
// supplied private preimage; publication belongs to the caller's apply phase.
type PreparedCapabilities struct {
	service             *capability.Service
	generation          uint64
	defaults, effective types.AppSettings
	root                string
	commit              func(context.Context, types.AppSettings, types.AppSettings, captypes.Scope) (types.AppSettings, error)
}

var ErrApplicationSettingsConflict = errors.New("application settings preimage changed")

func (w *Workspace) PrepareCapabilities(commit func(context.Context, types.AppSettings, types.AppSettings, captypes.Scope) (types.AppSettings, error)) (*PreparedCapabilities, error) {
	if w.Capabilities == nil {
		return nil, errors.New("capability service is unavailable")
	}
	clone := func(v types.AppSettings) types.AppSettings {
		raw, _ := json.Marshal(v)
		var out types.AppSettings
		_ = json.Unmarshal(raw, &out)
		// JSON omits empty capability maps. Re-establish the same normalized
		// contract as AppDefaults/FileConfigToAppSettings before retaining a
		// private preimage; nil maps must not fabricate a concurrent edit.
		types.ApplyAppDefaults(&out)
		return out
	}
	return &PreparedCapabilities{service: w.Capabilities, generation: w.Capabilities.Snapshot().Generation, defaults: clone(w.AppDefaults()), effective: clone(w.LoadAppSettings()), root: w.RootPath, commit: commit}, nil
}
func (p *PreparedCapabilities) Snapshot(ctx context.Context) (captypes.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return captypes.Snapshot{}, err
	}
	return p.service.Snapshot(), nil
}
func (p *PreparedCapabilities) Preview(ctx context.Context, action captypes.Action) (captypes.AcquisitionPreview, error) {
	if action.Operation != "install" && action.Operation != "update" {
		return captypes.AcquisitionPreview{}, errors.New("preview requires install or update")
	}
	if err := p.checkCurrent(ctx); err != nil {
		return captypes.AcquisitionPreview{}, err
	}
	return p.service.PreviewAcquisition(ctx, action.ID, action.Scope, action.Operation)
}

// PrepareAction detaches every nested operand before launching a worker.
func (p *PreparedCapabilities) PrepareAction(action captypes.Action) func(context.Context) (captypes.Snapshot, []captypes.Entry, string, types.AppSettings, error) {
	raw, _ := json.Marshal(action)
	_ = json.Unmarshal(raw, &action)
	var once sync.Once
	var snapshot captypes.Snapshot
	var entries []captypes.Entry
	var content string
	var published types.AppSettings
	var err error
	return func(ctx context.Context) (captypes.Snapshot, []captypes.Entry, string, types.AppSettings, error) {
		once.Do(func() { snapshot, entries, content, published, err = p.action(ctx, action) })
		return snapshot, entries, content, published, err
	}
}
func (p *PreparedCapabilities) action(ctx context.Context, action captypes.Action) (captypes.Snapshot, []captypes.Entry, string, types.AppSettings, error) {
	service := p.service
	if err := p.checkCurrent(ctx); err != nil {
		return captypes.Snapshot{}, nil, "", types.AppSettings{}, err
	}
	working := p.effective
	if action.Scope == captypes.ScopeApplication {
		working = p.defaults
	}
	working.Capabilities = captypes.Clone(working.Capabilities)
	persist := false
	var content string
	var entries []captypes.Entry
	var err error
	switch action.Operation {
	case "snapshot", "refresh", "poll-auth":
	case "preview-install", "preview-update":
		previewAction := action
		previewAction.Operation = strings.TrimPrefix(action.Operation, "preview-")
		var preview captypes.AcquisitionPreview
		preview, err = service.PreviewAcquisition(ctx, previewAction.ID, previewAction.Scope, previewAction.Operation)
		if err == nil {
			encoded, encodeErr := json.Marshal(preview)
			content, err = string(encoded), encodeErr
		}
	case "discover":
		if sourceID := action.Arguments["source_id"]; sourceID != "" {
			entries, err = service.DiscoverSource(ctx, action.Query, action.Kind, 100, sourceID)
		} else {
			entries, err = service.Discover(ctx, action.Query, action.Kind, 100)
		}
	case "search":
		entries, err = service.Search(ctx, action.Query, action.Kind, 100, false)
	case "connect":
		err = service.Connect(ctx, action.ID)
	case "disconnect":
		err = service.Disconnect(action.ID)
	case "refresh-tools":
		err = service.Refresh(ctx, action.ID)
	case "sign-in":
		err = service.SignIn(ctx, action.ID)
	case "sign-out":
		err = service.SignOut(action.ID)
	case "auth-return":
		err = service.SubmitAuthorization(action.ID, action.Content)
	case "open-auth-url":
		err = errors.New("authorization URL is expired; start Sign in again")
		for _, state := range service.Snapshot().Connections {
			if state.ID == action.ID && state.AuthPending && state.AuthURL == action.Content {
				err = fileio.OpenBrowserURL(ctx, state.AuthURL)
				break
			}
		}
	case "test":
		if action.Connection == nil {
			err = errors.New("test needs a detached connection draft")
		} else {
			var state captypes.ConnectionState
			state, err = service.Test(ctx, *action.Connection)
			encoded, _ := json.Marshal(state)
			content = "Detached draft test (Save is still required):\n" + string(encoded)
		}
	case "save-connection":
		if action.Connection == nil {
			err = errors.New("connection draft is missing")
		} else if err = mcpclient.Validate(builtinIdentity(working.Capabilities, *action.Connection)); err == nil {
			value := builtinIdentity(working.Capabilities, *action.Connection)
			value.Scope = action.Scope
			working.Capabilities.Connections[value.ID], persist = value, true
		}
	case "set-secrets":
		err = service.SetSecrets(ctx, action.Arguments)
	case "save-source":
		if action.Source == nil || action.Source.ID == "" || action.Source.Location == "" {
			err = errors.New("source needs an identity and location")
		} else {
			value := *action.Source
			if existing, exists := working.Capabilities.Sources[value.ID]; exists && existing.Builtin {
				value.Builtin = true
			}
			value.Scope = action.Scope
			working.Capabilities.Sources[value.ID], persist = value, true
		}
	case "remove-source":
		if source, exists := working.Capabilities.Sources[action.ID]; !exists {
			err = errors.New("source is unavailable")
		} else if source.Builtin {
			err = errors.New("built-in capability sources can be disabled but not removed")
		} else {
			delete(working.Capabilities.Sources, action.ID)
			persist = true
		}
	case "reset-inherited":
		if action.Scope != captypes.ScopeWorkspace {
			err = errors.New("only workspace overrides can reset to inherited")
			break
		}
		inherited := p.defaults.Capabilities
		switch action.Kind {
		case captypes.KindSkill:
			delete(working.Capabilities.Skills, action.ID)
			if value, exists := inherited.Skills[action.ID]; exists {
				working.Capabilities.Skills[action.ID] = value
			}
		case captypes.KindMCP:
			delete(working.Capabilities.Connections, action.ID)
			if value, exists := inherited.Connections[action.ID]; exists {
				working.Capabilities.Connections[action.ID] = value
			}
		case "source":
			delete(working.Capabilities.Sources, action.ID)
			if value, exists := inherited.Sources[action.ID]; exists {
				working.Capabilities.Sources[action.ID] = value
			}
		default:
			err = errors.New("unknown inherited capability kind")
		}
		persist = err == nil
	case "make-local-copy":
		if action.Skill == nil || action.Skill.ID == "" {
			err = errors.New("local copy needs a new identity and destination")
			break
		}
		value := *action.Skill
		if _, exists := working.Capabilities.Skills[value.ID]; exists {
			err = errors.New("local identity is already registered")
			break
		}
		path := value.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.root, path)
		}
		if filepath.Base(filepath.Clean(path)) != value.Name {
			err = errors.New("local name must match its destination directory")
			break
		}
		var source, copied skills.Skill
		source, err = service.InspectSkill(ctx, action.ID)
		if err == nil {
			copied, err = skills.MakeLocalCopy(ctx, source, path)
		}
		if err == nil {
			value.Name, value.Path, value.Digest, value.Scope = copied.Name, copied.Path, copied.Digest, action.Scope
			value.Managed, value.Source, value.Version = false, "", ""
			working.Capabilities.Skills[value.ID], persist = value, true
		}
	case "add-local", "save-skill", "create-skill":
		if action.Skill == nil {
			err = errors.New("skill draft is missing")
			break
		}
		value := *action.Skill
		path := value.Path
		if !filepath.IsAbs(path) {
			path = filepath.Join(p.root, path)
		}
		var loaded skills.Skill
		if action.Operation == "create-skill" {
			loaded, err = skills.Create(path, value.Name, action.Content)
		} else {
			loaded, err = skills.LoadContext(ctx, path)
		}
		if err == nil {
			value.Name, value.Path, value.Digest, value.Scope = loaded.Name, loaded.Path, loaded.Digest, action.Scope
			if value.ID == "" {
				value.ID = "local:" + loaded.Name
			}
			working.Capabilities.Skills[value.ID], persist = value, true
		}
	case "install", "update":
		var acquired captypes.Settings
		if action.Preview == nil || action.Preview.ID != action.ID || action.Preview.Scope != action.Scope || action.Preview.Operation != action.Operation {
			err = errors.New("review the exact installation/update preview first")
		} else {
			acquired, err = service.AcquireReviewed(ctx, *action.Preview)
		}
		if err == nil {
			registrationAction := action
			registrationAction.Kind = action.Preview.Kind
			working.Capabilities, err = applyAcquiredRegistration(working.Capabilities, p.effective.Capabilities, acquired, registrationAction)
			persist = err == nil
		}

	case "remove", "disable", "enable":
		persist = true
		if action.Kind == "source" {
			value, exists := working.Capabilities.Sources[action.ID]
			if !exists {
				err = errors.New("source is unavailable")
			} else if action.Operation == "remove" {
				if value.Builtin {
					err = errors.New("built-in capability sources can be disabled but not removed")
				} else {
					delete(working.Capabilities.Sources, action.ID)
				}
			} else {
				value.Disabled = action.Operation == "disable"
				working.Capabilities.Sources[action.ID] = value
			}
		} else if action.Kind == captypes.KindSkill {
			value, exists := working.Capabilities.Skills[action.ID]
			if !exists {
				err = errors.New("skill is unavailable")
				break
			}
			if action.Operation == "remove" {
				delete(working.Capabilities.Skills, action.ID)
			} else {
				value.Disabled = action.Operation == "disable"
				if action.Operation == "enable" && value.Mode == "disabled" {
					value.Mode = "automatic"
				}
				working.Capabilities.Skills[action.ID] = value
			}
		} else {
			value, exists := working.Capabilities.Connections[action.ID]
			if !exists {
				err = errors.New("connection is unavailable")
				break
			}
			if action.Operation == "remove" && value.IsBuiltin() {
				err = errors.New("built-in connections can be disabled but not removed")
			} else if action.Operation == "remove" {
				delete(working.Capabilities.Connections, action.ID)
			} else {
				value.Disabled = action.Operation == "disable"
				working.Capabilities.Connections[action.ID] = value
			}
		}
	case "read-file":
		content, err = service.SkillFile(ctx, action.ID, action.Content)
	case "list-files":
		var files []string
		files, err = service.SkillFiles(ctx, action.ID)
		encoded, _ := json.Marshal(files)
		content = string(encoded)
	case "open-file":
		content, err = service.SkillPath(ctx, action.ID, action.Content)
	case "resource":
		var result captypes.Result
		result, err = service.ReadResource(ctx, action.ID, action.Content)
		encoded, _ := json.Marshal(result)
		content = string(encoded)
	case "prompt":
		var result captypes.Result
		result, err = service.GetPrompt(ctx, action.ID, action.Content, action.Arguments)
		encoded, _ := json.Marshal(result)
		content = string(encoded)
	case "import":
		var imported map[string]captypes.ConnectionConfig
		imported, err = capability.ImportConnections([]byte(action.Content), action.Scope)
		if err == nil {
			for id, connection := range imported {
				if _, exists := working.Capabilities.Connections[id]; exists {
					err = fmt.Errorf("connection %q already exists", id)
					break
				}
				working.Capabilities.Connections[id] = connection
			}
			persist = err == nil
		}
	default:
		err = fmt.Errorf("unsupported capability operation %q", action.Operation)
	}
	var published types.AppSettings
	if err == nil && persist {
		if p.commit == nil {
			err = errors.New("capability persistence is unavailable")
		} else if ctx.Err() != nil {
			err = ctx.Err()
		} else {
			before := p.effective
			if action.Scope == captypes.ScopeApplication {
				before = p.defaults
			}
			published, err = p.commit(ctx, before, working, action.Scope)
			if err == nil {
				effective := published
				if action.Scope == captypes.ScopeApplication {
					effective = types.MergeAppWorkspace(published, types.OverridesFromDiff(p.defaults, p.effective))
				}
				err = service.Configure(ctx, effective.Capabilities)
			}
		}
	}
	return service.Snapshot(), entries, content, published, err
}

// checkCurrent rejects a stale captured configuration before any runtime effect.
// The caller must serialize prepare/run/apply with other settings writers; this
// precondition is not a lock spanning external effects or application publication.
func (p *PreparedCapabilities) checkCurrent(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	snapshot := p.service.Snapshot()
	if snapshot.Generation != p.generation {
		return ErrApplicationSettingsConflict
	}
	current, err := json.Marshal(captypes.Normalize(snapshot.Settings))
	if err != nil {
		return err
	}
	expected, err := json.Marshal(captypes.Normalize(p.effective.Capabilities))
	if err != nil {
		return err
	}
	if string(current) != string(expected) {
		return ErrApplicationSettingsConflict
	}
	return nil
}

func applyAcquiredRegistration(current, policy, acquired captypes.Settings, action captypes.Action) (captypes.Settings, error) {
	before := captypes.Clone(policy)
	// The registration preimage belongs to the writing layer, while policy and
	// source checks use effective settings. Never promote effective siblings.
	before.Skills, before.Connections = captypes.Clone(current).Skills, captypes.Clone(current).Connections
	change := captypes.RegistrationChange{ID: action.ID, Kind: action.Kind, Before: before}
	if action.Kind == captypes.KindSkill {
		value, exists := acquired.Skills[action.ID]
		if !exists {
			return captypes.Settings{}, errors.New("acquired skill is missing")
		}
		change.Skill = value
	} else {
		value, exists := acquired.Connections[action.ID]
		if !exists {
			return captypes.Settings{}, errors.New("acquired connection is missing")
		}
		change.Connection = value
	}
	return captypes.ApplyRegistration(current, change, policy)
}

// PrepareSkillActivation captures the exact backend and actor policy on-owner.
func (w *Workspace) PrepareSkillActivation(agentID, id, arguments string) (func(context.Context) (provider.UserInput, error), error) {
	frontend := w.sessionForAgent(agentID)
	if frontend == nil || frontend.backend == nil || w.Capabilities == nil {
		return nil, errors.New("owning agent or capability service unavailable")
	}
	backend, service := frontend.backend, w.Capabilities
	category := backend.RuntimeProfile().GetEffectiveConfig(agentID).GetActiveDefaultCategory()
	actor := captypes.RuntimeActor{ID: agentID, SessionID: backend.SessionID(), Effects: []captypes.Effect{captypes.EffectRead, captypes.EffectCommunication}, SkillModes: category.SkillModes, AllowedTools: category.AllowedTools, DeniedTools: category.DeniedTools, SessionDisabled: backend.DisabledCapabilities()}
	raw, _ := json.Marshal(actor)
	_ = json.Unmarshal(raw, &actor)
	return func(ctx context.Context) (provider.UserInput, error) {
		result, err := service.Activate(ctx, actor, id, true)
		if err != nil {
			return provider.UserInput{}, err
		}
		text := "/skill " + id
		if arguments != "" {
			text += " " + arguments
		}
		input := provider.PlainInput(text)
		for _, content := range result.Content {
			if content.Text != "" {
				input.ContextParts = append(input.ContextParts, content.Text)
			}
		}
		return input, nil
	}, nil
}

// CommitPreparedCapabilityOverlay CASes the exact persisted sparse overlay in a
// transaction. It uses only the captured database and defaults, never live maps.
func (w *Workspace) PrepareCapabilityOverlayCommit() func(context.Context, types.AppSettings, types.AppSettings, captypes.Scope) (types.AppSettings, error) {
	db, defaults := w.WorkspaceDB, w.AppDefaults()
	return func(ctx context.Context, before, after types.AppSettings, scope captypes.Scope) (types.AppSettings, error) {
		if scope != captypes.ScopeWorkspace || db == nil {
			return types.AppSettings{}, errors.New("workspace capability persistence unavailable")
		}
		tx, err := db.DB.BeginTx(ctx, nil)
		if err != nil {
			return types.AppSettings{}, err
		}
		defer func() { _ = tx.Rollback() }()
		current, err := loadWorkspaceOverridesTx(ctx, tx, defaults)
		if err != nil {
			return types.AppSettings{}, err
		}
		if !reflect.DeepEqual(types.MergeAppWorkspace(defaults, current).Capabilities, before.Capabilities) {
			return types.AppSettings{}, ErrApplicationSettingsConflict
		}
		effective := types.MergeAppWorkspace(defaults, current)
		effective.Capabilities = captypes.Clone(after.Capabilities)
		raw, err := json.Marshal(types.OverridesFromDiff(defaults, effective))
		if err != nil {
			return types.AppSettings{}, err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO workspace_metadata(key,value,updated_at) VALUES(?,?,unixepoch()*1000000000) ON CONFLICT(key) DO UPDATE SET value=excluded.value,updated_at=excluded.updated_at`, workspaceSettingsKey, string(raw))
		if err != nil {
			return types.AppSettings{}, err
		}
		if err = tx.Commit(); err != nil {
			return types.AppSettings{}, err
		}
		return effective, nil
	}
}
