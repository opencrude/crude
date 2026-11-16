package viewmodel

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/tui/editor"
	"crude/types"
	"crude/workspace"
)

// Disk awareness is the one policy that decides what the user hears about an
// open file that changed on disk. The core owns the text: it follows the file
// when the text is saved, merges external changes that touch no unsaved edit,
// and marks a conflict otherwise. The shell only tells the user, and only when
// unsaved work is affected:
//
//   - Saved text follows the file silently, as in every editor.
//   - External changes merged into unsaved text: one notice.
//   - A conflict or deletion with unsaved text: every view of the file shows
//     the disk bar (diskBarState), and a notice comes only when no view of the
//     file is visible.
//   - Each file says each thing once until its text is saved or discarded, and
//     notices raised together (a branch switch, a formatter run) are one notice.
const diskNoticeDelay = 300 * time.Millisecond

type diskNoticeKind uint8

const (
	diskNoticeMerged diskNoticeKind = iota + 1
	diskNoticeConflict
	diskNoticeRemoved
)

// diskEpisode remembers what one file already said since its text was last
// saved or discarded.
type diskEpisode struct{ merged, conflict bool }

type diskNotices struct {
	episodes  map[*editor.Editor]*diskEpisode
	queued    map[*editor.Editor]diskNoticeKind
	order     []*editor.Editor
	scheduled bool
}

type diskNoticeFlushMsg struct{ generation uint64 }

// editorDiskState is the one reader of a file's disk state for presentation:
// the core document for a connected editor, the local save state otherwise.
func (m *App) editorDiskState(ed *editor.Editor) (conflict, removed bool) {
	if ed == nil || !ed.IsOpen() || ed.FilePath() == "" {
		return false, false
	}
	ed = ed.Primary()
	if m.liveEditors[ed] != nil {
		return ed.DiskConflict(), ed.DiskConflict() && ed.DiskRemoved()
	}
	if state := m.editorSaves[ed.BufferID()]; state != nil && errors.Is(state.err, workspace.ErrDocumentConflict) {
		return true, state.removed
	}
	return false, false
}

// observeDiskState applies the policy after a document update. diskChange
// reports a change the core committed because the file changed; wasConflict
// is the state before the update.
func (m *App) observeDiskState(ed *editor.Editor, diskChange, wasConflict bool) {
	if ed == nil || !ed.IsOpen() {
		return
	}
	ed = ed.Primary()
	for known := range m.diskNotices.episodes {
		if !known.IsOpen() {
			delete(m.diskNotices.episodes, known)
		}
	}
	conflict, removed := m.editorDiskState(ed)
	if p := m.diskConflict; !conflict && p != nil && p.editor == ed && m.overlayHost != nil {
		// The conflict ended elsewhere (the file changed back, another
		// window chose): its question has no answer left to give.
		m.diskConflict = nil
		m.overlayHost.Close(diskConflictOverlay)
		m.cancelEditorClose(ed)
	}
	if !ed.Modified() && !conflict {
		// Saved or discarded: the next change on disk may speak again.
		delete(m.diskNotices.episodes, ed)
		return
	}
	if !ed.Modified() {
		return // nothing unsaved is at risk; the disk bar shows the state
	}
	if m.diskNotices.episodes == nil {
		m.diskNotices.episodes = make(map[*editor.Editor]*diskEpisode)
	}
	episode := m.diskNotices.episodes[ed]
	if episode == nil {
		episode = &diskEpisode{}
		m.diskNotices.episodes[ed] = episode
	}
	switch {
	case conflict && !wasConflict && !episode.conflict:
		episode.conflict = true
		kind := diskNoticeConflict
		if removed {
			kind = diskNoticeRemoved
		}
		m.queueDiskNotice(ed, kind)
	case diskChange && !conflict && !episode.merged:
		episode.merged = true
		m.queueDiskNotice(ed, diskNoticeMerged)
	}
}

func (m *App) queueDiskNotice(ed *editor.Editor, kind diskNoticeKind) {
	n := &m.diskNotices
	if n.queued == nil {
		n.queued = make(map[*editor.Editor]diskNoticeKind)
	}
	if _, ok := n.queued[ed]; !ok {
		n.order = append(n.order, ed)
	}
	n.queued[ed] = kind
	if n.scheduled {
		return
	}
	n.scheduled = true
	generation := m.workspaceSwitchGen.Load()
	m.editorCommands = append(m.editorCommands, tea.Tick(diskNoticeDelay, func(time.Time) tea.Msg {
		return diskNoticeFlushMsg{generation: generation}
	}))
}

