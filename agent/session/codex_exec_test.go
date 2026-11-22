//go:build !windows

package session

import (
	"encoding/json"
	"strings"
	"testing"

	"crude/agent/safety"
	"crude/types/command"
	"crude/workspace/fileio"
)

func TestNativeCommandSessionContinuesOnSameManager(t *testing.T) {
	mgr := fileio.NewAsyncCommandManager()
	defer mgr.CloseAndWait()
	result, err := executeCodexCommand(t.Context(), mgr, t.TempDir(), "exec_command", map[string]any{"cmd": "read x; printf 'received:%s' \"$x\"", "shell": "/bin/sh", "login": false, "yield_time_ms": 250}, safety.DefaultCommandPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var first command.Result
	if err := json.Unmarshal([]byte(result.Content), &first); err != nil {
		t.Fatal(err)
	}
	if first.SessionID == 0 {
		t.Fatalf("missing session %s", result.Content)
	}
	result, err = executeCodexCommand(t.Context(), mgr, "", "write_stdin", map[string]any{"session_id": first.SessionID, "chars": "hello\n", "yield_time_ms": 1000}, safety.DefaultCommandPolicy())
	if err != nil {
		t.Fatal(err)
	}
	var last command.Result
	if err := json.Unmarshal([]byte(result.Content), &last); err != nil {
		t.Fatal(err)
	}
	if last.ExitCode == nil || *last.ExitCode != 0 || !strings.Contains(last.Output, "received:hello") {
		t.Fatalf("%+v", last)
	}
}
