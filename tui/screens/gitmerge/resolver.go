// Package gitmerge resolves the conflicts of a stopped merge, rebase,
// cherry-pick or revert. It paints each conflict's current and incoming sides,
// records one choice per conflict and emits typed decisions; the owner reads
// files, writes resolutions and runs Git.
package gitmerge

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID ui.ComponentID = "git-merge/body"
	filesID  ui.ComponentID = "git-merge/files"
	docID    ui.ComponentID = "git-merge/document"
	barID    ui.ComponentID = "git-merge/actions"

	// Decisions handed to the owner.
	DecideSelect   = "select"   // Path
	DecideSave     = "save"     // Path, Fingerprint, Choices
	DecideOpen     = "open"     // Path, Line (1-based)
	DecideAbort    = "abort"    //
	DecideComplete = "complete" //
	DecideClose    = "close"    //

	actionSave     = "git-merge.save"
	actionOpen     = "git-merge.open"
	actionAbort    = "git-merge.abort"
	actionComplete = "git-merge.complete"
	actionClose    = "git-merge.close"
	actionFile     = "git-merge.file"

	// contextLines of unchanged text frame each conflict; longer stretches
	// collapse into one row.
	contextLines = 3
)

// Decision is one user decision for the owner to carry out.
type Decision struct {
	Kind        string
	Path        string
	Fingerprint string
	Choices     []workspace.GitConflictChoice
	Line        int
}

type rowKind uint8

const (
	rowContext rowKind = iota
	rowCollapsed
	rowHeader
	rowLabel
	rowLine
	rowSpacer
)

type side uint8

const (
	sideNone side = iota
	sideCurrent
	sideBase
	sideIncoming
)

type docRow struct {
	kind     rowKind
	text     string
	line     int // 1-based file line for context and conflict lines; 0 otherwise
	conflict int // index of the owning conflict, -1 for context
	side     side
}

// Resolver is the body of the merge dialog.
type Resolver struct {
	ui.Base
	tokens    *ui.TokenSet
	files     *ui.List
	doc       *ui.VirtualList
	actions   *ui.ActionBar
	decide    func(Decision) tea.Cmd
	operation string
	conflicts []string
	file      *workspace.GitConflictFile
	loading   string
	choices   []workspace.GitConflictChoice
	current   int
	rows      []docRow
	busy      string
	errText   string
	listW     int
}

func New(tokens *ui.TokenSet, decide func(Decision) tea.Cmd) *Resolver {
	r := &Resolver{Base: ui.NewBase(ScreenID), tokens: tokens, decide: decide}
	r.files = ui.NewList(filesID, tokens)
	r.files.SetActivateAction(actionFile)
	r.doc = ui.NewVirtualList(docID, tokens)
	r.doc.SetOpaqueRowFill(false)
	r.doc.SetSelectionAnchorsScroll(false)
	r.doc.SetPointerPolicy(ui.PointerPolicyPassthrough)
	r.doc.SetRowPainter(r.paintRow)
	r.actions = ui.NewActionBar(barID, tokens)
	r.actions.SetCancelAction(actionClose)
	r.syncActions()
	return r
}

// SetConflicts installs the paths still conflicted and the operation they
// belong to. The loaded file stays until it is no longer conflicted.
func (r *Resolver) SetConflicts(operation string, paths []string) {
	r.operation, r.conflicts = operation, append([]string(nil), paths...)
	items := make([]ui.ListItem, 0, len(paths)+1)
	for _, path := range paths {
		dir, name := filepath.Split(path)
		items = append(items, ui.ListItem{ID: path, Label: name, Detail: strings.TrimSuffix(dir, "/"), Payload: path, Command: true})
	}
	if len(items) == 0 {
		items = append(items, ui.ListItem{ID: "done", Label: "✓ All conflicts resolved", Disabled: true})
	}
	r.files.SetItemsStable(items)
	if r.file != nil && !containsPath(paths, r.file.Path) {
		r.file, r.rows, r.choices = nil, nil, nil
		r.doc.SetItems(nil)
	}
	if r.file != nil {
		r.selectListPath(r.file.Path)
	}
	r.syncActions()
}

