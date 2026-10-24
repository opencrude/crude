package core

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"crude/coreprotocol"
	captypes "crude/types/capability"
	"crude/types/control"
)

// Capability self control lets an agent manage MCP connections, their secrets
// and per-tool switches for the user through the same owner Settings uses.
// Effects follow the session's Manual/Auto Review/YOLO policy; secret values
// are write-only and never appear in results.

type controlConnectionInput struct {
	ID             string             `json:"id"`
	Name           *string            `json:"name"`
	Transport      *string            `json:"transport"`
	URL            *string            `json:"url"`
	Command        *string            `json:"command"`
	Args           *[]string          `json:"args"`
	Directory      *string            `json:"directory"`
	Environment    *map[string]string `json:"environment"`
	Headers        *map[string]string `json:"headers"`
	SecretRefs     *map[string]string `json:"secret_refs"`
	Auth           *string            `json:"auth"`
	TimeoutSeconds *int               `json:"timeout_seconds"`
	LaunchApproved *bool              `json:"launch_approved"`
	Enabled        *bool              `json:"enabled"`
}

type controlCapabilityInput struct {
	ID         string                  `json:"id"`
	Name       string                  `json:"name"`
	Secret     string                  `json:"secret"`
	Tool       string                  `json:"tool"`
	Enabled    *bool                   `json:"enabled"`
	Connection *controlConnectionInput `json:"connection"`
}

// CapabilityConnectionView is the agent-visible summary of one connection.
type CapabilityConnectionView struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	State     string   `json:"state"`
	Builtin   bool     `json:"builtin,omitempty"`
	Disabled  bool     `json:"disabled,omitempty"`
	Tools     []string `json:"tools,omitempty"`
	ToolsOff  []string `json:"tools_off,omitempty"`
	Secrets   []string `json:"secrets,omitempty"`
}

type CapabilityControlView struct {
	Revision      string                     `json:"revision"`
	Connections   []CapabilityConnectionView `json:"connections"`
	Skills        []string                   `json:"skills,omitempty"`
	StoredSecrets []string                   `json:"stored_secrets,omitempty"`
}

func (a *actor) prepareControlCapabilities(in control.Request, owner coreprotocol.Owner) (func(context.Context) control.Result, func(control.Result) control.Result, bool) {
	var input controlCapabilityInput
	if err := controlDecode(in.Input, &input); err != nil {
		return controlFailure(in, control.Invalid, err.Error())
	}
	current := a.ws.LoadAppSettings().Capabilities
	action := captypes.Action{Kind: captypes.KindMCP, ID: input.ID, Scope: captypes.ScopeApplication}
	var secrets *coreprotocol.ConnectionSecretUpdates
	switch in.CommandID {
	case "capabilities.inspect":
		action.Operation = "snapshot"
	case "capabilities.set_secret", "capabilities.remove_secret":
		if input.Name == "" {
			return controlFailure(in, control.Invalid, "name is required (for example EXA_API_KEY)")
		}
		value := input.Secret
		if in.CommandID == "capabilities.remove_secret" {
			value = ""
		} else if value == "" {
			return controlFailure(in, control.Invalid, "secret is required; use capabilities.remove_secret to delete one")
		}
		action.Operation, action.Arguments = "set-secrets", map[string]string{input.Name: value}
	case "capabilities.set_enabled":
		existing, exists := current.Connections[input.ID]
		if !exists || input.Enabled == nil {
			return controlFailure(in, control.Invalid, "an installed connection id and enabled are required")
		}
		action.Operation = "disable"
		if *input.Enabled {
			action.Operation = "enable"
		}
		action.Scope = scopeOf(existing)
	case "capabilities.set_tool_enabled":
		existing, exists := current.Connections[input.ID]
		if !exists || input.Tool == "" || input.Enabled == nil {
			return controlFailure(in, control.Invalid, "an installed connection id, tool and enabled are required")
		}
		grant, granted := existing.Grants[input.Tool]
		if *input.Enabled && !granted && !existing.IsBuiltin() {
			return controlFailure(in, control.Invalid, "tool access for an external server is granted with mcp_request_access, which reviews its exact schema and effect")
		}
		grant.Disabled = !*input.Enabled
		existing.Grants = cloneGrants(existing.Grants)
		existing.Grants[input.Tool] = grant
		action.Operation, action.Connection, action.Scope = "save-connection", &existing, scopeOf(existing)
		secrets = keepConnectionSecrets()
	case "capabilities.save_connection":
		if input.Connection == nil || input.Connection.ID == "" {
			return controlFailure(in, control.Invalid, "connection.id is required")
		}
		action.ID = input.Connection.ID
		existing, exists := current.Connections[action.ID]
		if !exists {
			existing = captypes.ConnectionConfig{ID: action.ID, Name: action.ID, Transport: "http", Scope: captypes.ScopeApplication}
		}
		merged, updates := mergeControlConnection(existing, *input.Connection)
		action.Operation, action.Connection, action.Scope, secrets = "save-connection", &merged, scopeOf(existing), &updates
	case "capabilities.remove_connection":
		existing, exists := current.Connections[input.ID]
		if !exists {
			return controlFailure(in, control.Invalid, "connection is not installed")
		}
		action.Operation, action.Scope = "remove", scopeOf(existing)
	default:
		return nil, nil, false
	}
	request := coreprotocol.CapabilityActionRequest{Action: action}
	method := coreprotocol.CapabilityAction
	if action.Operation == "snapshot" {
		method = coreprotocol.CapabilitySnapshot
	}
	if secrets != nil {
		request.ConnectionSecrets = *secrets
		connection := *action.Connection
		connection.Args, connection.Environment, connection.Headers, connection.SecretRefs, connection.CredentialRef, connection.URL = nil, nil, nil, nil, "", ""
		request.Action.Connection = &connection
	}
	payload, _ := coreprotocol.Marshal(&request)
	rpc := coreprotocol.Request{ID: in.ID, Owner: owner, WorkspaceID: in.Target.WorkspaceID, Method: method, Payload: payload}
	run, apply, handled := a.prepareApplicationSettings(rpc)
	if !handled {
		return controlFailure(in, control.Unavailable, "capability settings owner is unavailable")
	}
	summarized := func(ctx context.Context) coreprotocol.Response {
		response := run(ctx)
		if response.Error != nil {
			return response
		}
		result, ok := resultValue[coreprotocol.CapabilityResult](response)
		if !ok {
			return fault(rpc, "operation_failed", "capability result is unreadable")
		}
		response.Value = capabilityControlView(result)
		return response
	}
	var publish func(coreprotocol.Response) error
	if apply != nil {
		publish = func(result coreprotocol.Response) error { apply(result); return nil }
	}
	return wrapControlOperation(in, summarized, publish)
}

