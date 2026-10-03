package memorybrowser

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/memory"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID     ui.ComponentID = "memorybrowser"
	TitleID      ui.ComponentID = "memorybrowser/title"
	TabsID       ui.ComponentID = "memorybrowser/tabs"
	SearchID     ui.ComponentID = "memorybrowser/search"
	ListID       ui.ComponentID = "memorybrowser/list"
	ScrollbarID  ui.ComponentID = "memorybrowser/scrollbar"
	StatusID     ui.ComponentID = "memorybrowser/status"
	EmptyID      ui.ComponentID = "memorybrowser/empty"
	DetailID     ui.ComponentID = "memorybrowser/detail"
	FormID       ui.ComponentID = "memorybrowser/correction"
	FormScrollID ui.ComponentID = "memorybrowser/correction-scroll"
	ActionsID    ui.ComponentID = "memorybrowser/actions"
	ActionListID ui.ComponentID = "memorybrowser/action-list"
	BrowseMoreID ui.ComponentID = "memorybrowser/browse-more"
)

const pageBrowseActions = "browse-actions"

type Screen struct {
	ui.Base
	th               *theme.Theme
	tokens           *ui.TokenSet
	mode             string
	page             string
	open             bool
	epoch            uint64
	searchSequence   uint64
	detailSequence   uint64
	mutationSequence uint64
	mutationIntent   MutationMsg
	loading          bool
	detailLoading    bool
	mutating         bool
	searchError      error
	detailError      error
	mutationError    error
	records          []memory.ScoredRecord
	episodes         []memory.Episode
	detail           DetailData
	draft            *memory.Record
	retirePreview    *memory.Record
	actionSpecs      []ui.ActionBarButton
	compactActions   bool
	dockHeaderInset  int
	browseOverflow   bool
	title            *ui.Label
	tabs             *ui.Tabs
	search           *searchField
	list             *browserList
	scrollbar        *ui.ScrollbarControl
	status           *ui.Label
	empty            *ui.Label
	reader           *readingArea
	form             *ui.Form
	formModel        *ui.FormModel
	formScroll       *ui.ScrollbarControl
	actions          *ui.ActionBar
	actionList       *ui.List
	browseMore       *ui.Button
	focus            func(ui.ComponentID)
	clipboardCopy    func(string) tea.Cmd
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), th: th, tokens: tokens, mode: ModeKnowledge, page: pageBrowse, clipboardCopy: rendering.CopyToClipboardCmd}
	s.title = ui.NewLabel(TitleID, "Workspace Memory", th)
	s.tabs = ui.NewTabs(TabsID, tokens)
	s.tabs.SetChrome(ui.TabsShell)
	s.tabs.SetItems([]ui.TabItem{{ID: ModeKnowledge, Label: "Knowledge"}, {ID: ModeEpisodes, Label: "Episodes"}}, s.mode)

	field := ui.NewFilterField(SearchID, tokens)
	field.SetPrompt(ui.SearchGlyphEnd(tokens))
	field.SetPromptPlacement(ui.PromptEnd)
	field.SetPlaceholder("Search workspace knowledge")
	field.SetSubmitAction("")
	s.search = &searchField{Base: ui.NewBase(SearchID), field: field, owner: s}

	list := ui.NewList(ListID, tokens)
	list.SetActivateAction("memorybrowser.activate")
	list.SetDefaultRowBackground(ui.ColorBg)
	s.list = &browserList{Base: ui.NewBase(ListID), list: list, owner: s}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)
	s.scrollbar.SetSurfaceBackground(ui.ColorBg)
	s.empty = ui.NewLabel(EmptyID, "No knowledge yet. Browsing remains available with extraction off.", th)
	s.empty.SetColorRole(ui.ColorMuted)
	s.status = ui.NewLabel(StatusID, "Workspace-wide · Enter inspect · / search", th)
	s.status.SetColorRole(ui.ColorMuted)
	area := ui.NewTextArea(DetailID, tokens, true)
	area.SetReadOnly(true)
	area.SetSubmitAction("")
	area.SetBackgroundColor(tokens.Color(ui.ColorBg))
	s.reader = &readingArea{Base: ui.NewBase(DetailID), area: area, owner: s}
	s.actions = ui.NewDockedActionBar(ActionsID, tokens)
	s.actions.SetCancelAction("memorybrowser.back")
	s.actionList = ui.NewList(ActionListID, tokens)
	s.actionList.SetDefaultRowBackground(ui.ColorBg)
	s.actionList.SetActivateAction("memorybrowser.action")
	s.browseMore = ui.NewButton(BrowseMoreID, "More", "memorybrowser.browse-actions")
	s.browseMore.SetTokens(tokens)
	s.browseMore.SetVariant(ui.VariantSecondary)
	s.browseMore.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	s.syncActions()
	return s
}

