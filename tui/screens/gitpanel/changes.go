package gitpanel

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/gitdiff"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

type section uint8

const (
	sectionConflicts section = iota
	sectionStaged
	sectionChanges
)

type entry struct {
	header  bool
	section section
	count   int
	change  workspace.GitChange
}

type rowAction struct {
	id    string
	glyph string
	x, w  int
}

// changeList is the grouped change list: section headers with bulk actions,
// file rows with inline stage/unstage/discard that appear on hover/selection.
type changeList struct {
	ui.Base
	owner    *Panel
	entries  []entry
	selected int
	scroll   *ui.ScrollModel
	bar      *ui.ScrollbarControl
	hover    int
	hoverX   int
	hovered  bool
	lastRow  int
	lastAt   time.Time
}

func newChangeList(owner *Panel) *changeList {
	list := &changeList{Base: ui.NewBase(ListID), owner: owner, scroll: ui.NewScrollModel(), hover: -1, lastRow: -1}
	list.bar = ui.NewScrollbarControl(ui.NewID(ListID, "scrollbar"), list.scroll, owner.tokens)
	list.bar.SetSurfaceBackground(ui.ColorBg)
	return list
}

func (list *changeList) setTokens(tokens *ui.TokenSet) { list.bar.SetTokens(tokens) }

// setChanges rebuilds the rows, keeping the selected change by identity.
func (list *changeList) setChanges(changes []workspace.GitChange) {
	selectedKey, previous := list.selectedKey(), list.selected
	groups := [3][]workspace.GitChange{}
	for _, change := range changes {
		switch change.Area {
		case workspace.GitAreaConflict:
			groups[sectionConflicts] = append(groups[sectionConflicts], change)
		case workspace.GitAreaStaged:
			groups[sectionStaged] = append(groups[sectionStaged], change)
		default:
			groups[sectionChanges] = append(groups[sectionChanges], change)
		}
	}
	list.entries = list.entries[:0]
	for index, group := range groups {
		if len(group) == 0 {
			continue
		}
		list.entries = append(list.entries, entry{header: true, section: section(index), count: len(group)})
		for _, change := range group {
			list.entries = append(list.entries, entry{section: section(index), change: change})
		}
	}
	list.selected = -1
	for index, item := range list.entries {
		if !item.header && gitdiff.ChangeKey(item.change) == selectedKey {
			list.selected = index
			break
		}
	}
	if list.selected < 0 && previous >= 0 {
		// The selected change moved (staged, unstaged, committed): keep the
		// cursor at the same place so the next file is one keypress away.
		list.selected = list.next(min(previous, len(list.entries))-1, 1)
		if list.selected < 0 {
			list.selected = list.next(len(list.entries), -1)
		}
	}
	if list.selected < 0 {
		list.selected = list.next(-1, 1)
	}
	list.scroll.SetContent(len(list.entries))
	list.scroll.Clamp()
}

func (list *changeList) selectedKey() string {
	if change, ok := list.selectedChange(); ok {
		return gitdiff.ChangeKey(change)
	}
	return ""
}

func (list *changeList) selectedChange() (workspace.GitChange, bool) {
	if list.selected < 0 || list.selected >= len(list.entries) || list.entries[list.selected].header {
		return workspace.GitChange{}, false
	}
	return list.entries[list.selected].change, true
}

func (list *changeList) selectKey(key string) bool {
	for index, item := range list.entries {
		if !item.header && gitdiff.ChangeKey(item.change) == key {
			list.selected = index
			list.scroll.EnsureVisible(index)
			return true
		}
	}
	return false
}

func (list *changeList) next(from, direction int) int {
	for index := from + direction; index >= 0 && index < len(list.entries); index += direction {
		if !list.entries[index].header {
			return index
		}
	}
	return -1
}

func (list *changeList) sectionChanges(target section) []workspace.GitChange {
	var out []workspace.GitChange
	for _, item := range list.entries {
		if !item.header && item.section == target {
			out = append(out, item.change)
		}
	}
	return out
}

func (list *changeList) Children() []ui.Component {
	if list.bar.Bounds().Empty() {
		return nil
	}
	return []ui.Component{list.bar}
}

func (list *changeList) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	list.SetBounds(bounds)
	list.scroll.SetViewport(bounds.H)
	list.scroll.SetContent(len(list.entries))
	barW := 0
	if list.bar.NeededForViewport(bounds.H) {
		barW = min(ui.ScrollbarWidth, bounds.W)
	}
	list.bar.Layout(ctx, ui.Rect{X: bounds.W - barW, W: barW, H: bounds.H})
}

func (list *changeList) rowWidth() int { return list.Bounds().W - list.bar.Bounds().W }

