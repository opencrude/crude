package corewire

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestSSHArgumentsAndValidation(t *testing.T) {
	args, err := sshArgs(SSHOptions{Host: "example.org", User: "alice", Port: 2222, IdentityFile: "/tmp/a key"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "|")
	for _, required := range []string{"StrictHostKeyChecking=yes", "ClearAllForwardings=yes", "ForwardAgent=no", "PermitLocalCommand=no", "--|example.org|crude --core-stdio", "-i|/tmp/a key", "-p|2222"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("missing %s in %s", required, joined)
		}
	}
	for _, host := range []string{"-oProxyCommand=evil", "a;evil", "a b", "user@host", "a\n"} {
		if _, err := DialSSH(context.Background(), SSHOptions{Host: host}); err == nil {
			t.Fatalf("accepted %q", host)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DialSSH(ctx, SSHOptions{Host: "example.org"}); err == nil {
		t.Fatal("started cancelled SSH")
	}
}

func sshTestProgram(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable SSH fixture")
	}
	path := filepath.Join(t.TempDir(), "ssh")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSSHProcessRoundTripAndCancellationJoins(t *testing.T) {
	program := sshTestProgram(t, "exec cat\n")
	ctx, cancel := context.WithCancel(t.Context())
	conn, err := dialSSH(ctx, program, SSHOptions{Host: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("framed payload\n")); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len("framed payload\n"))
	if _, err := io.ReadFull(conn, got); err != nil || string(got) != "framed payload\n" {
		t.Fatalf("round trip %q: %v", got, err)
	}
	cancel()
	select {
	case <-conn.(*sshConn).done:
	case <-time.After(3 * time.Second):
		t.Fatal("owned SSH process not joined")
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestSSHProcessActionableBoundedDiagnostic(t *testing.T) {
	program := sshTestProgram(t, "printf 'Host key verification failed. Check known_hosts.\\n' >&2\nexit 255\n")
	conn, err := dialSSH(t.Context(), program, SSHOptions{Host: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = conn.Read(make([]byte, 1))
	if err == nil || !strings.Contains(err.Error(), "Host key verification failed") {
		t.Fatalf("lost actionable error: %v", err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	d := &sshDiagnostic{}
	_, _ = d.Write([]byte(strings.Repeat("x", 100000) + "\x1b\x00denied"))
	if len(d.String()) > 4096 || strings.ContainsAny(d.String(), "\x1b\x00") || !strings.HasSuffix(d.String(), "denied") {
		t.Fatalf("unbounded/unsafe diagnostics: %d", len(d.String()))
	}
}