func (s *Screen) Open() tea.Cmd {
	if !s.open {
		s.open = true
		s.epoch++
	}
	return s.Refresh()
}

func (s *Screen) Close() {
	if s.mutating {
		s.mutationError = errors.New("panel hidden while saving; reload the revision before retrying")
	}
	s.open = false
	s.epoch++
	s.loading, s.detailLoading, s.mutating = false, false, false
	s.freezeCorrection(false)
	s.syncActions()
	s.syncStatus()
}

func (s *Screen) IsOpen() bool { return s.open }

func (s *Screen) SetDockHeaderInset(width int) { s.dockHeaderInset = max(0, width) }
func (s *Screen) Epoch() uint64                { return s.epoch }
func (s *Screen) Query() string                { return s.search.field.Value() }
func (s *Screen) Mode() string                 { return s.mode }
func (s *Screen) SearchError() error           { return s.searchError }
func (s *Screen) MutationError() error         { return s.mutationError }
func (s *Screen) HasDraft() bool               { return s.draft != nil }

func (s *Screen) Refresh() tea.Cmd {
	if !s.open {
		return nil
	}
	s.searchSequence++
	s.loading, s.searchError = true, nil
	s.syncActions()
	s.syncStatus()
	msg := SearchMsg{Target: s, Epoch: s.epoch, Sequence: s.searchSequence, Query: s.Query(), Mode: s.mode}
	if s.mode == ModeKnowledge && strings.TrimSpace(msg.Query) == "" {
		msg.Status = "all"
	}
	return func() tea.Msg { return msg }
}

func (s *Screen) OwnsSearch(msg SearchMsg) bool {
	return s.open && msg.Target == s && msg.Epoch == s.epoch && msg.Sequence == s.searchSequence && msg.Mode == s.mode && msg.Query == s.Query()
}

func (s *Screen) ApplySearch(msg SearchMsg, records []memory.ScoredRecord, episodes []memory.Episode, err error) bool {
	if !s.OwnsSearch(msg) {
		return false
	}
	s.loading, s.searchError = false, err
	if err == nil {
		s.records, s.episodes = records, episodes
		s.syncList()
	}
	s.syncStatus()
	s.syncActions()
	return true
}

func (s *Screen) OwnsDetail(msg DetailMsg) bool {
	return s.open && msg.Target == s && msg.Epoch == s.epoch && msg.Sequence == s.detailSequence && msg.Mode == s.mode && msg.ID == s.detail.ID()
}

func (s *Screen) ApplyDetail(msg DetailMsg, detail DetailData, err error) bool {
	if !s.OwnsDetail(msg) {
		return false
	}
	s.detailLoading, s.detailError = false, err
	if err == nil {
		s.detail = detail
		if s.page == pageDetail || s.page == pageMenu {
			s.setReader(detailText(detail))
		}
	}
	s.syncActions()
	s.syncStatus()
	return true
}

func (s *Screen) OwnsMutation(msg MutationMsg) bool {
	intent := s.mutationIntent
	return s.open && s.mutating && msg.Target == s && msg.Epoch == s.epoch && msg.Sequence == s.mutationSequence && msg.ID == intent.ID && msg.Kind == intent.Kind && msg.ExpectedRevision == intent.ExpectedRevision && msg.Pinned == intent.Pinned
}

func (s *Screen) ApplyMutation(msg MutationMsg, record memory.Record, err error) bool {
	if !s.OwnsMutation(msg) {
		return false
	}
	s.mutating, s.mutationError = false, err
	s.freezeCorrection(false)
	if err == nil {
		s.detail.Record = record
		s.draft, s.retirePreview = nil, nil
		s.showPage(pageDetail)
		s.setReader(detailText(s.detail))
	}
	s.syncActions()
	if err != nil && s.page == pageRetire {
		s.actions.FocusFirst()
		s.actionList.SelectIndex(0)
	}
	s.syncStatus()
	return true
}

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }

func (s *Screen) FocusSearch() {
	if s.focus != nil {
		s.focus(SearchID)
	}
}

func (s *Screen) FocusList() {
	if s.focus == nil {
		return
	}
	if len(s.list.list.Items()) == 0 {
		s.focus(SearchID)
		return
	}
	s.focus(ListID)
}

func (s *Screen) Selected() (memory.ScoredRecord, bool) {
	item, ok := s.list.list.Selected()
	if !ok {
		return memory.ScoredRecord{}, false
	}
	record, ok := item.Payload.(memory.ScoredRecord)
	return record, ok
}

