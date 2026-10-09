// Package findinfiles provides the native Find in Files surface body.
package findinfiles

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/filetree"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const (
	ScreenID         ui.ComponentID = "find-in-files"
	QueryID          ui.ComponentID = "find-in-files/query"
	ReplaceID        ui.ComponentID = "find-in-files/replace"
	CaseID           ui.ComponentID = "find-in-files/case-sensitive"
	RegexID          ui.ComponentID = "find-in-files/regex"
	StatusID         ui.ComponentID = "find-in-files/status"
	ListID           ui.ComponentID = "find-in-files/list"
	ScrollbarID      ui.ComponentID = "find-in-files/scrollbar"
	PreviewID        ui.ComponentID = "find-in-files/preview"
	ActionsID        ui.ComponentID = "find-in-files/actions"
	ReplaceActionsID ui.ComponentID = "find-in-files/replace-actions"
	DividerID        ui.ComponentID = "find-in-files/divider"
)

// Mode selects Find in Files vs its Replace in Files sibling.
type Mode int

const (
	ModeFind Mode = iota
	ModeReplace
)

func (m Mode) Title() string {
	if m == ModeReplace {
		return "Replace in Files"
	}
	return "Find in Files"
}

type SearchRequestMsg struct {
	Screen     *Screen
	Generation int
	Query      string
	Options    fileio.ProjectSearchOptions
}

type SearchCancelMsg struct {
	Screen *Screen
}

type OpenMsg struct {
	Screen     *Screen
	Generation int
	Match      fileio.ProjectSearchMatch
}

type CloseMsg struct {
	Screen *Screen
}

type ReplaceMsg struct {
	Screen      *Screen
	Match       fileio.ProjectSearchMatch
	All         bool
	Query       string
	Replacement string
	Options     fileio.ProjectSearchOptions
}

type Screen struct {
	ui.Base
	th             *theme.Theme
	tokens         *ui.TokenSet
	root           string
	mode           Mode
	queryField     *findQuery
	replaceField   *replaceQuery
	caseToggle     *ui.ToggleButton
	regexToggle    *ui.ToggleButton
	status         *ui.Label
	list           *findList
	scrollbar      *ui.ScrollbarControl
	preview        *previewPane
	actions        *ui.ActionBar
	replaceActions *ui.ActionBar
	split          *ui.SplitModel
	divider        *ui.SplitDivider
	focus          func(ui.ComponentID)
	icons          *filetree.IconResolver

	results       []fileio.ProjectSearchMatch
	groups        []resultGroup
	rows          []displayRow
	collapsed     map[string]bool
	selected      int
	selRow        int
	histIdx       int
	histDraft     string
	loading       bool
	errText       string
	notice        string
	duration      time.Duration
	generation    int
	previewSrc    *editor.Editor
	caseSensitive bool
	regex         bool
	resultH       int
	listY0        int
	previewSurf   *surface.Surface
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	s := &Screen{
		Base:      ui.NewBase(ScreenID),
		th:        th,
		tokens:    tokens,
		selected:  -1,
		selRow:    -1,
		histIdx:   -1,
		collapsed: make(map[string]bool),
		icons:     filetree.NewIconResolver(),
	}
	s.icons.ApplyTheme(th)

	field := ui.NewSearchField(QueryID, tokens)
	field.SetPrompt(ui.SearchGlyph(tokens))
	field.SetPromptPlacement(ui.PromptStart)
	field.SetPlaceholder("Search file names and contents")
	field.SetSubmitAction("")
	s.queryField = &findQuery{Base: ui.NewBase(QueryID), field: field, owner: s}

	replace := ui.NewTextField(ReplaceID, tokens)
	replace.SetPlaceholder("Replace with")
	replace.SetSubmitAction("find.replace.one")
	s.replaceField = &replaceQuery{Base: ui.NewBase(ReplaceID), field: replace, owner: s}

	s.caseToggle = ui.NewToggleButton(CaseID, "Cc", "find.toggle-case", tokens)
	s.regexToggle = ui.NewToggleButton(RegexID, ".*", "find.toggle-regex", tokens)
	s.caseToggle.SetCancelAction("find.close")
	s.regexToggle.SetCancelAction("find.close")

	s.status = ui.NewLabel(StatusID, "", th)
	s.status.SetColorRole(ui.ColorMuted)

	list := ui.NewVirtualList(ListID, tokens)
	list.SetActivateAction("find.open")
	list.SetRowPainter(s.paintResultRow)
	s.list = &findList{Base: ui.NewBase(ListID), list: list, owner: s, lastClick: -1}
	s.scrollbar = ui.NewScrollbarControl(ScrollbarID, list.Scroll(), tokens)
	s.scrollbar.SetSurfaceBackground(ui.ColorPopupBg)

	s.preview = &previewPane{Base: ui.NewBase(PreviewID), owner: s}
	s.split = ui.NewSplitModel(ui.Vertical)
	s.split.SetFraction(0.42)
	s.split.SetMinSizes(4, 3)
	s.divider = ui.NewSplitDivider(DividerID, s.split)
	s.divider.SetTokens(tokens)
	s.divider.SetColorRole(ui.ColorPopupDivider)
	s.divider.SetTransparent(true)
	s.rebuildActions()
	s.rebuildReplaceActions()
	return s
}

