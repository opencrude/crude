package settings

import (
	"context"
	"reflect"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/provider"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/types"
	"crude/tui/ui"
	ttypes "crude/types"
)

const (
	WorkspaceSettingsSurfaceID       ui.ComponentID = "workspace-settings"
	WorkspaceSettingsScopeID         ui.ComponentID = "workspace-settings/scope"
	WorkspaceSettingsSearchID        ui.ComponentID = "workspace-settings/search"
	WorkspaceSettingsListID          ui.ComponentID = "workspace-settings/categories"
	WorkspaceSettingsFormID          ui.ComponentID = "workspace-settings/form"
	WorkspaceSettingsActionID        ui.ComponentID = "workspace-settings/actions"
	WorkspaceSettingsResetBarID      ui.ComponentID = "workspace-settings/reset-bar"
	WorkspaceSettingsSaveID          ui.ComponentID = "workspace-settings/save"
	WorkspaceSettingsCancelID        ui.ComponentID = "workspace-settings/cancel"
	WorkspaceSettingsResetID         ui.ComponentID = "workspace-settings/reset"
	WorkspaceSettingsListScrollID    ui.ComponentID = "workspace-settings/list-scroll"
	WorkspaceSettingsFormScrollID    ui.ComponentID = "workspace-settings/form-scroll"
	WorkspaceSettingsDividerID       ui.ComponentID = "workspace-settings/divider"
	WorkspaceSettingsAccountsID      ui.ComponentID = "workspace-settings/accounts"
	WorkspaceSettingsAddID           ui.ComponentID = "workspace-settings/account-add"
	WorkspaceSettingsAccountBarID    ui.ComponentID = "workspace-settings/account-actions"
	WorkspaceSettingsAccountScrollID ui.ComponentID = "workspace-settings/account-scroll"
	WorkspaceSettingsEditorBarID     ui.ComponentID = "workspace-settings/account-editor-actions"
	WorkspaceSettingsAuthBarID       ui.ComponentID = "workspace-settings/account-auth-actions"
	WorkspaceSettingsAccountMoreID   ui.ComponentID = "workspace-settings/account-more-actions"
	WorkspaceSettingsAccountFilterID ui.ComponentID = "workspace-settings/account-filter"
	workspaceSettingsInset                          = 2
	workspaceSettingsGap                            = 3
)

// WorkspaceSettingsSurface composes native category navigation, the shared
// FormModel/Form controls, and the shared ActionBar without a legacy bridge.
type WorkspaceSettingsSurface struct {
	ui.Base
	model            *Model
	tokens           *ui.TokenSet
	scope            *settingsScopeStrip
	search           *settingsSearchField
	categories       *settingsCategoryList
	accounts         *settingsAccountTable
	accountFilter    *settingsTableFilter
	accountAdd       *ui.ActionBar
	accountBar       *ui.ActionBar
	accountMoreBar   *ui.ActionBar
	accountEditorBar *ui.ActionBar
	accountAuthBar   *ui.ActionBar
	accountScroll    *ui.ScrollbarControl
	form             *ui.Form
	agents           *agentCatalog
	capCatalog       *capabilityCatalog
	reset            *ui.ActionBar
	actions          *ui.ActionBar
	listScroll       *ui.ScrollbarControl
	formScroll       *ui.ScrollbarControl
	split            *ui.SplitModel
	divider          *ui.SplitDivider
	contentRect      ui.Rect
	focus            func(ui.ComponentID)
}

