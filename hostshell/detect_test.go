package hostshell

import (
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
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
func TestDetectPrefersModernShells(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	if runtime.GOOS == "windows" {
		for _, key := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA", "SystemRoot", "ComSpec"} {
			t.Setenv(key, "")
		}
		fakeExecutable(t, dir, "powershell.exe")
		fakeExecutable(t, dir, "cmd.exe")
		pwsh := fakeExecutable(t, dir, "pwsh.exe")
		shells := Detect()
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
	shells := Detect()
	if len(shells) < 3 || shells[0].ID != "fish" || shells[0].Path != login || !slices.Equal(shells[0].LoginArgs, []string{"-l"}) {
		t.Fatalf("detected %+v; want the login shell first", shells)
	}
}

// PATH bash.exe on Windows is the WSL launcher, which runs in a separate file
// system. "bash" must resolve to Git Bash, or fail clearly when it is absent.
func TestRequestedBashIsNeverTheWSLLauncher(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows shells")
	}
	dir, programs := t.TempDir(), t.TempDir()
	for _, key := range []string{"ProgramW6432", "ProgramFiles(x86)", "LOCALAPPDATA", "SystemRoot", "ComSpec"} {
		t.Setenv(key, "")
	}
	t.Setenv("PATH", dir)
	t.Setenv("ProgramFiles", programs)
	fakeExecutable(t, dir, "bash.exe") // the WSL launcher's name on PATH
	if shell, err := Requested("bash"); err == nil {
		t.Fatalf("bash resolved to %q without Git Bash", shell.Path)
	}
	// A new installation location is a new environment, so it is detected at
	// once rather than after the inventory expires.
	programs = t.TempDir()
	t.Setenv("ProgramFiles", programs)
	if err := os.MkdirAll(filepath.Join(programs, "Git", "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	gitBash := fakeExecutable(t, filepath.Join(programs, "Git", "bin"), "bash.exe")
	shell, err := Requested("bash")
	if err != nil || !strings.EqualFold(shell.Path, gitBash) || shell.ID != "git-bash" {
		t.Fatalf("bash = %+v, %v; want Git Bash %q", shell, err, gitBash)
	}
	if auto := ResolveAgent(""); auto.ID != "git-bash" {
		t.Fatalf("Auto agent shell = %+v; want Git Bash first", auto)
	}
}
