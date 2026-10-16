package settings

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"crude/tui/rendering"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

const capabilityPreviewLimit = 128 << 10

func (catalog *capabilityCatalog) viewKey() string {
	if catalog.detail {
		return string(catalog.kind) + "/" + catalog.id + "/" + catalog.detailPage
	}
	return string(catalog.kind) + "/catalog"
}

func (catalog *capabilityCatalog) saveListState() {
	state := catalog.views[catalog.viewKey()]
	state.query = catalog.filter.Value()
	if catalog.detail {
		if row, ok := catalog.browse.Selected(); ok {
			state.selected = row.ID
		}
		state.offset = catalog.browse.Scroll().Offset()
	} else {
		if entry, ok := catalog.selectedStoreEntry(); ok {
			state.selected = entry.ID
		}
		state.offset = catalog.store.Scroll().Offset()
	}
	catalog.views[catalog.viewKey()] = state
}

func (catalog *capabilityCatalog) restoreFilter() {
	catalog.filter.SetValue(catalog.views[catalog.viewKey()].query)
}

func (catalog *capabilityCatalog) switchKind(kind captypes.Kind) {
	if kind == catalog.kind {
		return
	}
	catalog.saveListState()
	catalog.closeEditor()
	catalog.kind, catalog.id, catalog.edit, catalog.detail = kind, "", "", false
	catalog.choosing, catalog.message = "", ""
	catalog.setTabs()
	catalog.restoreFilter()
	catalog.syncRows()
}

func (catalog *capabilityCatalog) syncModel() {
	if catalog.model == catalog.owner.model {
		return
	}
	catalog.model = catalog.owner.model
	catalog.id, catalog.edit, catalog.detail = "", "", false
	catalog.forms = make(map[string]*ui.FormModel)
	catalog.inputForms = make(map[string]*ui.FormModel)
	catalog.requests = make(map[uint64]capabilityViewRequest)
	catalog.preview = make(map[string]CapabilityResultMsg)
	catalog.files = make(map[string][]string)
	catalog.resultSeen, catalog.seen = 0, 0
	catalog.message, catalog.nextOpen = "", nil
	catalog.acquisition = nil
	catalog.setTabs()
	catalog.restoreFilter()
	catalog.syncRows()
}

func (catalog *capabilityCatalog) setTabs() {
	var items []ui.TabItem
	active := ""
	if catalog.detail {
		active = catalog.detailPage
		if catalog.kind == captypes.KindSkill {
			items = []ui.TabItem{{ID: "overview", Label: "Overview"}, {ID: "instructions", Label: "Instructions"}, {ID: "files", Label: "Files"}, {ID: "access", Label: "Access & use"}}
			if entry := catalog.entry(); entry.Managed && entry.Version != "" {
				items = append(items, ui.TabItem{ID: "versions", Label: "Versions"})
			}
		} else {
			items = []ui.TabItem{{ID: "overview", Label: "Overview"}, {ID: "connection", Label: "Connection"}, {ID: "tools", Label: "Tools"}, {ID: "resources", Label: "Resources"}, {ID: "prompts", Label: "Prompts"}, {ID: "access", Label: "Access"}, {ID: "activity", Label: "Activity"}}
		}
		if catalog.acquisition != nil {
			items = append(items, ui.TabItem{ID: "acquisition", Label: "Review install"})
		}
	}
	catalog.tabs.SetItems(items, active)
}

func (catalog *capabilityCatalog) closeEditor() {
	catalog.edit = ""
	if catalog.owner.model.capabilityDraft != nil {
		catalog.owner.model.capabilityDraft.Reset()
	}
	catalog.owner.model.capabilityDraft = nil
	catalog.baseline = nil
}

func (catalog *capabilityCatalog) focusContent() {
	if catalog.edit != "" {
		catalog.owner.focusID(catalog.form.ID())
		return
	}
	if catalog.detail && !catalog.detailCollection() {
		catalog.focusDetailInput()
		return
	}
	catalog.owner.focusID(catalog.filter.ID())
}

func (catalog *capabilityCatalog) focusDetailInput() {
	if form := catalog.forms[catalog.viewKey()]; form != nil {
		for _, field := range form.Fields() {
			if !field.ReadOnly {
				catalog.owner.focusID(catalog.form.ID())
				return
			}
		}
	}
	catalog.owner.focusID(catalog.reader.ID())
}

func (catalog *capabilityCatalog) detailCollection() bool {
	return catalog.kind == captypes.KindMCP && slices.Contains([]string{"tools", "resources", "prompts", "access"}, catalog.detailPage)
}

func (catalog *capabilityCatalog) actionBars() []*ui.ActionBar {
	bars := []*ui.ActionBar{catalog.bar}
	if catalog.footerDecisions() {
		bars = nil // the decision bar is laid out in the settings footer
	}
	if len(catalog.more.ButtonLabels()) > 0 {
		bars = append(bars, catalog.more)
	}
	for _, bar := range catalog.extraBars {
		if len(bar.ButtonLabels()) > 0 {
			bars = append(bars, bar)
		}
	}
	return bars
}

func (catalog *capabilityCatalog) layoutActions(buttons []ui.ActionBarButton) {
	width := catalog.Bounds().W
	if width == 0 {
		width = 80
	}
	rows := [][]ui.ActionBarButton{{}}
	used := 0
	for _, button := range buttons {
		if rendering.VisibleWidth(button.Label)+4 > width && width >= 8 {
			button.Label = rendering.TruncateVisual(button.Label, width-4, "…")
		}
		buttonWidth := rendering.VisibleWidth(button.Label) + 4
		if used > 0 && used+2+buttonWidth > width {
			rows = append(rows, nil)
			used = 0
		}
		if used > 0 {
			used += 2
		}
		rows[len(rows)-1] = append(rows[len(rows)-1], button)
		used += buttonWidth
	}
	capacity := 2 // bar and more before any extra rows
	if catalog.footerDecisions() {
		capacity = 1
	}
	for len(catalog.extraBars)+capacity < len(rows) {
		bar := ui.NewActionBar(ui.NewID(capabilityCatalogID, "actions-"+strconv.Itoa(len(catalog.extraBars)+2)), catalog.tokens)
		bar.SetAlignEnd(false)
		bar.SetCancelAction("settings.capability.back")
		bar.SetExitActions("settings.capability.actions-next", "settings.capability.actions-prev")
		catalog.extraBars = append(catalog.extraBars, bar)
	}
	bars := append([]*ui.ActionBar{catalog.bar, catalog.more}, catalog.extraBars...)
	if catalog.footerDecisions() {
		// Operations fill the body rows; the first bar is the footer's.
		bars = bars[1:]
	}
	for index, bar := range bars {
		bar.SetExitActions("settings.capability.actions-next/"+strconv.Itoa(index), "settings.capability.actions-prev/"+strconv.Itoa(index))
		if index < len(rows) {
			bar.SetButtons(rows[index]...)
		} else {
			bar.SetButtons()
			bar.SetBounds(ui.Rect{})
		}
	}
}

