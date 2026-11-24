package capability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

// A person may classify a tool for a restricted agent. The live switch,
// not a reconnect or an old request snapshot, governs its next call.
func TestToolSwitchAndClassificationApplyToLiveRequests(t *testing.T) {
	s, a, calls, requests := accessFixture(t)
	a.RunID = "live"
	if _, err := s.Activate(t.Context(), a, "docs", false); err != nil {
		t.Fatal(err)
	}
	snapshot := s.Snapshot()
	tool := snapshot.Connections[0].Tools[0]
	cfg := snapshot.Settings.Connections["docs"]
	rule := captypes.ToolGrant{Effect: captypes.EffectRead, Scope: "docs", Fingerprint: tool.Fingerprint}
	set := func(rule captypes.ToolGrant) {
		cfg.Grants = map[string]captypes.ToolGrant{"lookup": rule}
		snapshot.Settings.Connections["docs"] = cfg
		before := requests.Load()
		if err := s.Configure(t.Context(), snapshot.Settings); err != nil {
			t.Fatal(err)
		}
		if state := s.Snapshot().Connections[0]; state.State != "connected" || state.Cached || before != requests.Load() {
			t.Fatal("rule edit restarted a live server")
		}
	}
	set(rule)
	prepared, err := s.Prepare(t.Context(), a)
	if err != nil || len(prepared.Tools) != 1 {
		t.Fatal("human classification was not offered", err)
	}
	if _, err = s.Execute(t.Context(), a, prepared, tool.Name, nil); err != nil {
		t.Fatal(err)
	}
	denied := a
	denied.DeniedTools = []string{tool.Name}
	if _, err = s.Execute(t.Context(), denied, prepared, tool.Name, nil); err == nil {
		t.Fatal("explicit tool denial ignored")
	}
	rule.Disabled = true
	set(rule)
	if _, err = s.Execute(t.Context(), a, prepared, tool.Name, nil); err == nil {
		t.Fatal("disabled tool reached the server")
	}
	rule.Disabled = false
	rule.Fingerprint = "old schema"
	set(rule)
	if p, err := s.Prepare(t.Context(), a); err != nil || len(p.Tools) != 0 {
		t.Fatal("changed schema inherited old classification")
	}
	if calls.Load() != 1 {
		t.Fatal("stale call executed")
	}
}

func TestClassifiedConnectionReopensPassivelyAndReconnectsOnUse(t *testing.T) {
	s, a, calls, requests := accessFixture(t)
	if err := s.Connect(t.Context(), "docs"); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	settings := snap.Settings
	cfg := settings.Connections["docs"]
	cfg.Grants = map[string]captypes.ToolGrant{"lookup": {Effect: captypes.EffectRead, Scope: "docs", Fingerprint: snap.Connections[0].Tools[0].Fingerprint}}
	settings.Connections["docs"] = cfg
	s.Close()
	before := requests.Load()
	reopened, err := New(s.root, s.cache, s.db)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err = reopened.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != before {
		t.Fatal("passive reopen contacted server")
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		p, err := reopened.Prepare(t.Context(), a)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Tools) == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("configured tool never returned")
		}
		time.Sleep(time.Millisecond * 10)
	}
	if calls.Load() != 0 {
		t.Fatal("reopen replayed a call")
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
	var found captypes.SearchResult
	if err != nil || json.Unmarshal([]byte(result.Content[0].Text), &found) != nil || len(found.Results) != 1 || !found.Results[0].Installed || networkCalls.Load() != 0 {
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
	if err != nil || len(result.Content) != 1 || result.Content[0].Text != `{"results":[]}` {
		t.Fatalf("empty discovery was ambiguous null data: %+v %v", result, err)
	}
}

func TestConnectedToolsFollowAgentPolicyAcrossOrdinaryTurns(t *testing.T) {
	s, a, calls, _ := accessFixture(t)
	a.SessionID, a.RunID = "same-session", "first"
	a.Effects = []captypes.Effect{captypes.EffectRead, captypes.EffectWrite, captypes.EffectDelete, captypes.EffectExecute, captypes.EffectCommunication}
	if _, err := s.Activate(t.Context(), a, "docs", false); err != nil {
		t.Fatal(err)
	}
	for _, run := range []string{"first", "next"} {
		a.RunID = run
		prepared, err := s.Prepare(t.Context(), a)
		if err != nil || len(prepared.Tools) != 1 {
			t.Fatalf("turn %s needs a separate access request: tools=%v error=%v", run, prepared.Tools, err)
		}
		// A consumer may change its own wire copy, never the next turn's schema.
		if !json.Valid(prepared.Tools[0].InputSchema) {
			t.Fatal("the last request corrupted the live schema")
		}
		prepared.Tools[0].InputSchema[0] = '!'
		if _, err = s.Execute(t.Context(), a, prepared, prepared.Tools[0].Name, nil); err != nil {
			t.Fatal(err)
		}
		s.RevokeRun(a)
	}
	a.Effects = []captypes.Effect{captypes.EffectRead}
	a.RunID = "restricted"
	prepared, err := s.Prepare(t.Context(), a)
	if err != nil || len(prepared.Tools) != 0 || calls.Load() != 2 {
		t.Fatalf("unknown effects leaked to restricted agent: tools=%v calls=%d err=%v", prepared.Tools, calls.Load(), err)
	}
}

// BenchmarkPrepareConnectedToolCatalog measures the request path, not rediscovery
// or network time. Tool descriptions and schemas are already installed.
func BenchmarkPrepareConnectedToolCatalog(b *testing.B) {
	server := mcp.NewServer(&mcp.Implementation{Name: "catalog", Version: "1"}, nil)
	for i := range 48 {
		server.AddTool(&mcp.Tool{Name: fmt.Sprintf("lookup_%02d", i), Description: strings.Repeat("Look up an item. ", 60), InputSchema: map[string]any{"type": "object", "properties": map[string]any{"query": map[string]any{"type": "string", "description": strings.Repeat("Search text. ", 64)}}}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			b.Fatal("building a request called the server tool")
			return nil, nil
		})
	}
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil))
	b.Cleanup(endpoint.Close)
	svc, err := New(b.TempDir(), b.TempDir(), nil)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(svc.Close)
	cfg := captypes.ConnectionConfig{ID: "catalog", Name: "Catalog", Transport: "http", URL: endpoint.URL}
	if err = svc.Configure(b.Context(), captypes.Settings{Connections: map[string]captypes.ConnectionConfig{cfg.ID: cfg}}); err != nil {
		b.Fatal(err)
	}
	if err = svc.Connect(b.Context(), cfg.ID); err != nil {
		b.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", RunID: "run", TurnGeneration: 1, Effects: []captypes.Effect{captypes.EffectRead, captypes.EffectWrite, captypes.EffectDelete, captypes.EffectExecute, captypes.EffectCommunication}}
	b.ReportAllocs()
	for b.Loop() {
		p, err := svc.Prepare(b.Context(), actor)
		if err != nil || len(p.Tools) != 48 {
			b.Fatal(len(p.Tools), err)
		}
	}
}
