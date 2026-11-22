package capability

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"crude/agent/provider"
	"crude/agent/websearch"
	captypes "crude/types/capability"
)

// webSearchTestQuery is the fixed query a Test sends. It is not user content
// and it is common enough that every working search returns sources.
const webSearchTestQuery = "Go programming language"

// WebSearchRunner is the core's account owner for search methods. Only the
// built-in web_search handler, after normal MCP admission, and an explicit
// Test use it. Implementations resolve the account at each call.
type WebSearchRunner interface {
	Search(ctx context.Context, route captypes.WebSearchRoute, query string, limit int) ([]provider.WebSearchSource, error)
	// Label names the route in search results, for example "OC · gpt-oss".
	Label(route captypes.WebSearchRoute) string
	Observation(route captypes.WebSearchRoute) (captypes.WebSearchObservation, bool)
}

// SetWebSearchRunner binds the core's account owner.
func (s *Service) SetWebSearchRunner(runner WebSearchRunner) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webSearch = runner
}

// SetPublishedWebSearchRoutes updates a retained service from the committed
// application settings. It is a memory-only observation: it does not test a
// route, scan configuration or repeat a search.
func (s *Service) SetPublishedWebSearchRoutes(routes []captypes.WebSearchRoute) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.webRoutesPublished = true
	if !slices.Equal(s.settings.WebSearchRoutes, routes) {
		s.settings.WebSearchRoutes = slices.Clone(routes)
		s.generation++
	}
}

// webSearchMethods snapshots the routes that are on as a search starts. The
// snapshot is not reachable through tool arguments or by other tools.
func (s *Service) webSearchMethods() []websearch.Method {
	s.mu.RLock()
	routes := slices.Clone(s.settings.WebSearchRoutes)
	runner := s.webSearch
	config := s.settings.Connections[captypes.WebConnectionID]
	off := s.closed || !config.IsBuiltinWeb() || config.Disabled || config.Grants["web_search"].Disabled
	s.mu.RUnlock()
	if runner == nil || off {
		return nil
	}
	methods := make([]websearch.Method, 0, len(routes))
	for _, route := range routes {
		if route.Disabled || !route.Valid() || len(methods) == captypes.MaxWebSearchRoutes {
			continue
		}
		methods = append(methods, websearch.Method{Name: runner.Label(route), Configured: true,
			Search: func(ctx context.Context, query string, limit int) ([]websearch.SearchResult, error) {
				found, err := runner.Search(ctx, route, query, limit)
				if err != nil {
					return nil, err
				}
				results := make([]websearch.SearchResult, 0, len(found))
				for _, source := range found {
					results = append(results, websearch.SearchResult{URL: source.URL, Title: source.Title, Description: source.Snippet})
				}
				return results, nil
			}})
	}
	return methods
}

// TestWebSearchRoute runs one search with a fixed query through a saved or
// draft route. It records the outcome like a live search and saves nothing.
func (s *Service) TestWebSearchRoute(ctx context.Context, route captypes.WebSearchRoute) (string, error) {
	s.mu.RLock()
	runner := s.webSearch
	s.mu.RUnlock()
	if runner == nil {
		return "", errors.New("search methods are unavailable in this workspace")
	}
	if route.ID == "" {
		route.ID = "test"
	}
	if !route.Valid() {
		return "", errors.New("choose an account for this search method")
	}
	started := time.Now()
	found, err := runner.Search(ctx, route, webSearchTestQuery, 5)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("search failed: %w", err)
	}
	return fmt.Sprintf("Search works: %d %s in %s.", len(found), plural(len(found), "source", "sources"), latencyText(time.Since(started))), nil
}

func (s *Service) webSearchObservations(routes []captypes.WebSearchRoute) map[string]captypes.WebSearchObservation {
	s.mu.RLock()
	runner := s.webSearch
	s.mu.RUnlock()
	if runner == nil || len(routes) == 0 {
		return nil
	}
	observations := make(map[string]captypes.WebSearchObservation)
	for _, route := range routes {
		if !route.Valid() {
			continue
		}
		if observation, ok := runner.Observation(route); ok {
			observations[route.ID] = observation
		}
	}
	return observations
}

func plural(count int, one, many string) string {
	if count == 1 {
		return one
	}
	return many
}

// latencyText matches the latency format of the Search methods list.
func latencyText(latency time.Duration) string {
	if latency < time.Second {
		return fmt.Sprintf("%d ms", latency.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", latency.Seconds())
}
