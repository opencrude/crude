package settings

import (
	"fmt"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

func (catalog *capabilityCatalog) selectedStoreEntry() (captypes.Entry, bool) {
	item, ok := catalog.store.Selected()
	if !ok {
		return captypes.Entry{}, false
	}
	entry, ok := item.Payload.(captypes.Entry)
	return entry, ok
}

func (catalog *capabilityCatalog) filteredStoreEntries(entries []captypes.Entry, state capabilityListState) ([]captypes.Entry, capabilityListState) {
	catalog.storeSources = catalog.enabledStoreSources()
	catalog.storeTypes = uniqueStoreValues(entries, catalog.storeEntryType)
	if state.show == "" {
		state.show = "all"
	}
	if state.sort == "" {
		state.sort = "relevance"
	}
	if state.source != "" && !slicesContain(catalog.storeSources, state.source) {
		state.source = ""
	}
	if state.entryType != "" && !slicesContain(catalog.storeTypes, state.entryType) {
		state.entryType = ""
	}
	query := strings.ToLower(strings.TrimSpace(state.query))
	filtered := make([]captypes.Entry, 0, len(entries))
	for _, entry := range entries {
		// A registry owns relevance for its exact query (including fuzzy matches).
		// Locally filter installed rows and old results, not the accepted search.
		remoteMatch := !entry.Installed && catalog.discoveryCurrent(state)
		if query != "" && !remoteMatch && !strings.Contains(strings.ToLower(strings.Join([]string{
			entry.Name, entry.ID, entry.Source, entry.Publisher, entry.Description,
			entry.State, entry.Reason, entry.Transport, entry.PackageType,
			entry.Repository, entry.Website,
		}, " ")), query) {
			continue
		}
		if state.source != "" && entry.Source != state.source {
			continue
		}
		if state.entryType != "" && catalog.storeEntryType(entry) != state.entryType {
			continue
		}
		if !storeShowMatches(state.show, entry) {
			continue
		}
		filtered = append(filtered, entry)
	}
	switch state.sort {
	case "popular":
		sort.SliceStable(filtered, func(first, second int) bool {
			if filtered[first].Popularity == filtered[second].Popularity {
				return strings.ToLower(filtered[first].Name) < strings.ToLower(filtered[second].Name)
			}
			return filtered[first].Popularity > filtered[second].Popularity
		})
	case "name":
		sort.SliceStable(filtered, func(first, second int) bool {
			return strings.ToLower(filtered[first].Name) < strings.ToLower(filtered[second].Name)
		})
	}
	return filtered, state
}

func uniqueStoreValues(entries []captypes.Entry, value func(captypes.Entry) string) []string {
	seen := make(map[string]struct{})
	var values []string
	for _, entry := range entries {
		current := value(entry)
		if current == "" {
			continue
		}
		if _, exists := seen[current]; exists {
			continue
		}
		seen[current] = struct{}{}
		values = append(values, current)
	}
	sort.Slice(values, func(first, second int) bool {
		return strings.ToLower(values[first]) < strings.ToLower(values[second])
	})
	return values
}

func slicesContain(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func storeShowMatches(show string, entry captypes.Entry) bool {
	return show != "installed" || entry.Installed
}

func (catalog *capabilityCatalog) storeEntryType(entry captypes.Entry) string {
	switch catalog.kind {
	case captypes.KindMCP:
		if entry.PackageType != "" || entry.Transport == "local package" || entry.Transport == "stdio" {
			return "package"
		}
		if entry.Transport == "remote" || entry.Transport == "http" {
			return "remote"
		}
		return "custom"
	case captypes.KindSkill:
		if entry.Source == captypes.SkillsDirectorySourceID || entry.Repository != "" && !entry.Installed {
			return "community"
		}
		if entry.Managed {
			return "managed"
		}
		return "local"
	default:
		return mapDefault(entry.Transport, "custom")
	}
}

func (catalog *capabilityCatalog) syncStoreFilters(_ []captypes.Entry, state capabilityListState) {
	button := func(label, action string, variant ui.ButtonVariant) ui.ActionBarButton {
		return ui.ActionBarButton{
			ID: ui.NewID(catalog.storeFilters.ID(), action), Label: label,
			Action: "settings.capability." + action, Variant: variant, Enabled: true,
		}
	}
	buttons := []ui.ActionBarButton{button("Source: "+catalog.sourceFilterLabel(state.source), "filter-source", ui.VariantGhost), button("Sort: "+storeFilterLabel(state.sort), "filter-sort", ui.VariantGhost)}
	if catalog.kind == "source" {
		buttons = []ui.ActionBarButton{button("Type: "+storeFilterLabel(mapDefault(state.entryType, "all")), "filter-type", ui.VariantGhost)}
	}
	catalog.storeFilters.SetButtons(buttons...)
	catalog.installed.SetChecked(state.show == "installed")
	catalog.installed.SetLabel(mapState(state.show == "installed", "[x] Installed only", "[ ] Installed only"))
}

func storeFilterLabel(value string) string {
	switch value {
	case "", "all":
		return "All"
	case "setup":
		return "Needs setup"
	case "installed":
		return "Installed"
	case "ready":
		return "Ready"
	case "popular":
		return "Popular"
	case "relevance":
		return "Relevant"
	default:
		if len(value) == 1 {
			return strings.ToUpper(value)
		}
		return strings.ToUpper(value[:1]) + value[1:]
	}
}

func (catalog *capabilityCatalog) sourceFilterLabel(sourceID string) string {
	if sourceID == "" {
		return "All"
	}
	if source, exists := catalog.owner.model.capabilities.Sources[sourceID]; exists && source.Name != "" {
		return rendering.TruncateVisual(source.Name, 18, "…")
	}
	return rendering.TruncateVisual(sourceID, 18, "…")
}

func (catalog *capabilityCatalog) layoutStore(ctx ui.LayoutContext, rect ui.Rect) {
	barWidth, stroke := 0, 0
	if catalog.storeScroll.NeededForViewport(rect.H) {
		barWidth = min(ui.ScrollbarWidth+1, rect.W)
		stroke = min(ui.ScrollbarWidth, barWidth)
	}
	catalog.store.Layout(ctx, ui.Rect{X: rect.X, Y: rect.Y, W: max(0, rect.W-barWidth), H: rect.H})
	catalog.storeScroll.Layout(ctx, ui.Rect{X: rect.X + rect.W - stroke, Y: rect.Y, W: stroke, H: rect.H})
}

func (catalog *capabilityCatalog) paintStoreRow(buffer *surface.CellBuffer, x, y, width int, item ui.VirtItem, localRow int, state ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	entry, ok := item.Payload.(captypes.Entry)
	if !ok || width <= 1 {
		return
	}
	fg, bg := tokens.ListRowColors(state)
	if localRow == item.Height-1 {
		return // The quiet gap belongs to the collection, not the card highlight.
	}
	ui.FillRect(buffer, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	// The header carries keyboard focus; the reading body stays on the quiet
	// selected surface rather than becoming a four-row block of accent ink.
	if state == ui.StateFocused && localRow > 0 {
		fg, bg = tokens.ListRowColors(ui.StateSelected)
		ui.FillRect(buffer, 0, 0, ui.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(bg))
	}
	primary, muted := tokens.MenuInk(state, fg)
	left := x + 1
	available := max(0, width-2)
	if localRow == 0 {
		button := catalog.storeButtons[entry.ID]
		buttonWidth := 0
		if button != nil {
			buttonWidth = button.Bounds().W
			paintButton := *button
			paintButton.SetSurfaceBackground(bg)
			if state == ui.StateFocused && !catalog.store.actionFocus {
				// Primary ink on an accent-filled header is invisible. Keep the
				// native button on its normal surface until its own focus enters.
				paintButton.SetSurfaceBackground(tokens.Color(ui.ColorPopupBg))
			}
			paintButton.Paint(buffer, x, y, ui.PaintContext{Hovered: state == ui.StateHovered, Focused: state == ui.StateFocused && catalog.store.actionFocus})
		}
		nameWidth := max(0, available-buttonWidth-2)
		buffer.WriteString(left, y, rendering.TruncateVisual(entry.Name, nameWidth, "…"), primary, bg, cell.AttrBold)
		return
	}
	text := catalog.storeEntryMeta(entry)
	if catalog.kind == "source" && localRow == 1 {
		text = entry.Path
	}
	if localRow >= 2 {
		description := strings.Join(strings.Fields(mapDefault(entry.Description, entry.Reason)), " ")
		if catalog.kind == "source" {
			description = catalog.sourceCoverage(entry.Transport)
			if localRow == 3 {
				text = strings.Join([]string{entry.Mode, entry.State, string(entry.Origin)}, " · ")
				if entry.Version != "" {
					text += " · commit " + rendering.TruncateVisual(entry.Version, 12, "")
				}
				buffer.WriteString(left, y, rendering.TruncateVisual(text, available, "…"), muted, bg, 0)
				return
			}
		}
		lines := rendering.WordWrap(description, available)
		if index := localRow - 2; index < len(lines) {
			text = lines[index]
		} else {
			text = ""
		}
		if localRow == 3 && len(lines) > 2 {
			text = rendering.TruncateVisual(strings.Join(lines[1:], " "), available, "…")
		}
		if localRow == 3 && entry.Reason != "" {
			text = entry.Reason
		}
	}
	buffer.WriteString(left, y, rendering.TruncateVisual(text, available, "…"), muted, bg, 0)
}

// capabilityStoreView retains VirtualList's selection/scroll authority while
// composing native row actions. Only the owning card can install its identity.
type capabilityStoreView struct {
	*ui.VirtualList
	owner       *capabilityCatalog
	actionFocus bool
}

type capabilityInstalledFilter struct {
	*ui.ToggleButton
	owner *capabilityCatalog
}

func (filter *capabilityInstalledFilter) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyTab {
		target := filter.owner.storeFilters.ID()
		if event.Key.Mod.Contains(tea.ModShift) {
			target = filter.owner.filter.ID()
		}
		filter.owner.owner.focusID(target)
		return filter.owner.changed()
	}
	return filter.ToggleButton.Handle(ctx, event)
}

func (view *capabilityStoreView) Layout(ctx ui.LayoutContext, rect ui.Rect) {
	view.VirtualList.Layout(ctx, rect)
	for _, button := range view.owner.storeButtons {
		width := min(max(0, rect.W-2), button.Measure(ctx, ui.Loose(rect.W, 1)).W)
		button.Layout(ctx, ui.Rect{X: max(0, rect.W-1-width), W: width, H: min(1, rect.H)})
	}
}

func (view *capabilityStoreView) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		switch event.Key.Code {
		case tea.KeyRight:
			view.actionFocus = true
			return view.owner.changed()
		case tea.KeyLeft:
			view.actionFocus = false
			return view.owner.changed()
		case tea.KeyEnter:
			if view.actionFocus {
				if entry, ok := view.owner.selectedStoreEntry(); ok {
					return view.activateEntry(entry)
				}
			}
		case tea.KeyTab:
			if event.Key.Mod.Contains(tea.ModShift) {
				view.owner.owner.focusID(view.owner.storeFilters.ID())
			} else if !view.actionFocus {
				view.actionFocus = true
				return view.owner.changed()
			} else {
				view.actionFocus = false
				view.owner.owner.focusID(view.owner.bar.ID())
			}
			return view.owner.changed()
		}
	}
	if event.Kind == ui.EventMouseClick && event.MouseClick.Button == tea.MouseLeft {
		index := view.ItemIndexAtRow(view.Scroll().Offset() + ctx.LocalY)
		if index < 0 || index >= len(view.Items()) {
			return ui.EventResult{}
		}
		item := view.Items()[index]
		start := 0
		for _, prior := range view.Items()[:index] {
			start += prior.Height
		}
		localRow := view.Scroll().Offset() + ctx.LocalY - start
		if localRow == item.Height-1 {
			return ui.EventResult{Handled: true}
		}
		view.SelectIndex(index)
		entry, ok := item.Payload.(captypes.Entry)
		if !ok {
			return ui.EventResult{}
		}
		if button := view.owner.storeButtons[entry.ID]; localRow == 0 && button != nil && button.Bounds().Contains(ctx.LocalX, 0) {
			view.actionFocus = true
			return view.activateEntry(entry)
		}
		view.actionFocus = false
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "settings.capability.inspect", Payload: item}, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
	}
	result := view.VirtualList.Handle(ctx, event)
	if event.IsKey() && result.Handled && event.Key.Code != tea.KeyEnter {
		view.EnsureSelectedVisible()
	}
	if result.Handled {
		view.owner.saveListState()
	}
	return result
}

