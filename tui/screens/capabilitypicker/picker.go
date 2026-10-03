package capabilitypicker

import (
	"encoding/json"
	"reflect"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	captypes "crude/types/capability"
)

type Selection struct {
	Kind       string
	ID         string
	Connection string
	Source     string
	Prompt     captypes.Prompt
}

type Screen struct {
	ui.Base
	tokens         *ui.TokenSet
	snapshot       captypes.Snapshot
	tabs           *ui.Tabs
	filter         *ui.FilterField
	list           *ui.List
	scroll         *ui.ScrollbarControl
	form           *ui.Form
	model          *ui.FormModel
	reader         *ui.TextArea
	actions        *ui.ActionBar
	status         *ui.Label
	category       string
	page           string
	selected       Selection
	preview        string
	busy           bool
	fetchSelection Selection
	fetchArguments map[string]string
	focus          func(ui.ComponentID)
}

func New(th *theme.Theme, snapshot captypes.Snapshot) *Screen {
	tokens := ui.NewTokenSet(th)
	screen := &Screen{Base: ui.NewBase("capability-picker"), tokens: tokens, snapshot: snapshot, category: "skills", page: "browse"}
	screen.tabs = ui.NewTabs("capability-picker/tabs", tokens)
	screen.tabs.SetChrome(ui.TabsPopup)
	screen.tabs.SetItems([]ui.TabItem{{ID: "skills", Label: "Skills"}, {ID: "tools", Label: "Tools"}, {ID: "resources", Label: "Resources"}, {ID: "prompts", Label: "Prompts"}}, "skills")
	screen.filter = ui.NewFilterField("capability-picker/filter", tokens)
	screen.filter.SetPlaceholder("Search available capabilities")
	screen.filter.SetSubmitAction("capability-picker.focus-list")
	screen.filter.OnChanged(func(string) { screen.rows() })
	screen.list = ui.NewList("capability-picker/list", tokens)
	screen.list.SetActivateAction("capability-picker.select")
	screen.scroll = ui.NewScrollbarControl("capability-picker/scroll", screen.list.Scroll(), tokens)
	screen.model = ui.NewFormModel()
	screen.form = ui.NewForm("capability-picker/form", screen.model, "", "", tokens)
	screen.reader = ui.NewTextArea("capability-picker/preview", tokens, true)
	screen.reader.SetReadOnly(true)
	screen.actions = ui.NewActionBar("capability-picker/actions", tokens)
	screen.actions.SetAlignEnd(false)
	screen.status = ui.NewLabel("capability-picker/status", "Selection stages a draft; it never executes or approves a tool.", th)
	screen.status.SetColorRole(ui.ColorMuted)
	screen.rows()
	screen.buttons()
	return screen
}

func (screen *Screen) SetFocusHandler(focus func(ui.ComponentID)) { screen.focus = focus }
func (screen *Screen) FocusSearch() {
	if screen.focus != nil {
		screen.focus(screen.filter.ID())
	}
}

func (screen *Screen) SetTokens(tokens *ui.TokenSet) {
	screen.tokens = tokens
	screen.tabs.SetTokens(tokens)
	screen.filter.SetTokens(tokens)
	screen.list.SetTokens(tokens)
	screen.scroll.SetTokens(tokens)
	screen.form.SetTokens(tokens)
	screen.reader.SetTokens(tokens)
	screen.actions.SetTokens(tokens)
	screen.status.SetTokens(tokens)
}