func NewSurface(th *theme.Theme, model *Model) *WorkspaceSettingsSurface {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &WorkspaceSettingsSurface{Base: ui.NewBase(WorkspaceSettingsSurfaceID), model: model, tokens: tokens}
	s.scope = newSettingsScopeStrip(s, tokens)
	s.search = &settingsSearchField{Base: ui.NewBase(WorkspaceSettingsSearchID), field: ui.NewFilterField(WorkspaceSettingsSearchID, tokens), owner: s}
	s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.search.field.SetPromptPlacement(ui.PromptEnd)
	s.search.field.SetPlaceholder("Search settings")
	s.search.field.SetSubmitAction("")
	s.search.field.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
	s.search.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	s.categories = &settingsCategoryList{Base: ui.NewBase(WorkspaceSettingsListID), list: ui.NewList(WorkspaceSettingsListID, tokens), owner: s}
	s.categories.list.SetActivateAction("settings.category.open")
	s.categories.list.SetSelectionMarker(false)
	s.accounts = &settingsAccountTable{Base: ui.NewBase(WorkspaceSettingsAccountsID), table: ui.NewTable(WorkspaceSettingsAccountsID, tokens), owner: s}
	s.accounts.table.SetActivateAction("settings.account.edit")
	s.accounts.table.SetShowHeader(true)
	s.accounts.table.SetColumnGap(2)
	s.accountFilter = &settingsTableFilter{Base: ui.NewBase(WorkspaceSettingsAccountFilterID), field: ui.NewFilterField(WorkspaceSettingsAccountFilterID, tokens), owner: s}
	s.accountFilter.field.SetPlaceholder("Search accounts")
	s.accountFilter.field.SetPrompt(ui.SearchGlyphEnd(tokens))
	s.accountFilter.field.SetPromptPlacement(ui.PromptEnd)
	s.accountScroll = ui.NewScrollbarControl(WorkspaceSettingsAccountScrollID, s.accounts.table.Scroll(), tokens)
	s.accountScroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.accountScroll.SetPointerTargetSize(1)
	s.form = ui.NewForm(WorkspaceSettingsFormID, nil, "", "", tokens)
	s.form.SetFieldGap(1)
	s.form.SetArrowExitsForm(true)
	s.agents = newAgentCatalog(s)
	s.capCatalog = newCapabilityCatalog(s)
	s.listScroll = ui.NewScrollbarControl(WorkspaceSettingsListScrollID, s.categories.list.Scroll(), tokens)
	s.listScroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.listScroll.SetPointerTargetSize(1)
	s.formScroll = ui.NewScrollbarControl(WorkspaceSettingsFormScrollID, s.form.Scroll(), tokens)
	s.formScroll.SetSurfaceBackground(ui.ColorPopupBg)
	s.formScroll.SetPointerTargetSize(1)
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.split.SetDividerSize(workspaceSettingsGap)
	s.split.SetMinSizes(12, 20)
	s.divider = ui.NewSplitDivider(WorkspaceSettingsDividerID, s.split)
	s.divider.SetTokens(tokens)
	s.divider.SetColorRole(ui.ColorPopupDivider)
	// The rail/content rule must carry the popup fill explicitly: the settings
	// body is a floating window, and a transparent (inherit) rule reads as a
	// background-less strip inside it.
	s.divider.SetSurfaceBackground(ui.ColorPopupBg)
	s.accountAdd = ui.NewActionBar(WorkspaceSettingsAddID, tokens,
		ui.ActionBarButton{Label: "Add account", Action: "settings.account.begin-add", Variant: ui.VariantPrimary, Enabled: true})
	s.accountAdd.SetAlignEnd(false)
	s.accountAdd.SetExitActions("accounts.add.forward", "accounts.add.backward")
	s.accountBar = ui.NewActionBar(WorkspaceSettingsAccountBarID, tokens,
		ui.ActionBarButton{Label: "Edit", Action: "settings.account.edit", Enabled: false},
		ui.ActionBarButton{Label: "Clone", Action: "settings.account.clone", Enabled: false})
	s.accountBar.SetAlignEnd(false)
	s.accountBar.SetExitActions("accounts.actions.forward", "accounts.actions.backward")
	s.accountMoreBar = ui.NewActionBar(WorkspaceSettingsAccountMoreID, tokens,
		ui.ActionBarButton{Label: "Remove", Action: "settings.account.remove", Variant: ui.VariantDanger, Enabled: false})
	s.accountMoreBar.SetAlignEnd(false)
	s.accountMoreBar.SetExitActions("accounts.more.forward", "accounts.more.backward")
	s.accountEditorBar = ui.NewActionBar(WorkspaceSettingsEditorBarID, tokens,
		ui.ActionBarButton{Label: "Add account", Action: "settings.account.commit", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{Label: "Back", Action: "settings.account.cancel-edit", Enabled: true})
	s.accountEditorBar.SetExitActions("account.editor.forward", "account.editor.backward")
	s.accountEditorBar.SetCancelAction("settings.cancel")
	s.accountAuthBar = ui.NewActionBar(WorkspaceSettingsAuthBarID, tokens,
		ui.ActionBarButton{Label: "Sign in", Action: "settings.chatgpt.signin", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{Label: "Disconnect", Action: "settings.chatgpt.signout", Enabled: false})
	s.accountAuthBar.SetAlignEnd(false)
	s.rebuildActions()
	s.syncModel()
	return s
}

func (s *WorkspaceSettingsSurface) rebuildActions() {
	s.actions = ui.NewActionBar(WorkspaceSettingsActionID, s.tokens,
		ui.ActionBarButton{ID: WorkspaceSettingsSaveID, Label: "Save", Action: "settings.save", Variant: ui.VariantPrimary, Enabled: true},
		ui.ActionBarButton{ID: WorkspaceSettingsCancelID, Label: "Cancel", Action: "settings.close", Variant: ui.VariantSecondary, Enabled: true},
	)
	s.actions.SetCancelAction("settings.cancel")
	s.reset = nil
	if s.model != nil && s.model.Scope() == types.SettingsScopeWorkspace {
		s.reset = ui.NewActionBar(WorkspaceSettingsResetBarID, s.tokens,
			ui.ActionBarButton{ID: WorkspaceSettingsResetID, Label: "Reset section", Action: "settings.reset", Variant: ui.VariantDanger, Enabled: true},
		)
		s.reset.SetAlignEnd(false)
		s.reset.SetExitActions("resetbar.exit.forward", "resetbar.exit.backward")
	}
	s.syncAccountControls()
	// Replacing the bars drops button bounds. Re-layout into the current
	// surface rect so the next paint still shows Save/Cancel without waiting
	// for a host size-change (SetModel used to hit this on overlay ticks).
	if !s.Bounds().Empty() {
		s.Layout(ui.LayoutContext{}, s.Bounds())
	}
}

func (s *WorkspaceSettingsSurface) accountsOpen() bool {
	return s.model != nil && s.model.Scope() == types.SettingsScopeApplication && s.model.SelectedCategory() == catAccounts
}

func (s *WorkspaceSettingsSurface) agentsOpen() bool {
	return s.model != nil && s.model.SelectedCategory() == catAgents
}

func (s *WorkspaceSettingsSurface) capabilitiesOpen() bool {
	return s.model != nil && (s.model.SelectedCategory() == catSkills || s.model.SelectedCategory() == catMCP || s.model.SelectedCategory() == catSources)
}

func (s *WorkspaceSettingsSurface) showReset() bool {
	return s.reset != nil && s.model != nil && !s.model.isNetworkCategory(s.model.SelectedCategory())
}

func (s *WorkspaceSettingsSurface) syncAccountControls() {
	if s.model == nil {
		return
	}
	busy := s.model.accountBusy || s.model.Saving()
	_, ok := s.model.accounts[s.model.accountID]
	for _, action := range []string{"settings.account.edit", "settings.account.clone"} {
		s.accountBar.SetEnabled(action, ok && !busy)
	}
	s.accountMoreBar.SetEnabled("settings.account.remove", ok && !busy)
	s.accountAdd.SetEnabled("settings.account.begin-add", !busy)
	if s.model.accountEditor != nil {
		label := "Save changes"
		if s.model.accountEditor.isNew {
			label = "Add account"
		}
		s.accountEditorBar.SetLabel("settings.account.commit", label)
		a := s.model.accountEditor.draft
		if a.Connection.AuthType == provider.AuthChatGPT {
			signIn := "Sign in"
			if a.OAuth != nil && a.OAuth.AccessToken != "" {
				signIn = "Reconnect"
			}
			s.accountAuthBar.SetLabel("settings.chatgpt.signin", signIn)
			s.accountAuthBar.SetEnabled("settings.chatgpt.signout", a.OAuth != nil && !busy)
		}
	}
	s.accountAuthBar.SetEnabled("settings.chatgpt.signin", !busy && (s.model.accountEditor == nil || s.model.authWaitingID != s.model.accountEditor.draft.ID))
	s.accountEditorBar.SetEnabled("settings.account.commit", !busy)
	s.accountEditorBar.SetEnabled("settings.account.cancel-edit", !busy)
	s.actions.SetEnabled("settings.save", !busy)
	s.actions.SetEnabled("settings.close", !busy)
	if s.reset != nil {
		s.reset.SetEnabled("settings.reset", !busy)
	}
}

func (s *WorkspaceSettingsSurface) Children() []ui.Component {
	// The form keeps its Host identity even while a table owns the content
	// area. This lets a category switch focus its first input before the next
	// frame's layout, without painting or hit-testing an empty form over rows.
	children := []ui.Component{s.scope, s.search, s.categories, s.form, s.agents, s.capCatalog}
	switch {
	case s.accountsOpen() && s.model.accountEditor != nil:
		children = append(children, s.accountEditorBar)
		if s.model.accountEditor.draft.Connection.AuthType == provider.AuthChatGPT {
			children = append(children, s.accountAuthBar)
		}
	case s.accountsOpen():
		children = append(children, s.accounts, s.accountFilter, s.accountAdd, s.accountBar, s.accountMoreBar)
		if s.accountScroll.Needed() {
			children = append(children, s.accountScroll)
		}
	default:
	}
	if s.showReset() {
		children = append(children, s.reset)
	}
	if (!s.accountsOpen() || s.model.accountEditor == nil) && s.agents.draft == nil {
		children = append(children, s.actions)
	}
	if s.listScroll != nil && s.listScroll.Needed() {
		children = append(children, s.listScroll)
	}
	if s.formScroll != nil && s.formScroll.Needed() && !s.agentsOpen() && (!s.accountsOpen() || s.model.accountEditor != nil) {
		children = append(children, s.formScroll)
	}
	if s.divider != nil {
		children = append(children, s.divider)
	}
	return children
}
func (s *WorkspaceSettingsSurface) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if s.agents != nil {
		s.agents.SetTokens(tokens)
	}
	if s.capCatalog != nil {
		s.capCatalog.SetTokens(tokens)
	}
	if s.scope != nil {
		s.scope.SetTokens(tokens)
	}
	if s.search != nil {
		s.search.SetTokens(tokens)
		s.search.field.SetBackgroundColor(tokens.Color(ui.ColorPopupSubtleBg))
		s.search.field.SetPromptColor(tokens.Color(ui.ColorAccent))
	}
	if s.categories != nil {
		s.categories.SetTokens(tokens)
	}
	if s.accounts != nil {
		s.accounts.SetTokens(tokens)
	}
	if s.accountFilter != nil {
		s.accountFilter.SetTokens(tokens)
	}
	if s.accountScroll != nil {
		s.accountScroll.SetTokens(tokens)
	}
	if s.accountAdd != nil {
		s.accountAdd.SetTokens(tokens)
	}
	if s.accountBar != nil {
		s.accountBar.SetTokens(tokens)
	}
	if s.accountMoreBar != nil {
		s.accountMoreBar.SetTokens(tokens)
	}
	if s.accountEditorBar != nil {
		s.accountEditorBar.SetTokens(tokens)
	}
	if s.accountAuthBar != nil {
		s.accountAuthBar.SetTokens(tokens)
	}
	if s.form != nil {
		s.form.SetTokens(tokens)
	}
	if s.reset != nil {
		s.reset.SetTokens(tokens)
	}
	if s.actions != nil {
		s.actions.SetTokens(tokens)
	}
	if s.listScroll != nil {
		s.listScroll.SetTokens(tokens)
	}
	if s.formScroll != nil {
		s.formScroll.SetTokens(tokens)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
		s.divider.SetColorRole(ui.ColorPopupDivider)
	}
	if s.search != nil {
		s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.search.field.SetPromptPlacement(ui.PromptEnd)
	}
}
func (s *WorkspaceSettingsSurface) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *WorkspaceSettingsSurface) FocusScope()                             { s.focusID(WorkspaceSettingsScopeID) }
func (s *WorkspaceSettingsSurface) FocusSearch()                            { s.focusID(WorkspaceSettingsSearchID) }
func (s *WorkspaceSettingsSurface) FocusCategories()                        { s.focusID(WorkspaceSettingsListID) }
func (s *WorkspaceSettingsSurface) FocusForm() {
	if s.capabilitiesOpen() {
		s.capCatalog.focusContent()
		return
	}
	if s.agentsOpen() {
		if s.agents.draft != nil {
			s.focusID(s.agents.form.ID())
		} else {
			s.focusID(s.agents.filter.ID())
		}
		return
	}
	s.focusID(WorkspaceSettingsFormID)
}
func (s *WorkspaceSettingsSurface) FocusAccountList()   { s.focusID(WorkspaceSettingsAccountsID) }
func (s *WorkspaceSettingsSurface) FocusAccountFilter() { s.focusID(WorkspaceSettingsAccountFilterID) }
func (s *WorkspaceSettingsSurface) FocusAccountAdd()    { s.focusID(WorkspaceSettingsAddID) }

