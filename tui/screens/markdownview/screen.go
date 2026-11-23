package markdownview

import (
	"math"
	"sync/atomic"

	tea "charm.land/bubbletea/v2"

	"crude/tui/cell"
	"crude/tui/editor"
	"crude/tui/rendering"
	"crude/tui/screens/panelchrome"
	"crude/tui/screens/shellchat"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types/setting"
)

// Mode is how a Markdown tab shows its file.
type Mode string

const (
	Preview Mode = setting.MarkdownViewPreview
	Split   Mode = setting.MarkdownViewSplit
	Source  Mode = setting.MarkdownViewSource
)

// ModeFromSetting returns the mode an editor setting names.
func ModeFromSetting(editor setting.Editor) Mode { return Mode(editor.MarkdownViewOrDefault()) }

// ActionMode bubbles after the user picks a mode in the toolbar. The payload
// is the new Mode.
const ActionMode = "markdownview.mode"

var modes = []struct {
	mode                  Mode
	label, nerd, fallback string
}{
	{Preview, "Preview", "\uf06e", "◧"},
	{Split, "Side by side", "\uf009", "◫"},
	{Source, "Source", "\uf121", "¶"},
}

// Next returns the mode after m in toolbar order, wrapping around.
func (m Mode) Next() Mode {
	for i, entry := range modes {
		if entry.mode == m {
			return modes[(i+1)%len(modes)].mode
		}
	}
	return Preview
}

// Split panes keep these sizes; a pane narrower than two columns of
// minSideColumns shows them one above the other.
const (
	minSideColumns = 30
	minSideRows    = 6
)

var screenSources atomic.Uint64

// Screen is one Markdown tab: viewer actions over the editor, its rendered
// preview, or both with a divider between them. The editor stays the only
// owner of the text; the preview renders the editor's buffer, unsaved edits
// included, and the two keep the same part of the file in view.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	mode      Mode
	actions   [3]ui.Component
	split     *ui.SplitModel
	divider   *ui.SplitDivider
	preview   *shellchat.Screen
	source    ui.Component
	editor    *editor.Editor
	links     rendering.MarkdownLinks
	projector *Projector
	sourceID  uint64

	parts      parts
	doc        *Document
	generation uint64
	request    Request // the last request; Text is not kept
	requested  bool
	requestSeq uint64

	// The positions last synchronized. A side that moved since then leads;
	// the other side follows it.
	syncedEditor  float64
	syncedPreview int
	syncedCaret   int
	// anchor is the source line at the preview's top. A new rendering puts
	// the same line back at the top, so editing or resizing never jumps.
	anchor float64
	// follow positions the follower from the leader at the next sync.
	follow bool
}

type parts struct {
	strip, source, divider, preview ui.Rect
}

// New returns a Markdown tab screen. deliver receives finished renderings,
// from a worker goroutine; the owner passes them back with Apply.
func New(th *theme.Theme, id ui.ComponentID, mode Mode, deliver func(tea.Msg)) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	s := &Screen{Base: ui.NewBase(id), th: th, tokens: ui.NewTokenSet(th), mode: mode,
		projector: NewProjector(deliver), sourceID: 1<<62 + screenSources.Add(1), follow: true}
	for i, entry := range modes {
		s.actions[i] = panelchrome.NewViewerButton(ui.NewID(ui.NewID(id, "toolbar"), string(entry.mode)), id, modeAction(entry.mode),
			entry.label, "", entry.nerd, entry.fallback)
	}
	s.split = ui.NewSplitModel(ui.Horizontal)
	s.divider = ui.NewSplitDivider(ui.NewID(id, "divider"), s.split)
	s.divider.SetColorRole(ui.ColorBorder)
	s.preview = shellchat.NewWithID(th, ui.NewID(id, "preview"))
	s.preview.SetDocument(true)
	s.SetMode(mode)
	s.SetTokens(s.tokens)
	return s
}