func (s *Screen) Root() string          { return s.root }
func (s *Screen) Mode() Mode            { return s.mode }
func (s *Screen) Query() string         { return s.queryField.field.Value() }
func (s *Screen) SetQuery(value string) { s.queryField.field.SetValue(value) }
func (s *Screen) Replacement() string {
	if s.replaceField == nil {
		return ""
	}
	return s.replaceField.field.Value()
}
func (s *Screen) SetReplacement(value string) {
	if s.replaceField != nil {
		s.replaceField.field.SetValue(value)
	}
}
func (s *Screen) Results() []fileio.ProjectSearchMatch {
	return s.results
}
func (s *Screen) Selected() int       { return s.selected }
func (s *Screen) Generation() int     { return s.generation }
func (s *Screen) CaseSensitive() bool { return s.caseSensitive }
func (s *Screen) Regex() bool         { return s.regex }

// SetSearchDebounce configures the shared query-field debounce interval.
func (s *Screen) SetSearchDebounce(delay time.Duration) { s.queryField.field.SetDebounce(delay) }

func (s *Screen) AcceptsSearch(screen *Screen, generation int) bool {
	return screen == s && generation == s.generation && strings.TrimSpace(s.Query()) != ""
}

func (s *Screen) SetOptions(caseSensitive, regex bool) {
	s.caseSensitive = caseSensitive
	s.regex = regex
	s.caseToggle.SetChecked(caseSensitive)
	s.regexToggle.SetChecked(regex)
}

func (s *Screen) Open(root string) {
	s.open(root, ModeFind)
}

func (s *Screen) OpenReplace(root string) {
	s.open(root, ModeReplace)
}

func (s *Screen) open(root string, mode Mode) {
	s.root = root
	s.mode = mode
	s.queryField.field.Cancel()
	s.queryField.field.SetValue("")
	s.SetReplacement("")
	s.caseSensitive = false
	s.regex = false
	s.caseToggle.SetChecked(false)
	s.regexToggle.SetChecked(false)
	s.setResults(nil)
	s.histIdx = -1
	s.histDraft = ""
	s.loading = false
	s.errText = ""
	s.notice = ""
	s.generation = 0
	s.duration = 0
	if mode == ModeReplace {
		s.queryField.field.SetPlaceholder("Find in files")
	} else {
		s.queryField.field.SetPlaceholder("Search file names and contents")
	}
	s.rebuildActions()
	s.rebuildReplaceActions()
	s.sync()
}

func (s *Screen) Close() {
	s.generation++
	s.loading = false
	s.queryField.field.Cancel()
	queryHistory.Add(s.Query())
}

func (s *Screen) SetFocusHandler(fn func(ui.ComponentID)) { s.focus = fn }
func (s *Screen) FocusQuery()                             { s.focusID(QueryID) }
func (s *Screen) FocusReplace()                           { s.focusID(ReplaceID) }
func (s *Screen) focusID(id ui.ComponentID) {
	if s.focus != nil {
		s.focus(id)
	}
}

func (s *Screen) ApplySearchResults(generation int, results []fileio.ProjectSearchMatch, err error, duration time.Duration) {
	if generation != s.generation {
		return
	}
	s.loading = false
	s.duration = duration
	s.notice = ""
	if err != nil {
		s.errText = err.Error()
		s.setResults(nil)
		s.sync()
		return
	}
	s.errText = ""
	s.setResults(results)
	s.list.lastClick = -1
	s.list.lastClickTime = time.Time{}
	s.list.list.Scroll().ScrollTo(0)
	if len(s.results) == 0 {
		s.sync()
		return
	}
	s.sync()
	s.rebuildPreview()
}

// ScheduleSearch reschedules the current query after a non-text state change,
// such as a search-option toggle.
func (s *Screen) ScheduleSearch() tea.Cmd {
	if cancel := s.queryChanged(); cancel != nil {
		return cancel
	}
	return s.queryField.field.Schedule()
}

