// Package inlay presents inert, source-qualified inlay evidence. It never reads
// a file, calls a provider, edits text, or executes commands or label locations.
package inlay

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
)

const (
	ScreenID  ui.ComponentID = "inlay"
	ListID    ui.ComponentID = "inlay/list"
	TextID    ui.ComponentID = "inlay/detail"
	ActionsID ui.ComponentID = "inlay/actions"

	ActionInspect = "inlay.inspect"
	ActionResolve = "inlay.resolve"
	ActionBack    = "inlay.back"
	ActionClose   = "inlay.close"

	maxHints        = 1024
	maxParts        = 32
	maxFieldBytes   = 64 << 10
	maxSummaryBytes = 128 << 10 // escaped aggregate caller fields
	maxRawBytes     = 1 << 20   // each original/resolved object
	maxDisplayBytes = 256 << 10 // complete reader, including generated labels
)

type InspectMsg struct {
	Target  *Screen
	Index   int // ORIGINAL response index, never the visible row ordinal
	Resolve bool
}

type CloseMsg struct{ Target *Screen }
type BackMsg struct{ Target *Screen }

// Screen retains detached accepted display only. After external Set calls the
// parent invalidates Host layout/content and focuses DefaultFocus. No Set call
// dispatches asynchronous work; InspectMsg is an intent for that parent.
type Screen struct {
	ui.Base
	tokens          *ui.TokenSet
	list            *ui.List
	scrollbar       *ui.ScrollbarControl
	detailScrollbar *ui.ScrollbarControl
	text            *reader
	actions         *ui.ActionBar
	result          types.LSPInlaySet
	hasResult       bool
	selected        int // retained result ordinal
	rowEpoch        uint64
	busy            bool
	inspected       bool
	hint            types.LSPInlayHint
	state           string
	textWidth       int
	widthGeneration uint64
}

func New(th *theme.Theme) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	ts := ui.NewTokenSet(th)
	s := &Screen{Base: ui.NewBase(ScreenID), selected: -1}
	s.list = ui.NewList(ListID, ts)
	s.list.SetDefaultRowBackground(ui.ColorPopupBg)
	s.list.SetActivateAction(ActionInspect)
	s.scrollbar = ui.NewScrollbarControl("inlay/scrollbar", s.list.Scroll(), ts)
	s.text = &reader{ui.NewTextArea(TextID, ts, true)}
	s.text.SetReadOnly(true)
	s.detailScrollbar = ui.NewScrollbarControl("inlay/detail-scrollbar", s.text.Scroll(), ts)
	s.detailScrollbar.SetPointerTargetSize(1)
	s.detailScrollbar.SetSurfaceBackground(ui.ColorPopupBg)
	s.actions = ui.NewActionBar(ActionsID, ts)
	s.actions.SetShowRule(true)
	s.actions.SetSurfaceBackground(ui.ColorPopupBg)
	s.SetTokens(ts)
	s.SetLoading()
	return s
}

func (s *Screen) SetTokens(ts *ui.TokenSet) {
	if ts == nil {
		return
	}
	s.tokens = ts
	s.list.SetTokens(ts)
	s.scrollbar.SetTokens(ts)
	s.detailScrollbar.SetTokens(ts)
	s.text.SetTokens(ts)
	s.text.SetBackgroundColor(ts.Color(ui.ColorPopupBg))
	s.actions.SetTokens(ts)
}

func (s *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		s.SetTokens(ui.NewTokenSet(th))
	}
}

func (s *Screen) clearInspection() {
	s.inspected = false
	s.hint = types.LSPInlayHint{}
	s.state = ""
}

func (s *Screen) reset() {
	s.result = types.LSPInlaySet{}
	s.hasResult, s.selected, s.busy = false, -1, false
	s.rowEpoch++
	s.clearInspection()
	s.list.SetItems(nil)
}

func (s *Screen) SetLoading() {
	s.reset()
	s.busy = true
	s.setText("Loading inlay hints…")
	s.rebuildActions()
}

// Diagnostic details stay in the caller's log, not in the reading surface.
func (s *Screen) SetError() {
	s.reset()
	s.setText("Could not load inlay hints.\n\nClose. Use Refresh Inlay Hints in the command palette.")
	s.rebuildActions()
}