// Bind shows ed through its editor screen. links resolve the document's
// relative links.
func (s *Screen) Bind(source ui.Component, ed *editor.Editor, links rendering.MarkdownLinks) {
	if s.editor != ed {
		s.requested, s.requestSeq, s.doc = false, 0, nil
		s.preview.SetMessages(nil, shellchat.PaintParams{})
		if ed != nil {
			s.anchor, s.syncedCaret = ed.TopLine(), ed.CursorLine()
		}
	}
	if s.editor != ed || s.links != links {
		s.follow = true
	}
	s.source, s.editor, s.links = source, ed, links
}

// Mode returns the mode the tab shows.
func (s *Screen) Mode() Mode { return s.mode }

// SetMode shows the file in mode. The part of the file in view stays in view.
func (s *Screen) SetMode(mode Mode) {
	if mode != Preview && mode != Split && mode != Source {
		mode = Preview
	}
	if s.mode != mode && s.doc != nil && s.editor != nil {
		switch {
		case s.mode == Preview:
			// The preview was the only view: the editor opens where the
			// reader was, with the caret there too when it was out of view,
			// so the next key acts on the text on screen.
			top, rows := int(math.Ceil(s.anchor)), max(1, s.preview.ScrollModel().Viewport())
			if line := s.editor.CursorLine(); line < top || line >= top+rows {
				s.editor.SetCursorPosition(top, 0)
			}
			s.editor.ScrollToLine(s.anchor)
			s.syncedEditor, s.syncedCaret = s.editor.TopLine(), s.editor.CursorLine()
		case mode == Preview:
			s.anchor = s.editor.TopLine()
		}
	}
	s.mode = mode
	s.follow = true
	for i, entry := range modes {
		s.actions[i].(*panelchrome.ViewerButton).SetSelected(entry.mode == mode)
	}
}

// Preview returns the rendered document's screen.
func (s *Screen) Preview() *shellchat.Screen { return s.preview }

// ToolbarOwnsFocus reports whether one of this preview's visible actions has focus.
func (s *Screen) ToolbarOwnsFocus(id ui.ComponentID) bool {
	for _, button := range s.actions {
		if button.ID() == id && !button.Bounds().Empty() {
			return true
		}
	}
	return false
}

// NextPreviewFocus moves between the read-only document and its visible actions.
// The editable source handles Tab as normal text input instead.
func (s *Screen) NextPreviewFocus(current ui.ComponentID, backward bool) ui.ComponentID {
	i, step := -1, 1
	if backward {
		i, step = len(s.actions), -1
	}
	for at, button := range s.actions {
		if button.ID() == current {
			i = at
			break
		}
	}
	for i += step; i >= 0 && i < len(s.actions); i += step {
		if button := s.actions[i]; !button.Bounds().Empty() {
			return button.ID()
		}
	}
	return s.preview.ID()
}

// Document returns the rendering on screen, or nil before the first one.
func (s *Screen) Document() *Document { return s.doc }

// PreviewOwnsKeys reports whether keys go to the rendered document: only the
// preview is on screen.
func (s *Screen) PreviewOwnsKeys() bool { return s.mode == Preview }

// SourceRect is the editor's area in parent coordinates, or empty when the
// editor is not on screen.
func (s *Screen) SourceRect() ui.Rect { return s.offset(s.parts.source) }

// PreviewRect is the rendered document's area in parent coordinates.
func (s *Screen) PreviewRect() ui.Rect { return s.offset(s.parts.preview) }

func (s *Screen) offset(r ui.Rect) ui.Rect {
	if r.Empty() {
		return ui.Rect{}
	}
	b := s.Bounds()
	return ui.Rect{X: b.X + r.X, Y: b.Y + r.Y, W: r.W, H: r.H}
}

// Close stops rendering for a closed tab.
func (s *Screen) Close() { s.projector.Close() }

func (s *Screen) SetTheme(th *theme.Theme) {
	if th == nil {
		return
	}
	s.th = th
	s.preview.SetTheme(th)
	s.SetTokens(ui.NewTokenSet(th))
}

func (s *Screen) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	s.tokens = tokens
	for _, b := range s.actions {
		b.(*panelchrome.ViewerButton).SetTokens(tokens)
	}
	s.divider.SetTokens(tokens)
	s.preview.SetTokens(tokens)
}

