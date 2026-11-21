package capability

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/webmcp"
	"crude/network"
	captypes "crude/types/capability"
)

func preparedNames(t *testing.T, service *Service, actor captypes.RuntimeActor) ([]string, captypes.RuntimeSnapshot) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	prepared, err := service.Prepare(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range prepared.Tools {
		names = append(names, tool.Name)
	}
	return names, prepared
}

func TestBuiltinWebToolsReachAgentsWithoutActivationAndHonorUserSwitches(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	settings = captypes.WithDefaults(settings)
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", SessionID: "s", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead}}
	names, _ := preparedNames(t, service, actor)
	for _, want := range webmcp.ToolNames() {
		if !slices.Contains(names, want) {
			t.Fatalf("built-in web tool %q missing from %v", want, names)
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, "mcp__") {
			t.Fatalf("built-in tools must keep plain names: %v", names)
		}
	}

	researcher := actor
	researcher.ID, researcher.AllowedTools = "researcher", []string{"web_search", "read_file"}
	if names, _ := preparedNames(t, service, researcher); !slices.Equal(names, []string{"web_search"}) {
		t.Fatalf("allow-list must select built-in tools by plain name: %v", names)
	}

	off := actor
	off.ID, off.SessionDisabled = "off", []string{captypes.WebConnectionID}
	if names, _ := preparedNames(t, service, off); len(names) != 0 {
		t.Fatalf("session switch did not remove built-in tools: %v", names)
	}

	web := settings.Connections[captypes.WebConnectionID]
	web.Grants = map[string]captypes.ToolGrant{"arxiv_search": {Disabled: true}}
	settings.Connections[captypes.WebConnectionID] = web
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if names, _ := preparedNames(t, service, actor); slices.Contains(names, "arxiv_search") || !slices.Contains(names, "web_fetch") {
		t.Fatalf("per-tool switch not honored: %v", names)
	}

	web.Disabled = true
	settings.Connections[captypes.WebConnectionID] = web
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	names, prepared := preparedNames(t, service, actor)
	if len(names) != 0 {
		t.Fatalf("disabled built-in still offered: %v", names)
	}
	if _, err := service.Execute(t.Context(), actor, prepared, "web_search", map[string]any{"query": "x"}); err == nil {
		t.Fatal("disabled built-in executed")
	}
	for _, entry := range service.Snapshot().Entries {
		if entry.ID == captypes.WebConnectionID && (entry.State != "disabled" || entry.Publisher != "Crude") {
			t.Fatalf("built-in entry=%+v", entry)
		}
	}
}

func TestSettingSecretsRestartsBuiltinAndReportsOnlyNames(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	settings = captypes.WithDefaults(settings)
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", SessionID: "s", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead}}
	preparedNames(t, service, actor)
	if err := service.SetSecrets(t.Context(), map[string]string{"EXA_API_KEY": "exa-secret"}); err != nil {
		t.Fatal(err)
	}
	snapshot := service.Snapshot()
	if !slices.Equal(snapshot.StoredSecrets, []string{"EXA_API_KEY"}) {
		t.Fatalf("stored secret names=%v", snapshot.StoredSecrets)
	}
	for _, state := range snapshot.Connections {
		if state.ID == captypes.WebConnectionID && state.State != "connected" {
			t.Fatalf("built-in did not come back after a secret change: %+v", state)
		}
	}
	if err := service.SetSecrets(t.Context(), nil); err == nil {
		t.Fatal("empty secret update accepted")
	}
}

