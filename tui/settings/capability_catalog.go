package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

const capabilityCatalogID ui.ComponentID = "settings/capabilities"
const capabilityFilterID ui.ComponentID = "settings/capabilities/filter"
const capabilityStoreID ui.ComponentID = "settings/capabilities/store"
const capabilityFormID ui.ComponentID = "settings/capabilities/form"
const capabilityTabsID ui.ComponentID = "settings/capabilities/tabs"

type capabilityCatalog struct {
	ui.Base
	owner                    *WorkspaceSettingsSurface
	tokens                   *ui.TokenSet
	tabs                     *ui.Tabs
	filter                   *ui.FilterField
	store                    *capabilityStoreView
	storeFilters             *ui.ActionBar
	installed                *capabilityInstalledFilter
	searchButton             *ui.ActionBar
	choices                  *ui.List
	choiceScroll             *ui.ScrollbarControl
	choosing                 string
	storeButtons             map[string]*ui.Button
	browse                   *ui.Table
	tree                     *ui.Tree
	form                     *ui.Form
	reader                   *ui.TextArea
	bar                      *ui.ActionBar
	more                     *ui.ActionBar
	storeScroll              *ui.ScrollbarControl
	formScroll               *ui.ScrollbarControl
	browseScroll             *ui.ScrollbarControl
	treeScroll               *ui.ScrollbarControl
	kind                     captypes.Kind
	id                       string
	detail                   bool
	edit                     string
	baseline                 []byte
	seen                     uint64
	message                  string
	detailPage               string
	forms                    map[string]*ui.FormModel
	inputForms               map[string]*ui.FormModel
	views                    map[string]capabilityListState
	requests                 map[uint64]capabilityViewRequest
	preview                  map[string]CapabilityResultMsg
	files                    map[string][]string
	extraBars                []*ui.ActionBar
	statusY                  int
	resultSeen               uint64
	model                    *Model
	draftOriginal            captypes.Action
	nextOpen                 *capabilityNavigation
	acquisition              *captypes.AcquisitionPreview
	acquisitionReturnToStore bool
	storeSources             []string
	storeTypes               []string
}

type capabilityListState struct {
	query, selected string
	inspected       string
	offset          int
	source          string
	show            string
	entryType       string
	sort            string
}
type capabilityViewRequest struct {
	key    string
	draft  []byte
	action captypes.Action
	form   *ui.FormModel
	entity []byte
}
type capabilityNavigation struct {
	kind      captypes.Kind
	query, id string
}

func newCapabilityCatalog(owner *WorkspaceSettingsSurface) *capabilityCatalog {
	catalog := &capabilityCatalog{Base: ui.NewBase(capabilityCatalogID), owner: owner, model: owner.model, tokens: owner.tokens, kind: captypes.KindSkill, forms: make(map[string]*ui.FormModel), inputForms: make(map[string]*ui.FormModel), views: make(map[string]capabilityListState), requests: make(map[uint64]capabilityViewRequest), preview: make(map[string]CapabilityResultMsg), files: make(map[string][]string), storeButtons: make(map[string]*ui.Button)}
	catalog.tabs = ui.NewTabs(capabilityTabsID, owner.tokens)
	catalog.tabs.SetChrome(ui.TabsPopup)
	catalog.filter = ui.NewFilterField(capabilityFilterID, owner.tokens)
	catalog.filter.SetPrompt(ui.SearchGlyphEnd(owner.tokens))
	catalog.filter.SetPromptPlacement(ui.PromptEnd)
	catalog.filter.SetSubmitAction("settings.capability.search")
	catalog.filter.OnChanged(func(query string) {
		state := catalog.views[catalog.viewKey()]
		state.query = query
		state.offset, state.selected = 0, ""
		catalog.message = ""
		catalog.views[catalog.viewKey()] = state
		catalog.store.Scroll().ScrollTo(0)
		if catalog.detail {
			catalog.syncDetailRows()
		} else {
			catalog.syncRows()
		}
	})
	catalog.store = &capabilityStoreView{VirtualList: ui.NewVirtualList(capabilityStoreID, owner.tokens), owner: catalog}
	catalog.store.SetSelectionAnchorsScroll(false)
	catalog.store.SetOpaqueRowFill(false)
	catalog.store.SetActivateAction("settings.capability.inspect")
	catalog.store.SetRowPainter(catalog.paintStoreRow)
	catalog.storeFilters = ui.NewActionBar("settings/capabilities/store-filters", owner.tokens)
	catalog.storeFilters.SetAlignEnd(false)
	catalog.storeFilters.SetShowRule(false)
	catalog.storeFilters.SetCancelAction("settings.capability.back")
	catalog.storeFilters.SetExitActions("settings.capability.filters-next", "settings.capability.filters-prev")
	catalog.installed = &capabilityInstalledFilter{ToggleButton: ui.NewToggleButton("settings/capabilities/installed", "Installed only", "settings.capability.installed-only", owner.tokens), owner: catalog}
	catalog.installed.SetCancelAction("settings.capability.back")
	catalog.searchButton = ui.NewActionBar("settings/capabilities/search", owner.tokens, ui.ActionBarButton{Label: "Search", Action: "settings.capability.search", Enabled: true})
	catalog.searchButton.SetAlignEnd(false)
	catalog.searchButton.SetCancelAction("settings.capability.back")
	catalog.searchButton.SetExitActions("settings.capability.search-next", "settings.capability.search-prev")
	catalog.choices = ui.NewList("settings/capabilities/choices", owner.tokens)
	catalog.choices.SetActivateAction("settings.capability.choose")
	catalog.choiceScroll = ui.NewScrollbarControl("settings/capabilities/choice-scroll", catalog.choices.Scroll(), owner.tokens)
	catalog.browse = ui.NewTable("settings/capabilities/browse", owner.tokens)
	catalog.browse.SetShowHeader(true)
	catalog.browse.SetColumnGap(2)
	catalog.browse.SetActivateAction("settings.capability.inspect-item")
	catalog.tree = ui.NewTree("settings/capabilities/files", owner.tokens)
	catalog.tree.SetSurfaceBackground(ui.ColorPopupBg)
	catalog.form = ui.NewForm(capabilityFormID, nil, "", "", owner.tokens)
	catalog.form.SetArrowExitsForm(true)
	catalog.form.SetFieldGap(0)
	catalog.reader = ui.NewTextArea("settings/capabilities/reader", owner.tokens, true)
	catalog.reader.SetReadOnly(true)
	catalog.storeScroll = ui.NewScrollbarControl("settings/capabilities/store-scroll", catalog.store.Scroll(), owner.tokens)
	catalog.storeScroll.OnScroll(func(int, int) { catalog.saveListState() })
	catalog.formScroll = ui.NewScrollbarControl("settings/capabilities/form-scroll", catalog.form.Scroll(), owner.tokens)
	catalog.browseScroll = ui.NewScrollbarControl("settings/capabilities/browse-scroll", catalog.browse.Scroll(), owner.tokens)
	catalog.treeScroll = ui.NewScrollbarControl("settings/capabilities/tree-scroll", catalog.tree.Scroll(), owner.tokens)
	catalog.bar = ui.NewActionBar("settings/capabilities/actions", owner.tokens)
	catalog.more = ui.NewActionBar("settings/capabilities/more", owner.tokens)
	catalog.bar.SetAlignEnd(false)
	catalog.more.SetAlignEnd(false)
	catalog.bar.SetCancelAction("settings.capability.back")
	catalog.more.SetCancelAction("settings.capability.back")
	catalog.bar.SetExitActions("settings.capability.actions-next", "settings.capability.actions-prev")
	catalog.more.SetExitActions("settings.capability.actions-next", "settings.capability.actions-prev")
	catalog.syncRows()
	return catalog
}

