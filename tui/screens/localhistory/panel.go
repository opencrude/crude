package localhistory

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/gitdiff"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace/localhistory"
)

// Panel is the Local History tab of the Workspace panel. The timeline lists
// every recorded point with what changed and who changed it; the filter
// shows one agent's exact file edits instead. The selected row's diff shows
// beside the list. Every restore opens the restore preview first; the panel
// itself never changes files.
type Panel struct {
	ui.Base
	tokens *ui.TokenSet
	mode   Mode

	events      *ui.List
	eventsBox   *selectList
	eventsBar   *ui.ScrollbarControl
	timeline    []localhistory.Event
	path        string
	summary     string
	loading     bool
	loadErr     string
	names       map[string]string
	undo        *ui.Button
	restore     *ui.Button
	showAll     *ui.Button
	kept        *ui.Button
	safetyShown bool

	receipts        *ui.List
	receiptsBox     *selectList
	receiptsBar     *ui.ScrollbarControl
	agent           AgentRef
	coverage        string
	receiptsAfter   int64
	receiptsLoading bool
	receiptsErr     string
	filter          *ui.Button
	undoAgent       *ui.Button
	activity        []float64 // changes per day, oldest first
	pendingMessage  [2]string // session and message to select when they arrive

	split          *ui.SplitModel
	splitter       *ui.SplitDivider
	preview        *gitdiff.Document
	more           *ui.Button
	previewKey     string
	previewTitle   string
	previewMeta    string
	previewAfter   string
	previewLoading bool
	previewOnly    bool

	wide          bool
	statusRow     ui.Rect
	headerRow     ui.Rect
	actionRow     ui.Rect
	titleW, metaW int
	divider       int
	focus         func(ui.ComponentID) bool
	focusedID     func() ui.ComponentID
}

// Mode is what the list shows: the timeline or one agent's file edits.
type Mode string

// The panel's modes.
const (
	ModeTimeline Mode = "timeline"
	ModeAgent    Mode = "agent"
)

// AgentRef names the agent whose edits the list shows in ModeAgent.
type AgentRef struct {
	SessionID string
	AgentID   string
	Name      string
}

// Component IDs of the panel and its controls, for focus and tests.
const (
	PanelID         ui.ComponentID = "history-panel"
	EventsID        ui.ComponentID = "history-panel/events"
	ReceiptsID      ui.ComponentID = "history-panel/receipts"
	PanelPreviewID  ui.ComponentID = "history-panel/preview"
	PanelDividerID  ui.ComponentID = "history-panel/divider"
	UndoButtonID    ui.ComponentID = "history-panel/undo"
	RestoreButtonID ui.ComponentID = "history-panel/restore"
	ShowAllID       ui.ComponentID = "history-panel/show-all"
	KeptID          ui.ComponentID = "history-panel/kept"
	FilterID        ui.ComponentID = "history-panel/filter"
	UndoAgentID     ui.ComponentID = "history-panel/undo-agent"
	MoreFilesID     ui.ComponentID = "history-panel/more-files"
)

// Panel intents. The viewmodel reads and plans off the UI goroutine.
const (
	// ActionSelectEvent asks for one point's files (payload localhistory.EventQuery).
	ActionSelectEvent = "historypanel.select-event"
	// ActionUndoEvent previews reversing one point's changes (payload localhistory.Event).
	ActionUndoEvent = "historypanel.undo-event"
	// ActionRestoreEvent previews restoring the files, or the filtered path,
	// to one point (payload EventRestore).
	ActionRestoreEvent = "historypanel.restore-event"
	ActionShowAll      = "historypanel.show-all"
	ActionKept         = "historypanel.kept"
	ActionRefresh      = "historypanel.refresh"
	// ActionSelectReceipt reads one receipt's versions (payload localhistory.AgentReceiptQuery).
	ActionSelectReceipt = "historypanel.select-receipt"
	// ActionMoreReceipts reads the next page (payload localhistory.AgentReceiptQuery).
	ActionMoreReceipts = "historypanel.more-receipts"
	// ActionFilter asks the owner for the filter menu: every change, or
	// one agent's file edits.
	ActionFilter = "historypanel.filter"
	// ActionUndoAgent previews undoing the agent's latest changes (payload AgentRef).
	ActionUndoAgent = "historypanel.undo-agent"

	actionEventMoved   = "historypanel.event-moved"
	actionReceiptMoved = "historypanel.receipt-moved"
	actionOpenPreview  = "historypanel.open-preview"
)

