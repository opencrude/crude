package ui

import (
	"fmt"

	"crude/tui/rendering"
	"crude/tui/surface"
	"crude/tui/theme"
)

// FindReplaceFrameIDs are the component IDs inside a find/replace frame.
const (
	FRFrameID      = "find-replace"
	FRFindField    = "find-replace/find"
	FRReplaceField = "find-replace/replace"
	FRContent      = "find-replace/content"

	// FRToggleCase / FRToggleWord / FRToggleRegex are the case-sensitivity,
	// whole-word, and regex filter toggles (rendered as "Aa", "ab", ".*").
	FRToggleCase  ComponentID = "find-replace/toggle-case"
	FRToggleWord  ComponentID = "find-replace/toggle-word"
	FRToggleRegex ComponentID = "find-replace/toggle-regex"

	// FRStatus is the match counter label ("3 of 10", "No results"). It
	// shares the buttons row (FRButtonsRow): counter left, buttons right.
	FRStatus ComponentID = "find-replace/status"

	// FRFindRow is the find line: Aa/ab/.* pills on the left, find input
	// filling every remaining column (Find in Files search-row layout).
	FRFindRow ComponentID = "find-replace/find-row"

	// FRButtonsRow is the last content line: match counter on the left,
	// ui.Button actions packed to the right edge.
	FRButtonsRow ComponentID = "find-replace/buttons-row"

	// FRActions is the ActionBar inside FRButtonsRow. Find mode offers
	// [ Up ] [ Down ]; replace mode offers [ Replace ] [ Replace All ]
	// [ Up ] [ Down ] (all label-sized, right-aligned).
	FRActions ComponentID = "find-replace/actions"
)

// FindReplaceFrameSpec configures a WindowFrame-backed find/replace popover.
type FindReplaceFrameSpec struct {
	Title        string // per-tab title (editor BaseTitle); defaults to "Find"
	FindValue    string
	ReplaceValue string
	ReplaceMode  bool
	FocusedField int // 0=find, 1=replace
	Width        int
	Height       int

	// Filter toggles, mirrored from the editor's SearchState.
	CaseSensitive bool
	WholeWord     bool
	Regex         bool

	// Match counter; CurrentIdx is 0-based, -1 when there are no matches.
	MatchCount int
	CurrentIdx int
}

// frGapRow is a fixed-height vertical spacer between find/replace content
// rows. It owns no children, paints nothing, and measures a fixed H so a
// content variant with more rows (replace mode) grows the window instead of
// stealing the last row's cells (the clipped "[ Up ]/[ Down ]" overflow).
type frGapRow struct {
	Base
	h int
}

func newFRGapRow(h int) *frGapRow {
	if h < 1 {
		h = 1
	}
	return &frGapRow{Base: NewBase(NewID(FRContent, "gap")), h: h}
}

func (g *frGapRow) Measure(_ LayoutContext, cs Constraints) Size {
	return cs.ClampSize(Size{W: 0, H: g.h})
}

func (g *frGapRow) Layout(_ LayoutContext, r Rect) { g.SetBounds(r) }

func (g *frGapRow) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

func (g *frGapRow) Handle(_ EventContext, _ Event) EventResult { return EventResult{} }

func (g *frGapRow) Semantics() Semantics { return Semantics{Role: RoleGroup, Label: "gap"} }

