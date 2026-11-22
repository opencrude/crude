package workspace

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/internal/terminalvt"
	"crude/internal/testterminal"
	"crude/types/setting"
)

func TestTerminalManagerCreateWriteClose(t *testing.T) {
	cmd, args := testterminal.Command(t, "echo")
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{
		Command: cmd, Args: args,
		CWD:   t.TempDir(),
		Cols:  40,
		Rows:  10,
		Title: "cat",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if info.ID == "" || !info.Alive {
		t.Fatalf("info=%+v", info)
	}
	list := mgr.List()
	if len(list) != 1 || list[0].ID != info.ID {
		t.Fatalf("List=%v", list)
	}
	if err := mgr.Write(info.ID, []byte("hi\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	mgr.SetPlacement(info.ID, TerminalFloating)
	got, ok := mgr.Get(info.ID)
	if !ok || got.Placement != TerminalFloating {
		t.Fatalf("Get=%+v %v", got, ok)
	}
	mgr.Close(info.ID)
	if len(mgr.List()) != 0 {
		t.Fatal("Close should remove the session")
	}
}

func TestTerminalManagerResize(t *testing.T) {
	cmd, args := testterminal.Command(t, "echo")
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{Command: cmd, Args: args, CWD: t.TempDir(), Cols: 20, Rows: 8})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	if err := mgr.Resize(info.ID, 60, 20); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	got, _ := mgr.Get(info.ID)
	if got.Cols != 60 || got.Rows != 20 {
		t.Fatalf("size=%dx%d", got.Cols, got.Rows)
	}
}

func TestTerminalWriteDoesNotBlockUI(t *testing.T) {
	cmd, args := testterminal.Command(t, "idle")
	mgr := NewTerminalManager(t.TempDir())
	info, err := mgr.Create(TerminalCreateOpts{
		Command: cmd,
		Args:    args,
		CWD:     t.TempDir(),
		Cols:    40,
		Rows:    10,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	t.Cleanup(func() { mgr.Close(info.ID) })
	payload := bytes.Repeat([]byte("x"), 256*1024)
	start := time.Now()
	if err := mgr.Write(info.ID, payload); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Write blocked for %s; PTY stdin must be asynchronous", elapsed)
	}
}

func terminalEnvironmentOutput(t *testing.T, manager *TerminalManager, opts TerminalCreateOpts) string {
	t.Helper()
	sink := &terminalTestSink{events: make(chan struct{}, 1)}
	bus := NewEventBus()
	bus.SetSink(sink)
	manager.SetEventBus(bus)
	info, err := manager.Create(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Close(info.ID) })
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	var output strings.Builder
	seen := 0
	for {
		sink.mu.Lock()
		messages := slices.Clone(sink.msgs[seen:])
		seen = len(sink.msgs)
		sink.mu.Unlock()
		for _, message := range messages {
			switch msg := message.(type) {
			case TerminalOutputMsg:
				output.Write(msg.Data)
			case TerminalExitedMsg:
				if msg.ExitCode != 0 {
					t.Fatalf("PTY child failed: %+v, output=%q", msg, output.String())
				}
				return output.String()
			}
		}
		select {
		case <-sink.events:
		case <-deadline.C:
			t.Fatalf("PTY child did not finish: %q", output.String())
		}
	}
}

func TestTerminalEnvironmentExpandsEnvReferences(t *testing.T) {
	root := t.TempDir()
	manager := NewTerminalManager(root)
	basePath := filepath.Join(root, "shell-tools")
	base := []string{"PATH=" + basePath, "TERM=outer-terminal", "PEER=base-value", "INDIRECT=${workspaceFolder}"}
	manager.SetShellEnvironment(base)
	// The manager owns a detached snapshot, not the caller's slice.
	base[0] = "PATH=changed-after-attach"
	separator := string(os.PathListSeparator)
	command, args := testterminal.Command(t, "env", "PATH", "VIRTUAL_ENV", "UNKNOWN", "LITERAL", "COPY", "PEER", "INDIRECT", "OUTER_TERM", "TERM")
	settings := setting.Terminal{Shell: setting.TerminalShellCustom, Command: command, Args: args,
		Environment: map[string]string{
			"PATH":        "${workspaceFolder}/.venv/bin" + separator + "${env:PATH}",
			"VIRTUAL_ENV": "${workspaceFolder}/.venv",
			"UNKNOWN":     "before${env:CRUDE_MISSING_TERMINAL_VARIABLE}after",
			"LITERAL":     "$PATH ${PATH} %PATH% ${command:test}",
			"COPY":        "${env:PEER}", "PEER": "override-value",
			"INDIRECT": "${env:INDIRECT}", "OUTER_TERM": "${env:TERM}",
		}}
	manager.SetSettings(settings)
	output := terminalEnvironmentOutput(t, manager, TerminalCreateOpts{CWD: t.TempDir(), Cols: 2048, Rows: 20})
	for key, want := range map[string]string{
		"PATH":        root + "/.venv/bin" + separator + basePath,
		"VIRTUAL_ENV": root + "/.venv", "UNKNOWN": "beforeafter",
		"LITERAL": "$PATH ${PATH} %PATH% ${command:test}", "COPY": "base-value",
		"PEER": "override-value", "INDIRECT": "${workspaceFolder}",
		"OUTER_TERM": "outer-terminal", "TERM": terminalvt.GhosttyTermName,
	} {
		if !strings.Contains(output, fmt.Sprintf("%s=[%s]", key, want)) {
			t.Errorf("child did not receive %s=%q: %q", key, want, output)
		}
	}
	if got := manager.Settings().Environment["PATH"]; got != settings.Environment["PATH"] {
		t.Fatalf("launch replaced the stored expression: %q", got)
	}
}

func TestTerminalLoginArgumentsRespectAttachingEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix shell inventory fixture; generic shell argument order is covered in types")
	}
	shell := filepath.Join(t.TempDir(), "test-shell")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\nprintf 'ARGS='\nprintf '<%s>' \"$@\"\nprintf '\\n'\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SHELL", shell)
	initialized := []string{"SHELL=" + shell}
	for _, tc := range []struct {
		name, choice string
		env          []string
		login        bool
	}{
		{name: "unknown base keeps login", login: true},
		{name: "attaching shell is initialized", env: initialized},
		{name: "explicit on with attaching shell", choice: "on", env: initialized, login: true},
		{name: "explicit off without attaching shell", choice: "off"},
		{name: "explicit off with attaching shell", choice: "off", env: initialized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewTerminalManager(t.TempDir())
			manager.SetShellEnvironment(tc.env)
			manager.SetSettings(setting.Terminal{LoginShell: tc.choice, Args: []string{"user-argument"}})
			output := terminalEnvironmentOutput(t, manager, TerminalCreateOpts{})
			want := "ARGS=<user-argument>"
			if tc.login {
				want = "ARGS=<-l><user-argument>"
			}
			if !strings.Contains(output, want) {
				t.Fatalf("child arguments = %q, want %q", output, want)
			}
			if manager.Settings().LoginShell != tc.choice {
				t.Fatal("launch overwrote the configured login choice")
			}
		})
	}
}