func (s *WorkspaceSettingsSurface) ShowAccounts() {
	s.model.SelectAccounts()
	s.syncModel()
	if len(s.model.accounts) == 0 {
		s.FocusAccountAdd()
	} else {
		s.FocusAccountFilter()
	}
}

func (s *WorkspaceSettingsSurface) ShowAccountSetup() {
	s.ShowAccounts()
	_ = s.openAccountEditor(true, "")
}

func (s *WorkspaceSettingsSurface) ShowMainAgent() {
	s.model.SetScope(types.SettingsScopeApplication)
	s.model.SelectCategory(catAgents)
	s.syncModel()
	s.agents.begin("role/default", false)
}

// ShowNetworkConnection reconciles the retained form and category list together;
// selecting the model alone leaves the old surface mounted until another input.
func (s *WorkspaceSettingsSurface) ShowNetworkConnection() {
	s.model.SelectCategory(catNetworkConnection)
	s.syncModel()
	s.FocusForm()
}
func (s *WorkspaceSettingsSurface) FocusReset() {
	if s.showReset() {
		s.focusID(WorkspaceSettingsResetBarID)
		return
	}
	s.FocusActions()
}
func (s *WorkspaceSettingsSurface) FocusActions() { s.focusID(WorkspaceSettingsActionID) }
func (s *WorkspaceSettingsSurface) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *WorkspaceSettingsSurface) SetModel(model *Model) {
	s.model = model
	s.search.owner, s.categories.owner = s, s
	s.syncModel()
}

func (s *WorkspaceSettingsSurface) Model() *Model { return s.model }

// RailScrollForTest exposes the category rail's viewport so tests can assert
// the reader's scroll position across pointer resyncs.
func (s *WorkspaceSettingsSurface) RailScrollForTest() *ui.ScrollModel {
	return s.categories.list.Scroll()
}

// FinishSave applies an asynchronous persistence result only to the exact
// retained model that emitted the request. Failures keep the draft mounted and
// editable for retry.
func (s *WorkspaceSettingsSurface) FinishSave(target *Model, err error) bool {
	if s == nil || s.model == nil || target == nil || s.model != target || !target.Saving() {
		return false
	}
	s.model.FinishSave(err)
	s.syncModel()
	if !s.Bounds().Empty() {
		s.Layout(ui.LayoutContext{}, s.Bounds())
	}
	return true
}

// WindowTitle is the frame title of the floating settings window. It names the
// window's scope and nothing else: navigation inside the window is reported by
// the right pane's own title (see PaneTitle), never by mutating the window
// frame the user has already positioned.
func (s *WorkspaceSettingsSurface) WindowTitle() string {
	if s.model != nil && s.model.Scope() == types.SettingsScopeWorkspace {
		return "Workspace Settings"
	}
	return "Application Settings"
}

// PaneTitle is the right pane's heading. At list level it names the page and
// counts its rows; once an editor opens it carries the page hierarchy
// ("Accounts ▸ Add account") so the user always knows where they are.
func (s *WorkspaceSettingsSurface) PaneTitle() string {
	return strings.Join(s.paneTitleParts(), s.breadcrumbSeparator())
}

func (s *WorkspaceSettingsSurface) breadcrumbSeparator() string {
	sep := " / "
	if s.tokens != nil {
		sep = " " + s.tokens.Glyphs().BreadcrumbSep + " "
	}
	return sep
}

func (s *WorkspaceSettingsSurface) paneTitleParts() []string {
	if s.model == nil {
		return nil
	}
	if s.capabilitiesOpen() {
		parts := []string{s.capCatalog.title()}
		if s.capCatalog.id != "" && (s.capCatalog.detail || s.capCatalog.edit != "") {
			parts = append(parts, mapDefault(s.capCatalog.entry().Name, s.capCatalog.id))
		}
		return parts
	}
	if s.agentsOpen() {
		if s.agents.draft == nil {
			return []string{"Agents"}
		}
		label := s.agents.id
		if label == "" {
			label = "New agent"
		}
		for _, role := range agentRoles {
			if s.agents.roleIndex == role.index {
				label = role.label
			}
		}
		return []string{"Agents", label}
	}
	if s.model.accountEditor != nil {
		action := "Edit account"
		if s.model.accountEditor.isNew {
			action = "Add account"
		}
		return []string{"Accounts", action}
	}
	if s.accountsOpen() {
		if len(s.model.accounts) > 0 {
			return []string{"Accounts  " + strconv.Itoa(len(s.model.accounts))}
		}
		return []string{"Accounts"}
	}
	return nil
}

// RequestClose closes the entire window, never a nested settings page. Back,
// breadcrumbs and Esc retain their separate navigation/discard policy.
func (s *WorkspaceSettingsSurface) RequestClose() ui.EventResult {
	if s.model == nil || s.model.accountBusy || s.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	// A close request supersedes any pending page or scope navigation.
	s.model.nextCategory, s.model.nextScope = -1, ""
	s.capCatalog.nextOpen = nil
	if s.hasUnsavedDrafts() {
		return s.requestPrompt(PromptDiscardSettings, "", "", 0)
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CancelMsg{} }}
}

func (s *WorkspaceSettingsSurface) hasUnsavedDrafts() bool {
	return s.model.hasUnsavedPreferences() || s.model.accountEditorDirty() || s.agents.dirty() || s.capCatalog.dirty()
}

func (s *WorkspaceSettingsSurface) syncModel() {
	if s.model == nil {
		return
	}
	s.agents.syncModel()
	s.search.field.SetValue(s.model.searchText())
	s.categories.syncItems()
	s.accounts.syncRows()
	s.syncAccountControls()
	if s.scope != nil {
		s.scope.sync()
	}
	s.refreshForm()
}
func (s *WorkspaceSettingsSurface) refreshForm() {
	if s.model == nil || s.model.selected < 0 || s.model.selected >= len(s.model.categories) {
		s.form.SetForm(nil, "", "")
		return
	}
	category := s.model.categories[s.model.selected]
	if s.agentsOpen() || s.capabilitiesOpen() {
		s.form.SetForm(nil, "", "")
	} else {
		s.form.SetForm(category.Form, "", "")
	}
	if s.accountsOpen() {
		s.accounts.syncRows()
		s.syncAccountControls()
		s.accountFilter.field.SetValue(s.model.accountQuery)
	}
}