func (s *Screen) SetNotice(text string) {
	s.notice = text
	s.syncStatus()
}

func (s *Screen) searchOptions() fileio.ProjectSearchOptions {
	return fileio.ProjectSearchOptions{
		CaseSensitive: s.caseSensitive,
		Regex:         s.regex,
	}
}

func (s *Screen) queryChanged() tea.Cmd {
	s.generation++
	s.notice = ""
	s.loading = strings.TrimSpace(s.Query()) != ""
	s.errText = ""
	// Rows belong to the query that produced them. Never activate or replace
	// an old result while a new query is still being debounced/searched.
	s.setResults(nil)
	s.sync()
	if strings.TrimSpace(s.Query()) == "" {
		s.setResults(nil)
		s.loading = false
		s.sync()
		return func() tea.Msg { return SearchCancelMsg{Screen: s} }
	}
	s.syncStatus()
	return nil
}

// HandleSearchDebounced converts an accepted shared SearchField notification
// into this screen's owner-tagged domain-search intent.
func (s *Screen) HandleSearchDebounced(msg ui.SearchDebouncedMsg) tea.Cmd {
	if !s.queryField.field.AcceptsDebounce(msg) || !s.AcceptsSearch(s, s.generation) {
		return nil
	}
	return func() tea.Msg {
		return SearchRequestMsg{
			Screen:     s,
			Generation: s.generation,
			Query:      s.Query(),
			Options:    s.searchOptions(),
		}
	}
}

func (s *Screen) rebuildPreview() {
	if s.selected < 0 || s.selected >= len(s.results) || s.results[s.selected].NameOnly {
		s.previewSrc = nil
		return
	}
	result := s.results[s.selected]
	if result.Context == "" {
		s.previewSrc = nil
		return
	}
	preview := editor.NewPreview(s.th, result.Path, result.Context, result.ContextStart)
	localLine := result.Line - result.ContextStart
	if localLine < 0 {
		localLine = 0
	}
	startCol := editor.RuneIndexToVisualColumn(result.LineText, result.Column)
	endCol := editor.RuneIndexToVisualColumn(result.LineText, max(result.EndColumn, result.Column+1))
	preview.SetCursorPosition(localLine, startCol)
	preview.SetLandingRange(localLine, startCol, endCol)
	preview.ScrollToCursor()
	s.applyPreviewMatches(preview, result)
	s.previewSrc = preview
}

// applyPreviewMatches highlights EVERY match of the same file that falls
// inside the preview excerpt through the editor's search-highlight path, so
// the pane shows all hits on the previewed lines rather than only the
// selected one. The selected match stays strongest via the landing cue.
func (s *Screen) applyPreviewMatches(preview *editor.Editor, selected fileio.ProjectSearchMatch) {
	lineCount := strings.Count(selected.Context, "\n") + 1
	st := preview.Search()
	st.Matches = nil
	st.CurrentIdx = -1
	for i, r := range s.results {
		if r.Path != selected.Path || r.NameOnly {
			continue
		}
		local := r.Line - selected.ContextStart
		if local < 0 || local >= lineCount {
			continue
		}
		sc := editor.RuneIndexToVisualColumn(r.LineText, r.Column)
		ec := editor.RuneIndexToVisualColumn(r.LineText, max(r.EndColumn, r.Column+1))
		if ec <= sc {
			ec = sc + 1
		}
		if i == s.selected {
			st.CurrentIdx = len(st.Matches)
		}
		st.Matches = append(st.Matches, editor.SearchMatch{
			Start: editor.Position{Line: local, Col: sc},
			End:   editor.Position{Line: local, Col: ec},
		})
	}
	st.Open = len(st.Matches) > 0
}

func (s *Screen) sync() {
	s.syncList()
	s.syncStatus()
	s.syncActionEnabled()
}

func (s *Screen) syncList() {
	items := make([]ui.VirtItem, 0, len(s.rows))
	for i, row := range s.rows {
		if row.kind == rowHeader {
			g := s.groups[row.group]
			items = append(items, ui.VirtItem{
				ID:      fmt.Sprintf("hdr:%s", g.path),
				Height:  1,
				Label:   g.rel,
				Detail:  fmt.Sprintf("%d", len(g.rows)),
				Payload: row,
			})
			continue
		}
		result := s.results[row.result]
		label := result.RelativePath
		if !result.NameOnly {
			label += fmt.Sprintf(":%d", result.Line)
		}
		items = append(items, ui.VirtItem{
			ID:      fmt.Sprintf("match:%s-%d", result.Path, i),
			Height:  1,
			Label:   label,
			Detail:  strings.TrimRight(result.LineText, "\r\n"),
			Payload: row,
		})
	}
	s.list.list.SetItems(items)
	if s.selRow >= 0 && s.selRow < len(items) {
		s.list.list.SelectIndex(s.selRow)
	}
}