func (catalog *capabilityCatalog) SetTokens(tokens *ui.TokenSet) {
	catalog.tokens = tokens
	catalog.tabs.SetTokens(tokens)
	catalog.filter.SetTokens(tokens)
	catalog.store.SetTokens(tokens)
	catalog.storeFilters.SetTokens(tokens)
	catalog.installed.SetTokens(tokens)
	catalog.searchButton.SetTokens(tokens)
	catalog.choices.SetTokens(tokens)
	catalog.choiceScroll.SetTokens(tokens)
	for _, button := range catalog.storeButtons {
		button.SetTokens(tokens)
	}
	catalog.browse.SetTokens(tokens)
	catalog.tree.SetTokens(tokens)
	catalog.form.SetTokens(tokens)
	catalog.reader.SetTokens(tokens)
	catalog.bar.SetTokens(tokens)
	catalog.more.SetTokens(tokens)
	catalog.storeScroll.SetTokens(tokens)
	catalog.formScroll.SetTokens(tokens)
	catalog.browseScroll.SetTokens(tokens)
	catalog.treeScroll.SetTokens(tokens)
	for _, bar := range catalog.extraBars {
		bar.SetTokens(tokens)
	}
}

func (catalog *capabilityCatalog) Children() []ui.Component {
	children := []ui.Component{catalog.tabs, catalog.filter, catalog.storeFilters, catalog.installed, catalog.searchButton, catalog.choices, catalog.choiceScroll, catalog.store, catalog.browse, catalog.tree, catalog.form, catalog.reader}
	if catalog.Bounds().Empty() {
		return children
	}
	children = append(children, catalog.storeScroll, catalog.formScroll, catalog.browseScroll, catalog.treeScroll, catalog.bar, catalog.more)
	for _, bar := range catalog.extraBars {
		if !bar.Bounds().Empty() {
			children = append(children, bar)
		}
	}
	return children
}

