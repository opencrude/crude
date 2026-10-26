package viewmodel

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"crude/fileio"
	"crude/shellport"

	tea "charm.land/bubbletea/v2"
)

// clientDesktopOpener captures the owning port before the command runs. Foreign
// paths materialize private bounded previews retained until shell shutdown;
// authenticated local-host attachments can reveal the original path.
func (m *App) clientDesktopOpener(directory bool) func(string, bool) error {
	files, remote := m.ws.(shellport.ClientFiles)
	if host, ok := m.ws.(shellport.LocalHostAuthority); ok && host.LocalHost() {
		remote = false
	}
	generation := m.workspaceSwitchGen.Load()
	state := asyncIOStateFor(m)
	state.mu.Lock()
	epoch := state.clientIOEpoch
	state.mu.Unlock()
	return func(path string, reveal bool) error {
		parent, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ctx, release, err := m.beginClientIO(state, parent, generation, epoch)
		if err != nil {
			return err
		}
		defer release()
		if !remote {
			return fileio.OpenDesktopPathContext(ctx, path, reveal)
		}
		cache, err := os.MkdirTemp("", "crude-desktop-")
		if err != nil {
			return err
		}
		state.mu.Lock()
		if m.asyncIOStopped.Load() {
			state.mu.Unlock()
			_ = os.RemoveAll(cache)
			return context.Canceled
		}
		state.desktopCaches = append(state.desktopCaches, cache)
		state.mu.Unlock()
		keep := false
		defer func() {
			if !keep {
				_ = os.RemoveAll(cache)
			}
		}()
		local, err := cacheDesktopTarget(ctx, files, path, cache, directory)
		if err != nil {
			return err
		}
		if generation != m.workspaceSwitchGen.Load() || m.asyncIOStopped.Load() {
			return context.Canceled
		}
		err = fileio.OpenDesktopPathContext(ctx, local, reveal)
		keep = err == nil
		return err
	}
}

// desktopTargetName treats both host path dialects as data and preserves the
// extension for OS file associations, without creating special Windows names.
func desktopTargetName(source string) string {
	name := path.Base(strings.ReplaceAll(source, "\\", "/"))
	name = strings.Map(func(r rune) rune {
		if r < ' ' || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.TrimRight(name, " .")
	if name == "" || name == "." || name == ".." {
		name = "preview"
	}
	// A fixed prefix also keeps Windows device names (CON, NUL, COM1…) ordinary.
	return "preview-" + name
}

// A remote directory is an explicit local listing, never a pretend project clone.
func cacheDesktopTarget(ctx context.Context, files shellport.ClientFiles, source, cache string, directory bool) (string, error) {
	local := filepath.Join(cache, desktopTargetName(source))
	if directory {
		entries, err := files.BrowseExternalDirectory(ctx, source)
		if err != nil {
			return "", err
		}
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := os.Mkdir(local, 0o700); err != nil {
			return "", err
		}
		var listing strings.Builder
		fmt.Fprintf(&listing, "Client-local preview of core folder:\n%s\n\nNo project folder was copied. Open individual files in Crude to download them.\n\n", source)
		for _, entry := range entries {
			if listing.Len() >= 64<<10 {
				listing.WriteString("\nListing clipped. Browse the complete folder in Crude.\n")
				break
			}
			fmt.Fprintf(&listing, "%s\n", entry.Name)
		}
		return local, os.WriteFile(filepath.Join(local, "REMOTE-FOLDER.txt"), []byte(listing.String()), 0o600)
	}
	staged, err := shellport.CacheClientFile(ctx, files, source, cache, true)
	if err != nil {
		return "", err
	}
	return local, os.Rename(staged, local)
}

type desktopPathOpenedMsg struct {
	owner      shellport.Workspace
	generation uint64
	err        error
	notice     string
}

func desktopRevealLabel() string {
	switch runtime.GOOS {
	case "windows":
		return "Reveal in Explorer"
	case "darwin":
		return "Reveal in Finder"
	default:
		return "Show in file manager"
	}
}

// revealTreePath enters the existing desktop owner off Update. It is workspace-
// scoped, not agent-scoped: Explorer works with no open agent session.
func (m *App) revealTreePath(path string, directory bool) tea.Cmd {
	owner, generation := m.ws, m.workspaceSwitchGen.Load()
	open := m.clientDesktopOpener(directory)
	remote := false
	if _, connected := owner.(shellport.ClientFiles); connected {
		host, local := owner.(shellport.LocalHostAuthority)
		remote = !local || !host.LocalHost()
	}
	return func() tea.Msg {
		err := open(path, true)
		notice := ""
		if remote && err == nil {
			notice = "Showing a client-local preview, not the original core path"
		}
		return desktopPathOpenedMsg{owner: owner, generation: generation, err: err, notice: notice}
	}
}

func (m *App) applyDesktopPathOpened(msg desktopPathOpenedMsg) {
	if msg.owner != m.ws || msg.generation != m.workspaceSwitchGen.Load() || m.asyncIOStopped.Load() || m.Feedback == nil {
		return
	}
	if msg.err != nil && !errors.Is(msg.err, context.Canceled) {
		m.Feedback.NotifyError("Could not reveal path: " + msg.err.Error())
	} else if msg.notice != "" {
		m.Feedback.NotifyInfo(msg.notice)
	}
}