func (s *Screen) syncStatus() {
	if s.notice != "" {
		s.status.SetColorRole(ui.ColorAccent)
		s.status.SetText(s.notice)
		return
	}
	if s.loading {
		s.status.SetColorRole(ui.ColorAccent)
		s.status.SetText("Searching…")
		return
	}
	if s.errText != "" {
		s.status.SetColorRole(ui.ColorError)
		s.status.SetText("Invalid query")
		return
	}
	s.status.SetColorRole(ui.ColorMuted)
	if s.Query() == "" {
		s.status.SetText(filepath.Base(s.root))
		return
	}
	files := make(map[string]struct{})
	replaceable := 0
	for _, r := range s.results {
		files[r.Path] = struct{}{}
		if fileio.ReplaceableMatch(r) {
			replaceable++
		}
	}
	matchLabel, fileLabel := "matches", "files"
	if len(s.results) == 1 {
		matchLabel = "match"
	}
	if len(files) == 1 {
		fileLabel = "file"
	}
	text := fmt.Sprintf("%d %s in %d %s", len(s.results), matchLabel, len(files), fileLabel)
	if len(s.results) == 0 {
		text = "No matches · try a shorter name or an exact phrase"
	}
	if s.mode == ModeReplace {
		text = fmt.Sprintf("%d matches · %d replaceable · %d files", len(s.results), replaceable, len(files))
	}
	if s.duration > 0 {
		text += fmt.Sprintf(" · %dms", s.duration.Milliseconds())
	}
	s.status.SetText(text)
}

func (s *Screen) rebuildActions() {
	s.actions = ui.NewActionBar(ActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "open"), Label: "Open", Action: "find.open", Variant: ui.VariantPrimary, Enabled: s.selected >= 0},
		ui.ActionBarButton{ID: ui.NewID(ActionsID, "close"), Label: "Close", Action: "find.close", Variant: ui.VariantGhost, Enabled: true},
	)
	s.actions.SetCancelAction("find.close")
	s.actions.SetAlignEnd(true)
	s.actions.SetExitActions("actionbar.exit.forward", "actionbar.exit.backward")
}

func (s *Screen) rebuildReplaceActions() {
	s.replaceActions = ui.NewActionBar(ReplaceActionsID, s.tokens,
		ui.ActionBarButton{ID: ui.NewID(ReplaceActionsID, "one"), Label: "Replace", Action: "find.replace.one", Variant: ui.VariantPrimary, Enabled: s.canReplaceSelected()},
		ui.ActionBarButton{ID: ui.NewID(ReplaceActionsID, "all"), Label: "All", Action: "find.replace.all", Variant: ui.VariantSecondary, Enabled: s.replaceableCount() > 0},
	)
	s.replaceActions.SetAlignEnd(true)
	s.replaceActions.SetCancelAction("find.close")
	s.replaceActions.SetExitActions("find.focus.list", "find.focus.query")
}

func (s *Screen) syncActionEnabled() {
	if s.actions != nil {
		s.actions.SetEnabled("find.open", s.selected >= 0)
	}
	if s.replaceActions != nil {
		s.replaceActions.SetEnabled("find.replace.one", s.canReplaceSelected())
		s.replaceActions.SetEnabled("find.replace.all", s.replaceableCount() > 0)
	}
}

func (s *Screen) canReplaceSelected() bool {
	if s.mode != ModeReplace || s.selected < 0 || s.selected >= len(s.results) {
		return false
	}
	return fileio.ReplaceableMatch(s.results[s.selected])
}

func (s *Screen) replaceableCount() int {
	n := 0
	for _, r := range s.results {
		if fileio.ReplaceableMatch(r) {
			n++
		}
	}
	return n
}

func (s *Screen) openSelected() tea.Cmd {
	if s.selected < 0 || s.selected >= len(s.results) {
		return nil
	}
	queryHistory.Add(s.Query())
	match := s.results[s.selected]
	generation := s.generation
	return func() tea.Msg { return OpenMsg{Screen: s, Generation: generation, Match: match} }
}

func (s *Screen) replaceSelected() tea.Cmd {
	if !s.canReplaceSelected() {
		return nil
	}
	queryHistory.Add(s.Query())
	return func() tea.Msg {
		return ReplaceMsg{
			Screen:      s,
			Match:       s.results[s.selected],
			Query:       s.Query(),
			Replacement: s.Replacement(),
			Options:     s.searchOptions(),
		}
	}
}