func (catalog *capabilityCatalog) syncRows() {
	model := catalog.owner.model
	if model == nil {
		return
	}
	state := catalog.views[catalog.viewKey()]
	var entries []captypes.Entry
	if catalog.kind == "source" {
		for id, source := range model.capabilities.Sources {
			mode := ""
			if source.Builtin {
				mode = "Built-in"
			}
			entries = append(entries, captypes.Entry{ID: id, Kind: catalog.kind, Name: source.Name, Description: source.Location, Source: id, Path: source.Location, Mode: mode, State: mapState(source.Disabled, "disabled", "enabled"), Origin: source.Scope, Installed: true, Transport: source.Kind, Version: source.Revision})
		}
		slices.SortFunc(entries, func(a, b captypes.Entry) int {
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
	} else {
		for _, entry := range model.capabilitySnapshot.Entries {
			if entry.Kind == catalog.kind {
				if model.capabilityScope() == captypes.ScopeApplication && entry.Origin == captypes.ScopeWorkspace {
					continue
				}
				entries = append(entries, entry)
			}
		}
		if len(entries) == 0 {
			if catalog.kind == captypes.KindSkill {
				for id, skill := range model.capabilities.Skills {
					entries = append(entries, captypes.Entry{ID: id, Name: skill.Name, Kind: catalog.kind, Source: skill.Source, State: mapState(skill.Disabled, "disabled", "installed"), Mode: skill.Mode, Origin: skill.Scope, Path: skill.Path, Managed: skill.Managed, Installed: true})
				}
			}
			if catalog.kind == captypes.KindMCP {
				for id, connection := range model.capabilities.Connections {
					entries = append(entries, captypes.Entry{ID: id, Name: connection.Name, Kind: catalog.kind, Source: connection.Source, State: mapState(connection.Disabled, "disabled", "not started"), Origin: connection.Scope, Installed: true, Transport: connection.Transport, PackageType: connection.PackageType, Repository: connection.RepositoryURL, Website: connection.WebsiteURL})
				}
			}
		}
		slices.SortFunc(entries, func(a, b captypes.Entry) int {
			return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name))
		})
		entries = catalog.mergeStoreDiscovery(entries, state)
	}
	entries, state = catalog.filteredStoreEntries(entries, state)
	catalog.views[catalog.viewKey()] = state
	items := make([]ui.VirtItem, 0, len(entries))
	for _, entry := range entries {
		items = append(items, ui.VirtItem{ID: entry.ID, Height: 5, Label: entry.Name, Detail: entry.Description, Payload: entry})
	}
	oldOffset := catalog.store.Scroll().Offset()
	catalog.store.SetItems(items)
	for index, item := range items {
		if item.ID == state.selected {
			catalog.store.SelectIndex(index)
			break
		}
	}
	if state.selected != "" {
		oldOffset = state.offset
	}
	catalog.store.Scroll().ScrollTo(oldOffset)
	catalog.syncStoreFilters(entries, state)
	catalog.syncStoreButtons(entries)
	catalog.seen = model.capabilityRevision
	catalog.actions()
}

func mapState(condition bool, first, second string) string {
	if condition {
		return first
	}
	return second
}

func (catalog *capabilityCatalog) actions() {
	button := func(label, action string) ui.ActionBarButton {
		return ui.ActionBarButton{ID: ui.NewID(capabilityCatalogID, action), Label: label, Action: "settings.capability." + action, Enabled: true}
	}
	var primary, secondary []ui.ActionBarButton
	if catalog.edit != "" {
		label := "Save"
		if catalog.edit == "grant" {
			label = "Save grant"
		} else if catalog.kind == captypes.KindMCP {
			label = "Save connection"
		} else if catalog.kind == "source" {
			label = "Save source"
		} else if catalog.edit == "make-local-copy" {
			label = "Create copy"
		}
		primary = []ui.ActionBarButton{button(label, "save"), button("Back", "back")}
		if catalog.kind == captypes.KindMCP && catalog.edit != "grant" && catalog.edit != "import" {
			secondary = []ui.ActionBarButton{button("Test draft", "test")}
		}
	} else if catalog.detail {
		primary, secondary = catalog.detailActions(button)
	} else if catalog.choosing != "" {
		primary = []ui.ActionBarButton{button("Back", "close-filter")}
	} else {
		if catalog.kind == captypes.KindSkill {
			primary = []ui.ActionBarButton{button("Create skill", "create-skill"), button("Add local", "add-local")}
		}
		if catalog.kind == captypes.KindMCP {
			primary = []ui.ActionBarButton{button("Add connection", "add"), button("Import", "import")}
		}
		if catalog.kind == "source" {
			primary = []ui.ActionBarButton{button("Add source", "add")}
			if selected, ok := catalog.selectedStoreEntry(); ok {
				source := catalog.owner.model.capabilities.Sources[selected.ID]
				if source.Builtin {
					primary = append(primary, button(mapState(source.Disabled, "Enable", "Disable"), mapState(source.Disabled, "enable", "disable")))
				} else {
					primary = append(primary, button("Remove", "remove-source"))
				}
			}
		}
		secondary = append(secondary, button("Refresh", "snapshot"))
		if catalog.kind == "source" {
			secondary = []ui.ActionBarButton{button("Reset inherited", "reset-inherited"), button("Refresh", "snapshot")}
		}
	}
	if catalog.owner.model.capabilityPending != nil {
		secondary = append(secondary, button("Cancel operation", "cancel"))
	}
	selected := catalog.id != ""
	if !catalog.detail && catalog.edit == "" {
		_, selected = catalog.selectedStoreEntry()
	}
	for index := range primary {
		operation := strings.TrimPrefix(primary[index].Action, "settings.capability.")
		if slices.Contains([]string{"inspect", "edit", "install", "remove-source"}, operation) {
			primary[index].Enabled = selected
		}
	}
	for _, buttons := range [][]ui.ActionBarButton{primary, secondary} {
		for index := range buttons {
			operation := strings.TrimPrefix(buttons[index].Action, "settings.capability.")
			if catalog.owner.model.capabilityPending != nil && !slices.Contains([]string{"back", "cancel", "snapshot", "auth-return", "open-auth-url", "sources", "acquisition"}, operation) {
				buttons[index].Enabled = false
			}
			if operation == "reset-inherited" {
				buttons[index].Enabled = selected && catalog.inheritedAvailable() && catalog.owner.model.capabilityPending == nil
			}
			if operation == "remove" || operation == "remove-source" {
				buttons[index].Variant = ui.VariantDanger
			}
		}
	}
	catalog.layoutActions(append(primary, secondary...))
}