// EventRestore restores Paths, or every file when Paths is empty, to Event.
type EventRestore struct {
	Event localhistory.Event
	Paths []string
}

const panelWideWidth = 96

// NewPanel builds the Local History tab showing the timeline.
func NewPanel(tokens *ui.TokenSet) *Panel {
	p := &Panel{Base: ui.NewBase(PanelID), tokens: tokens, mode: ModeTimeline, divider: -1, names: map[string]string{}}
	p.events = ui.NewList(EventsID, tokens)
	p.events.SetActivateAction(actionOpenPreview)
	p.events.SetDefaultRowBackground(ui.ColorBg)
	p.eventsBox = &selectList{list: p.events, onMove: func() *ui.Action { return &ui.Action{ID: actionEventMoved} }}
	p.eventsBar = ui.NewScrollbarControl(ui.NewID(EventsID, "scrollbar"), p.events.Scroll(), tokens)
	p.receipts = ui.NewList(ReceiptsID, tokens)
	p.receipts.SetActivateAction(actionOpenPreview)
	p.receipts.SetDefaultRowBackground(ui.ColorBg)
	p.receiptsBox = &selectList{list: p.receipts, onMove: func() *ui.Action { return &ui.Action{ID: actionReceiptMoved} }}
	p.receiptsBar = ui.NewScrollbarControl(ui.NewID(ReceiptsID, "scrollbar"), p.receipts.Scroll(), tokens)
	p.undo = ui.NewButton(UndoButtonID, "Undo changes…", ActionUndoEvent)
	p.restore = ui.NewButton(RestoreButtonID, "Restore to here…", ActionRestoreEvent)
	p.showAll = ui.NewButton(ShowAllID, "Show all files", ActionShowAll)
	p.kept = ui.NewButton(KeptID, "Kept for review", ActionKept)
	p.filter = ui.NewButton(FilterID, "All changes ▾", ActionFilter)
	p.undoAgent = ui.NewButton(UndoAgentID, "Undo latest changes…", ActionUndoAgent)
	p.more = ui.NewButton(MoreFilesID, "More files", ActionSelectEvent)
	for _, button := range p.buttons() {
		button.SetVariant(ui.VariantSecondary)
	}
	p.split = ui.NewSplitModel(ui.Horizontal)
	p.split.SetMinSizes(34, 30)
	p.split.SetFraction(0.4)
	p.split.SetCollapsible(true)
	p.splitter = ui.NewSplitDivider(PanelDividerID, p.split)
	p.splitter.SetColorRole(ui.ColorBorder)
	p.splitter.SetFill(true)
	p.preview = gitdiff.New(PanelPreviewID, tokens)
	p.preview.SetReadOnly(true)
	p.preview.SetEmptyText("Select a change to see its files")
	p.SetTokens(tokens)
	p.loading = true
	p.sync()
	return p
}

func (p *Panel) buttons() []*ui.Button {
	return []*ui.Button{p.filter, p.undo, p.restore, p.showAll, p.kept, p.undoAgent, p.more}
}

// SetTokens applies a theme to every child.
func (p *Panel) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	p.tokens = tokens
	p.events.SetTokens(tokens)
	p.eventsBar.SetTokens(tokens)
	p.receipts.SetTokens(tokens)
	p.receiptsBar.SetTokens(tokens)
	p.splitter.SetTokens(tokens)
	p.preview.SetTokens(tokens)
	for _, button := range p.buttons() {
		button.SetTokens(tokens)
		button.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	}
}

// SetFocusHandler installs the host's focus request and focus query.
func (p *Panel) SetFocusHandler(handler func(ui.ComponentID) bool, focused func() ui.ComponentID) {
	p.focus, p.focusedID = handler, focused
}

func (p *Panel) focusID(id ui.ComponentID) {
	if p.focus != nil {
		p.focus(id)
	}
}

// Mode is what the list shows.
func (p *Panel) Mode() Mode { return p.mode }

// SetMode shows the timeline or the agent's edits.
func (p *Panel) SetMode(mode Mode) {
	if mode == ModeTimeline || mode == ModeAgent {
		p.mode = mode
		p.previewOnly = false
		p.sync()
	}
}

// PreferredFocus is where keyboard focus lands when the panel appears.
func (p *Panel) PreferredFocus() ui.ComponentID {
	if p.mode == ModeAgent {
		return ReceiptsID
	}
	return EventsID
}