func TestBuiltinWebSearchFallbackThroughAdmittedMCPCall(t *testing.T) {
	// Search uses the production endpoints and transport; this isolated proxy
	// denies every public connection instead of using real keys or the web.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "fixture outage", http.StatusBadGateway)
	}))
	defer proxy.Close()
	prior := network.Current()
	t.Cleanup(func() {
		if err := network.ConfigureDefault(prior); err != nil {
			t.Error(err)
		}
	})
	policy := network.DefaultSettings()
	policy.ProxyMode, policy.ProxyURL, policy.NoProxy = network.ProxyModeCustom, proxy.URL, "127.0.0.1"
	if err := network.ConfigureDefault(policy); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"EXA_API_KEY", "BRAVE_SEARCH_API_KEY", "TAVILY_API_KEY", "SEARXNG_URL", "JINA_API_KEY"} {
		t.Setenv(key, "")
	}

	var primaryState atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search" || r.URL.Query().Get("q") != "literal query" {
			t.Errorf("wrong primary request: %s", r.URL)
		}
		switch primaryState.Load() {
		case 1:
			_, _ = fmt.Fprint(w, `{"results":[{"title":"Ordinary result","url":"https://regular.example/page","content":"snippet"}]}`)
		case 2:
			_, _ = fmt.Fprint(w, `{"results":[]}`)
		default:
			http.Error(w, "fixture outage", http.StatusBadGateway)
		}
	}))
	defer primary.Close()

	service, settings, _ := capabilityFixture(t)
	settings = captypes.WithDefaults(settings)
	connection := settings.Connections[captypes.WebConnectionID]
	connection.Environment = map[string]string{"SEARXNG_URL": primary.URL}
	settings.Connections[captypes.WebConnectionID] = connection
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	var modelCalls atomic.Int32
	var modelState atomic.Int32
	started, stopped := make(chan struct{}), make(chan struct{})
	service.SetWebSearchFallbackRunner(func(ctx context.Context, account, model, query string, limit int) ([]provider.WebSearchSource, error) {
		modelCalls.Add(1)
		if account != "selected-account" || model != "selected-model" || query != "literal query" || limit != 10 {
			t.Errorf("fallback lost exact route or query/limit: %q %q %q %d", account, model, query, limit)
		}
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) > operationLimitForTest {
			t.Errorf("fallback was outside the shared operation context: %v %v", deadline, bounded)
		}
		switch modelState.Load() {
		case 1:
			return nil, nil
		case 2:
			return nil, errors.New("selected route unavailable")
		case 3:
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		default:
			return []provider.WebSearchSource{{Title: "Typed title", URL: "https://fallback.example/actual"}}, nil
		}
	})

	actor := captypes.RuntimeActor{ID: "root", SessionID: "s", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead}, AllowedTools: []string{"web_search"}}
	_, prepared := preparedNames(t, service, actor)
	execute := func(ctx context.Context, actor captypes.RuntimeActor, prepared captypes.RuntimeSnapshot) (captypes.Result, error) {
		return service.Execute(ctx, actor, prepared, "web_search", map[string]any{"query": "literal query", "max_results": 45})
	}
	check := func(wantCalls int32, wantError bool, wantText string, who captypes.RuntimeActor, snapshot captypes.RuntimeSnapshot) {
		t.Helper()
		result, err := execute(t.Context(), who, snapshot)
		if err != nil || result.IsError != wantError || len(result.Content) != 1 ||
			!strings.Contains(result.Content[0].Text, wantText) || modelCalls.Load() != wantCalls {
			t.Fatalf("unexpected result=%+v err=%v calls=%d", result, err, modelCalls.Load())
		}
		if (result.Code == captypes.WebSearchFallbackAvailable) != wantError {
			t.Fatalf("failure marker must describe only provider failure: %+v", result)
		}
	}
	check(0, true, "Every search provider failed", actor, prepared)
	service.SetPublishedWebSearchFallback(captypes.WebSearchFallback{"selected-account", "selected-model"})
	if modelCalls.Load() != 0 {
		t.Fatal("saving settings replayed a search")
	}
	primaryState.Store(1)
	check(0, false, "https://regular.example/page", actor, prepared)
	primaryState.Store(2)
	check(0, false, "No results found", actor, prepared)
	primaryState.Store(0)
	check(1, false, "(Sources: OpenAI Responses model fallback (selected-model))", actor, prepared)
	child := actor
	child.ID, child.RunID, child.ExecutionRoot = "child", "child-run", t.TempDir()
	_, childPrepared := preparedNames(t, service, child)
	check(2, false, "https://fallback.example/actual", child, childPrepared)
	modelState.Store(1)
	check(3, false, "No results found", actor, prepared)
	modelState.Store(2)
	check(4, true, "selected route unavailable", actor, prepared)

	sessionOff := actor
	sessionOff.SessionDisabled = []string{captypes.WebConnectionID}
	if result, err := execute(t.Context(), sessionOff, prepared); err == nil || result.Code != "" || modelCalls.Load() != 4 {
		t.Fatalf("disabled session reached fallback: %+v %v", result, err)
	}
	denied := actor
	denied.DeniedTools = []string{"web_search"}
	if result, err := execute(t.Context(), denied, prepared); err == nil || result.Code != "" || modelCalls.Load() != 4 {
		t.Fatalf("denied call reached fallback: %+v %v", result, err)
	}

	modelState.Store(3)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	done := make(chan captypes.Result, 1)
	go func() {
		result, _ := execute(ctx, actor, prepared)
		done <- result
	}()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("model fallback did not reach its native operation")
	}
	cancel()
	if result := <-done; result.Code != "" || modelCalls.Load() != 5 {
		t.Fatalf("cancelled call acquired a settings marker or replayed: %+v", result)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("callback did not receive MCP cancellation")
	}

	connection.Disabled = true
	settings.Connections[captypes.WebConnectionID] = connection
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if result, err := execute(t.Context(), actor, prepared); err == nil || result.Code != "" || modelCalls.Load() != 5 {
		t.Fatalf("disabled connection reached fallback: %+v %v", result, err)
	}
}

const operationLimitForTest = 24 * time.Second