func (view *capabilityStoreView) activateEntry(entry captypes.Entry) ui.EventResult {
	button := view.owner.storeButtons[entry.ID]
	if button == nil || button.Semantics().Disabled {
		return ui.EventResult{Handled: true}
	}
	view.owner.id = entry.ID
	return button.Handle(ui.EventContext{Focused: true}, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyEnter}))
}

func (catalog *capabilityCatalog) syncStoreButtons(entries []captypes.Entry) {
	current := make(map[string]*ui.Button, len(entries))
	for _, entry := range entries {
		label, action := "Install", "install"
		if entry.Installed {
			label, action = "Manage", "inspect"
		}
		if catalog.kind == "source" {
			label, action = "Edit", "inspect"
		}
		if entry.State == "unsupported" {
			label, action = "Details", "inspect"
		}
		button := catalog.storeButtons[entry.ID]
		if button == nil {
			button = ui.NewButton(ui.NewID(capabilityStoreID, entry.ID), label, "settings.capability."+action)
		}
		button.SetLabel(label)
		button.SetActionID("settings.capability." + action)
		button.SetTokens(catalog.tokens)
		button.SetVariant(ui.VariantSecondary)
		if action == "install" {
			button.SetVariant(ui.VariantPrimary)
		}
		button.SetDisabled(catalog.owner.model.capabilityPending != nil && action == "install")
		current[entry.ID] = button
	}
	catalog.storeButtons = current
}