func (s *Screen) replaceAll() tea.Cmd {
	if s.mode != ModeReplace || s.replaceableCount() == 0 {
		return nil
	}
	queryHistory.Add(s.Query())
	return func() tea.Msg {
		return ReplaceMsg{
			Screen:      s,
			All:         true,
			Query:       s.Query(),
			Replacement: s.Replacement(),
			Options:     s.searchOptions(),
		}
	}
}

func (s *Screen) Children() []ui.Component {
	kids := []ui.Component{s.queryField, s.caseToggle, s.regexToggle, s.status, s.list, s.scrollbar, s.divider, s.preview, s.actions}
	if s.mode == ModeReplace {
		kids = append([]ui.Component{s.replaceField, s.replaceActions}, kids...)
	}
	return kids
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	if th := tokens.Theme(); th != nil {
		s.th = th
		s.icons.ApplyTheme(th)
		if s.previewSrc != nil && s.previewSrc.Theme() != th {
			s.previewSrc.SetTheme(th)
		}
	}
	s.queryField.SetTokens(tokens)
	if s.replaceField != nil {
		s.replaceField.SetTokens(tokens)
	}
	s.caseToggle.SetTokens(tokens)
	s.regexToggle.SetTokens(tokens)
	s.status.SetTokens(tokens)
	s.list.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	if s.actions != nil {
		s.actions.SetTokens(tokens)
	}
	if s.replaceActions != nil {
		s.replaceActions.SetTokens(tokens)
	}
	if s.divider != nil {
		s.divider.SetTokens(tokens)
		s.divider.SetColorRole(ui.ColorPopupDivider)
	}
	if s.queryField != nil && s.queryField.field != nil {
		s.queryField.field.SetPrompt(ui.SearchGlyph(tokens))
		s.queryField.field.SetPromptPlacement(ui.PromptStart)
	}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	caseW := min(2, max(0, r.W))
	regexW := min(2, max(0, r.W-caseW))
	innerGap := 0
	if caseW > 0 && regexW > 0 {
		innerGap = 1
	}
	cluster := caseW + innerGap + regexW
	lead := 0
	if r.W >= cluster+10 {
		lead = 1
	}
	queryW := max(1, r.W-cluster-lead)
	s.queryField.Layout(ctx, ui.Rect{X: 0, Y: 0, W: queryW, H: 1})
	s.caseToggle.Layout(ctx, ui.Rect{X: queryW + lead, Y: 0, W: caseW, H: 1})
	s.regexToggle.Layout(ctx, ui.Rect{X: queryW + lead + caseW + innerGap, Y: 0, W: regexW, H: 1})

	row := 1
	if s.mode == ModeReplace && s.replaceField != nil && s.replaceActions != nil {
		replaceBtnW := s.replaceActions.Measure(ctx, ui.Loose(r.W, 1)).W
		if replaceBtnW >= r.W-8 {
			replaceBtnW = min(replaceBtnW, max(0, r.W/2))
		}
		fieldW := max(1, r.W-replaceBtnW-1)
		s.replaceField.Layout(ctx, ui.Rect{X: 0, Y: row, W: fieldW, H: 1})
		s.replaceActions.Layout(ctx, ui.Rect{X: fieldW + 1, Y: row, W: max(0, r.W-fieldW-1), H: 1})
		row++
	}
	s.status.Layout(ctx, ui.Rect{X: 0, Y: row, W: r.W, H: 1})
	row++

	s.listY0 = row
	available := max(0, r.H-s.listY0-1)
	if s.split != nil {
		s.split.SetMinSizes(4, 3)
		s.split.SetMaxFirst(max(4, available-3-1))
	}
	divH, previewH := 1, 0
	if s.split != nil {
		s.resultH, divH, previewH = s.split.Resolve(available)
	} else {
		s.resultH = max(1, available*42/100)
		previewH = max(0, available-s.resultH-1)
	}
	if s.resultH < 1 {
		s.resultH = min(4, max(1, available))
	}
	previewStart := s.listY0 + s.resultH + divH
	barW := 0
	if s.scrollbar.NeededForViewport(s.resultH) {
		barW = min(ui.ScrollbarWidth, r.W)
	}
	s.list.Layout(ctx, ui.Rect{X: 0, Y: s.listY0, W: max(1, r.W-barW), H: s.resultH})
	s.scrollbar.Layout(ctx, ui.Rect{X: max(0, r.W-barW), Y: s.listY0, W: barW, H: s.resultH})
	if s.divider != nil {
		s.divider.SetCurrentFirst(s.resultH)
		s.divider.Layout(ctx, ui.Rect{X: 0, Y: s.listY0 + s.resultH, W: r.W, H: max(0, divH)})
	}
	s.preview.Layout(ctx, ui.Rect{X: 0, Y: previewStart, W: r.W, H: max(0, previewH)})
	s.actions.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: 1})
}