func (s *Screen) syncList() {
	var items []ui.ListItem
	if s.mode == ModeEpisodes {
		for _, episode := range s.episodes {
			items = append(items, ui.ListItem{ID: episode.ID, Label: "Episode · " + episode.Method, Detail: episode.Outcome + " · " + episode.SessionID, Payload: episode})
		}
	} else {
		for _, record := range s.records {
			label := record.Title
			if record.Pinned {
				label = "* " + label
			}
			items = append(items, ui.ListItem{ID: record.ID, Label: label, Detail: fmt.Sprintf("%s · r%d · %d evidence · %s", record.Kind, record.Revision, len(record.Evidence), record.Status), Payload: record})
		}
	}
	s.list.list.SetItemsStable(items)
}

func (s *Screen) syncStatus() {
	text, role := "Workspace-wide · Enter inspect · / search", ui.ColorMuted
	switch {
	case s.mutating:
		text = "Saving revision…"
	case s.mutationError != nil:
		text, role = "Save failed: "+s.mutationError.Error(), ui.ColorError
		if errors.Is(s.mutationError, memory.ErrRevisionConflict) || errors.Is(s.mutationError, memory.ErrRetired) {
			text = "Revision conflict. Draft kept; copy it, cancel, reload, then review."
		}
	case s.page != pageBrowse && s.detailError != nil:
		text, role = "Inspect failed: "+s.detailError.Error(), ui.ColorError
	case s.page != pageBrowse && s.detailLoading:
		text = "Loading revisions, evidence and feedback…"
	case s.searchError != nil:
		text, role = "Search failed: "+s.searchError.Error()+" · Retry", ui.ColorError
	case s.loading:
		text = "Loading workspace memory…"
	case s.page == pageRetire:
		text = "Retire this exact revision? Cancel is selected; history remains."
	case s.page == pageCorrect:
		text = fmt.Sprintf("Correcting r%d · evidence and source retained in history", s.draft.Revision)
	case s.mode == ModeEpisodes:
		text = "Workspace-wide · latest 100 episodes · observations, not knowledge"
	}
	s.status.SetText(text)
	s.status.SetColorRole(role)
	if s.searchError != nil {
		s.empty.SetText("Search failed. Use Retry; this is not an empty result.")
	} else if s.loading {
		s.empty.SetText("Loading workspace memory…")
	} else if strings.TrimSpace(s.Query()) != "" {
		s.empty.SetText(fmt.Sprintf("No %s match %q", s.mode, s.Query()))
	} else if s.mode == ModeEpisodes {
		s.empty.SetText("No episodes yet. Failed, unmeasured and cancelled attempts remain distinct.")
	} else {
		s.empty.SetText("No knowledge yet. Browsing remains available with extraction off.")
	}
}