func (catalog *capabilityCatalog) enabledStoreSources() []string {
	var ids []string
	for id, source := range catalog.owner.model.capabilities.Sources {
		if source.Disabled || catalog.kind == captypes.KindMCP && source.Kind != "registry" || catalog.kind == captypes.KindSkill && source.Kind == "registry" {
			continue
		}
		ids = append(ids, id)
	}
	sort.Slice(ids, func(a, b int) bool { return catalog.sourceFilterLabel(ids[a]) < catalog.sourceFilterLabel(ids[b]) })
	return ids
}

func (catalog *capabilityCatalog) discoveryCurrent(state capabilityListState) bool {
	search, ok := catalog.owner.model.capabilityDiscoveryQuery[catalog.kind]
	return ok && strings.EqualFold(strings.TrimSpace(search.Query), strings.TrimSpace(state.query)) &&
		(search.Arguments["source_id"] == "" || search.Arguments["source_id"] == state.source)
}

func (catalog *capabilityCatalog) mergeStoreDiscovery(installed []captypes.Entry, state capabilityListState) []captypes.Entry {
	positions := make(map[string]int, len(installed))
	for index, entry := range installed {
		positions[entry.ID] = index
	}
	if !catalog.discoveryCurrent(state) {
		return installed
	}
	for _, entry := range catalog.owner.model.capabilityDiscovery[catalog.kind] {
		if index, exists := positions[entry.ID]; exists {
			if installed[index].Description == "" {
				installed[index].Description = entry.Description
			}
			installed[index].Popularity = entry.Popularity
			continue
		}
		positions[entry.ID] = len(installed)
		installed = append(installed, entry)
	}
	return installed
}

