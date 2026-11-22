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
	"crude/internal/network"
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

// fixtureSearchRunner stands in for the core's account owner.
type fixtureSearchRunner struct {
	calls        atomic.Int32
	search       func(ctx context.Context, route captypes.WebSearchRoute, query string, limit int) ([]provider.WebSearchSource, error)
	observations map[string]captypes.WebSearchObservation
}

func (runner *fixtureSearchRunner) Search(ctx context.Context, route captypes.WebSearchRoute, query string, limit int) ([]provider.WebSearchSource, error) {
	runner.calls.Add(1)
	return runner.search(ctx, route, query, limit)
}

func (runner *fixtureSearchRunner) Label(route captypes.WebSearchRoute) string {
	return "Fixture · " + route.Model
}

func (runner *fixtureSearchRunner) Observation(route captypes.WebSearchRoute) (captypes.WebSearchObservation, bool) {
	observation, ok := runner.observations[route.Account+"/"+route.Model]
	return observation, ok
}

func TestBuiltinWebSearchRacesAccountMethodsThroughAdmittedMCPCall(t *testing.T) {
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
	var routeState atomic.Int32
	started, stopped := make(chan struct{}), make(chan struct{})
	runner := &fixtureSearchRunner{observations: map[string]captypes.WebSearchObservation{
		"account-a/model-a": {Working: true, Results: 3, LatencyMS: 1400, At: 1},
	}}
	runner.search = func(ctx context.Context, route captypes.WebSearchRoute, query string, limit int) ([]provider.WebSearchSource, error) {
		if route.Account != "account-a" || route.Model != "model-a" || query != "literal query" || limit != 10 {
			t.Errorf("search method lost its exact route, query or limit: %+v %q %d", route, query, limit)
		}
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) > operationLimitForTest {
			t.Errorf("search method ran outside the shared operation context: %v %v", deadline, bounded)
		}
		switch routeState.Load() {
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
			return []provider.WebSearchSource{{Title: "Typed title", URL: "https://account.example/actual", Snippet: "account snippet"}}, nil
		}
	}
	service.SetWebSearchRunner(runner)

	actor := captypes.RuntimeActor{ID: "root", SessionID: "s", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead}, AllowedTools: []string{"web_search"}}
	_, prepared := preparedNames(t, service, actor)
	execute := func(ctx context.Context, actor captypes.RuntimeActor, prepared captypes.RuntimeSnapshot) (captypes.Result, error) {
		return service.Execute(ctx, actor, prepared, "web_search", map[string]any{"query": "literal query", "max_results": 45})
	}
	check := func(wantCalls int32, wantError bool, who captypes.RuntimeActor, snapshot captypes.RuntimeSnapshot, wantText ...string) {
		t.Helper()
		result, err := execute(t.Context(), who, snapshot)
		if err != nil || result.IsError != wantError || len(result.Content) != 1 || runner.calls.Load() != wantCalls {
			t.Fatalf("unexpected result=%+v err=%v calls=%d", result, err, runner.calls.Load())
		}
		for _, text := range wantText {
			if !strings.Contains(result.Content[0].Text, text) {
				t.Fatalf("result lacks %q:\n%s", text, result.Content[0].Text)
			}
		}
		if (result.Code == captypes.WebSearchOutage) != wantError {
			t.Fatalf("outage marker must describe only a total outage: %+v", result)
		}
	}
	check(0, true, actor, prepared, "Every search provider failed")
	service.SetPublishedWebSearchRoutes([]captypes.WebSearchRoute{
		{ID: "r1", Account: "account-a", Model: "model-a"},
		{ID: "r2", Account: "account-b", Model: "model-b", Disabled: true},
	})
	if runner.calls.Load() != 0 {
		t.Fatal("saving search methods started a search")
	}
	if got := service.Snapshot().WebSearch; len(got) != 1 || !got["r1"].Working || got["r1"].LatencyMS != 1400 {
		t.Fatalf("snapshot lost the route observation: %+v", got)
	}
	// The account method races with the ordinary providers instead of
	// waiting for them to fail, and both results reach the agent.
	primaryState.Store(1)
	check(1, false, actor, prepared, "https://regular.example/page", "https://account.example/actual", "Fixture · model-a", "account snippet")
	primaryState.Store(2)
	routeState.Store(1)
	check(2, false, actor, prepared, "No results found")
	primaryState.Store(0)
	routeState.Store(0)
	check(3, false, actor, prepared, "(Sources: Fixture · model-a)", "https://account.example/actual")
	child := actor
	child.ID, child.RunID, child.ExecutionRoot = "child", "child-run", t.TempDir()
	_, childPrepared := preparedNames(t, service, child)
	check(4, false, child, childPrepared, "https://account.example/actual")
	routeState.Store(2)
	check(5, true, actor, prepared, "selected route unavailable")

	sessionOff := actor
	sessionOff.SessionDisabled = []string{captypes.WebConnectionID}
	if result, err := execute(t.Context(), sessionOff, prepared); err == nil || result.Code != "" || runner.calls.Load() != 5 {
		t.Fatalf("disabled session reached a search method: %+v %v", result, err)
	}
	denied := actor
	denied.DeniedTools = []string{"web_search"}
	if result, err := execute(t.Context(), denied, prepared); err == nil || result.Code != "" || runner.calls.Load() != 5 {
		t.Fatalf("denied call reached a search method: %+v %v", result, err)
	}

	routeState.Store(3)
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
		t.Fatal("the search method did not start")
	}
	cancel()
	if result := <-done; result.Code != "" || runner.calls.Load() != 6 {
		t.Fatalf("cancelled call acquired an outage marker or replayed: %+v", result)
	}
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the search method did not receive MCP cancellation")
	}

	connection.Disabled = true
	settings.Connections[captypes.WebConnectionID] = connection
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if result, err := execute(t.Context(), actor, prepared); err == nil || result.Code != "" || runner.calls.Load() != 6 {
		t.Fatalf("disabled connection reached a search method: %+v %v", result, err)
	}
	if got := service.Snapshot().Settings.WebSearchRoutes; len(got) != 2 {
		t.Fatalf("an older captured configuration replaced published search methods: %+v", got)
	}
}

