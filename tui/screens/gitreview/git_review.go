// Package gitreview is the main-area Git review: every changed file (or every
// file of one commit) as one continuous, hunk-focused document, with a file
// navigator that follows the reading position. It paints prepared files and
// forwards typed intents; the owner runs all repository work.
package gitreview

import (
	"fmt"
	"strings"

	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/gitdiff"
	"crude/tui/screens/panelchrome"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/workspace"
)

// ScreenPrefix starts every review component ID; each tab's screen has its
// own instance prefix so several reviews can be visible at once.
const ScreenPrefix = "gitreview"

const (
	ActionRefresh = "gitreview.refresh"
	ActionReturn  = "gitreview.return"
	// ActionOpenAll asks the owner for the unscoped all-changes review tab.
	ActionOpenAll = "gitreview.open-all"

	actionPresentation = "gitreview.presentation"
	actionCollapse     = "gitreview.collapse"
	actionNavigate     = "gitreview.navigate"
)

// Source says what the review shows.
type Source uint8

const (
	SourceChanges Source = iota // working tree and index
	SourceCommit                // one historical commit, read-only
)

// navigatorMinWidth is the narrowest review that keeps the file navigator.
const navigatorMinWidth = 110

// Screen is the main-area review tab.
type Screen struct {
	ui.Base
	th        *theme.Theme
	tokens    *ui.TokenSet
	document  *gitdiff.Document
	navigator *fileNavigator
	present   *ui.Button
	collapse  *ui.Button
	refresh   *ui.Button
	clear     *ui.Button
	split     *ui.SplitModel
	divider   *ui.SplitDivider
	focus     func(ui.ComponentID) bool
	source    Source
	all       []gitdiff.File
	scope     string // repository-relative folder or file prefix; "" = all
	commit    workspace.GitCommitSummary
	branch    string
	loading   bool
	errText   string
	navWidth  int
	find      *findBar
	findOpen  bool
	statusY   int
	bodyTop   int
}

// New creates a review with the default instance ID.
func New(th *theme.Theme) *Screen { return NewWithID(th, ScreenPrefix) }

// NewWithID creates a review whose component IDs all start with id, so
// several review tabs can be mounted (and split side by side) at once.
func NewWithID(th *theme.Theme, id ui.ComponentID) *Screen {
	if th == nil {
		th = theme.DefaultDark()
	}
	tokens := ui.NewTokenSet(th)
	screen := &Screen{Base: ui.NewBase(id), th: th, tokens: tokens}
	screen.document = gitdiff.New(ui.NewID(id, "document"), tokens)
	screen.document.SetEmptyText("No changes · the working tree matches the index and HEAD")
	screen.navigator = newFileNavigator(screen)
	screen.present = ui.NewButton(ui.NewID(id, "presentation"), "Split", actionPresentation)
	screen.collapse = ui.NewButton(ui.NewID(id, "collapse"), "Collapse all", actionCollapse)
	screen.refresh = ui.NewButton(ui.NewID(id, "refresh"), "Refresh", ActionRefresh)
	screen.clear = ui.NewButton(ui.NewID(id, "all-changes"), "All changes", ActionOpenAll)
	screen.split = ui.NewSplitModel(ui.Horizontal)
	screen.split.SetMinSizes(20, 40)
	screen.split.SetFraction(0.24)
	screen.divider = ui.NewSplitDivider(ui.NewID(id, "divider"), screen.split)
	screen.divider.SetColorRole(ui.ColorBorder)
	screen.divider.SetFill(true)
	screen.find = newFindBar(screen)
	screen.SetTokens(tokens)
	return screen
}

// DocumentID is the component that owns keyboard focus in the review.
func (screen *Screen) DocumentID() ui.ComponentID { return ui.NewID(screen.ID(), "document") }

// NavigatorID is the file navigator.
func (screen *Screen) NavigatorID() ui.ComponentID { return ui.NewID(screen.ID(), "files") }

// FindID is the find bar.
func (screen *Screen) FindID() ui.ComponentID { return ui.NewID(screen.ID(), "find") }

// DividerID is the navigator/document divider.
func (screen *Screen) DividerID() ui.ComponentID { return screen.divider.ID() }

func (screen *Screen) SetFocusHandler(handler func(ui.ComponentID) bool) { screen.focus = handler }