func (s *Screen) SetResult(result types.LSPInlaySet) {
	if !validResult(result) {
		s.SetError()
		return
	}
	s.reset()
	s.result = result
	s.result.Boundary.Context.UnknownInputs = slices.Clone(result.Boundary.Context.UnknownInputs)
	s.result.Hints = slices.Clone(result.Hints)
	for i := range s.result.Hints {
		s.result.Hints[i] = cloneHint(s.result.Hints[i])
	}
	s.hasResult = true
	if len(result.Hints) > 0 {
		items := make([]ui.ListItem, len(result.Hints))
		for i, hint := range result.Hints {
			location := "Line " + strconv.FormatUint(uint64(hint.Position.Line)+1, 10)
			if kind := kindText(hint); kind != "" {
				location += " · " + kind
			}
			items[i] = ui.ListItem{ID: fmt.Sprintf("%d/%d", s.rowEpoch, hint.Index), Payload: i, Command: true,
				Label: singleLine(hint.Label), Detail: location}
		}
		s.list.SetItems(items)
	} else {
		message := "No inlay hints in the visible lines."
		if result.Answer == types.LSPInlayUnsupported {
			message = "This language server does not support inlay hints."
		}
		s.setText(message)
	}
	s.rebuildActions()
}

// SetInspection refuses cross-row, stale, malformed and changed-geometry input.
// Raw JSON is validated, but never shown or retained by this reading surface.
// Possession of display identities grants no authority.
func (s *Screen) SetInspection(in types.LSPInlayInspection) {
	if !s.hasResult || s.selected < 0 || in.SetID != s.result.ID ||
		in.Digest != s.result.Digest || in.Index != s.result.Hints[s.selected].Index ||
		in.WireVersion != s.result.WireVersion || in.Range != s.result.Range ||
		!sameBoundary(in.Boundary, s.result.Boundary) || !validInspection(in) ||
		!sameGeometry(in.Hint, s.result.Hints[s.selected]) {
		s.SetError()
		return
	}
	s.hint = cloneHint(in.Hint)
	s.state = in.ResolveState
	s.inspected, s.busy = true, false
	s.setText(s.detailText())
	s.rebuildActions()
}

func (s *Screen) SetBusy(busy bool) {
	s.busy = busy
	s.rebuildActions()
}

func (s *Screen) InList() bool {
	return s.hasResult && len(s.result.Hints) > 0 && s.selected < 0
}

func (s *Screen) InDetail() bool { return s.hasResult && s.selected >= 0 }

func (s *Screen) SelectedIndex() (int, bool) {
	if !s.InDetail() {
		return 0, false
	}
	return s.result.Hints[s.selected].Index, true
}

func (s *Screen) DefaultFocus() ui.ComponentID {
	if s.InList() {
		return ListID
	}
	return ActionsID // Close is the safe initial action in every reader state.
}

func (s *Screen) setText(value string) {
	s.text.CancelPointerCapture()
	value = limitText(value, maxDisplayBytes)
	// Capture the profile before measuring. If calibration publishes while
	// measuring, the next Measure sees a new generation and recalculates.
	s.widthGeneration = rendering.WidthProfileGeneration()
	s.textWidth = max(1, ui.TooltipFrameWidth(value, 70)-ui.TooltipChrome)
	s.text.SetValue(value)
	s.text.SetCursorFlat(0)
	s.text.SetScrollOffset(0)
}

func kindText(h types.LSPInlayHint) string {
	if !h.HasKind {
		return ""
	}
	switch h.Kind {
	case 1:
		return "Type"
	case 2:
		return "Parameter"
	default:
		return ""
	}
}

// Treat provider text as literal content. Preserve useful line breaks and tabs;
// escape terminal controls, bidirectional controls and Unicode line separators
// instead of interpreting ANSI, links or commands. List rows use the same
// escaping on a single line.
func literal(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	var out strings.Builder
	for _, r := range value {
		if (unicode.IsControl(r) && r != '\n' && r != '\t') || unicode.Is(unicode.Bidi_Control, r) || r == '\u2028' || r == '\u2029' {
			q := strconv.QuoteRune(r)
			out.WriteString(q[1 : len(q)-1])
		} else {
			out.WriteRune(r)
		}
	}
	return out.String()
}
func singleLine(value string) string {
	return strings.NewReplacer("\n", `\n`, "\t", `\t`).Replace(literal(value))
}