func (catalog *capabilityCatalog) searchStore() ui.EventResult {
	state := catalog.views[catalog.viewKey()]
	if catalog.kind == "source" || state.show == "installed" {
		catalog.owner.focusID(catalog.store.ID())
		return catalog.changed()
	}
	if strings.TrimSpace(catalog.filter.Value()) == "" {
		catalog.message = "Enter a search to browse enabled sources, or select Installed only."
		return catalog.changed()
	}
	catalog.saveListState()
	return catalog.submit(captypes.Action{Operation: "discover", Kind: catalog.kind, Query: catalog.filter.Value(), Arguments: map[string]string{"source_id": state.source}})
}

func (catalog *capabilityCatalog) openStoreFilter(operation string) ui.EventResult {
	catalog.saveListState()
	state := catalog.views[catalog.viewKey()]
	catalog.choosing = operation
	var items []ui.ListItem
	selected := ""
	switch operation {
	case "filter-source":
		items = append(items, ui.ListItem{ID: "all", Label: "All enabled sources", Command: true, Payload: ""})
		for _, id := range catalog.enabledStoreSources() {
			source := catalog.owner.model.capabilities.Sources[id]
			items = append(items, ui.ListItem{ID: id, Label: source.Name, Detail: catalog.sourceCoverage(source.Kind), Command: true, Payload: id})
		}
		selected = mapDefault(state.source, "all")
	case "filter-sort":
		for _, id := range []string{"relevance", "popular", "name"} {
			items = append(items, ui.ListItem{ID: id, Label: storeFilterLabel(id), Command: true, Payload: id})
		}
		selected = state.sort
	case "filter-type":
		items = append(items, ui.ListItem{ID: "all", Label: "All types", Command: true, Payload: ""})
		for _, id := range catalog.storeTypes {
			items = append(items, ui.ListItem{ID: id, Label: storeFilterLabel(id), Command: true, Payload: id})
		}
		selected = mapDefault(state.entryType, "all")
	}
	catalog.choices.SetItems(items)
	for index, item := range items {
		if item.ID == selected {
			catalog.choices.SelectIndex(index)
		}
	}
	catalog.owner.focusID(catalog.choices.ID())
	return catalog.changed()
}

