package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/core"
	"crude/coreprotocol"
	"crude/terminalvt"
	"crude/workspace"

	"golang.org/x/sys/windows"
)

func TestDetachedCoreConsoleProbe(t *testing.T) {
	mode := os.Getenv("CRUDE_CONSOLE_PROBE")
	if mode == "" {
		return
	}
	if mode == "parent" {
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command(exe, "-test.run=^TestDetachedCoreConsoleProbe$")
		cmd.Env = append(os.Environ(), "CRUDE_CONSOLE_PROBE=child")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child: %s: %v", out, err)
		}
		fmt.Print(string(out))
		return
	}
	console, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("GetConsoleWindow").Call()
	visible, _, _ := windows.NewLazySystemDLL("user32.dll").NewProc("IsWindowVisible").Call(console)
	fmt.Printf("console-visible=%t\n", visible != 0)
}

func TestDetachedCorePTYInterruptsNativeForegroundCommand(t *testing.T) {
	home := coreTestHome(t)
	root := filepath.Join(home, "project")
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
	defer cancel()
	client, err := ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	attached := attach(t, client, root)
	var info workspace.TerminalInfo
	// This automated foreground-command fixture must not request interactive
	// PowerShell input from a headless runner.
	opts := workspace.TerminalCreateOpts{Command: "powershell.exe", Args: []string{"-NoLogo", "-NoProfile", "-NonInteractive", "-Command", `Write-Output DETACHED_CONTROL_READY; Start-Sleep -Seconds 30; Write-Output MUST_NOT_COMPLETE`}, CWD: root, Cols: 100, Rows: 10}
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalCreate, &opts, &info); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = client.Call(context.Background(), attached.WorkspaceID, coreprotocol.TerminalClose, coreprotocol.TerminalIDRequest{ID: info.ID}, nil)
	}()
	var lease workspace.TerminalLease
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalControl, coreprotocol.TerminalIDRequest{ID: info.ID}, &lease); err != nil {
		t.Fatal(err)
	}
	read := func() workspace.TerminalProjection {
		t.Helper()
		var p workspace.TerminalProjection
		if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalRows, &coreprotocol.TerminalRowsRequest{ID: info.ID, Start: -1}, &p); err != nil {
			t.Fatal(err)
		}
		return p
	}
	text := func(p workspace.TerminalProjection) string {
		var b strings.Builder
		for _, row := range p.Grid.Cells {
			for _, c := range row {
				b.WriteString(c.Text)
			}
			b.WriteByte('\n')
		}
		return b.String()
	}
	deadline := time.Now().Add(8 * time.Second)
	for !strings.Contains(text(read()), "DETACHED_CONTROL_READY") {
		if time.Now().After(deadline) {
			p := read()
			t.Fatalf("native foreground command never became ready: info=%+v generation=%d error=%q synchronized=%t text=%q", p.Info, p.Generation, p.Error, p.Grid.Synchronized, text(p))
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := client.Call(ctx, attached.WorkspaceID, coreprotocol.TerminalInput, &coreprotocol.TerminalInputRequest{Lease: lease, Kind: "key", Key: terminalvt.Key{Code: 'c', Mod: terminalvt.ModCtrl}}, nil); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		p := read()
		if !p.Info.Alive {
			if strings.Contains(text(p), "MUST_NOT_COMPLETE") {
				t.Fatal("child completed instead of interrupting")
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	h, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(info.PID))
	var state uint32
	var waitErr error
	if openErr == nil {
		state, waitErr = windows.WaitForSingleObject(h, 0)
		_ = windows.CloseHandle(h)
	}
	t.Fatalf("foreground Ctrl+C: processOpen=%v processWait=%d/%v projection=%+v text=%q", openErr, state, waitErr, read().Info, text(read()))
}

func TestDetachedCoreDoesNotCreateVisibleChildConsole(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestDetachedCoreConsoleProbe$")
	cmd.Env = append(os.Environ(), "CRUDE_APP_TEST_MAIN=0", "CRUDE_CONSOLE_PROBE=parent")
	cmd.SysProcAttr = detachedProcAttr()
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "console-visible=false") {
		t.Fatalf("detached core child console: %s: %v", out, err)
	}
}

func TestDetachedCoreNoWindowFlagsAreEffective(t *testing.T) {
	attr := detachedProcAttr()
	if !attr.HideWindow || attr.CreationFlags&windows.CREATE_NO_WINDOW == 0 || attr.CreationFlags&(windows.DETACHED_PROCESS|windows.CREATE_NEW_CONSOLE) != 0 {
		t.Fatalf("CREATE_NO_WINDOW is missing or disabled by conflicting flags: %+v", attr)
	}
	if attr.CreationFlags&windows.CREATE_NEW_PROCESS_GROUP == 0 {
		t.Fatal("core remains in the shell's process group")
	}
}
