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
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

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
	maxRawPreview   = 48 << 10  // escaped output, each
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
	tokens    *ui.TokenSet
	list      *ui.List
	scrollbar *ui.ScrollbarControl
	text      *reader
	actions   *ui.ActionBar
	result    types.LSPInlaySet
	hasResult bool
	selected  int // retained result ordinal
	rowEpoch  uint64
	busy      bool
	inspected bool
	hint      types.LSPInlayHint
	original  string
	resolved  string
	state     string
	detail    string
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
	s.original, s.resolved, s.state, s.detail = "", "", "", ""
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
	s.setText("Requesting inlay hints…\n\nWaiting for source/provider/context-qualified evidence.\n" +
		"This reader is read-only. No commands, edits or label locations are executed.")
	s.rebuildActions()
}

func (s *Screen) SetError(message string) {
	s.reset() // Failure never leaves an old Inspect or Resolve action active.
	if len(message) > maxFieldBytes || !utf8.ValidString(message) {
		message = "Invalid or oversized failure display data."
	}
	s.setText("Inlay hints failed\n\nFailure (literal): " + strconv.Quote(message) +
		"\n\nRead-only: no commands, edits or label locations were executed.")
	s.rebuildActions()
}

func (s *Screen) SetResult(result types.LSPInlaySet) {
	if !validResult(result) {
		s.SetError("Invalid or oversized inlay display data; the answer was refused.")
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
			// Position and original index precede the label: equal anchors and
			// long shared labels stay disambiguated even in a 60-column body.
			items[i] = ui.ListItem{
				ID: fmt.Sprintf("%d/%d", s.rowEpoch, hint.Index), Payload: i, Command: true,
				Label: fmt.Sprintf("%d:%d #%d %s %s", hint.Position.Line, hint.Position.Character,
					hint.Index, kindText(hint), strconv.Quote(hint.Label)),
			}
		}
		s.list.SetItems(items)
	} else {
		description := map[string]string{
			types.LSPInlayNull:        "Provider returned null.",
			types.LSPInlayEmpty:       "Provider returned an empty array [].",
			types.LSPInlayUnsupported: "Inlay hints are unsupported for this source/provider.",
		}[result.Answer]
		s.setText(description + "\n\nThis is not proof that no hints exist or that the project is clean.\n\n" + s.provenanceText())
	}
	s.rebuildActions()
}

// SetInspection refuses cross-row, stale, malformed and changed-geometry input.
// Raw JSON is validated, never decoded/reconstructed; only its bounded escaped
// literal preview is retained. Possession of display identities grants no authority.
func (s *Screen) SetInspection(in types.LSPInlayInspection) {
	if !s.hasResult || s.selected < 0 || in.SetID != s.result.ID ||
		in.Digest != s.result.Digest || in.Index != s.result.Hints[s.selected].Index ||
		in.WireVersion != s.result.WireVersion || in.Range != s.result.Range ||
		!sameBoundary(in.Boundary, s.result.Boundary) || !validInspection(in) ||
		!sameGeometry(in.Hint, s.result.Hints[s.selected]) {
		s.SetError("Invalid, mismatched or oversized inlay inspection display data; the inspection was refused.")
		return
	}
	s.hint = cloneHint(in.Hint)
	s.original, s.resolved = rawPreview(in.OriginalJSON), rawPreview(in.ResolvedJSON)
	s.state, s.detail = in.ResolveState, in.Detail
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
	s.text.SetValue(limitText(value, maxDisplayBytes))
	s.text.SetCursorFlat(0)
	s.text.SetScrollOffset(0)
}

func kindText(h types.LSPInlayHint) string {
	if !h.HasKind {
		return "kind absent"
	}
	switch h.Kind {
	case 1:
		return "type"
	case 2:
		return "parameter"
	default:
		return fmt.Sprintf("kind %d", h.Kind)
	}
}

func formatRange(r types.LSPInlayRange) string {
	return fmt.Sprintf("[%d:%d, %d:%d] anchors", r.Start.Line, r.Start.Character, r.End.Line, r.End.Character)
}

