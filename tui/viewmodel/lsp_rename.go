package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"crude/textdiff"
	"crude/tui/editor"
	"crude/tui/surface"
	"crude/tui/theme"
	"crude/tui/ui"
	"crude/tui/wm"
	model "crude/types"
	"crude/workspace"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

const lspRenameOverlay ui.OverlayID = "lsp-rename-preview"

type lspRenamePreparedMsg struct {
	request lspRenameResultMsg
	edits   []workspace.FileEdit
	preview string
	err     error
}
type lspRenameAppliedMsg struct {
	preview    *lspRenamePreview
	generation uint64
	result     workspace.FileOperationResultMsg
	safety     string
}
type lspRenameActionMsg struct {
	preview *lspRenamePreview
	action  string
}
type lspRenamePreview struct {
	ui.Base
	area    *ui.TextArea
	bar     *ui.ActionBar
	request lspRenameResultMsg
	edits   []workspace.FileEdit
	busy    bool
	cancel  context.CancelFunc
}

func newLSPRenamePreview(th *theme.Theme, msg lspRenamePreparedMsg) *lspRenamePreview {
	tokens := ui.NewTokenSet(th)
	p := &lspRenamePreview{Base: ui.NewBase("lsp-rename/body"), request: msg.request, edits: msg.edits}
	p.area = ui.NewTextArea("lsp-rename/detail", tokens, true)
	p.area.SetReadOnly(true)
	p.area.SetValue(msg.preview)
	p.area.SetCursorFlat(0)
	p.bar = ui.NewActionBar("lsp-rename/actions", tokens, ui.ActionBarButton{Label: "Apply rename", Action: "apply", Enabled: true, Variant: ui.VariantPrimary}, ui.ActionBarButton{Label: "Cancel", Action: "cancel", Enabled: true, Variant: ui.VariantSecondary})
	p.bar.SetCancelAction("cancel")
	p.bar.FocusLast()
	return p
}
func (p *lspRenamePreview) Children() []ui.Component { return []ui.Component{p.area, p.bar} }
func (p *lspRenamePreview) Measure(_ ui.LayoutContext, c ui.Constraints) ui.Size {
	return c.ClampSize(ui.Size{W: c.MaxW, H: c.MaxH})
}
func (p *lspRenamePreview) Layout(ctx ui.LayoutContext, r ui.Rect) {
	p.SetBounds(r)
	p.area.Layout(ctx, ui.Rect{W: r.W, H: max(0, r.H-2)})
	p.bar.Layout(ctx, ui.Rect{Y: max(0, r.H-1), W: r.W, H: min(1, r.H)})
}
func (p *lspRenamePreview) Paint(*surface.CellBuffer, int, int, ui.PaintContext) {}
func (p *lspRenamePreview) Handle(ctx ui.EventContext, event ui.Event) ui.EventResult {
	if event.IsKey() && event.Key.Code == tea.KeyEsc {
		return p.HandleAction(ctx, ui.Action{ID: "cancel"})
	}
	if event.IsKey() {
		return p.bar.Handle(ctx, event)
	}
	return ui.EventResult{}
}
func (p *lspRenamePreview) HandleAction(_ ui.EventContext, a ui.Action) ui.EventResult {
	if p.busy {
		return ui.EventResult{Handled: true}
	}
	if a.ID == "apply" || a.ID == "cancel" {
		return ui.EventResult{Handled: true, Cmd: func() tea.Msg { return lspRenameActionMsg{p, a.ID} }}
	}
	return ui.EventResult{}
}

func (p *lspRenamePreview) SetTokens(tokens *ui.TokenSet) {
	p.area.SetTokens(tokens)
	p.bar.SetTokens(tokens)
}

