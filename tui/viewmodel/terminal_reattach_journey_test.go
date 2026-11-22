package viewmodel

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"crude/core/coreprotocol"
	"crude/core/shellport"
	"crude/core/wiretest"
	"crude/tui/ui"
	"crude/workspace"
)

// TestTerminalEchoChild is the program of the retained terminals below when
// the test binary re-executes itself: it prints a marker line and copies its
// input to its output, like `printf marker; exec cat`, on every platform.
func TestTerminalEchoChild(t *testing.T) {
	args := os.Args
	if len(args) < 3 || args[len(args)-2] != "crude-echo-child" {
		return
	}
	fmt.Print(args[len(args)-1] + "\r\n")
	_, _ = io.Copy(os.Stdout, os.Stdin)
	os.Exit(0)
}

// echoTerminal describes a terminal that runs TestTerminalEchoChild.
func echoTerminal(t *testing.T, marker string, rows int) *workspace.TerminalCreateOpts {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return &workspace.TerminalCreateOpts{Command: bin, Args: []string{"-test.run=^TestTerminalEchoChild$", "--", "crude-echo-child", marker}, Cols: 80, Rows: rows}
}

// retainedTerminal creates a real PTY through a core connection that is not a
// shell, like a terminal started by an earlier window. The child echoes typed
// lines.
func retainedTerminal(t *testing.T, call func(string, string, any) coreprotocol.Response, root string) string {
	t.Helper()
	if got := call("attach", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}); got.Error != nil {
		t.Fatal(got.Error)
	}
	created := call("create", coreprotocol.TerminalCreate, echoTerminal(t, "OUTPUT-BEFORE-CLOSE", 8))
	var info workspace.TerminalInfo
	if created.Error != nil || coreprotocol.Decode(created.Payload, &info) != nil || info.ID == "" {
		t.Fatalf("create: %+v", created)
	}
	return info.ID
}

func paintedText(app *App) string { return ansi.Strip(app.View().RenderContent()) }

func typeTerminalText(t *testing.T, shell remoteJourneyShell, text string) {
	t.Helper()
	for _, r := range text {
		key := tea.KeyPressMsg{Code: r, Text: string(r)}
		if r == '\r' {
			key = tea.KeyPressMsg{Code: tea.KeyEnter}
		}
		_, cmd := shell.app.Update(key)
		runAppCommand(t, shell.app, cmd)
	}
}

func noticeText(app *App) string {
	var all []string
	for _, notice := range app.ws.Status().Notifications(true) {
		all = append(all, notice.Summary)
	}
	return strings.Join(all, "\n")
}