func (s *WorkspaceSettingsSurface) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (s *WorkspaceSettingsSurface) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	innerW := max(1, r.W-workspaceSettingsInset*2)
	leftW, gap := s.railGeometry(innerW)
	railX := workspaceSettingsInset
	formX, formW := railX+leftW+gap, max(1, r.W-(railX+leftW+gap)-workspaceSettingsInset)
	// A narrow editor cannot afford one clipped description row per field.
	// Keep full-size textareas and native controls; suppress painted help only.
	s.form.SetHideDescriptions(formW < 55)
	if formW < 55 {
		s.form.SetFieldGap(0)
	} else {
		s.form.SetFieldGap(1)
	}
	if s.accountsOpen() {
		if formW < 73 {
			s.actions.SetLabel("settings.save", "Save")
			s.actions.SetLabel("settings.close", "Close")
		} else {
			s.actions.SetLabel("settings.save", "Save")
			s.actions.SetLabel("settings.close", "Close")
		}
	} else {
		s.actions.SetLabel("settings.save", "Save")
		s.actions.SetLabel("settings.close", "Cancel")
	}
	footerH := 2
	topPad := s.topPad()
	searchY := topPad
	catY, catH := topPad+2, max(1, r.H-footerH-(topPad+2))
	formH := max(1, r.H-footerH-topPad)
	s.contentRect = ui.Rect{X: formX, Y: topPad, W: formW, H: formH}
	footerY := max(0, r.H-1)
	s.search.Layout(ctx, ui.Rect{X: railX, Y: searchY, W: leftW, H: 1})
	s.categories.Layout(ctx, ui.Rect{X: railX, Y: catY, W: leftW, H: catH})
	formRect := ui.Rect{X: formX, Y: topPad, W: formW, H: formH}
	if s.accountsOpen() {
		s.accountEditorBar.Layout(ctx, ui.Rect{X: formX, Y: footerY, W: formW, H: 1})
		if s.model.accountEditor != nil {
			formRect = ui.Rect{X: formX, Y: topPad + 2, W: formW, H: max(1, formH-3)}
			if s.model.accountEditor.draft.Connection.AuthType == provider.AuthChatGPT {
				s.accountAuthBar.Layout(ctx, ui.Rect{X: formX, Y: min(topPad+formH-2, topPad+4+s.form.PreferredHeight(formH-4)), W: formW, H: 1})
			}
		} else {
			s.accountFilter.Layout(ctx, ui.Rect{X: formX, Y: topPad + 1, W: formW, H: 1})
			actionY := s.layoutCatalogActions(ctx, formX, formW, topPad+formH-1, s.accountAdd, s.accountBar, s.accountMoreBar)
			// Keep one row between the table and actions for errors/progress.
			tableH := max(1, actionY-(topPad+3)-2)
			s.accounts.Layout(ctx, ui.Rect{X: formX, Y: topPad + 3, W: formW, H: tableH})
			if s.accountScroll.NeededForViewport(max(1, tableH-1)) {
				list := s.accounts.Bounds()
				barW := min(ui.ScrollbarWidth+1, list.W)
				stroke := min(ui.ScrollbarWidth, barW)
				s.accounts.Layout(ctx, ui.Rect{X: list.X, Y: list.Y, W: max(1, list.W-barW), H: list.H})
				// The gutter column stays between the table and the bar: the
				// scrollbar registers only its painted stroke, so a click in
				// the blank column can never resolve to the scrollbar.
				s.accountScroll.Layout(ctx, ui.Rect{X: list.X + list.W - stroke, Y: list.Y, W: stroke, H: list.H})
			} else {
				s.accountScroll.Layout(ctx, ui.Rect{})
			}
		}
	}
	// One authoritative viewport height per layout. Laying out the full form
	// first would clamp away a scrollbar drag before the shorter tab body ran.
	if s.agentsOpen() {
		s.agents.Layout(ctx, ui.Rect{X: formX, Y: topPad, W: formW, H: formH})
	} else {
		s.agents.Layout(ctx, ui.Rect{})
	}
	if s.capabilitiesOpen() {
		switch s.model.selected {
		case catSkills:
			s.capCatalog.switchKind("skill")
		case catMCP:
			s.capCatalog.switchKind("mcp")
		case catSources:
			s.capCatalog.switchKind("source")
		}
		s.capCatalog.Layout(ctx, ui.Rect{X: formX, Y: topPad, W: formW, H: formH})
	} else {
		s.capCatalog.Layout(ctx, ui.Rect{})
	}
	if s.agentsOpen() || s.capabilitiesOpen() || s.accountsOpen() && s.model.accountEditor == nil {
		s.form.Layout(ctx, ui.Rect{})
	} else {
		s.form.Layout(ctx, formRect)
	}
	if s.listScroll != nil && s.listScroll.Needed() {
		barW := min(ui.ScrollbarWidth+1, leftW)
		stroke := min(ui.ScrollbarWidth, barW)
		s.categories.Layout(ctx, ui.Rect{X: railX, Y: catY, W: max(1, leftW-barW), H: catH})
		s.listScroll.Layout(ctx, ui.Rect{X: railX + leftW - stroke, Y: catY, W: stroke, H: catH})
	} else if s.listScroll != nil {
		s.listScroll.Layout(ctx, ui.Rect{})
	}
	if !s.agentsOpen() && s.formScroll != nil && (!s.accountsOpen() || s.model.accountEditor != nil) && s.formScroll.Needed() {
		barW := min(ui.ScrollbarWidth+1, formRect.W)
		stroke := min(ui.ScrollbarWidth, barW)
		s.form.Layout(ctx, ui.Rect{X: formRect.X, Y: formRect.Y, W: max(1, formRect.W-barW), H: formRect.H})
		s.formScroll.Layout(ctx, ui.Rect{X: formRect.X + formRect.W - stroke, Y: formRect.Y, W: stroke, H: formRect.H})
	} else if s.formScroll != nil {
		s.formScroll.Layout(ctx, ui.Rect{})
	}
	if s.scope != nil {
		s.scope.Layout(ctx, ui.Rect{X: railX, Y: footerY, W: leftW, H: 1})
	}
	resetW := 0
	if s.showReset() {
		resetW = s.reset.Measure(ctx, ui.Loose(formW, 1)).W
		s.reset.Layout(ctx, ui.Rect{X: formX, Y: footerY, W: max(1, resetW), H: 1})
	} else if s.reset != nil {
		s.reset.Layout(ctx, ui.Rect{})
	}
	actionsX, actionsW := formX, formW
	if resetW > 0 {
		gap := 2
		if s.tokens != nil {
			gap = s.tokens.ButtonGap()
		}
		actionsX = formX + resetW + gap
		actionsW = max(1, formW-resetW-gap)
	}
	s.actions.Layout(ctx, ui.Rect{X: actionsX, Y: footerY, W: actionsW, H: 1})
	if s.divider != nil {
		s.divider.SetCurrentFirst(leftW)
		s.divider.Layout(ctx, ui.Rect{X: railX + leftW, Y: 0, W: gap, H: max(0, r.H-2)})
	}
}

// layoutCatalogActions keeps every catalog's first button left-aligned and
// anchors its last action row just above the shared settings footer.
func (s *WorkspaceSettingsSurface) layoutCatalogActions(ctx ui.LayoutContext, x, width, bottom int, groups ...*ui.ActionBar) int {
	gap := s.tokens.ButtonGap()
	widths := make([]int, len(groups))
	rows := make([]int, len(groups))
	row, used := 0, 0
	for i, group := range groups {
		w := group.Measure(ctx, ui.Loose(1024, 1)).W
		if used > 0 && used+gap+w > width {
			row++
			used = 0
		}
		rows[i], widths[i] = row, min(w, width)
		if used > 0 {
			used += gap
		}
		used += widths[i]
	}
	first := bottom - row
	used = 0
	previous := -1
	for i, group := range groups {
		if rows[i] != previous {
			used = 0
		} else {
			used += gap
		}
		group.Layout(ctx, ui.Rect{X: x + used, Y: first + rows[i], W: widths[i], H: 1})
		used += widths[i]
		previous = rows[i]
	}
	return first
}

func (s *WorkspaceSettingsSurface) topPad() int {
	pad := 1
	if s.tokens != nil {
		pad = s.tokens.FormTitlePad()
	}
	if pad < 1 {
		return 1
	}
	return pad
}

func (s *WorkspaceSettingsSurface) railGeometry(innerW int) (leftW, gap int) {
	gap = workspaceSettingsGap
	if s.split != nil {
		s.split.SetDividerSize(gap)
		s.split.SetMinSizes(12, 20)
		maxFirst := innerW * 2 / 3
		if maxFirst < 12 {
			maxFirst = 12
		}
		s.split.SetMaxFirst(maxFirst)
	}
	if s.split != nil && s.split.HasFirstSize() {
		leftW, gap, _ = s.split.Resolve(innerW)
		return leftW, gap
	}
	return s.leftWidth(innerW), gap
}

func (s *WorkspaceSettingsSurface) leftWidth(width int) int {
	leftW := 24
	if s.model != nil {
		for _, category := range s.model.categories {
			leftW = max(leftW, rendering.VisibleWidth(category.Name)+5)
		}
	}
	if maxLeft := width / 3; maxLeft > 0 && leftW > maxLeft {
		leftW = maxLeft
	}
	return max(12, leftW)
}

func (s *WorkspaceSettingsSurface) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	r := s.Bounds()
	if r.Empty() {
		return
	}
	bg, divider := cell.NoColor, cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorPopupBg)
		divider = s.tokens.Color(ui.ColorPopupDivider)
	}
	ui.FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	ox, oy := originX+r.X, originY+r.Y
	innerW := max(1, r.W-workspaceSettingsInset*2)
	leftW, _ := s.railGeometry(innerW)
	railX := workspaceSettingsInset
	if !s.accountsOpen() || s.model.accountEditor == nil {
		searchY := oy + s.topPad() + 1
		for x := ox + railX; x < ox+railX+leftW; x++ {
			buf.Set(x, searchY, cell.NewCell(ui.FirstRune(ui.DefaultGlyphsFromTokens(s.tokens).BorderH, '-'), divider, bg, 0))
		}
	}
	if s.accountsOpen() {
		accent := s.tokens.Color(ui.ColorAccent)
		contentX, contentW := s.contentRect.X, s.contentRect.W
		buf.WriteString(ox+contentX, oy+s.topPad(), rendering.TruncateVisual(s.PaneTitle(), contentW, ""), accent, bg, cell.AttrBold)
		if s.model.accountEditor == nil && len(s.model.accounts) == 0 {
			buf.WriteString(ox+contentX, oy+s.topPad()+4, "No accounts yet. Add one to start a session.", s.tokens.Color(ui.ColorMuted), bg, 0)
		} else if s.model.accountEditor == nil && len(s.model.accountRows()) == 0 {
			buf.WriteString(ox+contentX, oy+s.topPad()+4, "No matching accounts.", s.tokens.Color(ui.ColorMuted), bg, 0)
		}
		noticeY := s.accountBar.Bounds().Y - 1
		if s.model.accountEditor != nil {
			noticeY = s.accountEditorBar.Bounds().Y - 2
		} else if s.accountAdd.Bounds().Y < s.accountBar.Bounds().Y {
			noticeY = s.accountAdd.Bounds().Y - 1
		}
		if s.model.accountError != "" && s.model.accountEditor == nil {
			buf.WriteString(ox+contentX, oy+noticeY, rendering.TruncateVisual(s.model.accountError, contentW, ""), s.tokens.Color(ui.ColorError), bg, 0)
		} else if s.model.accountBusy {
			buf.WriteString(ox+contentX, oy+noticeY, "Saving account...", s.tokens.Color(ui.ColorMuted), bg, 0)
		}
	}
	// This separator is above the entire footer, not a tab baseline.
	for x := 0; x < r.W; x++ {
		buf.Set(ox+x, oy+r.H-2, cell.NewCell(ui.FirstRune(s.tokens.Glyphs().BorderH, '-'), divider, bg, 0))
	}
}