// BuildFindReplaceFrame builds a WindowFrame popover for find/replace.
// The find row packs the find input first, then the ⌕ glyph and the
// Aa/ab/.* pills at the trailing edge ("needle ⌕ Aa ab .*"); the replace
// input is full width; the last line carries the action buttons. In find
// mode the counter shares the action row; in replace mode the counter
// gets its own row (the Replace/Replace All bar would otherwise starve
// it). Both forms share one width — the popup sizer's floor — so the
// Ctrl+F ↔ Ctrl+H transform never changes the window width (and never
// pushes it off screen).
func BuildFindReplaceFrame(th *theme.Theme, spec FindReplaceFrameSpec) *ComponentWidget {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := NewTokenSet(th)

	w := spec.Width
	if w < 38 {
		w = 38
	}
	if w > 90 {
		w = 90
	}

	// Shared width floor: the replace-mode action bar is the widest
	// natural row of either form; both forms share it so the transform
	// never changes the window width. Long query/replace text can still
	// widen beyond the floor.
	widthFloor := FindReplaceSharedWidthFloor()
	if qw := rendering.VisibleWidth(spec.FindValue); qw+6 > widthFloor {
		widthFloor = qw + 6
	}
	if spec.ReplaceMode {
		if rw := rendering.VisibleWidth(spec.ReplaceValue); rw+6 > widthFloor {
			widthFloor = rw + 6
		}
	}
	w = max(w, widthFloor)
	w = min(w, 90)

	find := NewTextField(FRFindField, tokens)
	find.SetBackgroundColor(FilterSurface(tokens))
	find.SetPlaceholder("Find")
	find.SetValue(spec.FindValue)
	find.SetPrompt(SearchGlyphEnd(tokens))
	find.SetPromptPlacement(PromptEnd)

	caseToggle := NewToggleButton(FRToggleCase, "Aa", "find.toggle-case", tokens)
	caseToggle.SetChecked(spec.CaseSensitive)
	wordToggle := NewToggleButton(FRToggleWord, "ab", "find.toggle-word", tokens)
	wordToggle.SetChecked(spec.WholeWord)
	regexToggle := NewToggleButton(FRToggleRegex, ".*", "find.toggle-regex", tokens)
	regexToggle.SetChecked(spec.Regex)

	findRow := newFRFindRow(find, caseToggle, wordToggle, regexToggle)

	var children []Component
	children = append(children, findRow)

	replace := NewTextField(FRReplaceField, tokens)
	if spec.ReplaceMode {
		replace.SetBackgroundColor(FilterSurface(tokens))
		replace.SetPlaceholder("Replace")
		replace.SetValue(spec.ReplaceValue)
		replace.SetPrompt(ReplaceGlyphEnd(tokens))
		replace.SetPromptPlacement(PromptEnd)
		children = append(children, newFRGapRow(1), replace)
	}

	statusLabel := NewLabel(FRStatus, "", th)
	statusLabel.SetColorRole(ColorMuted)
	statusLabel.SetText(matchCountText(spec.MatchCount, spec.CurrentIdx))

	navBar := NewActionBar(FRActions, tokens, findNavButtons(spec)...)
	navBar.SetAlignEnd(true)
	navBar.SetCancelAction("find.close")
	navBar.SetExitActions("find.focus.find", "find.focus.find")
	if spec.ReplaceMode {
		// Replace mode: the counter gets its own full-width row above the
		// action bar — the Replace/Replace All bar would otherwise starve
		// the counter slice on narrow terminals.
		children = append(children, newFRGapRow(1), statusLabel, newFRGapRow(1), navBar)
	} else {
		children = append(children, newFRGapRow(1), newFRButtonsRow(statusLabel, navBar))
	}

	stack := NewStack(NewID(RootID, FRContent), Vertical, 0, children...)

	frame := NewWindowFrame(NewID(RootID, FRFrameID), FramePopover, tokens)
	title := spec.Title
	if title == "" {
		title = "Find"
	}
	frame.SetTitle(title)
	frame.SetIcon("⌕")
	frame.SetClosable(true)
	frame.SetContent(stack)

	// The window height is measured from the tree: find and replace forms
	// have different content rows, and clamping here keeps Ctrl+F ↔ Ctrl+H
	// transforms truthful (no clipped button rows, no dead blank rows).
	measured := Size{W: w, H: 5}
	if m := frame.Measure(LayoutContext{Theme: th}, Constraints{MinW: w, MaxW: w, MinH: 0, MaxH: 24}); m.H > 0 {
		measured = m
	}
	h := measured.H
	if spec.Height > h {
		h = spec.Height
	}
	if h < 5 {
		h = 5
	}
	if h > 12 {
		h = 12
	}

	cw := NewComponentWidget(th, frame, w, h)
	// Focus the find field by default
	if spec.FocusedField == 1 && spec.ReplaceMode {
		cw.Focus(FRReplaceField)
	} else {
		cw.Focus(FRFindField)
	}
	return cw
}