func (s *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	r := s.Bounds()
	bg := cell.NoColor
	bar := cell.NoColor
	if s.tokens != nil {
		bg = s.tokens.Color(ui.ColorPopupBg)
		bar = s.tokens.Color(ui.ColorPopupSubtleBg)
	}
	if !bg.IsDefault() {
		ui.FillRect(buf, originX, originY, r, cell.BlankCell(bg))
	}
	toolbarH := 1
	if s.mode == ModeReplace {
		toolbarH = 2
	}
	toolbarH = min(toolbarH, max(0, r.H))
	if !bar.IsDefault() && toolbarH > 0 {
		ui.FillRect(buf, originX, originY, ui.Rect{X: r.X, Y: r.Y, W: r.W, H: toolbarH}, cell.BlankCell(bar))
	}
}

func (s *Screen) Handle(_ ui.EventContext, _ ui.Event) ui.EventResult { return ui.EventResult{} }
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "find.open":
		return ui.EventResult{Handled: true, Cmd: s.openSelected()}
	case "find.close":
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{Screen: s} }}
	case "find.toggle-case":
		s.caseSensitive = !s.caseSensitive
		s.caseToggle.SetChecked(s.caseSensitive)
		return ui.EventResult{Handled: true, Cmd: s.ScheduleSearch(), Invalidate: ui.InvalidatePaint}
	case "find.toggle-regex":
		s.regex = !s.regex
		s.regexToggle.SetChecked(s.regex)
		return ui.EventResult{Handled: true, Cmd: s.ScheduleSearch(), Invalidate: ui.InvalidatePaint}
	case "find.replace.one":
		return ui.EventResult{Handled: true, Cmd: s.replaceSelected(), Invalidate: ui.InvalidatePaint}
	case "find.replace.all":
		return ui.EventResult{Handled: true, Cmd: s.replaceAll(), Invalidate: ui.InvalidatePaint}
	case "actionbar.exit.backward":
		if len(s.results) > 0 {
			s.focusID(ListID)
		} else if s.mode == ModeReplace {
			s.focusID(ReplaceID)
		} else {
			s.focusID(QueryID)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "actionbar.exit.forward":
		s.focusID(QueryID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "find.focus.query":
		s.focusID(QueryID)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "find.focus.list":
		if len(s.results) > 0 {
			s.focusID(ListID)
		} else {
			s.focusID(ActionsID)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case ui.ActionSplitResize:
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{Action: &action}
}
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: s.mode.Title(), Selected: len(s.results) > 0}
}

type findQuery struct {
	ui.Base
	field *ui.SearchField
	owner *Screen
}

func (q *findQuery) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return q.field.Measure(ctx, cs)
}
func (q *findQuery) Layout(ctx ui.LayoutContext, r ui.Rect) {
	q.SetBounds(r)
	q.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (q *findQuery) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	q.field.Paint(buf, ox+q.Bounds().X, oy+q.Bounds().Y, ctx)
}
func (q *findQuery) Semantics() ui.Semantics       { return q.field.Semantics() }
func (q *findQuery) SetTokens(tokens *ui.TokenSet) { q.field.SetTokens(tokens) }
func (q *findQuery) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if q.owner == nil {
		return q.field.Handle(ctx, evt)
	}
	if evt.IsKey() {
		key := evt.Key
		// Query history: Ctrl+P/Ctrl+N always browse; bare Up/Down join in
		// only when they cannot mean result navigation (empty field or an
		// active history browse).
		browsing := q.owner.HistoryBrowsing()
		switch {
		case key.String() == "ctrl+p":
			return q.owner.historyStep(1)
		case key.String() == "ctrl+n":
			return q.owner.historyStep(-1)
		case key.Code == tea.KeyUp && key.Mod == 0 && (browsing || strings.TrimSpace(q.field.Value()) == ""):
			return q.owner.historyStep(1)
		case key.Code == tea.KeyDown && key.Mod == 0 && browsing:
			return q.owner.historyStep(-1)
		}
		switch key.Code {
		case tea.KeyEsc:
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{Screen: q.owner} }}
		case tea.KeyEnter:
			return ui.EventResult{Handled: true, Cmd: q.owner.openSelected()}
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
			q.owner.focusID(ListID)
			result := q.owner.list.Handle(ui.EventContext{Focused: true}, evt)
			result.Invalidate |= ui.InvalidateFocus | ui.InvalidatePaint
			return result
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				q.owner.focusID(ActionsID)
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
			}
			if q.owner.mode == ModeReplace {
				q.owner.focusID(ReplaceID)
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
			}
			if len(q.owner.results) > 0 {
				q.owner.focusID(ListID)
			} else {
				q.owner.focusID(ActionsID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		if key.String() == "ctrl+r" {
			return q.owner.HandleAction(ctx, ui.Action{ID: "find.toggle-regex"})
		}
		if ui.IsCommitChord(key) && q.owner.mode == ModeReplace {
			return q.owner.HandleAction(ctx, ui.Action{ID: "find.replace.all"})
		}
	}
	return q.applyFilter(ctx, evt)
}
func (q *findQuery) applyFilter(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	before := q.field.Value()
	result := q.field.Handle(ctx, evt)
	if q.field.Value() != before {
		q.owner.histIdx = -1
		result.Invalidate |= ui.InvalidateLayout | ui.InvalidatePaint
		result.Cmd = combineFindCmd(result.Cmd, q.owner.queryChanged())
	}
	return result
}

