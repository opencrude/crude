package viewmodel

import (
	"context"
	"crude/text/textencoding"
	"crude/workspace"
	"crude/workspace/document"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/core/shellport"
	"crude/tui/editor"
	"crude/tui/screens/diffview"
	"crude/tui/screens/localdiff"
	"crude/tui/ui"
	"crude/tui/window"
)

type localDiffBaseline string

const (
	localDiffDisk        localDiffBaseline = "disk"
	localDiffSaved       localDiffBaseline = "saved"
	localDiffShared      localDiffBaseline = "shared"
	localDiffReplacement localDiffBaseline = "replacement"
	localDiffLimit                         = 64 << 20
)

// localDiffTitles names what a comparison tab compares the text with, in the
// user's words; the tooltip gives both versions in full.
var localDiffTitles = map[localDiffBaseline]string{
	localDiffDisk:        "disk diff",
	localDiffSaved:       "saved diff",
	localDiffShared:      "unapplied diff",
	localDiffReplacement: "existing file diff",
}

// A local comparison owns immutable evidence, never another live buffer. Tab
// membership/focus stay with Window, source content/savepoint with editor.Buffer.
type localDiffView struct {
	tab, returnTab           window.TabID
	source                   *editor.Editor
	sourceID                 string
	baseline                 localDiffBaseline
	screen                   *localdiff.Screen
	revision, diskGeneration uint64
	sequence                 uint64
	cancel                   context.CancelFunc
	notice                   string
	returnSave               *untitledSaveOwner
	// diskHash identifies the disk version a disk comparison shows. Saving a
	// resolution requires that exact version, never a newer unseen one.
	diskHash [32]byte
	// diskSeen is the core's file observation the comparison was prepared at.
	diskSeen [32]byte
	// hadChanges records that the comparison showed differences: when a
	// later preparation finds none, the decision was made elsewhere.
	hadChanges bool
	// refreshDue is the pending debounced refresh; wasActive notes the last
	// sync's tab selection, so selecting the tab reads the disk again.
	refreshDue, wasActive bool
}

// localDiffRefreshMsg runs a debounced refresh of one comparison.
type localDiffRefreshMsg struct {
	owner      *localDiffView
	generation uint64
}

// localDiffRefreshDelay coalesces typing and repeated disk writes into one
// preparation.
const localDiffRefreshDelay = 200 * time.Millisecond

type localDiffPreparedMsg struct {
	widthGen                 uint64
	owner                    *localDiffView
	generation, sequence     uint64
	revision, diskGeneration uint64
	diskHash, diskSeen       [32]byte
	prepared                 diffview.Prepared
	err                      error
}

func (m *App) activeLocalDiff() *localDiffView {
	if m.Win != nil {
		if tab := m.Win.ActiveTab(); tab != nil {
			return m.localDiffs[tab.ID]
		}
	}
	return nil
}

func (m *App) isComparisonActive() bool {
	return m.isGitReviewActive() || m.activeLocalDiff() != nil
}

// hostTabActive reports a main tab whose content is an ordinary host
// component (a comparison or a surface moved out of the dock): keys, close
// and chat visibility follow the host, not the composer or editor.
func (m *App) hostTabActive() bool {
	return m.isComparisonActive() || m.activePanelTabContent() != nil || m.activeImageView() != nil
}

func (m *App) compareEditorWithDisk(ed *editor.Editor) tea.Cmd {
	return m.openLocalDiff(ed, localDiffDisk)
}

func (m *App) openLocalDiff(ed *editor.Editor, baseline localDiffBaseline) tea.Cmd {
	if ed == nil || !ed.IsOpen() || m.Win == nil || ed.BufferID() == "" {
		return nil
	}
	resource := window.Resource{Kind: window.ResourceLocalDiff, ID: ed.BufferID() + ":" + string(baseline)}
	returnTab := window.TabID(0)
	if v := m.editorViewOf(ed); v != nil {
		returnTab = v.Tab
	} else if views := m.documentViews(ed.FilePath()); len(views) > 0 {
		returnTab = views[0].Tab
	}
	tab := m.Win.OpenResource(resource, ed.BaseTitle()+" · "+localDiffTitles[baseline], true)
	if m.localDiffs == nil {
		m.localDiffs = make(map[window.TabID]*localDiffView)
	}
	view := m.localDiffs[tab.ID]
	if view == nil {
		screen := localdiff.NewWithID(m.Theme, ui.ComponentID(fmt.Sprintf("localdiff/%d", tab.ID)))
		screen.SetFocusHandler(m.rootHost.Focus)
		view = &localDiffView{tab: tab.ID, returnTab: returnTab, source: ed, sourceID: ed.BufferID(), baseline: baseline, screen: screen}
		if baseline == localDiffShared || baseline == localDiffReplacement {
			screen.SetRefreshEnabled(false)
		} else if !ed.ReadOnly() {
			// Every change starts as the user's text; accepting a change from
			// the left takes the saved version of that change.
			screen.SetResolvable(true, diffview.ChoiceRight)
		}
		m.localDiffs[tab.ID] = view
	}
	view.wasActive = true // just opened: this preparation reads the file
	m.FocusStack.Clear()
	m.setTermDockFocused(false)
	m.setSessionDeckFocused(false)
	m.handleTabSwitch()
	return m.prepareLocalDiff(view)
}