func TestTerminalEnvironmentEmptyBaseAndKeyCase(t *testing.T) {
	t.Setenv("CRUDE_DAEMON_ONLY", "must-not-leak")
	for _, entry := range PTYEnviron([]string{}) {
		if strings.HasPrefix(entry, "CRUDE_DAEMON_ONLY=") {
			t.Fatal("an explicitly empty base inherited the daemon environment")
		}
	}
	base := []string{"PATH=first", "PATH=last"}
	if runtime.GOOS == "windows" {
		base = []string{"Path=last"}
	}
	env := terminalEnviron(base, map[string]string{"PATH": "prepend:" + "${env:PATH}"}, t.TempDir())
	count := 0
	for _, entry := range env {
		key, value, _ := strings.Cut(entry, "=")
		if environmentKey(key) == environmentKey("PATH") {
			count++
			if value != "prepend:last" {
				t.Fatalf("PATH override = %q", value)
			}
		}
	}
	if count != 1 {
		t.Fatalf("PATH override left %d entries", count)
	}
}

const shellPathFixture = "CRUDE_TEST_TERMINAL_SHELL_PATH"

func TestTerminalShellPathFixtureProcess(t *testing.T) {
	if os.Getenv(shellPathFixture) == "" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	installation := filepath.Base(filepath.Dir(exe))
	fmt.Println("STARTED=" + installation)
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		fmt.Println("REPLIED=" + installation)
	}
}