func (screen *Screen) rows() {
	query := strings.ToLower(screen.filter.Value())
	var rows []ui.ListItem
	add := func(selection Selection, label, detail string, disabled bool) {
		if strings.Contains(strings.ToLower(label+" "+detail+" "+selection.ID+" "+selection.Source), query) {
			rows = append(rows, ui.ListItem{ID: selection.Connection + "/" + selection.ID, Label: label, Detail: detail, Disabled: disabled, Payload: selection})
		}
	}
	if screen.category == "skills" {
		for _, entry := range screen.snapshot.Entries {
			if entry.Kind == captypes.KindSkill {
				add(Selection{Kind: "skills", ID: entry.ID, Source: entry.Source}, entry.Name, entry.Source+" · "+entry.State, entry.State != "available")
			}
		}
	}
	for _, connection := range screen.snapshot.Connections {
		if screen.category == "tools" {
			for _, tool := range connection.Tools {
				add(Selection{Kind: "tools", ID: tool.Name, Connection: connection.ID, Source: tool.Fingerprint}, tool.OriginalName, connection.ID+" · "+tool.Reason, false)
			}
		}
		if screen.category == "resources" {
			for _, resource := range connection.Resources {
				add(Selection{Kind: "resources", ID: resource.URI, Connection: connection.ID, Source: resource.URI}, resource.Name, connection.ID+" · "+resource.URI, connection.State != "connected")
			}
		}
		if screen.category == "prompts" {
			for _, prompt := range connection.Prompts {
				add(Selection{Kind: "prompts", ID: prompt.Name, Connection: connection.ID, Source: prompt.Name, Prompt: prompt}, prompt.Name, connection.ID+" · "+prompt.Description, connection.State != "connected")
			}
		}
	}
	if len(rows) == 0 {
		rows = append(rows, ui.ListItem{ID: "empty", Label: "No matches; manage Skills/MCP in Settings", Disabled: true})
	}
	screen.list.SetItemsStable(rows)
}

func (screen *Screen) buttons() {
	buttons := []ui.ActionBarButton{{Label: "Close", Action: "capability-picker.close", Enabled: true}, {Label: "Inspect / select", Action: "capability-picker.select", Enabled: !screen.busy}}
	if screen.page == "detail" {
		buttons = []ui.ActionBarButton{{Label: "Back", Action: "capability-picker.back", Enabled: true}, {Label: "Fetch", Action: "capability-picker.fetch", Enabled: !screen.busy}, {Label: "Stage", Action: "capability-picker.stage", Enabled: !screen.busy && screen.preview != ""}}
	}
	screen.actions.SetButtons(buttons...)
}

func (screen *Screen) Selected() Selection { return screen.selected }

func (screen *Screen) FetchAction() (captypes.Action, bool) {
	action := captypes.Action{ID: screen.selected.Connection, Kind: captypes.KindMCP, Content: screen.selected.ID, Arguments: make(map[string]string)}
	if screen.selected.Kind == "resources" {
		action.Operation = "resource"
	} else {
		action.Operation = "prompt"
		for _, argument := range screen.selected.Prompt.Arguments {
			value := screen.model.Field("arg/" + argument.Name).Value.String()
			if argument.Required && strings.TrimSpace(value) == "" {
				screen.status.SetText("Enter every required prompt argument before fetching.")
				return action, false
			}
			action.Arguments[argument.Name] = value
		}
	}
	screen.busy = true
	screen.fetchSelection = screen.selected
	screen.fetchArguments = action.Arguments
	screen.status.SetText("Fetching the exact selected external context…")
	screen.buttons()
	return action, true
}

func (screen *Screen) Apply(content string, err error) {
	screen.busy = false
	screen.preview = ""
	arguments := make(map[string]string)
	for _, argument := range screen.selected.Prompt.Arguments {
		if field := screen.model.Field("arg/" + argument.Name); field != nil {
			arguments[argument.Name] = field.Value.String()
		}
	}
	if screen.page != "detail" || !reflect.DeepEqual(screen.selected, screen.fetchSelection) || !reflect.DeepEqual(arguments, screen.fetchArguments) {
		screen.status.SetText("Selection or prompt arguments changed; fetch a fresh preview.")
		screen.buttons()
		return
	}
	if err != nil {
		screen.status.SetText(err.Error())
	} else {
		var response captypes.Result
		if json.Unmarshal([]byte(content), &response) != nil || response.IsError || response.Unknown || len(content) > 1<<20 {
			screen.status.SetText("Failed, unknown or oversized context cannot be staged.")
		} else {
			var body strings.Builder
			for _, item := range response.Content {
				if item.Data != "" {
					screen.status.SetText("Binary context requires a media attachment; this picker previews text.")
					screen.buttons()
					return
				}
				body.WriteString(item.Text + "\n")
			}
			screen.preview = content
			screen.reader.SetValue(body.String())
			screen.status.SetText("Source preview ready; Stage attaches it without submitting.")
		}
	}
	screen.buttons()
}