func (catalog *capabilityCatalog) chooseStoreFilter() ui.EventResult {
	item, ok := catalog.choices.Selected()
	if !ok || catalog.choosing == "" {
		return catalog.changed()
	}
	value, _ := item.Payload.(string)
	state := catalog.views[catalog.viewKey()]
	switch catalog.choosing {
	case "filter-source":
		state.source = value
	case "filter-sort":
		state.sort = value
	case "filter-type":
		state.entryType = value
	}
	state.selected, state.offset = "", 0
	catalog.views[catalog.viewKey()] = state
	catalog.choosing = ""
	catalog.syncRows()
	catalog.owner.focusID(catalog.storeFilters.ID())
	return catalog.changed()
}

func (catalog *capabilityCatalog) layoutStoreBrowse(ctx ui.LayoutContext, top, height int) {
	width := catalog.Bounds().W
	catalog.syncStoreFilters(nil, catalog.views[catalog.viewKey()])
	catalog.syncStoreButtons(catalog.storeEntries())
	if catalog.choosing != "" {
		for _, component := range []ui.Component{catalog.filter, catalog.installed, catalog.searchButton, catalog.storeFilters, catalog.store, catalog.storeScroll} {
			component.Layout(ctx, ui.Rect{})
		}
		barWidth, stroke := 0, 0
		if catalog.choiceScroll.NeededForViewport(height) {
			barWidth = min(ui.ScrollbarWidth+1, width)
			stroke = min(ui.ScrollbarWidth, barWidth)
		}
		catalog.choices.Layout(ctx, ui.Rect{Y: top, W: width - barWidth, H: height})
		catalog.choiceScroll.Layout(ctx, ui.Rect{X: width - stroke, Y: top, W: stroke, H: height})
		return
	}
	searchWidth := 0
	if catalog.kind != "source" && width >= 28 {
		searchWidth = catalog.searchButton.Measure(ctx, ui.Loose(width, 1)).W
	}
	catalog.filter.Layout(ctx, ui.Rect{Y: top, W: max(0, width-searchWidth-mapStateInt(searchWidth > 0, 2, 0)), H: min(1, height)})
	catalog.searchButton.Layout(ctx, ui.Rect{X: width - searchWidth, Y: top, W: searchWidth, H: min(1, height)})
	catalog.filter.SetPlaceholder("Search " + catalog.title())
	if catalog.kind != "source" {
		catalog.filter.SetPlaceholder("Search " + catalog.title() + " — Enter to search sources")
	}
	filterY := top + 1
	filterHeight := 1
	installedW := 0
	if catalog.kind != "source" {
		installedW = min(width, catalog.installed.Measure(ctx, ui.Loose(width, 1)).W)
	}
	catalog.installed.Layout(ctx, ui.Rect{Y: filterY, W: installedW, H: min(1, max(0, height-1))})
	filtersW := catalog.storeFilters.Measure(ctx, ui.Loose(1024, 1)).W
	filterX := mapStateInt(installedW > 0, installedW+2, 0)
	if filterX+filtersW > width {
		filterHeight = 2
		filterX, filterY = 0, filterY+1
	}
	// Shorten labels coherently before the native ActionBar publishes hit bounds.
	if filtersW > width {
		catalog.storeFilters.SetButtons(ui.ActionBarButton{Label: "Source", Action: "settings.capability.filter-source", Enabled: true}, ui.ActionBarButton{Label: "Sort", Action: "settings.capability.filter-sort", Enabled: true})
	}
	catalog.storeFilters.Layout(ctx, ui.Rect{X: filterX, Y: filterY, W: max(0, width-filterX), H: min(1, max(0, top+height-filterY))})
	catalog.layoutStore(ctx, ui.Rect{Y: top + filterHeight + 2, W: width, H: max(0, height-filterHeight-2)})
}