// FocusOrder follows the reading order: the top row, the row actions above
// a narrow list, the list, then the preview heading's actions and the
// preview. Hidden and unavailable buttons are skipped.
func (p *Panel) FocusOrder() []ui.ComponentID {
	var order []ui.ComponentID
	add := func(buttons ...*ui.Button) {
		for _, button := range buttons {
			if !button.Bounds().Empty() && !button.Semantics().Disabled {
				order = append(order, button.ID())
			}
		}
	}
	add(p.filter)
	add(p.viewButtons()...)
	if !p.actionRow.Empty() {
		add(p.rowButtons()...)
	}
	if list, _ := p.activeList(); !list.Bounds().Empty() {
		order = append(order, list.ID())
	}
	if !p.headerRow.Empty() {
		add(p.rowButtons()...)
		add(p.more)
	}
	if !p.preview.Bounds().Empty() {
		order = append(order, PanelPreviewID)
	}
	return order
}

// OwnsFocus reports whether id belongs to this panel's tree.
func (p *Panel) OwnsFocus(id ui.ComponentID) bool {
	return id == PanelID || strings.HasPrefix(string(id), string(PanelID)+"/")
}

// Path is the file or folder the Timeline shows, or "" for every file.
func (p *Panel) Path() string { return p.path }

// SetSafetyAvailable shows Kept for review when this shell can read them.
func (p *Panel) SetSafetyAvailable(available bool) { p.safetyShown = available; p.sync() }

// SetAgentNames resolves agent IDs to the names that rows show.
func (p *Panel) SetAgentNames(names map[string]string) {
	p.names = names
	p.syncEvents()
}

// BeginTimeline shows that the Timeline for path is being read.
func (p *Panel) BeginTimeline(path string) {
	if path != p.path {
		p.timeline, p.previewKey = nil, ""
		p.preview.SetFiles(nil)
	}
	p.path, p.loading, p.loadErr = path, true, ""
	p.sync()
}

// SetActivity installs the number of recorded changes per day, oldest
// first, for the small chart beside the summary.
func (p *Panel) SetActivity(days []float64) { p.activity = days }

// SetTimeline installs the recorded points, newest first. The selection
// stays on the same point when it is still kept.
func (p *Panel) SetTimeline(path string, events []localhistory.Event, summary string, err string) {
	if path != p.path {
		return
	}
	p.loading, p.loadErr, p.summary = false, err, summary
	if err == "" {
		p.timeline = events
	}
	p.syncEvents()
	p.selectPendingMessage()
	p.sync()
}

// SelectedEvent is the highlighted Timeline point.
func (p *Panel) SelectedEvent() (localhistory.Event, bool) {
	row, ok := p.events.Selected()
	if !ok {
		return localhistory.Event{}, false
	}
	event, ok := row.Payload.(localhistory.Event)
	return event, ok
}

// Events are the timeline points the list holds, newest first.
func (p *Panel) Events() []localhistory.Event { return p.timeline }

// SelectMessage selects the agent turn of messageID once the timeline holds
// it; until then the first matching point to arrive is selected.
func (p *Panel) SelectMessage(sessionID, messageID string) {
	p.pendingMessage = [2]string{sessionID, messageID}
	p.selectPendingMessage()
}

func (p *Panel) selectPendingMessage() {
	if p.pendingMessage[1] == "" {
		return
	}
	for index, event := range p.timeline {
		if event.SessionID == p.pendingMessage[0] && event.MessageID == p.pendingMessage[1] {
			p.events.SelectIndex(index)
			p.pendingMessage = [2]string{}
			return
		}
	}
}

// SelectionAction is the preview read for the highlighted row, or nil when
// the preview already shows it.
func (p *Panel) SelectionAction() *ui.Action {
	if p.Bounds().Empty() && p.previewKey != "" {
		return nil
	}
	return p.selectionAction()
}

// SelectEvent moves the Timeline selection to the point with id.
func (p *Panel) SelectEvent(id string) bool {
	for index, event := range p.timeline {
		if event.ID == id {
			p.events.SelectIndex(index)
			p.sync()
			return true
		}
	}
	return false
}

// PreviewKey names what the preview shows or is reading.
func (p *Panel) PreviewKey() string { return p.previewKey }