type replaceQuery struct {
	ui.Base
	field *ui.TextField
	owner *Screen
}

func (q *replaceQuery) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return q.field.Measure(ctx, cs)
}
func (q *replaceQuery) Layout(ctx ui.LayoutContext, r ui.Rect) {
	q.SetBounds(r)
	q.field.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (q *replaceQuery) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	q.field.Paint(buf, ox+q.Bounds().X, oy+q.Bounds().Y, ctx)
}
func (q *replaceQuery) Semantics() ui.Semantics       { return q.field.Semantics() }
func (q *replaceQuery) SetTokens(tokens *ui.TokenSet) { q.field.SetTokens(tokens) }
func (q *replaceQuery) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if q.owner == nil {
		return q.field.Handle(ctx, evt)
	}
	if evt.IsKey() {
		key := evt.Key
		switch key.Code {
		case tea.KeyEsc:
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{Screen: q.owner} }}
		case tea.KeyEnter:
			return q.owner.HandleAction(ctx, ui.Action{ID: "find.replace.one"})
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
			q.owner.focusID(ListID)
			result := q.owner.list.Handle(ui.EventContext{Focused: true}, evt)
			result.Invalidate |= ui.InvalidateFocus | ui.InvalidatePaint
			return result
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				q.owner.focusID(QueryID)
			} else {
				q.owner.focusID(ReplaceActionsID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		if key.String() == "ctrl+r" {
			return q.owner.HandleAction(ctx, ui.Action{ID: "find.toggle-regex"})
		}
		if ui.IsCommitChord(key) {
			return q.owner.HandleAction(ctx, ui.Action{ID: "find.replace.all"})
		}
	}
	return q.field.Handle(ctx, evt)
}

type findList struct {
	ui.Base
	list          *ui.VirtualList
	owner         *Screen
	lastClick     int
	lastClickTime time.Time
}

