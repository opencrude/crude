package input

import (
	"strings"

	"crude/sessiondb"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const slashPageSize = 8

type Event interface{ event() }

type SubmitEvent struct {
	Text         string
	ReferenceIDs []string
}

func (SubmitEvent) event() {}

type BtwSubmitEvent struct {
	Text         string
	ReferenceIDs []string
}

func (BtwSubmitEvent) event() {}

type SlashCommandEvent struct{ Command string }

func (SlashCommandEvent) event() {}

type SettingsOpenEvent struct{}

func (SettingsOpenEvent) event() {}

type HistorySaveEvent struct{ History []string }

func (HistorySaveEvent) event() {}

type AttachFilesEvent struct{}

func (AttachFilesEvent) event() {}

type CmdInfo struct {
	Name  string
	Label string
	Desc  string
	Icon  string
}

var availableCmds = []CmdInfo{
	{Name: "settings", Label: "settings", Desc: "Open session AI settings (models and multi-agent)"},
	{Name: "model", Label: "model", Desc: "Choose this session's model"},
	{Name: "undo", Label: "undo", Desc: "Undo last file operation"},
	{Name: "undo-n", Label: "undo N", Desc: "Undo last N file operations"},
	{Name: "undo-all", Label: "undo all", Desc: "Undo all file operations this session"},
	{Name: "yolo", Label: "yolo", Desc: "Disable ALL safety checks (DANGEROUS)"},
	{Name: "sessions", Label: "sessions", Desc: "Browse and resume past sessions"},
	{Name: "memory", Label: "memory", Desc: "Browse durable workspace memory"},
	{Name: "compact", Label: "compact", Desc: "Summarize older conversation history to free context"},
	{Name: "help", Label: "help", Desc: "Open help"},
}

func AvailableCmds() []CmdInfo {
	return availableCmds
}

func cmdsToItems(cmds []CmdInfo) []ui.ListItem {
	items := make([]ui.ListItem, len(cmds))
	for i, c := range cmds {
		items[i] = ui.ListItem{
			ID:     c.Name,
			Label:  c.Label,
			Detail: c.Desc,
			Icon:   c.Icon,
		}
	}
	return items
}

type Model struct {
	theme                 *theme.Theme
	tokens                *ui.TokenSet
	input                 *ui.TextArea
	history               []string
	histIdx               int
	histDraft, histPrefix string

	slashActive bool
	slashList   *ui.List
	slashFilter string

	showHint  bool
	width     int
	textWidth int
	thinking  bool
	blocking  bool
	spinFrame int
	refStore  ReferenceStore

	actionRegistry ActionRegistry
}

// ActionRegistry is the interface the input model needs to derive
// shortcut labels. Pointer so the value-type Model can carry it.
type ActionRegistry interface {
	HintFor(id string) string
	HintLabelFor(id string) string
}

// ActionRegistryAdapter bridges a concrete action registry (whose
// methods take a typed ActionID parameter) to the input model's
// string-based interface. Use it in the app layer where the concrete
// type is already known.
type ActionRegistryAdapter struct {
	HintForFn      func(id string) string
	HintLabelForFn func(id string) string
}

func (a ActionRegistryAdapter) HintFor(id string) string {
	if a.HintForFn != nil {
		return a.HintForFn(id)
	}
	return ""
}

func (a ActionRegistryAdapter) HintLabelFor(id string) string {
	if a.HintLabelForFn != nil {
		return a.HintLabelForFn(id)
	}
	return ""
}

// SetActionRegistry sets the action registry for shortcut derivation.
func (m *Model) SetActionRegistry(r ActionRegistry) {
	m.actionRegistry = r
}

func NewModel(t *theme.Theme) Model {
	tokens := ui.NewTokenSet(t)
	slashList := ui.NewList("input/slash", tokens)
	slashList.SetActivateAction("slash.select")
	slashList.SetDefaultRowBackground(ui.ColorBg)
	slashList.SetItems(cmdsToItems(availableCmds))

	m := Model{
		theme:     t,
		tokens:    tokens,
		input:     ui.NewTextArea("input/chat", tokens, true),
		slashList: slashList,
		histIdx:   -1,
		showHint:  true,
	}
	m.syncShellChrome()
	return m
}

// SetTokens updates theme tokens on the TextArea and slash list.
func (m *Model) SetTokens(ts *ui.TokenSet) {
	m.tokens = ts
	if m.input != nil {
		m.input.SetTokens(ts)
	}
	if m.slashList != nil {
		m.slashList.SetTokens(ts)
		m.slashList.SetDefaultRowBackground(ui.ColorBg)
	}
	m.syncShellChrome()
}

// syncShellChrome keeps the docked chat composer on the shell surface color.
// InputColors defaults to popup fill, which is correct for dialogs but paints
// a raised box under the agents prompt.
func (m *Model) syncShellChrome() {
	if m.input == nil || m.tokens == nil {
		return
	}
	m.input.SetBackgroundColor(m.tokens.Color(ui.ColorBg))
}

// SetTheme replaces the theme used for spacing and colors.
func (m *Model) SetTheme(t *theme.Theme) {
	if t == nil {
		return
	}
	m.theme = t
	m.SetTokens(ui.NewTokenSet(t))
	m.SetWidth(m.width)
}

func (m *Model) ShowHint() bool {
	return m.showHint
}

// ContextHint returns composer guidance for the shared bottom status region.
// The input view itself only renders editable content and slash results.
func (m *Model) ContextHint() string {
	h := ui.NewHint("input-hint", nil, nil)
	h.SetSegments(m.resolvedContextHelp())
	return h.PlainText()
}

// ContextHelpSegments returns status-bar hint segments for the chat input.
// The guidance persists while the prompt has text — every chord stays
// actionable mid-draft (Enter sends, Alt+Enter breaks the line) — and only
// hides via DismissHint. Callers with a full ActionRegistry should prefer
// these ActionSeg values.
func (m *Model) ContextHelpSegments() []ui.HintSegment {
	if !m.showHint {
		return nil
	}
	if m.thinking && !m.blocking {
		return []ui.HintSegment{
			ui.TextSeg("type to send a note to the agent"),
			ui.ActionSeg("input.submit"),
			ui.ChordSeg("Esc", "cancel"),
		}
	}
	return []ui.HintSegment{
		ui.ActionSeg("input.submit"),
		ui.ActionSeg("input.newline"),
		ui.ActionSeg("file.attach"),
		ui.ActionSeg("input.history"),
		ui.ActionSeg("input.cancel"),
	}
}

func (m *Model) resolvedContextHelp() []ui.HintSegment {
	segs := m.ContextHelpSegments()
	if len(segs) == 0 {
		return nil
	}
	out := make([]ui.HintSegment, 0, len(segs))
	for _, seg := range segs {
		if seg.Kind != ui.HintKindAction {
			out = append(out, seg)
			continue
		}
		if m.actionRegistry != nil {
			if hl := m.actionRegistry.HintLabelFor(string(seg.ActionID)); hl != "" {
				parts := strings.SplitN(hl, " ", 2)
				if len(parts) == 2 {
					out = append(out, ui.ChordSeg(parts[0], parts[1]))
					continue
				}
				out = append(out, ui.TextSeg(hl))
				continue
			}
		}
		// Fallback when registry/adapter cannot resolve the action.
		switch seg.ActionID {
		case "input.submit":
			out = append(out, ui.ChordSeg("Enter", "send"))
		case "input.newline":
			out = append(out, ui.ChordSeg("Alt+Enter", "new line"))
		case "file.attach":
			out = append(out, ui.ChordSeg("Ctrl+O", "attach file"))
		case "input.history":
			out = append(out, ui.ChordSeg("↑↓", "history"))
		case "input.cancel":
			out = append(out, ui.ChordSeg("Esc", "exit"))
		default:
			out = append(out, seg)
		}
	}
	return out
}

func (m *Model) DismissHint() {
	m.showHint = false
}

func (m *Model) SetState(thinking, blocking bool) {
	m.thinking = thinking
	m.blocking = blocking
}

func (m *Model) SetWidth(w int) {
	m.width = w
	m.textWidth = w - m.theme.Spacing.PadLR*2 - 2
	if m.textWidth < 1 {
		m.textWidth = 1
	}
	m.input.SetTextWidth(m.textWidth)
}

func (m *Model) SetSpinFrame(frame int) {
	m.spinFrame = frame
}

func (m *Model) SetHistory(h []string) {
	m.history = h
}

func (m Model) History() []string {
	return m.history
}

func (m *Model) SetInput(s string) {
	m.SetValuePreserveReferences(s)
	m.syncSlashState()
}

func (m *Model) ClearInput() {
	m.input.Clear()
	m.histIdx = -1
	m.histDraft = ""
	m.histPrefix = ""
	m.slashActive = false
}

func (m *Model) SetPrompt(p string) {
	m.input.SetPrompt(p)
}

func (m *Model) ResetPrompt() {
	m.input.SetPrompt("")
}

func (m Model) Height() int {
	return m.input.Height()
}

func (m Model) InputTextWidth() int {
	w := m.width - m.theme.Spacing.PadLR*2 - 2
	if w < 1 {
		w = 1
	}
	return w
}

// textAreaY returns the shell-local Y of the TextArea's first text row:
// one separator row, plus one chip row when reference chips are shown.
// MouseToInputPos maps shellinput-local Y against this row so a mouse hit
// lands on the same text position the shellinput screen paints.
func (m Model) textAreaY() int {
	y := 1 // after separator
	if len(m.activePromptRefs()) > 0 {
		y++
	}
	return y
}

// MouseToInputPos maps a shellinput-LOCAL point (relative to the input shell
// rect, as delivered by the Host hit tree) to a flat rune index in the input
// value. It returns ok=false when the point is above the text area.
func (m Model) MouseToInputPos(mouseX, mouseY int) (runeIdx int, ok bool) {
	relY := mouseY - m.textAreaY()
	if relY < 0 {
		return -1, false
	}

	textW := m.textWidth
	if textW < 1 {
		textW = m.width - 5
		if textW < 1 {
			textW = 1
		}
	}

	lines := m.input.Lines()
	visIdx := 0
	for li, logLine := range lines {
		segments := rendering.WordWrap(string(logLine), textW)
		for si, seg := range segments {
			if visIdx == relY {
				// Text starts after theme PadLR plus any configured
				// TextArea prompt prefix (chat input has none).
				col := mouseX - m.theme.Spacing.PadLR - m.input.PromptPrefixWidth()
				if col < 0 {
					col = 0
				}
				segW := rendering.VisibleWidth(seg)
				if col > segW {
					col = segW
				}

				pos := 0
				for i := 0; i < li; i++ {
					pos += len(lines[i]) + 1
				}
				segRunes := []rune(seg)
				segRuneIdx := rendering.ColToRuneIdx(segRunes, col)
				pos += rendering.FindSegmentStart(logLine, textW, si) + segRuneIdx
				return pos, true
			}
			visIdx++
		}
	}
	return -1, false
}

func (m Model) InputSelecting() bool { return m.input.HasActiveSelection() || m.input.IsSelecting() }

func (m Model) SelectedText() string { return m.input.SelectedText() }

func (m Model) InputSelectionRange() (start, end int) {
	return m.input.SelectionRange()
}

func (m *Model) ClearInputSelection() {
	m.input.ClearSelection()
}

// CursorFlat returns the flat rune index of the text cursor. Attachment
// flows (context-menu remove/rename) use it to keep the caret stable while
// the draft text is rewritten.
func (m Model) CursorFlat() int { return m.input.CursorFlat() }

// SetCursorFlat moves the text cursor to a flat rune index.
func (m *Model) SetCursorFlat(pos int) { m.input.SetCursorFlat(pos) }

func (m Model) Value() string {
	return m.input.Value()
}

func (m Model) DisplayValue() string {
	if m.refStore == nil {
		return m.input.Value()
	}
	return DisplayReferences(m.input.Value(), m.refStore.ListReferences())
}

func (m Model) ExpandedValue() string {
	if m.refStore == nil {
		return m.input.Value()
	}
	return ExpandReferences(m.input.Value(), m.refStore.ListReferences())
}

func (m Model) PromptReferences() []sessiondb.Reference {
	if m.refStore == nil {
		return nil
	}
	ids := m.ReferencedIDs()
	refs := make([]sessiondb.Reference, 0, len(ids))
	for _, id := range ids {
		if ref, ok := m.refStore.GetReference(id); ok && ref.Active() {
			refs = append(refs, ref)
		}
	}
	return refs
}

func (m Model) SlashActive() bool {
	return m.slashActive
}

func (m Model) SlashFiltered() []CmdInfo {
	if m.slashList == nil {
		return nil
	}
	items := m.slashList.Items()
	cmds := make([]CmdInfo, len(items))
	for i, item := range items {
		cmds[i] = CmdInfo{
			Name:  item.ID,
			Label: item.Label,
			Desc:  item.Detail,
			Icon:  item.Icon,
		}
	}
	return cmds
}

// ShellHeight returns separator + chips + textarea + slash rows for width.
func (m Model) ShellHeight(width int) int {
	h := 1 // separator
	if len(m.activePromptRefs()) > 0 {
		h++
	}
	h += m.inputVisualHeight(width)
	if m.showSlashPopup() {
		n := len(m.slashList.Items())
		if n > slashPageSize {
			n = slashPageSize
		}
		if n < 1 {
			n = 1
		}
		h += n
	}
	return h
}

func (m Model) inputVisualHeight(width int) int {
	tw := width - m.theme.Spacing.PadLR*2 - 2
	if tw < 1 {
		tw = 1
	}
	h := m.input.VisualLineCount(tw)
	if h < 1 {
		h = 1
	}
	return h
}

func (m Model) showSlashPopup() bool {
	if !m.slashActive || m.slashList == nil {
		return false
	}
	inputVal := m.input.Value()
	return strings.HasPrefix(inputVal, "/") && !strings.Contains(inputVal, " ") && !strings.Contains(inputVal, "\n")
}

func (m Model) activePromptRefs() []sessiondb.Reference {
	return m.PromptReferences()
}

// LayoutShell prepares TextArea and slash list bounds for a shell paint.
func (m *Model) LayoutShell(width, height int) {
	m.width = width
	m.SetWidth(width)
	pad := m.theme.Spacing.PadLR
	innerW := width - pad*2
	if innerW < 1 {
		innerW = 1
	}
	y := 1 // after separator
	if len(m.activePromptRefs()) > 0 {
		y++
	}
	inputH := m.inputVisualHeight(width)
	if inputH > height-y {
		inputH = max(1, height-y)
	}
	m.input.Layout(ui.LayoutContext{}, ui.Rect{X: pad, Y: y, W: innerW, H: inputH})
	if m.showSlashPopup() {
		slashY := y + inputH
		slashH := height - slashY
		if slashH < 1 {
			slashH = 1
		}
		if slashH > slashPageSize {
			slashH = slashPageSize
		}
		m.slashList.Layout(ui.LayoutContext{}, ui.Rect{X: pad, Y: slashY, W: innerW, H: slashH})
	}
}

// PaintShell paints separator, chips, TextArea, and slash list into buf.
func (m Model) PaintShell(buf *surface.CellBuffer, ox, oy, width, height int, ctx ui.PaintContext) {
	if buf == nil || width < 1 || height < 1 {
		return
	}
	bg := m.tokens.Color(ui.ColorBg)
	border := m.tokens.Color(ui.ColorBorder)
	pad := m.theme.Spacing.PadLR

	// Separator row
	sep := strings.Repeat("─", max(0, width))
	buf.WriteString(ox, oy, sep, border, bg, 0)

	y := 1
	refs := m.activePromptRefs()
	if len(refs) > 0 && y < height {
		paintRefChips(buf, ox+pad, oy+y, width-pad*2, refs, m.tokens)
		y++
	}
	if y >= height {
		return
	}
	inputH := m.inputVisualHeight(width)
	remain := height - y
	if inputH > remain {
		inputH = remain
	}
	innerW := width - pad*2
	if innerW < 1 {
		innerW = 1
	}
	m.input.Layout(ui.LayoutContext{}, ui.Rect{X: pad, Y: y, W: innerW, H: inputH})
	m.input.Paint(buf, ox, oy, ui.PaintContext{Focused: ctx.Focused, Frame: m.spinFrame})
	y += inputH
	if m.showSlashPopup() && y < height {
		slashH := height - y
		if slashH > slashPageSize {
			slashH = slashPageSize
		}
		m.slashList.Layout(ui.LayoutContext{}, ui.Rect{X: pad, Y: y, W: innerW, H: slashH})
		m.slashList.Paint(buf, ox, oy, ctx)
	}
}

// HandleShell routes mouse/key events that target the shell surface locally.
func (m *Model) HandleShell(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if m.showSlashPopup() && m.slashList != nil {
		b := m.slashList.Bounds()
		if ctx.LocalY >= b.Y && ctx.LocalY < b.Y+b.H {
			local := ctx
			local.LocalY = ctx.LocalY - b.Y
			local.LocalX = ctx.LocalX - b.X
			return m.slashList.Handle(local, evt)
		}
	}
	if m.input != nil {
		b := m.input.Bounds()
		if ctx.LocalY >= b.Y && ctx.LocalY < b.Y+b.H {
			local := ctx
			local.LocalY = ctx.LocalY - b.Y
			local.LocalX = ctx.LocalX - b.X
			return m.input.Handle(local, evt)
		}
	}
	return ui.EventResult{}
}

func paintRefChips(buf *surface.CellBuffer, x, y, width int, refs []sessiondb.Reference, tokens *ui.TokenSet) {
	if width < 1 {
		return
	}
	bg := tokens.Color(ui.ColorSelection)
	fg := tokens.Color(ui.ColorAccent2)
	errC := tokens.Color(ui.ColorError)
	baseBG := tokens.Color(ui.ColorBg)
	cx := x
	remain := width
	for _, ref := range refs {
		if !ref.Active() {
			continue
		}
		label := " " + ref.DisplayName() + " "
		closeMark := "×"
		need := rendering.VisibleWidth(label) + rendering.VisibleWidth(closeMark)
		if need+1 > remain {
			break
		}
		buf.WriteString(cx, y, label, fg, bg, 0)
		cx += rendering.VisibleWidth(label)
		buf.WriteString(cx, y, closeMark, errC, bg, cell.AttrBold)
		cx += rendering.VisibleWidth(closeMark)
		if remain > need+1 {
			buf.WriteString(cx, y, " ", tokens.Color(ui.ColorFg), baseBG, 0)
			cx++
			remain -= need + 1
		} else {
			remain -= need
		}
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