func (list *changeList) actionsFor(index int) []rowAction {
	item := list.entries[index]
	var actions []rowAction
	if item.header {
		switch item.section {
		case sectionStaged:
			actions = append(actions, rowAction{id: ActionUnstage, glyph: "Unstage all"})
		case sectionChanges:
			actions = append(actions, rowAction{id: ActionStage, glyph: "Stage all"})
		}
	} else {
		switch item.change.Area {
		case workspace.GitAreaStaged:
			actions = append(actions, rowAction{id: ActionUnstage, glyph: "−"})
		case workspace.GitAreaWorking:
			actions = append(actions, rowAction{id: ActionDiscard, glyph: "↶"}, rowAction{id: ActionStage, glyph: "+"})
		case workspace.GitAreaUntracked:
			actions = append(actions, rowAction{id: ActionStage, glyph: "+"})
		case workspace.GitAreaConflict:
			actions = append(actions, rowAction{id: ActionStage, glyph: "✓"})
		}
	}
	x := list.rowWidth()
	for position := len(actions) - 1; position >= 0; position-- {
		w := rendering.VisibleWidth(buttonText(actions[position].glyph))
		x -= w
		actions[position].x, actions[position].w = x, w
		x-- // one column between buttons
	}
	return actions
}

func (list *changeList) actionAt(index, localX int) string {
	if index < 0 || index >= len(list.entries) {
		return ""
	}
	for _, action := range list.actionsFor(index) {
		if localX >= action.x && localX < action.x+action.w {
			return action.id
		}
	}
	return ""
}

func (list *changeList) Paint(buf *surface.CellBuffer, originX, originY int, ctx ui.PaintContext) {
	bounds := list.Bounds()
	tokens := list.owner.tokens
	bg := tokens.Color(ui.ColorBg)
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(bg))
	list.hovered = ctx.Hovered
	x, y := originX+bounds.X, originY+bounds.Y
	width := list.rowWidth()
	if len(list.entries) == 0 {
		text, role := list.owner.emptyChangesText()
		for row, line := range rendering.WrapText(text, max(1, width-2)) {
			if row >= bounds.H {
				break
			}
			buf.WriteString(x+1, y+row, line, tokens.Color(role), bg, 0)
		}
		return
	}
	for local := 0; local < bounds.H; local++ {
		index := list.scroll.Offset() + local
		if index >= len(list.entries) {
			break
		}
		list.paintRow(buf, x, y+local, width, index, ctx.Focused)
	}
}

func (list *changeList) paintRow(buf *surface.CellBuffer, x, y, width, index int, focused bool) {
	tokens := list.owner.tokens
	item := list.entries[index]
	hovered := list.hovered && index == list.hover
	fill := tokens.Color(ui.ColorBg)
	switch {
	case index == list.selected && focused:
		fill = tokens.Color(ui.ColorSelection)
	case index == list.selected:
		fill = tokens.Color(ui.ColorSubPaneBg)
	case hovered:
		fill = tokens.Color(ui.ColorHover)
	}
	buf.FillRect(surface.Rect{X: x, Y: y, W: width, H: 1}, cell.BlankCell(fill))
	if index == list.selected && focused {
		buf.WriteString(x, y, "▌", tokens.Color(ui.ColorAccent), fill, 0)
	}
	ink := tokens.Color(ui.ColorFg)
	muted := tokens.Color(ui.ColorMuted)
	if index == list.selected && focused {
		ink = cell.ContrastColor(fill)
		muted = ink
	}
	showActions := hovered || index == list.selected || item.header
	actions := list.actionsFor(index)
	limit := width
	if len(actions) > 0 && showActions {
		limit = actions[0].x - 1
	}
	if item.header {
		label := [...]string{"Merge Conflicts", "Staged", "Changes"}[item.section]
		text := fmt.Sprintf("%s  %d", label, item.count)
		buf.WriteString(x+1, y, rendering.TruncateVisual(text, max(0, limit-1), "…"), muted, fill, cell.AttrBold)
	} else {
		code := item.change.Code
		buf.WriteString(x+2, y, code, tokens.Color(gitdiff.CodeRole(code, item.change.Area)), fill, cell.AttrBold)
		name, dir := gitdiff.SplitPath(item.change.Path)
		col := 2 + rendering.VisibleWidth(code) + 1
		nameText := rendering.TruncateVisual(name, max(0, limit-col), "…")
		attr := cell.Attrs(0)
		if index == list.selected {
			attr = cell.AttrBold
		}
		buf.WriteString(x+col, y, nameText, ink, fill, attr)
		col += rendering.VisibleWidth(nameText) + 1
		if dir != "" && col < limit {
			buf.WriteString(x+col, y, rendering.TruncateVisual(dir, limit-col, "…"), muted, fill, 0)
		}
	}
	if !showActions {
		return
	}
	for _, action := range actions {
		actionInk, actionFill := tokens.Color(ui.ColorAccent), fill
		if action.id == ActionDiscard {
			actionInk = tokens.Color(ui.ColorError)
		}
		if item.header {
			actionInk = muted
		}
		if hovered && list.hoverX >= action.x && list.hoverX < action.x+action.w {
			actionFill = tokens.Color(ui.ColorAccent)
			if action.id == ActionDiscard {
				actionFill = tokens.Color(ui.ColorError)
			}
			actionInk = cell.ContrastColor(actionFill)
		} else if index == list.selected && focused {
			actionInk = ink
		}
		buf.WriteString(x+action.x, y, buttonText(action.glyph), actionInk, actionFill, cell.AttrBold)
	}
}

