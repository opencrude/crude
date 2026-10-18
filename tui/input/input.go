package input

import (
	tea "charm.land/bubbletea/v2"
	"sort"
	"strings"

	"crude/history"
	"crude/sessiondb"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
)

const slashPageSize = 8

type Event interface{ event() }

type SubmitEvent struct {
	Text          string
	ReferenceIDs  []string
	DraftRevision uint64
}

func (SubmitEvent) event() {}

type SteeringSubmitEvent struct {
	Text          string
	ReferenceIDs  []string
	DraftRevision uint64
}

func (SteeringSubmitEvent) event() {}

type SlashCommandEvent struct {
	Command      string
	ReferenceIDs []string
}

func (SlashCommandEvent) event() {}

type SettingsOpenEvent struct{}

func (SettingsOpenEvent) event() {}

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
	{Name: "history", Label: "history", Desc: "Search and reuse prompts with their attachments"},
	{Name: "undo", Label: "undo", Desc: "Revert this agent's files; retain conversation history"},
	{Name: "yolo", Label: "yolo", Desc: "Disable ALL safety checks (DANGEROUS)"},
	{Name: "sessions", Label: "sessions", Desc: "Browse and resume past sessions"},
	{Name: "memory", Label: "memory", Desc: "Browse durable workspace memory"},
	{Name: "skills", Label: "skills", Desc: "Search and activate or deactivate skills"},
	{Name: "mcps", Label: "mcps", Desc: "Search and activate or deactivate MCPs"},
	{Name: "compact", Label: "compact", Desc: "Summarize older conversation history to free context"},
	{Name: "fast", Label: "fast", Desc: "Toggle the provider's fast tier for this session's main agent"},
	{Name: "retry", Label: "retry", Desc: "Continue this agent's pending recovery without replaying completed work"},
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
	history               []history.Entry
	histIdx               int
	histDraft, histPrefix string
	historyGeneration     uint64
	historyLoading        bool
	draftEpoch            uint64
	draftRevision         uint64
	pasteSequence         uint64
	pendingPastes         map[uint64]bool
	attachmentOffset      int
	promptReferenceIDs    map[string]bool
	histDraftReferenceIDs []string

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

// NewDraft creates a separate editor with the same shell configuration. Draft
// text, undo, history navigation and attachment identities remain session-owned.
func (m Model) NewDraft() Model {
	next := NewModel(m.theme)
	next.SetActionRegistry(m.actionRegistry)
	next.SetHistory(m.history)
	if m.width > 0 {
		next.SetWidth(m.width)
	}
	next.SetSpinFrame(m.spinFrame)
	next.showHint = m.showHint
	return next
}