func (s *Screen) detailText() string {
	h := s.result.Hints[s.selected]
	if s.inspected {
		h = s.hint
	}
	var out strings.Builder
	out.WriteString(singleLine(h.Label))
	hasDetails := false
	if h.Tooltip.Present && strings.TrimSpace(h.Tooltip.Value) != "" {
		out.WriteString("\n\n")
		out.WriteString(literal(h.Tooltip.Value))
		hasDetails = true
	}
	for _, part := range h.Parts {
		if !part.Tooltip.Present || strings.TrimSpace(part.Tooltip.Value) == "" {
			continue
		}
		out.WriteString("\n\n")
		out.WriteString(singleLine(part.Value))
		out.WriteByte('\n')
		out.WriteString(literal(part.Tooltip.Value))
		hasDetails = true
	}
	if !hasDetails {
		switch {
		case !s.inspected:
			out.WriteString("\n\nLoading hint details…")
		case s.result.ResolveSupported && s.state != "resolved-tooltips":
			out.WriteString("\n\nNo details yet. Select Load details.")
		default:
			out.WriteString("\n\nNo details are available.")
		}
	}
	return out.String()
}

func (s *Screen) canResolve() bool {
	return s.hasResult && s.selected >= 0 && s.inspected && s.result.ResolveSupported && !s.busy
}

func (s *Screen) rebuildActions() {
	s.actions.SetCancelAction(ActionClose)
	var buttons []ui.ActionBarButton
	if s.InList() {
		buttons = append(buttons, ui.ActionBarButton{ID: "inlay/actions/inspect", Label: "Show details",
			Action: ActionInspect, Enabled: !s.busy, Variant: ui.VariantPrimary})
	} else if s.hasResult && s.selected >= 0 {
		s.actions.SetCancelAction(ActionBack)
		if s.result.ResolveSupported {
			buttons = append(buttons, ui.ActionBarButton{ID: "inlay/actions/resolve", Label: "Load details",
				Action: ActionResolve, Enabled: s.canResolve(), Variant: ui.VariantPrimary})
		}
		buttons = append(buttons, ui.ActionBarButton{ID: "inlay/actions/back", Label: "Back", Action: ActionBack,
			Enabled: true, Variant: ui.VariantSecondary})
	}
	buttons = append(buttons, ui.ActionBarButton{ID: "inlay/actions/close", Label: "Close",
		Action: ActionClose, Enabled: true, Variant: ui.VariantSecondary})
	s.actions.SetButtons(buttons...)
	s.actions.FocusLast()
	s.actions.Layout(ui.LayoutContext{}, s.actions.Bounds())
}

func (s *Screen) Children() []ui.Component {
	if s.InList() {
		return []ui.Component{s.list, s.scrollbar, s.actions}
	}
	return []ui.Component{s.text, s.detailScrollbar, s.actions}
}

func (s *Screen) Measure(ctx ui.LayoutContext, cs ui.Constraints) ui.Size {
	actions := s.actions.Measure(ctx, cs)
	width, height := 0, 0
	if s.InList() {
		list := s.list.ContentSize(cs, 10)
		width, height = list.W, list.H
	} else {
		if generation := rendering.WidthProfileGeneration(); s.widthGeneration != generation {
			s.textWidth = max(1, ui.TooltipFrameWidth(s.text.Value(), 70)-ui.TooltipChrome)
			s.widthGeneration = generation
		}
		width = max(1, s.textWidth+ui.ScrollbarWidth)
	}
	width = min(max(width, actions.W), max(1, cs.MaxW))
	if !s.InList() {
		height = s.text.VisualLineCount(max(1, width-ui.ScrollbarWidth))
	}
	return cs.ClampSize(ui.Size{W: width, H: max(1, height) + s.actions.PreferredHeight()})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w, h := max(0, r.W), max(0, r.H)
	s.actions.SetShowRule(h > 1)
	barH := min(h, s.actions.PreferredHeight())
	bodyH := h - barH
	if s.InList() {
		barW := 0
		if w > 0 && s.scrollbar.NeededForViewport(bodyH) {
			barW = 1
		}
		s.list.Layout(ctx, ui.Rect{W: w - barW, H: bodyH})
		s.scrollbar.Layout(ctx, ui.Rect{X: w - barW, W: barW, H: bodyH})
	} else {
		barW := 0
		if w > 1 && bodyH > 0 && s.text.VisualLineCount(w) > bodyH {
			barW = ui.ScrollbarWidth
		}
		s.text.Layout(ctx, ui.Rect{W: w - barW, H: bodyH})
		s.detailScrollbar.Layout(ctx, ui.Rect{X: w - barW, W: barW, H: bodyH})
	}
	s.actions.Layout(ctx, ui.Rect{Y: bodyH, W: w, H: barH})
}