// FindReplaceSharedWidthFloor returns the minimum total window width both
// forms share: the replace-mode action bar — the widest natural row of
// either form — plus the frame chrome. The popup sizer floors on this so
// the Ctrl+F ↔ Ctrl+H transform never changes the window width, and the
// bar never clamps into the counter. Derived from the same Button metrics
// (label + 4, gap 2) the bar paints with, so theme label changes keep the
// floor truthful.
func FindReplaceSharedWidthFloor() int {
	barW := 0
	btns := findNavButtons(FindReplaceFrameSpec{ReplaceMode: true, MatchCount: 1})
	for i, b := range btns {
		bw := rendering.VisibleWidth(b.Label) + 4
		barW += bw
		if i < len(btns)-1 {
			barW += 2 // ActionBar gap
		}
	}
	return barW + 6 // +chrome (2 borders + 4 popover padding)
}

// findNavButtons returns the action-row buttons for the spec. They share the
// confirm-dialog Button + ButtonVariant language (label-sized, action-
// semantic variants, focus accents). Replace mode adds Replace/Replace All
// in front of the shared ↑/↓ navigation pair — glyph labels keep the bar
// compact so the counter keeps room.
func findNavButtons(spec FindReplaceFrameSpec) []ActionBarButton {
	nav := []ActionBarButton{
		{ID: NewID(FRActions, "up"), Label: "↑", Action: "find.prev", Variant: VariantSecondary, Enabled: spec.MatchCount > 0},
		{ID: NewID(FRActions, "down"), Label: "↓", Action: "find.next", Variant: VariantPrimary, Enabled: spec.MatchCount > 0},
	}
	if !spec.ReplaceMode {
		return nav
	}
	replace := []ActionBarButton{
		{ID: NewID(FRActions, "one"), Label: "Replace", Action: "find.replace.one", Variant: VariantPrimary, Enabled: spec.MatchCount > 0},
		{ID: NewID(FRActions, "all"), Label: "Replace All", Action: "find.replace.all", Variant: VariantSecondary, Enabled: spec.MatchCount > 0},
	}
	return append(replace, nav...)
}

// matchCountText renders the live match counter for the status label. The
// counter shares the buttons row: it paints on the left, the buttons pack
// the right, and the row never grows the box — long counters truncate from
// the right edge of the label's slice only.
func matchCountText(matchCount, currentIdx int) string {
	switch {
	case matchCount <= 0:
		return "No results"
	case currentIdx < 0:
		return fmt.Sprintf("%d matches", matchCount)
	default:
		return fmt.Sprintf("%d of %d", currentIdx+1, matchCount)
	}
}

// frFindRow lays out the find input first and the three filter toggles
// after it on one line: the input fills every leading column, then the ⌕
// search glyph (prompt at end) and the Aa/ab/.* pills pack the trailing
// edge ("needle ⌕ Aa ab .*"). Each toggle keeps its bare label width —
// one padding cell per side so its hit target matches its painted pill.
type frFindRow struct {
	Base
	find    *TextField
	toggles [3]*ToggleButton // case, whole-word, regex
}

func newFRFindRow(find *TextField, caseToggle, wordToggle, regexToggle *ToggleButton) *frFindRow {
	return &frFindRow{
		Base:    NewBase(FRFindRow),
		find:    find,
		toggles: [3]*ToggleButton{caseToggle, wordToggle, regexToggle},
	}
}

// Children implements Parent so Host paint/hit register the field + toggles.
func (f *frFindRow) Children() []Component {
	out := []Component{f.find}
	for _, t := range f.toggles {
		out = append(out, t)
	}
	return out
}

func (f *frFindRow) Measure(ctx LayoutContext, cs Constraints) Size {
	// The find input is the flexible element; the glyph + toggles keep
	// their bare natural widths at the trailing edge.
	n := f.find.Measure(ctx, Constraints{MaxW: 1 << 30, MaxH: 1}).W
	n += f.trailingWidth(ctx)
	return cs.ClampSize(Size{W: n + 1, H: 1})
}

func (f *frFindRow) trailingWidth(ctx LayoutContext) int {
	n := f.find.promptWidth() // ⌕ glyph paints at the input's end
	for _, t := range f.toggles {
		if t == nil {
			continue
		}
		n += t.Measure(ctx, Constraints{MaxW: 1 << 30, MaxH: 1}).W
		n++ // separator cell after the glyph and between labels
	}
	return n
}