// SuspendDraft fences asynchronous recall/paste/admission from an earlier
// activation without discarding the retained editor and its undo/history state.
func (m *Model) SuspendDraft() {
	m.CancelPointerCapture()
	m.invalidatePendingPastes()
	m.historyGeneration++
	m.historyLoading = false
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
// The guidance persists while the prompt has text, keeping every chord
// actionable mid-draft (Enter sends, the newline chord breaks the line), and only
// hides via DismissHint. Callers with a full ActionRegistry should prefer
// these ActionSeg values.
func (m *Model) ContextHelpSegments() []ui.HintSegment {
	if m.historyLoading {
		return []ui.HintSegment{ui.TextSeg("Restoring prompt and attachments...")}
	}
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
			out = append(out, ui.ChordSeg(ui.FormatShortcut(ui.NewlineChord), "new line"))
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
	m.textWidth = m.textWidthFor(w)
	m.input.SetTextWidth(m.textWidth)
}

func (m *Model) SetSpinFrame(frame int) {
	m.spinFrame = frame
}

func (m *Model) SetHistory(h []history.Entry) {
	m.history = h
}

func (m Model) History() []history.Entry {
	return m.history
}

func (m *Model) SetInput(s string) {
	m.promptReferenceIDs = nil
	m.SetValuePreserveReferences(s)
	m.syncSlashState()
}

func (m *Model) ClearInput() {
	m.invalidatePendingPastes()
	m.promptReferenceIDs = nil
	m.historyGeneration++
	m.historyLoading = false
	m.input.Clear()
	m.histIdx = -1
	m.histDraft = ""
	m.histPrefix = ""
	m.slashActive = false
	m.draftRevision++
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
	return m.textWidthFor(m.width)
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

	if bounds := m.input.Bounds(); bounds.H > 0 && relY >= bounds.H {
		return -1, false
	}
	pad, _ := m.inputGeometry(m.width)
	return m.input.MouseToFlatPos(mouseX-pad, relY), true
}

// InputSelecting reports a drag or selection in progress: true while the
// pointer owns the composer (IsSelecting) and true for a live selection.
func (m Model) InputSelecting() bool { return m.input.HasActiveSelection() || m.input.IsSelecting() }

// InputDragging distinguishes pointer capture from a retained keyboard range.
func (m Model) InputDragging() bool { return m.input.IsSelecting() }

// HasInputSelection reports an active non-empty text selection, ignoring an
// in-flight pointer drag (InputSelecting covers that).
func (m Model) HasInputSelection() bool { return m.input.HasActiveSelection() }

func (m Model) SelectedText() string { return m.input.SelectedText() }

func (m Model) InputSelectionRange() (start, end int) {
	return m.input.SelectionRange()
}

func (m *Model) ClearInputSelection() {
	defer m.recordDraftEdit(m.input.EditState())
	m.input.ClearSelection()
}

// CancelPointerCapture resets the composer's text-drag state when the Host
// revokes pointer capture without a matching release (another window claimed
// the pointer, the slot was hidden). It forwards to the TextArea so the drag
// flag can never outlive the pointer capture that justified it.
func (m *Model) CancelPointerCapture() { m.input.CancelPointerCapture() }

// CursorFlat returns the flat rune index of the text cursor. Attachment
// flows (context-menu remove/rename) use it to keep the caret stable while
// the draft text is rewritten.
func (m Model) CursorFlat() int { return m.input.CursorFlat() }

func (m Model) ScrollOffset() int { return m.input.ScrollOffset() }

func (m *Model) SetScrollOffset(offset int) {
	defer m.recordDraftEdit(m.input.EditState())
	m.input.SetScrollOffset(offset)
}

func (m *Model) SetSelection(anchor, end int) {
	defer m.recordDraftEdit(m.input.EditState())
	m.input.SetSelection(anchor, end)
}

func (m *Model) recordDraftEdit(before ui.TextAreaState) {
	if m.input.EditState() != before {
		m.draftRevision++
	}
}

// DraftRevision is a monotonic identity for the complete editable draft. It
// prevents a late admission result from clearing an edited or ABA-replaced
// composer value.
func (m Model) DraftRevision() uint64             { return m.draftRevision }
func (m *Model) SetDraftRevision(revision uint64) { m.draftRevision = revision }

// ClearInputAtRevision consumes only the exact draft submitted by an earlier
// command. A newer edit, selection/cursor change, or replacement is retained.
func (m *Model) ClearInputAtRevision(revision uint64) bool {
	if revision == 0 || m.draftRevision != revision {
		return false
	}
	m.ClearInput()
	return true
}

// SetCursorFlat moves the text cursor to a flat rune index.
func (m *Model) SetCursorFlat(pos int) {
	defer m.recordDraftEdit(m.input.EditState())
	m.input.SetCursorFlat(pos)
}

func (m Model) Value() string {
	return m.input.Value()
}

// SnapshotReferenceIDs returns the exact identities pinned to this draft,
// including currently unresolved attachments whose backing object is missing.
func (m Model) SnapshotReferenceIDs() []string {
	if m.promptReferenceIDs == nil {
		return m.ReferencedIDs()
	}
	ids := make([]string, 0, len(m.promptReferenceIDs))
	for id := range m.promptReferenceIDs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
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

// MaxComposerTextRows is a viewport limit, not a content limit. Typed, restored,
// and small pasted drafts remain complete while growing from one to six rows.
const MaxComposerTextRows = 6

func (m Model) inputGeometry(width int) (pad, areaWidth int) {
	width = max(0, width)
	pad = min(max(0, m.theme.Spacing.PadLR), width/2)
	return pad, width - 2*pad
}

func (m Model) textWidthFor(width int) int {
	_, areaWidth := m.inputGeometry(width)
	return max(1, areaWidth-m.input.PromptPrefixWidth())
}

func (m Model) inputVisualHeight(width int) int {
	return min(MaxComposerTextRows, max(1, m.input.VisualLineCount(m.textWidthFor(width))))
}

// ComposerViewport exposes the visible draft rows for quiet scroll chrome.
// Geometry and offset remain owned by the same TextArea that paints and hits.
func (m Model) ComposerViewport() (first, last, total int) {
	height := m.input.Bounds().H
	total = m.input.TotalVisualLines()
	if height <= 0 {
		return 0, 0, total
	}
	first = m.input.ScrollOffset() + 1
	return first, min(total, first+height-1), total
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
	pad, innerW := m.inputGeometry(width)
	y := 1 // after separator
	if len(m.activePromptRefs()) > 0 {
		y++
	}
	inputH := min(m.inputVisualHeight(width), max(0, height-y))
	m.input.Layout(ui.LayoutContext{}, ui.Rect{X: pad, Y: y, W: innerW, H: inputH})
	if m.showSlashPopup() {
		slashY := y + inputH
		slashH := max(0, height-slashY)
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
	pad, innerW := m.inputGeometry(width)

	// Separator row
	sep := strings.Repeat("─", max(0, width))
	buf.WriteString(ox, oy, sep, border, bg, 0)

	y := 1
	refs := m.activePromptRefs()
	if len(refs) > 0 && y < height {
		m.paintRefChips(buf, ox, oy, width)
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
	defer m.recordDraftEdit(m.input.EditState())
	if m.input != nil && m.input.IsSelecting() && (evt.Kind == ui.EventMouseMotion || evt.Kind == ui.EventMouseRelease) {
		return m.input.Handle(ui.ChildEventContext(ctx, m.input.Bounds()), evt)
	}
	if ctx.LocalY == 1 && len(m.activePromptRefs()) > 0 {
		if evt.Kind == ui.EventMouseWheel {
			delta := 1
			if evt.MouseWheel.Button == tea.MouseWheelUp || evt.MouseWheel.Button == tea.MouseWheelLeft {
				delta = -1
			}
			m.attachmentOffset = max(0, min(len(m.activePromptRefs())-1, m.attachmentOffset+delta))
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
		}
		if evt.Kind == ui.EventMouseClick && evt.MouseClick.Button == tea.MouseLeft {
			for _, chip := range m.ReferenceChips(m.width) {
				if chip.Body.Contains(ctx.LocalX, ctx.LocalY) {
					if chip.ID == "" {
						m.attachmentOffset = chip.Next
						return ui.EventResult{Handled: true, Invalidate: ui.InvalidatePaint}
					}
					return ui.EventResult{Handled: true, Action: &ui.Action{ID: "input.attachment.open", Payload: chip.ID}}
				}
				if chip.Remove.Contains(ctx.LocalX, ctx.LocalY) {
					return ui.EventResult{Handled: true, Action: &ui.Action{ID: "input.attachment.remove", Payload: chip.ID}}
				}
			}
		}
	}
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

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
