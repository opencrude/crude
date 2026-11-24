package capability

import (
	"context"
	"errors"
	"sync"
	"testing"

	captypes "crude/types/capability"
)

type lifecycleAuthority struct {
	mu     sync.Mutex
	actor  captypes.RuntimeActor
	active bool
}

func (authority *lifecycleAuthority) ValidateRuntime(ctx context.Context, actor captypes.RuntimeActor) error {
	authority.mu.Lock()
	defer authority.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if !authority.active || actor.RunID != authority.actor.RunID || actor.RuntimeEpoch != authority.actor.RuntimeEpoch {
		return errors.New("expired durable run")
	}
	return nil
}

func TestDurableAuthorityRejectsTerminalReprepareAndStaleReplacement(t *testing.T) {
	service, _, _ := capabilityFixture(t)
	actor := captypes.RuntimeActor{ID: "root", SessionID: "session", RunID: "durable-1", RuntimeEpoch: 3, TurnGeneration: 7, ExecutionRoot: t.TempDir(), Effects: []captypes.Effect{captypes.EffectRead}}
	authority := &lifecycleAuthority{actor: actor, active: true}
	service.SetRuntimeAuthority(authority)
	if _, err := service.Prepare(t.Context(), actor); err != nil {
		t.Fatal(err)
	}
	authority.mu.Lock()
	authority.active = false
	authority.mu.Unlock()
	service.RevokeRun(actor)
	if _, err := service.Prepare(t.Context(), actor); err == nil {
		t.Fatal("settled run resurrected through Prepare")
	}
	for _, operation := range []string{"capability_search", "capability_activate", "mcp_request_access", "mcp_read_resource", "mcp_get_prompt", "mcp__fixture__lookup"} {
		if _, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, operation, nil); err == nil {
			t.Fatalf("terminal owner executed %s", operation)
		}
	}
	next := actor
	next.RunID = "durable-2"
	authority.mu.Lock()
	authority.active = true
	authority.actor = next
	authority.mu.Unlock()
	if _, err := service.Prepare(t.Context(), next); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Prepare(t.Context(), actor); err == nil {
		t.Fatal("old owner replaced the new authority")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Prepare(ctx, next); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled preparation: %v", err)
	}
	service.RevokeRun(next)
	service.mu.RLock()
	defer service.mu.RUnlock()
	if len(service.taskGrants)+len(service.launches)+len(service.executionClients)+len(service.runs) != 0 {
		t.Fatal("settlement retained ephemeral authorization or transports")
	}
}

func TestConcurrentExpiredPreparationCannotRestoreTaskAuthority(t *testing.T) {
	service, _, _ := capabilityFixture(t)
	actor := captypes.RuntimeActor{ID: "root", RunID: "run", RuntimeEpoch: 1}
	authority := &lifecycleAuthority{actor: actor, active: true}
	service.SetRuntimeAuthority(authority)
	if _, err := service.Prepare(t.Context(), actor); err != nil {
		t.Fatal(err)
	}
	authority.mu.Lock()
	authority.active = false
	authority.mu.Unlock()
	service.RevokeRun(actor)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := service.Prepare(t.Context(), actor); err == nil {
				t.Error("expired concurrent preparation succeeded")
			}
		}()
	}
	wg.Wait()
	service.mu.RLock()
	defer service.mu.RUnlock()
	if len(service.runs) != 0 {
		t.Fatal("expired preparation recreated run cache")
	}
}

func TestExecutionClientsRemainIsolatedAndPolicyPublicationRevokesTaskTrust(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	first := captypes.RuntimeActor{ID: "first", RunID: "run-first", ExecutionRoot: t.TempDir()}
	second := captypes.RuntimeActor{ID: "second", RunID: "run-second", ExecutionRoot: t.TempDir()}
	if service.actorClients(first) == service.actorClients(second) {
		t.Fatal("distinct execution roots shared a transport owner")
	}
	if len(service.actorClients(first).States()) != 0 || len(service.actorClients(second).States()) != 0 {
		t.Fatal("passive actor preparation connected a transport")
	}
	settings.Connections["fixture"] = captypes.ConnectionConfig{ID: "fixture", Name: "Fixture", Transport: "http", URL: "https://example.org/mcp"}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if err := service.admitRun(t.Context(), first); err != nil {
		t.Fatal(err)
	}
	authorized := service.Snapshot().Settings
	service.mu.Lock()
	service.taskGrants[taskKey(first)] = map[string]taskGrant{"fixture\x00lookup": {Connection: authorized.Connections["fixture"],
		Policy: authorized.Acquisition, Source: authorized.Sources[""], Grant: captypes.ToolGrant{Effect: captypes.EffectRead, Scope: "fixture"}}}
	service.mu.Unlock()
	granted := func() bool {
		_, ok := service.taskAccess(first, service.Snapshot().Settings.Connections["fixture"], "lookup")
		return ok
	}
	launch := launchToken(authorized.Connections["fixture"], authorized, first)
	if !granted() {
		t.Fatal("run grant was not usable under the settings that authorized it")
	}
	// Removing or adding another connection publishes settings too; it must
	// not take tools away from a run that was granted them.
	settings.Connections["other"] = captypes.ConnectionConfig{ID: "other", Name: "Other", Transport: "http", URL: "https://example.org/other"}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if !granted() {
		t.Fatal("an unrelated connection change revoked the run's grant")
	}
	settings.Acquisition.AutomaticSelection = "off"
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if granted() || launchToken(authorized.Connections["fixture"], service.Snapshot().Settings, first) == launch {
		t.Fatal("policy revocation retained task authorization")
	}
	service.RevokeRun(first)
	service.RevokeRun(second)
}
