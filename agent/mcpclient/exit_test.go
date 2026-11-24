package mcpclient

import (
	"fmt"
	"os"
	"strings"
	"testing"

	captypes "crude/types/capability"
)

// A package runner installs its dependencies, then the server's own command
// line rejects its arguments and exits before speaking MCP.
func TestExitingServerHelper(t *testing.T) {
	if os.Getenv("CRUDE_MCP_EXIT_HELPER") != "exit-helper-enabled" {
		return
	}
	for i := range 40 {
		_, _ = fmt.Fprintf(os.Stderr, " + dependency-%02d==1.0.%d\n", i, i)
	}
	_, _ = fmt.Fprintln(os.Stderr, "Installed 40 packages in 1.2s")
	_, _ = fmt.Fprintln(os.Stderr, "Usage: windows-mcp [OPTIONS] COMMAND [ARGS]...")
	_, _ = fmt.Fprintln(os.Stderr, "Error: Missing command.")
	os.Exit(2)
}

// The status a person reads is the error's first line. When the server ended
// on its own, that line must carry the server's reason, not the protocol's
// report of a closed pipe; the tail and protocol detail follow it.
func TestExitedServerLeadsWithItsOwnReason(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRUDE_MCP_EXIT_HELPER", "exit-helper-enabled")
	manager := New(t.TempDir())
	defer manager.Close()
	manager.Configure(map[string]captypes.ConnectionConfig{"bare": {ID: "bare", Name: "Bare runner", Transport: "stdio",
		Command: executable, Args: []string{"-test.run=^TestExitingServerHelper$"}, LaunchApproved: true}})
	err = manager.Connect(t.Context(), "bare")
	if err == nil {
		t.Fatal("a server that exited was reported connected")
	}
	summary, detail, _ := strings.Cut(err.Error(), "\n")
	if summary != "server exited before it answered: Error: Missing command." {
		t.Fatalf("first line does not name the server's reason: %q", summary)
	}
	if !strings.Contains(detail, "Usage: windows-mcp") || !strings.Contains(detail, "Installed 40 packages") {
		t.Fatalf("server output was lost: %q", detail)
	}
}
