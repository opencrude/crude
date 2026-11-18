// Package gitmerge resolves the conflicts of a stopped merge, rebase,
// cherry-pick or revert. Each conflicted file opens in the shared comparison:
// the current side on the left, the incoming side on the right, one change per
// conflict, accepted with the same bar, keys and gutter boxes as every other
// comparison. The owner reads files, writes resolutions and runs Git.
package gitmerge

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/text/diff"
	"crude/tui/cell"
	"crude/tui/rendering"
	"crude/tui/screens/diffview"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/workspace"
)

const (
	ScreenID ui.ComponentID = "git-merge/body"
	filesID  ui.ComponentID = "git-merge/files"
	viewerID ui.ComponentID = "git-merge/comparison"
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

	// resolverBodyY is the first row below the summary.
	resolverBodyY = 1
)

// Decision is one user decision for the owner to carry out.
type Decision struct {
	Kind        string
	Path        string
	Fingerprint string
	Choices     []workspace.GitConflictChoice
	Line        int
}

// Resolver is the body of the merge dialog.
type Resolver struct {
	ui.Base
	tokens    *ui.TokenSet
	files     *ui.List
	viewer    *diffview.Screen
	actions   *ui.ActionBar
	decide    func(Decision) tea.Cmd
	operation string
	conflicts []string
	file      *workspace.GitConflictFile
	loading   string
	busy      string
	errText   string
	listW     int
}

func New(tokens *ui.TokenSet, decide func(Decision) tea.Cmd) *Resolver {
	r := &Resolver{Base: ui.NewBase(ScreenID), tokens: tokens, decide: decide}
	r.files = ui.NewList(filesID, tokens)
	r.files.SetActivateAction(actionFile)
	r.viewer = diffview.NewWithID(tokens.Theme(), viewerID)
	r.viewer.SetTokens(tokens)
	r.viewer.SetResolvable(true, diffview.ChoiceNone)
	r.viewer.SetOnResolutionChange(r.syncActions)
	r.actions = ui.NewActionBar(barID, tokens)
	r.actions.SetCancelAction(actionClose)
	r.syncActions()
	return r
}

// DocumentID is the comparison pane that takes keyboard focus.
func (r *Resolver) DocumentID() ui.ComponentID { return r.viewer.DocumentID() }

// Prepare builds the comparison of a conflicted file off the UI goroutine:
// the current side on the left, the incoming side on the right, one change
// for each conflict region.
func Prepare(ctx context.Context, file workspace.GitConflictFile) (diffview.Prepared, error) {
	var before, after strings.Builder
	var changes []textdiff.Hunk
	current, incoming := "", ""
	old, next := 0, 0
	for _, segment := range file.Segments {
		if segment.Conflict == nil {
			for _, line := range segment.Text {
				before.WriteString(line)
				after.WriteString(line)
			}
			old += len(segment.Text)
			next += len(segment.Text)
			continue
		}
		region := segment.Conflict
		if current == "" {
			current, incoming = markerLabel(region.CurrentLabel), markerLabel(region.IncomingLabel)
		}
		changes = append(changes, textdiff.Hunk{OldStart: old, NewStart: next, OldLines: region.Current, NewLines: region.Incoming})
		for _, line := range region.Current {
			before.WriteString(line)
		}
		for _, line := range region.Incoming {
			after.WriteString(line)
		}
		old += len(region.Current)
		next += len(region.Incoming)
	}
	return diffview.PrepareChangesContext(ctx, diffview.Comparison{
		Path: file.Path, BeforeLabel: sideLabel("Current", current), AfterLabel: sideLabel("Incoming", incoming),
		Before: before.String(), After: after.String(),
	}, changes)
}

func sideLabel(side, marker string) string {
	if marker == "" {
		return side
	}
	return side + " · " + marker
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
		r.file = nil
		r.viewer.SetPrepared(diffview.Prepared{})
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
	r.syncActions()
}