// arrange computes the strip, editor, divider and preview areas, local to
// the screen.
func (s *Screen) arrange(w, h int) parts {
	var p parts
	stripH := min(1, h)
	p.strip = ui.Rect{W: w, H: stripH}
	body := ui.Rect{Y: stripH, W: w, H: max(0, h-stripH)}
	switch s.mode {
	case Source:
		p.source = body
	case Preview:
		p.preview = body
	default:
		dir, total, minSide := ui.Horizontal, body.W, minSideColumns
		if body.W < 2*minSideColumns+1 && body.H >= 2*minSideRows+1 {
			dir, total, minSide = ui.Vertical, body.H, minSideRows
		}
		s.split.SetDirection(dir)
		s.split.SetMinSizes(min(minSide, total/2), min(minSide, total/2))
		first, gap, second := s.split.Resolve(total)
		s.divider.SetCurrentFirst(first)
		if dir == ui.Horizontal {
			p.source = ui.Rect{Y: body.Y, W: first, H: body.H}
			p.divider = ui.Rect{X: first, Y: body.Y, W: gap, H: body.H}
			p.preview = ui.Rect{X: first + gap, Y: body.Y, W: second, H: body.H}
		} else {
			p.source = ui.Rect{Y: body.Y, W: body.W, H: first}
			p.divider = ui.Rect{Y: body.Y + first, W: body.W, H: gap}
			p.preview = ui.Rect{Y: body.Y + first + gap, W: body.W, H: second}
		}
	}
	return p
}

// Arrange lays the screen out in r, in parent coordinates. The owner calls it
// while it publishes geometry, before the Host lays out the frame; Layout
// applies the same areas.
func (s *Screen) Arrange(r ui.Rect) {
	s.SetBounds(r)
	s.parts = s.arrange(max(0, r.W), max(0, r.H))
}

func (s *Screen) Children() []ui.Component {
	children := append([]ui.Component(nil), s.actions[:]...)
	if s.source != nil && !s.parts.source.Empty() {
		children = append(children, s.source)
	}
	if !s.parts.divider.Empty() {
		children = append(children, s.divider)
	}
	if !s.parts.preview.Empty() {
		children = append(children, s.preview)
	}
	return children
}

func (s *Screen) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

func (s *Screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.Arrange(r)
	toolbar := s.parts.strip
	toolbar.X, toolbar.W = ui.ContentInset(toolbar.W, s.tokens.PadLR())
	panelchrome.LayoutViewerActions(ctx, toolbar, s.actions[:])
	if s.source != nil {
		s.source.Layout(ctx, s.parts.source)
	}
	s.divider.Layout(ctx, s.parts.divider)
	s.preview.Layout(ctx, s.parts.preview)
}

func (s *Screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if p := s.parts.preview; s.doc == nil && !p.Empty() {
		pad, _ := ui.ContentInset(p.W, s.tokens.PadLR())
		buf.WriteString(ox+r.X+p.X+pad, oy+r.Y+p.Y, rendering.TruncateVisual("Rendering the document…", max(0, p.W-2*pad), "…"),
			s.tokens.Color(ui.ColorMuted), bg, 0)
	}
}

func (s *Screen) Handle(ui.EventContext, ui.Event) ui.EventResult { return ui.EventResult{} }

func modeAction(mode Mode) string { return "markdownview.show-" + string(mode) }

func (s *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	for _, entry := range modes {
		if action.ID != modeAction(entry.mode) {
			continue
		}
		s.SetMode(entry.mode)
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint | ui.InvalidateFocus,
			Action: &ui.Action{ID: ActionMode, Payload: s.mode}}
	}
	if action.ID == ui.ActionSplitResize {
		if resize, ok := action.Payload.(ui.SplitResize); ok && resize.ID == s.divider.ID() {
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
		}
	}
	return ui.EventResult{Action: &action}
}

