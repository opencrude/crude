package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSSHInvocationPathsAndNamespace(t *testing.T) {
	remote := CLIFlags{SSHHost: "dev-alias", SSHUser: "alice", SSHPort: 2222, Workspace: "relative remote path;$(not-a-command)"}
	root, err := invocationRoot(remote)
	if err != nil || root != remote.Workspace {
		t.Fatalf("remote path was locally resolved: %q %v", root, err)
	}
	local, err := invocationRoot(CLIFlags{Workspace: "relative-local"})
	if err != nil || !filepath.IsAbs(local) {
		t.Fatalf("local path %q: %v", local, err)
	}
	if draftNamespace(remote, "same-id") == "same-id" {
		t.Fatal("remote drafts alias local drafts")
	}
	other := remote
	other.SSHHost = "other"
	if draftNamespace(remote, "same-id") == draftNamespace(other, "same-id") {
		t.Fatal("remote hosts share drafts")
	}
	for _, flags := range []CLIFlags{{SSHUser: "alice"}, {SSHHost: "host"}, {SSHHost: "host", Workspace: "/remote", Core: true}, {SSHHost: "host", Workspace: "/remote", ListSessions: true}, {APIKey: "secret"}} {
		if err := validateConnectedInvocation(flags); !errors.Is(err, ErrInvocationConfiguration) {
			t.Fatalf("invalid route accepted: %+v %v", flags, err)
		}
	}
	if err := validateConnectedInvocation(CLIFlags{Account: "chosen", Model: "explicit", Provider: "openai", APIKey: "secret", SystemPrompt: "extra"}); err != nil {
		t.Fatal(err)
	}
}

func TestSSHCLIDialerReportsHostKeyFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable SSH fixture")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ssh"), []byte("#!/bin/sh\nprintf 'Host key verification failed. Repair known_hosts.\\n' >&2\nexit 255\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	client, err := shellDialer(t.TempDir(), CLIFlags{SSHHost: "fixture", Workspace: "/remote"})(context.Background(), "test-client")
	if client != nil {
		_ = client.Close()
		t.Fatal("unexpected negotiated client")
	}
	if err == nil || !strings.Contains(err.Error(), "Host key verification failed") {
		t.Fatalf("CLI lost diagnostic: %v", err)
	}
}