// BeginPreview clears the preview while key is read. More pages keep the
// files already shown.
func (p *Panel) BeginPreview(key, title, meta string, more bool) {
	p.previewKey, p.previewTitle, p.previewMeta, p.previewLoading = key, title, meta, true
	if !more {
		p.previewAfter = ""
		p.preview.SetFiles(nil)
	}
	p.preview.SetEmptyText("Reading the changed files…")
	p.sync()
}

// SetPreview shows files for key. after continues a long list.
func (p *Panel) SetPreview(key string, files []gitdiff.File, after string, empty string) {
	if key != p.previewKey {
		return
	}
	p.previewLoading, p.previewAfter = false, after
	p.preview.SetEmptyText(empty)
	p.preview.SetFiles(files)
	p.sync()
}

// PreviewFiles are the files the preview shows, in order.
func (p *Panel) PreviewFiles() []gitdiff.File { return p.preview.Files() }

// SetPreviewError explains why the preview could not be read.
func (p *Panel) SetPreviewError(key, problem string) {
	if key != p.previewKey {
		return
	}
	p.previewLoading = false
	p.preview.SetFiles(nil)
	p.preview.SetEmptyText(problem + " Select the row again to retry.")
	p.sync()
}

// Agent is the agent the Agent changes tab shows.
func (p *Panel) Agent() AgentRef { return p.agent }

// SetAgent selects whose edits the Agent changes tab lists.
func (p *Panel) SetAgent(agent AgentRef) {
	if agent != p.agent {
		p.agent = agent
		p.receipts.SetItems(nil)
		p.receiptsAfter, p.coverage, p.receiptsErr = 0, "", ""
		if p.mode == ModeAgent {
			p.previewKey = ""
			p.preview.SetFiles(nil)
		}
	}
	p.receiptsLoading = agent.AgentID != ""
	p.sync()
}

func agentLabel(agent AgentRef) string {
	if agent.Name != "" {
		return agent.Name
	}
	return "Agent"
}

// SetReceipts adds one page of the agent's receipts. A query with After
// continues the list; otherwise the list starts again.
func (p *Panel) SetReceipts(q localhistory.AgentReceiptQuery, page localhistory.RunChangePage, err string) {
	if q.SessionID != p.agent.SessionID || q.AgentID != p.agent.AgentID {
		return
	}
	p.receiptsLoading, p.receiptsErr = false, err
	if err != "" {
		p.sync()
		return
	}
	p.coverage = page.Coverage
	var rows []ui.ListItem
	if q.After != 0 {
		for _, row := range p.receipts.Items() {
			if row.ID != "more" {
				rows = append(rows, row)
			}
		}
	}
	first := len(rows)
	now := time.Now()
	for _, change := range page.Changes {
		exact := localhistory.AgentReceiptQuery{SessionID: q.SessionID, AgentID: q.AgentID, OperationID: change.OperationID, Path: change.Path}
		rows = append(rows, ui.ListItem{ID: change.OperationID + ":" + change.Path, Label: change.Path,
			Detail: receiptSource(change.Source) + "  " + ui.RelativeAge(time.Unix(0, change.CreatedAt), now), Payload: exact})
	}
	if page.After > 0 {
		next := localhistory.AgentReceiptQuery{SessionID: q.SessionID, AgentID: q.AgentID, After: page.After}
		rows = append(rows, ui.ListItem{ID: "more", Label: "Show more changes", Command: true, Payload: next})
	}
	p.receiptsAfter = page.After
	p.receipts.SetItemsStable(rows)
	if q.After != 0 {
		p.receipts.SelectIndex(first)
	}
	p.sync()
}

// SelectedReceipt is the highlighted receipt, if any.
func (p *Panel) SelectedReceipt() (localhistory.AgentReceiptQuery, bool) {
	row, ok := p.receipts.Selected()
	if !ok || row.ID == "more" {
		return localhistory.AgentReceiptQuery{}, false
	}
	q, ok := row.Payload.(localhistory.AgentReceiptQuery)
	return q, ok
}

func receiptSource(source string) string {
	switch source {
	case "write_file":
		return "wrote"
	case "edit_file", "apply_patch", "multi_edit":
		return "edited"
	case "delete_file":
		return "deleted"
	case "move_file", "rename_file":
		return "moved"
	}
	return "changed"
}

