package viewmodel

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/fileio"
	"crude/tui/editor"
	"crude/tui/window"
)

type paneFilePrepared struct {
	path     string
	buffer   *editor.Buffer
	err      error
	existing bool
}

type paneFilesOpenedMsg struct {
	seq, generation  uint64
	manager          *editor.BufferManager
	win              *window.Window
	group, initiated window.GroupID
	selected         window.TabID
	files            []paneFilePrepared
	landing          *fileio.ProjectSearchMatch
}

// openPaneFiles captures ownership on Update, but reads only in the command.
// Its private manager cannot publish buffers into a closed/replaced workspace.
func (m *App) openPaneFiles(paths []string, group window.GroupID) tea.Cmd {
	m.paneFileOpenSeq++ // An empty request also cancels an earlier pending open.
	if len(paths) == 0 || m.Win == nil || m.BufferMgr == nil || !paneHasGroup(m.Win.PaneRoot(), group) {
		return nil
	}
	msg := paneFilesOpenedMsg{seq: m.paneFileOpenSeq, generation: m.workspaceSwitchGen.Load(), manager: m.BufferMgr,
		win: m.Win, group: group, initiated: m.Win.ActiveGroup()}
	if tab := m.Win.ActiveTab(); tab != nil {
		msg.selected = tab.ID
	}
	// Copy the caller-owned slice before asynchronous execution.
	paths = append([]string(nil), paths...)
	return func() tea.Msg {
		prepared := editor.NewBufferManager()
		for _, path := range paths {
			abs, err := filepath.Abs(path)
			file := paneFilePrepared{path: path, err: err}
			if err == nil {
				file.path = filepath.Clean(abs)
				// Reading the manager's mutex-protected registry is safe. Do
				// not read an already open document from disk, even privately.
				file.existing = msg.manager.Get(file.path) != nil
				if !file.existing {
					file.buffer, file.err = prepared.OpenFile(file.path)
				}
			}
			msg.files = append(msg.files, file)
		}
		return msg
	}
}

func (m *App) applyPaneFilesOpened(msg paneFilesOpenedMsg) tea.Cmd {
	if msg.seq != m.paneFileOpenSeq || msg.generation != m.workspaceSwitchGen.Load() || msg.manager != m.BufferMgr ||
		msg.win != m.Win || m.Win == nil || !paneHasGroup(m.Win.PaneRoot(), msg.group) {
		return nil
	}
	currentGroup := m.Win.ActiveGroup()
	currentTab := window.TabID(0)
	if tab := m.Win.ActiveTab(); tab != nil {
		currentTab = tab.ID
	}
	foreground := currentGroup == msg.initiated && currentTab == msg.selected
	opened := false
	for _, file := range msg.files {
		// A file opened/edited during preparation is authoritative, including
		// when the delayed disk read failed after the file was removed.
		buf := m.BufferMgr.Get(file.path)
		if buf == nil && file.existing {
			// Explicit close/retarget while the command ran wins.
			continue
		}
		if buf == nil && file.err != nil {
			m.Feedback.NotifyError(fmt.Sprintf("Cannot open %s: %v", file.path, file.err))
			continue
		}
		if buf == nil {
			buf = m.BufferMgr.InstallPrepared(file.buffer)
		}
		if buf == nil {
			continue
		}
		m.installEditorBuffer(buf)
		m.Win.FocusGroup(msg.group)
		title := m.editorTabTitle(buf.Path)
		if m.Win.TabByTitle(title) == nil {
			m.Win.OpenTab(title, "✎", true)
		}
		tab := m.Win.TabByTitle(title)
		m.Win.SetTabDisplayTitleByIndex(m.Win.IndexOf(tab.ID), m.EditorState.Tabs[buf.Path].Editor.BaseTitle())
		if msg.landing != nil && tab.Group == msg.group {
			m.Win.SwitchTo(m.Win.IndexOf(tab.ID))
		} else {
			m.Win.MoveTabToGroup(tab.ID, msg.group, 0)
		}
		opened = true
	}
	if !foreground {
		// Restore the exact group even if it has no tab; selecting only a tab
		// cannot represent a user's deliberate empty-pane focus.
		if currentTab != 0 && m.Win.GroupOf(currentTab) == currentGroup {
			m.Win.SwitchTo(m.Win.IndexOf(currentTab))
		}
		if !m.Win.FocusGroup(currentGroup) {
			m.handleTabSwitch()
			m.forceFrame()
		}
		return nil
	}
	if !opened {
		m.Win.FocusGroup(currentGroup)
		return nil
	}
	m.handleTabSwitch()
	if match := msg.landing; match != nil && m.EditorState.ActiveView == match.Path {
		ed := m.EditorState.Editor
		line := max(0, match.Line-1)
		start, end, valid := searchLandingColumns(*match, ed.LineText(line))
		if !match.NameOnly && !valid {
			m.Feedback.NotifyInfo("Search result changed; the current editor text is preserved. Search again for the exact location.")
		} else if !match.NameOnly {
			ed.SetCursorPosition(line, start)
			ed.SetLandingRange(line, start, end)
			ed.ScrollToCursor()
		}
	}
	m.forceFrame()
	return m.refreshEditorGitCmd()
}

func searchLandingColumns(match fileio.ProjectSearchMatch, current string) (start, end int, valid bool) {
	from, to := match.Column, match.EndColumn
	if current != match.LineText {
		old := []rune(match.LineText)
		if from < 0 || to <= from || to > len(old) {
			return 0, 0, false
		}
		needle := string(old[from:to])
		live := []rune(current)
		if to > len(live) || string(live[from:to]) != needle {
			// Relocate only one exact occurrence on the same line. Never guess
			// between repeated hits or overwrite the live buffer with disk text.
			if strings.Count(current, needle) != 1 {
				return 0, 0, false
			}
			from = len([]rune(current[:strings.Index(current, needle)]))
			to = from + len([]rune(needle))
		}
	}
	return editor.RuneIndexToVisualColumn(current, from), editor.RuneIndexToVisualColumn(current, to), true
}