func (screen *Screen) SetTokens(tokens *ui.TokenSet) {
	if tokens == nil {
		return
	}
	screen.tokens = tokens
	screen.document.SetTokens(tokens)
	screen.navigator.setTokens(tokens)
	screen.find.field.SetTokens(tokens)
	screen.divider.SetTokens(tokens)
	for _, button := range []*ui.Button{screen.present, screen.collapse, screen.refresh, screen.clear} {
		button.SetTokens(tokens)
		button.SetVariant(ui.VariantSecondary)
		button.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	}
}

func (screen *Screen) SetTheme(th *theme.Theme) {
	if th != nil {
		screen.th = th
		screen.SetTokens(ui.NewTokenSet(th))
	}
}

// Source reports whether the review shows working changes or a commit.
func (screen *Screen) Source() Source { return screen.source }

// Commit is the commit shown in SourceCommit.
func (screen *Screen) Commit() workspace.GitCommitSummary { return screen.commit }

// Scope is the active path filter ("" = every change).
func (screen *Screen) Scope() string { return screen.scope }

// Title names the main tab.
func (screen *Screen) Title() string {
	if screen.source == SourceCommit {
		return "Commit " + screen.commit.ShortHash
	}
	if screen.scope != "" {
		name, _ := gitdiff.SplitPath(screen.scope)
		return "Changes · " + name
	}
	return "Changes"
}

// ShowChanges installs the working-tree change set. scope keeps its value
// unless changed with SetScope.
func (screen *Screen) ShowChanges(files []gitdiff.File, branch string) {
	if screen.source != SourceChanges {
		screen.document.SetFiles(nil)
	}
	screen.source, screen.branch = SourceChanges, branch
	screen.commit = workspace.GitCommitSummary{}
	screen.loading, screen.errText = false, ""
	screen.all = files
	screen.document.SetReadOnly(false)
	screen.apply()
}

// ShowCommit installs one commit's files, read-only.
func (screen *Screen) ShowCommit(evidence workspace.GitCommitEvidence, files []gitdiff.File) {
	screen.source, screen.commit, screen.scope = SourceCommit, evidence.Commit, ""
	screen.loading, screen.errText = false, ""
	screen.all = files
	screen.document.SetReadOnly(true)
	screen.document.SetFiles(nil)
	screen.apply()
}

// SetScope narrows working changes to a file or folder prefix. A tab keeps
// its scope for life; the all-changes review is a separate tab.
func (screen *Screen) SetScope(scope string) {
	screen.scope = strings.Trim(scope, "/")
	screen.apply()
}

func (screen *Screen) apply() {
	files := screen.all
	if screen.scope != "" {
		files = make([]gitdiff.File, 0, len(screen.all))
		for _, file := range screen.all {
			path := file.Path()
			if path == screen.scope || strings.HasPrefix(path, screen.scope+"/") {
				files = append(files, file)
			}
		}
	}
	empty := "No changes · the working tree matches the index and HEAD"
	if screen.scope != "" {
		empty = "No changes under " + screen.scope
	}
	if screen.source == SourceCommit {
		empty = "This commit changes no files"
	}
	screen.document.SetEmptyText(empty)
	screen.document.SetFiles(files)
	screen.navigator.setFiles(files)
	screen.syncLinks()
}

// Reveal scrolls the document to the file with this key.
func (screen *Screen) Reveal(key string) bool { return screen.document.RevealFile(key) }

func (screen *Screen) SetLoading(loading bool) {
	screen.loading = loading
	if loading {
		screen.errText = ""
	}
}

func (screen *Screen) SetError(err error) {
	screen.loading = false
	screen.errText = ""
	if err != nil {
		screen.errText = err.Error()
	}
}

func (screen *Screen) SetPresentation(presentation gitdiff.Presentation) {
	screen.document.SetPresentation(presentation)
	screen.syncLinks()
}

func (screen *Screen) Presentation() gitdiff.Presentation { return screen.document.Presentation() }

func (screen *Screen) Document() *gitdiff.Document { return screen.document }

// InvalidateWidthProfile is a geometry boundary; the document re-derives
// widths on its next layout, so nothing is cached here.
func (screen *Screen) InvalidateWidthProfile() {}