func scopeOf(config captypes.ConnectionConfig) captypes.Scope {
	if config.Scope == captypes.ScopeWorkspace {
		return captypes.ScopeWorkspace
	}
	return captypes.ScopeApplication
}

func cloneGrants(grants map[string]captypes.ToolGrant) map[string]captypes.ToolGrant {
	out := make(map[string]captypes.ToolGrant, len(grants)+1)
	for name, grant := range grants {
		out[name] = grant
	}
	return out
}

func keepConnectionSecrets() *coreprotocol.ConnectionSecretUpdates {
	keep := coreprotocol.ConnectionSecretUpdates{}
	keep.Endpoint.Keep, keep.Args.Keep, keep.Environment.Keep = true, true, true
	keep.Headers.Keep, keep.SecretRefs.Keep, keep.CredentialRef.Keep = true, true, true
	return &keep
}

// mergeControlConnection applies only the fields the agent supplied; every
// omitted field keeps its stored value, including write-only ones.
func mergeControlConnection(existing captypes.ConnectionConfig, input controlConnectionInput) (captypes.ConnectionConfig, coreprotocol.ConnectionSecretUpdates) {
	merged := existing
	updates := *keepConnectionSecrets()
	set := func(target *string, value *string) {
		if value != nil {
			*target = *value
		}
	}
	set(&merged.Name, input.Name)
	set(&merged.Transport, input.Transport)
	set(&merged.Command, input.Command)
	set(&merged.Directory, input.Directory)
	set(&merged.Auth, input.Auth)
	if input.TimeoutSeconds != nil {
		merged.TimeoutSeconds = *input.TimeoutSeconds
	}
	if input.LaunchApproved != nil {
		merged.LaunchApproved = *input.LaunchApproved
	}
	if input.Enabled != nil {
		merged.Disabled = !*input.Enabled
	}
	if input.URL != nil {
		updates.Endpoint = coreprotocol.SecretString{Value: input.URL}
	}
	if input.Args != nil {
		updates.Args = coreprotocol.SecretStrings{Value: *input.Args}
	}
	if input.Environment != nil {
		updates.Environment = coreprotocol.SecretMap{Value: *input.Environment}
	}
	if input.Headers != nil {
		updates.Headers = coreprotocol.SecretMap{Value: *input.Headers}
	}
	if input.SecretRefs != nil {
		updates.SecretRefs = coreprotocol.SecretMap{Value: *input.SecretRefs}
	}
	return merged, updates
}

func capabilityControlView(result coreprotocol.CapabilityResult) CapabilityControlView {
	snapshot := result.Snapshot
	view := CapabilityControlView{Revision: result.Revision, StoredSecrets: slices.Clone(snapshot.StoredSecrets)}
	states := make(map[string]captypes.ConnectionState)
	for _, state := range snapshot.Connections {
		states[state.ID] = state
	}
	for id, config := range snapshot.Settings.Connections {
		state := states[id]
		entry := CapabilityConnectionView{ID: id, Name: config.Name, Transport: config.Transport, State: state.State, Builtin: config.IsBuiltin(), Disabled: config.Disabled}
		if entry.State == "" {
			entry.State = "not started"
		}
		for _, tool := range state.Tools {
			if tool.Selected {
				entry.Tools = append(entry.Tools, tool.Name)
			} else {
				entry.ToolsOff = append(entry.ToolsOff, tool.Name)
			}
		}
		if builtin, ok := captypes.Builtin(config.Command); config.IsBuiltin() && ok {
			for _, secret := range builtin.Secrets {
				status := "unset"
				if slices.Contains(snapshot.StoredSecrets, secret.Name) {
					status = "stored"
				}
				entry.Secrets = append(entry.Secrets, fmt.Sprintf("%s (%s): %s", secret.Name, secret.Label, status))
			}
		}
		view.Connections = append(view.Connections, entry)
	}
	sort.Slice(view.Connections, func(i, j int) bool { return view.Connections[i].ID < view.Connections[j].ID })
	for id := range snapshot.Settings.Skills {
		view.Skills = append(view.Skills, id)
	}
	sort.Strings(view.Skills)
	return view
}