func (m *App) localDiffSourceLive(view *localDiffView) bool {
	if view == nil || view.source == nil || !view.source.IsOpen() || view.source.BufferID() != view.sourceID || m.EditorState == nil {
		return false
	}
	tab := m.EditorState.Tabs[editorDocumentKey(view.source)]
	return tab != nil && tab.Editor == view.source
}

func (m *App) prepareLocalDiff(view *localDiffView) tea.Cmd {
	if view.baseline == localDiffShared || view.baseline == localDiffReplacement {
		return nil
	}
	if !m.localDiffSourceLive(view) {
		view.screen.SetError(errors.New("source editor is closed; this snapshot is retained, but cannot be refreshed"))
		return nil
	}
	if view.cancel != nil {
		view.cancel()
	}
	view.sequence++
	ctx, cancel := m.sourceControlContext()
	view.cancel = cancel
	view.screen.SetLoading(true)
	snapshot := view.source.BufferSnapshot() // owned bytes, captured on Update
	baseline := view.baseline
	var saved []byte
	if baseline == localDiffSaved {
		buffer := m.BufferMgr.Get(editorDocumentKey(view.source))
		if buffer == nil || buffer.ID != snapshot.ID {
			cancel()
			view.cancel = nil
			view.screen.SetError(errors.New("source buffer changed; reopen the comparison"))
			return nil
		}
		saved = buffer.SavedBytes()
	}
	msg := localDiffPreparedMsg{owner: view, sequence: view.sequence, generation: m.workspaceSwitchGen.Load(), widthGen: m.comparisonWidthGen, revision: snapshot.Revision, diskGeneration: view.source.DiskGeneration(), diskSeen: view.source.DiskSeen()}
	files, remote := m.ws.(shellport.ClientFiles)
	return m.comparisonCommand(cancel, func() tea.Msg {
		defer cancel()
		before, err := saved, ctx.Err()
		label := "Saved version"
		if err == nil && baseline == localDiffDisk {
			label = "On disk"
			if remote {
				before, err = files.ReadExternalClientFile(ctx, snapshot.Path)
			} else {
				before, err = readLocalDiffDisk(ctx, snapshot.Path)
			}
			msg.diskHash = sha256.Sum256(before)
			if err == nil {
				before, _, err = textencoding.DecodeText(before)
			}
		}
		if err == nil && (len(before) > localDiffLimit || len(snapshot.Data) > localDiffLimit) {
			err = errors.New("comparison exceeds the 64 MiB per-version limit")
		}
		if err == nil {
			msg.prepared, err = diffview.PrepareContext(ctx, diffview.Comparison{Path: snapshot.Path, BeforeLabel: label, AfterLabel: "Your text", Before: string(before), After: string(snapshot.Data)})
		}
		msg.err = err
		return msg
	})
}

// openSnapshotComparison shares the same tab, layout, reader and async
// preparation as saved/disk comparisons. The supplied versions are immutable.
func (m *App) openSnapshotComparison(ed *editor.Editor, baseline localDiffBaseline, comparison diffview.Comparison, revision uint64) tea.Cmd {
	_ = m.openLocalDiff(ed, baseline)
	view := m.activeLocalDiff()
	if view == nil {
		return nil
	}
	if view.cancel != nil {
		view.cancel()
	}
	view.sequence++
	ctx, cancel := m.sourceControlContext()
	view.cancel = cancel
	view.screen.SetLoading(true)
	view.screen.SetNotice("Read-only comparison.")
	msg := localDiffPreparedMsg{owner: view, sequence: view.sequence, generation: m.workspaceSwitchGen.Load(),
		widthGen: m.comparisonWidthGen, revision: revision, diskGeneration: ed.DiskGeneration()}
	return m.comparisonCommand(cancel, func() tea.Msg {
		defer cancel()
		msg.prepared, msg.err = diffview.PrepareContext(ctx, comparison)
		return msg
	})
}