func (catalog *capabilityCatalog) moveActionFocus(index int, forward bool) {
	bars := catalog.actionBars()
	if forward && index+1 < len(bars) {
		catalog.owner.focusID(bars[index+1].ID())
		return
	}
	if !forward && index > 0 {
		catalog.owner.focusID(bars[index-1].ID())
		return
	}
	if forward {
		catalog.owner.FocusActions()
	} else {
		catalog.focusContent()
	}
}

// moveDecisionFocus leaves the footer decision bar: forward to Close,
// backward to the last operation row or the page content.
func (catalog *capabilityCatalog) moveDecisionFocus(forward bool) {
	if forward {
		catalog.owner.focusID(WorkspaceSettingsCloseBarID)
		return
	}
	if bars := catalog.actionBars(); len(bars) > 0 {
		catalog.owner.focusID(bars[len(bars)-1].ID())
		return
	}
	catalog.focusContent()
}

func (catalog *capabilityCatalog) layoutTable(ctx ui.LayoutContext, table *ui.Table, scrollbar *ui.ScrollbarControl, rect ui.Rect) {
	barWidth := 0
	stroke := 0
	if scrollbar.NeededForViewport(max(0, rect.H-1)) {
		barWidth = min(ui.ScrollbarWidth+1, rect.W)
		stroke = min(ui.ScrollbarWidth, barWidth)
	}
	table.Layout(ctx, ui.Rect{X: rect.X, Y: rect.Y, W: max(0, rect.W-barWidth), H: rect.H})
	// Only the painted stroke registers; the reserved gutter column stays
	// inert so clicks over the content never reach the scrollbar.
	scrollbar.Layout(ctx, ui.Rect{X: rect.X + rect.W - stroke, Y: rect.Y, W: stroke, H: rect.H})
}

func (catalog *capabilityCatalog) layoutForm(ctx ui.LayoutContext, rect ui.Rect) {
	catalog.form.Layout(ctx, rect)
	if catalog.formScroll.Needed() {
		barWidth := min(ui.ScrollbarWidth+1, rect.W)
		stroke := min(ui.ScrollbarWidth, barWidth)
		catalog.form.Layout(ctx, ui.Rect{X: rect.X, Y: rect.Y, W: max(0, rect.W-barWidth), H: rect.H})
		catalog.formScroll.Layout(ctx, ui.Rect{X: rect.X + rect.W - stroke, Y: rect.Y, W: stroke, H: rect.H})
	}
}

func (catalog *capabilityCatalog) layoutDetailBody(ctx ui.LayoutContext, rect ui.Rect) {
	model := catalog.forms[catalog.viewKey()]
	if model == nil {
		return
	}
	var inputs []*ui.FieldState
	var body strings.Builder
	for _, field := range model.Fields() {
		if field.ReadOnly {
			if body.Len() > 0 {
				body.WriteString("\n")
			}
			body.WriteString(field.Label + "\n\n" + alignKeyValues(field.Value.String()) + "\n")
			if field.Description != "" {
				body.WriteString("\n" + field.Description + "\n")
			}
		} else {
			inputs = append(inputs, field)
		}
	}
	text := boundedCapabilityText(body.String())
	if catalog.reader.Value() != text {
		catalog.reader.SetValue(text)
		catalog.reader.SetCursorFlat(0)
		catalog.reader.EnsureCursorVisible()
	}
	inputHeight := 0
	if len(inputs) > 0 {
		form := catalog.inputForms[catalog.viewKey()]
		if form == nil {
			form = formModel(inputs...)
			catalog.inputForms[catalog.viewKey()] = form
		}
		catalog.form.SetForm(form, "", "")
		inputHeight = min(max(0, rect.H/2), catalog.form.PreferredHeight(rect.H))
		catalog.layoutForm(ctx, ui.Rect{X: rect.X, Y: rect.Y, W: rect.W, H: inputHeight})
	} else {
		catalog.form.Layout(ctx, ui.Rect{})
	}
	catalog.reader.Layout(ctx, ui.Rect{X: rect.X, Y: rect.Y + inputHeight, W: rect.W, H: max(0, rect.H-inputHeight)})
}

// alignKeyValues lines up each run of "Key: value" lines on one value column
// so a read-only detail reads as a table instead of a ragged list.
func alignKeyValues(text string) string {
	lines := strings.Split(text, "\n")
	keyOf := func(line string) (string, string, bool) {
		key, value, ok := strings.Cut(line, ": ")
		if !ok || key == "" || len(key) > 24 || strings.Count(key, " ") > 2 {
			return "", "", false
		}
		return key, value, true
	}
	for start := 0; start < len(lines); {
		end, width := start, 0
		for end < len(lines) {
			key, _, ok := keyOf(lines[end])
			if !ok {
				break
			}
			width = max(width, len(key))
			end++
		}
		for index := start; index < end; index++ {
			key, value, _ := keyOf(lines[index])
			lines[index] = key + strings.Repeat(" ", width-len(key)+2) + value
		}
		start = max(end, start+1)
	}
	return strings.Join(lines, "\n")
}