func (s *Screen) Children() []ui.Component {
	kids := []ui.Component{s.status}
	switch s.page {
	case pageBrowse:
		kids = append(kids, s.tabs, s.search)
		if len(s.list.list.Items()) == 0 {
			kids = append(kids, s.empty)
		} else {
			kids = append(kids, s.list, s.scrollbar)
		}
	case pageCorrect:
		kids = append(kids, s.title, s.form, s.formScroll)
	case pageMenu, pageBrowseActions:
		kids = append(kids, s.title, s.actionList)
		return kids
	default:
		kids = append(kids, s.title, s.reader)
	}
	if s.page == pageBrowse && s.browseOverflow {
		kids = append(kids, s.browseMore)
	} else if s.compactActions {
		kids = append(kids, s.actionList)
	} else {
		kids = append(kids, s.actions)
	}
	return kids
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.title.SetTokens(tokens)
	s.tabs.SetTokens(tokens)
	s.search.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.empty.SetTokens(tokens)
	s.status.SetTokens(tokens)
	s.reader.area.SetTokens(tokens)
	s.reader.area.SetBackgroundColor(tokens.Color(ui.ColorBg))
	s.actions.SetTokens(tokens)
	s.actionList.SetTokens(tokens)
	s.browseMore.SetTokens(tokens)
	s.browseMore.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	if s.form != nil {
		s.form.SetTokens(tokens)
		s.formScroll.SetTokens(tokens)
	}
	if s.search != nil && s.search.field != nil {
		s.search.field.SetPrompt(ui.SearchGlyphEnd(tokens))
		s.search.field.SetPromptPlacement(ui.PromptEnd)
	}
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	width, height := max(0, r.W), max(0, r.H)
	headerW := max(0, width-s.dockHeaderInset)
	actionWidth := s.actions.Measure(ctx, ui.Loose(1000, 2)).W
	canShare := s.page == pageBrowse && height >= ui.StripHeight+2
	fullActions := canShare && width >= actionWidth+14
	moreWidth := s.browseMore.Measure(ctx, ui.Loose(1000, 1)).W
	s.browseOverflow = canShare && !fullActions && width >= moreWidth+6
	shared := fullActions || s.browseOverflow
	if s.browseOverflow {
		actionWidth = moreWidth
	}
	s.actions.SetShowRule(!shared)
	s.actions.SetAlignEnd(shared)
	s.actions.SetRuleBleed(0, width)
	s.compactActions = !shared && (actionWidth > width || height < s.actions.PreferredHeight())
	actionHeight := s.actions.PreferredHeight()
	if s.compactActions {
		actionHeight = len(s.actionSpecs)
	}
	if shared {
		actionHeight = 0
	}
	actionHeight = min(actionHeight, height)
	if s.dockHeaderInset > 0 {
		actionHeight = min(actionHeight, max(0, height-1))
	}
	footerY := height - actionHeight
	if shared {
		searchW := width - actionWidth - 2
		actions := ui.Rect{X: searchW + 2, Y: ui.StripHeight, W: actionWidth, H: 1}
		if s.browseOverflow {
			s.browseMore.Layout(ctx, actions)
			s.actions.Layout(ctx, ui.Rect{})
		} else {
			s.browseMore.Layout(ctx, ui.Rect{})
			s.actions.Layout(ctx, actions)
		}
		s.actionList.Layout(ctx, ui.Rect{})
	} else {
		s.browseMore.Layout(ctx, ui.Rect{})
		s.actions.Layout(ctx, ui.Rect{Y: footerY, W: width, H: actionHeight})
		s.actionList.Layout(ctx, ui.Rect{Y: footerY, W: width, H: actionHeight})
	}
	layoutRow := func(component ui.Component, top, rows int) {
		component.Layout(ctx, ui.Rect{Y: min(top, footerY), W: width, H: min(rows, max(0, footerY-top))})
	}
	if s.page == pageBrowse {
		s.tabs.SetRuleFullBleed(true)
		s.tabs.SetRuleBleed(0, width)
		s.tabs.Layout(ctx, ui.Rect{W: headerW, H: min(ui.StripHeight, footerY)})
		if s.tabs.Bounds().H < ui.StripHeight {
			s.tabs.Layout(ctx, ui.Rect{})
		}
		if shared {
			s.search.Layout(ctx, ui.Rect{Y: ui.StripHeight, W: width - actionWidth - 2, H: 1})
		} else {
			layoutRow(s.search, ui.StripHeight, 1)
		}
		layoutRow(s.status, ui.StripHeight+1, 1)
		bodyY := min(ui.StripHeight+2, footerY)
		bodyH := max(0, footerY-bodyY)
		barW := 0
		if s.scrollbar.NeededForViewport(bodyH) {
			barW = min(ui.ScrollbarWidth, width)
		}
		s.list.Layout(ctx, ui.Rect{Y: bodyY, W: max(0, width-barW), H: bodyH})
		s.scrollbar.Layout(ctx, ui.Rect{X: width - barW, Y: bodyY, W: barW, H: bodyH})
		s.empty.Layout(ctx, ui.Rect{Y: bodyY, W: width, H: bodyH})
		return
	}
	if s.page == pageMenu || s.page == pageBrowseActions {
		s.title.Layout(ctx, ui.Rect{W: headerW, H: min(1, height)})
		s.status.Layout(ctx, ui.Rect{Y: min(1, height), W: width, H: min(1, max(0, height-1))})
		s.actionList.Layout(ctx, ui.Rect{Y: min(2, height), W: width, H: max(0, height-2)})
		return
	}
	s.title.Layout(ctx, ui.Rect{W: headerW, H: min(1, footerY)})
	layoutRow(s.status, 1, 1)
	bodyY := min(2, footerY)
	body := ui.Rect{Y: bodyY, W: width, H: max(0, footerY-bodyY)}
	if s.page == pageCorrect {
		barW := 0
		if s.formScroll.NeededForViewport(body.H) {
			barW = min(ui.ScrollbarWidth, width)
		}
		body.W -= barW
		s.form.Layout(ctx, body)
		s.formScroll.Layout(ctx, ui.Rect{X: body.W, Y: bodyY, W: barW, H: body.H})
	} else {
		s.reader.Layout(ctx, body)
	}
}

func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	ui.FillRect(buf, originX, originY, s.Bounds(), cell.BlankCell(s.tokens.Color(ui.ColorBg)))
}

