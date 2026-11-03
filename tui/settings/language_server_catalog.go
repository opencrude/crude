package settings

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/insight/catalog"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/types"
)

const (
	lspCatalogID ui.ComponentID = "settings/language-servers"
	// LanguageServerFormID is the retained per-server editor form.
	LanguageServerFormID ui.ComponentID = "settings/language-servers/form"
)

var lspIDPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// lspCatalog is the unified language-tools collection and its configuration
// editors. Installed-tool operations retain the separate store owner.
type lspCatalog struct {
	ui.Base
	owner         *WorkspaceSettingsSurface
	filter        *lspCatalogFilter
	project       *lspToolFilter
	installed     *lspToolFilter
	all           *lspToolFilter
	table         *ui.Table
	listScroll    *ui.ScrollbarControl
	tabs          *ui.Tabs
	form          *ui.Form
	formScroll    *ui.ScrollbarControl
	bar           *ui.ActionBar
	more          *ui.ActionBar
	draft         *ui.FormModel
	editing       string
	notice        string
	detailFields  []*ui.FieldState
	rows          map[string]languageToolRow
	detail        *languageToolRow
	detailPage    string
	history       bool
	filterTouched bool
	compact       bool
	// before is the server inventory when the editor opened: Back restores
	// it, so an abandoned Add or edit leaves nothing behind.
	before       map[string]types.LSPLanguageSettings
	beforeResets [][]string
	snapshotted  bool
}

// snapshot records the inventory once per editor session.
func (c *lspCatalog) snapshot() {
	if c.snapshotted {
		return
	}
	m := c.model()
	c.before, c.beforeResets, c.snapshotted = cloneLSPLanguages(m.lspLanguages), slices.Clone(m.resetPaths), true
}

type lspCatalogFilter struct {
	*ui.FilterField
	owner *lspCatalog
}