func containsPath(paths []string, want string) bool {
	for _, path := range paths {
		if path == want {
			return true
		}
	}
	return false
}

func (r *Resolver) selectListPath(path string) {
	for i, item := range r.files.Items() {
		if item.ID == path {
			r.files.SelectIndex(i)
			return
		}
	}
}

// Conflicts lists the paths the dialog shows as conflicted.
func (r *Resolver) Conflicts() []string { return r.conflicts }

// Loading reports the path being read, if any.
func (r *Resolver) Loading() string { return r.loading }

// SetLoading marks path as being read.
func (r *Resolver) SetLoading(path string) {
	r.loading, r.errText = path, ""
	r.selectListPath(path)
}

// SetFile shows a parsed conflicted file. Choices restart when the file's
// bytes differ from the ones previously decided against.
func (r *Resolver) SetFile(file workspace.GitConflictFile) {
	if r.file == nil || r.file.Path != file.Path || r.file.Fingerprint != file.Fingerprint {
		r.choices = make([]workspace.GitConflictChoice, len(file.Conflicts()))
		r.current = 0
	}
	copy := file
	r.file, r.loading, r.errText = &copy, "", ""
	r.selectListPath(file.Path)
	r.rebuild()
	r.revealCurrent()
}

// File is the conflicted file being resolved.
func (r *Resolver) File() (workspace.GitConflictFile, bool) {
	if r.file == nil {
		return workspace.GitConflictFile{}, false
	}
	return *r.file, true
}

// Choices returns the decided choice of every conflict in the file.
func (r *Resolver) Choices() []workspace.GitConflictChoice {
	return append([]workspace.GitConflictChoice(nil), r.choices...)
}

// SetBusy labels a running write or Git command; empty clears it.
func (r *Resolver) SetBusy(label string) {
	r.busy = label
	r.syncActions()
}

func (r *Resolver) SetError(err error) {
	r.busy, r.loading = "", ""
	r.errText = ""
	if err != nil {
		r.errText = err.Error()
	}
	r.syncActions()
}

// Choose decides conflict index; out-of-range indexes are ignored.
func (r *Resolver) Choose(index int, choice workspace.GitConflictChoice) {
	if index < 0 || index >= len(r.choices) {
		return
	}
	r.choices[index] = choice
	r.current = index
	r.rebuild()
	r.syncActions()
}

func (r *Resolver) decided() bool {
	if r.file == nil || len(r.choices) == 0 {
		return false
	}
	for _, choice := range r.choices {
		if choice == workspace.GitChooseNone {
			return false
		}
	}
	return true
}

func operationLabel(operation string) (doing, finish string) {
	switch operation {
	case workspace.GitOperationRebase:
		return "Rebasing", "Continue rebase"
	case workspace.GitOperationCherryPick:
		return "Cherry-picking", "Continue cherry-pick"
	case workspace.GitOperationRevert:
		return "Reverting", "Continue revert"
	}
	return "Merging", "Complete merge"
}

func (r *Resolver) syncActions() {
	_, finish := operationLabel(r.operation)
	idle := r.busy == "" && r.loading == ""
	r.actions.SetButtons(
		ui.ActionBarButton{ID: "git-merge/save", Label: "Save resolution", Shortcut: "Ctrl+S", Action: actionSave, Enabled: idle && r.decided(), Variant: ui.VariantPrimary},
		ui.ActionBarButton{ID: "git-merge/open", Label: "Open in editor", Shortcut: "E", Action: actionOpen, Enabled: idle && r.file != nil, Variant: ui.VariantSecondary},
		ui.ActionBarButton{ID: "git-merge/complete", Label: finish, Action: actionComplete, Enabled: idle && len(r.conflicts) == 0, Variant: ui.VariantPrimary},
		ui.ActionBarButton{ID: "git-merge/abort", Label: "Abort…", Action: actionAbort, Enabled: idle, Variant: ui.VariantDanger},
		ui.ActionBarButton{ID: "git-merge/close", Label: "Close", Action: actionClose, Enabled: true, Variant: ui.VariantSecondary},
	)
}

