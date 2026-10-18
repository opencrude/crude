package viewmodel

import (
	"context"
	"crypto/sha256"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/localhistory"
	"crude/shellport"
	"crude/textdiff"
	findinfiles "crude/tui/screens/findinfiles"
	"crude/tui/surface"
	"crude/tui/ui"
	"crude/tui/wm"
	"crude/workspace"
)

const projectReplaceOverlay ui.OverlayID = "project-replace-review"

type projectReplaceReview struct {
	ui.Base
	area       *ui.TextArea
	bar        *ui.ActionBar
	owner      shellport.Workspace
	screen     *findinfiles.Screen
	generation uint64
	request    findinfiles.ReplaceMsg
	edits      []fileio.ProjectFileReplacement
	skipped    int
	busy       bool
	cancel     context.CancelFunc
	done       chan struct{}
	start      tea.Cmd
}
type projectReplaceActionMsg struct {
	review *projectReplaceReview
	action string
}
type clientProjectReplaceMsg struct {
	generation uint64
	screen     *findinfiles.Screen
	port       shellport.Workspace
	request    findinfiles.ReplaceMsg
	edits      []fileio.ProjectFileReplacement
	skipped    int
	err        error
	preview    string
}
type projectReplaceAppliedMsg struct {
	review *projectReplaceReview
	result workspace.FileOperationResultMsg
	safety string
}

func (p *projectReplaceReview) Children() []ui.Component { return []ui.Component{p.area, p.bar} }
func (p *projectReplaceReview) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (p *projectReplaceReview) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.area.Layout(ctx, ui.Rect{W: r.W, H: max(0, r.H-2)})
	p.bar.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (p *projectReplaceReview) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (p *projectReplaceReview) SetTokens(t *ui.TokenSet)                             { p.area.SetTokens(t); p.bar.SetTokens(t) }
func (p *projectReplaceReview) Handle(ctx ui.EventContext, e ui.Event) ui.EventResult {
	if e.IsKey() && e.Key.Code == tea.KeyEsc {
		return p.HandleAction(ctx, ui.Action{ID: "cancel"})
	}
	return ui.EventResult{}
}
func (p *projectReplaceReview) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if p.busy {
		return ui.EventResult{Handled: true}
	}
	if a.ID != "apply" && a.ID != "cancel" {
		return ui.EventResult{}
	}
	return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return projectReplaceActionMsg{p, a.ID} }}
}

func (m *App) applyProjectReplace(request findinfiles.ReplaceMsg) tea.Cmd {
	s := m.findInFiles
	if s == nil || s != request.Screen || s.Replacing() || request.Generation != s.Generation() || request.Query != s.Query() || request.Replacement != s.Replacement() || request.Options != s.Options() {
		return nil
	}
	matches := append([]fileio.ProjectSearchMatch(nil), s.Results()...)
	if !request.All {
		matches = []fileio.ProjectSearchMatch{request.Match}
	}
	// Capture immutable values on Update; all reads/planning run in the command.
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	local := make(map[string]string)
	if m.EditorState != nil {
		for path, tab := range m.EditorState.Tabs {
			if tab != nil && tab.Editor != nil {
				local[filepath.Clean(path)] = tab.Editor.Value()
			}
		}
	}
	ctx, _ := m.lspRequestContext()
	s.SetReplacing(true)
	m.markProjectSearchDirty()
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		var edits []fileio.ProjectFileReplacement
		var skipped int
		var err error
		if planner, ok := owner.(interface {
			PlanProjectReplace(context.Context, []fileio.ProjectSearchMatch, string, string, fileio.ProjectSearchOptions) ([]fileio.ProjectFileReplacement, int, error)
		}); ok {
			edits, skipped, err = planner.PlanProjectReplace(ctx, matches, request.Query, request.Replacement, request.Options)
		} else {
			edits, skipped, err = fileio.PlanProjectReplaceWithReader(matches, request.Query, request.Replacement, request.Options, func(path string) ([]byte, error) {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				if text, ok := local[filepath.Clean(path)]; ok {
					return []byte(text), nil
				}
				return readRenameLocalFile(path)
			})
		}
		preview := ""
		if err == nil {
			preview, err = projectReplacementPreview(edits, skipped)
		}
		return clientProjectReplaceMsg{generation: generation, screen: s, port: owner, request: request, edits: edits, skipped: skipped, err: err, preview: preview}
	}
}