func (screen *Screen) StageAction() captypes.Action {
	operation := "resource"
	if screen.selected.Kind == "prompts" {
		operation = "prompt"
	}
	return captypes.Action{Operation: "stage-context", Kind: captypes.KindMCP, ID: screen.selected.Connection, Content: screen.preview, Arguments: map[string]string{"operation": operation, "source": screen.selected.Source}}
}

func (screen *Screen) Children() []ui.Component {
	children := []ui.Component{screen.tabs, screen.filter, screen.status, screen.actions}
	if screen.page == "detail" {
		return append(children, screen.form, screen.reader)
	}
	return append(children, screen.list, screen.scroll)
}

func (screen *Screen) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

func (screen *Screen) Layout(ctx ui.LayoutContext, rect ui.Rect) {
	screen.SetBounds(rect)
	screen.tabs.Layout(ctx, ui.Rect{W: rect.W, H: min(ui.StripHeight, rect.H)})
	screen.filter.Layout(ctx, ui.Rect{Y: ui.StripHeight, W: rect.W, H: min(1, max(0, rect.H-ui.StripHeight))})
	bottom := max(0, rect.H-2)
	screen.status.Layout(ctx, ui.Rect{Y: bottom, W: rect.W, H: min(1, rect.H)})
	screen.actions.Layout(ctx, ui.Rect{Y: max(0, rect.H-1), W: rect.W, H: min(1, rect.H)})
	body := ui.Rect{Y: ui.StripHeight + 2, W: rect.W, H: max(0, bottom-ui.StripHeight-2)}
	if screen.page == "detail" {
		formHeight := min(body.H/2, screen.form.PreferredHeight(body.H))
		screen.form.Layout(ctx, ui.Rect{Y: body.Y, W: body.W, H: formHeight})
		screen.reader.Layout(ctx, ui.Rect{Y: body.Y + formHeight, W: body.W, H: max(0, body.H-formHeight)})
	} else {
		barWidth := 0
		if screen.scroll.NeededForViewport(body.H) {
			barWidth = min(ui.ScrollbarWidth, body.W)
		}
		screen.list.Layout(ctx, ui.Rect{Y: body.Y, W: max(0, body.W-barWidth), H: body.H})
		screen.scroll.Layout(ctx, ui.Rect{X: body.W - barWidth, Y: body.Y, W: barWidth, H: body.H})
	}
}

func (screen *Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (screen *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Capability picker"}
}

func (screen *Screen) Handle(_ ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyEsc {
		return screen.HandleAction(ui.EventContext{}, ui.Action{ID: "capability-picker.back"})
	}
	return ui.EventResult{}
}

func (screen *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	changed := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch action.ID {
	case "tabs.activate", "tabs.enter":
		category, _ := action.Payload.(string)
		screen.category, screen.page, screen.preview = category, "browse", ""
		screen.rows()
		screen.buttons()
	case "form.changed":
		screen.preview = ""
		screen.buttons()
	case "capability-picker.focus-list":
		if screen.focus != nil {
			screen.focus(screen.list.ID())
		}
	case "capability-picker.back":
		if screen.page == "detail" {
			screen.page, screen.preview = "browse", ""
			screen.buttons()
		} else {
			changed.Action = &ui.Action{ID: "capability-picker.close"}
		}
	case "capability-picker.select":
		row, exists := screen.list.Selected()
		if !exists || row.Disabled {
			return changed
		}
		screen.selected, _ = row.Payload.(Selection)
		if screen.selected.Kind == "skills" || screen.selected.Kind == "tools" {
			changed.Action = &ui.Action{ID: "capability-picker.insert", Payload: screen.selected}
			return changed
		}
		screen.page, screen.preview = "detail", ""
		screen.model = ui.NewFormModel()
		for _, argument := range screen.selected.Prompt.Arguments {
			screen.model.AddField(ui.TextFieldState("arg/"+argument.Name, argument.Name, "", argument.Description))
		}
		screen.form.SetForm(screen.model, "", "")
		screen.reader.SetValue("Fetch the selected external source to preview it. Nothing is submitted.")
		screen.buttons()
	case "capability-picker.fetch", "capability-picker.stage", "capability-picker.close":
		changed.Action = &action
	default:
		return ui.EventResult{Action: &action}
	}
	return changed
}