func (catalog *capabilityCatalog) layoutDetail(ctx ui.LayoutContext, top, height int) {
	width := catalog.Bounds().W
	if catalog.detailCollection() {
		catalog.filter.SetPlaceholder("Search " + catalog.detailPage)
		catalog.filter.Layout(ctx, ui.Rect{Y: top, W: width, H: min(1, height)})
		tableHeight := min(6, max(0, (height-2)/2))
		catalog.layoutTable(ctx, catalog.browse, catalog.browseScroll, ui.Rect{Y: top + 2, W: width, H: tableHeight})
		catalog.layoutDetailBody(ctx, ui.Rect{Y: top + 2 + tableHeight, W: width, H: max(0, height-2-tableHeight)})
	} else if catalog.kind == captypes.KindSkill && catalog.detailPage == "files" {
		treeHeight := min(6, max(0, height/3))
		barWidth, stroke := 0, 0
		if catalog.treeScroll.NeededForViewport(treeHeight) {
			barWidth = min(ui.ScrollbarWidth+1, width)
			stroke = min(ui.ScrollbarWidth, barWidth)
		}
		catalog.tree.Layout(ctx, ui.Rect{Y: top, W: max(0, width-barWidth), H: treeHeight})
		catalog.treeScroll.Layout(ctx, ui.Rect{X: width - stroke, Y: top, W: stroke, H: treeHeight})
		catalog.layoutDetailBody(ctx, ui.Rect{Y: top + treeHeight, W: width, H: max(0, height-treeHeight)})
	} else {
		catalog.layoutDetailBody(ctx, ui.Rect{Y: top, W: width, H: height})
	}
}

func (catalog *capabilityCatalog) entry() captypes.Entry {
	for _, entry := range catalog.owner.model.capabilitySnapshot.Entries {
		if entry.ID == catalog.id && entry.Kind == catalog.kind {
			return entry
		}
	}
	for _, entry := range catalog.owner.model.capabilityDiscovery[catalog.kind] {
		if entry.ID == catalog.id {
			return entry
		}
	}
	return captypes.Entry{ID: catalog.id}
}

func (catalog *capabilityCatalog) connectionState() captypes.ConnectionState {
	for _, state := range catalog.owner.model.capabilitySnapshot.Connections {
		if state.ID == catalog.id {
			return state
		}
	}
	return captypes.ConnectionState{ID: catalog.id, State: "not started", Cached: true}
}

func (catalog *capabilityCatalog) inheritedAvailable() bool {
	model := catalog.owner.model
	if model.capabilityScope() != captypes.ScopeWorkspace {
		return false
	}
	id := catalog.id
	if !catalog.detail {
		if entry, ok := catalog.selectedStoreEntry(); ok {
			id = entry.ID
		}
	}
	switch catalog.kind {
	case captypes.KindSkill:
		_, exists := model.appDefaults.Capabilities.Skills[id]
		return exists
	case captypes.KindMCP:
		_, exists := model.appDefaults.Capabilities.Connections[id]
		return exists
	default:
		_, exists := model.appDefaults.Capabilities.Sources[id]
		return exists
	}
}

func (catalog *capabilityCatalog) entityBytes() []byte {
	model := catalog.owner.model
	var entity any
	switch catalog.kind {
	case captypes.KindSkill:
		entity = model.capabilities.Skills[catalog.id]
	case captypes.KindMCP:
		entity = model.capabilities.Connections[catalog.id]
	default:
		entity = model.capabilities.Sources[catalog.id]
	}
	encoded, _ := json.Marshal(struct {
		Scope  captypes.Scope
		Kind   captypes.Kind
		Entity any
	}{model.capabilityScope(), catalog.kind, entity})
	return encoded
}

// detailActions splits a detail page's buttons into its footer decision
// (the install/review step, then Back, always last) and the page's own
// operations, which stay above the footer.
func (catalog *capabilityCatalog) detailActions(button func(string, string) ui.ActionBarButton) (decision, primary, secondary []ui.ActionBarButton) {
	back := button("Back", "back")
	decide := func(label, operation string) []ui.ActionBarButton {
		main := button(label, operation)
		main.Variant = ui.VariantPrimary
		return []ui.ActionBarButton{main, back}
	}
	if catalog.detailPage == "acquisition" && catalog.acquisition != nil {
		label := "Install reviewed"
		if catalog.acquisition.Operation == "update" {
			label = "Update reviewed"
		}
		return decide(label, "confirm-acquisition"), nil, nil
	}
	decision = []ui.ActionBarButton{back}
	entry := catalog.entry()
	if !entry.Installed && entry.State == "unsupported" {
		return decision, nil, nil
	}
	model := catalog.owner.model
	if catalog.kind == captypes.KindSkill {
		skill, installed := model.capabilities.Skills[catalog.id]
		if !installed {
			return decide("Install", "install"), nil, nil
		}
		switch catalog.detailPage {
		case "instructions":
			primary = append(primary, button("Read instructions", "read-file"))
		case "files":
			primary = append(primary, button("List files", "list-files"), button("Read file", "read-file"))
			if !skill.Managed {
				secondary = append(secondary, button("Open file", "open-file"))
			}
		case "access":
			primary = append(primary, button("Save mode", "save-mode"))
		default:
			primary = append(primary, button("Edit registration", "edit"))
		}
		if skill.Managed {
			secondary = append(secondary, button("Make local copy", "make-local-copy"))
		}
		if skill.Source != "" {
			secondary = append(secondary, button("Update", "update"))
		}
		disabled := skill.Disabled || skill.Mode == "disabled"
		secondary = append(secondary, button(mapState(disabled, "Enable", "Disable"), mapState(disabled, "enable", "disable")), button("Remove", "remove"))
		if catalog.inheritedAvailable() {
			secondary = append(secondary, button("Reset inherited", "reset-inherited"))
		}
	} else {
		connection, installed := model.capabilities.Connections[catalog.id]
		if !installed {
			return decide("Install", "install"), nil, nil
		}
		state := catalog.connectionState()
		switch catalog.detailPage {
		case "tools", "access":
			primary = append(primary, button("Inspect schema", "inspect-item"), button("Review grant", "review-grant"))
		case "resources":
			primary = append(primary, button("Fetch preview", "resource"), button("Stage context", "stage-context"))
		case "prompts":
			primary = append(primary, button("Fetch preview", "prompt"), button("Stage context", "stage-context"))
		case "connection":
			primary = append(primary, button("Edit connection", "edit"))
			if connection.Auth == "oauth" {
				secondary = append(secondary, button("Sign in", "sign-in"), button("Sign out", "sign-out"), button("Auth return", "auth-return"))
				if state.AuthPending && state.AuthURL != "" {
					secondary = append(secondary, button("Open browser", "open-auth-url"))
				}
			}
		default:
			primary = append(primary, button("Edit", "edit"))
		}
		if state.State == "connected" {
			secondary = append(secondary, button("Disconnect", "disconnect"), button("Refresh tools", "refresh-tools"))
		} else if !connection.Disabled {
			secondary = append(secondary, button("Connect", "connect"))
		}
		if state.AuthPending || model.capabilityPending != nil && model.capabilityPending.Operation == "sign-in" {
			secondary = append(secondary, button("Check sign-in", "snapshot"))
		}
		if entry.Source != "" {
			secondary = append(secondary, button("Update", "update"))
		}
		secondary = append(secondary, button(mapState(connection.Disabled, "Enable", "Disable"), mapState(connection.Disabled, "enable", "disable")), button("Remove", "remove"))
		if catalog.inheritedAvailable() {
			secondary = append(secondary, button("Reset inherited", "reset-inherited"))
		}
		for index := range primary {
			op := strings.TrimPrefix(primary[index].Action, "settings.capability.")
			if op == "stage-context" {
				preview, exists := catalog.preview[catalog.viewKey()]
				primary[index].Enabled = exists && capabilityPreviewUsable(preview) && reflect.DeepEqual(catalog.requests[preview.Sequence].draft, formDraftBytes(catalog.forms[catalog.viewKey()]))
			}
		}
	}
	return decision, primary, secondary
}