func (f *lspCatalogFilter) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyDown, tea.KeyEnter:
			f.owner.owner.focusID(f.owner.table.ID())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case tea.KeyTab:
			if evt.Key.Mod.Contains(tea.ModShift) {
				f.owner.owner.FocusCategories()
			} else if f.owner.project.Bounds().Empty() {
				f.owner.owner.focusID(f.owner.table.ID())
			} else {
				f.owner.owner.focusID(f.owner.project.ID())
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	before := f.Value()
	result := f.FilterField.Handle(ctx, evt)
	if f.Value() != before {
		f.owner.refreshRows()
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return result
}

type lspToolFilter struct {
	*ui.ToggleButton
	owner *lspCatalog
}

func (f *lspToolFilter) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		c := f.owner
		switch f.ID() {
		case c.project.ID():
			if evt.Key.Mod.Contains(tea.ModShift) {
				c.owner.focusID(c.filter.ID())
			} else {
				c.owner.focusID(c.installed.ID())
			}
		case c.installed.ID():
			if evt.Key.Mod.Contains(tea.ModShift) {
				c.owner.focusID(c.project.ID())
			} else {
				c.owner.focusID(c.all.ID())
			}
		default:
			if evt.Key.Mod.Contains(tea.ModShift) {
				c.owner.focusID(c.installed.ID())
			} else {
				c.owner.focusID(c.table.ID())
			}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return f.ToggleButton.Handle(ctx, evt)
}

func newLSPCatalog(owner *WorkspaceSettingsSurface) *lspCatalog {
	t := owner.tokens
	c := &lspCatalog{Base: ui.NewBase(lspCatalogID), owner: owner}
	c.filter = &lspCatalogFilter{FilterField: ui.NewFilterField("settings/language-servers/search", t), owner: c}
	c.filter.SetPlaceholder("Search tools, languages and commands")
	c.filter.SetPrompt(ui.SearchGlyphEnd(t))
	c.filter.SetPromptPlacement(ui.PromptEnd)
	c.project = &lspToolFilter{ToggleButton: ui.NewToggleButton("settings/language-tools/project", "This project", "lsp.filter-project", t), owner: c}
	c.installed = &lspToolFilter{ToggleButton: ui.NewToggleButton("settings/language-servers/installed", "Installed", "lsp.filter-installed", t), owner: c}
	c.all = &lspToolFilter{ToggleButton: ui.NewToggleButton("settings/language-tools/all", "All", "lsp.filter-all", t), owner: c}
	c.project.SetChip(true)
	c.installed.SetChip(true)
	c.all.SetChip(true)
	c.all.SetChecked(true)
	c.table = ui.NewTable("settings/language-servers/table", t)
	c.table.SetActivateAction("lsp.open")
	c.table.SetShowHeader(true)
	c.table.SetColumnGap(2)
	c.table.SetColumns([]ui.TableColumn{
		{ID: "tool", Label: "Tool", Track: ui.FractionGridTrack(4)},
		{ID: "languages", Label: "Languages", Track: ui.FractionGridTrack(3)},
		{ID: "installed", Label: "Installed by", Track: ui.FixedGridTrack(13)},
		{ID: "project", Label: "This project", Track: ui.FixedGridTrack(13)},
	})
	c.listScroll = ui.NewScrollbarControl("settings/language-servers/scroll", c.table.Scroll(), t)
	c.listScroll.SetSurfaceBackground(ui.ColorPopupBg)
	c.listScroll.SetPointerTargetSize(1)
	c.tabs = ui.NewTabs("settings/language-tools/detail-tabs", t)
	c.tabs.SetChrome(ui.TabsPopup)
	c.form = newSettingsForm(LanguageServerFormID, t)
	c.formScroll = ui.NewScrollbarControl("settings/language-servers/form-scroll", c.form.Scroll(), t)
	c.formScroll.SetSurfaceBackground(ui.ColorPopupBg)
	c.formScroll.SetPointerTargetSize(1)
	c.bar = ui.NewActionBar("settings/language-servers/actions", t)
	c.bar.SetAlignEnd(false)
	c.bar.SetExitActions("lsp.bar.forward", "lsp.bar.backward")
	c.bar.SetCancelAction("lsp.cancel")
	c.more = ui.NewActionBar("settings/language-tools/more", t)
	c.more.SetAlignEnd(false)
	c.more.SetExitActions("lsp.more.forward", "lsp.more.backward")
	c.more.SetCancelAction("lsp.cancel")
	return c
}

func (c *lspCatalog) model() *Model { return c.owner.model }

// syncModel adopts a replaced settings model, dropping any editor that
// belonged to the old one.
func (c *lspCatalog) syncModel() {
	if !c.owner.lspServersOpen() {
		c.detail, c.history = nil, false
		c.detailFields = nil
	}
	if c.draft != nil {
		if _, ok := c.model().lspServer(c.editing); !ok {
			c.closeEditor()
		}
	}
	if !c.filterTouched && c.model().codeTools.loaded && c.model().codeTools.inventory.Project.State == types.AssemblyReady {
		c.project.SetChecked(true)
		c.installed.SetChecked(false)
		c.all.SetChecked(false)
	}
	c.refreshRows()
	if c.detail != nil && c.detail.managed != nil && c.model().codeTools.detail != "" {
		// Use the latest store observation for an open identity, not the row
		// copy captured when the detail first opened.
		for _, tool := range c.model().codeTools.inventory.Tools {
			if tool.ID == c.model().codeTools.detail {
				latest := tool
				c.detail.managed = &latest
				break
			}
		}
	}
	if c.detail != nil && c.detail.managed != nil && c.model().codeTools.detail == "" &&
		!c.model().codeTools.busy && c.model().codeTools.preview == nil {
		c.detail = nil
	}
	if c.draft == nil && (c.detail != nil || c.history || c.model().codeTools.preview != nil) {
		c.showDetail()
	}
}

func (c *lspCatalog) refreshRows() {
	m := c.model()
	if m == nil {
		return
	}
	selected := ""
	if row, ok := c.table.Selected(); ok {
		selected = row.ID
	}
	query := strings.ToLower(strings.TrimSpace(c.filter.Value()))
	var rows []ui.TableRow
	c.rows = make(map[string]languageToolRow)
	for _, tool := range languageToolRows(m.lspServers(), m.codeTools.inventory) {
		if c.installed.Checked() && tool.managed == nil && tool.external == nil {
			continue
		}
		project, inProject := tool.projectState(m.codeTools.inventory.Project)
		if c.project.Checked() && !inProject {
			continue
		}
		languages := tool.languages()
		search := tool.name + " " + tool.command + " " + languages
		for _, server := range tool.servers {
			search += " " + server.id + " " + server.name + " " + strings.Join(server.def.Exts, " ") +
				" " + strings.Join(server.cfg.Extensions, " ") + " " + strings.Join(server.cfg.Args, " ")
		}
		if query != "" && !strings.Contains(strings.ToLower(search), query) {
			continue
		}
		owner := "Not checked"
		switch {
		case tool.managed != nil:
			owner = "Crude"
		case tool.external != nil:
			owner = "Outside Crude"
		case inProject && project == "Not installed":
			owner = "Not installed"
		case m.codeTools.busy:
			owner = "Checking"
		case m.codeTools.service == nil || !m.codeTools.loaded:
			owner = "Not checked"
		}
		c.rows[tool.id] = tool
		cells := []ui.TableCell{{Text: tool.name}}
		if !c.compact {
			cells = append(cells, ui.TableCell{Text: languages, Role: ui.ColorMuted})
		}
		cells = append(cells, ui.TableCell{Text: owner, Role: ui.ColorMuted}, ui.TableCell{Text: project, Role: ui.ColorMuted})
		rows = append(rows, ui.TableRow{ID: tool.id, Cells: cells})
	}
	c.table.SetRows(rows)
	for index, row := range rows {
		if row.ID == selected {
			c.table.SelectIndex(index)
		}
	}
	c.rebuildBar()
}

func (c *lspCatalog) selected() (lspServer, bool) {
	row, ok := c.table.Selected()
	if !ok {
		return lspServer{}, false
	}
	tool, ok := c.rows[row.ID]
	if !ok || len(tool.servers) == 0 {
		return lspServer{}, false
	}
	return tool.servers[0], true
}

func (c *lspCatalog) rebuildBar() {
	if c.draft != nil {
		server, _ := c.model().lspServer(c.editing)
		// Save keeps the server and persists; Back restores the inventory
		// from when the editor opened.
		buttons := []ui.ActionBarButton{{Label: "Restore default", Action: "lsp.reset", Enabled: true}}
		if !server.builtin {
			buttons = []ui.ActionBarButton{{Label: "Remove server", Action: "lsp.remove", Variant: ui.VariantDanger, Enabled: true}}
		}
		c.bar.SetButtons(append(buttons,
			ui.ActionBarButton{Label: "Save", Action: "lsp.save", Variant: ui.VariantPrimary, Enabled: true},
			ui.ActionBarButton{Label: "Back", Action: "lsp.cancel", Enabled: true})...)
		return
	}
	r := &c.model().codeTools
	if c.detail != nil || c.history || r.preview != nil {
		var buttons []ui.ActionBarButton
		if r.preview != nil || c.detail != nil && c.detail.managed != nil {
			buttons = append(buttons, ui.ActionBarButton{Label: "Remove", Action: "settings.code-tools.remove", Variant: ui.VariantDanger, Enabled: !r.busy && r.service != nil})
		} else if c.history {
			buttons = append(buttons, ui.ActionBarButton{Label: "Refresh", Action: "settings.code-tools.refresh", Enabled: !r.busy && r.service != nil})
			buttons = append(buttons, ui.ActionBarButton{Label: "Remove unused", Action: "settings.code-tools.remove-unused", Variant: ui.VariantDanger, Enabled: !r.busy && r.loaded && len(r.inventory.Tools) > 0})
		} else if c.detail != nil {
			if _, ok := c.detail.setupTarget(r.inventory.Project); ok {
				buttons = append(buttons, ui.ActionBarButton{Label: "Set up tools", Action: "lsp.setup", Variant: ui.VariantPrimary, Enabled: !r.busy})
			}
		}
		buttons = append(buttons, ui.ActionBarButton{Label: "Back", Action: "lsp.tool.back", Enabled: !r.busy})
		c.bar.SetButtons(buttons...)
		return
	}
	buttons := []ui.ActionBarButton{
		{Label: "Set up tools", Action: "lsp.setup", Variant: ui.VariantPrimary, Enabled: true},
		{Label: "Add server", Action: "lsp.add", Enabled: true},
	}
	c.bar.SetButtons(buttons...)
	c.more.SetButtons(
		ui.ActionBarButton{Label: "History", Action: "lsp.history", Enabled: r.service != nil},
		ui.ActionBarButton{Label: "Refresh", Action: "settings.code-tools.refresh", Enabled: !r.busy && r.service != nil},
	)
}

func (c *lspCatalog) historyFields(tool *types.CodeTool) []*ui.FieldState {
	r := &c.model().codeTools
	var history strings.Builder
	for _, event := range r.inventory.History {
		if tool != nil && event.ToolID != tool.ID &&
			(event.ToolID != "" || event.Package != tool.Package || event.Version != tool.Version) {
			continue
		}
		fmt.Fprintf(&history, "%s  %s  %s %s\nBy: %s\nApproval: %s\n%s\n\n",
			codeToolTime(event.Time, "Time not recorded"), codeToolHistoryKind(event.Kind),
			event.Package, event.Version, event.By, event.Approval, event.Detail)
	}
	empty := "No events on this history page."
	fields := []*ui.FieldState{codeToolText("tool-history", "History", mapDefault(history.String(), empty))}
	if tool == nil && len(r.inventory.Unrecognized) > 0 {
		fields = append(fields, codeToolText("tool-unrecognized", "Other store entries",
			strings.Join(r.inventory.Unrecognized, "\n")+"\nCrude cannot remove these entries."))
	}
	if r.inventory.HistoryOffset > 0 {
		fields = append(fields, remoteOperation("settings.code-tools.newer", "Newer history", "Previous page", "Read newer history.", r.busy))
	}
	if r.inventory.HistoryOffset+len(r.inventory.History) < r.inventory.HistoryTotal {
		fields = append(fields, remoteOperation("settings.code-tools.older", "Older history", "Next page", "Read older history.", r.busy))
	}
	return fields
}

// showDetail composes the existing exact store accounting with each applicable
// per-language launch editor. Store paths are never inferred from a command.
func (c *lspCatalog) showDetail() {
	m, r := c.model(), &c.model().codeTools
	var fields []*ui.FieldState
	switch {
	case r.preview != nil:
		c.detailFields = nil
		c.form.SetForm(buildCodeToolRemovalForm(r.preview), "", "")
		c.rebuildBar()
		return
	case c.history:
		fields = c.historyFields(nil)
	case c.detail != nil:
		tool := c.detail
		items := []ui.TabItem{{ID: "overview", Label: "Overview"}, {ID: "launch", Label: "Launch"}}
		if tool.managed != nil {
			items = append(items, ui.TabItem{ID: "files", Label: "Files"}, ui.TabItem{ID: "history", Label: "History"})
		}
		c.tabs.SetItems(items, c.detailPage)
		c.detailPage = c.tabs.ActiveID()
		switch c.detailPage {
		case "launch":
			for _, old := range tool.servers {
				server, ok := m.lspServer(old.id)
				if !ok {
					continue
				}
				status := "On"
				if !server.cfg.Enabled {
					status = "Off"
				}
				fields = append(fields, &ui.FieldState{
					ID: "configure/" + server.id, Label: "Configure " + server.name, Type: ui.FieldNav,
					Target: "lsp.configure/" + server.id, Columns: []string{status},
					Description: "Change the command, arguments and settings for this language.",
				})
			}
			if len(fields) == 0 {
				fields = append(fields, codeToolText("tool-launch-empty", "Launch", "No language configuration uses this tool."))
			}
		case "files":
			fields = append(fields, codeToolText("tool-files", "Files", toolFiles(*tool.managed)))
		case "history":
			fields = c.historyFields(tool.managed)
		default:
			switch {
			case tool.managed != nil:
				fields = append(fields, codeToolText("tool-overview", "Tool details", toolDescription(*tool.managed)))
			case tool.external != nil:
				t := tool.external
				fields = append(fields, codeToolText("tool-overview", "Tool details",
					"Installed outside Crude. Crude cannot remove this tool.\nCommand: "+t.Command+"\nExecutable: "+t.Executable))
			default:
				status := "Crude has not checked whether this tool is installed."
				if state, inProject := tool.projectState(r.inventory.Project); inProject {
					switch state {
					case "Not installed":
						status = "Not installed for this project. Choose Set up tools to review your options."
						if _, ok := tool.setupTarget(r.inventory.Project); !ok {
							status = "Not installed for this project. Choose Back, then Set up tools to review your options."
						}
					case "Off":
						status = "This tool is off for this project. Open Launch to change its configuration."
					case "Ready":
						status = "The project resolved this tool, but its installation source is not known."
					default:
						status = "Project status: " + state + ". Open Launch to review its configuration."
					}
				}
				fields = append(fields, codeToolText("tool-overview", "Tool details",
					"Command: "+tool.command+"\n"+status))
			}
		}
	}
	// Settings can reproject the inventory on every frame. Reusing an
	// identical detail form preserves the reader's scroll and selection.
	if !reflect.DeepEqual(c.detailFields, fields) {
		candidate := formModel(fields...)
		c.detailFields = fields
		c.form.SetForm(candidate, "", "")
	}
	c.rebuildBar()
}

func (c *lspCatalog) openTool() ui.EventResult {
	row, ok := c.table.Selected()
	if !ok {
		return c.changed()
	}
	tool, ok := c.rows[row.ID]
	if !ok {
		return c.changed()
	}
	c.detail, c.history, c.detailPage = &tool, false, "overview"
	c.detailFields = nil
	c.model().codeTools.detail = ""
	if tool.managed != nil {
		c.model().codeTools.detail = tool.managed.ID
	}
	c.showDetail()
	c.form.FocusFirst()
	c.owner.focusID(c.tabs.ID())
	return c.changed()
}

func (c *lspCatalog) backTool() ui.EventResult {
	r := &c.model().codeTools
	if r.preview != nil && (c.detail != nil || c.history) {
		c.model().cancelCodeTools()
		c.showDetail()
	} else {
		c.model().cancelCodeTools()
		r.detail, r.notice = "", ""
		c.detail, c.history = nil, false
		c.detailFields = nil
		c.form.SetForm(nil, "", "")
		c.refreshRows()
		c.owner.focusID(c.table.ID())
	}
	return c.changed()
}

// begin opens the editor for one server.
func (c *lspCatalog) begin(id string) {
	server, ok := c.model().lspServer(id)
	if !ok {
		return
	}
	c.snapshot()
	c.editing, c.notice = id, ""
	c.draft = buildLSPServerForm(server)
	c.draft.OnChanged(func(string) { c.applyDraft() })
	c.form.SetForm(c.draft, "", "")
	c.form.FocusFirst()
	c.rebuildBar()
	c.owner.focusID(c.form.ID())
}

// applyDraft writes the editor into the settings draft after every edit,
// renaming a custom server only when its new identifier is valid and free.
func (c *lspCatalog) applyDraft() {
	m := c.model()
	server, ok := m.lspServer(c.editing)
	if !ok || c.draft == nil {
		return
	}
	id, cfg := lspServerFromForm(server, c.draft)
	if field := c.draft.Field(lspFieldID); field != nil && id != c.editing {
		field.Errors = nil
		_, taken := m.lspLanguages[id]
		switch {
		case !lspIDPattern.MatchString(id):
			field.Errors = []string{"Use a short lowercase name: letters, digits, - or _."}
			id = c.editing
		case taken || builtinLSP(id):
			field.Errors = []string{"Another server already uses " + id + "."}
			id = c.editing
		}
	}
	if id != c.editing {
		delete(m.lspLanguages, c.editing)
		c.editing = id
	}
	if m.lspLanguages == nil {
		m.lspLanguages = map[string]types.LSPLanguageSettings{}
	}
	m.lspLanguages[id] = cfg
	c.notice = ""
	if err := (types.LSPSettings{Languages: m.lspLanguages}).Validate(); err != nil {
		c.notice = err.Error()
	}
	m.syncLSPSummary()
	c.refreshRows()
	if m.onChange != nil {
		m.onChange()
	}
}

// builtinLSP reports a catalog language that has a language server.
func builtinLSP(id string) bool {
	c := catalog.Current()
	language, ok := c.Language(id)
	return ok && len(language.Servers) > 0
}

func (c *lspCatalog) closeEditor() {
	c.draft, c.editing, c.notice = nil, "", ""
	c.before, c.beforeResets, c.snapshotted = nil, nil, false
	c.detailFields = nil
	c.form.SetForm(nil, "", "")
	c.rebuildBar()
}

// back is the editor's Back and Esc: it restores the inventory from when
// the editor opened and returns to the list.
func (c *lspCatalog) back() ui.EventResult {
	if c.draft == nil {
		return ui.EventResult{}
	}
	m := c.model()
	id := c.editing
	if c.snapshotted {
		m.lspLanguages, m.resetPaths = c.before, c.beforeResets
		m.syncLSPSummary()
	}
	c.closeEditor()
	c.refreshRows()
	c.selectRow(id)
	c.rebuildBar()
	if c.detail != nil {
		c.showDetail()
		c.owner.focusID(c.form.ID())
	} else {
		c.owner.focusID(c.table.ID())
	}
	if m.onChange != nil {
		m.onChange()
	}
	return c.changed()
}

// save keeps the server and persists Settings; an incomplete server stays
// open with guidance instead of being saved half-defined.
func (c *lspCatalog) save() ui.EventResult {
	result := c.keep()
	if c.draft != nil {
		return result
	}
	result.Action = &ui.Action{ID: "settings.save"}
	return result
}

func (c *lspCatalog) selectRow(id string) {
	for index, row := range c.table.Rows() {
		if tool, ok := c.rows[row.ID]; ok {
			for _, server := range tool.servers {
				if server.id == id {
					c.table.SelectIndex(index)
					return
				}
			}
		}
	}
}

// keep closes the editor keeping its changes in the page (navigation away
// from a valid editor). A custom server that cannot run yet stays open.
func (c *lspCatalog) keep() ui.EventResult {
	if c.draft == nil {
		return ui.EventResult{}
	}
	c.draft.ValidateAll()
	if c.draft.AnyErrors() {
		c.notice = "Fill in the highlighted fields, or remove this server."
		return c.changed()
	}
	id := c.editing
	c.closeEditor()
	c.refreshRows()
	for index, row := range c.table.Rows() {
		if row.ID == id {
			c.table.SelectIndex(index)
		}
	}
	c.rebuildBar()
	if c.detail != nil {
		c.showDetail()
		c.owner.focusID(c.form.ID())
	} else {
		c.owner.focusID(c.table.ID())
	}
	return c.changed()
}

func (c *lspCatalog) changed() ui.EventResult {
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
}

func (c *lspCatalog) mutate(fn func(m *Model)) {
	m := c.model()
	if m.lspLanguages == nil {
		m.lspLanguages = map[string]types.LSPLanguageSettings{}
	}
	fn(m)
	m.syncLSPSummary()
	c.refreshRows()
	if m.onChange != nil {
		m.onChange()
	}
}

func (c *lspCatalog) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	m := c.model()
	if m == nil || m.Saving() {
		return ui.EventResult{Handled: true}
	}
	if strings.HasPrefix(string(action.ID), "lsp.configure/") {
		c.begin(strings.TrimPrefix(string(action.ID), "lsp.configure/"))
		return c.changed()
	}
	switch action.ID {
	case "tabs.activate", "tabs.enter":
		if c.detail != nil && c.draft == nil && m.codeTools.preview == nil {
			if id, ok := action.Payload.(string); ok && id == c.tabs.ActiveID() {
				c.detailPage = id
				c.showDetail()
				c.form.FocusFirst()
				if action.ID == "tabs.enter" || c.tabs.ActivatedByPointer() {
					c.owner.focusID(c.form.ID())
				}
			}
			return c.changed()
		}
	case "lsp.filter-project", "lsp.filter-installed", "lsp.filter-all":
		c.filterTouched = true
		c.project.SetChecked(action.ID == "lsp.filter-project")
		c.installed.SetChecked(action.ID == "lsp.filter-installed")
		c.all.SetChecked(action.ID == "lsp.filter-all")
		c.refreshRows()
		return c.changed()
	case "lsp.setup":
		message := OpenProjectToolsMsg{Target: m, Generation: m.codeTools.generation, Selection: types.ProjectToolSetupRequest{Explicit: true}}
		if c.detail != nil {
			message.ToolID = c.detail.id
			var ok bool
			message.Selection, ok = c.detail.setupTarget(m.codeTools.inventory.Project)
			if !ok {
				return c.changed()
			}
		}
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return message }}
	case "lsp.open":
		return c.openTool()
	case "lsp.tool.back":
		return c.backTool()
	case "lsp.history":
		c.detail, c.history = nil, true
		c.showDetail()
		c.form.FocusFirst()
		c.owner.focusID(c.form.ID())
		return c.changed()
	case "lsp.edit":
		if server, ok := c.selected(); ok {
			c.begin(server.id)
		}
		return c.changed()
	case "lsp.add":
		c.snapshot()
		id := "custom"
		for n := 2; m.lspLanguages[id].Command != "" || m.lspLanguages[id].Extensions != nil; n++ {
			id = fmt.Sprintf("custom-%d", n)
		}
		c.mutate(func(m *Model) { m.lspLanguages[id] = types.LSPLanguageSettings{Enabled: true} })
		c.begin(id)
		if c.draft != nil {
			c.form.FocusField(lspFieldID)
		}
		return c.changed()
	case "lsp.toggle":
		if server, ok := c.selected(); ok {
			// Store only the switch: the command keeps following the
			// catalog unless the user chose another one.
			c.mutate(func(m *Model) {
				cfg := m.lspLanguages[server.id]
				if !server.builtin {
					cfg = server.cfg
				}
				cfg.Enabled = !server.cfg.Enabled
				if m.lspLanguages == nil {
					m.lspLanguages = map[string]types.LSPLanguageSettings{}
				}
				m.lspLanguages[server.id] = cfg
			})
		}
		return c.changed()
	case "lsp.reset":
		id := c.editing
		if id == "" {
			if server, ok := c.selected(); ok {
				id = server.id
			}
		}
		if builtinLSP(id) {
			// The default entry is on and follows the catalog.
			c.mutate(func(m *Model) {
				m.lspLanguages[id] = types.DefaultLSPSettings().Languages[id]
				if m.Scope() == types.SettingsScopeWorkspace {
					m.lspLanguages[id] = m.appDefaults.LSP.Languages[id]
					m.resetWorkspacePath("lsp", "languages", id)
				}
			})
			if c.draft != nil {
				c.begin(id)
			}
		}
		return c.changed()
	case "lsp.remove":
		id := c.editing
		if id == "" {
			if server, ok := c.selected(); ok {
				id = server.id
			}
		}
		if id != "" && !builtinLSP(id) {
			c.closeEditor()
			c.detail, c.history = nil, false
			m.codeTools.detail = ""
			c.mutate(func(m *Model) { delete(m.lspLanguages, id) })
			c.owner.focusID(c.table.ID())
		}
		return c.changed()
	case "lsp.cancel":
		if c.draft != nil {
			return c.back()
		}
		if c.detail != nil || c.history || m.codeTools.preview != nil {
			return c.backTool()
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "settings.cancel"}}
	case "lsp.save":
		return c.save()
	case "lsp.bar.forward":
		if c.draft != nil || c.detail != nil || c.history || m.codeTools.preview != nil {
			c.owner.focusID(WorkspaceSettingsCloseBarID)
		} else if !c.more.Bounds().Empty() {
			c.more.FocusFirst()
			c.owner.focusID(c.more.ID())
		} else {
			c.owner.FocusActions()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "lsp.more.forward":
		c.owner.FocusActions()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "lsp.more.backward":
		c.bar.FocusLast()
		c.owner.focusID(c.bar.ID())
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "lsp.bar.backward":
		if c.draft != nil || c.detail != nil || c.history || m.codeTools.preview != nil {
			c.form.FocusLast()
			c.owner.focusID(c.form.ID())
		} else {
			c.owner.focusID(c.table.ID())
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.forward":
		c.owner.focusID(c.bar.ID())
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.backward", "form.cancel":
		if action.ID == "form.cancel" {
			if c.draft != nil {
				return c.back()
			}
			return c.backTool()
		}
		if c.detail != nil && c.draft == nil && m.codeTools.preview == nil {
			c.owner.focusID(c.tabs.ID())
		} else {
			c.owner.FocusCategories()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (c *lspCatalog) Children() []ui.Component {
	children := []ui.Component{c.filter, c.project, c.installed, c.all, c.table, c.tabs, c.form}
	if c.Bounds().Empty() {
		return children
	}
	children = append(children, c.bar, c.more)
	if (c.draft != nil || c.detail != nil || c.history || c.model().codeTools.preview != nil) && c.formScroll.Needed() {
		children = append(children, c.formScroll)
	} else if c.draft == nil && c.detail == nil && !c.history && c.model().codeTools.preview == nil && c.listScroll.Needed() {
		children = append(children, c.listScroll)
	}
	return children
}

func (c *lspCatalog) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (c *lspCatalog) Layout(ctx ui.LayoutContext, r ui.Rect) {
	c.SetBounds(r)
	if r.Empty() {
		for _, child := range []ui.Component{c.filter, c.project, c.installed, c.all, c.table, c.listScroll, c.tabs, c.form, c.formScroll, c.bar, c.more} {
			child.Layout(ctx, ui.Rect{})
		}
		return
	}
	w, h := r.W, r.H
	if c.draft != nil || c.detail != nil || c.history || c.model().codeTools.preview != nil {
		c.filter.Layout(ctx, ui.Rect{})
		c.project.Layout(ctx, ui.Rect{})
		c.installed.Layout(ctx, ui.Rect{})
		c.all.Layout(ctx, ui.Rect{})
		c.table.Layout(ctx, ui.Rect{})
		c.listScroll.Layout(ctx, ui.Rect{})
		c.more.Layout(ctx, ui.Rect{})
		tabH := 0
		if c.detail != nil && c.draft == nil && !c.history && c.model().codeTools.preview == nil {
			tabH = min(ui.StripHeight, h)
		}
		c.tabs.Layout(ctx, ui.Rect{W: w, H: tabH})
		formH := max(0, h-tabH)
		c.form.Layout(ctx, ui.Rect{Y: tabH, W: max(1, w-2), H: formH})
		c.formScroll.Layout(ctx, ui.Rect{X: w - ui.ScrollbarWidth, Y: tabH, W: ui.ScrollbarWidth, H: formH})
		if !c.formScroll.Needed() {
			c.formScroll.Layout(ctx, ui.Rect{})
		}
		return
	}
	primaryW := c.bar.Measure(ctx, ui.Loose(w, 1)).W
	moreW := c.more.Measure(ctx, ui.Loose(w, 1)).W
	actionRows := 1
	if primaryW+2+moreW <= w {
		c.bar.Layout(ctx, ui.Rect{Y: h - 1, W: primaryW, H: 1})
		c.more.Layout(ctx, ui.Rect{X: primaryW + 2, Y: h - 1, W: moreW, H: 1})
	} else {
		actionRows = min(2, h)
		c.more.Layout(ctx, ui.Rect{Y: h - actionRows, W: w, H: min(1, actionRows)})
		c.bar.Layout(ctx, ui.Rect{Y: h - 1, W: w, H: 1})
	}
	c.tabs.Layout(ctx, ui.Rect{})
	c.form.Layout(ctx, ui.Rect{})
	c.formScroll.Layout(ctx, ui.Rect{})
	c.filter.Layout(ctx, ui.Rect{W: w, H: 1})
	x := 0
	for _, chip := range []*lspToolFilter{c.project, c.installed, c.all} {
		chipW := min(max(0, w-x), chip.Measure(ctx, ui.Loose(w, 1)).W)
		chip.Layout(ctx, ui.Rect{X: x, Y: 1, W: chipW, H: 1})
		x += chipW + 1
	}
	compact := w < 60
	if c.compact != compact {
		c.compact = compact
		c.refreshRows()
	}
	columns := []ui.TableColumn{{ID: "tool", Label: "Tool", Track: ui.FractionGridTrack(4)}}
	if !compact {
		columns = append(columns, ui.TableColumn{ID: "languages", Label: "Languages", Track: ui.FractionGridTrack(3)})
	}
	columns = append(columns,
		ui.TableColumn{ID: "installed", Label: "Installed by", Track: ui.FixedGridTrack(min(13, max(1, w-15)))},
		ui.TableColumn{ID: "project", Label: "This project", Track: ui.FixedGridTrack(min(13, max(1, w-15)))})
	c.table.SetColumns(columns)
	gap := 2
	if compact {
		gap = 1
	}
	c.table.SetColumnGap(gap)
	tableH := max(1, h-actionRows-3-2)
	c.table.Layout(ctx, ui.Rect{Y: 3, W: max(1, w-2), H: tableH})
	c.listScroll.Layout(ctx, ui.Rect{X: w - ui.ScrollbarWidth, Y: 4, W: ui.ScrollbarWidth, H: max(1, tableH-1)})
	if !c.listScroll.Needed() {
		c.listScroll.Layout(ctx, ui.Rect{})
	}
}

func (c *lspCatalog) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := c.Bounds()
	if r.Empty() {
		return
	}
	tokens := c.owner.tokens
	bg := tokens.Color(ui.ColorPopupBg)
	if c.draft == nil && c.detail == nil && !c.history && len(c.table.Rows()) == 0 {
		message := "Clear the search, or add a server for another language."
		if c.installed.Checked() {
			message = "Crude found no matching installed tools. Clear Installed to see all tools."
		} else if c.project.Checked() {
			message = "No matching project tools. Choose All to see the catalog."
			if c.model().codeTools.inventory.Project.State != types.AssemblyReady {
				message = "Crude has not finished checking this project. Choose Refresh."
			}
		}
		c.owner.paintEmptyState(buf, ox+r.X, oy+r.Y+4, r.W, "No matching tools", message, bg)
	}
	statusY := r.H - 2
	if c.more.Bounds().Y < c.bar.Bounds().Y && !c.more.Bounds().Empty() {
		statusY--
	}
	if c.notice != "" && c.draft == nil {
		buf.WriteString(ox+r.X, oy+r.Y+statusY, rendering.TruncateVisual(c.notice, r.W, "…"), tokens.Color(ui.ColorError), bg, 0)
	} else if notice := c.model().codeTools.notice; notice != "" && c.draft == nil && c.detail == nil && !c.history {
		buf.WriteString(ox+r.X, oy+r.Y+statusY, rendering.TruncateVisual(notice, r.W, "…"), tokens.Color(ui.ColorMuted), bg, 0)
	} else if inventory := c.model().codeTools.inventory; len(inventory.Tools) > 0 && c.draft == nil && c.detail == nil && !c.history {
		text := "Crude tools use " + codeToolSize(inventory.TotalBytes) + "."
		buf.WriteString(ox+r.X, oy+r.Y+statusY, rendering.TruncateVisual(text, r.W, "…"), tokens.Color(ui.ColorMuted), bg, 0)
	}
}

func (c *lspCatalog) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc && c.draft != nil {
		return c.back()
	}
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc && (c.detail != nil || c.history || c.model().codeTools.preview != nil) {
		return c.backTool()
	}
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		c.owner.focusID(c.bar.ID())
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	}
	if evt.IsKey() && (evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeyRight) && c.draft == nil && c.detail == nil && !c.history {
		return c.openTool()
	}
	return ui.EventResult{}
}

func (c *lspCatalog) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Language Tools"}
}

func (c *lspCatalog) SetTokens(tokens *ui.TokenSet) {
	c.filter.SetTokens(tokens)
	c.project.SetTokens(tokens)
	c.installed.SetTokens(tokens)
	c.all.SetTokens(tokens)
	c.table.SetTokens(tokens)
	c.tabs.SetTokens(tokens)
	c.listScroll.SetTokens(tokens)
	c.form.SetTokens(tokens)
	c.formScroll.SetTokens(tokens)
	c.bar.SetTokens(tokens)
	c.more.SetTokens(tokens)
}
