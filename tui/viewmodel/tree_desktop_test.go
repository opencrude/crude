package viewmodel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/shellport"
	"crude/tui/ui"

	tea "charm.land/bubbletea/v2"
)

func treeRevealLabel() string {
	switch runtime.GOOS {
	case "windows":
		return "Reveal in Explorer"
	case "darwin":
		return "Reveal in Finder"
	default:
		return "Show in file manager"
	}
}

func TestFileTreeContextMenuOffersDesktopReveal(t *testing.T) {
	for _, size := range [][2]int{{160, 40}, {80, 24}} {
		root := t.TempDir()
		file := filepath.Join(root, "reveal-me.txt")
		if err := os.WriteFile(file, []byte("exact tree target"), 0o600); err != nil {
			t.Fatal(err)
		}
		app := newEditorPaintApp(t, root)
		app.width, app.height = size[0], size[1]
		app.setActivity(activityFiles, true)
		x, y := editorTreePoint(t, app, "reveal-me.txt")
		app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
		if app.LastContextMenu == nil {
			t.Fatal("tree right-click did not open its menu")
		}
		item := menuItem(t, app.LastContextMenu, treeRevealLabel())
		if item.Disabled || item.Action == nil {
			t.Fatal("the clicked file cannot be revealed in the desktop file manager")
		}
	}
}

// The actual desktop owner is exercised through a private fake OS launcher,
// never a production seam or the user's file manager.
func recordDesktopLauncher(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("native Windows Explorer selection requires the Windows desktop journey")
	}
	dir := t.TempDir()
	output := filepath.Join(dir, "arguments")
	for _, name := range []string{"xdg-open", "open"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CRUDE_TEST_DESKTOP_ARGS\"\n"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("CRUDE_TEST_DESKTOP_ARGS", output)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return output
}

func TestFileTreeRevealTargetsClickedPathOffUpdate(t *testing.T) {
	output := recordDesktopLauncher(t)
	for _, directory := range []bool{false, true} {
		root := t.TempDir()
		path := filepath.Join(root, "a&b.txt")
		if directory {
			if err := os.Mkdir(path, 0o700); err != nil {
				t.Fatal(err)
			}
		} else if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
		app := newEditorPaintApp(t, root)
		app.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
		app.registerOverlayHandlers()
		app.setActivity(activityFiles, true)
		app.ActiveSession = nil // A tree operation does not need a model session.
		x, y := editorTreePoint(t, app, "a&b.txt")
		app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
		messages := make(chan tea.Msg, 1)
		restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { messages <- msg })
		editorChooseTreeMenu(t, app, treeRevealLabel())
		if app.LastContextMenu != nil {
			fw := app.overlayHost.Window(ui.OverlayContextMenu)
			cw := fw.Content.(*ui.ComponentWidget)
			list := ui.ContextMenuList(cw)
			r, _ := cw.Host().ScreenRect(ui.ComponentID(ui.ContextMenuListID))
			pointX, pointY := fw.Position()
			t.Fatalf("desktop action not activated dir=%v selected=%q window=%d,%d list=%+v index=%d offset=%d hit=%q focused=%v", directory, paneMenuLabel(app.LastContextMenu), pointX, pointY, r, list.SelectedIndex(), list.Scroll().Offset(), cw.Host().HitTest(r.X+1, r.Y+list.SelectedIndex()-list.Scroll().Offset()), app.windowManager.Focused().ID)
		}
		select {
		case msg := <-messages:
			result, ok := msg.(desktopPathOpenedMsg)
			if !ok || result.err != nil {
				t.Fatalf("desktop reveal failed: %#v", msg)
			}
			app.Update(msg)
		case <-time.After(3 * time.Second):
			t.Fatal("context menu did not complete desktop reveal")
		}
		restore()
		args, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		expect := path
		if !directory {
			if runtime.GOOS == "darwin" {
				expect = "-R\n" + path
			} else {
				expect = root
			}
		}
		if string(args) != expect+"\n" {
			t.Fatalf("revealed %q, want %q", args, expect)
		}
	}
}