func capabilityReadOnly(id, label, body, description string) *ui.FieldState {
	field := ui.TextAreaFieldState(id, label, boundedCapabilityText(body), description)
	field.ReadOnly, field.Rows = true, 8
	return field
}

func boundedCapabilityText(body string) string {
	if len(body) <= capabilityPreviewLimit {
		return body
	}
	end := capabilityPreviewLimit
	for end > 0 && !utf8.RuneStart(body[end]) {
		end--
	}
	return body[:end] + "\n[Preview truncated at 128 KiB]"
}

func (catalog *capabilityCatalog) buildDetailForm() *ui.FormModel {
	model, entry := catalog.owner.model, catalog.entry()
	if catalog.detailPage == "acquisition" && catalog.acquisition != nil {
		preview := catalog.acquisition
		body := fmt.Sprintf("Operation: %s\nIdentity: %s\nPublisher: %s\nSource: %s\nSource location: %s\nRevision: %s\nDigest: %s\nScope: %s\n\nRequirements\n%s\n\nAccess\n%s\n\nInstallation never starts a server, executes scripts or grants tool access. Removal keeps authored files and other workspaces. Changed source, revision or binding requires a new review.", preview.Operation, preview.ID, preview.Candidate.Publisher, preview.Source.Name, preview.Source.Location, preview.Candidate.Version, preview.Candidate.Digest, preview.Scope, strings.Join(preview.Requirements, "\n"), strings.Join(preview.RequestedAccess, "\n"))
		if preview.Existing != nil {
			body += fmt.Sprintf("\n\nPrevious binding\nRevision: %s\nDigest: %s\nOrigin: %s", preview.Existing.Version, preview.Existing.Digest, preview.Existing.Origin)
		}
		return formModel(capabilityReadOnly("acquisition", "Exact installation preview", body, "Review all details before confirming. Back cancels without installation."))
	}
	if catalog.kind == captypes.KindSkill {
		skill := model.capabilities.Skills[catalog.id]
		switch catalog.detailPage {
		case "instructions":
			return formModel(capabilityReadOnly("preview", "Instructions", "Choose Read instructions to fetch SKILL.md. Opening this page performs no disk I/O.", "Source instructions never override runtime policy; managed bytes are read-only."))
		case "files":
			return formModel(ui.TextFieldState("file", "Relative file", "SKILL.md", "Containment and revision checks run in the capability service."), capabilityReadOnly("preview", "File preview", "List files, select a file, then Read file. No scripts run while browsing.", "Managed artifacts require Make local copy before editing."))
		case "access":
			return formModel(ui.SelectFieldState("mode", "Invocation", mapDefault(skill.Mode, "automatic"), []ui.FormOption{{ID: "automatic", Label: "Automatic"}, {ID: "manual", Label: "Manual only"}, {ID: "disabled", Label: "Disabled"}}, "This changes the selected scope's default, not execution permission."), capabilityReadOnly("access", "Effective access", fmt.Sprintf("Scope: %s\nState: %s\nReason: %s\n\nSkills load instructions, not executable authority. Scripts still require normal command permissions. Disabling cannot remove instructions already loaded by an agent. /skills opens the searchable activation modal.", model.capabilityScope(), entry.State, entry.Reason), ""))
		case "versions":
			return formModel(capabilityReadOnly("versions", "Selected immutable version", fmt.Sprintf("Identity: %s\nSource: %s\nRevision: %s\nDigest: %s\nScope: %s\n\nRefresh Discover explicitly before reviewing a source update. Update previews the exact new source revision and current binding before publication. Existing agent context keeps its attributed loaded version; disabling does not erase it.", skill.ID, skill.Source, skill.Version, skill.Digest, model.capabilityScope()), ""))
		default:
			return formModel(capabilityReadOnly("overview", "Provenance and availability", fmt.Sprintf("Identity: %s\nName: %s\nDescription: %s\nSource: %s\nPublisher: %s\nRevision: %s\nDigest: %s\nOrigin: %s\nSelected scope: %s\nDirectory: %s\nManaged: %t\nInvocation: %s\nState: %s\nReason: %s\n\nRemoval unregisters this identity; authored files and other workspaces remain intact.", entry.ID, entry.Name, entry.Description, entry.Source, entry.Publisher, entry.Version, entry.Digest, entry.Origin, model.capabilityScope(), entry.Path, entry.Managed, skill.Mode, entry.State, entry.Reason), ""))
		}
	}
	connection, state := model.capabilities.Connections[catalog.id], catalog.connectionState()
	switch catalog.detailPage {
	case "connection":
		fields := []*ui.FieldState{capabilityReadOnly("connection", "Saved connection", fmt.Sprintf("Transport: %s\nEndpoint: %s\nExecutable: %s\nArguments: %q\nWorking directory: %s\nAuthentication: %s\nTimeout: %d seconds\nState: %s\nCached discovery: %t\n\nOpening never connects. Test draft is detached; Save connection is still required.", connection.Transport, connection.URL, connection.Command, connection.Args, connection.Directory, connection.Auth, connection.TimeoutSeconds, state.State, state.Cached), "")}
		if connection.Auth == "oauth" {
			fields = append(fields, capabilityReadOnly("auth-url", "Sign-in URL", state.AuthURL, "Open this endpoint-owned URL in your browser; paste the exact return URL below."), ui.TextFieldState("auth-return", "Authorization return", "", "Consumed only by Auth return; never copied to logs or previews."))
		}
		return formModel(fields...)
	case "tools", "access":
		return formModel(capabilityReadOnly("schema", "Tool schema and effective access", "Select a tool and Inspect schema. Review grant assigns an explicit effect, scope and observed fingerprint; server annotations are not grants.", "MCP writes are outside native file ownership and undo unless explicitly integrated."))
	case "resources":
		return formModel(ui.TextFieldState("uri", "Resource URI", "", "Select a resource or enter its exact URI."), capabilityReadOnly("preview", "Resource preview", "Fetch preview reads explicitly. Stage context attaches only a successfully fetched preview to the owning composer.", ""))
	case "prompts":
		return formModel(capabilityReadOnly("preview", "Prompt preview", "Select a prompt to enter its arguments, then Fetch preview. It is never executed or submitted by browsing.", ""))
	case "activity":
		return formModel(capabilityReadOnly("activity", "Redacted activity", catalog.activityText(), "Read-only logs. Credentials and authorization return payloads are never shown here."))
	default:
		return formModel(capabilityReadOnly("overview", "Connection identity and availability", fmt.Sprintf("Identity: %s\nName: %s\nDescription: %s\nSource: %s\nPublisher: %s\nRevision: %s\nOrigin: %s\nSelected scope: %s\nConnection: %s\nCached discovery: %t\nReason: %s\n\nCoverage: managed stdio and Streamable HTTP; endpoint-bound OAuth; bounded tools/resources/prompts discovery; schema-change review; text, structured and image tool results; progress, timeouts and cancellation. Resource/prompt composer previews are text-only. Elicitation, sampling, task extensions, remote channels and rich MCP apps are unsupported and not advertised.\n\nSelection is not approval. Unknown effects or changed schemas fail closed. External writes are outside native file ownership and undo.", entry.ID, entry.Name, entry.Description, entry.Source, entry.Publisher, entry.Version, entry.Origin, model.capabilityScope(), state.State, state.Cached, entry.Reason), ""))
	}
}

