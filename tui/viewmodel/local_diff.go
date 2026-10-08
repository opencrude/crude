package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/tui/screens/diffview"
	"crude/tui/screens/localdiff"
	"crude/tui/ui"
	"crude/tui/window"
)

type localDiffBaseline string

const (
	localDiffDisk  localDiffBaseline = "disk"
	localDiffSaved localDiffBaseline = "saved"
	localDiffLimit                   = 64 << 20
)

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
}

type localDiffPreparedMsg struct {
	owner                    *localDiffView
	generation, sequence     uint64
	revision, diskGeneration uint64
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

func (m *App) compareEditorWithDisk(ed *editor.Editor) tea.Cmd {
	return m.openLocalDiff(ed, localDiffDisk)
}

func (m *App) openLocalDiff(ed *editor.Editor, baseline localDiffBaseline) tea.Cmd {
	if ed == nil || !ed.IsOpen() || m.Win == nil || ed.BufferID() == "" {
		return nil
	}
	resource := window.Resource{Kind: window.ResourceLocalDiff, ID: ed.BufferID() + ":" + string(baseline)}
	returnTab := window.TabID(0)
	if tab := m.Win.TabByTitle(m.editorTabTitle(ed.FilePath())); tab != nil {
		returnTab = tab.ID
	}
	tab := m.Win.OpenResource(resource, fmt.Sprintf("%s · %s diff", ed.BaseTitle(), baseline), true)
	if m.localDiffs == nil {
		m.localDiffs = make(map[window.TabID]*localDiffView)
	}
	view := m.localDiffs[tab.ID]
	if view == nil {
		screen := localdiff.NewWithID(m.Theme, ui.ComponentID(fmt.Sprintf("localdiff/%d", tab.ID)))
		screen.SetFocusHandler(m.rootHost.Focus)
		view = &localDiffView{tab: tab.ID, returnTab: returnTab, source: ed, sourceID: ed.BufferID(), baseline: baseline, screen: screen}
		m.localDiffs[tab.ID] = view
	}
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
	tab := m.EditorState.Tabs[view.source.FilePath()]
	return tab != nil && tab.Editor == view.source
}

func (m *App) prepareLocalDiff(view *localDiffView) tea.Cmd {
	if !m.localDiffSourceLive(view) {
		view.screen.SetError(errors.New("source editor is closed; this snapshot is retained, but cannot be refreshed"))
		return nil
	}
	if view.cancel != nil {
		view.cancel()
	}
	view.sequence++
	ctx, cancel := context.WithCancel(context.Background())
	view.cancel = cancel
	view.screen.SetLoading(true)
	snapshot := view.source.BufferSnapshot() // owned bytes, captured on Update
	baseline := view.baseline
	var saved []byte
	if baseline == localDiffSaved {
		buffer := m.BufferMgr.Get(snapshot.Path)
		if buffer == nil || buffer.ID != snapshot.ID {
			cancel()
			view.cancel = nil
			view.screen.SetError(errors.New("source buffer changed; reopen the comparison"))
			return nil
		}
		saved = buffer.SavedBytes()
	}
	msg := localDiffPreparedMsg{owner: view, sequence: view.sequence, generation: m.workspaceSwitchGen.Load(), revision: snapshot.Revision, diskGeneration: view.source.DiskGeneration()}
	return func() tea.Msg {
		defer cancel()
		before, err := saved, ctx.Err()
		label := "Saved version"
		if err == nil && baseline == localDiffDisk {
			label = "Disk snapshot"
			before, err = readLocalDiffDisk(ctx, snapshot.Path)
		}
		if err == nil && (len(before) > localDiffLimit || len(snapshot.Data) > localDiffLimit) {
			err = errors.New("comparison exceeds the 64 MiB per-version limit")
		}
		if err == nil {
			msg.prepared = diffview.Prepare(diffview.Comparison{Path: snapshot.Path, BeforeLabel: label, AfterLabel: fmt.Sprintf("Editor snapshot · revision %d", snapshot.Revision), Before: string(before), After: string(snapshot.Data)})
			err = ctx.Err()
		}
		msg.err = err
		return msg
	}
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
	view.revision, view.diskGeneration = msg.revision, msg.diskGeneration
	view.screen.SetPrepared(msg.prepared)
	m.syncLocalDiffFreshness()
	m.forceFrame()
}

// Version checks are cheap owner facts. Never export or diff source text during
// layout/paint, and never silently replace the evidence while the user reads it.
func (m *App) syncLocalDiffFreshness() {
	for _, view := range m.localDiffs {
		notice := "Read-only snapshot · Refresh to compare the latest versions"
		switch {
		case !m.localDiffSourceLive(view):
			notice = "Source closed · retained read-only snapshot"
		case view.source.Revision() != view.revision || view.source.DiskGeneration() != view.diskGeneration:
			notice = "Source changed · showing the previous snapshot; Refresh to update"
		}
		if notice != view.notice {
			view.notice = notice
			view.screen.SetNotice(notice)
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
	m.Win.CloseTabByID(id)
	m.handleTabSwitch()
	m.forceFrame()
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
		case localdiff.ActionOpenSource:
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