func (s *Screen) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}

func (s *Screen) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() {
		switch event.Key.Code {
		case tea.KeyTab:
			return focus(ActionsID)
		case tea.KeyEsc:
			action := ActionClose
			if s.hasResult && s.selected >= 0 {
				action = ActionBack
			}
			return s.HandleAction(ctx, ui.Action{ID: action})
		}
	}
	return ui.EventResult{}
}

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	switch action.ID {
	case ActionClose:
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return CloseMsg{s} }}
	case ActionInspect:
		if !s.InList() || s.busy {
			return ui.EventResult{Handled: true}
		}
		row, ok := s.list.Selected()
		if action.Payload != nil {
			switch payload := action.Payload.(type) {
			case ui.ListItem:
				row = payload
			case string:
				// Native Button activation carries its literal label, unlike
				// List activation's exact retained row.
				ok = ok && payload == "Show details"
			default:
				ok = false
			}
		}
		if !ok {
			return ui.EventResult{Handled: true}
		}
		ordinal, ok := row.Payload.(int)
		if !ok || ordinal < 0 || ordinal >= len(s.result.Hints) || row != s.list.Items()[ordinal] {
			return ui.EventResult{Handled: true}
		}
		s.selected = ordinal
		s.clearInspection()
		s.busy = true
		s.setText(s.detailText())
		s.rebuildActions()
		index := s.result.Hints[ordinal].Index
		result := focus(ActionsID)
		result.Cmd = func() tea.Msg { return InspectMsg{s, index, false} }
		return result
	case ActionResolve:
		if !s.canResolve() {
			return ui.EventResult{Handled: true}
		}
		index := s.result.Hints[s.selected].Index
		s.SetBusy(true)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint,
			Cmd: func() tea.Msg { return InspectMsg{s, index, true} }}
	case ActionBack:
		if !s.hasResult || s.selected < 0 {
			return ui.EventResult{Handled: true}
		}
		s.selected = -1
		s.clearInspection()
		s.rebuildActions()
		result := focus(ListID)
		result.Cmd = func() tea.Msg { return BackMsg{Target: s} }
		return result
	case "actionbar.exit.backward", "actionbar.exit.forward":
		if s.InList() {
			return focus(ListID)
		}
		return focus(TextID)
	}
	return ui.EventResult{}
}

func focus(id ui.ComponentID) ui.EventResult {
	return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: id},
		Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
}

func (s *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Inlay hints"}
}

// Only Tab ownership changes; native selection/copy/scroll and Escape's
// selection-first lifecycle remain TextArea responsibilities.
type reader struct{ *ui.TextArea }

func (r *reader) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyTab {
		return focus(ActionsID)
	}
	return r.TextArea.Handle(ctx, event)
}

func cloneHint(h types.LSPInlayHint) types.LSPInlayHint {
	h.Parts = slices.Clone(h.Parts)
	for i := range h.Parts {
		if h.Parts[i].Location != nil {
			location := *h.Parts[i].Location
			h.Parts[i].Location = &location
		}
	}
	return h
}

func sameBoundary(a, b types.InsightBoundary) bool {
	return a.Source == b.Source && a.Producer == b.Producer && a.Context.ID == b.Context.ID &&
		a.Context.Root == b.Context.Root && a.Context.Generation == b.Context.Generation &&
		slices.Equal(a.Context.UnknownInputs, b.Context.UnknownInputs)
}

func sameGeometry(a, b types.LSPInlayHint) bool {
	if a.Index != b.Index || a.SourceByte != b.SourceByte || a.Position != b.Position ||
		a.Label != b.Label || a.LabelIsParts != b.LabelIsParts || a.Kind != b.Kind ||
		a.HasKind != b.HasKind || a.PaddingLeft != b.PaddingLeft || a.PaddingRight != b.PaddingRight ||
		a.HasTextEdits != b.HasTextEdits || a.HasData != b.HasData || len(a.Parts) != len(b.Parts) {
		return false
	}
	for i, p := range a.Parts {
		q := b.Parts[i]
		if p.Value != q.Value || p.HasCommand != q.HasCommand ||
			(p.Location == nil) != (q.Location == nil) ||
			p.Location != nil && *p.Location != *q.Location {
			return false
		}
	}
	return true
}