func (catalog *capabilityCatalog) activityText() string {
	state := catalog.connectionState()
	return boundedCapabilityText(fmt.Sprintf("State: %s\nCached: %t\nFailure: %s\nObserved fingerprint: %s\n\n%s", state.State, state.Cached, state.Error, state.Fingerprint, strings.Join(state.Logs, "\n")))
}

func (catalog *capabilityCatalog) syncDetailRows() {
	if !catalog.detail || catalog.edit != "" {
		return
	}
	state := catalog.connectionState()
	if catalog.kind == captypes.KindSkill {
		if catalog.detailPage == "overview" {
			if form := catalog.forms[catalog.viewKey()]; form != nil {
				fresh := catalog.buildDetailForm()
				form.Field("overview").Value = fresh.Field("overview").Value
			}
		}
		if catalog.detailPage == "files" {
			items := make([]ui.TreeItem, 0, len(catalog.files[catalog.id]))
			for _, path := range catalog.files[catalog.id] {
				items = append(items, ui.TreeItem{ID: path, Label: path, Depth: strings.Count(path, "/"), Payload: path})
			}
			catalog.tree.SetItems(items)
		}
		return
	}
	if form := catalog.forms[catalog.viewKey()]; form != nil {
		if slices.Contains([]string{"overview", "connection", "activity"}, catalog.detailPage) {
			fresh := catalog.buildDetailForm()
			for _, field := range form.Fields() {
				if field.ReadOnly {
					if updated := fresh.Field(field.ID); updated != nil {
						field.Value = updated.Value
					}
				}
			}
		}
		if field := form.Field("activity"); field != nil {
			field.Value = ui.StringValue(catalog.activityText())
			if control := catalog.form.Field(field.ID); control != nil {
				control.Sync()
			}
		}
		if field := form.Field("auth-url"); field != nil {
			field.Value = ui.StringValue(state.AuthURL)
			if control := catalog.form.Field(field.ID); control != nil {
				control.Sync()
			}
		}
	}
	query := strings.ToLower(catalog.filter.Value())
	var rows []ui.TableRow
	add := func(id, name, description, status string, payload any) {
		if !strings.Contains(strings.ToLower(id+" "+name+" "+description+" "+status), query) {
			return
		}
		rows = append(rows, ui.TableRow{ID: id, Payload: payload, Cells: []ui.TableCell{{Text: name}, {Text: description, Role: ui.ColorMuted}, {Text: status, Role: ui.ColorMuted}}})
	}
	switch catalog.detailPage {
	case "tools", "access":
		for _, tool := range state.Tools {
			add(tool.Name, tool.OriginalName, tool.Description, mapDefault(tool.Reason, string(tool.Effect)+" · "+tool.Scope), tool)
		}
	case "resources":
		for _, resource := range state.Resources {
			add(resource.URI, resource.Name, resource.URI, resource.MIMEType, resource)
		}
	case "prompts":
		for _, prompt := range state.Prompts {
			add(prompt.Name, prompt.Name, prompt.Description, fmt.Sprintf("%d arguments", len(prompt.Arguments)), prompt)
		}
	default:
		return
	}
	stored := catalog.views[catalog.viewKey()]
	catalog.browse.SetColumns([]ui.TableColumn{{ID: "name", Label: "Name", Track: ui.FractionGridTrack(2)}, {ID: "description", Label: "Description", Track: ui.FractionGridTrack(3)}, {ID: "access", Label: mapState(state.Cached, "Cached / unverified", "Access / type"), Track: ui.FractionGridTrack(2)}})
	catalog.browse.SetRows(rows)
	for index, row := range rows {
		if row.ID == stored.selected {
			catalog.browse.SelectIndex(index)
			break
		}
	}
	catalog.browse.Scroll().ScrollTo(stored.offset)
	if (catalog.detailPage == "tools" || catalog.detailPage == "access") && stored.inspected != "" {
		form := catalog.forms[catalog.viewKey()]
		if form == nil || form.Field("schema") == nil {
			return
		}
		body := fmt.Sprintf("Tool %s is no longer observed on this connection. Refresh live discovery before reviewing access; an earlier schema or grant is not current evidence.", stored.inspected)
		for _, tool := range state.Tools {
			if tool.Name == stored.inspected {
				body = capabilityToolDetail(tool)
				break
			}
		}
		// This is a read-only projection, not a user edit. FormModel.SetValue
		// deliberately refuses read-only fields.
		form.Field("schema").Value = ui.StringValue(body)
		if control := catalog.form.Field("schema"); control != nil {
			control.Sync()
		}
	}
}

