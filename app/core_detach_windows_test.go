package app

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

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