// rebuild projects the parsed file into painted rows.
func (r *Resolver) rebuild() {
	r.rows = r.rows[:0]
	if r.file == nil {
		r.doc.SetItems(nil)
		return
	}
	line := 1
	conflict := 0
	segments := r.file.Segments
	for i, segment := range segments {
		if segment.Conflict == nil {
			r.addContext(segment.Text, line, i > 0, i < len(segments)-1)
			line += len(segment.Text)
			continue
		}
		region := segment.Conflict
		r.rows = append(r.rows, docRow{kind: rowHeader, conflict: conflict, line: line})
		line++ // <<<<<<<
		r.rows = append(r.rows, docRow{kind: rowLabel, side: sideCurrent, conflict: conflict, text: region.CurrentLabel})
		for _, text := range region.Current {
			r.rows = append(r.rows, docRow{kind: rowLine, side: sideCurrent, conflict: conflict, text: text, line: line})
			line++
		}
		if len(region.Base) > 0 {
			line++ // |||||||
			r.rows = append(r.rows, docRow{kind: rowLabel, side: sideBase, conflict: conflict})
			for _, text := range region.Base {
				r.rows = append(r.rows, docRow{kind: rowLine, side: sideBase, conflict: conflict, text: text, line: line})
				line++
			}
		}
		line++ // =======
		r.rows = append(r.rows, docRow{kind: rowLabel, side: sideIncoming, conflict: conflict, text: region.IncomingLabel})
		for _, text := range region.Incoming {
			r.rows = append(r.rows, docRow{kind: rowLine, side: sideIncoming, conflict: conflict, text: text, line: line})
			line++
		}
		line++ // >>>>>>>
		r.rows = append(r.rows, docRow{kind: rowSpacer, conflict: -1})
		conflict++
	}
	items := make([]ui.VirtItem, len(r.rows))
	for i := range r.rows {
		items[i] = ui.VirtItem{ID: fmt.Sprint(i), Height: 1, Payload: i}
	}
	offset := r.doc.Scroll().Offset()
	r.doc.SetItems(items)
	r.doc.Scroll().ScrollTo(offset)
}

