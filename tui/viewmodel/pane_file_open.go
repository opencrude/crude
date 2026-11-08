package viewmodel

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/clientstate"
	"crude/document"
	"crude/fileio"
	"crude/shellport"
	"crude/tui/editor"
	"crude/tui/imagepreview"
	"crude/tui/window"
	"crude/types"
)

type paneFilePrepared struct {
	path        string
	buffer      *editor.Buffer
	err         error
	existing    bool
	imageFile   bool
	image       *paneImage
	imageTab    window.TabID
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
	diagnosticFacts  *types.InsightDiagnostics
	position         *editor.LSPDiagnostic // raw UTF-16 range, including point locations
	restore          *navLocation          // Go Back/Forward place of a reopened file
	navigation       *paneNavigation
	// window = landed tab in its own window (Open in window).
	window bool
	// activePath selects the remembered document after a restored pane opens.
	activePath string
	// restoring accepts several pane opens queued by one layout restoration.
	restoring bool
	// restoreForeground keeps the remembered active pane focused while other
	// panes reopen in the background.
	restoreForeground bool
}

// openPaneFiles captures ownership on Update, but reads only in the command.
// Its private manager cannot publish buffers into a closed/replaced workspace.
func (m *App) openPaneFiles(paths []string, group window.GroupID) tea.Cmd {
	return m.openPaneFilesContext(context.Background(), paths, group)
}

