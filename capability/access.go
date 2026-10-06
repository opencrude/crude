package capability

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"

	captypes "crude/types/capability"
)

func (service *Service) activationResult(ctx context.Context, actor captypes.RuntimeActor, id string) (captypes.Result, error) {
	prepared, err := service.Prepare(ctx, actor)
	if err != nil {
		return captypes.Result{}, err
	}
	result := captypes.ActivationResult{ConnectionID: id, State: "connected", Notices: prepared.Notices}
	budgets := service.Snapshot().Settings.Acquisition
	for _, state := range service.clients.States() {
		if state.ID == id {
			result.State = state.State
			schemaBytes := 0
			for _, tool := range state.Tools {
				size := len(tool.InputSchema) + len(tool.Description)
				if len(result.Tools) >= budgets.MaxTools || schemaBytes+size > min(budgets.MaxSchemaBytes, 256<<10) {
					result.Notices = append(result.Notices, "Observed tool details reached the configured count/schema budget; the full catalog remains available in MCP Settings.")
					break
				}
				result.Tools = append(result.Tools, tool)
				schemaBytes += size
			}
		}
	}
	for _, tool := range prepared.Tools {
		if tool.ConnectionID == id {
			// Schemas are already offered through the request snapshot. Avoid
			// repeating that potentially large budget in a tool result.
			tool.InputSchema, tool.Description = nil, ""
			result.UsableTools = append(result.UsableTools, tool)
		}
	}
	if len(result.UsableTools) > 0 {
		result.Next = "Use the exact UsableTools names offered at the next request boundary. Installation and grants remain durable at their configured binding."
	} else {
		result.Next = "Connected does not mean tool access. Request exact observed tools using mcp_request_access connection_id plus grants [{tool: original name, fingerprint: observed fingerprint, effect: intended effect, scope: resource scope}]. New or changed grants require explicit approval; agent ceilings and schema budgets still apply."
	}
	encoded, err := json.Marshal(result)
	return textResult(string(encoded)), err
}

func decodeAccessRequests(raw any) ([]captypes.AccessRequest, error) {
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var grants []captypes.AccessRequest
	if err := decoder.Decode(&grants); err != nil {
		return nil, fmt.Errorf("invalid MCP access request: %w", err)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return nil, errors.New("invalid trailing MCP access data")
	}
	if len(grants) == 0 || len(grants) > 32 {
		return nil, errors.New("select between one and 32 exact observed MCP tools")
	}
	return grants, nil
}