// EventTitle names a point by what happened, in the user's words.
func EventTitle(event localhistory.Event, name func(string) string) string {
	files := countLabel(len(event.Paths), "file")
	switch event.Kind {
	case "after_message":
		var who []string
		for _, id := range event.AgentIDs {
			who = append(who, name(id))
		}
		if len(who) == 0 {
			return "Agent turn changed " + files
		}
		return strings.Join(who, ", ") + " changed " + files
	case "before_message", "external_change", "reconcile", "automatic":
		return "Changed outside agents · " + files
	case "startup_reconcile":
		return "Changed while Crude was closed · " + files
	case "baseline":
		return "History started"
	case "safety_before_restore":
		return "Before a restore · " + files
	case "after_restore":
		return "Restored " + files
	case "safety":
		if event.Label != "" {
			return event.Label
		}
		return "Kept a copy for safety"
	case "manual":
		if event.Label != "" && event.Label != "Manual snapshot" {
			return event.Label + " · " + files
		}
		return "Saved snapshot · " + files
	}
	if event.Label != "" {
		return event.Label
	}
	return "Changed " + files
}

func (p *Panel) agentName(id string) string {
	if name := p.names[id]; name != "" {
		return name
	}
	return "Agent"
}

func (p *Panel) syncEvents() {
	now := time.Now()
	rows := make([]ui.ListItem, 0, len(p.timeline))
	for _, event := range p.timeline {
		rows = append(rows, ui.ListItem{ID: event.ID, Label: EventTitle(event, p.agentName), Detail: ui.RelativeAge(time.Unix(0, event.CreatedAt), now),
			Tooltip: snapshotTime(time.Unix(0, event.CreatedAt), now), Payload: event})
	}
	p.events.SetItemsStable(rows)
}

func (p *Panel) sync() {
	p.filter.SetLabel("All changes ▾")
	if p.mode == ModeAgent {
		p.filter.SetLabel(agentLabel(p.agent) + "'s edits ▾")
	}
	event, selected := p.SelectedEvent()
	p.undo.SetDisabled(!selected || len(event.Paths) == 0 || event.BaseID == "" && event.MessageID == "")
	p.restore.SetDisabled(!selected)
	if p.path != "" {
		p.restore.SetLabel("Restore this version…")
	} else {
		p.restore.SetLabel("Restore to here…")
	}
	p.undoAgent.SetDisabled(p.agent.AgentID == "")
	p.more.SetDisabled(p.previewLoading)
}

// viewButtons act on the whole list and sit at the end of the top row.
func (p *Panel) viewButtons() []*ui.Button {
	switch {
	case p.mode == ModeAgent:
		return nil
	case p.path != "":
		return []*ui.Button{p.showAll}
	case p.safetyShown:
		return []*ui.Button{p.kept}
	}
	return nil
}

// rowButtons act on the selected row: they sit in the preview heading, or
// in their own row above the list when the preview is not beside it.
func (p *Panel) rowButtons() []*ui.Button {
	if p.mode == ModeAgent {
		return []*ui.Button{p.undoAgent}
	}
	return []*ui.Button{p.undo, p.restore}
}

// placeEnd lays buttons out from the right end of row and returns the width
// left for text. A button that does not fit stays hidden.
func placeEnd(ctx ui.LayoutContext, row ui.Rect, buttons []*ui.Button, keep int) int {
	end := row.X + row.W
	for i := len(buttons) - 1; i >= 0; i-- {
		w := buttons[i].Measure(ctx, ui.Loose(30, 1)).W
		if end-w < row.X+keep {
			break
		}
		end -= w
		buttons[i].Layout(ctx, ui.Rect{X: end, Y: row.Y, W: w, H: 1})
		end--
	}
	return max(0, end-row.X)
}

func (p *Panel) activeList() (*selectList, *ui.ScrollbarControl) {
	if p.mode == ModeAgent {
		return p.receiptsBox, p.receiptsBar
	}
	return p.eventsBox, p.eventsBar
}

// Children lists the laid-out children in paint order.
func (p *Panel) Children() []ui.Component {
	if p.Bounds().Empty() {
		return nil
	}
	var children []ui.Component
	for _, child := range []ui.Component{p.eventsBox, p.eventsBar, p.receiptsBox, p.receiptsBar, p.splitter, p.preview} {
		if !child.Bounds().Empty() {
			children = append(children, child)
		}
	}
	for _, button := range p.buttons() {
		if !button.Bounds().Empty() {
			children = append(children, button)
		}
	}
	return children
}

// Measure fills the space the dock gives it.
func (p *Panel) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