// The reported defect: closing a Crude window and opening another showed an
// empty terminal. The retained PTY and its buffer were alive in the core, but
// a shell that had not created the terminal never read or streamed its rows,
// and typing opened a menu instead of reaching the shell.
func TestConnectedReattachedShellShowsRetainedTerminalBufferAndTypesAtOnce(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	controller, err := runtime.Connect("terminal-reattach-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	first := newRemoteJourneyShell(t, runtime, root)
	target := first.app.ws.(shellport.ShellControl).ControlIdentity()
	call := func(requestID, method string, input any) coreprotocol.Response {
		raw, err := wiretest.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return controller.Call(t.Context(), coreprotocol.Request{ID: requestID, Owner: controller.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: raw})
	}
	id := retainedTerminal(t, call, root)
	shells := []remoteJourneyShell{first}
	remoteJourneyUntil(t, shells, func() bool { _, ok := first.app.termMgr().Get(id); return ok })
	first.app.toggleTerminalFocus()
	first.app.View()
	remoteJourneyUntil(t, shells, func() bool {
		return strings.Contains(paintedText(first.app), "OUTPUT-BEFORE-CLOSE") && first.app.terminalLeases[id].Generation != 0
	})
	typeTerminalText(t, first, "typed-in-first\r")
	remoteJourneyUntil(t, shells, func() bool { return strings.Count(paintedText(first.app), "typed-in-first") >= 2 })

	// Close the first window, then open a new one on the same core.
	first.app.StopAsyncIO()
	if err := first.app.CloseWorkspaceAttachment(); err != nil {
		t.Fatal(err)
	}
	second := newRemoteJourneyShell(t, runtime, root)
	shells = []remoteJourneyShell{second}
	remoteJourneyUntil(t, shells, func() bool { _, ok := second.app.termMgr().Get(id); return ok })
	second.app.toggleTerminalFocus()
	second.app.View()
	if !second.app.terminalOwnsKeys() || second.app.termDock.ActiveID() != id {
		t.Fatal("Alt+F12 did not bring back the retained terminal")
	}
	// Type at once, before control or rows arrive: no key may be lost and no
	// menu may open.
	typeTerminalText(t, second, "typed-after-reattach\r")
	if second.app.LastContextMenu != nil && second.app.overlayHost.IsOpen(ui.OverlayContextMenu) {
		t.Fatal("typing into the reattached terminal opened a menu")
	}
	remoteJourneyUntil(t, shells, func() bool {
		text := paintedText(second.app)
		return strings.Contains(text, "OUTPUT-BEFORE-CLOSE") && strings.Contains(text, "typed-in-first") && strings.Count(text, "typed-after-reattach") >= 2
	})
	if second.app.terminalLeases[id].Generation == 0 {
		t.Fatal("the reattached window did not get input control")
	}
}

// Several windows show the same terminal at once. Each shows its buffer. A
// window that holds input control keeps it; another window gets an
// explanation and can take control explicitly.
func TestConnectedPeerWindowsShareTerminalBufferAndHandOverControlExplicitly(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	controller, err := runtime.Connect("terminal-peer-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	one := newRemoteJourneyShell(t, runtime, root)
	two := newRemoteJourneyShell(t, runtime, root)
	target := one.app.ws.(shellport.ShellControl).ControlIdentity()
	call := func(requestID, method string, input any) coreprotocol.Response {
		raw, err := wiretest.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return controller.Call(t.Context(), coreprotocol.Request{ID: requestID, Owner: controller.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: raw})
	}
	id := retainedTerminal(t, call, root)
	shells := []remoteJourneyShell{one, two}
	remoteJourneyUntil(t, shells, func() bool {
		_, a := one.app.termMgr().Get(id)
		_, b := two.app.termMgr().Get(id)
		return a && b
	})
	one.app.toggleTerminalFocus()
	one.app.View()
	remoteJourneyUntil(t, shells, func() bool { return one.app.terminalLeases[id].Generation != 0 })
	two.app.toggleTerminalFocus()
	two.app.View()
	remoteJourneyUntil(t, shells, func() bool {
		return strings.Contains(paintedText(two.app), "OUTPUT-BEFORE-CLOSE") && two.app.terminalClaims[id] == 0
	})
	if two.app.terminalLeases[id].Generation != 0 || strings.Contains(noticeText(two.app), "another window") {
		t.Fatal("showing the terminal took control or complained without input")
	}
	typeTerminalText(t, two, "lost\r")
	remoteJourneyUntil(t, shells, func() bool { return strings.Contains(noticeText(two.app), "gets input from another window") })
	typeTerminalText(t, one, "from-one\r")
	remoteJourneyUntil(t, shells, func() bool { return strings.Count(paintedText(two.app), "from-one") >= 2 })
	if strings.Contains(paintedText(one.app), "lost") {
		t.Fatal("input from a window without control reached the terminal")
	}
	// Explicit takeover from the terminal menu.
	two.app.openTerminalControlMenu(id)
	taken := false
	for _, section := range two.app.LastContextMenu.Sections {
		for _, item := range section.Items {
			if item.Label == "Take input control" && item.Action != nil {
				// Choosing a menu item closes the menu, then runs it.
				two.app.closeContextMenu()
				item.Action()
				taken = true
			}
		}
	}
	if !taken {
		t.Fatal("the viewer's menu has no Take input control")
	}
	remoteJourneyUntil(t, shells, func() bool { return two.app.terminalLeases[id].Generation != 0 })
	typeTerminalText(t, two, "from-two\r")
	remoteJourneyUntil(t, shells, func() bool { return strings.Count(paintedText(one.app), "from-two") >= 2 })
	typeTerminalText(t, one, "x")
	remoteJourneyUntil(t, shells, func() bool {
		return one.app.terminalLeases[id].Generation == 0 && strings.Contains(noticeText(one.app), "gets input from another window")
	})
}

// Dozens of retained terminals all come back to a new window as dock tabs,
// each with its own buffer, and each accepts input when chosen.
func TestConnectedReattachedShellRestoresEveryRetainedTerminal(t *testing.T) {
	runtime := remoteJourneyRuntime(t, "")
	root := t.TempDir()
	controller, err := runtime.Connect("terminal-many-fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer controller.Close()
	shell := newRemoteJourneyShell(t, runtime, root)
	target := shell.app.ws.(shellport.ShellControl).ControlIdentity()
	call := func(requestID, method string, input any) coreprotocol.Response {
		raw, err := wiretest.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		return controller.Call(t.Context(), coreprotocol.Request{ID: requestID, Owner: controller.Owner, WorkspaceID: target.WorkspaceID, Method: method, Payload: raw})
	}
	if got := call("attach", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}); got.Error != nil {
		t.Fatal(got.Error)
	}
	const count = 24
	ids := make([]string, count)
	for i := range count {
		created := call(fmt.Sprintf("create-%d", i), coreprotocol.TerminalCreate, echoTerminal(t, fmt.Sprintf("BUFFER-%02d", i), 6))
		var info workspace.TerminalInfo
		if created.Error != nil || coreprotocol.Decode(created.Payload, &info) != nil {
			t.Fatalf("create %d: %+v", i, created)
		}
		ids[i] = info.ID
	}
	shells := []remoteJourneyShell{shell}
	remoteJourneyUntil(t, shells, func() bool { return len(shell.app.terminalInfos()) == count })
	shell.app.toggleTerminalFocus()
	shell.app.View()
	if got := len(shell.app.termDock.IDs()); got != count {
		t.Fatalf("dock shows %d of %d retained terminals", got, count)
	}
	for i, id := range ids {
		shell.app.focusTerminal(id)
		shell.app.View()
		want := fmt.Sprintf("BUFFER-%02d", i)
		remoteJourneyUntil(t, shells, func() bool { return strings.Contains(paintedText(shell.app), want) })
		typeTerminalText(t, shell, fmt.Sprintf("echo-%02d\r", i))
		remoteJourneyUntil(t, shells, func() bool {
			return strings.Count(paintedText(shell.app), fmt.Sprintf("echo-%02d", i)) >= 2
		})
	}
}
