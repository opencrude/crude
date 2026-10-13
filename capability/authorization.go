package capability

import (
	"context"
	"crude/mcpclient"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	captypes "crude/types/capability"
)

type taskGrant struct {
	Connection captypes.ConnectionConfig
	Grant      captypes.ToolGrant
}

func actorKey(actor captypes.RuntimeActor) string { return actor.SessionID + "\x00" + actor.ID }
func taskKey(actor captypes.RuntimeActor) string {
	shell := ""
	if actor.ShellOwner != nil {
		shell = fmt.Sprintf("/%s/%d", actor.ShellOwner.ClientID, actor.ShellOwner.Epoch)
	}
	return actorKey(actor) + "\x00" + actor.RunID + fmt.Sprintf("/%d/%d", actor.RuntimeEpoch, actor.TurnGeneration) + "\x00" + actor.ExecutionRoot + "\x00" + actor.WorkspaceID + shell
}

func (service *Service) taskAccess(actor captypes.RuntimeActor, config captypes.ConnectionConfig, name string) (captypes.ToolGrant, bool) {
	service.mu.RLock()
	defer service.mu.RUnlock()
	if actor.RunID == "" || service.runs[actorKey(actor)] != taskKey(actor) {
		return captypes.ToolGrant{}, false
	}
	grant, ok := service.taskGrants[taskKey(actor)][config.ID+"\x00"+name]
	return grant.Grant, ok && reflect.DeepEqual(grant.Connection, config)
}

