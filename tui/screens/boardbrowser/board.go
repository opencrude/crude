package boardbrowser

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/agent/collaboration"
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

// SetDockHeaderInset sets the width that the dock header takes from the screen header.
func (s *Screen) SetDockHeaderInset(width int) { s.dockHeaderInset = max(0, width) }

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

// SetTokens replaces the token set of the screen and of its children.
func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	s.tabs.SetTokens(tokens)
	s.filter.SetTokens(tokens)
	s.table.SetTokens(tokens)
	s.detail.SetTokens(tokens)
	s.bar.SetTokens(tokens)
	s.narrowActions.SetTokens(tokens)
	s.overflowActions.SetTokens(tokens)
	s.overflowActions.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	s.scroll.SetTokens(tokens)
	s.detail.SetBackgroundColor(tokens.Color(ui.ColorBg))
}

// Request builds the next board request with a fresh sequence.
func (s *Screen) Request(operation string) Request {
	s.sequence++
	s.loading = true
	s.errorText = ""
	request := Request{
		Target: s, Sequence: s.sequence, Operation: operation,
		Query: s.filter.Value(), Thread: s.thread, Page: s.page,
	}
	if operation == "next" {
		request.Since = s.snapshot.Page.Cursor
		request.Snapshot = s.snapshot.Page.Snapshot
	}
	if operation == "mark-read" {
		request.Through = s.snapshot.Page.Snapshot
	}
	return request
}

// Apply stores the board snapshot when its sequence is still current.
func (s *Screen) Apply(sequence uint64, snapshot workspace.BoardSnapshot, err error) {
	if sequence != s.sequence {
		return
	}
	s.loading = false
	if err != nil {
		s.errorText = err.Error()
		return
	}
	s.snapshot = snapshot
	s.updateRows()
}

