package workspace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"crude/agent"
	"crude/buildinfo"
	"crude/capability"
	"crude/fileio"
	"crude/mcpclient"
	"crude/provider"
	"crude/session"
	"crude/skills"
	"crude/types"
	captypes "crude/types/capability"
)

func (workspace *Workspace) ensureCapabilities(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	workspace.capabilityMu.RLock()
	available := workspace.Capabilities != nil && workspace.WorkspaceDB != nil
	workspace.capabilityMu.RUnlock()
	if available {
		return nil
	}
	workspace.capabilityMu.Lock()
	defer workspace.capabilityMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if workspace.Capabilities != nil {
		return nil
	}
	if workspace.WorkspaceDB == nil {
		return errors.New("workspace persistence is unavailable")
	}
	cache := filepath.Join(workspace.homeDir, buildinfo.DataDir(), "capabilities", "artifacts")
	if workspace.homeDir == "" {
		cache = filepath.Join(workspace.WorkspaceDir, "artifacts")
	}
	service, err := capability.New(workspace.RootPath, cache, workspace.WorkspaceDB.DB)
	if err != nil {
		return err
	}
	service.SetRegistrationStore(newCapabilityRegistrationStore(workspace))
	if err := service.Configure(ctx, workspace.LoadAppSettings().Capabilities); err != nil {
		service.Close()
		return err
	}
	workspace.Capabilities = service
	if workspace.FileOwnership == nil {
		workspace.FileOwnership = agent.NewFileOwnership()
	}
	return nil
}

func (workspace *Workspace) bindSessionRuntime(backend *session.Session) error {
	if err := workspace.ensureWorkspacePersistence(); err != nil {
		return err
	}
	if err := workspace.ensureCapabilities(context.Background()); err != nil {
		return err
	}
	if workspace.Collaboration == nil {
		return errors.New("workspace collaboration runtime is unavailable")
	}
	backend.SetCollaboration(workspace.Collaboration, workspace.instanceID)
	backend.SetCapabilities(workspace.Capabilities, workspace.FileOwnership)
	return nil
}

func (workspace *Workspace) CapabilitySnapshot(ctx context.Context) (captypes.Snapshot, error) {
	if err := workspace.ensureCapabilities(ctx); err != nil {
		return captypes.Snapshot{}, err
	}
	workspace.capabilityMu.RLock()
	defer workspace.capabilityMu.RUnlock()
	if workspace.Capabilities == nil || workspace.WorkspaceDB == nil || ctx.Err() != nil {
		return captypes.Snapshot{}, errors.New("capability workspace owner is closed or cancelled")
	}
	if err := workspace.Capabilities.Configure(ctx, workspace.LoadAppSettings().Capabilities); err != nil {
		return captypes.Snapshot{}, err
	}
	return workspace.Capabilities.Snapshot(), nil
}

// CapabilityRuntimeSnapshot observes already initialized live state. Unlike
// CapabilitySnapshot it does not reload settings, inspect skill files, or
// reconfigure transports; presentation polling must never do that work.
func (workspace *Workspace) CapabilityRuntimeSnapshot(ctx context.Context) (captypes.Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return captypes.Snapshot{}, err
	}
	workspace.capabilityMu.RLock()
	defer workspace.capabilityMu.RUnlock()
	if workspace.Capabilities == nil {
		return captypes.Snapshot{}, nil
	}
	return workspace.Capabilities.Snapshot(), ctx.Err()
}

func (workspace *Workspace) CapabilityAction(ctx context.Context, action captypes.Action) (captypes.Snapshot, []captypes.Entry, string, error) {
	if err := workspace.ensureCapabilities(ctx); err != nil {
		return captypes.Snapshot{}, nil, "", err
	}
	workspace.capabilityMu.RLock()
	defer workspace.capabilityMu.RUnlock()
	if workspace.Capabilities == nil || workspace.WorkspaceDB == nil || ctx.Err() != nil {
		return captypes.Snapshot{}, nil, "", errors.New("capability workspace owner is closed or cancelled")
	}
	service := workspace.Capabilities
	if err := service.Configure(ctx, workspace.LoadAppSettings().Capabilities); err != nil {
		return service.Snapshot(), nil, "", err
	}
	working := workspace.LoadAppSettings()
	if action.Scope == captypes.ScopeApplication {
		working = workspace.AppDefaults()
	}
	working.Capabilities = captypes.Normalize(working.Capabilities)
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
		} else if err = mcpclient.Validate(*action.Connection); err == nil {
			value := *action.Connection
			value.Scope = action.Scope
			working.Capabilities.Connections[value.ID], persist = value, true
		}
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
		inherited := workspace.AppDefaults().Capabilities
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
			path = filepath.Join(workspace.RootPath, path)
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
			path = filepath.Join(workspace.RootPath, path)
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
			for id, skill := range acquired.Skills {
				if skill.Scope == action.Scope {
					working.Capabilities.Skills[id] = skill
				}
			}
			for id, connection := range acquired.Connections {
				if connection.Scope == action.Scope {
					working.Capabilities.Connections[id] = connection
				}
			}
			persist = true
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
				working.Capabilities.Skills[action.ID] = value
			}
		} else {
			value, exists := working.Capabilities.Connections[action.ID]
			if !exists {
				err = errors.New("connection is unavailable")
				break
			}
			if action.Operation == "remove" {
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
	if err == nil && persist {
		if err := ctx.Err(); err != nil {
			return service.Snapshot(), entries, content, err
		}
		if action.Scope == captypes.ScopeApplication {
			err = workspace.SaveAppSettings(working)
		} else {
			err = workspace.SaveWorkspaceSettings(types.OverridesFromDiff(workspace.AppDefaults(), working))
		}
		if err == nil {
			err = service.Configure(ctx, workspace.LoadAppSettings().Capabilities)
		}
	}
	return service.Snapshot(), entries, content, err
}

func (workspace *Workspace) PrepareSkillInput(ctx context.Context, agentID, id, arguments string) (provider.UserInput, error) {
	if err := workspace.ensureCapabilities(ctx); err != nil {
		return provider.UserInput{}, err
	}
	frontend := workspace.sessionForAgent(agentID)
	if frontend == nil || frontend.backend == nil {
		return provider.UserInput{}, errors.New("owning agent is unavailable")
	}
	category := frontend.backend.RuntimeProfile().GetEffectiveConfig(agentID).GetActiveDefaultCategory()
	actor := captypes.RuntimeActor{ID: agentID, SessionID: frontend.backend.SessionID(), Effects: []captypes.Effect{captypes.EffectRead, captypes.EffectCommunication}, SkillModes: category.SkillModes, AllowedTools: category.AllowedTools, DeniedTools: category.DeniedTools}
	result, err := workspace.Capabilities.Activate(ctx, actor, id, true)
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
}
