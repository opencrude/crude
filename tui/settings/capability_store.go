package settings

import (
	"fmt"
	"sort"
	"strings"

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
	catalog.storeSources = uniqueStoreValues(entries, func(entry captypes.Entry) string { return entry.Source })
	catalog.storeTypes = uniqueStoreValues(entries, catalog.storeEntryType)
	if state.show == "" {
		state.show = "all"
	}
	if state.sort == "" {
		if catalog.page == "discover" {
			state.sort = "relevance"
		} else {
			state.sort = "name"
		}
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
		if query != "" && !strings.Contains(strings.ToLower(strings.Join([]string{
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
	state := strings.ToLower(entry.State + " " + entry.Reason)
	switch show {
	case "installed":
		return entry.Installed
	case "ready":
		return !strings.Contains(state, "disabled") && !strings.Contains(state, "required") &&
			!strings.Contains(state, "unavailable") && !strings.Contains(state, "failed")
	case "setup":
		return strings.Contains(state, "required") || strings.Contains(state, "unavailable") ||
			strings.Contains(state, "failed")
	case "disabled":
		return strings.Contains(state, "disabled")
	default:
		return true
	}
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
	var buttons []ui.ActionBarButton
	if catalog.page == "discover" && catalog.kind != "source" {
		buttons = append(buttons, button("Search", "search", ui.VariantPrimary))
	}
	buttons = append(buttons,
		button("Show: "+storeFilterLabel(state.show), "filter-show", ui.VariantGhost),
		button("Type: "+storeFilterLabel(mapDefault(state.entryType, "all")), "filter-type", ui.VariantGhost),
		button("Source: "+catalog.sourceFilterLabel(state.source), "filter-source", ui.VariantGhost),
		button("Sort: "+storeFilterLabel(state.sort), "filter-sort", ui.VariantGhost),
	)
	catalog.storeFilters.SetButtons(buttons...)
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

func (catalog *capabilityCatalog) cycleStoreFilter(operation string) {
	state := catalog.views[catalog.viewKey()]
	switch operation {
	case "filter-source":
		state.source = cycleFilterValue(state.source, append([]string{""}, catalog.storeSources...))
	case "filter-type":
		state.entryType = cycleFilterValue(state.entryType, append([]string{""}, catalog.storeTypes...))
	case "filter-show":
		state.show = cycleFilterValue(mapDefault(state.show, "all"), []string{"all", "ready", "setup", "disabled", "installed"})
	case "filter-sort":
		state.sort = cycleFilterValue(mapDefault(state.sort, "relevance"), []string{"relevance", "popular", "name"})
	}
	catalog.views[catalog.viewKey()] = state
}

func cycleFilterValue(current string, options []string) string {
	if len(options) == 0 {
		return ""
	}
	for index, value := range options {
		if value == current {
			return options[(index+1)%len(options)]
		}
	}
	return ""
}

func (catalog *capabilityCatalog) layoutStore(ctx ui.LayoutContext, rect ui.Rect) {
	barWidth := 0
	if catalog.storeScroll.NeededForViewport(rect.H) {
		barWidth = min(ui.ScrollbarWidth, rect.W)
	}
	catalog.store.Layout(ctx, ui.Rect{X: rect.X, Y: rect.Y, W: max(0, rect.W-barWidth), H: rect.H})
	catalog.storeScroll.Layout(ctx, ui.Rect{X: rect.X + rect.W - barWidth, Y: rect.Y, W: barWidth, H: rect.H})
}

func (catalog *capabilityCatalog) paintStoreRow(buffer *surface.CellBuffer, x, y, width int, item ui.VirtItem, localRow int, state ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	entry, ok := item.Payload.(captypes.Entry)
	if !ok || width <= 1 {
		return
	}
	fg, bg := tokens.ListRowColors(state)
	primary, muted := tokens.MenuInk(state, fg)
	left := x + 1
	available := max(0, width-2)
	if localRow == 0 {
		status := strings.TrimSpace(entry.Mode + " " + entry.State)
		status = rendering.TruncateVisual(status, min(24, available/2), "…")
		statusWidth := rendering.VisibleWidth(status)
		nameWidth := max(0, available-statusWidth-2)
		buffer.WriteString(left, y, rendering.TruncateVisual(entry.Name, nameWidth, "…"), primary, bg, cell.AttrBold)
		if status != "" {
			buffer.WriteString(x+width-1-statusWidth, y, status, muted, bg, 0)
		}
		return
	}
	description := entry.Description
	if description == "" {
		description = entry.Reason
	}
	metaBudget := available
	if description != "" {
		metaBudget = available / 2
	}
	meta := rendering.TruncateVisual(catalog.storeEntryMeta(entry), metaBudget, "…")
	metaWidth := rendering.VisibleWidth(meta)
	descriptionWidth := max(0, available-metaWidth-2)
	buffer.WriteString(left, y, rendering.TruncateVisual(description, descriptionWidth, "…"), muted, bg, 0)
	if meta != "" {
		buffer.WriteString(x+width-1-metaWidth, y, rendering.TruncateVisual(meta, available, "…"), muted, bg, 0)
	}
}

func (catalog *capabilityCatalog) storeEntryMeta(entry captypes.Entry) string {
	var parts []string
	if entry.Source != "" {
		parts = append(parts, catalog.sourceFilterLabel(entry.Source))
	}
	if entry.PackageType != "" {
		parts = append(parts, entry.PackageType)
	} else if entry.Transport != "" {
		parts = append(parts, entry.Transport)
	}
	if entry.Popularity > 0 {
		parts = append(parts, formatStorePopularity(entry.Popularity))
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