func (catalog *capabilityCatalog) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}
func (catalog *capabilityCatalog) Layout(ctx ui.LayoutContext, rect ui.Rect) {
	catalog.SetBounds(rect)
	catalog.syncModel()
	if rect.Empty() {
		for _, child := range []ui.Component{catalog.tabs, catalog.filter, catalog.storeFilters, catalog.installed, catalog.searchButton, catalog.choices, catalog.choiceScroll, catalog.store, catalog.browse, catalog.tree, catalog.form, catalog.reader, catalog.storeScroll, catalog.formScroll, catalog.browseScroll, catalog.treeScroll, catalog.bar, catalog.more} {
			child.Layout(ctx, ui.Rect{})
		}
		for _, bar := range catalog.extraBars {
			bar.Layout(ctx, ui.Rect{})
		}
		return
	}
	catalog.consumeResult()
	if catalog.seen != catalog.owner.model.capabilityRevision {
		catalog.saveListState()
		if catalog.detail {
			catalog.syncDetailRows()
		} else if catalog.edit == "" {
			catalog.syncRows()
		}
		catalog.seen = catalog.owner.model.capabilityRevision
	}
	catalog.actions()
	if catalog.edit != "" || !catalog.detail {
		catalog.tabs.Layout(ctx, ui.Rect{})
	}
	if catalog.edit != "" || catalog.detail && !catalog.detailCollection() {
		catalog.filter.Layout(ctx, ui.Rect{})
	}
	if catalog.edit != "" || catalog.detail {
		catalog.store.Layout(ctx, ui.Rect{})
		catalog.storeFilters.Layout(ctx, ui.Rect{})
		catalog.storeScroll.Layout(ctx, ui.Rect{})
		catalog.installed.Layout(ctx, ui.Rect{})
		catalog.searchButton.Layout(ctx, ui.Rect{})
	}
	catalog.choices.Layout(ctx, ui.Rect{})
	catalog.choiceScroll.Layout(ctx, ui.Rect{})
	if catalog.edit != "" || !catalog.detail || !catalog.detailCollection() {
		catalog.browse.Layout(ctx, ui.Rect{})
		catalog.browseScroll.Layout(ctx, ui.Rect{})
	}
	if catalog.edit != "" || !catalog.detail || catalog.kind != captypes.KindSkill || catalog.detailPage != "files" {
		catalog.tree.Layout(ctx, ui.Rect{})
		catalog.treeScroll.Layout(ctx, ui.Rect{})
	}
	if catalog.edit != "" || !catalog.detail {
		catalog.reader.Layout(ctx, ui.Rect{})
	}
	if catalog.edit == "" && !catalog.detail {
		catalog.form.Layout(ctx, ui.Rect{})
	}
	catalog.formScroll.Layout(ctx, ui.Rect{})
	bars := catalog.actionBars()
	actionHeight := min(len(bars), max(0, rect.H-2))
	bottom := max(1, rect.H-actionHeight)
	catalog.statusY = max(0, bottom-1)
	for index, bar := range bars {
		if index < actionHeight {
			bar.Layout(ctx, ui.Rect{Y: bottom + index, W: rect.W, H: 1})
		} else {
			bar.Layout(ctx, ui.Rect{})
		}
	}
	contentY := 2
	if catalog.edit == "" && catalog.detail {
		catalog.tabs.Layout(ctx, ui.Rect{Y: 1, W: rect.W, H: min(ui.StripHeight, max(0, catalog.statusY-1))})
		contentY = 1 + ui.StripHeight
	}
	height := max(0, catalog.statusY-contentY)
	if catalog.edit != "" {
		catalog.layoutForm(ctx, ui.Rect{Y: contentY, W: rect.W, H: height})
	} else if catalog.detail {
		catalog.layoutDetail(ctx, contentY, height)
	} else {
		catalog.layoutStoreBrowse(ctx, contentY, height)
	}
}

func (catalog *capabilityCatalog) title() string {
	switch catalog.kind {
	case captypes.KindSkill:
		return "Skills"
	case captypes.KindMCP:
		return "MCP"
	default:
		return "Sources"
	}
}

func (catalog *capabilityCatalog) Paint(buffer *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	if catalog.Bounds().Empty() {
		return
	}
	rect := catalog.Bounds()
	ui.FillRect(buffer, originX, originY, rect, cell.BlankCell(catalog.tokens.Color(ui.ColorPopupBg)))
	buffer.WriteString(originX+rect.X, originY+rect.Y, rendering.TruncateVisual(catalog.owner.PaneTitle(), rect.W, "…"), catalog.tokens.Color(ui.ColorAccent), catalog.tokens.Color(ui.ColorPopupBg), cell.AttrBold)
	message := catalog.message
	if catalog.owner.model.capabilityPending != nil {
		message = catalog.owner.model.capabilityPending.Operation + " running — Cancel stops this operation"
	}
	if catalog.edit == "" && !catalog.detail {
		catalog.paintStoreHeading(buffer, originX+rect.X, originY+rect.Y, rect.W)
		catalog.paintStoreEmpty(buffer, originX+rect.X, originY+rect.Y)
		if message == "" {
			message = catalog.storeStatus()
		}
	}
	buffer.WriteString(originX+rect.X, originY+rect.Y+catalog.statusY, rendering.TruncateVisual(strings.ReplaceAll(message, "\n", " "), rect.W, "…"), catalog.tokens.Color(ui.ColorMuted), catalog.tokens.Color(ui.ColorPopupBg), 0)
}

func (catalog *capabilityCatalog) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: catalog.title()}
}