// Handle receives keys bubbled from focused children (search TextField leaves
// Up/Down/Esc unhandled).
//
// Region Tab order (documented + tested): search → categories → form →
// reset (workspace) → actions → scope → wrap. Shift+Tab reverses. Form Tab
// first walks fields, then emits form.exit.forward/backward to leave the form.
//
// Esc behaviour:
//   - search with non-empty query: clear query (handled by search field)
//   - form (no select open): leave form → categories (form.exit.backward)
//   - categories / empty search / actions / surface: CancelMsg (close +
//     revert live theme preview to the last saved preset)
func (s *WorkspaceSettingsSurface) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.model == nil {
		return ui.EventResult{}
	}
	if s.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && ctx.LocalY == s.topPad() {
		parts := s.paneTitleParts()
		if len(parts) > 1 && ui.BreadcrumbIndex(parts, s.breadcrumbSeparator(), ctx.LocalX-s.contentRect.X, s.contentRect.W) == 0 {
			if s.accountsOpen() && s.model.accountEditor != nil {
				return s.cancelAccountEdit()
			}
		}
	}
	// Custom events (paste, file drops) belong to the focused text control.
	// Rejecting them here left the value inserted but the horizontal
	// viewport stale until the next arrow key forced a re-layout.
	if !evt.IsKey() && evt.Kind != ui.EventCustom {
		return ui.EventResult{}
	}
	if evt.Kind == ui.EventCustom {
		if s.form != nil {
			return s.form.Handle(ui.EventContext{}, evt)
		}
		return ui.EventResult{}
	}
	key := evt.Key
	switch key.Code {
	case tea.KeyUp:
		if s.capCatalog.dirty() {
			return ui.EventResult{Handled: true}
		}
		if s.accountsOpen() && s.model.accountEditor != nil {
			return ui.EventResult{Handled: true}
		}
		if s.model.MoveCategory(-1) {
			s.categories.syncItems()
			s.refreshForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case tea.KeyDown:
		if s.capCatalog.dirty() {
			return ui.EventResult{Handled: true}
		}
		if s.accountsOpen() && s.model.accountEditor != nil {
			return ui.EventResult{Handled: true}
		}
		if s.model.MoveCategory(1) {
			s.categories.syncItems()
			s.refreshForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true}
	case tea.KeyTab:
		if key.Mod.Contains(tea.ModShift) {
			s.FocusScope()
		} else {
			s.model.SetSearchQuery(s.search.field.Value())
			s.categories.syncItems()
			s.refreshForm()
			s.FocusCategories()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidateLayout | ui.InvalidatePaint}
	case tea.KeyEsc:
		return s.cancelSettings()
	}
	return ui.EventResult{}
}