func readLocalDiffDisk(ctx context.Context, path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("comparison source is not a regular file")
	}
	if info.Size() > localDiffLimit {
		return nil, errors.New("disk version exceeds the 64 MiB comparison limit")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	// A chunked reader bounds memory and observes cancellation between reads.
	var content strings.Builder
	chunk := make([]byte, 32<<10)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		n, readErr := file.Read(chunk)
		if content.Len()+n > localDiffLimit {
			return nil, errors.New("disk version exceeds the 64 MiB comparison limit")
		}
		content.Write(chunk[:n])
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, readErr
		}
	}
	return []byte(content.String()), nil
}

func (m *App) applyLocalDiffPrepared(msg localDiffPreparedMsg) {
	view := msg.owner
	if view == nil || msg.generation != m.workspaceSwitchGen.Load() || m.localDiffs[view.tab] != view || msg.sequence != view.sequence || m.Win.IndexOf(view.tab) < 0 {
		return
	}
	view.cancel = nil
	if msg.err != nil {
		view.screen.SetError(msg.err)
		return
	}
	reader := view.screen.ComparisonReader()
	if view.screen.Resolvable() && view.hadChanges && msg.prepared.HunkCount() == 0 {
		// Nothing is left to choose: the text and the file agree, because the
		// user discarded, overwrote or edited elsewhere, or the file changed
		// back. A comparison with no decision left closes itself.
		ed := view.source
		m.closeLocalDiff(view.tab)
		m.returnToEditor(ed)
		m.Feedback.NotifyInfo("No differences remain, so the comparison closed.")
		return
	}
	// Decisions belong to changes, not positions: a change that is still the
	// same keeps its choice when the comparison is prepared again.
	decisions := reader.Decisions()
	view.revision, view.diskGeneration, view.diskHash, view.diskSeen = msg.revision, msg.diskGeneration, msg.diskHash, msg.diskSeen
	previous := reader.SourceGeometry()
	view.screen.SetPrepared(msg.prepared)
	if len(decisions) > 0 {
		reader.RestoreChoices(decisions)
	}
	view.hadChanges = view.hadChanges || msg.prepared.HunkCount() > 0
	m.cancelOrphanedComparisonGeometry(previous)
	if msg.widthGen != m.comparisonWidthGen {
		m.comparisonReflow = true
	}
	m.forceFrame()
}

// localDiffStale reports a comparison whose text, save baseline or file
// changed after it was prepared. The file can change again while a conflict
// waits, without a new text revision or baseline: DiskSeen tells.
func localDiffStale(view *localDiffView) bool {
	return view.source.Revision() != view.revision || view.source.DiskGeneration() != view.diskGeneration ||
		view.baseline == localDiffDisk && view.source.DiskSeen() != view.diskSeen
}

// localDiffRefreshable reports a comparison that follows its sources: the
// disk and saved-version comparisons. Shared and replacement comparisons are
// fixed evidence.
func localDiffRefreshable(view *localDiffView) bool {
	return view.baseline != localDiffShared && view.baseline != localDiffReplacement
}

// syncLocalDiffFreshness keeps every comparison true to its sources, so its
// choices always act on what the user sees. Version checks are cheap owner
// facts; the work is one debounced asynchronous preparation. A comparison
// whose file closed can change nothing, so it closes with it. Selecting a disk
// comparison reads the file again: the disk can change without a new text
// revision while a conflict waits.
func (m *App) syncLocalDiffFreshness() tea.Cmd {
	var cmds []tea.Cmd
	active := m.activeLocalDiff()
	for _, view := range m.localDiffs {
		wasActive := view.wasActive
		view.wasActive = view == active
		if !localDiffRefreshable(view) {
			continue
		}
		if !m.localDiffSourceLive(view) {
			if view.screen.Resolvable() {
				m.closeLocalDiff(view.tab)
				continue
			}
			if notice := "The file is closed. This comparison cannot change it."; notice != view.notice {
				view.notice = notice
				view.screen.SetNotice(notice)
			}
			continue
		}
		stale := localDiffStale(view)
		reselected := view == active && !wasActive && view.baseline == localDiffDisk && view.sequence > 0
		if (stale || reselected) && !view.refreshDue {
			view.refreshDue = true
			generation := m.workspaceSwitchGen.Load()
			cmds = append(cmds, tea.Tick(localDiffRefreshDelay, func(time.Time) tea.Msg {
				return localDiffRefreshMsg{owner: view, generation: generation}
			}))
		}
	}
	return tea.Batch(cmds...)
}