func prepareLSPRename(root string, msg lspRenameResultMsg, edit *protocol.WorkspaceEdit) lspRenamePreparedMsg {
	out := lspRenamePreparedMsg{request: msg}
	var preview strings.Builder
	grouped := make(map[string][]model.LSPTextEdit)
	add := func(uri protocol.DocumentURI, edits []protocol.TextEdit) error {
		path, err := uri.Path()
		if err != nil {
			return err
		}
		path = filepath.Clean(path)
		rel, err := filepath.Rel(root, path)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("rename path is outside workspace")
		}
		for _, e := range edits {
			grouped[path] = append(grouped[path], model.LSPTextEdit{Line: int(e.Range.Start.Line), Col: int(e.Range.Start.Character), EndLine: int(e.Range.End.Line), EndCol: int(e.Range.End.Character), NewText: e.NewText})
		}
		return nil
	}
	for uri, edits := range edit.Changes {
		if err := add(uri, edits); err != nil {
			out.err = err
			return out
		}
	}
	for _, change := range edit.DocumentChanges {
		if !change.Valid() || change.TextDocumentEdit == nil {
			out.err = fmt.Errorf("file create/rename/delete workspace operations are not supported")
			return out
		}
		document := change.TextDocumentEdit
		var edits []protocol.TextEdit
		for _, union := range document.Edits {
			switch e := union.Value.(type) {
			case protocol.TextEdit:
				edits = append(edits, e)
			case protocol.AnnotatedTextEdit:
				edits = append(edits, e.TextEdit)
			default:
				out.err = fmt.Errorf("unsupported annotated or snippet edit")
				return out
			}
		}
		if err := add(document.TextDocument.URI, edits); err != nil {
			out.err = err
			return out
		}
	}
	paths := make([]string, 0, len(grouped))
	for path := range grouped {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	if len(paths) > 200 {
		out.err = fmt.Errorf("rename exceeds 200-file review limit")
		return out
	}
	total := 0
	for _, path := range paths {
		info, err := os.Lstat(path)
		if err != nil {
			out.err = err
			return out
		}
		if !info.Mode().IsRegular() || info.Size() > 2<<20 {
			out.err = fmt.Errorf("rename requires regular files up to 2 MiB: %s", path)
			return out
		}
		f, err := os.Open(path)
		if err != nil {
			out.err = err
			return out
		}
		data, readErr := io.ReadAll(io.LimitReader(f, (2<<20)+1))
		closeErr := f.Close()
		if readErr != nil {
			out.err = readErr
			return out
		}
		if closeErr != nil {
			out.err = closeErr
			return out
		}
		total += len(data)
		if len(data) > 2<<20 || total > 16<<20 {
			out.err = fmt.Errorf("rename exceeds 16 MiB review budget")
			return out
		}
		updated, err := editor.ApplyLSPTextEdits(string(data), grouped[path])
		if err != nil {
			out.err = err
			return out
		}
		out.edits = append(out.edits, workspace.FileEdit{Path: path, OldContent: string(data), NewContent: updated})
		rel, _ := filepath.Rel(root, path)
		fmt.Fprintf(&preview, "%s\n", rel)
		for _, h := range textdiff.Hunks(string(data), updated) {
			fmt.Fprintf(&preview, "@@ line %d @@\n", h.OldStart+1)
			for _, line := range h.OldLines {
				preview.WriteString("- " + strings.TrimSuffix(line, "\n") + "\n")
			}
			for _, line := range h.NewLines {
				preview.WriteString("+ " + strings.TrimSuffix(line, "\n") + "\n")
			}
		}
		if preview.Len() > 1<<20 {
			out.err = fmt.Errorf("rename preview exceeds 1 MiB; narrow the operation")
			return out
		}
		preview.WriteByte('\n')
	}
	out.preview = fmt.Sprintf("Rename to %q in %d files.\nApply creates a Local History safety snapshot; recover via Local History.\n\n", msg.newName, len(out.edits)) + preview.String()
	return out
}

