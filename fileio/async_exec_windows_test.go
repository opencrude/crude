//go:build windows

package fileio

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

func TestWindowsAgentCommandsNeverAttachAVisibleConsole(t *testing.T) {
	background := setProcessGroupCmd(context.Background(), "cmd.exe", "/C", "echo hidden")
	if background.SysProcAttr == nil ||
		background.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 ||
		!background.SysProcAttr.HideWindow {
		t.Fatalf("background command flags=%+v", background.SysProcAttr)
	}

	synchronous := exec.Command("cmd.exe", "/C", "echo hidden")
	setHiddenCmdSysProcAttr(synchronous)
	if synchronous.SysProcAttr == nil ||
		synchronous.SysProcAttr.CreationFlags&windows.CREATE_NO_WINDOW == 0 ||
		!synchronous.SysProcAttr.HideWindow {
		t.Fatalf("synchronous command flags=%+v", synchronous.SysProcAttr)
	}
}

func TestWindowsInteractiveConsoleStreamsAreCaptured(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	result, err := manager.StartInteractive(ctx, InteractiveOptions{
		Command: "echo INTERACTIVE_STDOUT_READY&echo INTERACTIVE_STDERR_READY 1>&2",
		Shell:   "cmd.exe", TTY: true, CWD: t.TempDir(),
	}, 100*time.Millisecond, 1000)
	if err != nil {
		t.Fatal(err)
	}
	output := result.Output
	for result.ExitCode == nil {
		result, err = manager.WriteInteractive(ctx, result.SessionID, "", 100*time.Millisecond, 1000)
		if err != nil {
			t.Fatalf("interactive console never completed: output=%q error=%v", output, err)
		}
		output += result.Output
	}
	if *result.ExitCode != 0 || !strings.Contains(output, "INTERACTIVE_STDOUT_READY") || !strings.Contains(output, "INTERACTIVE_STDERR_READY") {
		t.Fatalf("interactive console output escaped capture: exit=%d output=%q", *result.ExitCode, output)
	}
}

func TestWindowsWingetOutputStaysInCapturedCommandChannel(t *testing.T) {
	if _, err := exec.LookPath("winget.exe"); err != nil {
		t.Skip("winget is not installed")
	}
	manager := NewAsyncCommandManager()
	if _, err := manager.Start("winget-version", "winget --version", t.TempDir(), 15); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		info, ok := manager.Check("winget-version")
		if !ok {
			t.Fatal("winget command disappeared")
		}
		if info.Status != AsyncRunning {
			if info.Status != AsyncCompleted {
				t.Fatalf("winget status=%+v", info)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("winget did not finish")
		}
		time.Sleep(20 * time.Millisecond)
	}
	output, _, _, err := manager.ReadOutputLimit("winget-version", 0, DefaultOutputChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.TrimSpace(output), "v") {
		t.Fatalf("captured winget output=%q", output)
	}
	manager.CloseAndWait()
}
