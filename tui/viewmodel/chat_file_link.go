package viewmodel

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/core/port"
	"crude/internal/filelink"
	"crude/tui/chatprojection"
	"crude/tui/editor"
	"crude/tui/window"
	"crude/workspace"
	"crude/workspace/fileio"
)

// Chat file references: an exact match opens the file; anything else opens
// Go to File seeded with the reference, where the user chooses. A chat link
// never creates a document, even for a moment.

type chatFileResolvedMsg struct {
	query                    string
	seq, generation, paneSeq uint64
	group                    window.GroupID
	tab                      window.TabID
	catalog                  *workspace.BrowserCatalog
	executionID              string
	err                      error
	incomplete               bool
	owner                    shellport.Workspace
	ref                      filelink.Reference
	open                     string
}

func (m *App) openChatFile(target string) tea.Cmd {
	ref, ok := filelink.Parse(target)
	if !ok || m.ws == nil || m.Win == nil {
		return nil
	}
	msg := m.newChatLookup(ref)
	return m.tryChatFile(msg, ref.Path)
}

func (m *App) newChatLookup(ref filelink.Reference) chatFileResolvedMsg {
	m.chatFileSeq++
	msg := chatFileResolvedMsg{seq: m.chatFileSeq, paneSeq: m.paneFileOpenSeq, generation: m.workspaceSwitchGen.Load(), owner: m.ws, ref: ref, group: m.Win.ActiveGroup()}
	if t := m.Win.ActiveTab(); t != nil {
		msg.tab = t.ID
	}
	return msg
}

// A scoped reference never consults the current workspace or next-turn selection.
func (m *App) openExecutionChatFile(link chatprojection.FileReference) tea.Cmd {
	ref, ok := filelink.Parse(link.Target)
	if !ok || m.ws == nil || m.Win == nil {
		return nil
	}
	port, ok := m.ws.(interface {
		CatalogExecutionFiles(context.Context, string, bool) (workspace.BrowserCatalog, error)
	})
	if !ok {
		m.Feedback.NotifyError("The checkout for this link is not available.")
		return nil
	}
	msg := m.newChatLookup(ref)
	msg.executionID = link.ExecutionID
	return m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
		catalog, err := port.CatalogExecutionFiles(ctx, link.ExecutionID, false)
		msg.catalog, msg.err = &catalog, err
		return executionChatCatalogMsg{msg}
	})
}

type executionChatCatalogMsg struct{ lookup chatFileResolvedMsg }