func (catalog *capabilityCatalog) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if catalog.owner != nil && catalog.owner.model != nil && catalog.owner.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	if event.Kind == ui.EventMouseClick && event.MouseClick.Button == tea.MouseLeft && ctx.LocalY == 0 && (catalog.detail || catalog.edit != "") {
		if ui.BreadcrumbIndex(catalog.owner.paneTitleParts(), catalog.owner.breadcrumbSeparator(), ctx.LocalX, catalog.Bounds().W) == 0 {
			return catalog.back()
		}
	}
	if event.IsKey() && event.Key.Code == tea.KeyEsc {
		return catalog.back()
	}
	if event.IsKey() && event.Key.Code == tea.KeyTab {
		catalog.moveBrowseFocus(event.Key.Mod.Contains(tea.ModShift))
		return catalog.changed()
	}
	return ui.EventResult{}
}

func (catalog *capabilityCatalog) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if catalog.owner != nil && catalog.owner.model != nil && catalog.owner.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	catalog.consumeResult()
	if action.ID == "tabs.activate" || action.ID == "tabs.enter" {
		id, _ := action.Payload.(string)
		if id != "" && catalog.detail {
			if catalog.edit != "" {
				return catalog.changed()
			}
			catalog.saveListState()
			catalog.detailForm(id)
			if action.ID == "tabs.enter" {
				catalog.focusContent()
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
	}
	switch action.ID {
	case "tree.activate":
		catalog.selectFile()
		catalog.owner.focusID(catalog.form.ID())
		return catalog.changed()
	case "form.cancel":
		return catalog.back()
	case "form.changed":
		catalog.actions()
		return catalog.changed()
	case "form.select.open":
		return catalog.changed()
	case "form.exit.forward":
		if catalog.detail && catalog.edit == "" {
			catalog.owner.focusID(catalog.reader.ID())
		} else {
			catalog.owner.focusID(catalog.bar.ID())
		}
		return catalog.changed()
	case "form.exit.backward":
		catalog.owner.focusID(catalog.tabs.ID())
		return catalog.changed()
	}
	if !strings.HasPrefix(string(action.ID), "settings.capability.") {
		return ui.EventResult{Action: &action}
	}
	operation := strings.TrimPrefix(string(action.ID), "settings.capability.")
	if strings.HasPrefix(operation, "actions-next/") || strings.HasPrefix(operation, "actions-prev/") {
		parts := strings.Split(operation, "/")
		index, _ := strconv.Atoi(parts[1])
		catalog.moveActionFocus(index, parts[0] == "actions-next")
		return catalog.changed()
	}
	model := catalog.owner.model
	selected, hasSelected := catalog.selectedStoreEntry()
	if hasSelected && !catalog.detail && catalog.edit == "" {
		catalog.id = selected.ID
	}
	switch operation {
	case "install", "update":
		if catalog.id == "" {
			return catalog.changed()
		}
		if operation == "install" && (catalog.entry().Installed || catalog.entry().State == "unsupported") {
			catalog.message = mapDefault(catalog.entry().Reason, "This item is already installed. Open its details to manage it.")
			return catalog.changed()
		}
		catalog.acquisition = nil
		catalog.acquisitionReturnToStore = !catalog.detail
		return catalog.submit(captypes.Action{Operation: "preview-" + operation, Kind: catalog.kind, ID: catalog.id})
	case "confirm-acquisition":
		preview := catalog.acquisition
		if preview == nil || preview.ID != catalog.id || preview.Scope != model.capabilityScope() {
			catalog.message = "Installation preview is unavailable; inspect and review again."
			return catalog.changed()
		}
		return catalog.submit(captypes.Action{Operation: preview.Operation, Kind: preview.Kind, ID: preview.ID, Preview: preview})
	case "confirm-remove":
		request, ok := action.Payload.(captypes.Action)
		if !ok {
			return catalog.changed()
		}
		if request.Kind != catalog.kind || request.ID != catalog.id {
			return catalog.changed()
		}
		return catalog.submit(request)
	case "back":
		return catalog.back()
	case "sources":
		if catalog.dirty() {
			model.nextCategory = catSources
			return catalog.owner.requestPrompt(PromptDiscardEditor, "", "", 0)
		}
		catalog.closeEditor()
		model.SelectCategory(catSources)
		catalog.switchKind("source")
		catalog.detail = false
		catalog.syncRows()
		return catalog.changed()
	case "acquisition":
		if catalog.dirty() {
			model.nextCategory = catAcquisition
			return catalog.owner.requestPrompt(PromptDiscardEditor, "", "", 0)
		}
		catalog.closeEditor()
		model.SelectCategory(catAcquisition)
		catalog.owner.refreshForm()
		return catalog.changed()
	case "inspect":
		if !hasSelected {
			return catalog.changed()
		}
		catalog.saveListState()
		if catalog.kind == "source" {
			catalog.id = selected.ID
			catalog.edit = "edit"
			catalog.editForm()
			catalog.owner.focusID(catalog.form.ID())
			return catalog.changed()
		}
		catalog.detail = true
		catalog.id = selected.ID
		catalog.detailForm("overview")
		catalog.focusContent()
		return catalog.changed()
	case "edit", "add", "add-local", "create-skill", "import", "make-local-copy", "review-grant":
		if operation == "edit" && catalog.id == "" {
			return catalog.changed()
		}
		if catalog.dirty() {
			return catalog.owner.requestPrompt(PromptDiscardEditor, "", "", 0)
		}
		if operation == "review-grant" {
			return catalog.beginGrant()
		}
		if operation != "edit" && operation != "make-local-copy" {
			catalog.id = ""
		}
		catalog.edit = operation
		catalog.editForm()
		catalog.owner.focusID(catalog.form.ID())
		return catalog.changed()
	case "save", "test":
		request, err := catalog.draftAction()
		if err != nil {
			catalog.message = err.Error()
			if draft := model.capabilityDraft; draft != nil {
				for _, field := range draft.Fields() {
					if field.HasErrors() {
						catalog.form.FocusField(field.ID)
						catalog.owner.focusID(catalog.form.ID())
						break
					}
				}
			}
			return catalog.changed()
		}
		if operation == "test" {
			request.Operation = "test"
		}
		return catalog.submit(request)
	case "remove", "remove-source", "reset-inherited":
		if catalog.id == "" || operation == "reset-inherited" && !catalog.inheritedAvailable() {
			return catalog.changed()
		}
		request := captypes.Action{Operation: operation, Kind: catalog.kind, ID: catalog.id, Scope: model.capabilityScope()}
		preview := catalog.entityBytes()
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg {
			return PromptMsg{Target: model, Kind: PromptRemoveCapability, ID: catalog.id, Label: operation + " " + catalog.id + " at " + string(model.capabilityScope()), CapabilityAction: &request, capabilityDraft: preview, capabilityEditor: catalog}
		}}
	case "use":
		return catalog.submit(captypes.Action{Operation: "use", Kind: catalog.kind, ID: catalog.id})
	case "open-auth-url":
		return catalog.submit(captypes.Action{Operation: "open-auth-url", Kind: catalog.kind, ID: catalog.id, Content: catalog.connectionState().AuthURL})
	case "enable":
		if catalog.kind == captypes.KindSkill {
			if skill, exists := model.capabilities.Skills[catalog.id]; exists && skill.Mode == "disabled" {
				skill.Mode, skill.Disabled = "manual", false
				return catalog.submit(captypes.Action{Operation: "save-skill", Kind: catalog.kind, ID: catalog.id, Skill: &skill})
			}
		}
	case "search":
		if catalog.detail {
			catalog.owner.focusID(catalog.browse.ID())
			return catalog.changed()
		}
		return catalog.searchStore()
	case "installed-only":
		state := catalog.views[catalog.viewKey()]
		state.show = mapState(state.show == "installed", "all", "installed")
		state.selected, state.offset = "", 0
		catalog.views[catalog.viewKey()] = state
		catalog.syncRows()
		return catalog.changed()
	case "filter-source", "filter-type", "filter-sort":
		return catalog.openStoreFilter(operation)
	case "choose":
		return catalog.chooseStoreFilter()
	case "close-filter":
		catalog.choosing = ""
		catalog.owner.focusID(catalog.storeFilters.ID())
		return catalog.changed()
	case "filters-next", "search-next":
		catalog.owner.focusID(catalog.store.ID())
		return catalog.changed()
	case "filters-prev":
		catalog.owner.focusID(catalog.installed.ID())
		return catalog.changed()
	case "search-prev":
		catalog.owner.focusID(catalog.filter.ID())
		return catalog.changed()
	case "inspect-item":
		catalog.inspectItem()
		catalog.focusDetailInput()
		return catalog.changed()
	case "select-file":
		catalog.selectFile()
		catalog.owner.focusID(catalog.form.ID())
		return catalog.changed()
	case "read-file", "list-files", "resource", "prompt", "stage-context", "open-file", "auth-return", "save-mode":
		return catalog.detailOperation(operation)
	}
	return catalog.submit(captypes.Action{Operation: operation, Kind: catalog.kind, ID: catalog.id, Query: catalog.filter.Value()})
}

