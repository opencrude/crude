package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"crude/agent/provider"
	"crude/agent/session"
	captypes "crude/types/capability"
	"crude/types/setting"
)

// accountSearchRunner resolves a search method's account at each call from
// the application credential owner. It holds no copy of credentials.
type accountSearchRunner struct {
	accounts *session.Config
}

func (runner accountSearchRunner) Search(ctx context.Context, route captypes.WebSearchRoute, query string, limit int) ([]provider.WebSearchSource, error) {
	connection, err := runner.accounts.ConnectionProvider(route.Account)
	if err != nil {
		return nil, errors.New("the account for this search method is unavailable. Choose an account in Settings")
	}
	return connection.SearchWeb(ctx, route.Model, query, limit)
}

func (runner accountSearchRunner) Label(route captypes.WebSearchRoute) string {
	label := "Account"
	if account, ok := runner.accounts.AccountManager.Account(route.Account); ok && account.Label != "" {
		label = account.Label
	}
	if route.Model != "" {
		label += " · " + route.Model
	}
	return label
}

func (runner accountSearchRunner) Observation(route captypes.WebSearchRoute) (captypes.WebSearchObservation, bool) {
	connection, err := runner.accounts.ConnectionProvider(route.Account)
	if err != nil {
		return captypes.WebSearchObservation{}, false
	}
	observed, ok := connection.WebSearchObservation(route.Model)
	if !ok {
		return captypes.WebSearchObservation{}, false
	}
	return captypes.WebSearchObservation{Working: observed.Working, Detail: observed.Detail, Results: observed.Results,
		LatencyMS: observed.Latency.Milliseconds(), At: observed.At.Unix()}, true
}

// applyWebSearchRoute is shared by the connected and in-process Settings
// owners. It validates and stages one search method change in Application
// settings without a probe or a search. The provider checks the account
// again at each search.
func applyWebSearchRoute(working *setting.App, action captypes.Action) error {
	if action.Scope != captypes.ScopeApplication {
		return errors.New("change search methods in Application Settings")
	}
	if action.Kind != captypes.KindMCP || action.ID != captypes.WebConnectionID ||
		!working.Capabilities.Connections[captypes.WebConnectionID].IsBuiltinWeb() {
		return errors.New("only Crude's built-in Web search and fetch has search methods")
	}
	if action.WebSearchRoute == nil {
		return errors.New("choose a search method")
	}
	route := *action.WebSearchRoute
	routes := slices.Clone(working.Capabilities.WebSearchRoutes)
	index := slices.IndexFunc(routes, func(saved captypes.WebSearchRoute) bool { return saved.ID == route.ID })
	if action.Operation == captypes.RemoveWebSearchRoute {
		if index < 0 {
			return errors.New("this search method is already removed")
		}
		routes = slices.Delete(routes, index, index+1)
	} else {
		var err error
		if route, err = validWebSearchRoute(working.Accounts, route); err != nil {
			return err
		}
		for _, saved := range routes {
			if saved.ID != route.ID && saved.Account == route.Account && saved.Model == route.Model {
				return errors.New("this account and model are already a search method")
			}
		}
		if index >= 0 {
			routes[index] = route
		} else if len(routes) >= captypes.MaxWebSearchRoutes {
			return fmt.Errorf("you can add up to %d search methods. Remove one first", captypes.MaxWebSearchRoutes)
		} else {
			routes = append(routes, route)
		}
	}
	if len(routes) == 0 {
		routes = nil
	}
	working.Capabilities.WebSearchRoutes = routes
	return nil
}

// validWebSearchRoute reads only the account's protocol declaration. A
// protocol that searches without a model never stores one.
func validWebSearchRoute(accounts map[string]provider.Account, route captypes.WebSearchRoute) (captypes.WebSearchRoute, error) {
	account, ok := accounts[route.Account]
	if !ok {
		return route, errors.New("choose an account for this search method")
	}
	supported, needsModel := provider.WebSearchSupport(account.Connection)
	if !supported {
		return route, errors.New("this account's protocol cannot search the web. Choose another account")
	}
	if account.OAuth != nil {
		return route, errors.New("accounts that sign in with OAuth cannot search the web. Choose an account with an API key")
	}
	if !needsModel {
		route.Model = ""
	} else if route.Model == "" {
		return route, errors.New("choose a model for this search method")
	}
	if !route.Valid() {
		return route, errors.New("the search method has an invalid account or model")
	}
	return route, nil
}

// publishWebSearchRoutes tells a retained capability service about committed
// Application search methods without reconfiguring connections.
func (w *Workspace) publishWebSearchRoutes(routes []captypes.WebSearchRoute) {
	w.capabilityMu.RLock()
	defer w.capabilityMu.RUnlock()
	if w.Capabilities != nil {
		w.Capabilities.SetPublishedWebSearchRoutes(routes)
	}
}
