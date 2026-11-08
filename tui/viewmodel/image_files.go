package viewmodel

import (
	"context"
	"fmt"
	"image"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"crude/coreprotocol"
	"crude/fileio"
	"crude/shellport"
	"crude/tui/imagepreview"
	"crude/tui/screens/imageviewer"
	"crude/tui/ui"
	"crude/tui/window"
)

// A file image is a read-only snapshot, not a text document. Each tab owns
// its own terminal resource: Kitty virtual placement geometry is per image ID.
type paneImage struct {
	image    image.Image
	resource *tea.KittyImage
}

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

// preparePaneImage runs on the same worker as other pane-file opens. Connected
// workspaces read from the core; a denied project read is never retried with
// external authority or against a similarly named client-local file.
func preparePaneImage(ctx context.Context, files shellport.ClientFiles, path string, external bool) (*paneImage, error) {
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
	if err != nil {
		return nil, err
	}
	img, resource, err := imagepreview.Decode(ctx, data)
	if err != nil {
		return nil, err
	}
	return &paneImage{image: img, resource: resource}, nil
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

// showPaneImage installs the already prepared immutable image in the requested
// pane. Its viewport and terminal identity belong to this tab, not the path.
func (m *App) showPaneImage(path string, group window.GroupID, activate bool, prepared *paneImage) bool {
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
	if prepared == nil || prepared.image == nil {
		return false
	}
	tab := m.Win.AddTab(group, window.Resource{Kind: window.ResourceDocument, ID: path}, imageTabTitle(path), activate)
	if tab == nil {
		return false
	}
	tab.Icon, tab.DisplayTitle = "▧", filepath.Base(path)
	p := m.ensureMainPanes()
	screen := imageviewer.NewWithID(m.Theme, ui.ComponentID(fmt.Sprintf("main-panes/image-%d", tab.ID)))
	screen.SetImage(prepared.image, prepared.resource)
	m.configureImageGraphics(screen)
	p.views[tab.ID] = &mainPaneView{image: screen}
	if activate {
		m.unfocus()
		m.handleTabSwitch()
	}
	return true
}

func (m *App) closeImageTab(id window.TabID) {
	if m.mainPanes != nil {
		delete(m.mainPanes.views, id)
	}
	m.Win.CloseTabByID(id)
	m.handleTabSwitch()
	if m.Win.TabCount() == 0 && m.AgentView.VisibleCount() == 0 {
		m.openLauncher()
	}
	m.forceFrame()
}

func (m *App) configureImageGraphics(screen *imageviewer.Screen) bool {
	state := m.terminalImages
	return screen.SetImageGraphics(state.supported && (!state.profileKnown || state.trueColor) &&
		state.cellWidth > 0 && state.cellHeight > 0, state.cellWidth, state.cellHeight)
}

// Retarget only paths moved by an explicit successful tree operation. Open
// snapshots keep their pixels; reopening the new path reads the new file.
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