func (catalog *capabilityCatalog) changed() ui.EventResult {
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateLayout | ui.InvalidateFocus}
}
func (catalog *capabilityCatalog) dirty() bool {
	return catalog.owner.model.capabilityDraft != nil && !reflect.DeepEqual(catalog.baseline, formDraftBytes(catalog.owner.model.capabilityDraft))
}
func (catalog *capabilityCatalog) back() ui.EventResult {
	if catalog.choosing != "" {
		catalog.choosing = ""
		catalog.owner.focusID(catalog.storeFilters.ID())
		return catalog.changed()
	}
	if catalog.edit == "" && !catalog.detail {
		return catalog.owner.cancelSettings()
	}
	if catalog.dirty() {
		return catalog.owner.requestPrompt(PromptDiscardEditor, "", "", 0)
	}
	if catalog.edit != "" && catalog.detail {
		catalog.closeEditor()
		catalog.detailForm(catalog.detailPage)
		return catalog.changed()
	}
	catalog.saveListState()
	catalog.closeEditor()
	catalog.id = ""
	catalog.detail = false
	catalog.setTabs()
	catalog.restoreFilter()
	catalog.syncRows()
	catalog.owner.focusID(catalog.store.ID())
	return catalog.changed()
}

func (catalog *capabilityCatalog) editForm() {
	model := catalog.owner.model
	var fields []*ui.FieldState
	if catalog.edit == "import" {
		fields = []*ui.FieldState{ui.TextAreaFieldState("import", "MCP definitions", "", "Paste a supported mcpServers or servers object. Secrets use environment references.")}
	} else if catalog.edit == "make-local-copy" {
		fields = []*ui.FieldState{ui.TextFieldState("id", "Local identity", "", "A new source-qualified registration; the managed artifact stays immutable."), ui.TextFieldState("name", "Local name", "", "Must match the destination directory name."), ui.TextFieldState("path", "Destination", "", "A new authored directory, outside the managed artifact.")}
	} else if catalog.kind == captypes.KindSkill {
		value := model.capabilities.Skills[catalog.id]
		catalog.draftOriginal = captypes.Action{Kind: catalog.kind, ID: catalog.id, Skill: &value}
		fields = []*ui.FieldState{ui.TextFieldState("id", "Identity", value.ID, "Source-qualified identity"), ui.TextFieldState("name", "Name", value.Name, "Lowercase skill directory name"), ui.TextFieldState("path", "Directory", value.Path, "Authored directories are never deleted by Remove"), ui.CycleFieldState("mode", "Invocation", []string{"automatic", "manual", "disabled"}, mapDefault(value.Mode, "automatic"), "Selection is not permission"), ui.TextAreaFieldState("description", "Description", "", "Required for Create skill")}
		if value.Managed {
			fields[2].ReadOnly = true
			fields[2].Description = "Managed bytes are immutable. Make local copy before editing files."
		}
	} else if catalog.kind == "source" {
		value := model.capabilities.Sources[catalog.id]
		catalog.draftOriginal = captypes.Action{Kind: catalog.kind, ID: catalog.id, Source: &value}
		fields = []*ui.FieldState{ui.TextFieldState("id", "Identity", value.ID, ""), ui.TextFieldState("name", "Name", value.Name, ""), ui.CycleFieldState("kind", "Source", []string{"local", "repository", "registry", "skill-registry"}, mapDefault(value.Kind, "local"), ""), ui.TextFieldState("location", "Location", value.Location, "HTTPS MCP/skill registry, pinned repository, or local directory"), ui.TextFieldState("revision", "Pinned revision", value.Revision, "Repositories require a full commit digest"), ui.TextFieldState("digest", "Artifact digest", value.Digest, "Exact SHA-256 when the source requires artifact verification."), ui.BoolFieldState("trusted", "Approved acquisition", value.Trusted, "Publisher provenance is not a code safety certificate"), ui.BoolFieldState("disabled", "Disabled", value.Disabled, "Do not search or acquire from this source.")}
	} else {
		value := captypes.Clone(model.capabilities).Connections[catalog.id]
		catalog.draftOriginal = captypes.Action{Kind: catalog.kind, ID: catalog.id, Connection: &value}
		args, _ := json.Marshal(value.Args)
		environment, _ := json.Marshal(value.Environment)
		headers, _ := json.Marshal(value.Headers)
		secrets, _ := json.Marshal(value.SecretRefs)
		environmentDescription := "Explicit non-secret values only"
		if len(value.RequiredEnv) > 0 {
			environmentDescription += ". Required by this package: " + strings.Join(value.RequiredEnv, ", ")
		}
		fields = []*ui.FieldState{ui.TextFieldState("id", "Identity", value.ID, "Stable connection identity"), ui.TextFieldState("name", "Name", value.Name, ""), ui.CycleFieldState("transport", "Transport", []string{"http", "stdio"}, mapDefault(value.Transport, "http"), "Streamable HTTP or a managed local process"), ui.TextFieldState("url", "Endpoint", value.URL, "Credentials are endpoint-bound"), ui.TextFieldState("command", "Executable", value.Command, "No shell interpolation"), ui.TextAreaFieldState("args", "Arguments", string(args), "JSON array, exact argument boundaries"), ui.TextFieldState("directory", "Working directory", value.Directory, ""), ui.TextAreaFieldState("environment", "Environment", string(environment), environmentDescription), ui.TextAreaFieldState("headers", "Headers", string(headers), "Non-secret headers"), ui.TextAreaFieldState("secrets", "Secret references", string(secrets), "Map header/env name to env:VARIABLE; credentials remain application-owned"), ui.CycleFieldState("auth", "Authentication", []string{"none", "oauth"}, mapDefault(value.Auth, "none"), "Sign in is explicit"), ui.TextFieldState("oauth-client", "OAuth client", value.OAuthClientID, "Blank uses dynamic registration"), ui.BoolFieldState("launch", "Approve local launch", value.LaunchApproved, "Unsandboxed launch exposes this executable to your user privileges; automatic acquisition cannot start it")}
		fields = append(fields, capabilityIntField("timeout", "Timeout (seconds)", int64(max(1, value.TimeoutSeconds)), 1, 3600, "Bounded operations. Unknown remote mutations are never retried automatically."), ui.TextAreaFieldState("oauth-scopes", "OAuth scopes", strings.Join(value.OAuthScopes, "\n"), "One scope per line; signing in stays application-owned."))
	}
	if catalog.id != "" && catalog.edit == "edit" {
		fields[0].ReadOnly = true
	}
	if catalog.edit == "create-skill" {
		for _, field := range fields {
			if field.ID == "description" {
				field.Required = true
			}
		}
	}
	if catalog.edit == "import" {
		fields[0].Required = true
	}
	for _, field := range fields {
		if slices.Contains([]string{"id", "name", "path", "location"}, field.ID) {
			field.Required = true
		}
		if slices.Contains([]string{"args", "environment", "headers", "secrets"}, field.ID) {
			id := field.ID
			field.Validator = func(value ui.FieldValue) []string {
				var target any = &map[string]string{}
				if id == "args" {
					target = &[]string{}
				}
				if err := json.Unmarshal([]byte(mapDefault(strings.TrimSpace(value.String()), "null")), target); err != nil {
					return []string{"Enter a valid JSON " + mapState(id == "args", "string array", "string map") + "."}
				}
				return nil
			}
		}
	}
	model.capabilityDraft = formModel(fields...)
	catalog.form.SetForm(model.capabilityDraft, "", "")
	catalog.baseline = formDraftBytes(model.capabilityDraft)
	catalog.actions()
}

func mapDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func (catalog *capabilityCatalog) draftAction() (captypes.Action, error) {
	form := catalog.owner.model.capabilityDraft
	if form == nil {
		return captypes.Action{}, fmt.Errorf("open an editor before saving")
	}
	form.ValidateAll()
	if form.AnyErrors() {
		return captypes.Action{}, fmt.Errorf("correct the marked fields before saving")
	}
	value := func(id string) string {
		field := form.Field(id)
		if field == nil {
			return ""
		}
		return field.Value.String()
	}
	request := captypes.Action{Kind: catalog.kind, ID: catalog.id}
	if catalog.edit == "grant" {
		return catalog.grantAction()
	}
	if catalog.edit == "import" {
		request.Operation, request.Content = "import", value("import")
		return request, nil
	}
	if value("id") == "" || value("name") == "" {
		return request, fmt.Errorf("identity and name are required")
	}
	switch catalog.kind {
	case captypes.KindSkill:
		skill := catalog.owner.model.capabilities.Skills[catalog.id]
		if catalog.edit == "make-local-copy" {
			skill = captypes.SkillConfig{}
		}
		skill.ID, skill.Name, skill.Path, skill.Mode = value("id"), value("name"), value("path"), mapDefault(value("mode"), "manual")
		skill.Disabled = skill.Mode == "disabled"
		request.Operation = "save-skill"
		if catalog.edit == "create-skill" {
			request.Operation = "create-skill"
		}
		if catalog.edit == "make-local-copy" {
			request.Operation = "make-local-copy"
		}
		if catalog.edit == "add-local" {
			request.Operation = "add-local"
		}
		request.Skill, request.Content = &skill, value("description")
	case "source":
		request.Operation = "save-source"
		source := catalog.owner.model.capabilities.Sources[catalog.id]
		source.ID, source.Name, source.Kind, source.Location = value("id"), value("name"), value("kind"), value("location")
		source.Revision, source.Digest = value("revision"), value("digest")
		source.Trusted, source.Disabled = ui.BoolFieldValue(form.Field("trusted")), ui.BoolFieldValue(form.Field("disabled"))
		request.Source = &source
	default:
		connection := captypes.Clone(catalog.owner.model.capabilities).Connections[catalog.id]
		if catalog.draftOriginal.Connection != nil {
			connection = captypes.Clone(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{catalog.id: *catalog.draftOriginal.Connection}}).Connections[catalog.id]
		}
		connection.ID, connection.Name, connection.Transport, connection.URL, connection.Command, connection.Directory, connection.Auth, connection.OAuthClientID = value("id"), value("name"), value("transport"), value("url"), value("command"), value("directory"), value("auth"), value("oauth-client")
		connection.LaunchApproved = ui.BoolFieldValue(form.Field("launch"))
		connection.TimeoutSeconds, _ = strconv.Atoi(value("timeout"))
		connection.OAuthScopes = strings.Fields(value("oauth-scopes"))
		for _, entry := range []struct {
			raw    string
			target any
		}{{value("args"), &connection.Args}, {value("environment"), &connection.Environment}, {value("headers"), &connection.Headers}, {value("secrets"), &connection.SecretRefs}} {
			if err := json.Unmarshal([]byte(mapDefault(entry.raw, "null")), entry.target); err != nil {
				return request, fmt.Errorf("invalid JSON in connection arguments, environment, headers or references: %w", err)
			}
		}
		request.Operation, request.Connection = "save-connection", &connection
	}
	return request, nil
}

