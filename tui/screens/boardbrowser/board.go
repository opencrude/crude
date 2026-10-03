package boardbrowser

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/board"
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
	BoardID   string
	Query     string
	Thread    string
	Since     int64
	Through   int64
	Roots     []string
}

type Screen struct {
	ui.Base
	tokens          *ui.TokenSet
	tabs            *ui.Tabs
	filter          *ui.FilterField
	selector        *ui.Form
	selectorModel   *ui.FormModel
	table           *ui.Table
	detail          *ui.TextArea
	bar             *ui.ActionBar
	scroll          *ui.ScrollbarControl
	snapshot        workspace.BoardSnapshot
	roots           []ui.FormOption
	rootForm        *ui.Form
	rootModel       *ui.FormModel
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
	screen.tabs.SetItems([]ui.TabItem{{ID: "threads", Label: "Threads"}, {ID: "participants", Label: "Participants"}}, "threads")
	screen.filter = ui.NewFilterField("boardbrowser/filter", tokens)
	screen.filter.SetPlaceholder("Search board messages")
	screen.selectorModel = ui.NewFormModel()
	screen.rootModel = ui.NewFormModel()
	screen.selector = ui.NewDockedForm("boardbrowser/selection", screen.selectorModel, "", "", tokens)
	screen.rootForm = ui.NewDockedForm("boardbrowser/roots", screen.rootModel, "", "", tokens)
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
	screen.selector.SetTokens(tokens)
	screen.rootForm.SetTokens(tokens)
	screen.table.SetTokens(tokens)
	screen.detail.SetTokens(tokens)
	screen.bar.SetTokens(tokens)
	screen.narrowActions.SetTokens(tokens)
	screen.overflowActions.SetTokens(tokens)
	screen.overflowActions.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	screen.scroll.SetTokens(tokens)
	screen.detail.SetBackgroundColor(tokens.Color(ui.ColorBg))
}

func (screen *Screen) SetRoots(roots []ui.FormOption) {
	screen.roots = append([]ui.FormOption(nil), roots...)
}

