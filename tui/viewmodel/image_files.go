package viewmodel

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"crude/core/port"
	"crude/core/protocol"
	"crude/tui/imagepreview"
	"crude/tui/screens/imageviewer"
	"crude/tui/ui"
	"crude/tui/window"
	"crude/workspace/fileio"
)

func imageTabTitle(path string) string { return "image:" + path }

func isImageTab(tab *window.Tab) bool {
	return tab != nil && tab.Resource.Kind == window.ResourceDocument &&
		strings.HasPrefix(tab.Title, "image:")
}

func (m *App) imageView(id window.TabID) *imageviewer.Screen {
	if m.mainPanes == nil {
		return nil
	}
	if view := m.mainPanes.views[id]; view != nil {
		return view.image
	}
	return nil
}

func (m *App) activeImageView() *imageviewer.Screen {
	if m.Win == nil || !isImageTab(m.Win.ActiveTab()) {
		return nil
	}
	return m.imageView(m.Win.ActiveTab().ID)
}

// preparePaneImage snapshots bytes but never decodes a dormant file. Connected
// workspaces read from the core; a denied project read is never retried with
// external authority or against a similarly named client-local file.
func preparePaneImage(ctx context.Context, files shellport.ClientFiles, path string, external bool, store *paneImageStore, lane chan struct{}) (*paneImageSnapshot, error) {
	if err := claimPaneImageWork(ctx, lane); err != nil {
		return nil, err
	}
	defer func() { <-lane }()
	data, err := readPaneImage(ctx, files, path, external)
	if err != nil {
		return nil, err
	}
	if err := imagepreview.CheckHeader(ctx, data); err != nil {
		return nil, err
	}
	return store.save(ctx, data)
}

func readPaneImage(ctx context.Context, files shellport.ClientFiles, path string, external bool) ([]byte, error) {
	var data []byte
	var err error
	if files != nil {
		if external {
			data, err = files.ReadExternalClientFile(ctx, path)
		} else {
			data, err = files.ReadClientFile(ctx, path)
		}
	} else {
		var before os.FileInfo
		err = fileio.CheckDirectoryNoSymlinks(filepath.Dir(path))
		if err == nil {
			before, err = os.Lstat(path)
		}
		if err == nil && (!before.Mode().IsRegular() || before.Size() > coreprotocol.MaxFileRead) {
			err = fmt.Errorf("the file is not a regular image or is larger than 64 MiB")
		}
		if err == nil {
			var f *os.File
			f, err = fileio.OpenReadSharedDelete(path)
			if err == nil {
				var opened os.FileInfo
				opened, err = f.Stat()
				if err == nil && !os.SameFile(before, opened) {
					err = fmt.Errorf("the file changed while it was opened")
				}
				if err == nil {
					data, err = io.ReadAll(io.LimitReader(f, coreprotocol.MaxFileRead+1))
				}
				if err == nil {
					var after, current os.FileInfo
					after, err = f.Stat()
					if err == nil {
						current, err = os.Lstat(path)
					}
					if err == nil {
						err = fileio.CheckDirectoryNoSymlinks(filepath.Dir(path))
					}
					if err == nil && (!os.SameFile(before, current) || before.Size() != after.Size() ||
						!before.ModTime().Equal(after.ModTime())) {
						err = fmt.Errorf("the file changed while it was opened")
					}
				}
				if closeErr := f.Close(); err == nil {
					err = closeErr
				}
			}
		}
	}
	return data, err
}

// clientPathWithinRoot compares core-canonical paths without applying this
// shell's operating-system path rules to a foreign core's paths.
func clientPathWithinRoot(root, candidate string) bool {
	root = path.Clean(strings.ReplaceAll(root, "\\", "/"))
	candidate = path.Clean(strings.ReplaceAll(candidate, "\\", "/"))
	if len(root) >= 2 && root[1] == ':' {
		root, candidate = strings.ToLower(root), strings.ToLower(candidate)
	}
	return candidate == root || strings.HasPrefix(candidate, strings.TrimRight(root, "/")+"/")
}

// showPaneImage claims the exact snapshot and keeps an independent viewport
// for this tab. The residency owner decodes it only when it is selected.
func (m *App) showPaneImage(path string, group window.GroupID, activate bool, snapshot *paneImageSnapshot) bool {
	defer func() {
		if snapshot.take() {
			m.discardPaneImageSnapshot(snapshot)
		}
	}()
	if m.Win == nil || !m.Win.HasGroup(group) {
		return false
	}
	if tab := m.Win.GroupTabByTitle(group, imageTabTitle(path)); tab != nil {
		if activate {
			m.Win.SwitchTo(m.Win.IndexOf(tab.ID))
			m.unfocus()
			m.handleTabSwitch()
		}
		return true
	}
	if snapshot == nil || snapshot.store != m.imageResidency.store || !snapshot.take() {
		return false
	}
	tab := m.Win.AddTab(group, window.Resource{Kind: window.ResourceDocument, ID: path}, imageTabTitle(path), activate)
	if tab == nil {
		m.discardPaneImageSnapshot(snapshot)
		return false
	}
	tab.Icon, tab.DisplayTitle = "▧", filepath.Base(path)
	p := m.ensureMainPanes()
	screen := imageviewer.NewWithID(m.Theme, ui.ComponentID(fmt.Sprintf("main-panes/image-%d", tab.ID)))
	screen.SetImageResidency(nil, nil, paneImageLoading)
	m.configureImageGraphics(screen)
	p.views[tab.ID] = &mainPaneView{image: screen, imageState: paneImageViewState{tab: tab.ID, snapshot: snapshot}}
	m.imageResidency.openTabs++
	if activate {
		m.unfocus()
		m.handleTabSwitch()
	}
	return true
}

func (m *App) closeImageTab(id window.TabID) {
	m.releaseImageTab(id)
	m.Win.CloseTabByID(id)
	m.handleTabSwitch()
	if m.Win.TabCount() == 0 && m.AgentView.VisibleCount() == 0 {
		m.openLauncher()
	}
	m.forceFrame()
}

func (m *App) configureImageGraphics(screen *imageviewer.Screen) bool {
	state := m.terminalImages
	return screen.SetImageGraphics(state.protocol(), state.cellWidth, state.cellHeight)
}

// Retarget only paths moved by an explicit successful tree operation. Open
// snapshots keep their bytes; reopening the new path reads the new file.
func (m *App) retargetImageTabs(oldPath, newPath string, directory bool) {
	if m.Win == nil {
		return
	}
	for _, tab := range m.Win.Tabs() {
		if !isImageTab(&tab) {
			continue
		}
		target := ""
		if tab.Resource.ID == oldPath {
			target = newPath
		} else if directory {
			rel, err := filepath.Rel(oldPath, tab.Resource.ID)
			if err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				target = filepath.Join(newPath, rel)
			}
		}
		if target != "" {
			index := m.Win.IndexOf(tab.ID)
			m.Win.SetTabTitleByIndex(index, imageTabTitle(target))
			m.Win.SetTabDisplayTitleByIndex(index, filepath.Base(target))
			m.Win.TabByID(tab.ID).Resource.ID = target
		}
	}
}

func (m *App) closeImageTabsUnder(path string) {
	if m.Win == nil {
		return
	}
	for _, tab := range m.Win.Tabs() {
		if !isImageTab(&tab) {
			continue
		}
		rel, err := filepath.Rel(path, tab.Resource.ID)
		if tab.Resource.ID == path || err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			m.closeImageTab(tab.ID)
		}
	}
}