func (catalog *capabilityCatalog) detailForm(page string) {
	catalog.detailPage = page
	catalog.setTabs()
	catalog.restoreFilter()
	key := catalog.viewKey()
	form := catalog.forms[key]
	if form == nil {
		form = catalog.buildDetailForm()
		catalog.forms[key] = form
	}
	catalog.form.SetForm(form, "", "")
	if catalog.kind == captypes.KindSkill && page == "access" && catalog.owner.model.capabilityDraft != form {
		catalog.owner.model.capabilityDraft = form
		catalog.baseline = formDraftBytes(form)
	}
	catalog.syncDetailRows()
	catalog.actions()
}

func (surface *WorkspaceSettingsSurface) OpenCapabilities(kind captypes.Kind, query, id string) ui.EventResult {
	if surface.model.accountEditorDirty() || surface.model.providerEditorDirty() || surface.capCatalog.dirty() {
		surface.capCatalog.nextOpen = &capabilityNavigation{kind: kind, query: query, id: id}
		return surface.requestPrompt(PromptDiscardEditor, "", "", 0)
	}
	if surface.agents.draft != nil && surface.agents.dirty() {
		surface.capCatalog.nextOpen = &capabilityNavigation{kind: kind, query: query, id: id}
		return surface.agents.back()
	}
	if surface.agents.draft != nil {
		surface.agents.closeDraft()
	}
	if surface.model.accountEditor != nil {
		surface.model.cancelAccountEdit()
	}
	if surface.model.providerEditor != nil {
		surface.model.cancelProviderEdit()
	}
	index := catSkills
	if kind == captypes.KindMCP {
		index = catMCP
	}
	surface.model.SelectCategory(index)
	catalog := surface.capCatalog
	catalog.syncModel()
	catalog.closeEditor()
	catalog.switchKind(kind)
	catalog.id, catalog.detail, catalog.edit = id, id != "", ""
	state := catalog.views[catalog.viewKey()]
	state.query = query
	catalog.views[catalog.viewKey()] = state
	catalog.restoreFilter()
	catalog.syncRows()
	if id != "" {
		catalog.detailForm("overview")
	}
	surface.refreshForm()
	catalog.focusContent()
	return catalog.submit(captypes.Action{Operation: "snapshot", Kind: kind})
}
