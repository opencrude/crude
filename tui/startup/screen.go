package startup

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/coreprotocol"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/filepicker"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/types"
	"crude/workspace"
)

const (
	goID       ui.ComponentID = "startup/go"
	openID     ui.ComponentID = "startup/open"
	quitID     ui.ComponentID = "startup/quit"
	previousID ui.ComponentID = "startup/previous"
	pathID     ui.ComponentID = "startup/path"
	pathGoID   ui.ComponentID = "startup/path-go"
	moreID     ui.ComponentID = "startup/more"
	issuesID   ui.ComponentID = "startup/issues"
)

type phase uint8

const (
	introduction phase = iota
	selection
	picking
)

type screen struct {
	ui.Base
	tokens                             *ui.TokenSet
	mode                               phase
	initial                            coreprotocol.StartupSnapshot
	goButton, open, quit, pathGo, more *ui.Button
	previous                           *ui.List
	scrollbar                          *ui.ScrollbarControl
	path                               *ui.TextField
	issues                             *ui.TextArea
	picker                             *filepicker.Screen
	frame                              *ui.WindowFrame
	body                               *pickerBody
	notice, pickerNotice               string
	content                            ui.Rect
}
type pickerBody struct {
	ui.Base
	owner *screen
}

func newScreen(th *theme.Theme, picker *filepicker.Screen, initial coreprotocol.StartupSnapshot) *screen {
	tokens := ui.NewTokenSet(th)
	s := &screen{Base: ui.NewBase("startup"), tokens: tokens, initial: initial, picker: picker, mode: selection}
	if initial.ShowIntroduction {
		s.mode = introduction
	}
	s.goButton = ui.NewButton(goID, "Let's go!", "startup.go")
	s.open = ui.NewButton(openID, "Open Workspace", "startup.open")
	s.quit = ui.NewButton(quitID, "Quit", "startup.quit")
	s.pathGo = ui.NewButton(pathGoID, "Go", "startup.path")
	s.more = ui.NewButton(moreID, "More folders", "startup.more")
	s.path = ui.NewTextField(pathID, tokens)
	s.path.SetSubmitAction("startup.path")
	s.path.SetPlaceholder("Enter a folder path")
	s.issues = ui.NewTextArea(issuesID, tokens, true)
	s.issues.SetReadOnly(true)
	s.previous = ui.NewList(previousID, tokens)
	s.previous.SetActivateAction("startup.workspace")
	s.previous.SetDefaultRowBackground(ui.ColorBg)
	items := make([]ui.ListItem, 0, len(initial.Workspaces))
	for _, info := range initial.Workspaces {
		if info.ProjectDir != "" {
			items = append(items, ui.ListItem{ID: info.ProjectDir, Label: workspace.WorkspaceDisplayName(info), Detail: info.ProjectDir, Payload: info.ProjectDir})
		}
	}
	if len(items) == 0 {
		items = append(items, ui.ListItem{ID: "startup/empty", Label: "No previous workspaces. Open a project folder to begin.", Disabled: true})
	}
	s.previous.SetItems(items)
	s.scrollbar = ui.NewScrollbarControl("startup/previous-scroll", s.previous.Scroll(), tokens)
	s.body = &pickerBody{Base: ui.NewBase("startup/picker-body"), owner: s}
	s.frame = ui.NewWindowFrame("startup/picker-frame", ui.FrameWindow, tokens)
	s.frame.SetTitle("Choose a project folder")
	s.frame.SetClosable(true)
	s.frame.SetContent(s.body)
	s.SetTokens(tokens)
	return s
}
func (s *screen) SetTokens(tokens *ui.TokenSet) {
	s.tokens = tokens
	for _, b := range []*ui.Button{s.goButton, s.open, s.quit, s.pathGo, s.more} {
		b.SetTokens(tokens)
		b.SetSurfaceBackground(tokens.Color(ui.ColorBg))
	}
	s.pathGo.SetSurfaceBackground(tokens.Color(ui.ColorPopupBg))
	s.more.SetSurfaceBackground(tokens.Color(ui.ColorPopupBg))
	s.goButton.SetVariant(ui.VariantPrimary)
	s.open.SetVariant(ui.VariantPrimary)
	s.quit.SetVariant(ui.VariantSecondary)
	s.pathGo.SetVariant(ui.VariantSecondary)
	s.more.SetVariant(ui.VariantSecondary)
	s.path.SetTokens(tokens)
	s.issues.SetTokens(tokens)
	s.issues.SetBackgroundColor(tokens.Color(ui.ColorBg))
	if s.mode == picking {
		s.issues.SetBackgroundColor(tokens.Color(ui.ColorPopupBg))
	}
	s.previous.SetTokens(tokens)
	s.scrollbar.SetTokens(tokens)
	s.frame.SetTokens(tokens)
	s.picker.SetTokens(tokens)
}
func (s *screen) Children() []ui.Component {
	var children []ui.Component
	switch s.mode {
	case introduction:
		children = []ui.Component{s.goButton}
	case picking:
		return []ui.Component{s.frame}
	default:
		children = []ui.Component{s.open, s.previous, s.scrollbar, s.quit}
	}
	if s.mode != picking && s.issues.Value() != "" {
		children = append(children, s.issues)
	}
	return children
}