func capabilityToolDetail(tool captypes.Tool) string {
	var schema any
	text := string(tool.InputSchema)
	if json.Unmarshal(tool.InputSchema, &schema) == nil {
		encoded, _ := json.MarshalIndent(schema, "", "  ")
		text = string(encoded)
	}
	return fmt.Sprintf("Stable name: %s\nOriginal: %s\nDescription: %s\nEffect: %s\nScope: %s\nFingerprint: %s\nEffective: %s\n\n%s", tool.Name, tool.OriginalName, tool.Description, tool.Effect, tool.Scope, tool.Fingerprint, mapDefault(tool.Reason, "Granted; agent role and selection still apply"), text)
}

func (catalog *capabilityCatalog) inspectItem() {
	row, ok := catalog.browse.Selected()
	if !ok {
		catalog.message = "No cached items. Connect, then Refresh tools."
		return
	}
	form := catalog.forms[catalog.viewKey()]
	switch value := row.Payload.(type) {
	case captypes.Tool:
		stored := catalog.views[catalog.viewKey()]
		stored.inspected = value.Name
		catalog.views[catalog.viewKey()] = stored
		form = formModel(capabilityReadOnly("schema", "Schema and effective access", capabilityToolDetail(value), "Annotations do not grant authority. Changed fingerprints require review."))
	case captypes.Resource:
		form.SetValue("uri", ui.StringValue(value.URI))
	case captypes.Prompt:
		fields := []*ui.FieldState{capabilityReadOnly("prompt-name", "Prompt", value.Name, value.Description)}
		for _, argument := range value.Arguments {
			field := ui.TextFieldState("argument/"+argument.Name, argument.Name, "", argument.Description)
			field.Required = argument.Required
			fields = append(fields, field)
		}
		fields = append(fields, capabilityReadOnly("preview", "Prompt preview", "Fetch preview after entering arguments. Nothing is submitted automatically.", ""))
		form = formModel(fields...)
	}
	catalog.forms[catalog.viewKey()] = form
	delete(catalog.inputForms, catalog.viewKey())
	catalog.form.SetForm(form, "", "")
	delete(catalog.preview, catalog.viewKey())
}

func (catalog *capabilityCatalog) selectFile() {
	if form := catalog.forms[catalog.viewKey()]; form != nil {
		form.SetValue("file", ui.StringValue(catalog.tree.ActiveID()))
	}
}

func (catalog *capabilityCatalog) detailOperation(operation string) ui.EventResult {
	form := catalog.forms[catalog.viewKey()]
	value := func(id string) string {
		if form != nil {
			if field := form.Field(id); field != nil {
				return field.Value.String()
			}
		}
		return ""
	}
	request := captypes.Action{Operation: operation, Kind: catalog.kind, ID: catalog.id}
	switch operation {
	case "read-file", "open-file":
		request.Content = value("file")
		if catalog.detailPage == "instructions" {
			request.Content = "SKILL.md"
		}
	case "resource":
		request.Content = value("uri")
	case "prompt":
		request.Content = value("prompt-name")
		request.Arguments = make(map[string]string)
		if form != nil {
			form.ValidateAll()
			if form.AnyErrors() {
				catalog.message = "Enter required prompt arguments."
				return catalog.changed()
			}
			for _, field := range form.Fields() {
				if strings.HasPrefix(field.ID, "argument/") {
					request.Arguments[strings.TrimPrefix(field.ID, "argument/")] = field.Value.String()
				}
			}
		}
	case "stage-context":
		preview, ok := catalog.preview[catalog.viewKey()]
		if !ok || !capabilityPreviewUsable(preview) || !reflect.DeepEqual(catalog.requests[preview.Sequence].draft, formDraftBytes(form)) {
			catalog.message = "Fetch the current URI or arguments before staging context."
			return catalog.changed()
		}
		request.Content = preview.Content
		request.Arguments = map[string]string{"operation": preview.Action.Operation, "source": preview.Action.Content, "sequence": strconv.FormatUint(preview.Sequence, 10)}
		if !reflect.DeepEqual(catalog.requests[preview.Sequence].entity, catalog.entityBytes()) {
			catalog.message = "Connection changed; fetch a fresh preview before staging."
			return catalog.changed()
		}
	case "auth-return":
		request.Content = value("auth-return")
	case "save-mode":
		skill, exists := catalog.owner.model.capabilities.Skills[catalog.id]
		if !exists {
			catalog.message = "Install the skill before changing invocation."
			return catalog.changed()
		}
		skill.Mode, skill.Disabled = value("mode"), value("mode") == "disabled"
		request.Operation, request.Skill = "save-skill", &skill
	}
	if slices.Contains([]string{"resource", "prompt", "read-file", "open-file", "auth-return"}, operation) && request.Content == "" {
		catalog.message = "Select an item or enter its exact identity first."
		return catalog.changed()
	}
	return catalog.submit(request)
}

