package workspace

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"crude/internal/testterminal"
	ttypes "crude/types"
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
	settings := ttypes.TerminalSettings{Shell: ttypes.TerminalShellCustom, Command: command, Args: args,
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
		"OUTER_TERM": "outer-terminal", "TERM": ttypes.GhosttyTermName,
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
	for _, tc := range []struct {
		name, choice string
		env          []string
		login        bool
	}{
		{name: "unknown base keeps login", login: true},
		{name: "attaching shell is initialized", env: []string{}},
		{name: "explicit on with attaching shell", choice: "on", env: []string{}, login: true},
		{name: "explicit off without attaching shell", choice: "off"},
		{name: "explicit off with attaching shell", choice: "off", env: []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager := NewTerminalManager(t.TempDir())
			manager.SetShellEnvironment(tc.env)
			manager.SetSettings(ttypes.TerminalSettings{LoginShell: tc.choice, Args: []string{"user-argument"}})
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