// Layout places the top row, the list and the preview beside or instead of
// it.
func (p *Panel) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	p.SetBounds(bounds)
	width, height := bounds.W, bounds.H
	for _, child := range []ui.Component{p.eventsBox, p.eventsBar, p.receiptsBox, p.receiptsBar, p.splitter, p.preview} {
		child.Layout(ctx, ui.Rect{})
	}
	for _, button := range p.buttons() {
		button.Layout(ctx, ui.Rect{})
	}
	// The top row spans the tab: the filter, what the list holds, the
	// activity chart and the list-wide action.
	p.statusRow, p.headerRow, p.actionRow = ui.Rect{}, ui.Rect{}, ui.Rect{}
	top := min(1, height)
	if top > 0 {
		x := 1
		if w := p.filter.Measure(ctx, ui.Loose(30, 1)).W; w < width-12 {
			p.filter.Layout(ctx, ui.Rect{X: 1, W: w, H: 1})
			x += w + 1
		}
		p.statusRow = ui.Rect{X: x, W: max(0, width-x-1), H: 1}
		p.statusRow.W = placeEnd(ctx, p.statusRow, p.viewButtons(), 12)
	}
	bodyH := max(0, height-top)
	p.wide = width >= panelWideWidth && bodyH >= 4
	leftW := width
	p.divider = -1
	if p.wide {
		first, gap, _ := p.split.Resolve(width)
		switch p.split.Collapsed() {
		case ui.SplitFirst:
			leftW = 0
		case ui.SplitSecond:
			leftW = width - gap
		default:
			leftW = max(34, min(first, width-30))
		}
		p.divider = leftW
		p.splitter.SetCurrentFirst(leftW)
		p.splitter.Layout(ctx, ui.Rect{X: leftW, Y: top, W: gap, H: bodyH})
	} else if p.previewOnly {
		leftW = 0
	}
	y := top
	if leftW > 0 && !p.wide && bodyH > 2 {
		// Without the preview beside the list, the row actions get a row.
		p.actionRow = ui.Rect{X: 1, Y: y, W: max(0, leftW-2), H: 1}
		placeEnd(ctx, p.actionRow, p.rowButtons(), 0)
		y++
	}
	if leftW > 0 {
		list, bar := p.activeList()
		listH := max(0, height-y)
		barW := 0
		if bar.NeededForViewport(listH) {
			barW = ui.ScrollbarWidth
		}
		list.Layout(ctx, ui.Rect{X: 0, Y: y, W: max(0, leftW-barW), H: listH})
		bar.Layout(ctx, ui.Rect{X: leftW - barW, Y: y, W: barW, H: listH})
	}
	right := ui.Rect{X: leftW + 1, Y: top, W: max(0, width-leftW-1), H: bodyH}
	if !p.wide && p.previewOnly {
		right = ui.Rect{X: 0, Y: top, W: width, H: bodyH}
	}
	if (p.wide || p.previewOnly) && right.W > 2 && right.H > 2 {
		// The preview heading: the selected row's title with its actions,
		// then when it happened and More files for a long change.
		p.headerRow = ui.Rect{X: right.X + 1, Y: right.Y, W: right.W - 2, H: 2}
		titleRow, metaRow := p.headerRow, p.headerRow
		titleRow.H, metaRow.Y, metaRow.H = 1, p.headerRow.Y+1, 1
		buttons := p.rowButtons()
		p.titleW = placeEnd(ctx, titleRow, buttons, 16)
		p.metaW = p.headerRow.W
		if p.mode == ModeTimeline && p.previewAfter != "" {
			p.metaW = placeEnd(ctx, metaRow, []*ui.Button{p.more}, 10)
		} else if title := p.headerTitle(); rendering.VisibleWidth(title) > p.titleW {
			// A long title keeps its row: the actions move beside the time
			// when they fit there.
			need := 0
			for _, button := range buttons {
				need += button.Measure(ctx, ui.Loose(30, 1)).W + 1
			}
			if meta := rendering.VisibleWidth(p.headerMeta()); len(buttons) > 0 && meta+need <= metaRow.W {
				p.metaW = placeEnd(ctx, metaRow, buttons, meta)
				p.titleW = titleRow.W
			}
		}
		p.preview.Layout(ctx, ui.Rect{X: right.X, Y: right.Y + 2, W: right.W, H: right.H - 2})
	}
}

