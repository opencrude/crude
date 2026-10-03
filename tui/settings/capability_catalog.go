package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
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
const capabilityTableID ui.ComponentID = "settings/capabilities/table"
const capabilityFormID ui.ComponentID = "settings/capabilities/form"
const capabilityTabsID ui.ComponentID = "settings/capabilities/tabs"

type capabilityCatalog struct {
	ui.Base
	owner         *WorkspaceSettingsSurface
	tokens        *ui.TokenSet
	tabs          *ui.Tabs
	filter        *ui.FilterField
	table         *ui.Table
	browse        *ui.Table
	tree          *ui.Tree
	form          *ui.Form
	reader        *ui.TextArea
	bar           *ui.ActionBar
	more          *ui.ActionBar
	scroll        *ui.ScrollbarControl
	formScroll    *ui.ScrollbarControl
	browseScroll  *ui.ScrollbarControl
	treeScroll    *ui.ScrollbarControl
	kind          captypes.Kind
	page          string
	id            string
	detail        bool
	edit          string
	baseline      []byte
	seen          uint64
	message       string
	detailPage    string
	forms         map[string]*ui.FormModel
	inputForms    map[string]*ui.FormModel
	views         map[string]capabilityListState
	pages         map[captypes.Kind]string
	requests      map[uint64]capabilityViewRequest
	preview       map[string]CapabilityResultMsg
	files         map[string][]string
	extraBars     []*ui.ActionBar
	statusY       int
	resultSeen    uint64
	model         *Model
	draftOriginal captypes.Action
	nextOpen      *capabilityNavigation
	acquisition   *captypes.AcquisitionPreview
}

type capabilityListState struct {
	query, selected string
	offset          int
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
	catalog := &capabilityCatalog{Base: ui.NewBase(capabilityCatalogID), owner: owner, model: owner.model, tokens: owner.tokens, kind: captypes.KindSkill, page: "installed", forms: make(map[string]*ui.FormModel), inputForms: make(map[string]*ui.FormModel), views: make(map[string]capabilityListState), pages: make(map[captypes.Kind]string), requests: make(map[uint64]capabilityViewRequest), preview: make(map[string]CapabilityResultMsg), files: make(map[string][]string)}
	catalog.tabs = ui.NewTabs(capabilityTabsID, owner.tokens)
	catalog.tabs.SetChrome(ui.TabsPopup)
	catalog.tabs.SetItems([]ui.TabItem{{ID: "installed", Label: "Installed"}, {ID: "discover", Label: "Discover"}}, "installed")
	catalog.filter = ui.NewFilterField(capabilityFilterID, owner.tokens)
	catalog.filter.SetPrompt(ui.SearchGlyphEnd(owner.tokens))
	catalog.filter.SetPromptPlacement(ui.PromptEnd)
	catalog.filter.SetSubmitAction("settings.capability.focus-table")
	catalog.filter.OnChanged(func(query string) {
		state := catalog.views[catalog.viewKey()]
		state.query = query
		catalog.views[catalog.viewKey()] = state
		if catalog.detail {
			catalog.syncDetailRows()
		} else {
			catalog.syncRows()
		}
	})
	catalog.table = ui.NewTable(capabilityTableID, owner.tokens)
	catalog.table.SetShowHeader(true)
	catalog.table.SetActivateAction("settings.capability.inspect")
	catalog.table.SetColumnGap(2)
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
	catalog.scroll = ui.NewScrollbarControl("settings/capabilities/scroll", catalog.table.Scroll(), owner.tokens)
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
	catalog.table.SetTokens(tokens)
	catalog.browse.SetTokens(tokens)
	catalog.tree.SetTokens(tokens)
	catalog.form.SetTokens(tokens)
	catalog.reader.SetTokens(tokens)
	catalog.bar.SetTokens(tokens)
	catalog.more.SetTokens(tokens)
	catalog.scroll.SetTokens(tokens)
	catalog.formScroll.SetTokens(tokens)
	catalog.browseScroll.SetTokens(tokens)
	catalog.treeScroll.SetTokens(tokens)
	for _, bar := range catalog.extraBars {
		bar.SetTokens(tokens)
	}
}