func (s *Screen) provenanceText() string {
	r, b := s.result, s.result.Boundary
	// Every untrusted string is quoted, including unknown-input descriptions.
	var out strings.Builder
	fmt.Fprintf(&out, "Read-only: unversioned standard hints.\n"+
		"Commands, text edits and label locations are inert metadata; never executed.\n"+
		"Unknown-input limits: even an empty UnknownInputs list is not proof of complete build context or semantic freshness.\n\n"+
		"Set ID: %q\nDigest: %q\nView ID: %q\nSequence: %d\nAnswer: %q\n"+
		"Source wire version: %d\nRequested range (zero-based UTF-16): %s\n"+
		"Expiry: %q\nResolve tooltips supported: %t\nDetail (literal): %q\n\n"+
		"Source kind: %q\nWorkspace generation: %q\nDocument ID: %q\nSource path: %q\n"+
		"Source lifetime: %q\nSource revision: %d\n"+
		"Provider: %q\nProvider instance: %q\nTool identity: %q\n"+
		"Context ID: %q\nContext root: %q\nContext generation: %d\nUnknownInputs:\n",
		r.ID, r.Digest, r.ViewID, r.Sequence, r.Answer, r.WireVersion, formatRange(r.Range),
		r.ExpiresAt.String(), r.ResolveSupported, r.Detail,
		b.Source.Kind, b.Source.WorkspaceGeneration, b.Source.DocumentID, b.Source.Path,
		b.Source.Lifetime, b.Source.Revision, b.Producer.Provider, b.Producer.InstanceID,
		b.Producer.ToolIdentity, b.Context.ID, b.Context.Root, b.Context.Generation)
	if len(b.Context.UnknownInputs) == 0 {
		out.WriteString("  [] (not proof of completeness)\n")
	}
	for _, value := range b.Context.UnknownInputs {
		fmt.Fprintf(&out, "  %q\n", value)
	}
	return out.String()
}

func markupText(out *strings.Builder, label string, m types.LSPInlayMarkup) {
	fmt.Fprintf(out, "%s present: %t; form: %q; kind: %q\n%s value (literal): %q\n",
		label, m.Present, m.Form, m.Kind, label, m.Value)
}

func (s *Screen) detailText() string {
	h := s.result.Hints[s.selected]
	if s.inspected {
		h = s.hint
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Inlay hint #%d · %s\nPosition (zero-based UTF-16): %d:%d\n"+
		"Source byte: %d\nLabel (literal): %q\nLabel parts form: %t\n"+
		"Padding left/right: %t/%t\nInert text edits present: %t; opaque data present: %t\n",
		h.Index, kindText(h), h.Position.Line, h.Position.Character, h.SourceByte,
		h.Label, h.LabelIsParts, h.PaddingLeft, h.PaddingRight, h.HasTextEdits, h.HasData)
	markupText(&out, "Tooltip", h.Tooltip)
	for i, part := range h.Parts {
		fmt.Fprintf(&out, "\nPart %d value (literal): %q\nInert command present: %t\n", i, part.Value, part.HasCommand)
		markupText(&out, "Part tooltip", part.Tooltip)
		if part.Location == nil {
			out.WriteString("Inert label location: absent\n")
		} else {
			fmt.Fprintf(&out, "Inert label location URI: %q\nLocation range: %s\n",
				part.Location.URI, formatRange(part.Location.Range))
		}
	}
	out.WriteString("\n" + s.provenanceText())
	if !s.inspected {
		out.WriteString("\nOriginal raw evidence: awaiting selected-row inspection.\nNo tooltips have been resolved by this reader.\n")
	} else {
		fmt.Fprintf(&out, "\nResolve state (literal): %q\nInspection detail (literal): %q\n"+
			"Raw previews are quoted literal bytes, not reconstructed JSON. Explicit output limits apply.\n"+
			"Original raw JSON:\n%s\n\nResolved raw JSON:\n%s\n", s.state, s.detail, s.original, s.resolved)
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
		buttons = append(buttons, ui.ActionBarButton{ID: "inlay/actions/inspect", Label: "Inspect",
			Action: ActionInspect, Enabled: !s.busy, Variant: ui.VariantPrimary})
	} else if s.hasResult && s.selected >= 0 {
		s.actions.SetCancelAction(ActionBack)
		buttons = append(buttons,
			ui.ActionBarButton{ID: "inlay/actions/resolve", Label: "Resolve tooltips",
				Action: ActionResolve, Enabled: s.canResolve(), Variant: ui.VariantPrimary},
			ui.ActionBarButton{ID: "inlay/actions/back", Label: "Back", Action: ActionBack,
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
	return []ui.Component{s.text, s.actions}
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: 100, H: 24})
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
		s.text.Layout(ctx, ui.Rect{W: w, H: bodyH})
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
				ok = ok && payload == "Inspect"
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
	return ui.Semantics{Role: ui.RoleGroup, Label: "Read-only source-qualified inlay hints"}
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
	suffix := "\n[OUTPUT TRUNCATED: bounded literal preview; remaining evidence is not displayed.]\n"
	n := limit - len(suffix)
	for n > 0 && !utf8.RuneStart(value[n]) {
		n--
	}
	return value[:n] + suffix
}

func rawPreview(raw []byte) string {
	if len(raw) == 0 {
		return "Not supplied (no resolved raw evidence)."
	}
	// Quote at most the output cap's worth of input before the final cap, so
	// large accepted operands do not create multi-megabyte display intermediates.
	n := min(len(raw), maxRawPreview/6)
	for n < len(raw) && n > 0 && !utf8.RuneStart(raw[n]) {
		n--
	}
	quoted := strconv.Quote(string(raw[:n]))
	if n != len(raw) {
		quoted += fmt.Sprintf("\n[OUTPUT TRUNCATED: literal prefix of %d/%d raw bytes; output preview limit %d bytes.]", n, len(raw), maxRawPreview)
	}
	return quoted
}