// Paint fills the panel and draws the top row and the preview heading.
func (p *Panel) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	bounds := p.Bounds()
	bg := p.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(bg))
	x, y := originX+bounds.X, originY+bounds.Y
	if row := p.statusRow; row.W > 0 && row.H > 0 {
		text, role := p.statusText()
		textW := row.W
		if spark := len(p.activity); p.mode == ModeTimeline && p.path == "" && spark > 0 && row.W >= rendering.VisibleWidth(text)+spark+4 {
			textW = row.W - spark - 2
			p.paintActivity(buf, x+row.X+row.W-spark, y+row.Y, bg)
		}
		buf.WriteString(x+row.X, y+row.Y, rendering.TruncateVisual(text, textW, "…"), p.tokens.Color(role), bg, 0)
	}
	if row := p.headerRow; row.W > 0 {
		meta, title := p.headerMeta(), p.headerTitle()
		buf.WriteString(x+row.X, y+row.Y, rendering.TruncateVisual(title, p.titleW, "…"), p.tokens.Color(ui.ColorFg), bg, cell.AttrBold)
		buf.WriteString(x+row.X, y+row.Y+1, rendering.TruncateVisual(meta, p.metaW, "…"), p.tokens.Color(ui.ColorMuted), bg, 0)
	}
}

// headerTitle names what the preview shows.
func (p *Panel) headerTitle() string {
	switch {
	case p.previewTitle != "":
		return p.previewTitle
	case p.mode == ModeAgent:
		return "Select an edit to see its diff"
	}
	return "Select a change to see its files"
}

// headerMeta is the line under the title: when it happened, and whether the
// files are still being read.
func (p *Panel) headerMeta() string {
	if p.previewLoading {
		return strings.TrimSpace(p.previewMeta + "  · reading…")
	}
	return p.previewMeta
}

// paintActivity draws changes per day as one row of columns, today last.
func (p *Panel) paintActivity(buf *surface.CellBuffer, x, y int, bg cell.Color) {
	peak := 0.0
	for _, count := range p.activity {
		peak = max(peak, count)
	}
	for i, count := range p.activity {
		glyph, role := ui.ColumnCell(ui.ColumnLevel(count, peak, 1), 0), ui.ColorAccent2
		if glyph == ' ' {
			glyph, role = '·', ui.ColorBorder
		}
		buf.Set(x+i, y, cell.NewCell(glyph, p.tokens.Color(role), bg, 0))
	}
}

// statusText states what the list shows, or why it shows nothing.
func (p *Panel) statusText() (string, ui.SemanticColor) {
	if p.mode == ModeAgent {
		switch {
		case p.agent.AgentID == "":
			return "Open an agent to see its file edits.", ui.ColorMuted
		case p.receiptsErr != "":
			return p.receiptsErr, ui.ColorError
		case p.receiptsLoading:
			return "Reading " + agentLabel(p.agent) + "'s file edits…", ui.ColorMuted
		case len(p.receipts.Items()) == 0:
			return agentLabel(p.agent) + " has no recorded file edits in this session.", ui.ColorMuted
		}
		return agentLabel(p.agent) + " · " + p.coverage, ui.ColorMuted
	}
	switch {
	case p.loadErr != "":
		return p.loadErr, ui.ColorError
	case p.loading:
		return "Reading Local History…", ui.ColorMuted
	case len(p.timeline) == 0 && p.path != "":
		return "No recorded changes to " + p.path + ".", ui.ColorMuted
	case len(p.timeline) == 0:
		return "No changes recorded yet. Crude records each agent turn and outside change.", ui.ColorMuted
	case p.path != "":
		return "History of " + p.path, ui.ColorFg
	}
	return p.summary, ui.ColorMuted
}

// Handle owns the panel's keyboard shortcuts; children handle the rest.
func (p *Panel) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.String() {
	case "ctrl+r":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionRefresh}}
	case "esc":
		if p.previewOnly {
			p.previewOnly = false
			p.focusID(p.PreferredFocus())
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
	case "[", "]":
		if p.wide {
			side := ui.SplitFirst
			if evt.Key.String() == "]" {
				side = ui.SplitSecond
			}
			p.split.ToggleCollapsed(side)
			if p.split.Collapsed() == ui.SplitFirst {
				p.focusID(PanelPreviewID)
			} else {
				p.focusID(p.PreferredFocus())
			}
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus}
		}
	}
	return ui.EventResult{}
}