func (catalog *capabilityCatalog) storeEntries() []captypes.Entry {
	entries := make([]captypes.Entry, 0, len(catalog.store.Items()))
	for _, item := range catalog.store.Items() {
		if entry, ok := item.Payload.(captypes.Entry); ok {
			entries = append(entries, entry)
		}
	}
	return entries
}

func mapStateInt(condition bool, first, second int) int {
	if condition {
		return first
	}
	return second
}

func (catalog *capabilityCatalog) paintStoreHeading(buffer *surface.CellBuffer, x, y, width int) {
	text := "Reusable expertise for your next task"
	switch catalog.kind {
	case captypes.KindMCP:
		text = "Tools and services, ready to connect"
	case "source":
		text = "Your discovery sources · select a card to configure"
	}
	if catalog.choosing != "" {
		text = "Choose " + strings.TrimPrefix(catalog.choosing, "filter-") + " · Enter to apply · Esc to return"
	}
	buffer.WriteString(x, y+1, rendering.TruncateVisual(text, width, "…"), catalog.tokens.Color(ui.ColorMuted), catalog.tokens.Color(ui.ColorPopupBg), 0)
}

func (catalog *capabilityCatalog) paintStoreEmpty(buffer *surface.CellBuffer, x, y int) {
	if catalog.choosing != "" || len(catalog.store.Items()) > 0 || catalog.store.Bounds().Empty() {
		return
	}
	bounds := catalog.store.Bounds()
	state := catalog.views[catalog.viewKey()]
	title, body := "Find your next capability", "Search a task, tool or service above. Results include source, publisher and an Install action."
	if state.show == "installed" {
		title, body = "No installed matches", "Clear the search, or turn off Installed only to find something new."
	} else if catalog.discoveryCurrent(state) {
		title, body = "No matches in these sources", "Try another query or source. Add a repository, registry or local directory in Sources."
	}
	if catalog.kind == "source" {
		title, body = "No matching sources", "Clear the search or add a discovery source below."
	}
	if catalog.owner.model.capabilityPending != nil {
		title, body = "Searching your sources", "Results will appear here. You can cancel this search without changing installed items."
	}
	row := bounds.Y + min(1, max(0, bounds.H-1))
	buffer.WriteString(x+1, y+row, rendering.TruncateVisual(title, max(0, bounds.W-2), "…"), catalog.tokens.Color(ui.ColorFg), catalog.tokens.Color(ui.ColorPopupBg), cell.AttrBold)
	for index, line := range rendering.WordWrap(body, max(1, bounds.W-2)) {
		if row+1+index >= bounds.Y+bounds.H {
			break
		}
		buffer.WriteString(x+1, y+row+1+index, rendering.TruncateVisual(line, max(0, bounds.W-2), "…"), catalog.tokens.Color(ui.ColorMuted), catalog.tokens.Color(ui.ColorPopupBg), 0)
	}
}