func (s *WorkspaceSettingsSurface) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if s.model != nil && s.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	if strings.HasPrefix(string(action.ID), "settings.capability.") {
		return s.capCatalog.HandleAction(ui.EventContext{}, action)
	}
	switch action.ID {
	case "form.changed":
		fieldID, _ := action.Payload.(string)
		if fieldID == "preset" {
			preset := s.model.value(catAppearance, "preset")
			return ui.EventResult{
				Handled: true,
				Cmd:     func() tea.Msg { return ThemePreviewMsg{Preset: preset} },
			}
		}
		if frame, ok := s.model.framePreview(fieldID); ok {
			return ui.EventResult{
				Handled: true,
				Cmd:     func() tea.Msg { return FramePreviewMsg(frame) },
			}
		}
		// Account connection changes rebuild their FormModel;
		// rebind the widget to paint the correct rows immediately.
		if s.model != nil && s.accountsOpen() && (fieldID == accountFieldSeed || fieldID == accountFieldAuthType || fieldID == accountFieldProtocol) {
			s.refreshForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
		return ui.EventResult{Handled: true}
	case "settings.account.begin-add":
		return s.openAccountEditor(true, "")
	case "settings.account.clone":
		return s.openAccountClone()
	case "settings.account.edit":
		return s.openAccountEditor(false, "")
	case "settings.account.cancel-edit":
		return s.cancelAccountEdit()
	case "settings.account.commit":
		if s.model.accountBusy {
			return ui.EventResult{Handled: true}
		}
		change, err := s.model.accountEditChange()
		if err != nil {
			s.model.accountError = err.Error()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		return s.submitAccountChange(change)
	case "settings.account.remove":
		if s.model.accountBusy {
			return ui.EventResult{Handled: true}
		}
		account, ok := s.model.accounts[s.model.accountID]
		if !ok {
			return ui.EventResult{Handled: true}
		}
		return s.requestPrompt(PromptRemoveAccount, account.ID, account.Label, 0)
	case "settings.account.confirm-remove":
		id, _ := action.Payload.(string)
		if s.model.accountBusy || id == "" || id != s.model.accountID {
			return ui.EventResult{Handled: true}
		}
		change, err := s.model.accountSelectionChange("remove")
		if err != nil {
			return ui.EventResult{Handled: true}
		}
		return s.submitAccountChange(change)
	case "settings.chatgpt.signin":
		if s.model.Scope() != types.SettingsScopeApplication || s.model.accountEditor == nil || s.model.accountBusy || s.model.accountEditor.draft.Connection.AuthType != provider.AuthChatGPT || s.model.authWaitingID == s.model.accountEditor.draft.ID {
			return ui.EventResult{Handled: true}
		}
		accountID, generation := s.model.SignInRequest()
		s.model.authWaitingID = accountID
		s.model.rebuildAccountsForm()
		s.refreshForm()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint, Cmd: func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
			defer cancel()
			creds, err := provider.SignInWithChatGPT(ctx)
			return ChatGPTSignInResultMsg{AccountID: accountID, Generation: generation, Creds: creds, Err: err}
		}}
	case "settings.chatgpt.signout":
		if s.model.accountEditor == nil || s.model.accountEditor.draft.Connection.AuthType != provider.AuthChatGPT || s.model.accountBusy {
			return ui.EventResult{Handled: true}
		}
		accountID := s.model.accountEditor.draft.ID
		_, generation := s.model.SignInRequest()
		s.model.authWaitingID = ""
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ChatGPTSignOutMsg{AccountID: accountID, Generation: generation} }}
	case "form.select.open":
		fieldID, _ := action.Payload.(string)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint, Cmd: s.model.OpenSelect(fieldID)}
	case "form.cancel":
		if s.accountsOpen() {
			return s.cancelSettings()
		} else {
			s.FocusCategories()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.backward":
		if s.accountsOpen() {
			if s.model.accountEditor != nil {
				s.accountEditorBar.FocusLast()
				s.focusID(WorkspaceSettingsEditorBarID)
			} else {
				s.FocusAccountList()
			}
		} else {
			s.FocusCategories()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "form.exit.forward":
		if s.accountsOpen() {
			if s.model.accountEditor != nil {
				s.focusID(WorkspaceSettingsEditorBarID)
			} else {
				s.focusID(WorkspaceSettingsAccountBarID)
			}
		} else {
			s.FocusReset()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "accounts.add.forward":
		s.FocusAccountList()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "accounts.add.backward":
		s.FocusAccountFilter()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "accounts.actions.forward":
		s.focusID(WorkspaceSettingsAccountMoreID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "accounts.actions.backward":
		s.FocusAccountList()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "accounts.more.forward":
		s.FocusActions()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "accounts.more.backward":
		s.focusID(WorkspaceSettingsAccountBarID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "account.editor.forward":
		s.form.FocusFirst()
		s.FocusForm()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "account.editor.backward":
		s.form.FocusLast()
		s.FocusForm()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "resetbar.exit.backward":
		s.form.FocusLast()
		s.FocusForm()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "resetbar.exit.forward":
		s.FocusActions()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.backward":
		if s.accountsOpen() {
			s.focusID(WorkspaceSettingsAccountMoreID)
		} else if s.showReset() {
			s.FocusReset()
		} else {
			s.form.FocusLast()
			s.FocusForm()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.forward":
		s.FocusScope()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "settings.save":
		if s.model.capabilityDraft != nil {
			s.capCatalog.message = "Save or discard the capability draft first."
			s.capCatalog.focusContent()
			return s.capCatalog.changed()
		}
		if s.agents.draft != nil {
			s.model.SelectCategory(catAgents)
			s.agents.notice = "Apply or discard the agent draft before saving settings."
			s.syncModel()
			s.FocusForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
		if s.model.accountBusy || s.model.accountEditor != nil {
			return ui.EventResult{Handled: true}
		}
		if category, fieldID, valid := s.model.validateNetworkSettings(); !valid {
			s.model.SelectCategory(category)
			s.model.EnterForm()
			s.categories.syncItems()
			s.refreshForm()
			s.form.FocusField(fieldID)
			s.FocusForm()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
		if !s.model.BeginSave() {
			return ui.EventResult{Handled: true}
		}
		s.syncAccountControls()
		target := s.model
		scope := target.Scope()
		appSettings := target.AppSettings()
		overrides := target.Overrides()
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg {
			return SaveMsg{Target: target, Scope: scope, AppSettings: appSettings, Overrides: overrides}
		}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "settings.reset":
		if !s.showReset() {
			return ui.EventResult{Handled: true}
		}
		if s.agents.draft != nil {
			return s.agents.back()
		}
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ResetSectionMsg{Key: s.model.currentOverrideKey()} }}
	case "settings.scope":
		id, _ := action.Payload.(string)
		next := types.SettingsScopeApplication
		if id == scopeTabWorkspace {
			next = types.SettingsScopeWorkspace
		}
		if s.model.Scope() == next {
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		if s.capCatalog.dirty() {
			s.model.nextScope = next
			return s.requestPrompt(PromptDiscardEditor, "", "", 0)
		}
		if s.model.capabilityDraft != nil {
			s.capCatalog.closeEditor()
		}
		if s.model.hasUnsavedPreferences() {
			s.model.nextScope = next
			return s.requestPrompt(PromptDiscardSettings, "", "", 0)
		}
		if s.agents.draft != nil {
			if s.agents.dirty() {
				s.model.nextScope = next
				return s.agents.back()
			}
			s.agents.closeDraft()
		}
		if s.model.accountEditor != nil {
			result := s.cancelAccountEdit()
			if result.Cmd != nil {
				s.model.nextScope = next
				return result
			}
		}
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ScopeMsg{Scope: next} }}
	case "settings.cancel":
		return s.cancelSettings()
	case "settings.close":
		return s.RequestClose()
	case "settings.discard.stay":
		s.model.nextCategory, s.model.nextScope = -1, ""
		s.capCatalog.nextOpen = nil
		return ui.EventResult{Handled: true}
	case "settings.discard.confirm":
		kind, _ := action.Payload.(PromptKind)
		if kind == PromptDiscardSettings {
			if s.model.nextScope != "" {
				scope := s.model.nextScope
				s.model.nextCategory, s.model.nextScope = -1, ""
				return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ScopeMsg{Scope: scope} }}
			}
			s.model.nextCategory, s.model.nextScope = -1, ""
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CancelMsg{} }}
		}
		if kind != PromptDiscardEditor {
			return ui.EventResult{Handled: true}
		}
		if s.model.capabilityDraft != nil {
			s.capCatalog.closeEditor()
			s.capCatalog.detail = false
			s.capCatalog.syncRows()
		} else {
			s.model.cancelAccountEdit()
		}
		if next := s.capCatalog.nextOpen; next != nil {
			s.capCatalog.nextOpen = nil
			return s.OpenCapabilities(next.kind, next.query, next.id)
		}
		s.refreshForm()
		if s.model.nextScope != "" {
			scope := s.model.nextScope
			s.model.nextCategory, s.model.nextScope = -1, ""
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return ScopeMsg{Scope: scope} }}
		}
		if s.model.nextCategory >= 0 {
			s.model.SelectCategory(s.model.nextCategory)
			s.model.nextCategory = -1
			s.refreshForm()
		}
		if s.accountsOpen() {
			s.FocusAccountList()
		} else {
			s.form.FocusFirst()
			s.FocusForm()
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
	case ui.ActionSplitResize:
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

func (s *WorkspaceSettingsSurface) openAccountEditor(create bool, providerID string) ui.EventResult {
	if !s.accountsOpen() || s.model.accountBusy {
		return ui.EventResult{Handled: true}
	}
	if err := s.model.beginAccountEdit(create, providerID); err != nil {
		s.model.accountError = err.Error()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.refreshForm()
	s.form.FocusFirst()
	s.FocusForm()
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
}

func (s *WorkspaceSettingsSurface) openAccountClone() ui.EventResult {
	if !s.accountsOpen() || s.model.accountBusy {
		return ui.EventResult{Handled: true}
	}
	if err := s.model.beginAccountClone(); err != nil {
		s.model.accountError = err.Error()
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	s.refreshForm()
	s.form.FocusFirst()
	s.FocusForm()
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
}

func (s *WorkspaceSettingsSurface) submitAccountChange(change ttypes.AccountChange) ui.EventResult {
	s.model.accountBusy = true
	s.model.accountError = ""
	s.model.rebuildAccountsForm()
	s.refreshForm()
	s.syncAccountControls()
	owner := s.model
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint, Cmd: func() tea.Msg {
		return AccountChangeMsg{Target: owner, Change: change}
	}}
}

func (s *WorkspaceSettingsSurface) requestPrompt(kind PromptKind, id, label string, count int) ui.EventResult {
	owner := s.model
	msg := PromptMsg{Target: owner, Kind: kind, ID: id, Label: label, RelatedCount: count}
	switch kind {
	case PromptRemoveAccount:
		before := provider.CloneAccount(owner.accounts[id])
		msg.beforeAccount = &before
	case PromptDiscardEditor, PromptDiscardSettings:
		if kind == PromptDiscardSettings {
			msg.preferences = owner.preferenceBytes()
			msg.agentEditor = s.agents.draft
			msg.agentDraft = formDraftBytes(s.agents.draft)
			msg.capabilityForm = owner.capabilityDraft
		}
		if owner.capabilityDraft != nil || kind == PromptDiscardSettings {
			msg.capabilityEditor = s.capCatalog
			msg.capabilityDraft = formDraftBytes(owner.capabilityDraft)
		}
		if owner.accountEditor != nil {
			owner.storeAccountForm()
			msg.accountEditor = owner.accountEditor
			draft := provider.CloneAccount(owner.accountEditor.draft)
			msg.accountDraft = &draft
		}
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg {
		return msg
	}}
}

// PromptRelevant rejects queued actions from a closed, replaced, or switched
// settings page before opening another modal window above it.
func (s *WorkspaceSettingsSurface) PromptRelevant(msg PromptMsg) bool {
	if s.model == nil || s.model != msg.Target || s.model.accountBusy || s.model.Saving() {
		return false
	}
	switch msg.Kind {
	case PromptRemoveAccount:
		current, ok := s.model.accounts[msg.ID]
		return ok && s.accountsOpen() && s.model.accountID == msg.ID && msg.beforeAccount != nil && reflect.DeepEqual(current, *msg.beforeAccount)
	case PromptDiscardEditor:
		if s.model.capabilityDraft != nil && msg.capabilityEditor == s.capCatalog {
			return reflect.DeepEqual(formDraftBytes(s.model.capabilityDraft), msg.capabilityDraft)
		}
		if s.accountsOpen() && s.model.accountEditor != nil && s.model.accountEditor == msg.accountEditor && msg.accountDraft != nil {
			s.model.storeAccountForm()
			return reflect.DeepEqual(s.model.accountEditor.draft, *msg.accountDraft)
		}
		return false
	case PromptDiscardSettings:
		return s.hasUnsavedDrafts() && reflect.DeepEqual(s.model.preferenceBytes(), msg.preferences) && s.closeDraftsRelevant(msg)
	case PromptRemoveCapability:
		return s.capabilitiesOpen() && msg.capabilityEditor == s.capCatalog && msg.ID == s.capCatalog.id && msg.CapabilityAction != nil && reflect.DeepEqual(msg.capabilityDraft, s.capCatalog.entityBytes()) && s.model.capabilityPending == nil
	}
	return false
}

func (s *WorkspaceSettingsSurface) closeDraftsRelevant(msg PromptMsg) bool {
	if s.model.accountEditor != msg.accountEditor || s.agents.draft != msg.agentEditor || s.model.capabilityDraft != msg.capabilityForm ||
		s.capCatalog != msg.capabilityEditor {
		return false
	}
	if s.model.accountEditor != nil {
		s.model.storeAccountForm()
		if msg.accountDraft == nil || !reflect.DeepEqual(s.model.accountEditor.draft, *msg.accountDraft) {
			return false
		}
	}
	return reflect.DeepEqual(formDraftBytes(s.agents.draft), msg.agentDraft) &&
		reflect.DeepEqual(formDraftBytes(s.model.capabilityDraft), msg.capabilityDraft)
}

func (s *WorkspaceSettingsSurface) cancelAccountEdit() ui.EventResult {
	if s.model.accountBusy {
		return ui.EventResult{Handled: true}
	}
	if s.model.accountEditorDirty() {
		return s.requestPrompt(PromptDiscardEditor, "", "", 0)
	}
	s.model.cancelAccountEdit()
	s.refreshForm()
	s.FocusAccountList()
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
}

func (s *WorkspaceSettingsSurface) cancelSettings() ui.EventResult {
	if s.model != nil && s.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	if s.capabilitiesOpen() && (s.capCatalog.detail || s.capCatalog.edit != "") {
		return s.capCatalog.back()
	}
	if s.agents.draft != nil {
		s.model.SelectCategory(catAgents)
		s.syncModel()
		return s.agents.back()
	}
	if s.model.accountBusy {
		return ui.EventResult{Handled: true}
	}
	if s.accountsOpen() {
		if s.model.accountEditor != nil {
			return s.cancelAccountEdit()
		}
	}
	if s.model.hasUnsavedPreferences() {
		return s.requestPrompt(PromptDiscardSettings, "", "", 0)
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CancelMsg{} }}
}
func (s *WorkspaceSettingsSurface) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace settings"}
}