func TestTerminalReattachUsesNewShellPathWithoutRebindingExistingPTY(t *testing.T) {
	name, id := "bash", "bash"
	if runtime.GOOS == "windows" {
		name, id = "pwsh.exe", "pwsh"
	}
	root := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	copyShell := func(version string) string {
		t.Helper()
		dir := filepath.Join(root, version)
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		src, err := os.Open(exe)
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		dst, err := os.OpenFile(filepath.Join(dir, name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o700)
		if err != nil {
			t.Fatal(err)
		}
		_, err = io.Copy(dst, src)
		if err := errors.Join(err, dst.Close()); err != nil {
			t.Fatal(err)
		}
		return dir
	}
	old, updated := copyShell("shell-7.6.7"), copyShell("shell-7.7.0")
	t.Setenv("PATH", old) // the retained core predates the update
	t.Setenv("SHELL", "")
	t.Setenv(shellPathFixture, "1")
	oldEnv := os.Environ()
	newEnv := setEnv(slices.Clone(oldEnv), "PATH", updated)

	manager := NewTerminalManager(root)
	t.Cleanup(manager.CloseAll)
	sink := &terminalTestSink{events: make(chan struct{}, 1)}
	manager.SetOutputSink(sink)
	manager.SetShellEnvironment(oldEnv)
	settings := setting.Terminal{Args: []string{"-test.run=^TestTerminalShellPathFixtureProcess$"}}
	manager.SetSettings(settings)

	newTerminal := func() string {
		t.Helper()
		info, err := manager.Create(TerminalCreateOpts{})
		if err != nil {
			t.Fatal(err)
		}
		return info.ID
	}
	waitOutput := func(id, want string) {
		t.Helper()
		deadline := time.NewTimer(8 * time.Second)
		defer deadline.Stop()
		var output strings.Builder
		seen := 0
		for {
			sink.mu.Lock()
			messages := slices.Clone(sink.msgs[seen:])
			seen = len(sink.msgs)
			sink.mu.Unlock()
			for _, message := range messages {
				if line, ok := message.(TerminalOutputMsg); ok && line.ID == id {
					output.Write(line.Data)
				}
			}
			if strings.Contains(output.String(), want) {
				return
			}
			select {
			case <-sink.events:
			case <-deadline.C:
				t.Fatalf("terminal %s did not print %q; got %q", id, want, output.String())
			}
		}
	}
	first := newTerminal()
	waitOutput(first, "STARTED=shell-7.6.7")
	manager.SetShellEnvironment(newEnv)
	waitOutput(newTerminal(), "STARTED=shell-7.7.0")
	settings.Shell = id
	manager.SetSettings(settings)
	waitOutput(newTerminal(), "STARTED=shell-7.7.0")
	if err := manager.Write(first, []byte("check\r\n")); err != nil {
		t.Fatal(err)
	}
	waitOutput(first, "REPLIED=shell-7.6.7")
	settings.Shell, settings.Command = setting.TerminalShellCustom, strings.TrimSuffix(name, ".exe")
	manager.SetSettings(settings)
	waitOutput(newTerminal(), "STARTED=shell-7.7.0")
	settings.Shell, settings.Command = id, ""
	manager.SetSettings(settings)
	manager.SetShellEnvironment([]string{"PATH=" + t.TempDir()})
	if _, err := manager.Create(TerminalCreateOpts{}); err == nil || !strings.Contains(err.Error(), "Settings › Terminal") || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("missing selected shell did not fail with actionable guidance: %v", err)
	}
}