func (screen *Screen) syncLinks() {
	if screen.document.Presentation() == gitdiff.Split {
		screen.present.SetLabel("Unified")
	} else {
		screen.present.SetLabel("Split")
	}
	if screen.document.AllCollapsed() {
		screen.collapse.SetLabel("Expand all")
	} else {
		screen.collapse.SetLabel("Collapse all")
	}
}

func (screen *Screen) Children() []ui.Component {
	children := []ui.Component{}
	for _, child := range []ui.Component{screen.present, screen.collapse, screen.refresh, screen.clear, screen.find, screen.navigator, screen.divider, screen.document} {
		if !child.Bounds().Empty() {
			children = append(children, child)
		}
	}
	return children
}

func (screen *Screen) Measure(_ ui.LayoutContext, constraints ui.Constraints) ui.Size {
	return constraints.ClampSize(ui.Size{W: constraints.MaxW, H: constraints.MaxH})
}

func (screen *Screen) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	screen.SetBounds(bounds)
	screen.syncLinks()
	pad := min(screen.tokens.PadLR(), bounds.W/4)
	right := bounds.W - pad
	buttons := []*ui.Button{screen.refresh, screen.collapse, screen.present}
	if screen.scope != "" && screen.source == SourceChanges {
		buttons = append(buttons, screen.clear)
	} else {
		screen.clear.Layout(ctx, ui.Rect{})
	}
	for _, button := range buttons {
		w := button.Measure(ctx, ui.Loose(40, 1)).W
		if right-w-1 < bounds.W/2 || bounds.H < 4 {
			button.Layout(ctx, ui.Rect{})
			continue
		}
		right -= w
		button.Layout(ctx, ui.Rect{X: right, Y: 0, W: w, H: 1})
		right--
	}
	top := 1
	screen.statusY = top
	top++
	screen.find.Layout(ctx, ui.Rect{})
	if screen.findOpen && bounds.H > top+3 {
		screen.find.Layout(ctx, ui.Rect{X: pad, Y: top, W: max(0, min(60, bounds.W-2*pad)), H: 1})
		top++
	}
	top = min(top, bounds.H)
	screen.bodyTop = top
	body := ui.Rect{Y: top, W: bounds.W, H: max(0, bounds.H-top)}
	screen.navWidth = 0
	if bounds.W >= navigatorMinWidth && len(screen.navigator.files) > 1 {
		first, gap, _ := screen.split.Resolve(bounds.W)
		first = max(20, min(first, bounds.W-40))
		screen.navWidth = first
		screen.divider.SetCurrentFirst(first)
		screen.navigator.Layout(ctx, ui.Rect{X: 0, Y: body.Y, W: first, H: body.H})
		screen.divider.Layout(ctx, ui.Rect{X: first, Y: body.Y, W: gap, H: body.H})
		body.X, body.W = first+gap, max(0, bounds.W-first-gap)
	} else {
		screen.navigator.Layout(ctx, ui.Rect{})
		screen.divider.Layout(ctx, ui.Rect{})
	}
	screen.document.Layout(ctx, body)
}

func (screen *Screen) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	bounds := screen.Bounds()
	tokens := screen.tokens
	bg := tokens.Color(ui.ColorBg)
	ui.FillRect(buf, originX, originY, bounds, cell.BlankCell(bg))
	if bounds.Empty() {
		return
	}
	x, y := originX+bounds.X, originY+bounds.Y
	pad := min(tokens.PadLR(), bounds.W/4)
	limit := bounds.W - pad
	for _, button := range []*ui.Button{screen.present, screen.collapse, screen.refresh, screen.clear} {
		if !button.Bounds().Empty() {
			limit = min(limit, button.Bounds().X-1)
		}
	}
	col := x + pad
	end := x + max(pad, limit)
	write := func(text string, role ui.SemanticColor, attr cell.Attrs) {
		if col >= end || text == "" {
			return
		}
		text = rendering.TruncateVisual(text, end-col, "…")
		buf.WriteString(col, y, text, tokens.Color(role), bg, attr)
		col += rendering.VisibleWidth(text)
	}
	files, added, removed := screen.document.Stats()
	if screen.source == SourceCommit {
		write(screen.commit.ShortHash+"  ", ui.ColorAccent, cell.AttrBold)
		write(screen.commit.Subject, ui.ColorFg, cell.AttrBold)
		write("  "+screen.commit.Author, ui.ColorMuted, 0)
	} else {
		title := "Working tree changes"
		if screen.scope != "" {
			title = "Changes in " + screen.scope
		}
		write(title, ui.ColorFg, cell.AttrBold)
		if screen.branch != "" {
			write("  "+gitdiff.BranchIcon(panelchrome.NerdFont(screen.th))+" "+screen.branch, ui.ColorMuted, 0)
		}
	}
	noun := "files"
	if files == 1 {
		noun = "file"
	}
	write(fmt.Sprintf("   %d %s  ", files, noun), ui.ColorMuted, 0)
	write(fmt.Sprintf("+%d", added), ui.ColorDiffAddition, 0)
	write(fmt.Sprintf(" −%d", removed), ui.ColorDiffDeletion, 0)
	if screen.statusY < bounds.H {
		text, role := screen.status()
		buf.WriteString(x+pad, y+screen.statusY, rendering.TruncateVisual(text, max(0, bounds.W-2*pad), "…"), tokens.Color(role), bg, 0)
	}
}