type desktopFilesPort struct {
	fakeClientFiles
	local bool
	read  func(context.Context, string) ([]byte, error)
}

func (p *desktopFilesPort) LocalHost() bool { return p.local }
func (p *desktopFilesPort) ReadExternalClientFile(ctx context.Context, path string) ([]byte, error) {
	return p.read(ctx, path)
}

func TestConnectedRevealNeverPassesForeignPathToDesktop(t *testing.T) {
	output := recordDesktopLauncher(t)
	for _, source := range []string{"/foreign/project/foreign-only.txt", `C:\foreign\project\foreign-only.txt`} {
		app := newFullTestApp(t)
		port := &desktopFilesPort{fakeClientFiles: fakeClientFiles{Workspace: app.ws}}
		reads := 0
		port.read = func(_ context.Context, requested string) ([]byte, error) {
			reads++
			if requested != source {
				t.Errorf("downloaded %q, want exact %q", requested, source)
			}
			return []byte("core-only content"), nil
		}
		app.ws = port
		msg := app.revealTreePath(source, false)().(desktopPathOpenedMsg)
		if msg.err != nil || msg.notice == "" || reads != 1 {
			t.Fatalf("remote reveal: %#v; reads=%d", msg, reads)
		}
		args, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		local := strings.TrimSpace(string(args))
		if runtime.GOOS == "darwin" {
			local = strings.TrimPrefix(local, "-R\n")
		} else {
			local = filepath.Join(local, "preview-foreign-only.txt")
		}
		data, err := os.ReadFile(local)
		if err != nil || string(data) != "core-only content" || strings.Contains(string(args), source) {
			t.Fatalf("desktop did not receive a client-local copy: args=%q content=%q err=%v", args, data, err)
		}
		app.StopAsyncIO()
		if _, err := os.Stat(local); !os.IsNotExist(err) {
			t.Fatalf("shutdown retained desktop cache: %v", err)
		}
	}
}

func TestConnectedLocalRevealUsesOriginalPath(t *testing.T) {
	output := recordDesktopLauncher(t)
	app := newFullTestApp(t)
	path := filepath.Join(t.TempDir(), "original.txt")
	if err := os.WriteFile(path, []byte("source"), 0o600); err != nil {
		t.Fatal(err)
	}
	port := &desktopFilesPort{fakeClientFiles: fakeClientFiles{Workspace: app.ws}, local: true}
	port.read = func(context.Context, string) ([]byte, error) {
		t.Error("local reveal downloaded a copy")
		return nil, errors.New("unexpected")
	}
	app.ws = port
	if msg := app.revealTreePath(path, false)().(desktopPathOpenedMsg); msg.err != nil || msg.notice != "" {
		t.Fatalf("local reveal failed: %#v", msg)
	}
	args, _ := os.ReadFile(output)
	expect := filepath.Dir(path)
	if runtime.GOOS == "darwin" {
		expect = "-R\n" + path
	}
	if string(args) != expect+"\n" {
		t.Fatalf("local core path changed: %q", args)
	}
}

func TestRemoteFolderRevealIsExplicitLocalPreview(t *testing.T) {
	output := recordDesktopLauncher(t)
	app := newFullTestApp(t)
	port := &desktopFilesPort{fakeClientFiles: fakeClientFiles{Workspace: app.ws, entries: []shellport.ClientDirectoryEntry{{Name: "file.txt"}, {Name: "subdir", IsDir: true}}}}
	port.read = func(context.Context, string) ([]byte, error) {
		t.Error("folder reveal tried to read a file")
		return nil, nil
	}
	app.ws = port
	if msg := app.revealTreePath("/core-only/folder", true)().(desktopPathOpenedMsg); msg.err != nil || msg.notice == "" {
		t.Fatalf("folder preview failed: %#v", msg)
	}
	args, _ := os.ReadFile(output)
	preview := strings.TrimSpace(string(args))
	note, err := os.ReadFile(filepath.Join(preview, "REMOTE-FOLDER.txt"))
	if err != nil || !strings.Contains(string(note), "/core-only/folder") || !strings.Contains(string(note), "No project folder was copied") {
		t.Fatalf("remote folder misrepresented: %q %v", note, err)
	}
}