func previewToken(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (service *Service) recordAuthorization(ctx context.Context, actor captypes.RuntimeActor, origin, phase, token string, evidence any) error {
	if service.db == nil {
		return errors.New("capability authorization audit storage is unavailable")
	}
	// Audit facts bind authorization without retaining externally authored schema
	// prose, source instructions, launch environment or credential material.
	facts := make(map[string]any)
	if fields, ok := evidence.(map[string]any); ok {
		for _, key := range []string{"source", "revision", "digest", "kind", "binding", "effect", "host", "scope", "grants"} {
			if value, exists := fields[key]; exists {
				facts[key] = value
			}
		}
		if config, ok := fields["connection"].(captypes.ConnectionConfig); ok {
			facts["connection"] = config.ID
			facts["revision"] = config.Version
			facts["source"] = config.Source
		}
	}
	encoded, err := json.Marshal(facts)
	if err != nil {
		return err
	}
	_, err = service.db.ExecContext(ctx, `INSERT INTO capability_authorizations(owner_id,session_id,run_id,origin,phase,token,evidence,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		actor.ID, actor.SessionID, actor.RunID, origin, phase, token, string(encoded), time.Now().UnixMilli())
	return err
}

func (service *Service) connectActor(ctx context.Context, actor captypes.RuntimeActor, config captypes.ConnectionConfig, settings captypes.Settings, manual bool) error {
	if config.Disabled {
		return errors.New("MCP connection is disabled")
	}
	if missing := missingConnectionEnvironment(config); len(missing) != 0 {
		return fmt.Errorf("MCP connection needs required environment: %s. Supply values you have through capability_activate environment; ask the user only for credentials you do not have", strings.Join(missing, ", "))
	}
	if config.Source != "" {
		source, exists := settings.Sources[config.Source]
		if !exists || source.Disabled || !source.Trusted {
			return errors.New("MCP source is unavailable or untrusted")
		}
	}
	if config.Transport != "stdio" || manual {
		return service.actorClients(actor).Connect(ctx, config.ID)
	}
	if !actorAllows(actor, "capability_activate", captypes.EffectExecute) {
		return errors.New("local MCP host execution exceeds the agent effect ceiling")
	}
	clients := service.actorClients(actor)
	// Reconfiguration disconnects a changed connection, so a live session runs
	// this exact configuration. Reusing it launches nothing; tool access still
	// requires its own grants.
	for _, state := range clients.States() {
		if state.ID == config.ID && state.State == "connected" {
			return nil
		}
	}
	if !config.LaunchApproved {
		if err := service.authorizeLaunch(ctx, actor, config, settings); err != nil {
			return err
		}
	}
	prepared, err := service.prepareLaunch(ctx, config)
	if err != nil {
		return err
	}
	if config.LaunchApproved {
		err = clients.Connect(ctx, config.ID)
	} else {
		err = clients.ConnectAuthorized(ctx, config.ID, config)
	}
	if err == nil {
		service.markPrepared(prepared)
	}
	return err
}

// authorizeLaunch admits one exact local launch for the owning run. A recorded
// authorization survives a failed connection or a package preparation in the
// same run, so retrying does not ask again; a new run or any configuration,
// policy, source or actor change requires a fresh authorization.
func (service *Service) authorizeLaunch(ctx context.Context, actor captypes.RuntimeActor, config captypes.ConnectionConfig, settings captypes.Settings) error {
	preview := captypes.LaunchPreview{Connection: config, Actor: actor, Token: launchToken(config, settings, actor)}
	service.mu.RLock()
	previous := service.launches[taskKey(actor)][config.ID]
	service.mu.RUnlock()
	if actor.RunID != "" && previous == preview.Token {
		return nil
	}
	approved, ok := ctx.Value(captypes.LaunchApprovedKey{}).(captypes.LaunchPreview)
	if !ok {
		return &captypes.LaunchApprovalRequired{Preview: preview}
	}
	if approved.Token != preview.Token || !reflect.DeepEqual(approved.Actor, actor) {
		return errors.New("MCP launch preimage or actor changed after authorization")
	}
	origin := "human"
	if auth, exists := captypes.AuthorizationFrom(ctx); exists {
		origin = auth.Origin
	}
	if actor.RunID == "" {
		return errors.New("MCP task launch requires an active run identity")
	}
	request := &captypes.LaunchApprovalRequired{Preview: preview}
	if err := service.recordAuthorization(ctx, actor, origin, "launch", preview.Token, request.Evidence()); err != nil {
		return err
	}
	service.grantLaunch(actor, config.ID, preview.Token)
	return nil
}

// launchToken binds a launch authorization to the exact configuration, policy,
// source and owning run.
func launchToken(config captypes.ConnectionConfig, settings captypes.Settings, actor captypes.RuntimeActor) string {
	return previewToken(struct {
		Config captypes.ConnectionConfig
		Policy captypes.AcquisitionSettings
		Source captypes.SourceConfig
		Actor  captypes.RuntimeActor
	}{config, settings.Acquisition, settings.Sources[config.Source], actor})
}

func (service *Service) grantLaunch(actor captypes.RuntimeActor, id, token string) {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.launches[taskKey(actor)] == nil {
		service.launches[taskKey(actor)] = make(map[string]string)
	}
	service.launches[taskKey(actor)][id] = token
}

func (service *Service) SetRuntimeAuthority(authority captypes.RuntimeAuthority) {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.authority = authority
}

func (service *Service) validateRuntime(ctx context.Context, actor captypes.RuntimeActor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service.mu.RLock()
	authority, closed := service.authority, service.closed
	service.mu.RUnlock()
	if closed {
		return errors.New("capability service is closed")
	}
	// Standalone services may be used as direct fixtures. The workspace always
	// installs its durable authority before exposing a product runtime.
	if authority != nil {
		return authority.ValidateRuntime(ctx, actor)
	}
	return nil
}

func (service *Service) actorClients(actor captypes.RuntimeActor) actorClient {
	local := service.localActorClients(actor)
	service.mu.RLock()
	shell := false
	for _, config := range service.settings.Connections {
		shell = shell || config.Host == "shell"
	}
	service.mu.RUnlock()
	if !shell {
		return local
	}
	return &routedClient{s: service, actor: actor, local: local}
}

func (service *Service) localActorClients(actor captypes.RuntimeActor) *mcpclient.Manager {
	if actor.ExecutionRoot == "" || actor.ExecutionRoot == service.root {
		return service.clients
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	key := taskKey(actor)
	clients := service.executionClients[key]
	if clients == nil {
		clients = mcpclient.New(actor.ExecutionRoot)
		clients.SetCredentialDirectory(filepath.Join(filepath.Dir(service.cache), "credentials"))
		clients.Configure(service.settings.Connections)
		service.executionClients[key] = clients
	}
	return clients
}

func (service *Service) actorSnapshot(actor captypes.RuntimeActor) captypes.Snapshot {
	snapshot := service.Snapshot()
	snapshot.Connections = service.actorClients(actor).States()
	return snapshot
}

func (service *Service) RevokeRun(actor captypes.RuntimeActor) {
	service.revokeHostRun(actor)
	service.mu.Lock()
	key := taskKey(actor)
	delete(service.taskGrants, key)
	delete(service.launches, key)
	delete(service.hostStates, key)
	if service.runs[actorKey(actor)] == key {
		delete(service.runs, actorKey(actor))
	}
	clients := service.executionClients[key]
	delete(service.executionClients, key)
	service.mu.Unlock()
	if clients != nil {
		clients.Close()
	}
}