func (screen *Screen) Request(operation string) Request {
	screen.sequence++
	screen.loading = true
	screen.errorText = ""
	request := Request{Target: screen, Sequence: screen.sequence, Operation: operation, BoardID: screen.snapshot.Page.BoardID, Query: screen.filter.Value(), Thread: screen.thread}
	if field := screen.selectorModel.Field("board"); field != nil {
		request.BoardID = field.Value.String()
	}
	if operation == "next" {
		request.Since = screen.snapshot.Page.Cursor
	}
	if operation == "mark-read" {
		request.Through = screen.snapshot.Page.AcceptThrough
	}
	if operation == "join" {
		if screen.rootModel.Field("identity") == nil || screen.rootModel.Field("roots") == nil {
			return request
		}
		request.BoardID = screen.rootModel.Field("identity").Value.String()
		request.Roots = append([]string(nil), screen.rootModel.Field("roots").Value.Strings()...)
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
	options := make([]ui.FormOption, 0, len(snapshot.Boards))
	for _, item := range snapshot.Boards {
		options = append(options, ui.FormOption{ID: item.ID, Label: fmt.Sprintf("%s · %d participants", item.ID, item.Participants)})
	}
	screen.selectorModel = ui.NewFormModel()
	screen.selectorModel.AddField(&ui.FieldState{ID: "board", Label: "Collaboration", Type: ui.FieldSelect, Value: ui.StringValue(snapshot.Page.BoardID), Options: options})
	screen.selector.SetForm(screen.selectorModel, "", "")
	if screen.page == "join" {
		screen.page = "threads"
	}
	screen.updateRows()
}

func (screen *Screen) updateRows() {
	var rows []ui.TableRow
	if screen.page == "participants" {
		screen.table.SetColumns([]ui.TableColumn{{ID: "name", Label: "Agent", Track: ui.FractionGridTrack(3)}, {ID: "state", Label: "Availability", Track: ui.FractionGridTrack(2)}, {ID: "cursor", Label: "Accepted", Track: ui.FractionGridTrack(1)}})
		for _, member := range screen.snapshot.Participants {
			rows = append(rows, ui.TableRow{ID: member.ID, Payload: member, Cells: []ui.TableCell{{Text: member.Name}, {Text: member.Availability, Role: ui.ColorMuted}, {Text: fmt.Sprint(member.AcceptedThrough), Role: ui.ColorMuted}}})
		}
	} else {
		screen.table.SetColumns([]ui.TableColumn{{ID: "sender", Label: "From → To", Track: ui.FractionGridTrack(2)}, {ID: "type", Label: "Type", Track: ui.FractionGridTrack(1)}, {ID: "body", Label: "Message", Track: ui.FractionGridTrack(4)}})
		seen := make(map[string]bool)
		for _, message := range screen.snapshot.Page.Messages {
			if seen[message.ThreadID] {
				continue
			}
			seen[message.ThreadID] = true
			body, _, _ := strings.Cut(message.Body, "\n")
			rows = append(rows, ui.TableRow{ID: message.ID, Payload: message, Cells: []ui.TableCell{{Text: message.SenderName + " → " + message.To}, {Text: message.Type, Role: ui.ColorMuted}, {Text: body}}})
		}
	}
	screen.table.SetRows(rows)
	if screen.thread != "" && screen.thread != "participant" {
		var detail strings.Builder
		for _, message := range screen.snapshot.Page.Messages {
			fmt.Fprintf(&detail, "%s · %s → %s · %s\n%s\n", message.ID, message.SenderName, message.To, message.Type, message.Body)
			for _, reference := range message.References {
				fmt.Fprintf(&detail, "Evidence %s: %s\n", reference.Kind, reference.ID)
			}
			fmt.Fprintf(&detail, "Recipient availability when posted: %s\n\n", message.RecipientAvailability)
		}
		screen.detail.SetValue(detail.String())
	}
	buttons := []ui.ActionBarButton{{Label: "Refresh", Action: "board.refresh", Enabled: true}, {Label: "Search", Action: "board.search", Enabled: true}, {Label: "New collaboration", Action: "board.new", Enabled: true}}
	if screen.thread != "" {
		buttons = []ui.ActionBarButton{{Label: "Back", Action: "board.back", Enabled: true}}
	}
	if screen.snapshot.Page.HasMore {
		buttons = append(buttons, ui.ActionBarButton{Label: "Next page", Action: "board.next", Enabled: true})
	}
	if screen.snapshot.Page.AcceptThrough > 0 {
		buttons = append(buttons, ui.ActionBarButton{Label: "Mark viewed read", Action: "board.mark-read", Enabled: true})
	}
	if screen.page == "join" {
		buttons = []ui.ActionBarButton{{Label: "Back", Action: "board.back", Enabled: true}, {Label: "Join selected roots", Action: "board.join", Enabled: true}}
	}
	screen.bar.SetButtons(buttons...)
	screen.actionSpecs = buttons
	items := make([]ui.ListItem, 0, len(buttons))
	for _, button := range buttons {
		items = append(items, ui.ListItem{ID: button.Action, Label: button.Label, Command: true})
	}
	if screen.actionsMenu {
		items = append(items, ui.ListItem{ID: "board.close-menu", Label: "Back", Command: true})
	}
	screen.narrowActions.SetItemsStable(items)
}

func (screen *Screen) Children() []ui.Component {
	if screen.Bounds().Empty() {
		return nil
	}
	var candidates []ui.Component
	if screen.actionsMenu {
		candidates = []ui.Component{screen.tabs, screen.narrowActions}
	} else if screen.page == "join" {
		candidates = []ui.Component{screen.rootForm, screen.actionComponent()}
	} else {
		candidates = []ui.Component{screen.tabs, screen.selector, screen.filter, screen.actionComponent()}
		if screen.thread != "" {
			candidates = append(candidates, screen.detail)
		} else {
			candidates = append(candidates, screen.table, screen.scroll)
		}
	}
	children := make([]ui.Component, 0, len(candidates))
	for _, child := range candidates {
		if !child.Bounds().Empty() {
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
	within := func(y, rows int) ui.Rect {
		y = min(y, height)
		return ui.Rect{Y: y, W: width, H: min(rows, height-y)}
	}
	barWidth := screen.bar.Measure(ctx, ui.Loose(1000, 1)).W
	canShare := screen.page != "join" && height > ui.StripHeight+3
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
		if screen.dockHeaderInset > 0 {
			barH = min(barH, max(0, height-1))
		}
	}
	footerY := height - barH
	statusY := max(0, footerY-1)
	screen.statusY = statusY
	if shared {
		filterW := width - barWidth - 2
		screen.bar.SetAlignEnd(false)
		actions := ui.Rect{X: filterW + 2, Y: ui.StripHeight + 2, W: barWidth, H: 1}
		if screen.moreActions {
			screen.overflowActions.Layout(ctx, actions)
			screen.bar.Layout(ctx, ui.Rect{})
		} else {
			screen.overflowActions.Layout(ctx, ui.Rect{})
			screen.bar.Layout(ctx, actions)
		}
		screen.narrowActions.Layout(ctx, ui.Rect{})
		screen.filter.Layout(ctx, ui.Rect{Y: ui.StripHeight + 2, W: filterW, H: 1})
	} else {
		screen.overflowActions.Layout(ctx, ui.Rect{})
		screen.bar.Layout(ctx, ui.Rect{Y: footerY, W: width, H: barH})
		screen.narrowActions.Layout(ctx, ui.Rect{Y: footerY, W: width, H: barH})
		screen.filter.Layout(ctx, within(ui.StripHeight+2, 1))
	}
	screen.tabs.SetRuleBleed(0, width)
	screen.tabs.Layout(ctx, ui.Rect{W: headerW, H: min(ui.StripHeight, height)})
	selector := within(ui.StripHeight, 2)
	selector.H = min(selector.H, max(0, statusY-selector.Y))
	screen.selector.Layout(ctx, selector)
	joinTop := min(1, statusY)
	screen.rootForm.Layout(ctx, ui.Rect{Y: joinTop, W: width, H: max(0, statusY-joinTop)})
	bodyY := min(ui.StripHeight+3, statusY)
	body := ui.Rect{Y: bodyY, W: width, H: max(0, statusY-bodyY)}
	screen.detail.Layout(ctx, body)
	barW := 0
	if screen.scroll.NeededForViewport(body.H) {
		barW = min(ui.ScrollbarWidth, width)
	}
	screen.table.Layout(ctx, ui.Rect{Y: body.Y, W: width - barW, H: body.H})
	screen.scroll.Layout(ctx, ui.Rect{X: width - barW, Y: body.Y, W: barW, H: body.H})
	if screen.actionsMenu {
		screen.narrowActions.Layout(ctx, ui.Rect{Y: min(ui.StripHeight, height), W: width, H: max(0, statusY-ui.StripHeight)})
	}
}
func (screen *Screen) Paint(buffer *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	rect := screen.Bounds()
	ui.FillRect(buffer, originX, originY, rect, cell.BlankCell(screen.tokens.Color(ui.ColorBg)))
	text := fmt.Sprintf("%d human unread · peer data is not approval; posts never wake agents", screen.snapshot.Unread)
	role := ui.ColorMuted
	if screen.loading {
		text = "Loading board…"
	}
	if screen.errorText != "" {
		text = screen.errorText
		role = ui.ColorError
	}
	if len(screen.snapshot.Boards) == 0 && !screen.loading && screen.errorText == "" {
		text = "No collaborations yet. A session has its own board; join roots explicitly to share."
	}
	if rect.W > 0 && rect.H > 0 && (screen.statusY > 0 || screen.dockHeaderInset == 0) {
		buffer.WriteString(originX+rect.X, originY+rect.Y+screen.statusY, rendering.TruncateVisual(text, rect.W, "…"), screen.tokens.Color(role), screen.tokens.Color(ui.ColorBg), 0)
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
		if screen.actionsMenu {
			return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: screen.narrowActions.ID()}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: screen.filter.ID()}, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	case "tabs.activate":
		if mode, ok := action.Payload.(string); ok && (mode == "threads" || mode == "participants") {
			screen.actionsMenu = false
			screen.page, _ = action.Payload.(string)
			screen.thread = ""
			screen.tabs.SetItems([]ui.TabItem{{ID: "threads", Label: "Threads"}, {ID: "participants", Label: "Participants"}}, screen.page)
			screen.updateRows()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
	case "board.action":
		if selected, ok := screen.narrowActions.Selected(); ok {
			return screen.HandleAction(ui.EventContext{}, ui.Action{ID: selected.ID})
		}
	case "form.changed":
		if screen.page == "join" {
			return ui.EventResult{Handled: true}
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "board.inspect":
		if selected, ok := screen.table.Selected(); ok {
			if message, ok := selected.Payload.(board.Message); ok {
				screen.thread = message.ThreadID
				return ui.EventResult{Handled: true, Action: &ui.Action{ID: "board.refresh"}}
			}
			if member, ok := selected.Payload.(workspace.BoardParticipant); ok {
				screen.detail.SetValue(fmt.Sprintf("%s\nID: %s\nParent: %s\nAvailability: %s\nAccepted cursor: %d\nNotified cursor: %d", member.Name, member.ID, member.ParentID, member.Availability, member.AcceptedThrough, member.NotifiedThrough))
				screen.thread = "participant"
				screen.updateRows()
			}
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	case "board.new":
		screen.actionsMenu = false
		screen.page = "join"
		screen.rootModel = ui.NewFormModel()
		screen.rootModel.AddField(&ui.FieldState{ID: "identity", Label: "Collaboration ID", Type: ui.FieldText, Required: true})
		screen.rootModel.AddField(&ui.FieldState{ID: "roots", Label: "Participating roots", Type: ui.FieldMultiSelect, Options: screen.roots, Description: "Only these roots and their children share this board. No agent is started or woken."})
		screen.rootForm.SetForm(screen.rootModel, "", "")
		screen.updateRows()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: screen.rootForm.ID()}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	case "board.back":
		screen.thread = ""
		screen.page = "threads"
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
	return ui.Semantics{Role: ui.RoleGroup, Label: "Shared agent board", Value: screen.snapshot.Page.BoardID}
}