func (m *App) openPaneFilesContext(ctx context.Context, paths []string, group window.GroupID) tea.Cmd {
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
	files, remoteFiles := m.ws.(shellport.ClientFiles)
	root := m.ws.RootPath()
	existingImages := make(map[string]window.TabID)
	for _, path := range paths {
		if imagepreview.SupportedPath(path) {
			for _, tab := range m.Win.Tabs() {
				if tab.Group == group && isImageTab(&tab) {
					existingImages[tab.Resource.ID] = tab.ID
				}
			}
			break
		}
	}
	if connected {
		if m.liveOpenRefs == nil || m.liveOpenRefs.owner != m.ws {
			m.liveOpenRefs = &liveDocumentRefs{owner: m.ws, live: live, counts: make(map[string]int)}
		}
		msg.refs = m.liveOpenRefs
	}
	loadRecovery := m.prepareEditorRecovery()
	command := m.clientIOCommand(ctx, func(owner context.Context) tea.Msg {
		ctx, cancel := context.WithTimeout(owner, 2*time.Minute)
		defer cancel()
		prepared := editor.NewBufferManager()
		for _, path := range paths {
			if imagepreview.SupportedPath(path) {
				file := paneFilePrepared{path: path, imageFile: true}
				if remoteFiles {
					file.path = files.CanonicalClientPath(path)
				} else {
					abs, err := filepath.Abs(path)
					file.err = err
					if err == nil {
						file.path = filepath.Clean(abs)
					}
				}
				file.imageTab = existingImages[file.path]
				if file.err == nil && file.imageTab == 0 {
					if remoteFiles {
						external := !clientPathWithinRoot(files.CanonicalClientPath(root), file.path)
						file.image, file.err = preparePaneImage(ctx, files, file.path, external)
					} else {
						file.image, file.err = preparePaneImage(ctx, nil, file.path, false)
					}
				}
				msg.files = append(msg.files, file)
				continue
			}
			if connected {
				s, err := msg.refs.open(ctx, path)
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
	})
	return func() tea.Msg {
		result := command()
		if result == nil {
			// Several callers decorate the typed completion. A command cancelled
			// before admission has no acquired resources or files to install.
			return msg
		}
		return result
	}
}

func (m *App) applyPaneFilesOpened(msg paneFilesOpenedMsg) tea.Cmd {
	if msg.navigation != nil && !m.navigationCurrent(msg.navigation.owner) {
		return releasePaneDocuments(msg)
	}
	if msg.restoring {
		if m.paneFileOpenSeq > m.paneRestoreCeiling {
			return releasePaneDocuments(msg)
		}
	} else if msg.seq != m.paneFileOpenSeq {
		return releasePaneDocuments(msg)
	}
	if msg.generation != m.workspaceSwitchGen.Load() || msg.manager != m.BufferMgr ||
		msg.win != m.Win || m.Win == nil || !m.Win.HasGroup(msg.group) {
		return releasePaneDocuments(msg)
	}
	if msg.navigation != nil && !m.navigationLandingMatches(msg) {
		m.closeNavigation()
		if len(msg.files) == 1 && msg.files[0].err != nil {
			m.Feedback.NotifyError(fmt.Sprintf("Cannot open navigation target: %v", msg.files[0].err))
		} else if len(msg.files) == 1 && (msg.files[0].recoveryErr != nil || len(msg.files[0].recovery.Payload) != 0) {
			m.Feedback.NotifyInfo("Navigation target has unresolved private recovery; open the file normally to resolve it first.")
		} else {
			m.Feedback.NotifyInfo("Navigation target changed; no tab or cursor was changed. Request the location again.")
		}
		return releasePaneDocuments(msg)
	}
	currentGroup := m.Win.ActiveGroup()
	currentTab := window.TabID(0)
	if tab := m.Win.ActiveTab(); tab != nil {
		currentTab = tab.ID
	}
	foreground := currentGroup == msg.initiated && currentTab == msg.selected
	if msg.restoring {
		foreground = msg.restoreForeground
	}
	opened := false
	for _, file := range msg.files {
		if file.imageFile {
			if file.imageTab != 0 {
				current := m.Win.GroupTabByTitle(msg.group, imageTabTitle(file.path))
				if current == nil || current.ID != file.imageTab {
					continue
				}
			}
			if file.err != nil {
				if !msg.restoring {
					m.Feedback.NotifyError("Could not open image. Check the file and try again.")
				}
				continue
			}
			if m.showPaneImage(file.path, msg.group, foreground, file.image) {
				opened = true
			}
			continue
		}
		if file.snapshot != nil {
			if file.err != nil {
				if !msg.restoring {
					m.Feedback.NotifyError(fmt.Sprintf("Cannot open document: %v", file.err))
				}
				continue
			}
			if err := m.installLiveEditor(*file.snapshot); err != nil {
				if !msg.restoring {
					m.Feedback.NotifyError(err.Error())
				}
				m.editorCommands = append(m.editorCommands, msg.refs.releaseCmd(file.snapshot.ID))
				continue
			}
			state := m.liveEditors[m.EditorState.Tabs[file.path].Editor.Primary()]
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
			// The file opens where it was requested. A view elsewhere stays
			// where it is; this pane gets its own view of the same document.
			if m.showDocument(file.path, msg.group, foreground) != nil {
				opened = true
			}
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
			if !msg.restoring {
				m.Feedback.NotifyError(fmt.Sprintf("Cannot open %s: %v", file.path, file.err))
			}
			continue
		}
		if buf == nil {
			buf = m.BufferMgr.InstallPrepared(file.buffer)
		}
		if buf == nil {
			continue
		}
		m.installEditorBuffer(buf)
		if m.showDocument(buf.Path, msg.group, foreground) != nil {
			opened = true
		}
	}
	if msg.activePath != "" {
		// A restored pane reopens its last selected document, not simply the
		// last file in the remembered order.
		tab := m.Win.GroupTabByTitle(msg.group, m.editorTabTitle(msg.activePath))
		if tab == nil {
			tab = m.Win.GroupTabByTitle(msg.group, imageTabTitle(msg.activePath))
		}
		if tab != nil {
			if foreground {
				m.Win.SwitchTo(m.Win.IndexOf(tab.ID))
			} else {
				m.Win.SelectGroupTab(msg.group, tab.ID)
			}
		}
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
	if msg.navigation != nil {
		m.applyNavigationLanding(msg)
	}
	m.applyNavRestore(msg.restore)
	if p := msg.position; p != nil && m.EditorState.Editor != nil && m.EditorState.ActiveView == msg.files[len(msg.files)-1].path {
		ed := m.EditorState.Editor
		valid := true
		if f := msg.diagnosticFacts; f != nil && f.FromProjectCheck() {
			// The open document replaces the project view of this file.
			// The location came from the same disk text unless the buffer
			// already had unsaved changes.
			valid = f.Boundary.Source.Path == ed.FilePath() && !ed.Modified()
		} else if f != nil {
			current, ok := m.ws.LSPMgr().FileDiagnostics(ed.FilePath())
			valid = ok && current.ID == f.ID && ed.MatchesDiagnosticSource(current) && ed.MatchesDiagnosticSource(*f)
		}
		if !valid {
			m.Feedback.NotifyInfo("Diagnostic source changed; location not applied")
		} else if err := ed.SetLSPDiagnosticPosition(*p); err != nil {
			m.Feedback.NotifyError(err.Error())
		}
		ed.ScrollToCursor()
	}
	if tab := m.Win.ActiveTab(); msg.window && tab != nil && (m.isEditorTabTitle(tab.Title) || isImageTab(tab)) {
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
func (m *App) queueLiveNavigation(path string, position editor.LSPDiagnostic) bool {
	if _, ok := m.ws.(shellport.LiveDocuments); !ok {
		return false
	}
	cmd := m.openPaneFiles([]string{path}, m.Win.ActiveGroup())
	if cmd != nil {
		m.editorCommands = append(m.editorCommands, func() tea.Msg {
			msg := cmd().(paneFilesOpenedMsg)
			msg.position = &position
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