func (catalog *capabilityCatalog) Children() []ui.Component {
	children := []ui.Component{catalog.tabs, catalog.filter, catalog.table, catalog.browse, catalog.tree, catalog.form, catalog.reader}
	if catalog.Bounds().Empty() {
		return children
	}
	children = append(children, catalog.scroll, catalog.formScroll, catalog.browseScroll, catalog.treeScroll, catalog.bar, catalog.more)
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
	if catalog.page == "discover" {
		entries = model.capabilityDiscovery[catalog.kind]
	} else if catalog.kind == "source" {
		for id, source := range model.capabilities.Sources {
			entries = append(entries, captypes.Entry{ID: id, Name: source.Name, Source: source.Kind, Path: source.Location, State: mapState(source.Disabled, "disabled", "configured"), Origin: source.Scope})
		}
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
					entries = append(entries, captypes.Entry{ID: id, Name: skill.Name, Kind: catalog.kind, Source: skill.Source, State: mapState(skill.Disabled, "disabled", "installed"), Mode: skill.Mode, Origin: skill.Scope, Path: skill.Path})
				}
			}
			if catalog.kind == captypes.KindMCP {
				for id, connection := range model.capabilities.Connections {
					entries = append(entries, captypes.Entry{ID: id, Name: connection.Name, Kind: catalog.kind, Source: connection.Transport, State: mapState(connection.Disabled, "disabled", "not started"), Origin: connection.Scope})
				}
			}
		}
	}
	sort.Slice(entries, func(first, second int) bool { return entries[first].Name < entries[second].Name })
	rows := make([]ui.TableRow, 0, len(entries))
	query := strings.ToLower(state.query)
	for _, entry := range entries {
		if !strings.Contains(strings.ToLower(entry.Name+" "+entry.ID+" "+entry.Source+" "+entry.Description+" "+entry.State), query) {
			continue
		}
		origin := string(entry.Origin)
		rows = append(rows, ui.TableRow{ID: entry.ID, Payload: entry, Cells: []ui.TableCell{{Text: entry.Name}, {Text: entry.Source, Role: ui.ColorMuted}, {Text: origin, Role: ui.ColorMuted}, {Text: strings.TrimSpace(entry.Mode + " " + entry.State), Role: ui.ColorMuted}}})
	}
	oldOffset := catalog.table.Scroll().Offset()
	catalog.table.SetColumns([]ui.TableColumn{{ID: "name", Label: "Name", Track: ui.FractionGridTrack(3)}, {ID: "source", Label: "Source", Track: ui.FractionGridTrack(2)}, {ID: "origin", Label: "Scope", Track: ui.FractionGridTrack(2)}, {ID: "state", Label: "Use / state", Track: ui.FractionGridTrack(3)}})
	catalog.table.SetRows(rows)
	for index, row := range rows {
		if row.ID == state.selected {
			catalog.table.SelectIndex(index)
			break
		}
	}
	if state.selected != "" {
		oldOffset = state.offset
	}
	catalog.table.Scroll().ScrollTo(oldOffset)
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
	} else if catalog.page == "discover" {
		primary = []ui.ActionBarButton{button("Search sources", "discover"), button("Inspect", "inspect"), button("Install", "install")}
	} else {
		if catalog.kind == captypes.KindSkill {
			primary = []ui.ActionBarButton{button("Create", "create-skill"), button("Add local", "add-local"), button("Inspect", "inspect")}
			if selected, ok := catalog.table.Selected(); ok {
				skill := catalog.owner.model.capabilities.Skills[selected.ID]
				secondary = append(secondary, button(mapState(skill.Disabled || skill.Mode == "disabled", "Enable", "Disable"), mapState(skill.Disabled || skill.Mode == "disabled", "enable", "disable")), button("Remove", "remove"))
				if skill.Source != "" {
					secondary = append(secondary, button("Update", "update"))
				}
			}
		}
		if catalog.kind == captypes.KindMCP {
			primary = []ui.ActionBarButton{button("Add", "add"), button("Import", "import"), button("Inspect", "inspect")}
		}
		if catalog.kind == "source" {
			primary = []ui.ActionBarButton{button("Add source", "add"), button("Edit", "edit"), button("Remove", "remove-source")}
		}
		secondary = append(secondary, button("Sources", "sources"), button("Acquisition", "acquisition"), button("Refresh", "snapshot"))
		if catalog.kind == "source" {
			secondary = []ui.ActionBarButton{button("Reset inherited", "reset-inherited"), button("Refresh", "snapshot")}
		}
	}
	if catalog.owner.model.capabilityPending != nil {
		secondary = append(secondary, button("Cancel operation", "cancel"))
	}
	selected := catalog.id != ""
	if !catalog.detail && catalog.edit == "" {
		_, selected = catalog.table.Selected()
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
		for _, child := range []ui.Component{catalog.tabs, catalog.filter, catalog.table, catalog.browse, catalog.tree, catalog.form, catalog.reader, catalog.scroll, catalog.formScroll, catalog.browseScroll, catalog.treeScroll, catalog.bar, catalog.more} {
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
	if catalog.edit != "" {
		catalog.tabs.Layout(ctx, ui.Rect{})
	}
	if catalog.edit != "" || catalog.detail && !catalog.detailCollection() {
		catalog.filter.Layout(ctx, ui.Rect{})
	}
	if catalog.edit != "" || catalog.detail {
		catalog.table.Layout(ctx, ui.Rect{})
		catalog.scroll.Layout(ctx, ui.Rect{})
	}
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
	if catalog.edit == "" {
		catalog.tabs.Layout(ctx, ui.Rect{Y: 1, W: rect.W, H: min(ui.StripHeight, max(0, catalog.statusY-1))})
		contentY = 1 + ui.StripHeight
	}
	height := max(0, catalog.statusY-contentY)
	if catalog.edit != "" {
		catalog.layoutForm(ctx, ui.Rect{Y: contentY, W: rect.W, H: height})
	} else if catalog.detail {
		catalog.layoutDetail(ctx, contentY, height)
	} else {
		catalog.filter.Layout(ctx, ui.Rect{Y: contentY, W: rect.W, H: min(1, height)})
		catalog.filter.SetPlaceholder("Search " + catalog.title())
		catalog.layoutTable(ctx, catalog.table, catalog.scroll, ui.Rect{Y: contentY + 2, W: rect.W, H: max(0, height-2)})
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
	if catalog.edit == "" && !catalog.detail && len(catalog.table.Rows()) == 0 && message == "" {
		message = "No matches. Discover searches configured sources only on request."
	}
	buffer.WriteString(originX+rect.X, originY+rect.Y+catalog.statusY, rendering.TruncateVisual(strings.ReplaceAll(message, "\n", " "), rect.W, "…"), catalog.tokens.Color(ui.ColorMuted), catalog.tokens.Color(ui.ColorPopupBg), 0)
}

func (catalog *capabilityCatalog) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: catalog.title()}
}