// SetFile shows a conflicted file and its prepared comparison. Choices stay
// when the same bytes are shown again.
func (r *Resolver) SetFile(file workspace.GitConflictFile, prepared diffview.Prepared) {
	same := r.file != nil && r.file.Path == file.Path && r.file.Fingerprint == file.Fingerprint
	previous := r.viewer.Choices()
	copy := file
	r.file, r.loading, r.errText = &copy, "", ""
	r.viewer.SetPrepared(prepared)
	if same {
		r.viewer.SetChoices(previous)
	}
	r.selectListPath(file.Path)
	r.syncActions()
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
	choices := r.viewer.Choices()
	out := make([]workspace.GitConflictChoice, len(choices))
	for i, choice := range choices {
		switch choice {
		case diffview.ChoiceLeft:
			out[i] = workspace.GitChooseCurrent
		case diffview.ChoiceRight:
			out[i] = workspace.GitChooseIncoming
		case diffview.ChoiceBoth:
			out[i] = workspace.GitChooseBoth
		}
	}
	return out
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

func (r *Resolver) decided() bool {
	return r.file != nil && len(r.viewer.Choices()) > 0 && r.viewer.Decided()
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

// syncActions keeps the file decision in the comparison's own bar (Keep
// buttons, change navigation, Resolve file) and the operation decisions in
// the dialog's action row.
func (r *Resolver) syncActions() {
	_, finish := operationLabel(r.operation)
	idle := r.busy == "" && r.loading == ""
	r.viewer.SetCommit("Resolve file", "Ctrl+S", actionSave, idle && r.file != nil)
	r.actions.SetButtons(
		ui.ActionBarButton{ID: "git-merge/open", Label: "Open in editor", Shortcut: "E", Action: actionOpen, Enabled: idle && r.file != nil, Variant: ui.VariantSecondary},
		ui.ActionBarButton{ID: "git-merge/complete", Label: finish, Action: actionComplete, Enabled: idle && len(r.conflicts) == 0, Variant: ui.VariantPrimary},
		ui.ActionBarButton{ID: "git-merge/abort", Label: "Abort…", Action: actionAbort, Enabled: idle, Variant: ui.VariantDanger},
		ui.ActionBarButton{ID: "git-merge/close", Label: "Close", Action: actionClose, Enabled: true, Variant: ui.VariantSecondary},
	)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// markerLabel shortens a full commit id (a pull merges a commit, not a
// branch) to the abbreviation Git prints elsewhere.
func markerLabel(label string) string {
	if len(label) == 40 && strings.Trim(label, "0123456789abcdef") == "" {
		return label[:7]
	}
	return label
}

func (r *Resolver) Children() []ui.Component {
	children := []ui.Component{r.viewer, r.actions}
	if r.listW > 0 {
		children = append([]ui.Component{r.files}, children...)
	}
	return children
}

func (r *Resolver) Measure(_ ui.LayoutContext, cs ui.Constraints) ui.Size {
	return cs.ClampSize(ui.Size{W: cs.MaxW, H: cs.MaxH})
}

// Layout: the summary row, then the file list beside the comparison (whose
// bar holds the file's decisions), then the operation actions.
func (r *Resolver) Layout(ctx ui.LayoutContext, bounds ui.Rect) {
	r.SetBounds(bounds)
	bodyY, bodyH := resolverBodyY, max(0, bounds.H-resolverBodyY-2)
	r.listW = 0
	if bounds.W >= 80 {
		r.listW = min(34, bounds.W/4)
	}
	r.files.Layout(ctx, ui.Rect{Y: bodyY, W: r.listW, H: bodyH})
	viewX := 0
	if r.listW > 0 {
		viewX = r.listW + 1
	}
	if r.file == nil && r.loading == "" {
		// No file to decide: no comparison and no decision bar, only the
		// message Paint writes in their place.
		r.viewer.Layout(ctx, ui.Rect{})
	} else {
		r.viewer.Layout(ctx, ui.Rect{X: viewX, Y: bodyY, W: max(0, bounds.W-viewX), H: bodyH})
	}
	r.actions.Layout(ctx, ui.Rect{Y: max(0, bounds.H-1), W: bounds.W, H: min(1, bounds.H)})
}

func (r *Resolver) Paint(buf *surface.CellBuffer, originX, originY int, _ ui.PaintContext) {
	bounds := r.Bounds()
	tokens := r.tokens
	bg := tokens.Color(ui.ColorPopupBg)
	x, y := originX+bounds.X, originY+bounds.Y
	doing, finish := operationLabel(r.operation)
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
		summary, role = "✓ Every conflict is resolved. "+finish+" to finish.", ui.ColorSuccess
	}
	buf.WriteString(x, y, rendering.TruncateVisual(summary, bounds.W, "…"), tokens.Color(role), bg, cell.AttrBold)
	if r.listW > 0 {
		for row := resolverBodyY; row < bounds.H-2; row++ {
			buf.WriteString(x+r.listW, y+row, "│", tokens.Color(ui.ColorPopupDivider), bg, 0)
		}
	}
	if r.file == nil && r.loading == "" {
		message := "Select a conflicted file"
		if len(r.conflicts) == 0 {
			message = "Every file is resolved and staged. " + finish + " to record it, or Abort to return to the branch as it was."
		}
		left := r.listW + 3
		buf.WriteString(x+left, y+resolverBodyY+1, rendering.TruncateVisual(message, max(0, bounds.W-left), "…"), tokens.Color(ui.ColorMuted), bg, 0)
	}
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
		if conflicts := r.file.Conflicts(); len(conflicts) > 0 {
			current := min(max(0, r.viewer.CurrentChange()), len(conflicts)-1)
			line = conflicts[current].StartLine + 1
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

// Handle owns the dialog keys. Change keys (1, 2, 3, 0, n, p) belong to the
// shared comparison; keys that reach the dialog body go there too.
func (r *Resolver) Handle(ctx ui.EventContext, evt ui.Event) ui.EventResult {
	if !evt.IsKey() {
		return ui.EventResult{}
	}
	switch evt.Key.String() {
	case "e":
		return r.act(actionOpen)
	case "ctrl+s":
		return r.act(actionSave)
	case "esc":
		return r.act(actionClose)
	}
	return r.viewer.Handle(ctx, evt)
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