func (s *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	if evt.Key.Code == tea.KeyEsc {
		return s.back()
	}
	if evt.Key.Code == tea.KeyTab {
		return s.focusResult(s.preferredBody())
	}
	if s.page == pageBrowse {
		result := s.list.Handle(ui.EventContext{}, evt)
		if result.Action != nil {
			response := s.HandleAction(ui.EventContext{}, *result.Action)
			result = result.Merge(response)
			result.Action = response.Action
		}
		return result
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "tabs.activate":
		mode, _ := action.Payload.(string)
		if mode != s.mode && (mode == ModeKnowledge || mode == ModeEpisodes) {
			s.mode = mode
			s.records, s.episodes = nil, nil
			s.tabs.SetItems([]ui.TabItem{{ID: ModeKnowledge, Label: "Knowledge"}, {ID: ModeEpisodes, Label: "Episodes"}}, mode)
			s.search.field.SetPlaceholder("Search workspace " + mode)
			s.syncList()
			return s.commandResult(s.Refresh())
		}
		return ui.EventResult{Handled: true}
	case "tabs.enter":
		return s.focusResult(SearchID)
	case "memorybrowser.activate", "memorybrowser.inspect":
		return s.inspectSelected()
	case "memorybrowser.action":
		item, ok := s.actionList.Selected()
		if ok {
			return s.HandleAction(ui.EventContext{}, ui.Action{ID: item.ID})
		}
		return ui.EventResult{Handled: true}
	case "memorybrowser.retry":
		return s.commandResult(s.Refresh())
	case "memorybrowser.reload":
		return s.commandResult(s.ReloadDetail())
	case "memorybrowser.hide":
		msg := CancelMsg{Target: s, Epoch: s.epoch}
		return s.commandResult(func() tea.Msg { return msg })
	case "memorybrowser.back", "form.cancel":
		return s.back()
	case "memorybrowser.menu":
		s.showPage(pageMenu)
		return s.focusResult(ActionListID)
	case "memorybrowser.browse-actions":
		s.showPage(pageBrowseActions)
		return s.focusResult(ActionListID)
	case "memorybrowser.copy":
		return s.copyDetail()
	case "memorybrowser.pin":
		if !s.canMutate() {
			return ui.EventResult{Handled: true}
		}
		record := cloneRecord(s.detail.Record)
		return s.mutation(MutationPin, record, !record.Pinned)
	case "memorybrowser.correct":
		return s.beginCorrection()
	case "memorybrowser.retire":
		return s.beginRetire()
	case "memorybrowser.save":
		return s.saveCorrection()
	case "memorybrowser.confirm-retire":
		if s.retirePreview != nil {
			return s.mutation(MutationRetire, *s.retirePreview, false)
		}
		return ui.EventResult{Handled: true}
	case "form.exit.forward", "form.exit.backward":
		return s.focusResult(s.actionFocus())
	case "actionbar.exit.forward", "actionbar.exit.backward":
		return s.focusResult(s.preferredBody())
	case "form.changed":
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace memory browser", Value: s.mode}
}

func (s *Screen) SetClipboardCopy(fn func(string) tea.Cmd) {
	if fn == nil {
		fn = rendering.CopyToClipboardCmd
	}
	s.clipboardCopy = fn
	s.reader.area.SetClipboardCopy(fn)
	s.search.field.TextEditor().(*ui.TextField).SetClipboardCopy(fn)
	s.wireCorrectionClipboard()
}

func (s *Screen) commandResult(cmd tea.Cmd) ui.EventResult {
	return ui.EventResult{Handled: true, Cmd: cmd, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *Screen) inspectSelected() ui.EventResult {
	item, ok := s.list.list.Selected()
	if !ok {
		return ui.EventResult{Handled: true}
	}
	s.detail = DetailData{}
	s.mutationError = nil
	switch value := item.Payload.(type) {
	case memory.ScoredRecord:
		s.detail.Record = cloneRecord(value.Record)
	case memory.Episode:
		s.detail.Episode = value
	}
	s.showPage(pageDetail)
	s.setReader(detailText(s.detail))
	return s.commandResult(s.ReloadDetail()).Merge(s.focusResult(DetailID))
}

func (s *Screen) ReloadDetail() tea.Cmd {
	if !s.open || s.detail.ID() == "" || s.draft != nil || s.retirePreview != nil {
		return nil
	}
	s.detailSequence++
	s.detailLoading, s.detailError = true, nil
	s.syncStatus()
	s.syncActions()
	msg := DetailMsg{Target: s, Epoch: s.epoch, Sequence: s.detailSequence, ID: s.detail.ID(), Mode: s.mode, Episode: s.detail.Episode}
	return func() tea.Msg { return msg }
}

func (s *Screen) beginCorrection() ui.EventResult {
	if !s.canMutate() {
		return ui.EventResult{Handled: true}
	}
	record := cloneRecord(s.detail.Record)
	s.draft = &record
	s.formModel = ui.NewFormModel()
	for _, field := range []*ui.FieldState{
		{ID: "title", Label: "Title", Type: ui.FieldText, Value: ui.StringValue(record.Title), Required: true},
		{ID: "content", Label: "Content", Type: ui.FieldTextArea, Rows: 4, Value: ui.StringValue(record.Content), Required: true},
		{ID: "applies_when", Label: "Applies when (blank keeps existing)", Type: ui.FieldTextArea, Rows: 2, Value: ui.StringValue(record.AppliesWhen)},
		{ID: "paths", Label: "Scope paths (one per line)", Type: ui.FieldTextArea, Rows: 2, Value: ui.StringValue(strings.Join(record.ScopePaths, "\n"))},
		{ID: "evidence", Label: "Evidence (retained)", Type: ui.FieldTextArea, Rows: 3, ReadOnly: true, Value: ui.StringValue(evidenceText(record.Evidence))},
	} {
		field.Initial = field.Value
		s.formModel.AddField(field)
	}
	s.form = ui.NewDockedForm(FormID, s.formModel, "", "", s.tokens)
	s.form.SetCompact(false)
	s.form.SetFieldGap(0)
	s.wireCorrectionClipboard()
	s.formScroll = ui.NewScrollbarControl(FormScrollID, s.form.Scroll(), s.tokens)
	s.formScroll.SetSurfaceBackground(ui.ColorBg)
	s.mutationError = nil
	s.showPage(pageCorrect)
	return s.focusResult(FormID)
}

func (s *Screen) Draft() (memory.Record, bool) {
	if s.draft == nil {
		return memory.Record{}, false
	}
	record := cloneRecord(*s.draft)
	record.Title = s.formModel.Field("title").Value.String()
	record.Content = s.formModel.Field("content").Value.String()
	record.AppliesWhen = s.formModel.Field("applies_when").Value.String()
	record.ScopePaths = []string{}
	for _, path := range strings.Split(s.formModel.Field("paths").Value.String(), "\n") {
		if path = strings.TrimSpace(path); path != "" {
			record.ScopePaths = append(record.ScopePaths, path)
		}
	}
	return record, true
}

func (s *Screen) saveCorrection() ui.EventResult {
	record, ok := s.Draft()
	if !ok || s.mutating {
		return ui.EventResult{Handled: true}
	}
	s.formModel.ValidateAll()
	if s.formModel.AnyErrors() || strings.TrimSpace(record.Content) == "" || strings.TrimSpace(record.Title) == "" {
		s.mutationError = errors.New("title and content are required")
		s.syncStatus()
		return s.focusResult(FormID)
	}
	return s.mutation(MutationCorrect, record, record.Pinned)
}

func (s *Screen) beginRetire() ui.EventResult {
	if !s.canMutate() {
		return ui.EventResult{Handled: true}
	}
	record := cloneRecord(s.detail.Record)
	s.retirePreview = &record
	s.mutationError = nil
	s.showPage(pageRetire)
	s.setReader(fmt.Sprintf("Retire %s at revision %d?\n\n%s\n\nThe exact revision is checked before saving. Retired knowledge is excluded from recall; immutable history stays available. Cancel makes no change.", record.Title, record.Revision, detailText(DetailData{Record: record})))
	s.actions.FocusFirst()
	s.actionList.SelectIndex(0)
	return s.focusResult(s.actionFocus())
}

func (s *Screen) canMutate() bool {
	return s.mode == ModeKnowledge && s.detail.Record.ID != "" && s.detail.Record.Revision > 0 && s.detail.Record.Status != memory.StatusSuperseded && !s.detailLoading && s.detailError == nil && !s.mutating
}

func (s *Screen) mutation(kind string, record memory.Record, pinned bool) ui.EventResult {
	if s.mutating || record.ID == "" || record.Revision < 1 || record.Status == memory.StatusSuperseded {
		return ui.EventResult{Handled: true}
	}
	s.mutationSequence++
	s.mutating, s.mutationError = true, nil
	s.freezeCorrection(true)
	s.syncActions()
	s.syncStatus()
	msg := MutationMsg{Target: s, Epoch: s.epoch, Sequence: s.mutationSequence, Kind: kind, ID: record.ID, ExpectedRevision: record.Revision, Pinned: pinned, Replacement: cloneRecord(record)}
	s.mutationIntent = msg
	return s.commandResult(func() tea.Msg { return msg })
}

func (s *Screen) back() ui.EventResult {
	if s.mutating {
		return ui.EventResult{Handled: true}
	}
	if s.page == pageBrowse {
		msg := CancelMsg{Target: s, Epoch: s.epoch}
		return s.commandResult(func() tea.Msg { return msg })
	}
	if s.page == pageBrowseActions {
		s.showPage(pageBrowse)
		return s.focusResult(SearchID)
	}
	if s.page == pageDetail {
		s.detailSequence++
		s.detailLoading = false
		s.showPage(pageBrowse)
		return s.focusResult(ListID)
	}
	s.draft, s.retirePreview, s.mutationError = nil, nil, nil
	s.showPage(pageDetail)
	s.setReader(detailText(s.detail))
	return s.focusResult(DetailID)
}

func (s *Screen) showPage(page string) {
	s.page = page
	label := page
	if page == pageBrowseActions {
		label = "Actions"
	}
	s.title.SetText("Workspace Memory · " + label)
	s.syncActions()
	s.syncStatus()
}

func (s *Screen) syncActions() {
	button := func(label, action string, enabled bool) ui.ActionBarButton {
		return ui.ActionBarButton{ID: ui.NewID(ActionsID, action), Label: label, Action: "memorybrowser." + action, Variant: ui.VariantSecondary, Enabled: enabled}
	}
	switch s.page {
	case pageBrowse, pageBrowseActions:
		s.actionSpecs = []ui.ActionBarButton{button("Inspect", "inspect", len(s.list.list.Items()) > 0 && !s.loading && s.searchError == nil), button("Retry", "retry", true), button("Hide", "hide", true)}
		if s.page == pageBrowseActions {
			s.actionSpecs = append(s.actionSpecs, button("Back", "back", true))
		}
	case pageCorrect:
		s.actionSpecs = []ui.ActionBarButton{button("Cancel", "back", !s.mutating), button("Copy draft", "copy", true), button("Save", "save", !s.mutating)}
	case pageRetire:
		s.actionSpecs = []ui.ActionBarButton{button("Cancel", "back", !s.mutating), button("Retire", "confirm-retire", !s.mutating)}
		s.actionSpecs[1].Variant = ui.VariantDanger
	case pageMenu:
		s.actionSpecs = []ui.ActionBarButton{button("Back", "back", true), button("Copy full detail", "copy", true), button("Reload detail", "reload", !s.detailLoading)}
		if s.mode == ModeKnowledge {
			pin := "Pin"
			if s.detail.Record.Pinned {
				pin = "Unpin"
			}
			s.actionSpecs = append(s.actionSpecs, button(pin, "pin", s.canMutate()), button("Correct", "correct", s.canMutate()), button("Retire", "retire", s.canMutate()))
		}
	default:
		s.actionSpecs = []ui.ActionBarButton{button("Back", "back", !s.mutating), button("Copy", "copy", true), button("Actions", "menu", !s.mutating)}
	}
	s.actions.SetButtons(s.actionSpecs...)
	items := make([]ui.ListItem, 0, len(s.actionSpecs))
	for _, spec := range s.actionSpecs {
		items = append(items, ui.ListItem{ID: spec.Action, Label: spec.Label, Disabled: !spec.Enabled})
	}
	s.actionList.SetItemsStable(items)
}

func (s *Screen) actionFocus() ui.ComponentID {
	if s.page == pageBrowse && s.browseOverflow {
		return BrowseMoreID
	}
	bounds := s.Bounds()
	compact := s.actions.Measure(ui.LayoutContext{}, ui.Loose(1000, 2)).W > bounds.W || bounds.H < s.actions.PreferredHeight()
	if compact || s.page == pageMenu || s.page == pageBrowseActions {
		return ActionListID
	}
	return ActionsID
}

func (s *Screen) preferredBody() ui.ComponentID {
	switch s.page {
	case pageBrowse:
		return SearchID
	case pageCorrect:
		return FormID
	case pageMenu, pageBrowseActions:
		return ActionListID
	default:
		return DetailID
	}
}

func (s *Screen) focusResult(id ui.ComponentID) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
}

func (s *Screen) PreferredFocus() ui.ComponentID { return s.preferredBody() }

func (s *Screen) wireCorrectionClipboard() {
	if s.form == nil || s.formModel == nil {
		return
	}
	for _, state := range s.formModel.Fields() {
		field := s.form.Field(state.ID)
		if control := field.Control(); control != nil {
			control.SetClipboardCopy(s.clipboardCopy)
		}
		if area := field.Area(); area != nil {
			area.SetClipboardCopy(s.clipboardCopy)
		}
	}
}

func (s *Screen) freezeCorrection(frozen bool) {
	if s.formModel == nil || s.form == nil {
		return
	}
	for _, state := range s.formModel.Fields() {
		state.ReadOnly = frozen || state.ID == "evidence"
		s.form.Field(state.ID).Sync()
	}
}

func (s *Screen) setReader(text string) {
	s.reader.area.SetValue(text)
	s.reader.area.SetCursorFlat(0)
}

func (s *Screen) copyDetail() ui.EventResult {
	text := s.reader.area.Value()
	if draft, ok := s.Draft(); ok {
		payload, err := json.MarshalIndent(draft, "", "  ")
		if err == nil {
			text = string(payload)
		}
	}
	return s.commandResult(s.clipboardCopy(text))
}

type browserList struct {
	ui.Base
	list  *ui.List
	owner *Screen
}

func (p *browserList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return p.list.Measure(ctx, cs)
}
func (p *browserList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (p *browserList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	p.list.Paint(buf, ox+p.Bounds().X, oy+p.Bounds().Y, ctx)
}
func (p *browserList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if p.owner != nil && evt.IsKey() {
		key := evt.Key
		switch {
		case key.Code == tea.KeyEsc:
			return p.owner.back()
		case key.Code == tea.KeyTab:
			return p.owner.focusResult(p.owner.actionFocus())
		case key.String() == "/":
			p.owner.FocusSearch()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint | ui.InvalidateFocus}
		case key.String() == "k":
			return p.list.Handle(ctx, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyUp}))
		case key.String() == "j":
			return p.list.Handle(ctx, ui.KeyEvent(tea.KeyPressMsg{Code: tea.KeyDown}))
		}
	}
	result := p.list.Handle(ctx, evt)
	result.Invalidate |= ui.InvalidatePaint
	return result
}
func (p *browserList) Semantics() ui.Semantics       { return p.list.Semantics() }
func (p *browserList) SetTokens(tokens *ui.TokenSet) { p.list.SetTokens(tokens) }