func (s *screen) setIssues(issues []types.OperationIssue) {
	var blocks []string
	for _, issue := range issues {
		parts := []string{issue.Title}
		if issue.Path != "" {
			parts = append(parts, "Location: "+issue.Path)
		}
		for _, part := range []string{issue.Detail, issue.Summary, issue.Guidance} {
			if part != "" {
				parts = append(parts, part)
			}
		}
		blocks = append(blocks, strings.Join(parts, "\n"))
	}
	text := strings.Join(blocks, "\n\n")
	if text != s.issues.Value() {
		s.issues.SetValue(text)
		s.issues.SetCursorFlat(0)
	}
}
func (s *screen) Layout(ctx ui.LayoutContext, r ui.Rect) {
	s.SetBounds(r)
	w := min(76, max(0, r.W-8))
	h := min(24, max(0, r.H-2))
	s.content = ui.Rect{X: max(0, (r.W-w)/2), Y: max(0, (r.H-h)/2), W: w, H: h}
	c := s.content
	if s.mode == picking {
		fw, fh := min(112, max(0, r.W-2)), min(34, max(0, r.H-3))
		s.frame.Layout(ctx, ui.Rect{X: max(0, (r.W-fw)/2), Y: max(0, (r.H-fh)/2), W: fw, H: fh})
		return
	}
	s.goButton.Layout(ctx, ui.Rect{X: c.X, Y: c.Y + min(19, max(0, c.H-2)), W: min(15, c.W), H: 1})
	s.open.Layout(ctx, ui.Rect{X: c.X, Y: c.Y + 6, W: min(22, c.W), H: 1})
	listY := c.Y + 10
	listH := max(0, c.H-13)
	if s.issues.Value() != "" {
		s.issues.SetBackgroundColor(s.tokens.Color(ui.ColorBg))
		issueY := max(0, c.H-8)
		s.issues.Layout(ctx, ui.Rect{X: c.X, Y: c.Y + issueY, W: c.W, H: min(5, max(0, c.H-issueY-3))})
		listH = max(0, issueY-1-10)
		if s.mode == introduction {
			s.goButton.Layout(ctx, ui.Rect{X: c.X, Y: c.Y + max(0, issueY-1), W: min(15, c.W), H: 1})
		}
	}
	s.previous.Layout(ctx, ui.Rect{X: c.X, Y: listY, W: max(0, c.W-1), H: listH})
	s.scrollbar.Layout(ctx, ui.Rect{X: c.X + max(0, c.W-1), Y: listY, W: min(1, c.W), H: listH})
	s.quit.Layout(ctx, ui.Rect{X: c.X, Y: c.Y + max(0, c.H-2), W: min(10, c.W), H: 1})
}
func (s *screen) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	r := s.Bounds()
	bg := s.tokens.Color(ui.ColorBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	c := s.content
	x, y := ox+r.X+c.X, oy+r.Y+c.Y
	write := func(row int, text string, role ui.SemanticColor, bold bool) {
		attr := cell.Attrs(0)
		if bold {
			attr = cell.AttrBold
		}
		buf.WriteString(x, y+row, rendering.TruncateVisual(text, c.W, "…"), s.tokens.Color(role), bg, attr)
	}
	if s.mode == picking {
		if s.notice != "" {
			buf.WriteString(ox+r.X+1, oy+r.Y+max(0, r.H-1), rendering.TruncateVisual(s.notice, max(0, r.W-2), "…"), s.tokens.Color(ui.ColorWarning), bg, 0)
		}
		return
	}
	if s.mode == introduction {
		logo := []string{" ██████╗██████╗ ██╗   ██╗██████╗ ███████╗", "██╔════╝██╔══██╗██║   ██║██╔══██╗██╔════╝", "██║     ██████╔╝██║   ██║██║  ██║█████╗  ", "██║     ██╔══██╗██║   ██║██║  ██║██╔══╝  ", "╚██████╗██║  ██║╚██████╔╝██████╔╝███████╗", " ╚═════╝╚═╝  ╚═╝ ╚═════╝ ╚═════╝ ╚══════╝"}
		for i, line := range logo {
			write(i, line, ui.ColorAccent, true)
		}
		write(7, "Welcome to Crude", ui.ColorFg, true)
		features := []string{"Edit project files.", "Compare changes with Git.", "Run commands in built-in terminals.", "Use AI agents, skills, and MCP tools."}
		stride := 2
		if s.issues.Value() != "" {
			stride = 1
		}
		for i, feature := range features {
			write(9+i*stride, feature, ui.ColorFg, false)
		}
		write(min(21, max(0, c.H-1)), "Select a project folder next. You can edit files without an account.", ui.ColorMuted, false)
		return
	}
	write(0, "Crude", ui.ColorAccent, true)
	write(2, "Choose a project workspace", ui.ColorFg, true)
	write(3, s.initial.Root, ui.ColorMuted, false)
	for i, line := range rendering.WordWrap(selectionReason(s.initial), max(1, c.W)) {
		if i >= 2 {
			break
		}
		write(4+i, line, ui.ColorFg, false)
	}
	write(8, "Previous workspaces", ui.ColorFg, true)
	if s.initial.WorkspacesError != "" {
		write(9, "Could not load workspaces: "+s.initial.WorkspacesError, ui.ColorError, false)
	}
	if s.notice != "" {
		write(7, s.notice, ui.ColorWarning, false)
	}
	if s.issues.Value() != "" {
		write(max(0, c.H-9), "Startup issues · Tab to read · Arrow keys to scroll", ui.ColorWarning, true)
	}
	write(max(0, c.H-1), "Tab to navigate · Enter to open · Esc to quit", ui.ColorMuted, false)
}
func selectionReason(v coreprotocol.StartupSnapshot) string {
	if v.PathError != "" {
		return v.PathError
	}
	switch v.FolderKind {
	case "root":
		return "This is a filesystem root. Crude does not start project services here. Choose a project folder."
	case "home":
		return "This is your home folder. Crude does not start project services here. Choose a project folder."
	case "personal":
		return "This is a personal folder. Crude does not start project services here. Choose a project folder."
	case "":
		return "Open a project folder to get started."
	default:
		return fmt.Sprintf("Choose a project folder inside this %s folder.", v.FolderKind)
	}
}
func (s *screen) Handle(_ ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyEsc {
		action := "startup.quit"
		if s.mode == picking {
			action = "startup.cancel"
		}
		return ui.EventResult{Handled: true, Action: &ui.Action{ID: action}}
	}
	return ui.EventResult{}
}
func (s *screen) Semantics() ui.Semantics {
	return ui.Semantics{Role: ui.RoleGroup, Label: "Crude startup"}
}
func (b *pickerBody) Children() []ui.Component {
	s := b.owner
	children := []ui.Component{s.path, s.pathGo, s.more, s.picker}
	if s.issues.Value() != "" {
		children = append(children, s.issues)
	}
	return children
}
func (b *pickerBody) Layout(ctx ui.LayoutContext, r ui.Rect) {
	b.SetBounds(r)
	s := b.owner
	goW, moreW := min(8, r.W), min(18, max(0, r.W-8))
	fieldW := max(0, r.W-goW-moreW-2)
	s.path.Layout(ctx, ui.Rect{W: fieldW, H: 1})
	s.pathGo.Layout(ctx, ui.Rect{X: fieldW + 1, W: goW, H: 1})
	s.more.Layout(ctx, ui.Rect{X: fieldW + goW + 2, W: moreW, H: 1})
	issueH := 0
	if s.issues.Value() != "" {
		issueH = min(6, max(0, r.H-7))
		s.issues.SetBackgroundColor(s.tokens.Color(ui.ColorPopupBg))
		s.issues.Layout(ctx, ui.Rect{Y: max(2, r.H-2-issueH), W: r.W, H: max(0, issueH-1)})
	}
	s.picker.Layout(ctx, ui.Rect{Y: 2, W: r.W, H: max(0, r.H-5-issueH)})
}
func (b *pickerBody) Paint(buf *surface.CellBuffer, ox, oy int, _ ui.PaintContext) {
	s := b.owner
	r := b.Bounds()
	bg := s.tokens.Color(ui.ColorPopupBg)
	ui.FillRect(buf, ox, oy, r, cell.BlankCell(bg))
	if s.issues.Value() != "" {
		buf.WriteString(ox+r.X, oy+r.Y+max(0, s.issues.Bounds().Y-1), rendering.TruncateVisual("Startup issues · Tab to read · Arrow keys to scroll", r.W, "…"), s.tokens.Color(ui.ColorWarning), bg, cell.AttrBold)
	}
	lines := []string{s.pickerNotice, "Ctrl+L path · Enter browse · Ctrl+O open selection · Esc clear / back"}
	for i, line := range lines {
		buf.WriteString(ox+r.X, oy+r.Y+max(0, r.H-2)+i, rendering.TruncateVisual(strings.TrimSpace(line), r.W, "…"), s.tokens.Color(ui.ColorMuted), bg, 0)
	}
}