// flushDiskNotices says what is still true when the short batch ends: a
// conflict the user resolved or opened in the meantime needs no notice.
func (m *App) flushDiskNotices(msg diskNoticeFlushMsg) {
	n := &m.diskNotices
	queued, order := n.queued, n.order
	n.queued, n.order, n.scheduled = nil, nil, false
	if msg.generation != m.workspaceSwitchGen.Load() {
		return
	}
	groups := map[diskNoticeKind][]*editor.Editor{}
	for _, ed := range order {
		kind := queued[ed]
		if !ed.IsOpen() || !ed.Modified() {
			continue
		}
		conflict, removed := m.editorDiskState(ed)
		switch kind {
		case diskNoticeMerged:
			if conflict {
				continue
			}
		case diskNoticeConflict, diskNoticeRemoved:
			if !conflict || m.editorVisible(ed) {
				continue
			}
			if removed {
				kind = diskNoticeRemoved
			}
		}
		groups[kind] = append(groups[kind], ed)
	}
	for _, kind := range []diskNoticeKind{diskNoticeConflict, diskNoticeRemoved, diskNoticeMerged} {
		if eds := groups[kind]; len(eds) > 0 {
			m.notifyDisk(kind, eds)
		}
	}
}

// diskNoticeText states what happened and the next step, naming up to three
// files.
func diskNoticeText(kind diskNoticeKind, names []string) string {
	list := names
	if len(list) > 3 {
		list = append(append([]string(nil), names[:3]...), fmt.Sprintf("%d more", len(names)-3))
	}
	files := strings.Join(list, ", ")
	one := len(names) == 1
	switch kind {
	case diskNoticeConflict:
		if one {
			return names[0] + " changed on disk and conflicts with your unsaved changes. Open it to choose the text to keep."
		}
		return fmt.Sprintf("%d files changed on disk and conflict with your unsaved changes: %s. Open them to choose the text to keep.", len(names), files)
	case diskNoticeRemoved:
		if one {
			return names[0] + " was deleted on disk. Your unsaved text is still open. Save it to create the file again."
		}
		return fmt.Sprintf("%d files were deleted on disk: %s. Your unsaved text is still open.", len(names), files)
	}
	if one {
		return names[0] + " changed on disk. Crude added the changes to your unsaved text."
	}
	return fmt.Sprintf("%d files changed on disk. Crude added the changes to your unsaved text: %s.", len(names), files)
}

func (m *App) notifyDisk(kind diskNoticeKind, eds []*editor.Editor) {
	center := m.Feedback.center()
	if center == nil {
		return
	}
	names, paths := make([]string, len(eds)), make([]string, len(eds))
	for i, ed := range eds {
		names[i], paths[i] = ed.BaseTitle(), ed.FilePath()
	}
	severity := types.SeverityWarning
	if kind == diskNoticeMerged {
		severity = types.SeverityInfo
	}
	// One file names its editor, so the notice opens it.
	owner := types.ResourceRef{Kind: types.ResourceWorkspace, ID: m.ws.RootPath()}
	if len(eds) == 1 {
		owner = types.ResourceRef{Kind: types.ResourceEditor, ID: paths[0]}
	}
	center.Notify(types.NotificationRecord{
		DedupKey: fmt.Sprintf("disk:%d:%s", kind, strings.Join(paths, "\x00")),
		Owner:    owner, Severity: severity, Source: "editor",
		Summary: diskNoticeText(kind, names), Detail: strings.Join(relativePaths(m.ws.RootPath(), paths), "\n"),
		Read: true, Transient: true,
	})
}

func relativePaths(root string, paths []string) []string {
	out := make([]string, len(paths))
	for i, path := range paths {
		out[i] = path
		if rel, err := filepath.Rel(root, path); err == nil && !strings.HasPrefix(rel, "..") {
			out[i] = rel
		}
	}
	return out
}

// editorVisible reports a document shown in a pane: one of its views is the
// selected tab of a group on screen.
func (m *App) editorVisible(ed *editor.Editor) bool {
	if m.Win == nil || ed == nil {
		return false
	}
	compact := m.mainPanes != nil && m.mainPanes.compact
	for _, v := range m.documentViews(editorDocumentKey(ed)) {
		tab := m.Win.TabByID(v.Tab)
		if tab == nil {
			continue
		}
		if active := m.Win.GroupActiveTab(tab.Group); active != nil && active.ID == v.Tab && (!compact || tab.Group == m.Win.ActiveGroup()) {
			return true
		}
	}
	return false
}
