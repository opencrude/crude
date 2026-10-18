package workspace

import (
	"crude/terminalvt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	ttypes "crude/types"
)

func fakeExecutable(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// Windows PowerShell 5.1 is installed on every Windows machine, so finding it
// first used to hide an installed PowerShell 7 forever.
func TestDetectTerminalShellsPrefersModernShells(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if runtime.GOOS == "windows" {
		for _, key := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA", "SystemRoot", "ComSpec"} {
			t.Setenv(key, "")
		}
		fakeExecutable(t, dir, "powershell.exe")
		fakeExecutable(t, dir, "cmd.exe")
		pwsh := fakeExecutable(t, dir, "pwsh.exe")
		shells := DetectTerminalShells()
		ids := make([]string, len(shells))
		for i, shell := range shells {
			ids[i] = shell.ID
		}
		if !slices.Equal(ids, []string{"pwsh", "powershell", "cmd"}) || !strings.EqualFold(shells[0].Path, pwsh) {
			t.Fatalf("detected %v (%+v); want PowerShell 7 first", ids, shells)
		}
		return
	}
	login := fakeExecutable(t, dir, "fish")
	fakeExecutable(t, dir, "bash")
	fakeExecutable(t, dir, "zsh")
	t.Setenv("SHELL", login)
	shells := DetectTerminalShells()
	if len(shells) < 3 || shells[0].ID != "fish" || shells[0].Path != login || !slices.Equal(shells[0].LoginArgs, []string{"-l"}) {
		t.Fatalf("detected %+v; want the login shell first", shells)
	}
}

func TestTerminalSettingsStartConfiguredShellAndEnvironment(t *testing.T) {
	settings := ttypes.TerminalSettings{Shell: ttypes.TerminalShellCustom, Command: "/bin/sh", Args: []string{"-c", `printf "VALUE=$CRUDE_TERMINAL_PROBE"; cat`}, Environment: map[string]string{"CRUDE_TERMINAL_PROBE": "from-settings"}}
	if runtime.GOOS == "windows" {
		settings.Command, settings.Args = "cmd.exe", []string{"/q", "/k", "echo VALUE=%CRUDE_TERMINAL_PROBE%"}
	}
	manager := NewTerminalManager(t.TempDir())
	service, err := NewTerminalService(manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.CloseAll)
	manager.SetSettings(settings)
	info, err := service.Create(TerminalCreateOpts{Cols: 60, Rows: 6})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		projection, err := service.Rows(info.ID, terminalvt.RowRequest{Start: -1, Count: 6})
		if err != nil {
			t.Fatal(err)
		}
		var text strings.Builder
		for _, row := range projection.Grid.Cells {
			for _, cell := range row {
				text.WriteString(cell.Text)
			}
		}
		if strings.Contains(text.String(), "VALUE=from-settings") {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("configured shell or environment not used: %q", text.String())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestTerminalSettingsRejectMissingShellWithGuidance(t *testing.T) {
	manager := NewTerminalManager(t.TempDir())
	service, err := NewTerminalService(manager)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.CloseAll)
	manager.SetSettings(ttypes.TerminalSettings{Shell: "definitely-not-a-shell"})
	if _, err := service.Create(TerminalCreateOpts{}); err == nil || !strings.Contains(err.Error(), "Settings › Terminal") {
		t.Fatalf("missing shell error = %v", err)
	}
	if len(manager.List()) != 0 {
		t.Fatal("a failed launch left a terminal behind")
	}
}