func (catalog *capabilityCatalog) submit(action captypes.Action) ui.EventResult {
	sequence, command := catalog.owner.model.requestCapability(action)
	if command == nil {
		catalog.message = "Another capability operation is running; cancel it first."
		return catalog.changed()
	}
	form := catalog.forms[catalog.viewKey()]
	if catalog.edit != "" || action.Operation == "save-skill" {
		form = catalog.owner.model.capabilityDraft
	}
	if action.Operation != "snapshot" {
		catalog.requests[sequence] = capabilityViewRequest{key: catalog.viewKey(), draft: formDraftBytes(form), action: action, form: form, entity: catalog.entityBytes()}
	}
	catalog.actions()
	result := catalog.changed()
	result.Cmd = command
	return result
}

func (catalog *capabilityCatalog) consumeResult() {
	result := catalog.owner.model.capabilityResult
	if result == nil || result.Sequence == catalog.resultSeen {
		return
	}
	catalog.resultSeen = result.Sequence
	request, owned := catalog.requests[result.Sequence]
	if !owned {
		return
	}
	keepRequest := false
	defer func() {
		if !keepRequest {
			delete(catalog.requests, result.Sequence)
		}
	}()
	if result.Err != nil {
		catalog.message = result.Err.Error()
		if (request.action.Operation == "discover" || request.action.Operation == "search") && len(result.Entries) > 0 {
			catalog.syncRows()
		}
		return
	}
	catalog.message = request.action.Operation + " completed"
	if request.action.Operation == "discover" || request.action.Operation == "search" {
		catalog.syncRows()
		catalog.message = ""
		if !catalog.detail && catalog.choosing == "" && catalog.discoveryCurrent(catalog.views[catalog.viewKey()]) {
			catalog.owner.focusID(catalog.store.ID())
		}
	}
	if strings.HasPrefix(request.action.Operation, "preview-") {
		var preview captypes.AcquisitionPreview
		if json.Unmarshal([]byte(result.Content), &preview) != nil || preview.Token == "" || preview.ID != request.action.ID || catalog.id != preview.ID || preview.Scope != catalog.owner.model.capabilityScope() {
			catalog.message = "Preview owner changed or response is invalid; review again."
			return
		}
		catalog.saveListState()
		catalog.acquisition = &preview
		catalog.detail = true
		delete(catalog.forms, string(catalog.kind)+"/"+catalog.id+"/acquisition")
		catalog.detailForm("acquisition")
		catalog.focusDetailInput()
		catalog.message = "Exact preview ready; nothing installed."
		return
	}
	if request.action.Operation == "install" || request.action.Operation == "update" {
		catalog.acquisition = nil
		if catalog.acquisitionReturnToStore {
			catalog.detail, catalog.id = false, ""
			catalog.setTabs()
			catalog.restoreFilter()
			catalog.syncRows()
			catalog.owner.focusID(catalog.store.ID())
			catalog.message = "Installed. Manage opens its settings; nothing was executed."
		} else {
			catalog.detailForm("overview")
		}
	}
	if request.action.Operation == "test" {
		if request.form != catalog.owner.model.capabilityDraft || !reflect.DeepEqual(request.draft, formDraftBytes(catalog.owner.model.capabilityDraft)) {
			catalog.message = "Detached test finished for an older draft; current edits are untested."
			return
		}
		catalog.message = fmt.Sprintf("Detached draft tested (request %d); Save connection is still required.", result.Sequence)
		return
	}
	if request.action.Operation == "list-files" {
		var paths []string
		if json.Unmarshal([]byte(result.Content), &paths) != nil {
			catalog.message = "File listing returned an unsupported response."
			return
		}
		sort.Strings(paths)
		catalog.files[request.action.ID] = paths
		catalog.syncDetailRows()
	}
	if slices.Contains([]string{"read-file", "resource", "prompt"}, request.action.Operation) {
		form := catalog.forms[request.key]
		if form == nil || form != request.form || !reflect.DeepEqual(request.draft, formDraftBytes(form)) {
			catalog.message = "Preview belongs to earlier file or argument edits; fetch again."
			return
		}
		if request.key == catalog.viewKey() && !reflect.DeepEqual(request.entity, catalog.entityBytes()) {
			catalog.message = "Capability revision changed; fetch a fresh preview."
			return
		}
		catalog.preview[request.key] = *result
		keepRequest = true
		for sequence, prior := range catalog.requests {
			if sequence != result.Sequence && prior.key == request.key && slices.Contains([]string{"read-file", "resource", "prompt"}, prior.action.Operation) {
				delete(catalog.requests, sequence)
			}
		}
		if field := form.Field("preview"); field != nil {
			field.Value = ui.StringValue(capabilityResultText(*result))
			if request.key == catalog.viewKey() {
				if control := catalog.form.Field("preview"); control != nil {
					control.Sync()
				}
			}
		}
		catalog.message = "Preview ready; nothing was submitted."
		if (request.action.Operation == "resource" || request.action.Operation == "prompt") && !capabilityPreviewUsable(*result) {
			catalog.message = "Remote response is failed, unknown or unsupported; it cannot be staged."
		}
	}
	if slices.Contains([]string{"save-connection", "set-secrets", "save-skill", "save-source", "create-skill", "add-local", "import", "make-local-copy"}, request.action.Operation) {
		if request.form != nil && catalog.owner.model.capabilityDraft == request.form && reflect.DeepEqual(request.draft, formDraftBytes(catalog.owner.model.capabilityDraft)) {
			catalog.baseline = slices.Clone(request.draft)
			catalog.owner.model.capabilityDraft.Commit()
			if request.action.Connection != nil {
				catalog.draftOriginal.Connection = request.action.Connection
			}
		}
		if form := catalog.forms[request.key]; form != nil && form == request.form && reflect.DeepEqual(request.draft, formDraftBytes(form)) {
			form.Commit()
		}
	}
	if request.action.Operation == "auth-return" {
		if form := catalog.forms[request.key]; form != nil {
			form.SetValue("auth-return", ui.StringValue(""))
		}
	}
	if request.action.Operation == "cancel" {
		catalog.message = "Cancellation requested; completed effects and other acquisition users are unchanged."
		for sequence := range catalog.requests {
			if _, pending := catalog.owner.model.capabilityRequests[sequence]; !pending {
				delete(catalog.requests, sequence)
			}
		}
	}
	catalog.actions()
}

