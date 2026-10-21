package viewmodel

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/clientstate"
	"crude/shellport"
	"crude/tui/ui"
	"crude/workspace"
)

func switchTestFile(t *testing.T, root, name, text string) string {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.WriteFile(path, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func switchTestKey(t *testing.T, app *App, code rune) {
	t.Helper()
	_, cmd := app.Update(tea.KeyPressMsg{Code: code, Text: string(code)})
	runAppCommand(t, app, cmd)
}

// Existing lifecycle tests begin with real navigation consent before exercising
// asynchronous preparation, dirty decisions, cancellation and recovery.
func confirmWorkspaceSwitchTest(t *testing.T, app *App, root string) tea.Cmd {
	t.Helper()
	if cmd := app.launcherOpenFolder(root); cmd != nil {
		t.Fatal("navigation started work before consent")
	}
	if app.workspaceOpenPrompt == nil {
		t.Fatal("missing switch confirmation")
	}
	app.handleWorkspaceOpenPromptInput(tea.KeyPressMsg{Code: tea.KeyLeft})
	_, cmd := app.handleWorkspaceOpenPromptInput(tea.KeyPressMsg{Code: tea.KeyEnter})
	return cmd
}

func TestConnectedWorkspaceSwitchRailDirtyDecisionsRetainCoreAndComposer(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root, target := t.TempDir(), t.TempDir()
	one := newRemoteJourneyShell(t, rt, root)
	id := remoteJourneyCreate(t, one)
	two := newRemoteJourneyShell(t, rt, root)
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool { return two.app.ws.AgentMgr().Get(id) != nil })
	app := one.app
	first := switchTestFile(t, root, "a.txt", "first")
	second := switchTestFile(t, root, "b.txt", "second")
	a, b := liveTestOpen(t, app, first), liveTestOpen(t, app, second)
	peer := liveTestOpen(t, two.app, second)
	a.InsertRune('A')
	b.InsertRune('B')
	// Both edits are still private/pending when the rail entry is activated.
	store, err := clientstate.Open(filepath.Join(t.TempDir(), "switch-drafts.db"))
	if err != nil {
		t.Fatal(err)
	}
	attachment, err := store.Acquire(t.Context(), "switch-shell", clientstate.AcquireOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if err := app.ConfigureComposerDrafts(store, attachment, app.ws.(shellport.Connection).WorkspaceID()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { app.StopAsyncIO(); app.composerDraftClient = nil; _ = store.Close() })
	app.composerHydratedFor = nil
	runAppCommand(t, app, app.hydrateComposerDraftCmd())
	app.Input.SetInput("private composer survives switch and return")
	app.Input.SetCursorFlat(7)
	app.Input.SetSelection(1, 5)

	service, err := app.ws.EnsureTerminals()
	if err != nil {
		t.Fatal(err)
	}
	opts := workspace.TerminalCreateOpts{CWD: root, Command: "/bin/sleep", Args: []string{"300"}, Cols: 60, Rows: 12, Title: "retained switch PTY"}
	if runtime.GOOS == "windows" {
		opts.Command, opts.Args = "powershell.exe", []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", "Start-Sleep -Seconds 300"}
	}
	terminal, err := service.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { two.app.ws.Terminals().Close(terminal.ID) })
	app.setActivity(activityWorkspaces, true)
	app.workspaceInfos = []workspace.WorkspaceInfo{{ID: "target", ProjectName: "Target workspace", ProjectDir: target}}
	app.forceFrame()
	if !strings.Contains(ansi.Strip(app.View().Content), "Target workspace") {
		t.Fatal("rail catalog entry is not painted")
	}
	cmd := app.handleSidebarActivate(ui.ListItem{ID: sidebarItemWorkspacePrefix + target})
	runAppCommand(t, app, cmd)
	if app.workspaceOpenPrompt == nil || app.workspaceSwitchIntent != nil {
		t.Fatal("rail skipped initial consent")
	}
	switchTestKey(t, app, tea.KeyLeft)
	switchTestKey(t, app, tea.KeyEnter)
	runAppCommand(t, app, app.drainLiveEditorCommands())
	if app.DirtyPromptPath != first || app.DirtyPromptChoice != 2 {
		t.Fatalf("first dirty decision/default lost: %q/%d", app.DirtyPromptPath, app.DirtyPromptChoice)
	}
	paint := ansi.Strip(app.View().Content)
	if !strings.Contains(paint, "Switch workspace") || !strings.Contains(paint, "Close view") {
		t.Fatalf("switch decision is not user-visible: %s", paint)
	}
	old := app.ws
	switchTestKey(t, app, 's')
	if app.ws != old || !a.IsOpen() || app.DirtyPromptPath != second {
		t.Fatal("Save lost switch intent, closed current view, or skipped second dirty buffer")
	}
	data, err := os.ReadFile(first)
	if err != nil || string(data) != "Afirst" {
		t.Fatalf("Save did not write exact user text: %q %v", data, err)
	}
	switchTestKey(t, app, 'd')
	if app.ws == old || !sameWorkspaceRoot(app.ws.RootPath(), target) || app.workspaceSwitchIntent != nil {
		t.Fatal("Close-view did not automatically continue the original switch")
	}
	liveTestSync(t, two.app)
	if peer.Value() != "Bsecond" || !peer.Modified() || two.app.ws.AgentMgr().Get(id) == nil {
		t.Fatal("switch discarded shared unsaved text or retained agent")
	}
	data, err = os.ReadFile(second)
	if err != nil || string(data) != "second" {
		t.Fatalf("Close-view unexpectedly saved disk: %q %v", data, err)
	}
	remoteJourneyUntil(t, []remoteJourneyShell{two}, func() bool {
		info, exists := two.app.ws.TermMgr().Get(terminal.ID)
		return exists && info.Alive && info.PID == terminal.PID
	})
	runAppCommand(t, app, confirmWorkspaceSwitchTest(t, app, root))
	remoteJourneyUntil(t, []remoteJourneyShell{one, two}, func() bool {
		return app.AgentView.ActiveID() == id && app.Input.Value() == "private composer survives switch and return"
	})
	if info, exists := app.ws.TermMgr().Get(terminal.ID); !exists || !info.Alive || info.PID != terminal.PID {
		t.Fatal("return did not retain the original core PTY")
	}
	reopened := liveTestOpen(t, app, second)
	if reopened.Value() != "Bsecond" || !reopened.Modified() {
		t.Fatal("return lost accepted unsaved text")
	}
	if from, to := app.Input.InputSelectionRange(); from != 1 || to != 5 || app.Input.CursorFlat() != 7 {
		t.Fatalf("composer private view state lost: cursor=%d selection=%d:%d", app.Input.CursorFlat(), from, to)
	}
}

func TestConnectedWorkspaceSwitchCancelAndSaveFailureKeepEveryView(t *testing.T) {
	for _, failure := range []bool{false, true} {
		name := "cancel"
		if failure {
			name = "save-conflict"
		}
		t.Run(name, func(t *testing.T) {
			rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			root := t.TempDir()
			shell := newRemoteJourneyShell(t, rt, root)
			app, old := shell.app, shell.app.ws
			p := switchTestFile(t, root, "dirty.txt", "base")
			q := switchTestFile(t, root, "other.txt", "other")
			a, b := liveTestOpen(t, app, p), liveTestOpen(t, app, q)
			a.InsertRune('X')
			b.InsertRune('Y')
			app.Input.SetInput("safe composer")
			runAppCommand(t, app, confirmWorkspaceSwitchTest(t, app, t.TempDir()))
			runAppCommand(t, app, app.drainLiveEditorCommands())
			if app.DirtyPromptPath != p {
				t.Fatalf("missing first prompt: %q", app.DirtyPromptPath)
			}
			if failure {
				if err := os.WriteFile(p, []byte("external disk change"), 0600); err != nil {
					t.Fatal(err)
				}
				switchTestKey(t, app, 's')
				if state := app.liveEditors[a]; state.err == nil {
					t.Fatal("conflicting Save was not rejected")
				}
			} else {
				switchTestKey(t, app, tea.KeyEsc)
			}
			if app.ws != old || !a.IsOpen() || !b.IsOpen() || a.Value() != "Xbase" || b.Value() != "Yother" || app.Input.Value() != "safe composer" || app.workspaceSwitchIntent != nil || app.workspaceAttachPending {
				t.Fatal("cancel/save failure lost current workspace, views, draft, or left switching frozen")
			}
			// Explicitly resolving the ordinary dirty-close prompt cannot resurrect
			// the cancelled workspace intent.
			app.closeWorkspaceBoundOverlays()
			app.showDirtyClosePrompt(p)
			app.dirtyPromptCancel()
			if app.workspaceSwitchIntent != nil || app.ws != old {
				t.Fatal("unrelated dirty dialog resurrected switch intent")
			}
		})
	}
}

// This delegate delays only destination return. Attach/detach still use actual
// framed net.Pipe connections; ignoring ctx reproduces a late backend success.
type switchDelayedPort struct {
	shellport.Workspace
	shellport.Connection
	started, release chan struct{}
	closes           atomic.Int32
	prepared         *switchCountedPort
}

type switchCountedPort struct {
	shellport.Workspace
	shellport.Connection
	closes                  atomic.Int32
	closed                  chan struct{}
	closeStarted, closeGate chan struct{}
}

func (p *switchCountedPort) Close() error {
	p.closes.Add(1)
	if p.closeStarted != nil {
		close(p.closeStarted)
	}
	if p.closeGate != nil {
		<-p.closeGate
	}
	err := p.Connection.Close()
	if p.closed != nil {
		close(p.closed)
	}
	return err
}
func (p *switchDelayedPort) Close() error { p.closes.Add(1); return p.Connection.Close() }
func (p *switchDelayedPort) AttachWorkspace(_ context.Context, root string) (shellport.Workspace, error) {
	next, err := p.Connection.AttachWorkspace(context.Background(), root)
	if err != nil {
		return nil, err
	}
	p.prepared = &switchCountedPort{Workspace: next, Connection: next.(shellport.Connection)}
	close(p.started)
	<-p.release
	return p.prepared, nil
}

func TestConnectedWorkspaceSwitchEscRejectsLateSuccessAndStaleSequence(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	shell := newRemoteJourneyShell(t, rt, t.TempDir())
	app := shell.app
	remoteJourneyCreate(t, shell)
	configureComposerDraftTest(t, app)
	old := app.ws
	port := &switchDelayedPort{Workspace: old, Connection: old.(shellport.Connection), started: make(chan struct{}), release: make(chan struct{})}
	app.ws = port
	app.Input.SetInput("do not consume")
	cmd := confirmWorkspaceSwitchTest(t, app, t.TempDir())
	if cmd == nil || !app.workspaceAttachPending {
		t.Fatal("switch did not schedule preparation")
	}
	sequence := app.workspaceAttachSequence
	result := make(chan tea.Msg, 1)
	go func() { result <- cmd() }()
	select {
	case <-port.started:
	case <-time.After(10 * time.Second):
		t.Fatal("destination preparation did not start")
	}
	_, cancel := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	runAppCommand(t, app, cancel)
	if app.workspaceAttachPending || app.workspaceSwitchIntent != nil || app.workspaceAttachSequence <= sequence {
		t.Fatal("Esc did not immediately invalidate switch authority")
	}
	close(port.release)
	var late tea.Msg
	select {
	case late = <-result:
	case <-time.After(10 * time.Second):
		t.Fatal("late attach did not finish")
	}
	runAppCommand(t, app, func() tea.Msg { return late })
	if app.ws != port || app.Input.Value() != "do not consume" || port.closes.Load() != 0 || port.prepared.closes.Load() != 1 {
		t.Fatal("late success replaced/closed current attachment or leaked prepared destination")
	}
	// Replaying a stale completion cannot cancel a newer exact intent.
	app.ws = old
	newer := confirmWorkspaceSwitchTest(t, app, t.TempDir())
	current := app.workspaceSwitchIntent
	_, staleClose := app.Update(remoteWorkspaceReadyMsg{owner: old, sequence: sequence, err: context.Canceled})
	runAppCommand(t, app, staleClose)
	if current == nil || app.workspaceSwitchIntent != current || !app.workspaceAttachPending {
		t.Fatal("stale sequence erased newer switch")
	}
	app.cancelRemoteWorkspaceSwitch(false)
	runAppCommand(t, app, newer)
}

func TestConnectedWorkspaceSwitchPeerDeltaAndPendingHistoryFailClosed(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	path := switchTestFile(t, root, "peer.txt", "base")
	one, two := newRemoteJourneyShell(t, rt, root), newRemoteJourneyShell(t, rt, root)
	a, b := liveTestOpen(t, one.app, path), liveTestOpen(t, two.app, path)
	old := one.app.ws
	prepared := confirmWorkspaceSwitchTest(t, one.app, t.TempDir())
	if prepared == nil {
		t.Fatal("clean editor did not prepare attachment")
	}
	ready := prepared().(remoteWorkspaceReadyMsg)
	b.InsertRune('P')
	runAppCommand(t, two.app, two.app.drainLiveEditorCommands())
	liveTestSync(t, one.app)
	_, closeNext := one.app.Update(ready)
	runAppCommand(t, one.app, closeNext)
	if one.app.ws != old || !a.IsOpen() || a.Value() != "Pbase" || one.app.workspaceSwitchIntent != nil {
		t.Fatal("peer revision during attach was silently detached")
	}
	one.app.closeWorkspaceBoundOverlays()
	// Undo can make bytes clean before its exact canonical command completes.
	b.Undo()
	runAppCommand(t, two.app, two.app.drainLiveEditorCommands())
	liveTestSync(t, one.app)
	a.InsertRune('L')
	a.Undo()
	if cmd := confirmWorkspaceSwitchTest(t, one.app, t.TempDir()); cmd != nil || one.app.workspaceSwitchIntent == nil || one.app.workspaceSwitchIntent.attaching {
		t.Fatal("unpublished clean history was not held behind the editor barrier")
	}
	runAppCommand(t, one.app, one.app.drainLiveEditorCommands())
	if one.app.ws == old {
		t.Fatal("settled history did not automatically resume switch")
	}
	liveTestSync(t, two.app)
	if b.Value() != "base" {
		t.Fatalf("pending history was lost/replayed across detach: %q", b.Value())
	}
}

func TestConnectedWorkspaceSwitchAttachFailureKeepsDirtyViews(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root := t.TempDir()
	shell := newRemoteJourneyShell(t, rt, root)
	p := switchTestFile(t, root, "draft.txt", "base")
	ed := liveTestOpen(t, shell.app, p)
	ed.InsertRune('X')
	old := shell.app.ws
	runAppCommand(t, shell.app, confirmWorkspaceSwitchTest(t, shell.app, filepath.Join(t.TempDir(), "missing")))
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	switchTestKey(t, shell.app, 'd')
	if shell.app.ws != old || !ed.IsOpen() || ed.Value() != "Xbase" || !ed.Modified() || shell.app.workspaceAttachPending {
		t.Fatal("destination failure closed the original dirty view")
	}
}

func TestConnectedWorkspaceSwitchPendingSaveWaitsForAcknowledgement(t *testing.T) {
	rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
	root, target := t.TempDir(), t.TempDir()
	shell := newRemoteJourneyShell(t, rt, root)
	path := switchTestFile(t, root, "saving.txt", "base")
	ed := liveTestOpen(t, shell.app, path)
	ed.InsertRune('S')
	runAppCommand(t, shell.app, shell.app.drainLiveEditorCommands())
	shell.app.requestEditorSaveWithFormat(ed, workspace.DocumentSaveManual, false, false)
	held := shell.app.drainLiveEditorCommands()
	old := shell.app.ws
	if cmd := confirmWorkspaceSwitchTest(t, shell.app, target); cmd != nil || shell.app.workspaceSwitchIntent == nil || shell.app.workspaceSwitchIntent.attaching {
		t.Fatal("pending Save allowed premature detach")
	}
	if !strings.Contains(shell.app.remoteWorkspaceSwitchStatus(), "Preparing") {
		t.Fatal("pending Save freezes input without visible preparation status")
	}
	runAppCommand(t, shell.app, held)
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "Sbase" || shell.app.ws == old || shell.app.ws.RootPath() != target {
		t.Fatalf("Save acknowledgement did not safely resume intent: disk=%q error=%v root=%q", data, err, shell.app.ws.RootPath())
	}
}

func TestConnectedWorkspaceSwitchPreparedCleanupNeverBlocksUpdateAndJoinsShutdown(t *testing.T) {
	for _, shutdown := range []bool{false, true} {
		name := "esc"
		if shutdown {
			name = "shutdown-undelivered"
		}
		t.Run(name, func(t *testing.T) {
			rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			shell := newRemoteJourneyShell(t, rt, t.TempDir())
			app, old := shell.app, shell.app.ws
			prepared := confirmWorkspaceSwitchTest(t, app, t.TempDir())
			if prepared == nil {
				t.Fatal("clean switch did not return asynchronous command")
			}
			// Return a real prepared connection but never deliver its message.
			ready := prepared().(remoteWorkspaceReadyMsg)
			candidate := &switchCountedPort{Workspace: ready.next, Connection: ready.next.(shellport.Connection), closed: make(chan struct{})}
			ready.intent.preparation.mu.Lock()
			ready.intent.preparation.next = candidate
			ready.intent.preparation.mu.Unlock()
			ready.next = candidate
			if shutdown {
				app.StopAsyncIO()
				if candidate.closes.Load() != 1 || len(app.workspaceSwitchPreparations) != 0 {
					t.Fatal("shutdown did not join cleanup of the returned-but-undelivered attachment")
				}
			} else {
				// The actual connection Close happens only in the cancellation
				// worker; the UI can immediately take another input message.
				candidate.closeStarted, candidate.closeGate = make(chan struct{}), make(chan struct{})
				defer func() {
					select {
					case <-candidate.closeGate:
					default:
						close(candidate.closeGate)
					}
				}()
				updated := make(chan tea.Cmd, 1)
				go func() { _, cmd := app.Update(tea.KeyPressMsg{Code: tea.KeyEsc}); updated <- cmd }()
				var cmd tea.Cmd
				select {
				case cmd = <-updated:
				case <-time.After(time.Second):
					t.Fatal("Update blocked on destination Close")
				}
				if app.workspaceAttachPending || app.ws != old {
					t.Fatal("Esc did not synchronously release the input shield")
				}
				runAppCommand(t, app, cmd)
				select {
				case <-candidate.closeStarted:
				case <-time.After(10 * time.Second):
					t.Fatal("cancel did not start destination cleanup")
				}
				close(candidate.closeGate)
				select {
				case <-candidate.closed:
				case <-time.After(10 * time.Second):
					t.Fatal("cancel leaked an undelivered prepared attachment")
				}
			}
			runAppCommand(t, app, func() tea.Msg { return ready })
			if candidate.closes.Load() != 1 || app.ws != old {
				t.Fatal("late ready double-closed or adopted a released connection")
			}
		})
	}
}

func TestConnectedWorkspaceSwitchUnsupportedComposerFailsClosed(t *testing.T) {
	for _, hasSession := range []bool{false, true} {
		name := "unowned"
		if hasSession {
			name = "store-unavailable"
		}
		t.Run(name, func(t *testing.T) {
			rt := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			shell := newRemoteJourneyShell(t, rt, t.TempDir())
			if hasSession {
				remoteJourneyCreate(t, shell)
			}
			app, old := shell.app, shell.app.ws
			app.Input.SetInput("exact unsupported private draft")
			if cmd := confirmWorkspaceSwitchTest(t, app, t.TempDir()); cmd != nil {
				t.Fatal("unsupported draft allowed destination preparation")
			}
			if app.ws != old || app.workspaceSwitchIntent != nil || app.workspaceAttachPending || app.Input.Value() != "exact unsupported private draft" {
				t.Fatal("unsupported draft was consumed or hidden behind pending switch")
			}
		})
	}
}