func (service *Service) accessPreview(actor captypes.RuntimeActor, id string, requests []captypes.AccessRequest) (captypes.AccessPreview, captypes.Settings, bool, error) {
	snapshot := service.Snapshot()
	connection, exists := snapshot.Settings.Connections[id]
	if !exists || connection.Disabled {
		return captypes.AccessPreview{}, snapshot.Settings, false, errors.New("installed MCP connection is unavailable")
	}
	var state captypes.ConnectionState
	for _, candidate := range snapshot.Connections {
		if candidate.ID == id {
			state = candidate
		}
	}
	if state.State != "connected" || state.Cached {
		return captypes.AccessPreview{}, snapshot.Settings, false, errors.New("refresh live MCP tool discovery before requesting access")
	}
	preview := captypes.AccessPreview{Connection: connection, Grants: slices.Clone(requests)}
	seen := make(map[string]bool)
	changed := false
	schemaBytes := 0
	for _, request := range requests {
		if request.Tool == "" || request.Scope == "" || len(request.Scope) > 1024 || seen[request.Tool] {
			return preview, snapshot.Settings, false, errors.New("MCP access needs unique tool names and a bounded nonempty resource scope")
		}
		seen[request.Tool] = true
		var observed captypes.Tool
		found := false
		for _, tool := range state.Tools {
			if tool.OriginalName == request.Tool {
				observed, found = tool, true
				break
			}
		}
		if !found || request.Fingerprint == "" || request.Fingerprint != observed.Fingerprint {
			return preview, snapshot.Settings, false, errors.New("MCP access request does not match an exact observed tool fingerprint")
		}
		schemaBytes += len(observed.InputSchema) + len(observed.Description)
		if schemaBytes > snapshot.Settings.Acquisition.MaxSchemaBytes {
			return preview, snapshot.Settings, false, errors.New("selected MCP access schemas exceed the configured review budget; request a smaller exact tool set")
		}
		if !slices.Contains([]captypes.Effect{captypes.EffectRead, captypes.EffectWrite, captypes.EffectDelete, captypes.EffectExecute, captypes.EffectCommunication}, request.Effect) ||
			!actorAllows(actor, observed.Name, request.Effect) {
			return preview, snapshot.Settings, false, errors.New("MCP access would exceed this agent's tool or effect ceiling")
		}
		preview.Tools = append(preview.Tools, observed)
		expected := captypes.ToolGrant{Fingerprint: request.Fingerprint, Effect: request.Effect, Scope: request.Scope}
		if connection.Grants[request.Tool] != expected {
			changed = true
		}
	}
	// Bind approval to the producing endpoint/configuration, live schemas, exact
	// requested grants and current acquisition policy, never only a boolean.
	encoded, err := json.Marshal(struct {
		Preview captypes.AccessPreview
		Policy  captypes.AcquisitionSettings
		Source  captypes.SourceConfig
	}{preview, snapshot.Settings.Acquisition, snapshot.Settings.Sources[connection.Source]})
	if err != nil {
		return preview, snapshot.Settings, false, err
	}
	digest := sha256.Sum256(encoded)
	preview.Token = hex.EncodeToString(digest[:])
	return preview, snapshot.Settings, changed, nil
}

func (service *Service) activateAccess(ctx context.Context, actor captypes.RuntimeActor, id string, raw any) (captypes.Result, error) {
	requests, err := decodeAccessRequests(raw)
	if err != nil {
		return captypes.Result{}, err
	}
	service.mu.RLock()
	_, installed := service.settings.Connections[id]
	service.mu.RUnlock()
	if !installed {
		return captypes.Result{}, errors.New("install and activate the MCP connection first to observe its exact tool fingerprints")
	}
	service.mu.RLock()
	boundScope := service.settings.Connections[id].Scope
	service.mu.RUnlock()
	if _, err := service.ActivateScoped(ctx, actor, id, false, boundScope); err != nil {
		return captypes.Result{}, err
	}
	preview, before, changed, err := service.accessPreview(actor, id, requests)
	if err != nil {
		return captypes.Result{}, err
	}
	if !changed {
		return service.activationResult(ctx, actor, id)
	}
	approved, exists := ctx.Value(captypes.AccessApprovedKey{}).(captypes.AccessPreview)
	if !exists {
		return captypes.Result{}, &captypes.AccessApprovalRequired{Preview: preview}
	}
	if approved.Token == "" || approved.Token != preview.Token {
		return captypes.Result{}, errors.New("MCP connection, schema or requested access changed; request a fresh exact approval")
	}
	after := captypes.Clone(before)
	connection := after.Connections[id]
	// Grants belong to the connection's own binding. Rewriting the scope here
	// used to silently move an application-bound connection into the workspace,
	// so a first tool grant made it disappear from Application Settings and
	// destroyed it with the project.
	if connection.Scope != captypes.ScopeWorkspace {
		connection.Scope = captypes.ScopeApplication
	}
	if connection.Grants == nil {
		connection.Grants = make(map[string]captypes.ToolGrant)
	}
	for _, request := range requests {
		connection.Grants[request.Tool] = captypes.ToolGrant{Effect: request.Effect, Scope: request.Scope, Fingerprint: request.Fingerprint}
	}
	after.Connections[id] = connection
	if err := service.publishRegistration(ctx, id, before, after); err != nil {
		return captypes.Result{}, err
	}
	// Publication changes transport configuration. Reconnect explicitly as part
	// of this activation, never during passive restart or a presentation read.
	return service.Activate(ctx, actor, id, false)
}
