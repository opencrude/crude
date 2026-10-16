package core

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

func coreGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.name=T", "-c", "user.email=t@example.test", "-c", "init.defaultBranch=main"}, args...)...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s", args, out)
	}
}

// A connected shell resolves a pull conflict end to end through the core's
// service RPCs; every repository-changing call is journaled as a mutation.
func TestGitRemoteAndConflictServicesOverCore(t *testing.T) {
	base := t.TempDir()
	origin, theirs, ours := filepath.Join(base, "origin.git"), filepath.Join(base, "theirs"), filepath.Join(base, "ours")
	coreGit(t, base, "init", "--bare", "-b", "main", origin)
	coreGit(t, base, "clone", origin, theirs)
	write := func(dir, text string) {
		if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(theirs, "x\n")
	coreGit(t, theirs, "add", ".")
	coreGit(t, theirs, "commit", "-m", "base")
	coreGit(t, theirs, "push", "-u", "origin", "main")
	coreGit(t, base, "clone", origin, ours)
	for _, dir := range []string{theirs, ours} {
		coreGit(t, dir, "config", "user.name", "T")
		coreGit(t, dir, "config", "user.email", "t@example.test")
	}
	write(theirs, "theirs\n")
	coreGit(t, theirs, "commit", "-am", "theirs")
	coreGit(t, theirs, "push")
	write(ours, "ours\n")
	coreGit(t, ours, "commit", "-am", "ours")

	ws := workspace.NewWorkspace(ours, "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	if ws.Git == nil {
		ws.Git = workspace.NewGitService(ours)
	}
	if ws.LocalHistory == nil {
		t.Skip("Local History is not configured for this workspace")
	}
	a := &actor{ws: ws}
	call := func(method string, in, out any) {
		t.Helper()
		raw, _ := json.Marshal(in)
		run, ok := a.prepareService(coreprotocol.Request{ID: method, Method: method, Payload: raw})
		if !ok {
			t.Fatalf("%s is not routed", method)
		}
		response := run(context.Background())
		if response.Error != nil {
			t.Fatalf("%s: %+v", method, response.Error)
		}
		if out != nil {
			if err := json.Unmarshal(response.Payload, out); err != nil {
				t.Fatal(err)
			}
		}
		if method != coreprotocol.GitConflictFile && !serviceMutation(method) {
			t.Fatalf("%s changes the repository but is not journaled", method)
		}
	}
	var pulled coreprotocol.GitRemoteResult
	call(coreprotocol.GitPull, coreprotocol.GitRemoteRequest{}, &pulled)
	if len(pulled.Sync.Conflicts) != 1 {
		t.Fatalf("pull = %+v", pulled)
	}
	var file coreprotocol.GitConflictFileResult
	call(coreprotocol.GitConflictFile, coreprotocol.GitConflictFileRequest{Path: "a.txt"}, &file)
	call(coreprotocol.ResolveGitConflict, coreprotocol.ResolveGitConflictRequest{Path: "a.txt", Fingerprint: file.File.Fingerprint, Choices: []workspace.GitConflictChoice{workspace.GitChooseIncoming}}, nil)
	call(coreprotocol.GitContinueOperation, coreprotocol.GitRemoteRequest{}, nil)
	call(coreprotocol.GitPush, coreprotocol.GitRemoteRequest{}, nil)
	if data, _ := os.ReadFile(filepath.Join(ours, "a.txt")); strings.TrimSpace(string(data)) != "theirs" {
		t.Fatalf("resolved = %q", data)
	}
}