func (l *findList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return l.list.Measure(ctx, cs)
}
func (l *findList) Layout(ctx ui.LayoutContext, r ui.Rect) {
	l.SetBounds(r)
	l.list.Layout(ctx, ui.Rect{W: r.W, H: r.H})
}
func (l *findList) Paint(buf *surface.CellBuffer, ox, oy int, ctx ui.PaintContext) {
	l.list.Paint(buf, ox+l.Bounds().X, oy+l.Bounds().Y, ctx)
}
func (l *findList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if l.owner == nil {
		return l.list.Handle(ctx, evt)
	}
	if evt.IsKey() {
		key := evt.Key
		if key.String() == "ctrl+r" {
			return l.owner.HandleAction(ctx, ui.Action{ID: "find.toggle-regex"})
		}
		if ui.IsCommitChord(key) && l.owner.mode == ModeReplace {
			return l.owner.HandleAction(ctx, ui.Action{ID: "find.replace.all"})
		}
		switch key.Code {
		case tea.KeyEsc:
			return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{Screen: l.owner} }}
		case tea.KeyEnter:
			if row, ok := l.owner.rowAt(l.owner.selRow); ok && row.kind == rowHeader {
				l.owner.toggleGroup(row.group)
				return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
			}
			return ui.EventResult{Handled: true, Cmd: l.owner.openSelected()}
		case tea.KeyLeft:
			return l.owner.collapseAtSelection()
		case tea.KeyRight:
			return l.owner.expandAtSelection()
		case tea.KeyTab:
			if key.Mod.Contains(tea.ModShift) {
				if l.owner.mode == ModeReplace {
					l.owner.focusID(ReplaceID)
				} else {
					l.owner.focusID(QueryID)
				}
			} else {
				l.owner.focusID(PreviewID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown, tea.KeyHome, tea.KeyEnd:
			old := l.list.SelectedIndex()
			result := l.list.Handle(ctx, evt)
			if l.list.SelectedIndex() != old {
				l.owner.selectRow(l.list.SelectedIndex())
			}
			return result
		default:
			if key.Text != "" && !key.Mod.Contains(tea.ModCtrl) && !key.Mod.Contains(tea.ModAlt) {
				l.owner.focusID(QueryID)
				return l.owner.queryField.applyFilter(ui.EventContext{Focused: true}, evt)
			}
		}
	}
	if evt.Kind == ui.EventCustom {
		if _, ok := evt.Custom.(tea.PasteMsg); ok {
			l.owner.focusID(QueryID)
			return l.owner.queryField.applyFilter(ui.EventContext{Focused: true}, evt)
		}
	}
	result := l.list.Handle(ctx, evt)
	if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft && result.Action != nil {
		index := l.list.SelectedIndex()
		if row, ok := l.owner.rowAt(index); ok && row.kind == rowHeader {
			// A header click toggles the group immediately — no double-click
			// arming, no open action.
			l.owner.selectRow(index)
			l.owner.toggleGroup(row.group)
			l.lastClick, l.lastClickTime = -1, time.Time{}
			result.Action = nil
			result.Invalidate |= ui.InvalidatePaint
			return result
		}
		now := time.Now()
		if l.lastClick != index || now.Sub(l.lastClickTime) > 400*time.Millisecond {
			l.lastClick, l.lastClickTime = index, now
			result.Action = nil
			if index >= 0 && index < len(l.owner.rows) {
				l.owner.selectRow(index)
			}
		}
	}
	return result
}
func (l *findList) Semantics() ui.Semantics       { return l.list.Semantics() }
func (l *findList) SetTokens(tokens *ui.TokenSet) { l.list.SetTokens(tokens) }

func combineFindCmd(first, second tea.Cmd) tea.Cmd {
	if first == nil {
		return second
	}
	if second == nil {
		return first
	}
	return tea.Batch(first, second)
}

type previewPane struct {
	ui.Base
	owner *Screen
}

func (p *previewPane) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}
func (p *previewPane) Layout(_ ui.LayoutContext, r ui.Rect) { p.SetBounds(r) }
func (p *previewPane) SetTokens(*ui.TokenSet)               {}
func (p *previewPane) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	r := p.Bounds()
	if r.Empty() {
		return
	}
	ed := p.owner.previewSrc
	if ed == nil {
		return
	}
	w, h := r.W, r.H
	if p.owner.previewSurf == nil || p.owner.previewSurf.Width() != w || p.owner.previewSurf.Height() != h {
		p.owner.previewSurf = surface.NewSurface("fif-preview", w, h, cell.TransparentBlank())
	}
	p.owner.previewSurf.Buffer().Clear()
	wctx := ui.PaintContext{Theme: p.owner.th}
	ed.Render(p.owner.previewSurf, wctx)
	surface.BlitOpaque(buf, originX+r.X, originY+r.Y, p.owner.previewSurf.Buffer())
}
func (p *previewPane) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	ed := p.owner.previewSrc
	if ed == nil {
		return ui.EventResult{}
	}
	var msg tea.Msg
	switch evt.Kind {
	case ui.EventKey:
		if evt.Key.Code == tea.KeyTab {
			if evt.Key.Mod.Contains(tea.ModShift) {
				p.owner.focusID(ListID)
			} else {
				p.owner.focusID(ActionsID)
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		if evt.Key.Code == tea.KeyEsc {
			return p.owner.HandleAction(ctx, ui.Action{ID: "find.close"})
		}
		msg = evt.Key
	case ui.EventMouseClick:
		m := evt.MouseClick
		m.X, m.Y = ctx.LocalX, ctx.LocalY
		msg = m
	case ui.EventMouseMotion:
		m := evt.MouseMotion
		m.X, m.Y = ctx.LocalX, ctx.LocalY
		msg = m
	case ui.EventMouseRelease:
		m := evt.MouseRelease
		m.X, m.Y = ctx.LocalX, ctx.LocalY
		msg = m
	case ui.EventMouseWheel:
		m := evt.MouseWheel
		m.X, m.Y = ctx.LocalX, ctx.LocalY
		msg = m
	default:
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Cmd: ed.HandleEvent(msg), Invalidate: ui.InvalidatePaint}
}

func (p *previewPane) CancelPointerCapture() {
	if p.owner.previewSrc != nil {
		p.owner.previewSrc.CancelPointerCapture()
	}
}
func (p *previewPane) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Preview"}
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