type searchField struct {
	ui.Base
	field *ui.FilterField
	owner *Screen
}

func (f *searchField) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return f.field.Measure(ctx, cs)
}
func (f *searchField) Layout(ctx ui.LayoutContext, r ui.Rect) {
	f.SetBounds(r)
	prompt := ui.SearchGlyphEnd(f.field.Tokens())
	if rendering.VisibleWidth(prompt) > r.W {
		prompt = ""
	}
	f.field.SetPrompt(prompt)
	f.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (f *searchField) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	f.field.Paint(buf, ox+f.Bounds().X, oy+f.Bounds().Y, ctx)
}
func (f *searchField) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		switch evt.Key.Code {
		case tea.KeyEsc:
			if f.field.Value() == "" {
				return f.owner.back()
			}
		case tea.KeyDown, tea.KeyEnter:
			return f.owner.focusResult(ListID)
		case tea.KeyTab:
			if evt.Key.Mod.Contains(tea.ModShift) {
				return f.owner.focusResult(TabsID)
			}
			return f.owner.focusResult(ListID)
		}
	}
	before := f.field.Value()
	result := f.field.Handle(ctx, evt)
	if before != f.field.Value() {
		result = result.Merge(f.owner.commandResult(f.owner.Refresh()))
	}
	return result
}
func (f *searchField) Semantics() ui.Semantics            { return f.field.Semantics() }
func (f *searchField) SetTokens(tokens *ui.TokenSet)      { f.field.SetTokens(tokens) }
func (f *searchField) TextEditor() ui.TextClipboardTarget { return f.field.TextEditor() }

type readingArea struct {
	ui.Base
	area  *ui.TextArea
	owner *Screen
}

func (r *readingArea) Measure(ctx ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return r.area.Measure(ctx, constraints)
}
func (r *readingArea) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	r.SetBounds(bounds)
	local := ui.Rect{W: bounds.W, H: bounds.H}
	if r.area.Bounds() != local {
		r.area.Layout(ctx, local)
	}
}
func (r *readingArea) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	r.area.Paint(buf, originX+r.Bounds().X, originY+r.Bounds().Y, ctx)
}
func (r *readingArea) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if evt.IsKey() {
		if evt.Key.Code == tea.KeyTab {
			return r.owner.focusResult(r.owner.actionFocus())
		}
		if evt.Key.Code == tea.KeyEsc {
			if result, handled := r.area.HandleClipboardKey(evt.Key); handled {
				return result
			}
			return r.owner.back()
		}
	}
	return r.area.Handle(ctx, evt)
}
func (r *readingArea) Semantics() ui.Semantics            { return r.area.Semantics() }
func (r *readingArea) TextEditor() ui.TextClipboardTarget { return r.area }
func (r *readingArea) CancelPointerCapture()              { r.area.CancelPointerCapture() }

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