func TestDesktopRevealCancellationAndStaleOwnership(t *testing.T) {
	for _, stop := range []string{"switch", "shutdown"} {
		t.Run(stop, func(t *testing.T) {
			app := newFullTestApp(t)
			started := make(chan struct{})
			port := &desktopFilesPort{fakeClientFiles: fakeClientFiles{Workspace: app.ws}}
			port.read = func(ctx context.Context, _ string) ([]byte, error) {
				close(started)
				<-ctx.Done()
				return nil, ctx.Err()
			}
			app.ws = port
			cmd := app.revealTreePath("/foreign/blocked.txt", false)
			done := make(chan desktopPathOpenedMsg, 1)
			go func() { done <- cmd().(desktopPathOpenedMsg) }()
			<-started
			if stop == "shutdown" {
				app.StopAsyncIO()
			} else {
				app.beginWorkspaceSwitch()
			}
			select {
			case msg := <-done:
				if !errors.Is(msg.err, context.Canceled) {
					t.Fatalf("desktop request did not cancel: %v", msg.err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("desktop request survived its owner")
			}
		})
	}
	app := newFullTestApp(t)
	cmd := app.revealTreePath("/must-not-open", false)
	app.workspaceSwitchGen.Add(1)
	if msg := cmd().(desktopPathOpenedMsg); !errors.Is(msg.err, context.Canceled) {
		t.Fatalf("stale reveal started: %v", msg.err)
	}
}

func TestFileTreeRevealMenuRejectsLaterWorkspace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "stale.txt"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	app := newEditorPaintApp(t, root)
	app.setActivity(activityFiles, true)
	x, y := editorTreePoint(t, app, "stale.txt")
	menu := app.buildSidebarContextMenu(tea.Mouse{X: x, Y: y})
	app.workspaceSwitchGen.Add(1)
	activateMenuItem(t, menu, treeRevealLabel())
	state := asyncIOStateFor(app)
	state.mu.Lock()
	defer state.mu.Unlock()
	if state.clientIONext != 0 {
		t.Fatal("stale file-tree action started a desktop job")
	}
}

// A genuine connected core exercises the port provenance rather than inferring
// locality from whether the path happens to exist on the shell host.
func TestConnectedCoreTreeRevealUsesHostAuthority(t *testing.T) {
	output := recordDesktopLauncher(t)
	root := t.TempDir()
	path := filepath.Join(root, "connected.txt")
	if err := os.WriteFile(path, []byte("core file"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := remoteJourneyRuntime(t, "http://unused.invalid")
	shell := newRemoteJourneyShell(t, r, root)
	app := shell.app
	app.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	runAppCommand(t, app, app.beginClientFileTreeLoad())
	app.setActivity(activityFiles, true)
	x, y := editorTreePoint(t, app, "connected.txt")
	app.Update(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseRight})
	result := make(chan tea.Msg, 1)
	restore := installDetachedMessageSinkForTest(app, func(msg tea.Msg) { result <- msg })
	defer restore()
	editorChooseTreeMenu(t, app, treeRevealLabel())
	select {
	case msg := <-result:
		if opened, ok := msg.(desktopPathOpenedMsg); !ok || opened.err != nil {
			t.Fatalf("connected tree reveal failed: %#v", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("connected tree reveal did not finish")
	}
	args, _ := os.ReadFile(output)
	expect := root
	if runtime.GOOS == "darwin" {
		expect = "-R\n" + path
	}
	if string(args) != expect+"\n" {
		t.Fatalf("real local core reveal got %q, want %q", args, expect)
	}
}

// Keep these import-time contracts explicit when fixtures are changed.
var _ shellport.ClientFiles = (*desktopFilesPort)(nil)
var _ shellport.LocalHostAuthority = (*desktopFilesPort)(nil)
