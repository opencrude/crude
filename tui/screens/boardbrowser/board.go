package boardbrowser

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/collaboration"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

const ScreenID ui.ComponentID = "boardbrowser"

type Request struct {
	Target    *Screen
	Sequence  uint64
	Operation string
	Query     string
	Thread    string
	Page      string
	Since     int64
	Snapshot  int64
	Through   int64
}

type Screen struct {
	ui.Base
	tokens          *ui.TokenSet
	tabs            *ui.Tabs
	filter          *ui.FilterField
	table           *ui.Table
	detail          *ui.TextArea
	bar             *ui.ActionBar
	scroll          *ui.ScrollbarControl
	snapshot        workspace.BoardSnapshot
	page            string
	thread          string
	sequence        uint64
	loading         bool
	errorText       string
	actionSpecs     []ui.ActionBarButton
	narrowActions   *ui.List
	dockHeaderInset int
	compactActions  bool
	statusY         int
	overflowActions *ui.Button
	moreActions     bool
	actionsMenu     bool
}

func (screen *Screen) SetDockHeaderInset(width int) { screen.dockHeaderInset = max(0, width) }

func New(th *theme.Theme) *Screen {
	tokens := ui.NewTokenSet(th)
	screen := &Screen{Base: ui.NewBase(ScreenID), tokens: tokens, page: "threads"}
	screen.tabs = ui.NewTabs("boardbrowser/tabs", tokens)
	screen.tabs.SetChrome(ui.TabsShell)
	screen.tabs.SetRuleFullBleed(true)
	screen.tabs.SetItems([]ui.TabItem{
		{ID: "threads", Label: "Threads"},
		{ID: "agents", Label: "Agents"},
		{ID: "reports", Label: "Reports"},
	}, "threads")
	screen.filter = ui.NewFilterField("boardbrowser/filter", tokens)
	screen.filter.SetPlaceholder("Search workspace communication")
	screen.table = ui.NewTable("boardbrowser/table", tokens)
	screen.table.SetShowHeader(true)
	screen.table.SetActivateAction("board.inspect")
	screen.detail = ui.NewTextArea("boardbrowser/detail", tokens, true)
	screen.detail.SetReadOnly(true)
	screen.bar = ui.NewActionBar("boardbrowser/actions", tokens)
	screen.bar.SetAlignEnd(false)
	screen.narrowActions = ui.NewList("boardbrowser/action-list", tokens)
	screen.narrowActions.SetActivateAction("board.action")
	screen.overflowActions = ui.NewButton("boardbrowser/more", "More", "board.menu")
	screen.overflowActions.SetVariant(ui.VariantSecondary)
	screen.scroll = ui.NewScrollbarControl("boardbrowser/scroll", screen.table.Scroll(), tokens)
	screen.SetTokens(tokens)
	screen.updateRows()
	return screen
}

func (screen *Screen) SetTokens(tokens *ui.TokenSet) {
	screen.tokens = tokens
	screen.tabs.SetTokens(tokens)
	screen.filter.SetTokens(tokens)
	screen.table.SetTokens(tokens)
	screen.detail.SetTokens(tokens)
	screen.bar.SetTokens(tokens)
	screen.narrowActions.SetTokens(tokens)
	screen.overflowActions.SetTokens(tokens)
	screen.overflowActions.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	screen.scroll.SetTokens(tokens)
	screen.detail.SetBackgroundColor(tokens.Color(ui.ColorBg))
}

func (screen *Screen) Request(operation string) Request {
	screen.sequence++
	screen.loading = true
	screen.errorText = ""
	request := Request{
		Target: screen, Sequence: screen.sequence, Operation: operation,
		Query: screen.filter.Value(), Thread: screen.thread, Page: screen.page,
	}
	if operation == "next" {
		request.Since = screen.snapshot.Page.Cursor
		request.Snapshot = screen.snapshot.Page.Snapshot
	}
	if operation == "mark-read" {
		request.Through = screen.snapshot.Page.Snapshot
	}
	return request
}

func (screen *Screen) Apply(sequence uint64, snapshot workspace.BoardSnapshot, err error) {
	if sequence != screen.sequence {
		return
	}
	screen.loading = false
	if err != nil {
		screen.errorText = err.Error()
		return
	}
	screen.snapshot = snapshot
	screen.updateRows()
}