func (f *frFindRow) Layout(ctx LayoutContext, r Rect) {
	f.SetBounds(r)
	trail := f.trailingWidth(ctx)
	// The find input takes every column left of the trailing glyph + pills.
	fw := max(0, r.W-trail)
	if f.find != nil {
		f.find.Layout(ctx, Rect{X: 0, Y: 0, W: fw, H: 1})
	}
	// The glyph paints inside the input (prompt at end); the pills start
	// one cell after the input's right edge.
	x := fw + 1 // skip the separator cell after the glyph
	for _, t := range f.toggles {
		if t == nil {
			continue
		}
		tw := t.Measure(ctx, Constraints{MaxW: 1 << 30, MaxH: 1}).W
		if x+tw > r.W {
			tw = max(0, r.W-x)
		}
		t.Layout(ctx, Rect{X: x, Y: 0, W: tw, H: 1})
		x += tw
		if x < r.W {
			x++ // separator cell
		}
	}
}

// Paint is a no-op: children paint via Host (parent-local) using the bounds
// layout assigned above.
func (f *frFindRow) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

func (f *frFindRow) Handle(_ EventContext, _ Event) EventResult { return EventResult{} }

func (f *frFindRow) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: "find filters and input"}
}

// frButtonsRow lays out the match counter and the action buttons on one
// line: the live counter ("3 of 10" / "No results") hugs the left on the
// muted ink, and the ActionBar packs from the right edge. This row is the
// last content line of the popup in both forms — no separate filter row.
type frButtonsRow struct {
	Base
	status *Label
	bar    *ActionBar
}

func newFRButtonsRow(status *Label, bar *ActionBar) *frButtonsRow {
	return &frButtonsRow{
		Base:   NewBase(FRButtonsRow),
		status: status,
		bar:    bar,
	}
}

// Children implements Parent so Host paint/hit register counter + bar.
func (f *frButtonsRow) Children() []Component {
	return []Component{f.status, f.bar}
}

// minGapFR is the least separation between the counter and the buttons.
const minGapFR = 2

func (f *frButtonsRow) Measure(ctx LayoutContext, cs Constraints) Size {
	n := 0
	if f.status != nil {
		n += f.status.Measure(ctx, Constraints{MaxW: 1 << 30, MaxH: 1}).W
	}
	if f.bar != nil {
		if n > 0 {
			n += minGapFR
		}
		n += f.bar.Measure(ctx, Loose(1<<30, 1)).W
	}
	return cs.ClampSize(Size{W: n, H: 1})
}

func (f *frButtonsRow) Layout(ctx LayoutContext, r Rect) {
	f.SetBounds(r)
	barW := 0
	if f.bar != nil {
		barW = f.bar.Measure(ctx, Loose(r.W, 1)).W
		if barW > r.W {
			barW = r.W
		}
		f.bar.Layout(ctx, Rect{X: r.W - barW, Y: 0, W: barW, H: 1})
	}
	if f.status != nil {
		sw := r.W - barW - minGapFR
		if sw < 0 {
			sw = 0
		}
		f.status.Layout(ctx, Rect{X: 0, Y: 0, W: sw, H: 1})
	}
}

// Paint is a no-op: children paint via Host (parent-local) using the bounds
// layout assigned above.
func (f *frButtonsRow) Paint(_ *surface.CellBuffer, _, _ int, _ PaintContext) {}

func (f *frButtonsRow) Handle(_ EventContext, _ Event) EventResult { return EventResult{} }

func (f *frButtonsRow) Semantics() Semantics {
	return Semantics{Role: RoleGroup, Label: "find actions"}
}

// findreplaceToggles returns the three filter ToggleButtons in frame order
// (case, whole-word, regex).
func findreplaceToggles(cw *ComponentWidget) [3]*ToggleButton {
	return [3]*ToggleButton{
		findToggle(cw.Root(), FRToggleCase),
		findToggle(cw.Root(), FRToggleWord),
		findToggle(cw.Root(), FRToggleRegex),
	}
}