func (m *App) showLSPRenamePreview(msg lspRenamePreparedMsg) {
	r := msg.request
	if !m.acceptsLSPDocument(r.generation, r.filePath, r.owner, r.revision) {
		return
	}
	if msg.err != nil {
		m.Feedback.NotifyError("Rename: " + msg.err.Error())
		return
	}
	if len(msg.edits) == 0 {
		m.Feedback.NotifyInfo("Rename: no changes")
		return
	}
	for _, edit := range msg.edits {
		if tab := m.EditorState.Tabs[edit.Path]; tab != nil && tab.Editor.Modified() {
			m.Feedback.NotifyError("Save modified files before applying a workspace rename")
			return
		}
	}
	p := newLSPRenamePreview(m.Theme, msg)
	m.LSPPopup.RenamePreview = p
	w, h := min(100, max(1, m.width-4)), min(26, max(1, m.height-4))
	frame := ui.NewWindowFrameWidget(m.Theme, "lsp-rename/frame", ui.FrameDialog, "Review rename", p, nil, false, w, h)
	frame.Host().Focus(p.bar.ID())
	fw := wm.NewFloatingWindow(string(lspRenameOverlay), "", frame, w, h, wm.WithDecoration(true), wm.WithMovable(true), wm.WithResizable(true), wm.WithModal(true), wm.WithFocusable(true), wm.WithClosable(true), wm.WithClickOutside(wm.ClickOutsideIgnore))
	wm.CenterOnScreen(fw, m.width, m.height)
	m.overlayHost.Open(lspRenameOverlay, fw, ui.ModalDialogPolicy(), func() {
		if p.cancel != nil {
			p.cancel()
		}
		if m.LSPPopup.RenamePreview == p {
			m.LSPPopup.RenamePreview = nil
		}
	})
}
func (m *App) handleLSPRenameAction(msg lspRenameActionMsg) tea.Cmd {
	p := msg.preview
	if p == nil || m.LSPPopup.RenamePreview != p || p.busy {
		return nil
	}
	if msg.action == "cancel" {
		m.overlayHost.Close(lspRenameOverlay)
		return nil
	}
	r := p.request
	if !m.acceptsLSPDocument(r.generation, r.filePath, r.owner, r.revision) {
		m.overlayHost.Close(lspRenameOverlay)
		return nil
	}
	for _, edit := range p.edits {
		if tab := m.EditorState.Tabs[edit.Path]; tab != nil && (tab.Editor.Modified() || tab.Editor.Value() != edit.OldContent) {
			m.Feedback.NotifyError("Rename preview is stale; save changed buffers and try again")
			return nil
		}
	}
	p.busy = true
	p.bar.SetButtons(ui.ActionBarButton{Label: "Applying…", Action: "apply", Enabled: false})
	owner := m.Workspace
	ctx, _ := m.lspRequestContext()
	ctx, p.cancel = context.WithCancel(ctx)
	edits := append([]workspace.FileEdit(nil), p.edits...)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		result, safety := owner.ApplyLSPRename(ctx, edits)
		return lspRenameAppliedMsg{p, r.generation, result, safety}
	}
}
func (m *App) handleLSPRenameApplied(msg lspRenameAppliedMsg) {
	if !m.acceptsLSPGeneration(msg.generation) {
		return
	}
	if m.LSPPopup.RenamePreview != msg.preview {
		return
	}
	if msg.result.Err != nil {
		m.Feedback.NotifyError("Rename: " + msg.result.Err.Error())
		m.overlayHost.Close(lspRenameOverlay)
		return
	}
	for _, edit := range msg.preview.edits {
		if tab := m.EditorState.Tabs[edit.Path]; tab != nil {
			tab.Editor.SignalDiskChanged()
		}
	}
	m.Feedback.NotifyInfo(fmt.Sprintf("Renamed in %d files · recover via Local History (%s)", len(msg.preview.edits), msg.safety[:min(8, len(msg.safety))]))
	m.overlayHost.Close(lspRenameOverlay)
}