func (m *App) applyClientProjectReplace(msg clientProjectReplaceMsg) tea.Cmd {
	if msg.generation != m.workspaceSwitchGen.Load() || msg.port != m.ws || msg.screen != m.findInFiles {
		return nil
	}
	s := msg.screen
	s.SetReplacing(false)
	if msg.request.Generation != s.Generation() || msg.request.Query != s.Query() || msg.request.Replacement != s.Replacement() || msg.request.Options != s.Options() {
		m.markProjectSearchDirty()
		return nil
	}
	if msg.err != nil {
		s.SetNotice("Replace failed: " + msg.err.Error())
		m.markProjectSearchDirty()
		return nil
	}
	if len(msg.edits) == 0 {
		s.SetNotice(fmt.Sprintf("Nothing to replace · skipped %d non-exact hits", msg.skipped))
		m.markProjectSearchDirty()
		return nil
	}
	if err := m.validateProjectReplacement(msg.edits); err != nil {
		s.SetNotice(err.Error())
		m.markProjectSearchDirty()
		return nil
	}
	p := &projectReplaceReview{Base: ui.NewBase("project-replace/body"), owner: msg.port, screen: s, generation: msg.generation, request: msg.request, edits: msg.edits, skipped: msg.skipped}
	tokens := ui.NewTokenSet(m.Theme)
	p.area = ui.NewTextArea("project-replace/detail", tokens, true)
	p.area.SetReadOnly(true)
	p.area.SetValue(msg.preview)
	p.area.SetCursorFlat(0)
	p.bar = ui.NewActionBar("project-replace/actions", tokens, ui.ActionBarButton{Label: "Apply replacements", Action: "apply", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	p.bar.SetCancelAction("cancel")
	p.bar.FocusLast()
	w, h := min(100, max(1, m.width-4)), min(28, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "project-replace/frame", ui.FrameDialog, "Review replacements", p, nil, false, w, h)
	frame.Focus(p.bar.ID())
	fw := wm.NewFloatingWindow(string(projectReplaceOverlay), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(false), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.projectReplace = p
	m.overlayHost.Open(projectReplaceOverlay, fw, ui.ModalDialogPolicy(), func() {
		if p.cancel != nil {
			p.cancel()
		}
		if m.projectReplace == p {
			m.projectReplace = nil
		}
	})
	return nil
}

func projectReplacementPreview(edits []fileio.ProjectFileReplacement, skipped int) (string, error) {
	var detail strings.Builder
	count := 0
	for _, edit := range edits {
		count += edit.Count
		fmt.Fprintf(&detail, "%s · %d replacements\n", edit.RelativePath, edit.Count)
		for _, h := range textdiff.Hunks(edit.OldContent, edit.NewContent) {
			fmt.Fprintf(&detail, "@@ line %d @@\n", h.OldStart+1)
			for _, line := range h.OldLines {
				detail.WriteString("- " + strings.TrimSuffix(line, "\n") + "\n")
			}
			for _, line := range h.NewLines {
				detail.WriteString("+ " + strings.TrimSuffix(line, "\n") + "\n")
			}
		}
		if detail.Len() > 1<<20 {
			return "", fmt.Errorf("replacement review exceeds 1 MiB; narrow the search")
		}
	}
	return fmt.Sprintf("%d exact replacements in %d files; %d discovery hits skipped.\nApply creates a Local History safety snapshot and writes the reviewed files.\nDirty/shared conflicts abort the whole transaction; save or reconcile first.\nRecover through Local History. Cancel changes nothing.\n\n", count, len(edits), skipped) + detail.String(), nil
}

func (m *App) validateProjectReplacement(edits []fileio.ProjectFileReplacement) error {
	for _, edit := range edits {
		if m.EditorState != nil {
			if tab := m.EditorState.Tabs[filepath.Clean(edit.Path)]; tab != nil && tab.Editor != nil {
				ed := tab.Editor
				state := ed.DocumentState()
				if ed.Modified() || ed.Value() != edit.OldContent || state.PendingEdits > 0 || state.InFlight || state.UndoPending || state.Conflict != nil {
					return fmt.Errorf("save or reconcile %s, then search and review again", edit.RelativePath)
				}
			}
		}
	}
	return nil
}

func (m *App) handleProjectReplaceAction(msg projectReplaceActionMsg) tea.Cmd {
	p := msg.review
	if p == nil || m.projectReplace != p || p.busy || p.owner != m.ws || p.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if msg.action == "cancel" {
		m.overlayHost.Close(projectReplaceOverlay)
		return nil
	}
	if msg.action != "apply" {
		return nil
	}
	if p.screen != m.findInFiles || p.request.Generation != p.screen.Generation() || p.request.Query != p.screen.Query() || p.request.Replacement != p.screen.Replacement() || p.request.Options != p.screen.Options() {
		m.overlayHost.Close(projectReplaceOverlay)
		return nil
	}
	if err := m.validateProjectReplacement(p.edits); err != nil {
		p.area.SetValue(err.Error() + "\nNo files changed. Cancel and review again.")
		m.forceFrame()
		return nil
	}
	p.busy = true
	p.bar.SetButtons(ui.ActionBarButton{Label: "Applying…", Action: "apply", Enabled: false})
	if fw := m.overlayHost.Window(projectReplaceOverlay); fw != nil {
		fw.MarkContentDirty()
	}
	ctx, _ := m.lspRequestContext()
	ctx, p.cancel = context.WithCancel(ctx)
	p.done = make(chan struct{})
	var once sync.Once
	var output projectReplaceAppliedMsg
	done, owner, edits := p.done, p.owner, p.edits
	p.start = func() tea.Msg {
		once.Do(func() {
			defer close(done)
			ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			defer cancel()
			result, safety := applyReviewedProjectReplacement(ctx, owner, edits)
			output = projectReplaceAppliedMsg{p, result, safety}
		})
		return output
	}
	return p.start
}

func applyReviewedProjectReplacement(ctx context.Context, owner shellport.Workspace, edits []fileio.ProjectFileReplacement) (workspace.FileOperationResultMsg, string) {
	fail := func(err error) (workspace.FileOperationResultMsg, string) {
		return workspace.FileOperationResultMsg{Op: workspace.FileOpApplyEdit, Err: err}, ""
	}
	history := owner.LocalHistory()
	if history == nil {
		return fail(fmt.Errorf("local history is required for recoverable project replacement"))
	}
	paths := make([]string, 0, len(edits))
	disk := make([]workspace.FileEdit, 0, len(edits))
	for _, edit := range edits {
		rel, err := filepath.Rel(owner.RootPath(), edit.Path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fail(fmt.Errorf("replacement path is outside the workspace"))
		}
		paths = append(paths, filepath.ToSlash(rel))
		disk = append(disk, workspace.FileEdit{Path: edit.Path, OldContent: edit.OldContent, NewContent: edit.NewContent})
	}
	snapshot, _, err := history.SnapshotIfChanged(ctx, localhistory.SnapshotOptions{Kind: "safety", Label: "Before project replacement", IncludePaths: paths, DisableGitIgnore: true})
	if err != nil {
		return fail(err)
	}
	for i, edit := range edits {
		entry, ok := snapshot.Entries[paths[i]]
		if !ok || entry.Type != localhistory.EntryFile || entry.Hash != fmt.Sprintf("%x", sha256.Sum256([]byte(edit.OldContent))) {
			return fail(fmt.Errorf("%s changed before its safety snapshot; no files changed", edit.RelativePath))
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return owner.ApplyWorkspaceEdit(disk), snapshot.ID
}

func (m *App) handleProjectReplaceApplied(msg projectReplaceAppliedMsg) tea.Cmd {
	p := msg.review
	if p == nil || p.owner != m.ws || p.generation != m.workspaceSwitchGen.Load() {
		return nil
	}
	if m.projectReplace != p {
		return nil
	}
	p.busy = false
	if msg.result.Err != nil {
		p.area.SetValue("Replacement did not complete: " + msg.result.Err.Error() + "\nDo not replay an uncertain operation. Review current files and Local History before trying again.\nSafety snapshot: " + msg.safety)
		p.bar.SetButtons(ui.ActionBarButton{Label: "Close", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
		if fw := m.overlayHost.Window(projectReplaceOverlay); fw != nil {
			fw.MarkContentDirty()
		}
		return nil
	}
	for _, edit := range p.edits {
		if m.EditorState != nil {
			if tab := m.EditorState.Tabs[filepath.Clean(edit.Path)]; tab != nil {
				tab.Editor.SignalDiskChanged()
			}
		}
	}
	m.overlayHost.Close(projectReplaceOverlay)
	if p.screen != m.findInFiles {
		return nil
	}
	cmd := p.screen.ScheduleSearch()
	p.screen.SetNotice(fmt.Sprintf("Replaced in %d files · skipped %d · Local History %s", len(p.edits), p.skipped, msg.safety[:min(8, len(msg.safety))]))
	m.markProjectSearchDirty()
	return cmd
}
