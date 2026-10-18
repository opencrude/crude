package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/clientstate"
	"crude/document"
	"crude/fileio"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/window"
)

type paneFilePrepared struct {
	path        string
	buffer      *editor.Buffer
	err         error
	existing    bool
	recovery    clientstate.EditorRecoveryRecord
	recoveryErr error
	snapshot    *document.Snapshot
}

type paneFilesOpenedMsg struct {
	seq, generation  uint64
	manager          *editor.BufferManager
	win              *window.Window
	group, initiated window.GroupID
	selected         window.TabID
	files            []paneFilePrepared
	landing          *fileio.ProjectSearchMatch
	refs             *liveDocumentRefs
	position         *editor.LSPDiagnostic
	lspPosition      bool
	// window opens the landed tab in its own window (Open in window).
	window bool
}

// openPaneFiles captures ownership on Update, but reads only in the command.
// Its private manager cannot publish buffers into a closed/replaced workspace.
func (m *App) openPaneFiles(paths []string, group window.GroupID) tea.Cmd {
	m.paneFileOpenSeq++ // An empty request also cancels an earlier pending open.
	if len(paths) == 0 || m.Win == nil || m.BufferMgr == nil || !m.Win.HasGroup(group) {
		return nil
	}
	msg := paneFilesOpenedMsg{seq: m.paneFileOpenSeq, generation: m.workspaceSwitchGen.Load(), manager: m.BufferMgr,
		win: m.Win, group: group, initiated: m.Win.ActiveGroup()}
	if tab := m.Win.ActiveTab(); tab != nil {
		msg.selected = tab.ID
	}
	// Copy the caller-owned slice before asynchronous execution.
	paths = append([]string(nil), paths...)
	live, connected := m.ws.(shellport.LiveDocuments)
	if connected {
		if m.liveOpenRefs == nil || m.liveOpenRefs.owner != m.ws {
			m.liveOpenRefs = &liveDocumentRefs{owner: m.ws, live: live, counts: make(map[string]int)}
		}
		msg.refs = m.liveOpenRefs
	}
	loadRecovery := m.prepareEditorRecovery()
	return func() tea.Msg {
		prepared := editor.NewBufferManager()
		for _, path := range paths {
			if connected {
				s, err := msg.refs.open(context.Background(), path)
				file := paneFilePrepared{path: s.Path, err: err, snapshot: &s}
				if err == nil {
					file.recovery, file.recoveryErr = loadRecovery(s.Path)
				}
				msg.files = append(msg.files, file)
				continue
			}
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
		msg.win != m.Win || m.Win == nil || !m.Win.HasGroup(msg.group) {
		return releasePaneDocuments(msg)
	}
	currentGroup := m.Win.ActiveGroup()
	currentTab := window.TabID(0)
	if tab := m.Win.ActiveTab(); tab != nil {
		currentTab = tab.ID
	}
	foreground := currentGroup == msg.initiated && currentTab == msg.selected
	opened := false
	for _, file := range msg.files {
		if file.snapshot != nil {
			if file.err != nil {
				m.Feedback.NotifyError(fmt.Sprintf("Cannot open document: %v", file.err))
				continue
			}
			if err := m.installLiveEditor(*file.snapshot); err != nil {
				m.Feedback.NotifyError(err.Error())
				m.editorCommands = append(m.editorCommands, msg.refs.releaseCmd(file.snapshot.ID))
				continue
			}
			state := m.liveEditors[m.EditorState.Tabs[file.path].Editor]
			if state.refs != nil {
				// Reopening an already live view must not reinstall an older private
				// recovery over edits made since that view was first opened.
			} else if file.recoveryErr != nil {
				m.showOperationFailure("Editor recovery could not be loaded", file.recoveryErr.Error())
			} else if err := m.installEditorRecovery(m.EditorState.Tabs[file.path].Editor, file.recovery); err != nil {
				m.showOperationFailure("Editor draft could not be restored", err.Error())
			}
			if state.refs == nil {
				state.refs = msg.refs
			} else {
				m.editorCommands = append(m.editorCommands, msg.refs.releaseCmd(file.snapshot.ID))
			}
			m.Win.FocusGroup(msg.group)
			m.activateEditorTab(file.path)
			if tab := m.Win.TabByTitle(m.editorTabTitle(file.path)); tab != nil {
				m.Win.MoveTabToGroup(tab.ID, msg.group, 0)
			}
			opened = true
			continue
		}
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
	if p := msg.position; p != nil && m.EditorState.Editor != nil {
		ed := m.EditorState.Editor
		if msg.lspPosition {
			if err := ed.SetLSPCursorPosition(p.Line, p.Col); err != nil {
				m.Feedback.NotifyError(err.Error())
			}
		} else {
			ed.SetCursorPosition(p.Line, p.Col)
			ed.SetSelection(p.Line, p.Col, p.EndLine, p.EndCol)
		}
		ed.ScrollToCursor()
	}
	if tab := m.Win.ActiveTab(); msg.window && tab != nil && m.isEditorTabTitle(tab.Title) {
		m.openTabInWindow(tab.ID)
	}
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

func releasePaneDocuments(msg paneFilesOpenedMsg) tea.Cmd {
	if msg.refs == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, file := range msg.files {
		if file.err == nil && file.snapshot != nil {
			cmds = append(cmds, msg.refs.releaseCmd(file.snapshot.ID))
		}
	}
	return tea.Batch(cmds...)
}

// queueLiveNavigation captures the destination and position together. Never
// position the old active editor before the asynchronous open is installed.
func (m *App) queueLiveNavigation(path string, position editor.LSPDiagnostic, lsp bool) bool {
	if _, ok := m.ws.(shellport.LiveDocuments); !ok {
		return false
	}
	cmd := m.openPaneFiles([]string{path}, m.Win.ActiveGroup())
	if cmd != nil {
		m.editorCommands = append(m.editorCommands, func() tea.Msg {
			msg := cmd().(paneFilesOpenedMsg)
			msg.position, msg.lspPosition = &position, lsp
			return msg
		})
	}
	return true
}

func searchLandingColumns(match fileio.ProjectSearchMatch, current string) (start, end int, valid bool) {
	from, to := match.Column, match.EndColumn
	if match.EndLine > match.Line {
		to = len([]rune(match.LineText))
	}
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