// budget validates before cloning/quoting caller data. Escaped lengths bound
// adversarial control expansion as well as aggregate input size.
type budget int

func (b *budget) text(values ...string) bool {
	for _, value := range values {
		if len(value) > maxFieldBytes || !utf8.ValidString(value) {
			return false
		}
		*b += budget(len(strconv.Quote(value)))
		if *b > maxSummaryBytes {
			return false
		}
	}
	return true
}

func (b *budget) boundary(v types.InsightBoundary) bool {
	if len(v.Context.UnknownInputs) > maxHints || !b.text(v.Source.WorkspaceGeneration,
		v.Source.DocumentID, v.Source.Path, v.Source.Lifetime, v.Source.Kind, v.Context.ID,
		v.Context.Root, v.Producer.InstanceID, v.Producer.Provider, v.Producer.ToolIdentity) {
		return false
	}
	return b.text(v.Context.UnknownInputs...)
}

func validPosition(p types.LSPInlayPosition) bool { return p.Line >= 0 && p.Character >= 0 }

func validRange(r types.LSPInlayRange) bool {
	return validPosition(r.Start) && validPosition(r.End) &&
		(r.Start.Line < r.End.Line || r.Start.Line == r.End.Line && r.Start.Character <= r.End.Character)
}

func (b *budget) markup(m types.LSPInlayMarkup) bool {
	if !b.text(m.Form, m.Kind, m.Value) {
		return false
	}
	if !m.Present {
		return m.Form == "" && m.Kind == "" && m.Value == ""
	}
	return m.Form == "string" && m.Kind == "" ||
		m.Form == "markup" && (m.Kind == "plaintext" || m.Kind == "markdown")
}

func (b *budget) hint(h types.LSPInlayHint) bool {
	if h.Index < 0 || h.SourceByte < 0 || !validPosition(h.Position) || len(h.Label) > 4096 ||
		len(h.Parts) > maxParts || !b.text(h.Label) || !b.markup(h.Tooltip) ||
		h.LabelIsParts != (len(h.Parts) > 0) {
		return false
	}
	var label strings.Builder
	for _, part := range h.Parts {
		if len(part.Value) > 4096 || !b.text(part.Value) || !b.markup(part.Tooltip) ||
			part.Location != nil && (!b.text(part.Location.URI) || !validRange(part.Location.Range)) {
			return false
		}
		label.WriteString(part.Value)
	}
	return !h.LabelIsParts || label.String() == h.Label
}

func validResult(r types.LSPInlaySet) bool {
	switch r.Answer {
	case types.LSPInlayHints:
		if len(r.Hints) == 0 || r.ID == "" || r.Digest == "" {
			return false
		}
	case types.LSPInlayNull, types.LSPInlayEmpty, types.LSPInlayUnsupported:
		if len(r.Hints) != 0 {
			return false
		}
	default:
		return false
	}
	var b budget
	if len(r.Hints) > maxHints || r.WireVersion < 0 || !validRange(r.Range) ||
		!b.text(r.ID, r.Digest, r.ViewID, r.Detail, r.ExpiresAt.String()) || !b.boundary(r.Boundary) {
		return false
	}
	seen := make(map[int]bool, len(r.Hints))
	for _, h := range r.Hints {
		if seen[h.Index] || !b.hint(h) {
			return false
		}
		seen[h.Index] = true
	}
	return true
}

func validRaw(raw []byte, required bool) bool {
	if len(raw) == 0 {
		return !required
	}
	if len(raw) > maxRawBytes || !utf8.Valid(raw) {
		return false
	}
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && trimmed[0] == '{' && json.Valid(trimmed)
}

func validInspection(in types.LSPInlayInspection) bool {
	var b budget
	return b.text(in.SetID, in.Digest, in.ResolveState, in.Detail) && b.boundary(in.Boundary) &&
		b.hint(in.Hint) && in.Hint.Index == in.Index &&
		validRaw(in.OriginalJSON, true) && validRaw(in.ResolvedJSON, false)
}

func limitText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	suffix := "\n[Some details are not shown.]\n"
	n := limit - len(suffix)
	for n > 0 && !utf8.RuneStart(value[n]) {
		n--
	}
	return value[:n] + suffix
}