// addContext keeps contextLines next to each neighbouring conflict and
// collapses the rest.
func (r *Resolver) addContext(lines []string, first int, before, after bool) {
	keepHead, keepTail := 0, 0
	if before {
		keepHead = contextLines
	}
	if after {
		keepTail = contextLines
	}
	if !before && !after {
		keepHead = len(lines)
	}
	// Collapsing a single line saves nothing; show it instead.
	if keepHead+keepTail+1 >= len(lines) {
		for i, text := range lines {
			r.rows = append(r.rows, docRow{kind: rowContext, text: text, line: first + i, conflict: -1})
		}
		return
	}
	for i := 0; i < keepHead; i++ {
		r.rows = append(r.rows, docRow{kind: rowContext, text: lines[i], line: first + i, conflict: -1})
	}
	hidden := len(lines) - keepHead - keepTail
	r.rows = append(r.rows, docRow{kind: rowCollapsed, text: fmt.Sprintf("⋯ %d unchanged line%s", hidden, plural(hidden)), conflict: -1})
	for i := len(lines) - keepTail; i < len(lines); i++ {
		r.rows = append(r.rows, docRow{kind: rowContext, text: lines[i], line: first + i, conflict: -1})
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

func (r *Resolver) headerRow(conflict int) int {
	for i, row := range r.rows {
		if row.kind == rowHeader && row.conflict == conflict {
			return i
		}
	}
	return 0
}

// revealCurrent scrolls so the current conflict's header is near the top.
func (r *Resolver) revealCurrent() {
	scroll := r.doc.Scroll()
	row := r.headerRow(r.current)
	if row < scroll.Offset() || row >= scroll.Offset()+scroll.Viewport()-2 {
		scroll.ScrollTo(max(0, row-1))
	}
}

func (r *Resolver) Children() []ui.Component {
	children := []ui.Component{r.doc, r.actions}
	if r.listW > 0 {
		children = append([]ui.Component{r.files}, children...)
	}
	return children
}

func (r *Resolver) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

// Layout: summary row, then the file list beside the document, then actions.
func (r *Resolver) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	r.SetBounds(bounds)
	bodyY, bodyH := 2, max(0, bounds.H-4)
	r.listW = 0
	if bounds.W >= 80 {
		r.listW = min(34, bounds.W/4)
	}
	r.files.Layout(ctx, ui.Rect{Y: bodyY, W: r.listW, H: bodyH})
	docX := 0
	if r.listW > 0 {
		docX = r.listW + 1
	}
	r.doc.Layout(ctx, ui.Rect{X: docX, Y: bodyY, W: max(0, bounds.W-docX), H: bodyH})
	r.actions.Layout(ctx, ui.Rect{Y: max(0, bounds.H-1), W: bounds.W, H: min(1, bounds.H)})
}

func (r *Resolver) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	bounds := r.Bounds()
	tokens := r.tokens
	bg := tokens.Color(ui.ColorPopupBg)
	x, y := originX+bounds.X, originY+bounds.Y
	doing, _ := operationLabel(r.operation)
	summary := fmt.Sprintf("%s · %d file%s with conflicts", doing, len(r.conflicts), plural(len(r.conflicts)))
	role := ui.ColorFg
	switch {
	case r.errText != "":
		summary, role = "✕ "+r.errText, ui.ColorError
	case r.busy != "":
		summary, role = r.busy+"…", ui.ColorAccent
	case r.loading != "":
		summary, role = "Reading "+r.loading+"…", ui.ColorMuted
	case len(r.conflicts) == 0:
		summary, role = "✓ Every conflict is resolved · "+strings.ToLower(doing[:1])+doing[1:]+" can be completed", ui.ColorSuccess
	case r.file != nil:
		decided := 0
		for _, choice := range r.choices {
			if choice != workspace.GitChooseNone {
				decided++
			}
		}
		summary += fmt.Sprintf(" · %s: %d of %d decided", r.file.Path, decided, len(r.choices))
	}
	buf.WriteString(x, y, rendering.TruncateVisual(summary, bounds.W, "…"), tokens.Color(role), bg, cell.AttrBold)
	hint := "1 keep current · 2 take incoming · 3 keep both · 0 undecide · n/p next/previous conflict"
	buf.WriteString(x, y+1, rendering.TruncateVisual(hint, bounds.W, "…"), tokens.Color(ui.ColorMuted), bg, 0)
	if r.listW > 0 {
		for row := 2; row < bounds.H-2; row++ {
			buf.WriteString(x+r.listW, y+row, "│", tokens.Color(ui.ColorPopupDivider), bg, 0)
		}
	}
	if r.file == nil && r.loading == "" {
		message := "Select a conflicted file"
		if len(r.conflicts) == 0 {
			_, finish := operationLabel(r.operation)
			message = "Every file is resolved and staged. " + finish + " to record it, or Abort to return to the branch as it was."
		}
		buf.WriteString(x+r.listW+3, y+3, rendering.TruncateVisual(message, max(0, bounds.W-r.listW-3), "…"), tokens.Color(ui.ColorMuted), bg, 0)
	}
}

func choiceLabel(choice workspace.GitConflictChoice) (string, ui.SemanticColor) {
	switch choice {
	case workspace.GitChooseCurrent:
		return "keeping current", ui.ColorSuccess
	case workspace.GitChooseIncoming:
		return "taking incoming", ui.ColorSuccess
	case workspace.GitChooseBoth:
		return "keeping both", ui.ColorSuccess
	}
	return "undecided", ui.ColorWarning
}

// kept reports whether the row's side survives the conflict's choice.
func (r *Resolver) kept(row docRow) bool {
	if row.conflict < 0 || row.conflict >= len(r.choices) {
		return true
	}
	switch r.choices[row.conflict] {
	case workspace.GitChooseCurrent:
		return row.side == sideCurrent
	case workspace.GitChooseIncoming:
		return row.side == sideIncoming
	case workspace.GitChooseBoth:
		return row.side != sideBase
	}
	return true
}

// markerLabel shortens a full commit id (a pull merges a commit, not a
// branch) to the abbreviation Git prints elsewhere.
func markerLabel(label string) string {
	if len(label) == 40 && strings.Trim(label, "0123456789abcdef") == "" {
		return label[:7]
	}
	return label
}