func TestWebSearchRouteTestRecordsWithoutSaving(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	if err := service.Configure(t.Context(), captypes.WithDefaults(settings)); err != nil {
		t.Fatal(err)
	}
	if _, err := service.TestWebSearchRoute(t.Context(), captypes.WebSearchRoute{Account: "account-a"}); err == nil {
		t.Fatal("a test without an account owner succeeded")
	}
	runner := &fixtureSearchRunner{search: func(_ context.Context, route captypes.WebSearchRoute, query string, limit int) ([]provider.WebSearchSource, error) {
		if route.Model == "broken" {
			return nil, errors.New("The account refused the key (HTTP 401). Check the account in Settings.")
		}
		if query == "" || limit != 5 {
			t.Errorf("test search sent query %q limit %d", query, limit)
		}
		return []provider.WebSearchSource{{URL: "https://go.dev/", Title: "Go"}, {URL: "https://pkg.go.dev/", Title: "Packages"}}, nil
	}}
	service.SetWebSearchRunner(runner)
	message, err := service.TestWebSearchRoute(t.Context(), captypes.WebSearchRoute{Account: "account-a"})
	if err != nil || !strings.HasPrefix(message, "Search works: 2 sources in ") {
		t.Fatalf("test result %q %v", message, err)
	}
	if _, err := service.TestWebSearchRoute(t.Context(), captypes.WebSearchRoute{Account: "account-a", Model: "broken"}); err == nil ||
		err.Error() != "search failed: The account refused the key (HTTP 401). Check the account in Settings." {
		t.Fatalf("failed test lost its reason: %v", err)
	}
	if routes := service.Snapshot().Settings.WebSearchRoutes; len(routes) != 0 {
		t.Fatalf("a test saved a search method: %+v", routes)
	}
}

const operationLimitForTest = 24 * time.Second