// selectionAction asks for the preview of the highlighted row.
func (p *Panel) selectionAction() *ui.Action {
	if p.mode == ModeAgent {
		row, ok := p.receipts.Selected()
		if !ok {
			return nil
		}
		q, ok := row.Payload.(localhistory.AgentReceiptQuery)
		if !ok {
			return nil
		}
		if row.ID == "more" {
			return &ui.Action{ID: ActionMoreReceipts, Payload: q}
		}
		if p.previewKey == receiptKey(q) {
			return nil
		}
		return &ui.Action{ID: ActionSelectReceipt, Payload: q}
	}
	event, ok := p.SelectedEvent()
	if !ok || p.previewKey == event.ID {
		return nil
	}
	return &ui.Action{ID: ActionSelectEvent, Payload: localhistory.EventQuery{SnapshotID: event.ID}}
}

// HandleAction turns child actions into panel intents.
func (p *Panel) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch action.ID {
	case actionEventMoved, actionReceiptMoved:
		p.sync()
		result.Action = p.selectionAction()
		return result
	case actionOpenPreview:
		if row, ok := p.receipts.Selected(); p.mode == ModeAgent && ok && row.ID == "more" {
			result.Action = p.selectionAction()
			return result
		}
		result.Action = p.selectionAction()
		if !p.wide {
			p.previewOnly = true
		}
		p.focusID(PanelPreviewID)
		result.Invalidate |= ui.InvalidateFocus
		return result
	case ActionUndoEvent:
		if event, ok := p.SelectedEvent(); ok && len(event.Paths) > 0 {
			result.Action = &ui.Action{ID: ActionUndoEvent, Payload: event}
		}
		return result
	case ActionRestoreEvent:
		if event, ok := p.SelectedEvent(); ok {
			restore := EventRestore{Event: event}
			if p.path != "" {
				restore.Paths = []string{p.path}
			}
			result.Action = &ui.Action{ID: ActionRestoreEvent, Payload: restore}
		}
		return result
	case ActionSelectEvent:
		if p.previewAfter != "" && !p.previewLoading {
			result.Action = &ui.Action{ID: ActionSelectEvent, Payload: localhistory.EventQuery{SnapshotID: p.previewKey, After: p.previewAfter}}
		}
		return result
	case ActionUndoAgent:
		if p.agent.AgentID != "" {
			result.Action = &ui.Action{ID: ActionUndoAgent, Payload: p.agent}
		}
		return result
	case ui.ActionSplitResize:
		if resize, ok := action.Payload.(ui.SplitResize); ok && resize.ID == PanelDividerID {
			return result
		}
		return ui.EventResult{Action: &action}
	}
	if strings.HasPrefix(action.ID, "historypanel.") || action.ID == gitdiff.ActionFile {
		result.Action = &action
		return result
	}
	return ui.EventResult{Action: &action}
}

// Semantics describes the panel for assistive and control surfaces.
func (p *Panel) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Local History", Description: fmt.Sprintf("%d recorded changes", len(p.timeline))}
}

func receiptKey(q localhistory.AgentReceiptQuery) string {
	return "receipt:" + q.OperationID + ":" + q.Path
}

// ReceiptKey is the preview key of one receipt.
func ReceiptKey(q localhistory.AgentReceiptQuery) string { return receiptKey(q) }

var _ ui.Component = (*Panel)(nil)
var _ ui.Parent = (*Panel)(nil)
var _ ui.ActionHandler = (*Panel)(nil)

// selectList wraps a ui.List so keyboard and pointer movement can request a
// preview, as the Git History list does.
type selectList struct {
	list   *ui.List
	onMove func() *ui.Action
}

func (box *selectList) ID() ui.ComponentID                     { return box.list.ID() }
func (box *selectList) Bounds() ui.Rect                        { return box.list.Bounds() }
func (box *selectList) SetBounds(r ui.Rect)                    { box.list.SetBounds(r) }
func (box *selectList) Layout(ctx ui.LayoutContext, r ui.Rect) { box.list.Layout(ctx, r) }
func (box *selectList) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	return box.list.Measure(ctx, cs)
}
func (box *selectList) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	box.list.Paint(buf, originX, originY, ctx)
}
func (box *selectList) Semantics() ui.Semantics { return box.list.Semantics() }

func (box *selectList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	before := box.list.SelectedIndex()
	result := box.list.Handle(ctx, evt)
	if box.onMove == nil || box.list.SelectedIndex() == before || evt.IsKey() && evt.Key.Code == tea.KeyEnter {
		return result
	}
	if action := box.onMove(); action != nil && result.Action == nil {
		result.Action = action
		result.Handled = true
	}
	return result
}
