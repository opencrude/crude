package capability

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	captypes "crude/types/capability"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func accessFixture(t *testing.T) (*Service, captypes.RuntimeActor, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	service, settings, _ := capabilityFixture(t)
	calls, requests := new(atomic.Int32), new(atomic.Int32)
	server := mcp.NewServer(&mcp.Implementation{Name: "Access fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", Description: "Read evidence", InputSchema: map[string]any{"type": "object"}},
		func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "SDK access evidence"}}}, nil
		})
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true})
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler.ServeHTTP(w, r)
	}))
	t.Cleanup(endpoint.Close)
	t.Cleanup(service.Close)
	settings.Connections["docs"] = captypes.ConnectionConfig{ID: "docs", Name: "Documentation fixture", Source: "team", Scope: captypes.ScopeWorkspace, Transport: "http", URL: endpoint.URL}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	return service, captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}}, calls, requests
}

func observedAccess(t *testing.T, service *Service, actor captypes.RuntimeActor) map[string]any {
	t.Helper()
	result, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": "docs"})
	if err != nil {
		t.Fatal(err)
	}
	var activation captypes.ActivationResult
	if err := json.Unmarshal([]byte(result.Content[0].Text), &activation); err != nil {
		t.Fatal(err)
	}
	if activation.State != "connected" || len(activation.UsableTools) != 0 || len(activation.Tools) != 1 || !strings.Contains(activation.Next, "grants") {
		t.Fatalf("activation claimed access without a grant: %+v", activation)
	}
	return map[string]any{"connection_id": "docs", "grants": []captypes.AccessRequest{
		{Tool: "lookup", Fingerprint: activation.Tools[0].Fingerprint, Effect: captypes.EffectRead, Scope: "public test evidence"},
	}}
}

func TestMCPAgentAccessApprovalUseAndPassiveDurability(t *testing.T) {
	service, actor, calls, requests := accessFixture(t)
	params := observedAccess(t, service, actor)
	_, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params)
	var approval *captypes.AccessApprovalRequired
	if !errors.As(err, &approval) || calls.Load() != 0 {
		t.Fatalf("access self-authorized or executed early: %v calls=%d", err, calls.Load())
	}
	if len(service.Snapshot().Settings.Connections["docs"].Grants) != 0 {
		t.Fatal("unapproved grant became durable")
	}
	result, err := service.Execute(approval.CapabilityApprovalContext(t.Context()), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params)
	if err != nil {
		t.Fatal(err)
	}
	var activation captypes.ActivationResult
	if err := json.Unmarshal([]byte(result.Content[0].Text), &activation); err != nil || len(activation.UsableTools) != 1 {
		t.Fatalf("approved activation has no usable tool: %+v %v", activation, err)
	}
	prepared, err := service.Prepare(t.Context(), actor)
	if err != nil || len(prepared.Tools) != 1 {
		t.Fatalf("next request lacks exact dynamic definition: %+v %v", prepared, err)
	}
	evidence, err := service.Execute(t.Context(), actor, prepared, prepared.Tools[0].Name, map[string]any{})
	if err != nil || evidence.Content[0].Text != "SDK access evidence" || calls.Load() != 1 {
		t.Fatalf("real SDK use: %+v %v calls=%d", evidence, err, calls.Load())
	}
	settings := service.store.(*testRegistrationStore).load(t)
	service.Close()
	beforeRestart := requests.Load()
	reopened, err := New(service.root, service.cache, service.db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(reopened.Close)
	reopened.SetRegistrationStore(service.store)
	if err := reopened.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	prepared, err = reopened.Prepare(t.Context(), actor)
	if err != nil || len(prepared.Tools) != 0 || requests.Load() != beforeRestart || calls.Load() != 1 {
		t.Fatalf("passive reopen connected or replayed a tool: err=%v tools=%d requests=%d before=%d calls=%d", err, len(prepared.Tools), requests.Load(), beforeRestart, calls.Load())
	}
	if len(reopened.Snapshot().Settings.Connections["docs"].Grants) != 1 {
		t.Fatal("grant vanished after reopen")
	}
	if _, err := reopened.Activate(t.Context(), actor, "docs", false); err != nil {
		t.Fatal(err)
	}
	prepared, err = reopened.Prepare(t.Context(), actor)
	if err != nil || len(prepared.Tools) != 1 {
		t.Fatalf("durable grant unusable after explicit reactivation: %+v %v", prepared, err)
	}
}