func (catalog *capabilityCatalog) storeStatus() string {
	if catalog.kind == "source" {
		return fmt.Sprintf("%d sources · source trust never grants execution access", len(catalog.store.Items()))
	}
	if state := catalog.views[catalog.viewKey()]; state.show == "installed" {
		return fmt.Sprintf("%d installed matches · no external search", len(catalog.store.Items()))
	} else if strings.TrimSpace(state.query) != "" && !catalog.discoveryCurrent(state) {
		return "Press Enter or Search to query enabled sources."
	}
	return fmt.Sprintf("%d items · Enter details · Right / Tab row action", len(catalog.store.Items()))
}

func (catalog *capabilityCatalog) sourceCoverage(kind string) string {
	switch kind {
	case "repository":
		return "Skills · exact commit-pinned repository"
	case "skill-registry":
		return "Community skills · resolves an exact commit before installation"
	case "registry":
		return "MCP · remote HTTP and local packages · setup stays explicit"
	case "local":
		return "Skills · authored directories on this computer"
	default:
		return kind
	}
}

func (catalog *capabilityCatalog) moveBrowseFocus(backward bool) {
	if catalog.detail || catalog.edit != "" {
		catalog.owner.focusID(catalog.bar.ID())
		return
	}
	if backward {
		catalog.owner.FocusCategories()
	} else {
		catalog.owner.focusID(catalog.installed.ID())
		if catalog.kind == "source" {
			catalog.owner.focusID(catalog.storeFilters.ID())
		}
	}
}

func (catalog *capabilityCatalog) storeEntryMeta(entry captypes.Entry) string {
	var parts []string
	if entry.Source != "" {
		parts = append(parts, catalog.sourceFilterLabel(entry.Source))
	}
	if entry.Publisher != "" && entry.Publisher != entry.Source {
		parts = append(parts, entry.Publisher)
	}
	if entry.PackageType != "" {
		parts = append(parts, entry.PackageType)
	} else if entry.Transport != "" {
		parts = append(parts, entry.Transport)
	}
	if entry.Popularity > 0 {
		parts = append(parts, formatStorePopularity(entry.Popularity))
	}
	if entry.Installed {
		parts = append(parts, "Installed")
	}
	if entry.State != "" && entry.State != "not acquired" && entry.State != "available" {
		parts = append(parts, entry.State)
	}
	if entry.Mode != "" {
		parts = append(parts, entry.Mode)
	}
	if entry.Origin != "" && entry.Origin != captypes.ScopeApplication {
		parts = append(parts, string(entry.Origin))
	}
	return strings.Join(parts, " · ")
}

func formatStorePopularity(value int64) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM installs", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fK installs", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d installs", value)
	}
}