func capabilityPreviewUsable(result CapabilityResultMsg) bool {
	var response captypes.Result
	return result.Err == nil && json.Unmarshal([]byte(result.Content), &response) == nil && !response.IsError && !response.Unknown && len(result.Content) <= 1<<20
}

func capabilityResultText(result CapabilityResultMsg) string {
	if result.Action.Operation == "read-file" {
		return boundedCapabilityText(result.Content)
	}
	var response captypes.Result
	if json.Unmarshal([]byte(result.Content), &response) != nil {
		return boundedCapabilityText(result.Content)
	}
	var body strings.Builder
	if response.IsError {
		body.WriteString("Remote result reports an error; context cannot be staged.\n")
	}
	if response.Unknown {
		body.WriteString("Result unknown; do not blindly retry.\n")
	}
	for _, content := range response.Content {
		if content.URI != "" {
			fmt.Fprintf(&body, "Source: %s\n", content.URI)
		}
		body.WriteString(content.Text)
		if content.Data != "" {
			fmt.Fprintf(&body, "\n[Binary content: %s; %d encoded bytes]", content.MIMEType, len(content.Data))
		}
		body.WriteString("\n")
	}
	if len(response.Structured) > 0 {
		body.Write(response.Structured)
	}
	return boundedCapabilityText(body.String())
}

func (catalog *capabilityCatalog) beginGrant() ui.EventResult {
	row, ok := catalog.browse.Selected()
	tool, valid := row.Payload.(captypes.Tool)
	if !ok || !valid || tool.Fingerprint == "" {
		catalog.message = "Select an observed tool with a fingerprint first."
		return catalog.changed()
	}
	connection := captypes.Clone(catalog.owner.model.capabilities).Connections[catalog.id]
	grant := connection.Grants[tool.OriginalName]
	fields := []*ui.FieldState{capabilityReadOnly("grant-tool", "Stable tool identity", tool.Name, tool.Description), capabilityReadOnly("grant-original", "Server tool identity", tool.OriginalName, "Grant keys are the server's exact original tool names."), capabilityReadOnly("grant-fingerprint", "Observed fingerprint", tool.Fingerprint, "Saving binds this grant to these exact observed bytes."), ui.CheckboxFieldState("grant-enabled", "Allow selected operation", !grant.Disabled && grant.Fingerprint == tool.Fingerprint && grant.Effect != "", "A role or workspace ceiling can still deny use."), ui.SelectFieldState("grant-effect", "Effect", string(grant.Effect), []ui.FormOption{{ID: "", Label: "Choose explicitly"}, {ID: "read", Label: "Read"}, {ID: "write", Label: "Write"}, {ID: "delete", Label: "Delete"}, {ID: "execute", Label: "Execute"}, {ID: "communication", Label: "Communication"}}, "Never infer authority from a read-only annotation."), ui.TextFieldState("grant-scope", "Resource scope", grant.Scope, "Explicit approved boundary; a scope label alone does not sandbox remote effects.")}
	catalog.edit = "grant"
	catalog.draftOriginal = captypes.Action{Kind: captypes.KindMCP, ID: catalog.id, Connection: &connection}
	catalog.owner.model.capabilityDraft = formModel(fields...)
	catalog.baseline = formDraftBytes(catalog.owner.model.capabilityDraft)
	catalog.form.SetForm(catalog.owner.model.capabilityDraft, "", "")
	catalog.owner.focusID(catalog.form.ID())
	catalog.actions()
	return catalog.changed()
}

func (catalog *capabilityCatalog) grantAction() (captypes.Action, error) {
	form := catalog.owner.model.capabilityDraft
	value := func(id string) string { return form.Field(id).Value.String() }
	connection := catalog.draftOriginal.Connection
	if connection == nil || !reflect.DeepEqual(*connection, catalog.owner.model.capabilities.Connections[catalog.id]) {
		return captypes.Action{}, fmt.Errorf("connection changed; reopen access review")
	}
	observed := false
	for _, tool := range catalog.connectionState().Tools {
		if tool.OriginalName == value("grant-original") && tool.Fingerprint == value("grant-fingerprint") {
			observed = true
		}
	}
	if !observed {
		return captypes.Action{}, fmt.Errorf("schema changed; inspect the current fingerprint before granting")
	}
	effect := captypes.Effect(value("grant-effect"))
	enabled := form.Field("grant-enabled").Value.Bool()
	if enabled && (!slices.Contains([]captypes.Effect{captypes.EffectRead, captypes.EffectWrite, captypes.EffectDelete, captypes.EffectExecute, captypes.EffectCommunication}, effect) || strings.TrimSpace(value("grant-scope")) == "") {
		return captypes.Action{}, fmt.Errorf("choose an explicit effect and non-empty resource scope")
	}
	copy := captypes.Clone(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{catalog.id: *connection}}).Connections[catalog.id]
	if copy.Grants == nil {
		copy.Grants = make(map[string]captypes.ToolGrant)
	}
	copy.Grants[value("grant-original")] = captypes.ToolGrant{Effect: effect, Scope: strings.TrimSpace(value("grant-scope")), Fingerprint: value("grant-fingerprint"), Disabled: !enabled}
	return captypes.Action{Operation: "save-connection", Kind: captypes.KindMCP, ID: catalog.id, Connection: &copy}, nil
}