type settingsSearchField struct {
	ui.Base
	field *ui.FilterField
	owner *WorkspaceSettingsSurface
}

func (s *settingsSearchField) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.field.Measure(ctx, cs)
}
func (s *settingsSearchField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *settingsSearchField) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	s.field.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, ctx)
}
func (s *settingsSearchField) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.owner == nil || s.owner.model == nil {
		return s.field.Handle(ctx, evt)
	}
	if s.owner.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	result := s.field.Handle(ctx, evt)
	if evt.Kind == ui.EventMouseClick && s.owner.form.CloseOpenSelects() {
		result.Invalidate |= ui.InvalidatePaint | ui.InvalidateLayout
	}
	s.owner.model.SetSearchActive(true)
	if evt.IsKey() {
		key := evt.Key
		switch key.Code {
		case tea.KeyEnter:
			s.owner.model.SetSearchQuery(s.field.Value())
			s.owner.model.EnterForm()
			s.owner.categories.syncItems()
			s.owner.refreshForm()
			if s.owner.accountsOpen() {
				s.owner.FocusAccountFilter()
			} else {
				s.owner.form.FocusFirst()
				s.owner.FocusForm()
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				s.owner.FocusScope()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
			}
			s.owner.model.SetSearchQuery(s.field.Value())
			s.owner.categories.syncItems()
			s.owner.refreshForm()
			s.owner.FocusCategories()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
		// Esc with a non-empty query is cleared by the FilterField itself;
		// Esc with an empty query is unhandled so the surface cancels.
	}
	s.owner.model.SetSearchQuery(s.field.Value())
	s.owner.categories.syncItems()
	s.owner.refreshForm()
	if result.Invalidate == 0 {
		result.Invalidate = ui.InvalidateLayout | ui.InvalidatePaint
	} else {
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return result
}
func (s *settingsSearchField) Semantics() ui.Semantics       { return s.field.Semantics() }
func (s *settingsSearchField) SetTokens(tokens *ui.TokenSet) { s.field.SetTokens(tokens) }

type settingsCategoryList struct {
	ui.Base
	list  *ui.List
	owner *WorkspaceSettingsSurface
}

// settingsTableFilter is the retained search field for the application account table.
// Its query changes the table projection, never the saved accounts.
type settingsTableFilter struct {
	ui.Base
	field *ui.FilterField
	owner *WorkspaceSettingsSurface
}

func (s *settingsTableFilter) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.field.Measure(ctx, cs)
}
func (s *settingsTableFilter) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *settingsTableFilter) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	s.field.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, ctx)
}
func (s *settingsTableFilter) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.owner == nil || s.owner.model == nil {
		return s.field.Handle(ctx, evt)
	}
	if s.owner.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyTab:
			if evt.Key.Mod.Contains(tea.ModShift) {
				s.owner.FocusCategories()
			} else {
				s.owner.focusID(WorkspaceSettingsAddID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case tea.KeyDown, tea.KeyEnter:
			s.owner.FocusAccountList()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	result := s.field.Handle(ctx, evt)
	if s.owner.model.accountQuery != s.field.Value() {
		s.owner.model.accountQuery = s.field.Value()
		s.owner.accounts.syncRows()
		s.owner.syncAccountControls()
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
	}
	return result
}
func (s *settingsTableFilter) Semantics() ui.Semantics       { return s.field.Semantics() }
func (s *settingsTableFilter) SetTokens(tokens *ui.TokenSet) { s.field.SetTokens(tokens) }

func (s *settingsCategoryList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.list.Measure(ctx, cs)
}
func (s *settingsCategoryList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *settingsCategoryList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	s.list.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, ctx)
}
func (s *settingsCategoryList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.owner != nil && s.owner.model != nil && s.owner.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	result := s.list.Handle(ctx, evt)
	if s.owner == nil || s.owner.model == nil {
		return result
	}
	if evt.Kind == ui.EventMouseClick && s.owner.form.CloseOpenSelects() {
		result.Invalidate |= ui.InvalidatePaint | ui.InvalidateLayout
	}
	if selected, ok := s.list.Selected(); ok {
		if index, ok := selected.Payload.(int); ok && index != s.owner.model.SelectedCategory() {
			if s.owner.capCatalog.dirty() {
				s.owner.model.nextCategory = index
				s.syncItems()
				return s.owner.requestPrompt(PromptDiscardEditor, "", "", 0)
			}
			if s.owner.model.capabilityDraft != nil {
				s.owner.capCatalog.closeEditor()
			}
			if s.owner.model.accountEditor != nil {
				change := s.owner.cancelAccountEdit()
				if change.Cmd != nil {
					s.owner.model.nextCategory = index
					s.syncItems()
					return ui.EventResult{Handled: true, Cmd: change.Cmd, Invalidate: change.Invalidate | ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
				}
			}
			s.owner.model.SelectCategory(index)
			s.owner.refreshForm()
			result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
		}
	}
	if evt.IsKey() {
		key := evt.Key
		switch key.Code {
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				s.owner.FocusSearch()
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
			}
			s.owner.model.EnterForm()
			s.owner.refreshForm()
			if s.owner.accountsOpen() {
				s.owner.FocusAccountFilter()
			} else {
				s.owner.form.FocusFirst()
				s.owner.FocusForm()
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		case tea.KeyEnter, tea.KeyRight:
			s.owner.model.EnterForm()
			s.owner.refreshForm()
			if s.owner.accountsOpen() {
				s.owner.FocusAccountFilter()
			} else {
				s.owner.form.FocusFirst()
				s.owner.FocusForm()
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
	}
	return result
}
func (s *settingsCategoryList) Semantics() ui.Semantics       { return s.list.Semantics() }
func (s *settingsCategoryList) SetTokens(tokens *ui.TokenSet) { s.list.SetTokens(tokens) }

// syncItems reconciles the rail rows with the model. It re-anchors the
// viewport only when the selected category actually moved — keyboard
// navigation, a deep link, or a filtered row disappearing.
//
// The popup forwards every pointer event its children do not consume through
// HandleEvent → SetModel → syncModel, so this runs several times while the
// mouse merely crosses the window. Re-anchoring unconditionally snapped a
// wheel-scrolled rail back onto the selected category (the reported "sidebar
// jumps when I scroll then move the mouse or click"). A pointer resync keeps
// the same selection, so it must leave the reader's viewport alone.
func (s *settingsCategoryList) syncItems() {
	if s.owner == nil || s.owner.model == nil {
		return
	}
	previous := -1
	if current, ok := s.list.Selected(); ok {
		if index, ok := current.Payload.(int); ok {
			previous = index
		}
	}
	items := s.owner.model.CategoryItems()
	s.list.SetItemsStable(items)
	target := s.owner.model.SelectedCategory()
	for index, item := range items {
		if selected, ok := item.Payload.(int); ok && selected == target {
			// Select directly: List.SelectIndex would also re-anchor the
			// viewport, and choosing a page is not itself a scroll request.
			s.list.Selection().SetIndex(index)
			if previous != target {
				s.list.Scroll().EnsureVisible(index)
			}
			break
		}
	}
}

// settingsAccountTable retains row selection and scroll through ordinary
// paints. Clicking a different row selects it; Enter or a second click edits.
type settingsAccountTable struct {
	ui.Base
	table   *ui.Table
	owner   *WorkspaceSettingsSurface
	columns int
}

func (s *settingsAccountTable) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return s.table.Measure(ctx, cs)
}
func (s *settingsAccountTable) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	columns := 2
	if r.W >= 54 {
		columns = 3
	}
	if columns != s.columns {
		s.columns = columns
		s.syncRows()
	}
	s.table.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (s *settingsAccountTable) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	s.table.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, ctx)
}
func (s *settingsAccountTable) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.owner != nil && s.owner.model != nil && (s.owner.model.accountBusy || s.owner.model.Saving()) {
		return ui.EventResult{Handled: true}
	}
	previous := s.owner.model.accountID
	result := s.table.Handle(ctx, evt)
	if s.owner == nil || s.owner.model == nil {
		return result
	}
	if row, ok := s.table.Selected(); ok && row.ID != previous {
		s.owner.model.selectAccount(row.ID)
		s.owner.syncAccountControls()
		result.Invalidate |= ui.InvalidatePaint
		if evt.Kind == ui.EventMouseClick {
			result.Action = nil
		}
	}
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyTab:
			if evt.Key.Mod.Contains(tea.ModShift) {
				s.owner.focusID(WorkspaceSettingsAddID)
			} else {
				s.owner.focusID(WorkspaceSettingsAccountBarID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
	}
	return result
}
func (s *settingsAccountTable) Semantics() ui.Semantics       { return s.table.Semantics() }
func (s *settingsAccountTable) SetTokens(tokens *ui.TokenSet) { s.table.SetTokens(tokens) }
func (s *settingsAccountTable) syncRows() {
	if s.owner == nil || s.owner.model == nil {
		return
	}
	var cols []ui.TableColumn
	switch s.columns {
	case 3:
		cols = []ui.TableColumn{{ID: "connection", Label: "Connection", Track: ui.FractionGridTrack(2)}, {ID: "name", Label: "Account", Track: ui.FractionGridTrack(2)}, {ID: "status", Label: "Credentials", Track: ui.FractionGridTrack(3)}}
	default:
		cols = []ui.TableColumn{{ID: "name", Label: "Account", Track: ui.FractionGridTrack(3)}, {ID: "connection", Label: "Connection", Track: ui.FractionGridTrack(2)}}
	}
	if len(s.table.Columns()) != len(cols) {
		s.table.SetColumns(cols)
	}
	base := s.owner.model.accountRows()
	rows := make([]ui.TableRow, 0, len(base))
	for _, row := range base {
		cells := row.Cells
		if len(cells) != 3 {
			cells = []ui.TableCell{{Text: "Account row unavailable"}, {Text: "Reopen settings"}, {Text: ""}}
		}
		if s.columns == 2 {
			cells = []ui.TableCell{cells[1], cells[0]}
		}
		rows = append(rows, ui.TableRow{ID: row.ID, Cells: cells})
	}
	old := s.table.Rows()
	changed := len(old) != len(rows)
	if !changed {
		for i := range rows {
			if old[i].ID != rows[i].ID || len(old[i].Cells) != len(rows[i].Cells) {
				changed = true
				break
			}
			for j := range rows[i].Cells {
				if old[i].Cells[j].Text != rows[i].Cells[j].Text {
					changed = true
					break
				}
			}
		}
	}
	if changed {
		s.table.SetRows(rows)
	}
	for index, row := range rows {
		if row.ID == s.owner.model.accountID {
			if index != s.table.SelectedIndex() {
				s.table.SelectIndex(index)
			}
			break
		}
	}
}

