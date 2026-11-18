package capability

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"strings"

	captypes "crude/types/capability"
)

var environmentName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

const (
	maxEnvironmentEntries = 64
	maxEnvironmentValue   = 8 << 10
)

// decodeEnvironment validates the agent-supplied launch environment. An empty
// value removes that variable from the connection's explicit environment.
func decodeEnvironment(raw any) (map[string]string, error) {
	if raw == nil {
		return nil, nil
	}
	values, ok := raw.(map[string]any)
	if !ok {
		return nil, errors.New("environment must be an object of variable names to string values")
	}
	// Providers that force every schema property send an empty object.
	if len(values) == 0 {
		return nil, nil
	}
	if len(values) > maxEnvironmentEntries {
		return nil, fmt.Errorf("environment accepts at most %d variables", maxEnvironmentEntries)
	}
	environment := make(map[string]string, len(values))
	for name, value := range values {
		text, ok := value.(string)
		if !ok {
			return nil, fmt.Errorf("environment value for %q must be a string", name)
		}
		if !environmentName.MatchString(name) {
			return nil, fmt.Errorf("environment name %q is not a portable variable name", name)
		}
		if len(text) > maxEnvironmentValue || strings.ContainsRune(text, 0) {
			return nil, fmt.Errorf("environment value for %q is oversized or contains NUL", name)
		}
		environment[name] = text
	}
	return environment, nil
}

// configureEnvironment applies one exact, authorized change to an installed
// local server's launch environment. The process already inherits the host
// environment, so this is only for server-specific settings. Approval follows
// the session's Manual/Auto Review/YOLO policy like acquisition, publishes a
// durable registration change against its reviewed preimage, and authorizes
// launching the resulting configuration for this run. Credential references
// remain user-owned and cannot be shadowed.
func (service *Service) configureEnvironment(ctx context.Context, actor captypes.RuntimeActor, id string, requested map[string]string) error {
	service.mu.RLock()
	before := captypes.Clone(service.settings)
	service.mu.RUnlock()
	config, exists := before.Connections[id]
	if !exists || config.Transport != "stdio" {
		return errors.New("environment applies only to an installed local (stdio) MCP connection")
	}
	if config.ExecutionHost() != "core" {
		return errors.New("environment bytes are not forwarded to another execution host; configure that host's environment instead")
	}
	if !actorAllows(actor, "capability_activate", captypes.EffectExecute) {
		return errors.New("local MCP host execution exceeds the agent effect ceiling")
	}
	after := config
	after.Environment = maps.Clone(config.Environment)
	if after.Environment == nil {
		after.Environment = make(map[string]string)
	}
	for name, value := range requested {
		if _, credential := config.SecretRefs[name]; credential {
			return fmt.Errorf("%s is a user-owned credential reference; it cannot be replaced by a plain environment value", name)
		}
		if value == "" {
			delete(after.Environment, name)
		} else {
			after.Environment[name] = value
		}
	}
	if len(after.Environment) == 0 {
		after.Environment = nil
	}
	if reflect.DeepEqual(after.Environment, config.Environment) {
		return nil
	}
	preview := captypes.ConfigurationPreview{Connection: after, Environment: requested}
	preview.Token = previewToken(struct {
		Before, After captypes.ConnectionConfig
		Policy        captypes.AcquisitionSettings
		Source        captypes.SourceConfig
		Actor         captypes.RuntimeActor
	}{config, after, before.Acquisition, before.Sources[config.Source], actor})
	approved, ok := ctx.Value(captypes.ConfigurationApprovedKey{}).(captypes.ConfigurationPreview)
	if !ok {
		return &captypes.ConfigurationApprovalRequired{Preview: preview, Actor: actor}
	}
	if approved.Token != preview.Token {
		return errors.New("MCP connection or requested environment changed after authorization; request a fresh exact approval")
	}
	origin := "human"
	if authorization, exists := captypes.AuthorizationFrom(ctx); exists {
		if authorization.Token != preview.Token || !reflect.DeepEqual(authorization.Actor, actor) {
			return errors.New("MCP configuration authorization owner or run changed")
		}
		origin = authorization.Origin
	}
	if actor.RunID == "" {
		return errors.New("MCP configuration requires an active run identity")
	}
	request := &captypes.ConfigurationApprovalRequired{Preview: preview, Actor: actor}
	if err := service.recordAuthorization(ctx, actor, origin, "configuration", preview.Token, request.Evidence()); err != nil {
		return err
	}
	next := captypes.Clone(before)
	next.Connections[id] = after
	if err := service.publishRegistration(ctx, id, before, next); err != nil {
		return err
	}
	// Publication revoked run-local launch authority. The approval covered
	// launching exactly this configuration, so grant it for the run again.
	service.mu.RLock()
	published := captypes.Clone(service.settings)
	service.mu.RUnlock()
	if current, exists := published.Connections[id]; exists && reflect.DeepEqual(current.Environment, after.Environment) {
		service.grantLaunch(actor, id, launchToken(current, published, actor))
	}
	return nil
}