func (m *App) applyExecutionChatCatalog(msg chatFileResolvedMsg) tea.Cmd {
	if !m.chatLookupCurrent(msg) {
		return nil
	}
	if msg.err != nil || msg.catalog == nil || msg.catalog.Root == "" {
		m.Feedback.NotifyError("The checkout for this link is not available.")
		return nil
	}
	target := strings.ReplaceAll(msg.ref.Path, `\`, "/")
	absolute := strings.HasPrefix(target, "/") || len(target) >= 3 && target[1:3] == ":/"
	if !absolute {
		target = path.Join(strings.ReplaceAll(msg.catalog.Root, `\`, "/"), target)
	}
	return m.tryChatFile(msg, target)
}

func (m *App) tryChatFile(msg chatFileResolvedMsg, target string) tea.Cmd {
	root := m.ws.RootPath()
	files, remote := m.ws.(shellport.ClientFiles)
	command := m.openSourceLocationChecked(target, msg.ref.Line, msg.ref.Column, true)
	if command == nil {
		return nil
	}
	msg.paneSeq = m.paneFileOpenSeq
	return m.clientIOCommand(context.Background(), func(ctx context.Context) tea.Msg {
		opened := command().(paneFilesOpenedMsg)
		if len(opened.files) != 1 || !errors.Is(opened.files[0].err, os.ErrNotExist) {
			return opened
		}
		if msg.catalog == nil {
			var catalog workspace.BrowserCatalog
			if remote {
				catalog, msg.err = files.CatalogFiles(ctx, false)
			} else {
				catalog, msg.err = workspace.ScanBrowserPaths(ctx, workspace.BrowserPathRequest{Root: root})
			}
			msg.catalog = &catalog
		}
		msg.incomplete = msg.err != nil || msg.catalog.Truncated || msg.catalog.Errors > 0
		if !msg.incomplete {
			msg.open, msg.query = uniqueChatFile(*msg.catalog, msg.ref.Path)
		}
		return msg
	})
}

// chatFilePath resolves a reference against the selected core's root.
func (m *App) chatFilePath(reference string) string {
	if files, ok := m.ws.(shellport.ClientFiles); ok {
		return files.CanonicalClientPath(reference)
	}
	if !filepath.IsAbs(reference) {
		return filepath.Join(m.ws.RootPath(), filepath.FromSlash(reference))
	}
	return filepath.Clean(reference)
}

// chatFileQuery is the reference as a workspace-relative search query.
func chatFileQuery(reference string) string {
	return strings.TrimPrefix(path.Clean(strings.ReplaceAll(reference, `\`, "/")), "./")
}

// Only one component-exact suffix can land without confirmation. If a stale
// directory prefix does not match, offer the basename rather than opening a
// different directory just because it happens to have one such file.
func uniqueChatFile(c workspace.BrowserCatalog, ref string) (string, string) {
	q := chatFileQuery(ref)
	var exact string
	count, names := 0, 0
	for _, e := range c.Entries {
		if e.IsDir {
			continue
		}
		switch fileio.FilePathMatchClass(strings.ReplaceAll(e.Relative, `\`, "/"), q) {
		case 0:
			exact = e.Path
			count++
		case 1:
			names++
		}
	}
	if count == 1 {
		return exact, q
	}
	if count == 0 && names > 0 {
		return "", path.Base(q)
	}
	return "", q
}

func (m *App) chatLookupCurrent(msg chatFileResolvedMsg) bool {
	if msg.seq != m.chatFileSeq || msg.generation != m.workspaceSwitchGen.Load() || msg.owner != m.ws || m.Win == nil || msg.group != m.Win.ActiveGroup() || msg.paneSeq != m.paneFileOpenSeq {
		return false
	}
	if t := m.Win.ActiveTab(); t != nil && t.ID != msg.tab || t == nil && msg.tab != 0 {
		return false
	}
	return true
}

func (m *App) applyChatFileResolved(msg chatFileResolvedMsg) tea.Cmd {
	if !m.chatLookupCurrent(msg) {
		return nil
	}
	if msg.open != "" {
		return m.openSourceLocationChecked(msg.open, msg.ref.Line, msg.ref.Column, true)
	}
	if msg.query == "" {
		msg.query = chatFileQuery(msg.ref.Path)
	}
	cmd := m.openGoToFile(msg.query, msg.ref.Line, msg.ref.Column)
	m.goToFileLanding.catalog, m.goToFileLanding.external = msg.catalog, msg.executionID != ""
	if msg.incomplete {
		m.Feedback.NotifyInfo("Crude could not check every file. Choose a match or refine the search.")
	}
	return cmd
}

// openSourceLocation opens a file at a one-based line and column (zero means
// none) in the pane that asked, through the shared pane-file owner.
func (m *App) openSourceLocation(path string, line, column int) tea.Cmd {
	return m.openSourceLocationChecked(path, line, column, false)
}

// openSourceLocationChecked with existing set opens only an open named buffer
// or an existing file; a missing file reports a problem and creates nothing.
func (m *App) openSourceLocationChecked(path string, line, column int, existing bool) tea.Cmd {
	if m.ws == nil || m.Win == nil || path == "" {
		return nil
	}
	path = m.chatFilePath(path)
	command := m.openPaneFilesChecked(context.Background(), []string{path}, m.Win.ActiveGroup(), existing)
	if command == nil {
		return nil
	}
	return func() tea.Msg {
		msg := command().(paneFilesOpenedMsg)
		if line > 0 {
			msg.filePosition = &editor.Position{Line: line - 1, Col: max(0, column-1)}
		}
		return msg
	}
}