var _ ui.Component = (*WorkspaceSettingsSurface)(nil)
var _ ui.Parent = (*WorkspaceSettingsSurface)(nil)
var _ ui.ActionHandler = (*WorkspaceSettingsSurface)(nil)

// Settings scope is a two-state toggle, not a content tab bar.
const (
	scopeTabApplication = "scope:application"
	scopeTabWorkspace   = "scope:workspace"
)

type settingsScopeStrip struct {
	ui.Base
	owner       *WorkspaceSettingsSurface
	application *ui.ToggleButton
	workspace   *ui.ToggleButton
	hover       int
}

func newSettingsScopeStrip(owner *WorkspaceSettingsSurface, tokens *ui.TokenSet) *settingsScopeStrip {
	s := &settingsScopeStrip{Base: ui.NewBase(WorkspaceSettingsScopeID), owner: owner}
	s.hover = -1
	s.application = ui.NewToggleButton(WorkspaceSettingsScopeID+"/application", " Application ", "settings.scope", tokens)
	s.workspace = ui.NewToggleButton(WorkspaceSettingsScopeID+"/workspace", " Workspace ", "settings.scope", tokens)
	s.application.SetSwitchChrome(true)
	s.workspace.SetSwitchChrome(true)
	s.sync()
	return s
}

// sync projects the model's scope onto the strip.
func (s *settingsScopeStrip) sync() {
	workspace := s.owner != nil && s.owner.model != nil && s.owner.model.Scope() == types.SettingsScopeWorkspace
	s.application.SetChecked(!workspace)
	s.workspace.SetChecked(workspace)
}

func (s *settingsScopeStrip) SetTokens(tokens *ui.TokenSet) {
	s.application.SetTokens(tokens)
	s.workspace.SetTokens(tokens)
}

func (s *settingsScopeStrip) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 24, H: 1})
}

func (s *settingsScopeStrip) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	s.application.SetLabel(" Application ")
	s.workspace.SetLabel(" Workspace ")
	left := 13
	if r.W < 24 {
		s.application.SetLabel("Application")
		s.workspace.SetLabel("Workspace")
		left = min(12, r.W/2+1)
	}
	if r.W < 21 {
		s.application.SetLabel("App")
		s.workspace.SetLabel("Work")
		left = min(6, r.W/2)
	}
	s.application.Layout(ctx, ui.Rect{W: min(left, r.W), H: 1})
	s.workspace.Layout(ctx, ui.Rect{X: left, W: max(0, min(11, r.W-left)), H: 1})
}

func (s *settingsScopeStrip) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	a, w := ctx, ctx
	a.Focused = ctx.Focused && s.application.Checked()
	w.Focused = ctx.Focused && s.workspace.Checked()
	a.Hovered = ctx.Hovered && s.hover == 0
	w.Hovered = ctx.Hovered && s.hover == 1
	s.application.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, a)
	s.workspace.Paint(buf, ox+s.Bounds().X, oy+s.Bounds().Y, w)
}

func (s *settingsScopeStrip) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if s.owner == nil {
		return ui.EventResult{}
	}
	if s.owner.model != nil && s.owner.model.Saving() {
		return ui.EventResult{Handled: true}
	}
	if evt.Kind == ui.EventMouseMotion {
		s.hover = 0
		if ctx.LocalX >= s.workspace.Bounds().X {
			s.hover = 1
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	if evt.IsKey() {
		key := evt.Key
		switch key.Code {
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				s.owner.FocusActions()
			} else {
				s.owner.FocusSearch()
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case tea.KeyUp:
			s.owner.FocusCategories()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case tea.KeyLeft:
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "settings.scope", Payload: scopeTabApplication}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		case tea.KeyRight:
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "settings.scope", Payload: scopeTabWorkspace}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		case tea.KeyEnter, tea.KeySpace:
			next := scopeTabWorkspace
			if s.workspace.Checked() {
				next = scopeTabApplication
			}
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "settings.scope", Payload: next}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
	}
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
		id := scopeTabApplication
		if ctx.LocalX >= s.workspace.Bounds().X {
			id = scopeTabWorkspace
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "settings.scope", Payload: id}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (s *settingsScopeStrip) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleButton, Label: "Settings scope", Value: string(s.owner.model.Scope())}
}