func findToggle(c Component, id ComponentID) *ToggleButton {
	if c == nil {
		return nil
	}
	if c.ID() == id {
		if t, ok := c.(*ToggleButton); ok {
			return t
		}
	}
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			if found := findToggle(ch, id); found != nil {
				return found
			}
		}
	}
	return nil
}

// SyncFindReplaceFrame pushes live popup state (filter toggles + match
// counter + per-tab title + action-row enabled states) into an already-built
// frame, marking the window content dirty so the change reaches the
// compositor. Text values are synced separately via the TextField helpers.
func SyncFindReplaceFrame(cw *ComponentWidget, caseSensitive, wholeWord, regex bool, matchCount, currentIdx int, title string) {
	if cw == nil {
		return
	}
	toggles := findreplaceToggles(cw)
	states := [3]bool{caseSensitive, wholeWord, regex}
	for i, t := range toggles {
		if t != nil {
			t.SetChecked(states[i])
		}
	}
	if lbl := findLabel(cw.Root(), FRStatus); lbl != nil {
		lbl.SetText(matchCountText(matchCount, currentIdx))
	}
	if bar := findActionBar(cw.Root()); bar != nil {
		hasMatches := matchCount > 0
		bar.SetEnabled("find.prev", hasMatches)
		bar.SetEnabled("find.next", hasMatches)
		bar.SetEnabled("find.replace.one", hasMatches)
		bar.SetEnabled("find.replace.all", hasMatches)
	}
	if fr := findFrame(cw.Root()); fr != nil && title != "" {
		fr.SetTitle(title)
	}
	cw.MarkContentDirty()
}

func findActionBar(c Component) *ActionBar {
	if c == nil {
		return nil
	}
	if bar, ok := c.(*ActionBar); ok && bar.ID() == FRActions {
		return bar
	}
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			if found := findActionBar(ch); found != nil {
				return found
			}
		}
	}
	return nil
}

func findFrame(c Component) *WindowFrame {
	if c == nil {
		return nil
	}
	if f, ok := c.(*WindowFrame); ok {
		return f
	}
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			if found := findFrame(ch); found != nil {
				return found
			}
		}
	}
	return nil
}

func findLabel(c Component, id ComponentID) *Label {
	if c == nil {
		return nil
	}
	if c.ID() == id {
		if l, ok := c.(*Label); ok {
			return l
		}
	}
	if p, ok := c.(Parent); ok {
		for _, ch := range p.Children() {
			if found := findLabel(ch, id); found != nil {
				return found
			}
		}
	}
	return nil
}

// FindReplaceFrameFindField returns the find TextField from a framed find/replace.
func FindReplaceFrameFindField(cw *ComponentWidget) *TextField {
	if cw == nil {
		return nil
	}
	return findTextField(cw.Root(), FRFindField)
}

// FindReplaceFrameReplaceField returns the replace TextField from a framed find/replace.
func FindReplaceFrameReplaceField(cw *ComponentWidget) *TextField {
	if cw == nil {
		return nil
	}
	return findTextField(cw.Root(), FRReplaceField)
}

// FindReplaceFrameToggleBounds reports the POPUP-LOCAL bounds of a filter
// toggle (0=case, 1=whole-word, 2=regex) as painted by the Host — the same
// screen-rect registry that drives Host.Handle's EventContext locals, so the
// reported rect is exactly the pill a click must land on. ok is false when
// the toggle has no geometry yet.
func FindReplaceFrameToggleBounds(cw *ComponentWidget, idx int) (r Rect, ok bool) {
	if cw == nil || cw.Host() == nil || idx < 0 || idx > 2 {
		return Rect{}, false
	}
	t := findreplaceToggles(cw)[idx]
	if t == nil {
		return Rect{}, false
	}
	r, ok = cw.Host().ScreenRect(t.ID())
	if !ok || r.Empty() {
		return Rect{}, false
	}
	return r, true
}

