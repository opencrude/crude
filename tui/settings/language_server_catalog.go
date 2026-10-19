package settings

import (
	"fmt"
	"regexp"
	"strings"

	tea "charm.land/bubbletea/v2"

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

// lspCatalog is the Language Servers page: a searchable server table that
// opens a focused per-server editor, the same list → detail rhythm as
// Accounts and Agents. Edits apply to the settings draft immediately and
// persist with the window's Save.
type lspCatalog struct {
	ui.Base
	owner      *WorkspaceSettingsSurface
	filter     *lspCatalogFilter
	table      *ui.Table
	listScroll *ui.ScrollbarControl
	form       *ui.Form
	formScroll *ui.ScrollbarControl
	bar        *ui.ActionBar
	draft      *ui.FormModel
	editing    string
	notice     string
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
			} else {
				f.owner.owner.focusID(f.owner.table.ID())
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

func newLSPCatalog(owner *WorkspaceSettingsSurface) *lspCatalog {
	t := owner.tokens
	c := &lspCatalog{Base: ui.NewBase(lspCatalogID), owner: owner}
	c.filter = &lspCatalogFilter{FilterField: ui.NewFilterField("settings/language-servers/search", t), owner: c}
	c.filter.SetPlaceholder("Search servers, commands and file types")
	c.filter.SetPrompt(ui.SearchGlyphEnd(t))
	c.filter.SetPromptPlacement(ui.PromptEnd)
	c.table = ui.NewTable("settings/language-servers/table", t)
	c.table.SetActivateAction("lsp.edit")
	c.table.SetShowHeader(true)
	c.table.SetColumnGap(2)
	c.table.SetColumns([]ui.TableColumn{
		{ID: "server", Label: "Server", Track: ui.FractionGridTrack(3)},
		{ID: "command", Label: "Command", Track: ui.FractionGridTrack(4)},
		{ID: "status", Label: "Status", Track: ui.FractionGridTrack(1)},
	})
	c.listScroll = ui.NewScrollbarControl("settings/language-servers/scroll", c.table.Scroll(), t)
	c.listScroll.SetSurfaceBackground(ui.ColorPopupBg)
	c.listScroll.SetPointerTargetSize(1)
	c.form = newSettingsForm(LanguageServerFormID, t)
	c.formScroll = ui.NewScrollbarControl("settings/language-servers/form-scroll", c.form.Scroll(), t)
	c.formScroll.SetSurfaceBackground(ui.ColorPopupBg)
	c.formScroll.SetPointerTargetSize(1)
	c.bar = ui.NewActionBar("settings/language-servers/actions", t)
	c.bar.SetAlignEnd(false)
	c.bar.SetExitActions("lsp.bar.forward", "lsp.bar.backward")
	c.bar.SetCancelAction("lsp.back")
	return c
}

func (c *lspCatalog) model() *Model { return c.owner.model }

// syncModel adopts a replaced settings model, dropping any editor that
// belonged to the old one.
func (c *lspCatalog) syncModel() {
	if c.draft != nil {
		if _, ok := c.model().lspServer(c.editing); !ok {
			c.closeEditor()
		}
	}
	c.refreshRows()
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
	for _, server := range m.lspServers() {
		command := strings.TrimSpace(server.cfg.Command + " " + joinShellWords(server.cfg.Args))
		extensions := strings.Join(server.def.Exts, " ")
		if !server.builtin {
			extensions = strings.Join(server.cfg.Extensions, " ")
		}
		if query != "" && !strings.Contains(strings.ToLower(server.name+" "+server.id+" "+command+" "+extensions), query) {
			continue
		}
		status, role := "On", ui.ColorFg
		if !server.cfg.Enabled {
			status, role = "Off", ui.ColorMuted
		}
		name := server.name
		if !server.builtin {
			name += "  custom"
		}
		rows = append(rows, ui.TableRow{ID: server.id, Cells: []ui.TableCell{
			{Text: name}, {Text: command, Role: ui.ColorMuted}, {Text: status, Role: role},
		}})
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
	return c.model().lspServer(row.ID)
}

func (c *lspCatalog) rebuildBar() {
	if c.draft != nil {
		server, _ := c.model().lspServer(c.editing)
		// Edits apply to the settings draft as they are made, so the editor
		// closes with Done rather than a separate apply step.
		buttons := []ui.ActionBarButton{{Label: "Restore default", Action: "lsp.reset", Enabled: true}}
		if !server.builtin {
			buttons = []ui.ActionBarButton{{Label: "Remove server", Action: "lsp.remove", Variant: ui.VariantDanger, Enabled: true}}
		}
		c.bar.SetButtons(append(buttons, ui.ActionBarButton{Label: "Done", Action: "lsp.back", Variant: ui.VariantPrimary, Enabled: true})...)
		return
	}
	server, ok := c.selected()
	toggle := "Disable"
	if ok && !server.cfg.Enabled {
		toggle = "Enable"
	}
	buttons := []ui.ActionBarButton{
		{Label: "Add server", Action: "lsp.add", Variant: ui.VariantPrimary, Enabled: true},
		{Label: "Edit", Action: "lsp.edit", Enabled: ok},
		{Label: toggle, Action: "lsp.toggle", Enabled: ok},
	}
	if ok && !server.builtin {
		buttons = append(buttons, ui.ActionBarButton{Label: "Remove", Action: "lsp.remove", Variant: ui.VariantDanger, Enabled: true})
	} else {
		buttons = append(buttons, ui.ActionBarButton{Label: "Restore default", Action: "lsp.reset", Enabled: ok})
	}
	c.bar.SetButtons(buttons...)
}

// begin opens the editor for one server.
func (c *lspCatalog) begin(id string) {
	server, ok := c.model().lspServer(id)
	if !ok {
		return
	}
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

func builtinLSP(id string) bool {
	for _, def := range types.DefaultLSPLanguages() {
		if def.ID == id {
			return true
		}
	}
	return false
}

func (c *lspCatalog) closeEditor() {
	c.draft, c.editing, c.notice = nil, "", ""
	c.form.SetForm(nil, "", "")
	c.rebuildBar()
}

// back leaves the editor. A custom server that cannot run yet stays open so
// its missing fields are fixed rather than silently saved half-defined.
func (c *lspCatalog) back() ui.EventResult {
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
	c.owner.focusID(c.table.ID())
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
	switch action.ID {
	case "lsp.edit":
		if server, ok := c.selected(); ok {
			c.begin(server.id)
		}
		return c.changed()
	case "lsp.add":
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
			c.mutate(func(m *Model) {
				cfg := server.cfg
				cfg.Enabled = !cfg.Enabled
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
			c.mutate(func(m *Model) { delete(m.lspLanguages, id) })
			c.owner.focusID(c.table.ID())
		}
		return c.changed()
	case "lsp.back":
		return c.back()
	case "lsp.bar.forward":
		if c.draft != nil {
			c.owner.focusID(WorkspaceSettingsCloseBarID)
		} else {
			c.owner.FocusActions()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "lsp.bar.backward":
		if c.draft != nil {
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
			return c.back()
		}
		c.owner.FocusCategories()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (c *lspCatalog) Children() []ui.Component {
	children := []ui.Component{c.filter, c.table, c.form}
	if c.Bounds().Empty() {
		return children
	}
	children = append(children, c.bar)
	if c.draft != nil && c.formScroll.Needed() {
		children = append(children, c.formScroll)
	} else if c.draft == nil && c.listScroll.Needed() {
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
		for _, child := range []ui.Component{c.filter, c.table, c.listScroll, c.form, c.formScroll, c.bar} {
			child.Layout(ctx, ui.Rect{})
		}
		return
	}
	w, h := r.W, r.H
	if c.draft != nil {
		c.filter.Layout(ctx, ui.Rect{})
		c.table.Layout(ctx, ui.Rect{})
		c.listScroll.Layout(ctx, ui.Rect{})
		// The decision bar, notices and field help live in the shared
		// settings footer; the form owns the whole body.
		formH := max(1, h)
		c.form.Layout(ctx, ui.Rect{W: max(1, w-2), H: formH})
		c.formScroll.Layout(ctx, ui.Rect{X: w - ui.ScrollbarWidth, W: ui.ScrollbarWidth, H: formH})
		if !c.formScroll.Needed() {
			c.formScroll.Layout(ctx, ui.Rect{})
		}
		return
	}
	c.bar.Layout(ctx, ui.Rect{Y: h - 1, W: w, H: 1})
	c.form.Layout(ctx, ui.Rect{})
	c.formScroll.Layout(ctx, ui.Rect{})
	c.filter.Layout(ctx, ui.Rect{W: w, H: 1})
	tableH := max(1, h-1-2-2)
	c.table.Layout(ctx, ui.Rect{Y: 2, W: max(1, w-2), H: tableH})
	c.listScroll.Layout(ctx, ui.Rect{X: w - ui.ScrollbarWidth, Y: 3, W: ui.ScrollbarWidth, H: max(1, tableH-1)})
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
	if c.draft == nil && len(c.table.Rows()) == 0 {
		c.owner.paintEmptyState(buf, ox+r.X, oy+r.Y+3, r.W, "No matching servers", "Clear the search, or add a server for another language.", bg)
	}
	if c.notice != "" && c.draft == nil {
		buf.WriteString(ox+r.X, oy+r.Y+r.H-2, rendering.TruncateVisual(c.notice, r.W, "…"), tokens.Color(ui.ColorError), bg, 0)
	}
}

func (c *lspCatalog) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() && evt.Key.Code == tea.KeyEsc && c.draft != nil {
		return c.back()
	}
	if evt.IsKey() && evt.Key.Code == tea.KeyTab {
		c.owner.focusID(c.bar.ID())
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus}
	}
	if evt.IsKey() && (evt.Key.Code == tea.KeyEnter || evt.Key.Code == tea.KeyRight) && c.draft == nil {
		if server, ok := c.selected(); ok {
			c.begin(server.id)
			return c.changed()
		}
	}
	return ui.EventResult{}
}

func (c *lspCatalog) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Language Servers"}
}

func (c *lspCatalog) SetTokens(tokens *ui.TokenSet) {
	c.filter.SetTokens(tokens)
	c.table.SetTokens(tokens)
	c.listScroll.SetTokens(tokens)
	c.form.SetTokens(tokens)
	c.formScroll.SetTokens(tokens)
	c.bar.SetTokens(tokens)
}