func (s *Screen) updateRows() {
	var rows []ui.TableRow
	switch s.page {
	case "agents":
		s.table.SetColumns([]ui.TableColumn{
			{ID: "name", Label: "Agent", Track: ui.FractionGridTrack(3)},
			{ID: "state", Label: "State", Track: ui.FractionGridTrack(2)},
			{ID: "run", Label: "Current work", Track: ui.FractionGridTrack(3)},
		})
		for _, participant := range s.snapshot.Participants {
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
		s.table.SetColumns([]ui.TableColumn{
			{ID: "route", Label: "Author → Recipients", Track: ui.FractionGridTrack(3)},
			{ID: "kind", Label: "Type", Track: ui.FractionGridTrack(1)},
			{ID: "body", Label: "Report", Track: ui.FractionGridTrack(5)},
		})
		for _, event := range s.snapshot.Page.Events {
			if event.Kind != collaboration.EventReportPublished {
				continue
			}
			rows = append(rows, eventRow(event))
		}
	default:
		s.table.SetColumns([]ui.TableColumn{
			{ID: "route", Label: "From → To", Track: ui.FractionGridTrack(3)},
			{ID: "kind", Label: "Type", Track: ui.FractionGridTrack(2)},
			{ID: "body", Label: "Message", Track: ui.FractionGridTrack(5)},
		})
		seen := make(map[string]bool)
		for _, event := range s.snapshot.Page.Events {
			if seen[event.ThreadID] {
				continue
			}
			seen[event.ThreadID] = true
			rows = append(rows, eventRow(event))
		}
	}
	s.table.SetRows(rows)
	if s.thread != "" && s.thread != "agent" {
		var detail strings.Builder
		for _, event := range s.snapshot.Page.Events {
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
		s.detail.SetValue(strings.TrimSpace(detail.String()))
	}
	buttons := []ui.ActionBarButton{
		{Label: "Refresh", Action: "board.refresh", Enabled: true},
		{Label: "Search", Action: "board.search", Enabled: true},
	}
	if s.thread != "" {
		buttons = []ui.ActionBarButton{{Label: "Back", Action: "board.back", Enabled: true}}
	}
	if s.snapshot.Page.HasMore {
		buttons = append(buttons, ui.ActionBarButton{Label: "Next page", Action: "board.next", Enabled: true})
	}
	if s.snapshot.Page.Snapshot > s.snapshot.Page.HumanReadThrough {
		buttons = append(buttons, ui.ActionBarButton{Label: "Mark viewed read", Action: "board.mark-read", Enabled: true})
	}
	s.bar.SetButtons(buttons...)
	s.actionSpecs = buttons
	items := make([]ui.ListItem, 0, len(buttons)+1)
	for _, button := range buttons {
		items = append(items, ui.ListItem{ID: button.Action, Label: button.Label, Command: true})
	}
	if s.actionsMenu {
		items = append(items, ui.ListItem{ID: "board.close-menu", Label: "Back", Command: true})
	}
	s.narrowActions.SetItemsStable(items)
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
	case collaboration.EventCommandOutcome:
		return "Command outcome"
	case collaboration.EventPeerReply:
		return "Reply"
	default:
		return "Message"
	}
}

// Children returns the components that the screen shows.
func (s *Screen) Children() []ui.Component {
	if s.Bounds().Empty() {
		return nil
	}
	if s.actionsMenu {
		return visibleChildren(s.tabs, s.narrowActions)
	}
	candidates := []ui.Component{s.tabs, s.filter, s.actionComponent()}
	if s.thread != "" {
		candidates = append(candidates, s.detail)
	} else {
		candidates = append(candidates, s.table, s.scroll)
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

// Measure reports the whole constraint box as the size of the screen.
func (s *Screen) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

// Layout places the header, filter, actions and content in the given rect.
func (s *Screen) Layout(ctx ui.LayoutContext, rect ui.Rect) {
	s.SetBounds(rect)
	width, height := max(0, rect.W), max(0, rect.H)
	headerW := max(0, width-s.dockHeaderInset)
	barWidth := s.bar.Measure(ctx, ui.Loose(1000, 1)).W
	canShare := height > ui.StripHeight+2
	fullActions := canShare && width >= barWidth+14
	moreWidth := s.overflowActions.Measure(ctx, ui.Loose(1000, 1)).W
	s.moreActions = canShare && !fullActions && width >= moreWidth+6
	shared := fullActions || s.moreActions
	if s.moreActions {
		barWidth = moreWidth
	}
	s.compactActions = !shared && barWidth > width
	barH := 0
	if !shared {
		barH = 1
		if s.compactActions {
			barH = len(s.actionSpecs)
		}
		barH = min(barH, height)
	}
	footerY := height - barH
	s.statusY = max(0, footerY-1)
	filterY := min(ui.StripHeight, s.statusY)
	if shared {
		filterW := max(0, width-barWidth-2)
		s.bar.SetAlignEnd(false)
		actions := ui.Rect{X: filterW + 2, Y: filterY, W: barWidth, H: min(1, height-filterY)}
		if s.moreActions {
			s.overflowActions.Layout(ctx, actions)
			s.bar.Layout(ctx, ui.Rect{})
		} else {
			s.overflowActions.Layout(ctx, ui.Rect{})
			s.bar.Layout(ctx, actions)
		}
		s.narrowActions.Layout(ctx, ui.Rect{})
		s.filter.Layout(ctx, ui.Rect{Y: filterY, W: filterW, H: min(1, height-filterY)})
	} else {
		s.overflowActions.Layout(ctx, ui.Rect{})
		s.bar.Layout(ctx, ui.Rect{Y: footerY, W: width, H: barH})
		s.narrowActions.Layout(ctx, ui.Rect{Y: footerY, W: width, H: barH})
		s.filter.Layout(ctx, ui.Rect{Y: filterY, W: width, H: min(1, height-filterY)})
	}
	s.tabs.SetRuleBleed(0, width)
	s.tabs.Layout(ctx, ui.Rect{W: headerW, H: min(ui.StripHeight, height)})
	bodyY := min(filterY+1, s.statusY)
	body := ui.Rect{Y: bodyY, W: width, H: max(0, s.statusY-bodyY)}
	s.detail.Layout(ctx, body)
	barW := 0
	if s.scroll.NeededForViewport(body.H) {
		barW = min(ui.ScrollbarWidth, width)
	}
	s.table.Layout(ctx, ui.Rect{Y: body.Y, W: max(0, width-barW), H: body.H})
	s.scroll.Layout(ctx, ui.Rect{X: max(0, width-barW), Y: body.Y, W: barW, H: body.H})
	if s.actionsMenu {
		s.narrowActions.Layout(ctx, ui.Rect{
			Y: min(ui.StripHeight, height), W: width,
			H: max(0, s.statusY-ui.StripHeight),
		})
	}
}

// Paint fills the screen background and draws its status line.
func (s *Screen) Paint(buffer *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	rect := s.Bounds()
	ui.FillRect(buffer, originX, originY, rect, cell.BlankCell(s.tokens.Color(ui.ColorBg)))
	text := fmt.Sprintf("%d unread · %d workspace agents · directed delivery is automatic",
		s.snapshot.Unread, len(s.snapshot.Participants))
	role := ui.ColorMuted
	if s.loading {
		text = "Loading workspace communication…"
	}
	if s.errorText != "" {
		text = s.errorText
		role = ui.ColorError
	}
	if len(s.snapshot.Participants) == 0 && len(s.snapshot.Page.Events) == 0 &&
		!s.loading && s.errorText == "" {
		text = "No workspace agents or communication yet."
	}
	if rect.W > 0 && rect.H > 0 && (s.statusY > 0 || s.dockHeaderInset == 0) {
		buffer.WriteString(originX+rect.X, originY+rect.Y+s.statusY,
			rendering.TruncateVisual(text, rect.W, "…"),
			s.tokens.Color(role), s.tokens.Color(ui.ColorBg), 0)
	}
}

// Handle closes the action menu or leaves a thread when the user presses Escape.
func (s *Screen) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyEsc && s.actionsMenu {
		return s.HandleAction(ui.EventContext{}, ui.Action{ID: "board.close-menu"})
	}
	if event.IsKey() && event.Key.Code == tea.KeyEsc && s.thread != "" {
		s.thread = ""
		s.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

// HandleAction opens the action menu, switches tabs and moves focus.
func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case "board.menu":
		s.actionsMenu = true
		s.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: s.narrowActions.ID()}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	case "board.close-menu":
		s.actionsMenu = false
		s.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: s.filter.ID()}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	case "tabs.enter":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: s.filter.ID()}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "tabs.activate":
		mode, _ := action.Payload.(string)
		if mode == "threads" || mode == "agents" || mode == "reports" {
			s.actionsMenu = false
			s.page = mode
			s.thread = ""
			s.tabs.SetItems([]ui.TabItem{
				{ID: "threads", Label: "Threads"},
				{ID: "agents", Label: "Agents"},
				{ID: "reports", Label: "Reports"},
			}, s.page)
			s.updateRows()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
	case "board.action":
		if selected, ok := s.narrowActions.Selected(); ok {
			return s.HandleAction(ui.EventContext{}, ui.Action{ID: selected.ID})
		}
	case "form.changed":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "board.inspect":
		if selected, ok := s.table.Selected(); ok {
			if event, ok := selected.Payload.(collaboration.Event); ok {
				s.thread = event.ThreadID
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}}
			}
			if participant, ok := selected.Payload.(workspace.BoardParticipant); ok {
				s.detail.SetValue(fmt.Sprintf(
					"%s\nID: %s\nSession: %s\nParent: %s\nState: %s\nPresence: %s\nRuntime epoch: %d\nCurrent run: %s",
					participant.Name, participant.ID, participant.SessionID, participant.ParentID,
					participant.State, participant.Presence, participant.RuntimeEpoch, participant.CurrentRunID,
				))
				s.thread = "agent"
				s.updateRows()
			}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "board.back":
		s.thread = ""
		s.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	if strings.HasPrefix(action.ID, "board.") {
		return ui.EventResult{Handled: true, Action: &action}
	}
	return ui.EventResult{}
}

func (s *Screen) actionComponent() ui.Component {
	if s.moreActions {
		return s.overflowActions
	}
	if s.compactActions {
		return s.narrowActions
	}
	return s.bar
}

// Semantics describes the screen to accessibility tools.
func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Workspace collaboration", Value: s.snapshot.WorkspaceID}
}
