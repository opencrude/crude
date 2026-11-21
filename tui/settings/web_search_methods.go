package settings

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/agent/provider"
	"crude/tui/ui"
	"crude/types"
	captypes "crude/types/capability"
)

// Search methods are account routes of the built-in web connection. The list
// is a detail collection page; one route opens in its own editor.
const (
	webSearchPage   = "search"
	webRouteEditor  = "web-search-route"
	webRouteOn      = "route-on"
	webRouteAccount = "route-account"
	webRouteModel   = "route-model"
)

func (catalog *capabilityCatalog) builtinWebOpen() bool {
	return catalog.kind == captypes.KindMCP && catalog.owner.model.capabilities.Connections[catalog.id].IsBuiltinWeb()
}

// savedWebSearchRoutes reads the published Application search methods. A
// workspace cannot add or redirect them, so both scopes read the same list.
func (catalog *capabilityCatalog) savedWebSearchRoutes() []captypes.WebSearchRoute {
	model := catalog.owner.model
	if model.capabilitySnapshot.Generation > 0 {
		return model.capabilitySnapshot.Settings.WebSearchRoutes
	}
	return model.appDefaults.Capabilities.WebSearchRoutes
}

func (catalog *capabilityCatalog) savedWebSearchRoute(id string) (captypes.WebSearchRoute, bool) {
	for _, route := range catalog.savedWebSearchRoutes() {
		if route.ID == id {
			return route, true
		}
	}
	return captypes.WebSearchRoute{}, false
}

// webSearchAccount reads only the account's protocol declaration.
func webSearchAccount(account provider.Account) (supported, needsModel bool) {
	if account.ID == "" || account.OAuth != nil {
		return false, false
	}
	return provider.WebSearchSupport(account.Connection)
}