// applyLocalDiffRefresh prepares a stale comparison again. A preparation in
// flight finishes first; the next sync sees whether it is still stale.
func (m *App) applyLocalDiffRefresh(msg localDiffRefreshMsg) tea.Cmd {
	view := msg.owner
	if view == nil || msg.generation != m.workspaceSwitchGen.Load() || m.localDiffs[view.tab] != view {
		return nil
	}
	view.refreshDue = false
	if view.cancel != nil {
		return nil
	}
	return m.prepareLocalDiff(view)
}

// saveLocalDiffResolution writes the chosen changes into the source document
// and saves it. A disk comparison also makes the shown disk version the save
// baseline, which settles a disk conflict.
func (m *App) saveLocalDiffResolution(view *localDiffView) tea.Cmd {
	reader := view.screen.ComparisonReader()
	text, decided := reader.ResolvedText()
	switch {
	case !decided:
		m.Feedback.NotifyInfo("Choose a side for each change, then save.")
		return nil
	case !m.localDiffSourceLive(view):
		m.Feedback.NotifyError("The file is closed. Open it and compare again.")
		return nil
	case localDiffStale(view):
		// Never save choices made against older text: compare again first.
		m.Feedback.NotifyInfo("The text changed. Crude updated the comparison. Check it, then save again.")
		return m.prepareLocalDiff(view)
	}
	ed := view.source
	if m.liveEditors[ed.Primary()] != nil {
		resolve := &document.Resolution{Text: []byte(text), Replace: true}
		if view.baseline == localDiffDisk {
			resolve.DiskHash = view.diskHash
		} else if ed.DiskConflict() {
			m.Feedback.NotifyInfo("The file changed on disk. Compare with the disk version instead.")
			return nil
		}
		m.resolveLiveDisk(ed, resolve, view.revision, false)
	} else {
		if view.baseline == localDiffDisk {
			// The compared disk version becomes the save baseline, which
			// settles a disk conflict for this text.
			ed.AcceptDiskBaseline([]byte(reader.Comparison().Before), view.diskHash, true)
			delete(m.editorSaves, ed.BufferID())
		}
		ed.ApplyFormatted(text)
		m.closeLocalDiff(view.tab)
		m.returnToEditor(ed)
		return m.requestEditorSave(ed, workspace.DocumentSaveManual, false)
	}
	m.closeLocalDiff(view.tab)
	m.returnToEditor(ed)
	return nil
}

// returnToEditor selects a view of ed after a comparison closes.
func (m *App) returnToEditor(ed *editor.Editor) {
	if v := m.editorViewOf(ed); v != nil {
		if index := m.Win.IndexOf(v.Tab); index >= 0 {
			m.Win.SwitchTo(index)
			m.handleTabSwitch()
			m.FocusStack.Clear()
		}
	}
}

func (m *App) closeLocalDiff(id window.TabID) {
	view := m.localDiffs[id]
	if view == nil {
		return
	}
	if view.cancel != nil {
		view.cancel()
	}
	delete(m.localDiffs, id)
	m.cancelOrphanedComparisonGeometry(view.screen.ComparisonReader().SourceGeometry())
	view.screen.ComparisonReader().StopSearch()
	m.Win.CloseTabByID(id)
	m.handleTabSwitch()
	m.forceFrame()
	m.restoreUntitledSave(view.returnSave)
}

func (m *App) cancelLocalDiffs() {
	for _, view := range m.localDiffs {
		if view.cancel != nil {
			view.cancel()
			view.cancel = nil
		}
		view.sequence++
		view.screen.SetLoading(false)
	}
}

func (m *App) handleLocalDiffAction(action ui.Action) (tea.Cmd, bool) {
	if !strings.HasPrefix(action.ID, "localdiff.") {
		return nil, false
	}
	target, ok := action.Payload.(*localdiff.Screen)
	if !ok {
		return nil, true
	}
	for _, view := range m.localDiffs {
		if view.screen != target || m.Win.IndexOf(view.tab) < 0 {
			continue
		}
		switch action.ID {
		case localdiff.ActionRefresh:
			return m.prepareLocalDiff(view), true
		case localdiff.ActionClose:
			m.closeLocalDiff(view.tab)
		case localdiff.ActionSave:
			return m.saveLocalDiffResolution(view), true
		case localdiff.ActionOpenSource:
			if view.returnSave != nil {
				m.closeLocalDiff(view.tab)
				return nil, true
			}
			if !m.localDiffSourceLive(view) {
				view.screen.SetError(errors.New("source editor is closed; open the file explicitly to start a new editor lifetime"))
				return nil, true
			}
			if index := m.Win.IndexOf(view.returnTab); index >= 0 {
				m.Win.SwitchTo(index)
				m.handleTabSwitch()
				m.FocusStack.Clear()
			}
		}
		return nil, true
	}
	return nil, true
}