func (screen *Screen) updateRows() {
	var rows []ui.TableRow
	switch screen.page {
	case "agents":
		screen.table.SetColumns([]ui.TableColumn{
			{ID: "name", Label: "Agent", Track: ui.FractionGridTrack(3)},
			{ID: "state", Label: "State", Track: ui.FractionGridTrack(2)},
			{ID: "run", Label: "Current work", Track: ui.FractionGridTrack(3)},
		})
		for _, participant := range screen.snapshot.Participants {
			state := string(participant.State) + " · " + string(participant.Presence)
			run := participant.CurrentRunID
			if run == "" {
				run = "No active run"
			}
			rows = append(rows, ui.TableRow{
				ID: participant.ID, Payload: participant,
				Cells: []ui.TableCell{
					{Text: participant.Name},
					{Text: state, Role: ui.ColorMuted},
					{Text: run, Role: ui.ColorMuted},
				},
			})
		}
	case "reports":
		screen.table.SetColumns([]ui.TableColumn{
			{ID: "route", Label: "Author → Recipients", Track: ui.FractionGridTrack(3)},
			{ID: "kind", Label: "Type", Track: ui.FractionGridTrack(1)},
			{ID: "body", Label: "Report", Track: ui.FractionGridTrack(5)},
		})
		for _, event := range screen.snapshot.Page.Events {
			if event.Kind != collaboration.EventReportPublished {
				continue
			}
			rows = append(rows, eventRow(event))
		}
	default:
		screen.table.SetColumns([]ui.TableColumn{
			{ID: "route", Label: "From → To", Track: ui.FractionGridTrack(3)},
			{ID: "kind", Label: "Type", Track: ui.FractionGridTrack(2)},
			{ID: "body", Label: "Message", Track: ui.FractionGridTrack(5)},
		})
		seen := make(map[string]bool)
		for _, event := range screen.snapshot.Page.Events {
			if seen[event.ThreadID] {
				continue
			}
			seen[event.ThreadID] = true
			rows = append(rows, eventRow(event))
		}
	}
	screen.table.SetRows(rows)
	if screen.thread != "" && screen.thread != "agent" {
		var detail strings.Builder
		for _, event := range screen.snapshot.Page.Events {
			fmt.Fprintf(&detail, "%s · %s → %s · %s\n%s\n",
				event.ID, event.SenderName, recipientNames(event.Recipients),
				eventKindLabel(event.Kind), event.Body)
			if event.RequestID != "" {
				fmt.Fprintf(&detail, "Request %s · revision %d\n", event.RequestID, event.RequestRevision)
			}
			if event.RunID != "" {
				fmt.Fprintf(&detail, "Run %s\n", event.RunID)
			}
			for _, reference := range event.References {
				fmt.Fprintf(&detail, "Evidence %s: %s", reference.Kind, reference.ID)
				if reference.Revision > 0 {
					fmt.Fprintf(&detail, " · revision %d", reference.Revision)
				}
				detail.WriteByte('\n')
			}
			detail.WriteByte('\n')
		}
		screen.detail.SetValue(strings.TrimSpace(detail.String()))
	}
	buttons := []ui.ActionBarButton{
		{Label: "Refresh", Action: "board.refresh", Enabled: true},
		{Label: "Search", Action: "board.search", Enabled: true},
	}
	if screen.thread != "" {
		buttons = []ui.ActionBarButton{{Label: "Back", Action: "board.back", Enabled: true}}
	}
	if screen.snapshot.Page.HasMore {
		buttons = append(buttons, ui.ActionBarButton{Label: "Next page", Action: "board.next", Enabled: true})
	}
	if screen.snapshot.Page.Snapshot > screen.snapshot.Page.HumanReadThrough {
		buttons = append(buttons, ui.ActionBarButton{Label: "Mark viewed read", Action: "board.mark-read", Enabled: true})
	}
	screen.bar.SetButtons(buttons...)
	screen.actionSpecs = buttons
	items := make([]ui.ListItem, 0, len(buttons)+1)
	for _, button := range buttons {
		items = append(items, ui.ListItem{ID: button.Action, Label: button.Label, Command: true})
	}
	if screen.actionsMenu {
		items = append(items, ui.ListItem{ID: "board.close-menu", Label: "Back", Command: true})
	}
	screen.narrowActions.SetItemsStable(items)
}