// FindReplaceActionAt maps popup-local mouse coords (window-local: title row
// is y=0, content starts at y=1) to a find/replace action-row action ID
// ("find.prev", "find.next", "find.replace.one", "find.replace.all") using
// the Host's screen-rect registry — the same geometry that drives Paint and
// Host.Handle's EventContext locals. Empty when the point is not on an
// action-row button.
func FindReplaceActionAt(cw *ComponentWidget, localX, localY int) string {
	if cw == nil || cw.Host() == nil {
		return ""
	}
	bar := findActionBar(cw.Root())
	if bar == nil {
		return ""
	}
	br, ok := cw.Host().ScreenRect(bar.ID())
	if !ok {
		return ""
	}
	// Button bounds are bar-local; ScreenRect(FRActions) is the bar's
	// popup-local origin. Translate once — never sum ancestors by hand
	// (componentOffset already includes the target's own bounds, so adding
	// bb.X/bb.Y on top double-counted the offset and shifted every button
	// below the popup).
	for _, act := range []string{"find.prev", "find.next", "find.replace.one", "find.replace.all"} {
		if r, ok := bar.ButtonBounds(act); ok && !r.Empty() {
			hit := Rect{X: br.X + r.X, Y: br.Y + r.Y, W: r.W, H: r.H}
			if hit.Contains(localX, localY) {
				return act
			}
		}
	}
	return ""
}

// FindReplaceActionBounds reports the popup-local bounds of one action-row
// button by action ID ("find.prev", "find.next", "find.replace.one",
// "find.replace.all"), derived from the Host's screen-rect registry so the
// reported rect is exactly where the button paints. ok is false when the
// button has no geometry yet (e.g. replace buttons in find mode).
func FindReplaceActionBounds(cw *ComponentWidget, action string) (r Rect, ok bool) {
	if cw == nil || cw.Host() == nil {
		return Rect{}, false
	}
	bar := findActionBar(cw.Root())
	if bar == nil {
		return Rect{}, false
	}
	br, ok := cw.Host().ScreenRect(bar.ID())
	if !ok {
		return Rect{}, false
	}
	r, ok = bar.ButtonBounds(action)
	if !ok || r.Empty() {
		return Rect{}, false
	}
	return Rect{X: br.X + r.X, Y: br.Y + r.Y, W: r.W, H: r.H}, true
}

// SetHoveredButton moves Host hover to the hovered action-row button (or
// clears it past the row) without touching keyboard focus — hover paints
// the subtle ColorHover fill while the focused control keeps its accent
// fill, keeping the two states distinct per the control-state language.
// Hover resolves through the Host's screen rects, mirroring
// FindReplaceActionAt. Returns true when the hover target changed.
func SetHoveredButton(cw *ComponentWidget, localX, localY int) bool {
	if cw == nil || cw.Host() == nil {
		return false
	}
	bar := findActionBar(cw.Root())
	if bar == nil {
		return false
	}
	host := cw.Host()
	hovered := -1
	if act := FindReplaceActionAt(cw, localX, localY); act != "" {
		hovered = bar.indexOfAction(act)
	}
	if hovered < 0 {
		if host.Hovered() == bar.ID() {
			bar.hovered = -1
			host.SetHovered(RootID)
			cw.MarkContentDirty()
			return true
		}
		return false
	}
	// Host hover targets a registered component: hover the bar itself (the
	// row) so the row paints hovered while the retained buttons keep their
	// own focused/hovered paint via ActionBar's internal hovered index.
	if host.Hovered() == bar.ID() && bar.hovered == hovered {
		return false
	}
	bar.hovered = hovered
	host.SetHovered(bar.ID())
	cw.MarkContentDirty()
	return true
}

// FindReplaceButtonEnabled reports whether the action-row button carrying
// the action ID is enabled. ok is false when no mounted button carries the
// action. Disabled buttons (e.g. navigation with zero matches) must not
// fire on clicks.
func FindReplaceButtonEnabled(cw *ComponentWidget, action string) bool {
	if cw == nil {
		return false
	}
	bar := findActionBar(cw.Root())
	if bar == nil {
		return false
	}
	return bar.ActionEnabled(action)
}

// FindReplaceButtonLabels returns the action-row button labels in layout
// order. Test seam pinning the VSCode-style action row composition.
func FindReplaceButtonLabels(cw *ComponentWidget) []string {
	if cw == nil {
		return nil
	}
	bar := findActionBar(cw.Root())
	if bar == nil {
		return nil
	}
	return bar.ButtonLabels()
}