func (catalog *capabilityCatalog) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.Kind == ui.EventMouseClick && event.MouseClick.Button == tea.MouseLeft && ctx.LocalY == 0 && (catalog.detail || catalog.edit != "") {
		if ui.BreadcrumbIndex(catalog.owner.paneTitleParts(), catalog.owner.breadcrumbSeparator(), ctx.LocalX, catalog.Bounds().W) == 0 {
			return catalog.back()
		}
	}
	if event.IsKey() && event.Key.Code == tea.KeyEsc {
		return catalog.back()
	}
	if event.IsKey() && event.Key.Code == tea.KeyTab {
		catalog.owner.focusID(catalog.bar.ID())
		return catalog.changed()
	}
	return ui.EventResult{}
}

func (catalog *capabilityCatalog) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	catalog.consumeResult()
	if action.ID == "tabs.activate" || action.ID == "tabs.enter" {
		id, _ := action.Payload.(string)
		if id != "" {
			if catalog.kind == "source" && id == "discover" {
				return catalog.changed()
			}
			if catalog.edit != "" {
				return catalog.changed()
			}
			catalog.saveListState()
			if catalog.detail {
				catalog.detailForm(id)
			} else {
				catalog.page = id
				catalog.setTabs()
				catalog.restoreFilter()
				catalog.syncRows()
			}
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
	selected, hasSelected := catalog.table.Selected()
	if hasSelected && !catalog.detail && catalog.edit == "" {
		catalog.id = selected.ID
	}
	switch operation {
	case "install", "update":
		if catalog.id == "" {
			return catalog.changed()
		}
		catalog.acquisition = nil
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
	case "focus-table":
		catalog.owner.focusID(catalog.activeTable().ID())
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
	if operation == "discover" {
		catalog.saveListState()
		catalog.page = "discover"
		catalog.setTabs()
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
	catalog.owner.focusID(catalog.table.ID())
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
		fields = []*ui.FieldState{ui.TextFieldState("id", "Identity", value.ID, ""), ui.TextFieldState("name", "Name", value.Name, ""), ui.CycleFieldState("kind", "Source", []string{"local", "repository", "registry"}, mapDefault(value.Kind, "local"), ""), ui.TextFieldState("location", "Location", value.Location, "HTTPS registry, pinned repository, or local directory"), ui.TextFieldState("revision", "Pinned revision", value.Revision, "Repositories require a full commit digest"), ui.TextFieldState("digest", "Artifact digest", value.Digest, "Exact SHA-256 when the source requires artifact verification."), ui.BoolFieldState("trusted", "Approved acquisition", value.Trusted, "Publisher provenance is not a code safety certificate"), ui.BoolFieldState("disabled", "Disabled", value.Disabled, "Do not search or acquire from this source.")}
	} else {
		value := captypes.Clone(model.capabilities).Connections[catalog.id]
		catalog.draftOriginal = captypes.Action{Kind: catalog.kind, ID: catalog.id, Connection: &value}
		args, _ := json.Marshal(value.Args)
		environment, _ := json.Marshal(value.Environment)
		headers, _ := json.Marshal(value.Headers)
		secrets, _ := json.Marshal(value.SecretRefs)
		fields = []*ui.FieldState{ui.TextFieldState("id", "Identity", value.ID, "Stable connection identity"), ui.TextFieldState("name", "Name", value.Name, ""), ui.CycleFieldState("transport", "Transport", []string{"http", "stdio"}, mapDefault(value.Transport, "http"), "Streamable HTTP or a managed local process"), ui.TextFieldState("url", "Endpoint", value.URL, "Credentials are endpoint-bound"), ui.TextFieldState("command", "Executable", value.Command, "No shell interpolation"), ui.TextAreaFieldState("args", "Arguments", string(args), "JSON array, exact argument boundaries"), ui.TextFieldState("directory", "Working directory", value.Directory, ""), ui.TextAreaFieldState("environment", "Environment", string(environment), "Explicit non-secret values only"), ui.TextAreaFieldState("headers", "Headers", string(headers), "Non-secret headers"), ui.TextAreaFieldState("secrets", "Secret references", string(secrets), "Map header/env name to env:VARIABLE; credentials remain application-owned"), ui.CycleFieldState("auth", "Authentication", []string{"none", "oauth"}, mapDefault(value.Auth, "none"), "Sign in is explicit"), ui.TextFieldState("oauth-client", "OAuth client", value.OAuthClientID, "Blank uses dynamic registration"), ui.BoolFieldState("launch", "Approve local launch", value.LaunchApproved, "Unsandboxed launch exposes this executable to your user privileges; automatic acquisition cannot start it")}
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
		request.Source = &captypes.SourceConfig{ID: value("id"), Name: value("name"), Kind: value("kind"), Location: value("location"), Revision: value("revision"), Digest: value("digest"), Trusted: ui.BoolFieldValue(form.Field("trusted")), Disabled: ui.BoolFieldValue(form.Field("disabled"))}
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