func eventRow(event collaboration.Event) ui.TableRow {
	body, _, _ := strings.Cut(event.Body, "\n")
	return ui.TableRow{
		ID: event.ID, Payload: event,
		Cells: []ui.TableCell{
			{Text: event.SenderName + " → " + recipientNames(event.Recipients)},
			{Text: eventKindLabel(event.Kind), Role: ui.ColorMuted},
			{Text: body},
		},
	}
}

func recipientNames(recipients []collaboration.Recipient) string {
	names := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		name := recipient.Name
		if name == "" {
			name = recipient.ID
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func eventKindLabel(kind collaboration.EventKind) string {
	switch kind {
	case collaboration.EventWorkRequest:
		return "Work request"
	case collaboration.EventWorkRevision:
		return "Work revision"
	case collaboration.EventRequestDisposition:
		return "Request response"
	case collaboration.EventReportPublished:
		return "Report"
	case collaboration.EventRunOutcome:
		return "Run outcome"
	case collaboration.EventLifecycleControl:
		return "Control"
	case collaboration.EventControlOutcome:
		return "Control outcome"
	case collaboration.EventPeerReply:
		return "Reply"
	default:
		return "Message"
	}
}

func (screen *Screen) Children() []ui.Component {
	if screen.Bounds().Empty() {
		return nil
	}
	if screen.actionsMenu {
		return visibleChildren(screen.tabs, screen.narrowActions)
	}
	candidates := []ui.Component{screen.tabs, screen.filter, screen.actionComponent()}
	if screen.thread != "" {
		candidates = append(candidates, screen.detail)
	} else {
		candidates = append(candidates, screen.table, screen.scroll)
	}
	return visibleChildren(candidates...)
}

func visibleChildren(candidates ...ui.Component) []ui.Component {
	children := make([]ui.Component, 0, len(candidates))
	for _, child := range candidates {
		if child != nil && !child.Bounds().Empty() {
			children = append(children, child)
		}
	}
	return children
}

func (screen *Screen) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

func (screen *Screen) Layout(ctx ui.LayoutContext, rect ui.Rect) {
	screen.SetBounds(rect)
	width, height := max(0, rect.W), max(0, rect.H)
	headerW := max(0, width-screen.dockHeaderInset)
	barWidth := screen.bar.Measure(ctx, ui.Loose(1000, 1)).W
	canShare := height > ui.StripHeight+2
	fullActions := canShare && width >= barWidth+14
	moreWidth := screen.overflowActions.Measure(ctx, ui.Loose(1000, 1)).W
	screen.moreActions = canShare && !fullActions && width >= moreWidth+6
	shared := fullActions || screen.moreActions
	if screen.moreActions {
		barWidth = moreWidth
	}
	screen.compactActions = !shared && barWidth > width
	barH := 0
	if !shared {
		barH = 1
		if screen.compactActions {
			barH = len(screen.actionSpecs)
		}
		barH = min(barH, height)
	}
	footerY := height - barH
	screen.statusY = max(0, footerY-1)
	filterY := min(ui.StripHeight, screen.statusY)
	if shared {
		filterW := max(0, width-barWidth-2)
		screen.bar.SetAlignEnd(false)
		actions := ui.Rect{X: filterW + 2, Y: filterY, W: barWidth, H: min(1, height-filterY)}
		if screen.moreActions {
			screen.overflowActions.Layout(ctx, actions)
			screen.bar.Layout(ctx, ui.Rect{})
		} else {
			screen.overflowActions.Layout(ctx, ui.Rect{})
			screen.bar.Layout(ctx, actions)
		}
		screen.narrowActions.Layout(ctx, ui.Rect{})
		screen.filter.Layout(ctx, ui.Rect{Y: filterY, W: filterW, H: min(1, height-filterY)})
	} else {
		screen.overflowActions.Layout(ctx, ui.Rect{})
		screen.bar.Layout(ctx, ui.Rect{Y: footerY, W: width, H: barH})
		screen.narrowActions.Layout(ctx, ui.Rect{Y: footerY, W: width, H: barH})
		screen.filter.Layout(ctx, ui.Rect{Y: filterY, W: width, H: min(1, height-filterY)})
	}
	screen.tabs.SetRuleBleed(0, width)
	screen.tabs.Layout(ctx, ui.Rect{W: headerW, H: min(ui.StripHeight, height)})
	bodyY := min(filterY+1, screen.statusY)
	body := ui.Rect{Y: bodyY, W: width, H: max(0, screen.statusY-bodyY)}
	screen.detail.Layout(ctx, body)
	barW := 0
	if screen.scroll.NeededForViewport(body.H) {
		barW = min(ui.ScrollbarWidth, width)
	}
	screen.table.Layout(ctx, ui.Rect{Y: body.Y, W: max(0, width-barW), H: body.H})
	screen.scroll.Layout(ctx, ui.Rect{X: max(0, width-barW), Y: body.Y, W: barW, H: body.H})
	if screen.actionsMenu {
		screen.narrowActions.Layout(ctx, ui.Rect{
			Y: min(ui.StripHeight, height), W: width,
			H: max(0, screen.statusY-ui.StripHeight),
		})
	}
}

func (screen *Screen) Paint(buffer *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	rect := screen.Bounds()
	ui.FillRect(buffer, originX, originY, rect, cell.BlankCell(screen.tokens.Color(ui.ColorBg)))
	text := fmt.Sprintf("%d unread · %d workspace agents · directed delivery is automatic",
		screen.snapshot.Unread, len(screen.snapshot.Participants))
	role := ui.ColorMuted
	if screen.loading {
		text = "Loading workspace communication…"
	}
	if screen.errorText != "" {
		text = screen.errorText
		role = ui.ColorError
	}
	if len(screen.snapshot.Participants) == 0 && len(screen.snapshot.Page.Events) == 0 &&
		!screen.loading && screen.errorText == "" {
		text = "No workspace agents or communication yet."
	}
	if rect.W > 0 && rect.H > 0 && (screen.statusY > 0 || screen.dockHeaderInset == 0) {
		buffer.WriteString(originX+rect.X, originY+rect.Y+screen.statusY,
			rendering.TruncateVisual(text, rect.W, "…"),
			screen.tokens.Color(role), screen.tokens.Color(ui.ColorBg), 0)
	}
}

func (screen *Screen) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyEsc && screen.actionsMenu {
		return screen.HandleAction(ui.EventContext{}, ui.Action{ID: "board.close-menu"})
	}
	if event.IsKey() && event.Key.Code == tea.KeyEsc && screen.thread != "" {
		screen.thread = ""
		screen.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (screen *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "board.menu":
		screen.actionsMenu = true
		screen.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: screen.narrowActions.ID()}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	case "board.close-menu":
		screen.actionsMenu = false
		screen.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: screen.filter.ID()}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	case "tabs.enter":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: screen.filter.ID()}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "tabs.activate":
		mode, _ := action.Payload.(string)
		if mode == "threads" || mode == "agents" || mode == "reports" {
			screen.actionsMenu = false
			screen.page = mode
			screen.thread = ""
			screen.tabs.SetItems([]ui.TabItem{
				{ID: "threads", Label: "Threads"},
				{ID: "agents", Label: "Agents"},
				{ID: "reports", Label: "Reports"},
			}, screen.page)
			screen.updateRows()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
	case "board.action":
		if selected, ok := screen.narrowActions.Selected(); ok {
			return screen.HandleAction(ui.EventContext{}, ui.Action{ID: selected.ID})
		}
	case "form.changed":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "board.inspect":
		if selected, ok := screen.table.Selected(); ok {
			if event, ok := selected.Payload.(collaboration.Event); ok {
				screen.thread = event.ThreadID
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}}
			}
			if participant, ok := selected.Payload.(workspace.BoardParticipant); ok {
				screen.detail.SetValue(fmt.Sprintf(
					"%s\nID: %s\nSession: %s\nParent: %s\nState: %s\nPresence: %s\nRuntime epoch: %d\nCurrent run: %s",
					participant.Name, participant.ID, participant.SessionID, participant.ParentID,
					participant.State, participant.Presence, participant.RuntimeEpoch, participant.CurrentRunID,
				))
				screen.thread = "agent"
				screen.updateRows()
			}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "board.back":
		screen.thread = ""
		screen.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	if strings.HasPrefix(action.ID, "board.") {
		return ui.EventResult{Handled: true, Action: &action}
	}
	return ui.EventResult{}
}

func (screen *Screen) actionComponent() ui.Component {
	if screen.moreActions {
		return screen.overflowActions
	}
	if screen.compactActions {
		return screen.narrowActions
	}
	return screen.bar
}

func (screen *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace collaboration", Value: screen.snapshot.WorkspaceID}
}
