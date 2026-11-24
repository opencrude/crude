package capability

import (
	"context"
	"crude/agent/mcpclient"
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
	"crude/types/command"
)

func actorKey(actor captypes.RuntimeActor) string { return actor.SessionID + "\x00" + actor.ID }
func taskKey(actor captypes.RuntimeActor) string {
	shell := ""
	if actor.ShellOwner != nil {
		shell = fmt.Sprintf("/%s/%d", actor.ShellOwner.ClientID, actor.ShellOwner.Epoch)
	}
	return actorKey(actor) + "\x00" + actor.RunID + fmt.Sprintf("/%d/%d", actor.RuntimeEpoch, actor.TurnGeneration) + "\x00" + actor.ExecutionRoot + "\x00" + actor.WorkspaceID + shell
}

func previewToken(value any) string {
	encoded, _ := json.Marshal(value)
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])
}

func (s *Service) recordAuthorization(ctx context.Context, actor captypes.RuntimeActor, origin, phase, token string, evidence any) error {
	if s.db == nil {
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
	_, err = s.db.ExecContext(ctx, `INSERT INTO capability_authorizations(owner_id,session_id,run_id,origin,phase,token,evidence,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		actor.ID, actor.SessionID, actor.RunID, origin, phase, token, string(encoded), time.Now().UnixMilli())
	return err
}

func (s *Service) activateConnection(ctx context.Context, actor captypes.RuntimeActor, entry captypes.Entry, config captypes.ConnectionConfig, settings captypes.Settings, manual bool) (captypes.Result, error) {
	if config.Disabled {
		return captypes.Result{}, errors.New("MCP connection is disabled")
	}
	if missing := mcpclient.MissingConfiguration(config); len(missing) != 0 {
		return captypes.Result{}, fmt.Errorf("MCP connection is installed but needs setup: %s. Configure headers or credential references in Settings; local environment values can also be supplied through capability_activate", strings.Join(missing, ", "))
	}
	if config.Source != "" {
		source, exists := settings.Sources[config.Source]
		if !exists || source.Disabled || !source.Trusted {
			return captypes.Result{}, errors.New("MCP source is unavailable or untrusted")
		}
	}
	clients := s.actorClients(actor)
	if config.Transport == "stdio" && !manual && !actorAllows(actor, "capability_activate", captypes.EffectExecute) {
		return captypes.Result{}, errors.New("local MCP host execution exceeds the agent effect ceiling")
	}
	// A running server has nothing left to start: report its observed tools in
	// this result instead of scheduling a job that would only reconnect it.
	for _, state := range clients.Catalog() {
		if state.ID == config.ID && state.State == "connected" && !state.Cached {
			return s.finishConnection(ctx, actor, entry, clients)
		}
	}
	if config.Transport == "stdio" && !manual && !config.LaunchApproved {
		if err := s.authorizeLaunch(ctx, actor, config, settings); err != nil {
			return captypes.Result{}, err
		}
	}
	return captypes.RunActivation(ctx, entry.Name, func(ctx context.Context) (captypes.Result, error) {
		captypes.ReportProgress(ctx, command.PhaseConnecting, 0, 0)
		var err error
		if config.Transport == "stdio" && !manual {
			err = clients.ConnectAuthorized(ctx, config.ID, config)
		} else {
			err = clients.Connect(ctx, config.ID)
		}
		if err != nil {
			return captypes.Result{}, err
		}
		return s.finishConnection(ctx, actor, entry, clients)
	})
}

func (s *Service) finishConnection(ctx context.Context, actor captypes.RuntimeActor, entry captypes.Entry, clients actorClient) (captypes.Result, error) {
	if err := s.bind(ctx, actor.ID, entry); err != nil {
		return captypes.Result{}, err
	}
	return s.observedActivation(ctx, actor, entry.ID, clients)
}

// authorizeLaunch admits one exact local launch for the owning run. A recorded
// authorization survives a failed connection in the same run, so retrying does not ask again; a new run or any configuration,
// policy, source or actor change requires a fresh authorization.
func (s *Service) authorizeLaunch(ctx context.Context, actor captypes.RuntimeActor, config captypes.ConnectionConfig, settings captypes.Settings) error {
	preview := captypes.LaunchPreview{Connection: config, Actor: actor, Token: launchToken(config, settings, actor)}
	s.mu.RLock()
	previous := s.launches[taskKey(actor)][config.ID]
	s.mu.RUnlock()
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
	if err := s.recordAuthorization(ctx, actor, origin, "launch", preview.Token, request.Evidence()); err != nil {
		return err
	}
	s.grantLaunch(actor, config.ID, preview.Token)
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

func (s *Service) grantLaunch(actor captypes.RuntimeActor, id, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.launches[taskKey(actor)] == nil {
		s.launches[taskKey(actor)] = make(map[string]string)
	}
	s.launches[taskKey(actor)][id] = token
}

// SetRuntimeAuthority records the authority that later runtime calls are checked against.
func (s *Service) SetRuntimeAuthority(authority captypes.RuntimeAuthority) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authority = authority
}

func (s *Service) validateRuntime(ctx context.Context, actor captypes.RuntimeActor) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.RLock()
	authority, closed := s.authority, s.closed
	s.mu.RUnlock()
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

func (s *Service) actorClients(actor captypes.RuntimeActor) actorClient {
	local := s.localActorClients(actor)
	s.mu.RLock()
	shell := false
	for _, config := range s.settings.Connections {
		shell = shell || config.Host == "shell"
	}
	s.mu.RUnlock()
	if !shell {
		return local
	}
	return &routedClient{s: s, actor: actor, local: local}
}

func (s *Service) localActorClients(actor captypes.RuntimeActor) *mcpclient.Manager {
	if actor.ExecutionRoot == "" || actor.ExecutionRoot == s.root {
		return s.clients
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := taskKey(actor)
	clients := s.executionClients[key]
	if clients == nil {
		clients = s.newMCPManager(actor.ExecutionRoot)
		clients.SetCredentialDirectory(filepath.Join(filepath.Dir(s.cache), "credentials"))
		clients.Configure(s.settings.Connections)
		s.executionClients[key] = clients
	}
	return clients
}

// RevokeRun drops the launches and clients that belong to one actor's run.
func (s *Service) RevokeRun(actor captypes.RuntimeActor) {
	s.revokeHostRun(actor)
	s.mu.Lock()
	key := taskKey(actor)
	delete(s.launches, key)
	delete(s.hostStates, key)
	if s.runs[actorKey(actor)] == key {
		delete(s.runs, actorKey(actor))
	}
	clients := s.executionClients[key]
	delete(s.executionClients, key)
	s.mu.Unlock()
	if clients != nil {
		clients.Close()
	}
}