func (s *Screen) Semantics() ui.Semantics {
	label := "Markdown"
	for _, entry := range modes {
		if entry.mode == s.mode {
			label = "Markdown " + entry.label
		}
	}
	return ui.Semantics{Role: ui.RoleGroup, Label: label}
}

// Configure runs once per frame after Arrange, on the UI goroutine. It asks
// for a rendering when the source, width or theme changed, installs the
// current rendering in the preview and keeps the editor and the preview on
// the same part of the file. Nothing here waits for a rendering.
func (s *Screen) Configure(resizing bool) {
	p := s.parts.preview
	if s.editor == nil || p.Empty() {
		return
	}
	pad, innerW := ui.ContentInset(p.W, s.tokens.PadLR())
	wrap := innerW
	// Start with space for a track so a long file needs only one layout.
	// Reclaim both the track and separator when the document fits.
	if s.doc == nil || s.doc.Height > p.H {
		wrap -= ui.ScrollbarFootprint(ui.ScrollbarWidth)
	}
	wrap = max(1, wrap)
	request := Request{Revision: s.editor.Revision(), Width: wrap, Theme: s.th, Links: s.links}
	// A divider or window drag keeps the width it started with, as the chat
	// does: the document rewraps once, when the drag ends.
	if (resizing || s.split.Dragging()) && s.doc != nil && s.requested && s.request.Revision == request.Revision && s.request.Theme == request.Theme {
		request.Width = s.request.Width
	}
	if !s.requested || !s.request.same(request) {
		s.request, s.requested = request, true
		request.Text = s.editor.Value()
		s.requestSeq = s.projector.Request(request)
	}
	if s.doc == nil {
		return
	}
	content := s.doc.Height
	track := content > p.H
	// Compose removes a visible track itself. Cap the paint width at the
	// same boundary used for wrapping, even while wider rows are retained
	// during a resize or background reflow.
	inner := wrap
	if track {
		inner += ui.ScrollbarWidth
	}
	s.preview.SyncScrollMetrics(p.H, content)
	s.preview.SetMessages(s.doc.Rows, shellchat.PaintParams{
		SourceID:   s.sourceID,
		Generation: s.generation,
		Compose:    shellchat.ComposeParams{InnerW: inner, PadLR: pad, Theme: s.th, MainSBVisible: track},
	})
	s.syncScroll()
}

// Apply installs only the latest request from this screen. The last accepted
// document stays visible until a newer one is ready.
func (s *Screen) Apply(msg RenderedMsg) bool {
	if msg.projector != s.projector || msg.seq != s.requestSeq || msg.doc == nil {
		return false
	}
	if s.doc != nil && s.mode != Split {
		s.anchor = s.doc.LineForRow(float64(s.preview.ScrollModel().Offset()))
	}
	s.doc = msg.doc
	s.generation++
	s.follow = true
	return true
}

// syncScroll keeps both sides on the same part of the file. In Split the
// side that moved since the last sync leads; with only the preview on screen
// it keeps its source line, and an editor caret jump (Go to Line, a heading
// link) reveals the caret's line.
func (s *Screen) syncScroll() {
	scroll := s.preview.ScrollModel()
	top, caret := s.editor.TopLine(), s.editor.CursorLine()
	switch s.mode {
	case Split:
		switch {
		case s.follow || top != s.syncedEditor:
			scroll.ScrollTo(int(math.Round(s.doc.RowForLine(top))))
		case scroll.Offset() != s.syncedPreview:
			s.editor.ScrollToLine(s.doc.LineForRow(float64(scroll.Offset())))
			top = s.editor.TopLine()
		}
	case Preview:
		switch {
		case caret != s.syncedCaret && !s.follow:
			scroll.ScrollTo(max(0, int(math.Round(s.doc.RowForLine(float64(caret))))))
		case s.follow:
			scroll.ScrollTo(int(math.Round(s.doc.RowForLine(s.anchor))))
		}
		s.anchor = s.doc.LineForRow(float64(scroll.Offset()))
	}
	s.follow = false
	s.syncedEditor, s.syncedPreview, s.syncedCaret = top, scroll.Offset(), caret
}