func (screen *Screen) status() (string, ui.SemanticColor) {
	switch {
	case screen.errText != "":
		return "✕ " + screen.errText, ui.ColorError
	case screen.loading:
		return "Reading changes…", ui.ColorMuted
	case screen.findOpen && screen.document.SearchStatus() != "":
		return "Find · " + screen.document.SearchStatus() + " · Enter next · Shift+Enter previous · Esc close", ui.ColorMuted
	case screen.source == SourceCommit:
		return "Read-only · drag to copy · Ctrl+F find · ]/[ files · n/p hunks · double-click opens the line", ui.ColorMuted
	}
	return "s/u file · +/− hunk · x discard hunk · drag to copy · Ctrl+F find · ]/[ files · n/p hunks · z fold", ui.ColorMuted
}

func (screen *Screen) Handle(_ ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.String() {
	case "ctrl+f":
		screen.openFind()
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: "ui.focus", Payload: screen.FindID()}, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
	case "esc":
		if screen.findOpen {
			screen.closeFind()
			return ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidateFocus | ui.InvalidatePaint}
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionReturn, Payload: screen}}
	case "ctrl+r":
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: ActionRefresh}}
	case "v":
		return screen.HandleAction(ui.EventContext{}, ui.Action{ID: actionPresentation})
	case "tab", "shift+tab":
		target := screen.DocumentID()
		if !screen.navigator.focused && screen.navWidth > 0 {
			target = screen.NavigatorID()
		}
		if screen.focus != nil {
			screen.focus(target)
		}
		return ui.EventResult{Handled: true, Invalidate: ui.InvalidateFocus | ui.InvalidatePaint}
	}
	return ui.EventResult{}
}

func (screen *Screen) HandleAction(_ ui.EventContext, action ui.Action) ui.EventResult {
	result := ui.EventResult{Handled: true, Invalidate: ui.InvalidateLayout | ui.InvalidatePaint}
	switch action.ID {
	case actionPresentation:
		if screen.document.Presentation() == gitdiff.Split {
			screen.SetPresentation(gitdiff.Unified)
		} else {
			screen.SetPresentation(gitdiff.Split)
		}
	case actionCollapse:
		screen.document.SetAllCollapsed(!screen.document.AllCollapsed())
		screen.syncLinks()
	case actionNavigate:
		key, _ := action.Payload.(string)
		screen.document.RevealFile(key)
		screen.focusID(screen.DocumentID())
		result.Invalidate |= ui.InvalidateFocus
	case ui.ActionSplitResize:
		if resize, ok := action.Payload.(ui.SplitResize); ok && resize.ID == screen.divider.ID() {
			return result
		}
		return ui.EventResult{Action: &action}
	case ActionRefresh, ActionReturn, ActionOpenAll:
		result.Action = &ui.Action{ID: action.ID, Payload: screen}
	case gitdiff.ActionFile:
		result.Action = &action
	default:
		return ui.EventResult{Action: &action}
	}
	return result
}

func (screen *Screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Git review", Value: screen.Title()}
}

var _ ui.Component = (*Screen)(nil)
var _ ui.Parent = (*Screen)(nil)
var _ ui.ActionHandler = (*Screen)(nil)