func (list *changeList) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	paint := ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	if evt.IsKey() {
		return list.handleKey(evt.Key)
	}
	switch evt.Kind {
	case ui.EventMouseMotion:
		index := list.scroll.Offset() + ctx.LocalY
		if ctx.LocalY < 0 || index >= len(list.entries) {
			index = -1
		}
		if index != list.hover || ctx.LocalX != list.hoverX {
			list.hover, list.hoverX = index, ctx.LocalX
			return paint
		}
		return ui.EventResult{Handled: true}
	case ui.EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			list.scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			list.scroll.ScrollBy(3)
		}
		return paint
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		index := list.scroll.Offset() + ctx.LocalY
		if ctx.LocalY < 0 || index >= len(list.entries) {
			return ui.EventResult{Handled: true}
		}
		paint.Invalidate |= ui.InvalidateFocus
		if id := list.actionAt(index, ctx.LocalX); id != "" {
			paint.Action = list.intent(id, index)
			return paint
		}
		if list.entries[index].header {
			return paint
		}
		now := time.Now()
		double := index == list.lastRow && now.Sub(list.lastAt) <= 400*time.Millisecond
		list.lastRow, list.lastAt = index, now
		list.selected = index
		paint.Action = &ui.Action{ID: actionSelect}
		if double {
			paint.Action = &ui.Action{ID: ActionReview, Payload: list.selectedKey()}
		}
		return paint
	}
	return ui.EventResult{}
}

// intent builds the action for a row (single file) or header (whole section).
func (list *changeList) intent(id string, index int) *ui.Action {
	item := list.entries[index]
	if item.header {
		changes := list.sectionChanges(item.section)
		if len(changes) == 0 {
			return nil
		}
		return &ui.Action{ID: id, Payload: changes}
	}
	if id == ActionDiscard || id == ActionOpenFile {
		return &ui.Action{ID: id, Payload: item.change}
	}
	return &ui.Action{ID: id, Payload: []workspace.GitChange{item.change}}
}

func (list *changeList) move(direction int) bool {
	next := list.next(list.selected, direction)
	if next < 0 {
		return false
	}
	list.selected = next
	list.scroll.EnsureVisible(next)
	if direction < 0 && next > 0 && list.entries[next-1].header {
		list.scroll.EnsureVisible(next - 1)
	}
	return true
}

func (list *changeList) handleKey(key tea.KeyPressMsg) ui.EventResult {
	paint := ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	selectAction := &ui.Action{ID: actionSelect}
	switch key.String() {
	case "up", "k":
		if list.move(-1) {
			paint.Action = selectAction
		}
	case "down", "j":
		if list.move(1) {
			paint.Action = selectAction
		}
	case "home":
		list.selected = list.next(-1, 1)
		list.scroll.ScrollTo(0)
		paint.Action = selectAction
	case "end":
		list.selected = list.next(len(list.entries), -1)
		list.scroll.EnsureVisible(list.selected)
		paint.Action = selectAction
	case "pgup", "pgdown":
		direction := 1
		if key.String() == "pgup" {
			direction = -1
		}
		for step := 0; step < max(1, list.scroll.Viewport()-1); step++ {
			if !list.move(direction) {
				break
			}
		}
		paint.Action = selectAction
	case "space":
		if change, ok := list.selectedChange(); ok {
			id := ActionStage
			if change.Area == workspace.GitAreaStaged {
				id = ActionUnstage
			}
			paint.Action = list.intent(id, list.selected)
		}
	case "enter":
		if _, ok := list.selectedChange(); ok {
			paint.Action = &ui.Action{ID: ActionReview, Payload: list.selectedKey()}
		}
	case "o":
		if _, ok := list.selectedChange(); ok {
			paint.Action = list.intent(ActionOpenFile, list.selected)
		}
	case "d", "delete":
		if change, ok := list.selectedChange(); ok && change.Area == workspace.GitAreaWorking {
			paint.Action = list.intent(ActionDiscard, list.selected)
		}
	case "a", "+":
		if changes := list.sectionChanges(sectionChanges); len(changes) > 0 {
			paint.Action = &ui.Action{ID: ActionStage, Payload: changes}
		}
	case "-":
		if changes := list.sectionChanges(sectionStaged); len(changes) > 0 {
			paint.Action = &ui.Action{ID: ActionUnstage, Payload: changes}
		}
	default:
		return ui.EventResult{}
	}
	return paint
}

func (list *changeList) Semantics() ui.Semantics {
	value := ""
	if change, ok := list.selectedChange(); ok {
		value = change.Path
	}
	return ui.Semantics{Role: ui.RoleList, Label: "Changed files", Value: value, Description: "Space stages or unstages, Enter opens the review, O opens the file, D discards"}
}

// buttonText brackets an inline action so it reads as a button: glyph
// actions stay compact ("[+]"), worded actions use the full "[ Label ]".
func buttonText(label string) string {
	if rendering.VisibleWidth(label) == 1 {
		return "[" + label + "]"
	}
	return "[ " + label + " ]"
}