func TestMCPAccessRejectsChangedFingerprintScopeAndCeilings(t *testing.T) {
	service, actor, calls, _ := accessFixture(t)
	params := observedAccess(t, service, actor)
	_, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params)
	var approval *captypes.AccessApprovalRequired
	if !errors.As(err, &approval) {
		t.Fatal(err)
	}
	grants := params["grants"].([]captypes.AccessRequest)
	changed := grants[0]
	changed.Scope = "different scope"
	altered := map[string]any{"connection_id": "docs", "grants": []captypes.AccessRequest{changed}}
	if _, err := service.Execute(approval.CapabilityApprovalContext(t.Context()), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", altered); err == nil {
		t.Fatal("approval widened to a different scope")
	}
	changed = grants[0]
	changed.Fingerprint = "guessed"
	altered["grants"] = []captypes.AccessRequest{changed}
	if _, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", altered); err == nil {
		t.Fatal("unobserved fingerprint accepted")
	}
	denied := actor
	denied.DeniedTools = []string{"mcp__docs__lookup"}
	if _, err := service.Execute(approval.CapabilityApprovalContext(t.Context()), denied, captypes.RuntimeSnapshot{}, "mcp_request_access", params); err == nil {
		t.Fatal("grant widened agent tool selection")
	}
	changed = grants[0]
	changed.Effect = captypes.EffectWrite
	altered["grants"] = []captypes.AccessRequest{changed}
	if _, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", altered); err == nil {
		t.Fatal("read-only agent granted write access")
	}
	if calls.Load() != 0 || len(service.Snapshot().Settings.Connections["docs"].Grants) != 0 {
		t.Fatal("rejected access executed or persisted")
	}
}

// TestMCPAccessGrantPreservesConnectionBinding is the regression for the grant
// scope rewrite: approving tool access must not silently move an
// application-bound connection into the workspace, which used to make it
// disappear from Application Settings and die with the project.
func TestMCPAccessGrantPreservesConnectionBinding(t *testing.T) {
	service, actor, _, _ := accessFixture(t)
	settings := service.Snapshot().Settings
	connection := settings.Connections["docs"]
	connection.Scope = captypes.ScopeApplication
	settings.Connections["docs"] = connection
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	params := observedAccess(t, service, actor)
	_, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params)
	var approval *captypes.AccessApprovalRequired
	if !errors.As(err, &approval) {
		t.Fatalf("access was not gated by exact approval: %v", err)
	}
	if _, err := service.Execute(approval.CapabilityApprovalContext(t.Context()), actor, captypes.RuntimeSnapshot{}, "mcp_request_access", params); err != nil {
		t.Fatal(err)
	}
	granted := service.Snapshot().Settings.Connections["docs"]
	if granted.Scope != captypes.ScopeApplication {
		t.Fatalf("grant rewrote the connection binding to %q", granted.Scope)
	}
	if len(granted.Grants) != 1 {
		t.Fatalf("grant was not persisted: %+v", granted.Grants)
	}
}

func TestInstalledSearchUsesLocalStateAndHonorsLimit(t *testing.T) {
	service, settings, source := capabilityFixture(t)
	settings.Skills["installed"] = captypes.SkillConfig{ID: "installed", Path: source, Name: "review"}
	var networkCalls atomic.Int32
	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		networkCalls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer broken.Close()
	settings.Sources["broken"] = captypes.SourceConfig{ID: "broken", Name: "Broken registry", Kind: "skill-registry", Location: broken.URL, Trusted: true}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), captypes.RuntimeActor{Effects: []captypes.Effect{captypes.EffectRead}}, captypes.RuntimeSnapshot{}, "capability_search",
		map[string]any{"kind": "skill", "query": "review", "limit": float64(1)})
	var entries []captypes.Entry
	if err != nil || json.Unmarshal([]byte(result.Content[0].Text), &entries) != nil || len(entries) != 1 || !entries[0].Installed || networkCalls.Load() != 0 {
		t.Fatalf("installed search queried network or ignored limit: %+v %v requests=%d", result, err, networkCalls.Load())
	}
}

func TestSkillActivationRequiresOnlyIdentityAndPublishesPath(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	settings.Acquisition.Mode = "automatic"
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Search(t.Context(), "review", captypes.KindSkill, 1, true); err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}},
		captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": "team:review"})
	if err != nil || len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, ` path="`) {
		t.Fatalf("identity-only skill activation did not publish its installed path: %+v %v", result, err)
	}
	if _, err := service.Execute(t.Context(), captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}},
		captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": "team:review", "grants": []any{}}); err == nil {
		t.Fatal("activation silently retained the rejected mixed grant/activation interface")
	}
}

func TestEmptyCapabilitySearchReturnsAnEmptyCollection(t *testing.T) {
	service, _, _ := capabilityFixture(t)
	result, err := service.Execute(t.Context(), captypes.RuntimeActor{Effects: []captypes.Effect{captypes.EffectRead}},
		captypes.RuntimeSnapshot{}, "capability_search", map[string]any{"kind": "mcp", "query": "missing"})
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != "[]" {
		t.Fatalf("empty discovery was ambiguous null data: %+v %v", result, err)
	}
}