func displayText(text string) string {
	return strings.ReplaceAll(strings.TrimRight(text, "\r\n"), "\t", "    ")
}

func (r *Resolver) paintRow(buf *surface.CellBuffer, x, y, width int, item ui.VirtItem, _ int, _ ui.ControlState, _ ui.PaintContext, tokens *ui.TokenSet) {
	index, _ := item.Payload.(int)
	if index < 0 || index >= len(r.rows) {
		return
	}
	row := r.rows[index]
	bg := tokens.Color(ui.ColorPopupBg)
	put := func(col int, text string, fg ui.SemanticColor, rowBg cell.Color, attr cell.Attrs) int {
		if col >= width {
			return col
		}
		text = rendering.TruncateVisual(text, width-col, "…")
		buf.WriteString(x+col, y, text, tokens.Color(fg), rowBg, attr)
		return col + rendering.VisibleWidth(text)
	}
	fill := func(rowBg cell.Color) {
		buf.WriteString(x, y, strings.Repeat(" ", max(0, width)), tokens.Color(ui.ColorFg), rowBg, 0)
	}
	gutter := func(line int, role ui.SemanticColor, rowBg cell.Color) int {
		label := "     "
		if line > 0 {
			label = fmt.Sprintf("%4d ", line)
		}
		return put(0, label, role, rowBg, 0)
	}
	switch row.kind {
	case rowContext:
		col := gutter(row.line, ui.ColorMuted, bg)
		put(col+2, displayText(row.text), ui.ColorMuted, bg, 0)
	case rowCollapsed:
		put(7, row.text, ui.ColorMuted, bg, cell.AttrItalic)
	case rowSpacer:
	case rowHeader:
		rowBg := bg
		if row.conflict == r.current {
			rowBg = tokens.Color(ui.ColorHover)
			fill(rowBg)
		}
		marker := "  "
		if row.conflict == r.current {
			marker = "▶ "
		}
		col := put(0, marker, ui.ColorAccent, rowBg, cell.AttrBold)
		col = put(col, fmt.Sprintf("Conflict %d of %d", row.conflict+1, len(r.choices)), ui.ColorFg, rowBg, cell.AttrBold)
		label, role := choiceLabel(r.choices[row.conflict])
		put(col+3, label, role, rowBg, 0)
	case rowLabel:
		names := map[side]string{sideCurrent: "Current", sideBase: "Base", sideIncoming: "Incoming"}
		roles := map[side]ui.SemanticColor{sideCurrent: ui.ColorDiffAddition, sideBase: ui.ColorMuted, sideIncoming: ui.ColorAccent}
		keys := map[side]string{sideCurrent: "  [1] keep", sideIncoming: "  [2] take"}
		text := "▍" + names[row.side]
		if row.text != "" {
			text += " · " + markerLabel(row.text)
		}
		col := put(5, text, roles[row.side], bg, cell.AttrBold)
		put(col, keys[row.side], ui.ColorMuted, bg, 0)
	case rowLine:
		rowBg, role, bar := bg, ui.ColorFg, ui.ColorMuted
		switch row.side {
		case sideCurrent:
			rowBg, bar = tokens.Color(ui.ColorDiffAdditionBg), ui.ColorDiffAddition
		case sideIncoming:
			rowBg, bar = tokens.Color(ui.ColorPopupSubtleBg), ui.ColorAccent
		case sideBase:
			role = ui.ColorMuted
		}
		attr := cell.Attrs(0)
		if !r.kept(row) {
			rowBg, role, attr = bg, ui.ColorMuted, cell.AttrStrikethrough
		}
		fill(rowBg)
		col := gutter(row.line, ui.ColorMuted, rowBg)
		col = put(col, "▍ ", bar, rowBg, 0)
		put(col, displayText(row.text), role, rowBg, attr)
	}
}

func (r *Resolver) nextConflict(delta int) {
	if len(r.choices) == 0 {
		return
	}
	r.current = (r.current + delta + len(r.choices)) % len(r.choices)
	r.revealCurrent()
}

