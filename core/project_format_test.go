package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

// Project formatting must cross the shell/core boundary as one explicit
// resource method and return its presentation-neutral evidence.
func TestProjectFormatBridgeFormatsSavedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module bridge\n\ngo 1.22\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "main.go")
	if err := os.WriteFile(path, []byte("package bridge\n\nfunc  Run( )  int { return 1 }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	a := &actor{ws: ws}
	raw, err := testPayload(coreprotocol.ProjectFormatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	run, known := a.prepareResource(coreprotocol.Request{ID: "project-format", Method: coreprotocol.ProjectFormat, Payload: raw})
	if !known {
		t.Fatal("project.format is not a known resource method")
	}
	response := run(context.Background())
	if response.Error != nil {
		t.Fatal(response.Error)
	}
	var result workspace.FormatProjectResult
	if err := coreprotocol.Decode(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Changed != 1 || len(result.Files) != 1 || result.SafetySnapshotID == "" {
		t.Fatalf("project format result = %+v", result)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "package bridge\n\nfunc Run() int { return 1 }\n" {
		t.Fatalf("formatted file = %q, %v", data, err)
	}
}