// webSearchProtocolNames lists the protocols that declare native search, from
// the protocol registry.
func webSearchProtocolNames() string {
	var names []string
	for _, protocol := range provider.ProtocolList() {
		if info := protocol.Info(); info.WebSearch != provider.WebSearchNone {
			names = append(names, info.Name)
		}
	}
	sort.Strings(names)
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

func protocolLabel(account provider.Account) string {
	if protocol, ok := provider.ProtocolByID(account.Connection.APIStandard); ok {
		return protocol.Info().Name
	}
	return mapDefault(account.Connection.APIStandard, "Unknown protocol")
}

func (catalog *capabilityCatalog) webRouteAccountLabel(route captypes.WebSearchRoute) string {
	account, ok := catalog.owner.model.accounts[route.Account]
	if !ok {
		return "Account removed"
	}
	return mapDefault(account.Label, route.Account)
}

func (catalog *capabilityCatalog) webRouteObservation(route captypes.WebSearchRoute) (captypes.WebSearchObservation, bool) {
	observation, ok := catalog.owner.model.capabilitySnapshot.WebSearch[route.ID]
	return observation, ok
}

func latencyText(milliseconds int64) string {
	latency := time.Duration(milliseconds) * time.Millisecond
	if latency < time.Second {
		return fmt.Sprintf("%d ms", latency.Milliseconds())
	}
	return fmt.Sprintf("%.1f s", latency.Seconds())
}

// webRouteStatus is the row status: Off, or the latest outcome of a route
// that is on.
func (catalog *capabilityCatalog) webRouteStatus(route captypes.WebSearchRoute) string {
	if route.Disabled {
		return "Off"
	}
	if supported, _ := webSearchAccount(catalog.owner.model.accounts[route.Account]); !supported {
		return "On · Account unavailable"
	}
	observation, ok := catalog.webRouteObservation(route)
	switch {
	case !ok:
		return "On · Not checked yet"
	case observation.Working:
		return "On · Working · " + latencyText(observation.LatencyMS)
	default:
		return "On · Failed"
	}
}

func (catalog *capabilityCatalog) webRouteDetail(route captypes.WebSearchRoute) string {
	account := catalog.owner.model.accounts[route.Account]
	supported, needsModel := webSearchAccount(account)
	model := route.Model
	if !needsModel {
		model = "Not used"
	}
	state := "On"
	if route.Disabled {
		state = "Off"
	}
	result := "Not checked yet. Choose Test, or run a web search."
	if !supported {
		result = "This account cannot search the web. Edit the method and choose another account."
	} else if observation, ok := catalog.webRouteObservation(route); ok {
		checked := time.Unix(observation.At, 0).Format("15:04")
		if observation.Working {
			result = fmt.Sprintf("Working. %d %s in %s at %s.", observation.Results, mapState(observation.Results == 1, "source", "sources"), latencyText(observation.LatencyMS), checked)
		} else {
			result = fmt.Sprintf("Failed at %s. %s", checked, observation.Detail)
		}
	}
	// The last result comes first: the row shows the rest, and a short body
	// must not clip the failure reason.
	return fmt.Sprintf("Last result: %s\nProtocol: %s\nAccount: %s\nModel: %s\nState: %s", result, protocolLabel(account), catalog.webRouteAccountLabel(route), model, state)
}

// webSearchPageForm is the read-only body of the Search methods page.
func (catalog *capabilityCatalog) webSearchPageForm() *ui.FormModel {
	body := "Select a method to see its last result."
	if len(catalog.savedWebSearchRoutes()) == 0 {
		body = "Add a method to also search with an account you already use. Some protocols search with the account alone. Others search through a model you choose."
	}
	if row, ok := catalog.browse.Selected(); ok {
		if route, valid := row.Payload.(captypes.WebSearchRoute); valid {
			body = catalog.webRouteDetail(route)
		}
	}
	description := "Every method that is on runs with each search. Pages that more methods find rank first. Charges can apply."
	if catalog.owner.model.capabilityScope() == captypes.ScopeWorkspace {
		description = "Search methods belong to Application Settings. Switch to Application to change them."
	}
	return formModel(capabilityReadOnly("search-method", "Search method", body, description))
}

func (catalog *capabilityCatalog) syncWebSearchRows(add func(id, name, description, status string, payload any)) {
	for _, route := range catalog.savedWebSearchRoutes() {
		model := route.Model
		if _, needsModel := webSearchAccount(catalog.owner.model.accounts[route.Account]); !needsModel {
			model = "Not used"
		}
		add(route.ID, catalog.webRouteAccountLabel(route), model, catalog.webRouteStatus(route), route)
	}
	catalog.browse.SetColumns([]ui.TableColumn{{ID: "account", Label: "Account", Track: ui.FractionGridTrack(2)}, {ID: "model", Label: "Model", Track: ui.FractionGridTrack(3)}, {ID: "status", Label: "Status", Track: ui.FractionGridTrack(2)}})
}

// refreshWebSearchDetail follows the table selection in the read-only body.
func (catalog *capabilityCatalog) refreshWebSearchDetail() {
	form := catalog.forms[catalog.viewKey()]
	if form == nil || form.Field("search-method") == nil {
		return
	}
	fresh := catalog.webSearchPageForm()
	form.Field("search-method").Value = fresh.Field("search-method").Value
	form.Field("search-method").Description = fresh.Field("search-method").Description
}

func (catalog *capabilityCatalog) selectedWebSearchRoute() (captypes.WebSearchRoute, bool) {
	row, ok := catalog.browse.Selected()
	if !ok {
		return captypes.WebSearchRoute{}, false
	}
	route, valid := row.Payload.(captypes.WebSearchRoute)
	return route, valid
}

func newWebSearchRouteID() string {
	var raw [8]byte
	_, _ = rand.Read(raw[:])
	return "r" + hex.EncodeToString(raw[:])
}

// openWebRouteEditor edits a saved route or a new one. It never starts a
// search.
func (catalog *capabilityCatalog) openWebRouteEditor(route captypes.WebSearchRoute) ui.EventResult {
	model := catalog.owner.model
	if !catalog.builtinWebOpen() {
		return catalog.changed()
	}
	catalog.saveListState()
	catalog.edit = webRouteEditor
	catalog.message = ""
	catalog.webRoute = route
	options := make([]ui.FormOption, 0, len(model.accounts))
	for id, account := range model.accounts {
		if supported, _ := webSearchAccount(account); supported {
			options = append(options, ui.FormOption{ID: id, Label: mapDefault(account.Label, id)})
		}
	}
	sort.Slice(options, func(i, j int) bool {
		if strings.EqualFold(options[i].Label, options[j].Label) {
			return options[i].ID < options[j].ID
		}
		return strings.ToLower(options[i].Label) < strings.ToLower(options[j].Label)
	})
	if route.Account != "" && !slices.ContainsFunc(options, func(option ui.FormOption) bool { return option.ID == route.Account }) {
		options = append(options, ui.FormOption{ID: route.Account, Label: catalog.webRouteAccountLabel(route) + " (unavailable)", Disabled: true})
	}
	accountHelp := "Crude sends each search query to this account. Charges can apply."
	if len(options) == 0 {
		accountHelp = "No account can search the web. Add an account that uses the " + webSearchProtocolNames() + " protocol."
	}
	on := ui.BoolFieldState(webRouteOn, "Use this method", !route.Disabled, "Every method that is on runs with each search.")
	account := ui.SelectFieldState(webRouteAccount, "Account", route.Account, options, accountHelp)
	account.Placeholder = "Choose an account"
	modelField := ui.ModelFieldState(webRouteModel, "Model", route.Model, nil, "Choose a model that can search the web, or enter its exact ID.")
	if model.capabilityScope() != captypes.ScopeApplication {
		on.Disabled, account.Disabled, modelField.Disabled = true, true, true
	}
	model.capabilityDraft = formModel(on, account, modelField)
	catalog.form.SetForm(model.capabilityDraft, "", "")
	catalog.baseline = formDraftBytes(model.capabilityDraft)
	catalog.form.FocusField(webRouteAccount)
	catalog.owner.focusID(catalog.form.ID())
	result := catalog.changed()
	catalog.syncWebRouteModel(false)
	catalog.baseline = formDraftBytes(model.capabilityDraft)
	result.Cmd = catalog.prefetchWebRouteModels()
	catalog.actions()
	return result
}

// syncWebRouteModel follows the chosen account: a protocol that searches
// without a model shows Not used; other protocols list the account's models.
func (catalog *capabilityCatalog) syncWebRouteModel(accountChanged bool) {
	model := catalog.owner.model
	form := model.capabilityDraft
	if form == nil || form.Field(webRouteModel) == nil {
		return
	}
	accountID := form.Field(webRouteAccount).Value.String()
	entry := model.accounts[accountID]
	supported, needsModel := webSearchAccount(entry)
	field := form.Field(webRouteModel)
	if accountChanged && accountID != catalog.webRouteAccount {
		form.SetValue(webRouteModel, ui.StringValue(""))
	}
	catalog.webRouteAccount = accountID
	field.Disabled = !supported || !needsModel || model.capabilityScope() != captypes.ScopeApplication
	field.Loading, field.LoadMessage = false, ""
	// The help row shows only the focused field, and a disabled Model row
	// cannot take focus, so the account help also states the model choice.
	account := form.Field(webRouteAccount)
	switch {
	case !supported:
		field.Placeholder = "Choose an account first"
		field.Description = "Choose an account that can search the web."
	case !needsModel:
		form.SetValue(webRouteModel, ui.StringValue(""))
		field.Placeholder = "Not used"
		field.Description = protocolLabel(entry) + " searches with the account. It does not use a model."
		account.Description = field.Description + " Crude sends each search query to this account. Charges can apply."
	default:
		field.Placeholder = "Choose or enter a model ID"
		field.Description = "Choose a model that can search the web, or enter its exact ID."
		field.Options = ui.ModelOptions(model.models[accountModelKey(entry.ProviderID, accountID)])
		account.Description = protocolLabel(entry) + " searches through a model. Choose the model next. Charges can apply."
	}
	if control := catalog.form.Field(webRouteModel); control != nil {
		control.Sync()
	}
}

func (catalog *capabilityCatalog) prefetchWebRouteModels() tea.Cmd {
	model := catalog.owner.model
	form := model.capabilityDraft
	if form == nil {
		return nil
	}
	field := form.Field(webRouteModel)
	if field == nil || field.Disabled || field.Loading || len(field.Options) != 0 {
		return nil
	}
	accountID := form.Field(webRouteAccount).Value.String()
	account := model.accounts[accountID]
	if _, loaded := model.models[accountModelKey(account.ProviderID, accountID)]; loaded {
		return nil
	}
	field.Loading, field.LoadMessage = true, "Fetching models…"
	return model.fetchModelsForAccountCmd(account.ProviderID, accountID)
}

// setWebRouteModels receives an account's model list for an open editor.
func (m *Model) setWebRouteModels(providerID, accountID string, models []types.ModelEntry, problem string) {
	form := m.capabilityDraft
	if form == nil {
		return
	}
	account, field := form.Field(webRouteAccount), form.Field(webRouteModel)
	if field == nil || account == nil || account.Value.String() != accountID || m.accounts[accountID].ProviderID != providerID {
		return
	}
	field.Loading, field.LoadMessage = false, ""
	if problem == "" {
		field.Options = ui.ModelOptions(models)
	} else {
		field.Description = "Could not list models. Enter the exact model ID or check the account."
	}
}

// webRouteDraft reads the editor into one route. It checks only what the
// user must still choose; the workspace validates the account again.
func (catalog *capabilityCatalog) webRouteDraft() (captypes.WebSearchRoute, error) {
	form := catalog.owner.model.capabilityDraft
	route := catalog.webRoute
	route.Account = form.Field(webRouteAccount).Value.String()
	route.Model = strings.TrimSpace(form.Field(webRouteModel).Value.String())
	route.Disabled = !ui.BoolFieldValue(form.Field(webRouteOn))
	supported, needsModel := webSearchAccount(catalog.owner.model.accounts[route.Account])
	switch {
	case route.Account == "":
		return route, fmt.Errorf("choose an account for this search method")
	case !supported:
		return route, fmt.Errorf("this account cannot search the web. Choose another account")
	case !needsModel:
		route.Model = ""
	case route.Model == "":
		return route, fmt.Errorf("choose a model for this search method")
	}
	return route, nil
}

func (catalog *capabilityCatalog) webRouteAction(operation string, route captypes.WebSearchRoute) captypes.Action {
	return captypes.Action{Operation: operation, Kind: captypes.KindMCP, ID: captypes.WebConnectionID, Scope: captypes.ScopeApplication, WebSearchRoute: &route}
}

// OpenWebSearchMethods opens Search methods of the built-in web connection.
// It shares the normal dirty-draft guard and never starts a search.
func (surface *WorkspaceSettingsSurface) OpenWebSearchMethods() ui.EventResult {
	return surface.openCapabilityNavigation(capabilityNavigation{kind: captypes.KindMCP, id: captypes.WebConnectionID, page: webSearchPage})
}