func (r *Resolver) emit(decision Decision) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	if r.decide != nil {
		result.Cmd = r.decide(decision)
	}
	return result
}

func (r *Resolver) act(action string) ui.EventResult {
	switch action {
	case actionSave:
		if r.file == nil || !r.decided() || r.busy != "" {
			return ui.EventResult{Handled: true}
		}
		return r.emit(Decision{Kind: DecideSave, Path: r.file.Path, Fingerprint: r.file.Fingerprint, Choices: r.Choices()})
	case actionOpen:
		if r.file == nil {
			return ui.EventResult{Handled: true}
		}
		line := 1
		if len(r.choices) > 0 {
			line = r.rows[r.headerRow(r.current)].line
		}
		return r.emit(Decision{Kind: DecideOpen, Path: r.file.Path, Line: line})
	case actionAbort:
		return r.emit(Decision{Kind: DecideAbort})
	case actionComplete:
		if len(r.conflicts) > 0 {
			return ui.EventResult{Handled: true}
		}
		return r.emit(Decision{Kind: DecideComplete})
	case actionClose:
		return r.emit(Decision{Kind: DecideClose})
	case actionFile:
		if item, ok := r.files.Selected(); ok {
			if path, ok := item.Payload.(string); ok && (r.file == nil || r.file.Path != path) {
				return r.emit(Decision{Kind: DecideSelect, Path: path})
			}
		}
		return ui.EventResult{Handled: true}
	}
	return ui.EventResult{}
}

func (r *Resolver) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	scroll := r.doc.Scroll()
	switch evt.Kind {
	case ui.EventMouseWheel:
		switch evt.MouseWheel.Button {
		case tea.MouseWheelUp:
			scroll.ScrollBy(-3)
		case tea.MouseWheelDown:
			scroll.ScrollBy(3)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
	case ui.EventMouseClick:
		if evt.MouseClick.Button != tea.MouseLeft {
			return ui.EventResult{}
		}
		docBounds := r.doc.Bounds()
		if !docBounds.Contains(ctx.LocalX, ctx.LocalY) {
			return ui.EventResult{}
		}
		item, ok := r.doc.ItemAtViewportRow(ctx.LocalY - docBounds.Y)
		if !ok {
			return ui.EventResult{Handled: true}
		}
		row := r.rows[item.Payload.(int)]
		if row.conflict < 0 {
			return ui.EventResult{Handled: true}
		}
		r.current = row.conflict
		// Clicking a side's label or lines chooses that side.
		switch row.side {
		case sideCurrent:
			r.Choose(row.conflict, workspace.GitChooseCurrent)
		case sideIncoming:
			r.Choose(row.conflict, workspace.GitChooseIncoming)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	}
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	key := evt.Key.String()
	switch key {
	case "1":
		r.Choose(r.current, workspace.GitChooseCurrent)
	case "2":
		r.Choose(r.current, workspace.GitChooseIncoming)
	case "3":
		r.Choose(r.current, workspace.GitChooseBoth)
	case "0":
		r.Choose(r.current, workspace.GitChooseNone)
	case "n", "j":
		r.nextConflict(1)
	case "p", "k":
		r.nextConflict(-1)
	case "e":
		return r.act(actionOpen)
	case "ctrl+s":
		return r.act(actionSave)
	case "esc":
		return r.act(actionClose)
	case "up":
		scroll.ScrollBy(-1)
	case "down":
		scroll.ScrollBy(1)
	case "pgup":
		scroll.ScrollBy(-max(1, scroll.Viewport()-1))
	case "pgdown":
		scroll.ScrollBy(max(1, scroll.Viewport()-1))
	case "home":
		scroll.ScrollTo(0)
	case "end":
		scroll.ScrollTo(scroll.MaxOffset())
	default:
		return ui.EventResult{}
	}
	r.syncActions()
	return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (r *Resolver) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	if result := r.act(action.ID); result.Handled {
		return result
	}
	return ui.EventResult{Action: &action}
}

func (r *Resolver) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleDialog, Label: "Resolve conflicts", Description: fmt.Sprintf("%d conflicted files", len(r.conflicts))}
}
