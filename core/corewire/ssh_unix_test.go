//go:build !windows

package corewire

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFakeSSHCancellationJoins(t *testing.T) {
	program := filepath.Join(t.TempDir(), "ssh")
	argsFile := filepath.Join(t.TempDir(), "arguments")
	t.Setenv("COREWIRE_TEST_ARGS", argsFile)
	if err := os.WriteFile(program, []byte("#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$COREWIRE_TEST_ARGS\"\nexec cat\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rwc, err := dialSSH(ctx, program, SSHOptions{Host: "fake"})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _, _ = io.WriteString(rwc, "hello") }()
	b := make([]byte, 5)
	if _, err = io.ReadFull(rwc, b); err != nil || string(b) != "hello" {
		t.Fatalf("%q %v", b, err)
	}
	args, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(args), "--\nfake\ncrude --core-stdio\n") || !strings.Contains(string(args), "StrictHostKeyChecking=yes\n") {
		t.Fatalf("unsafe actual arguments: %s", args)
	}
	cancel()
	done := make(chan struct{})
	go func() { _ = rwc.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("SSH not joined")
	}
	c := rwc.(*sshConn)
	select {
	case <-c.done:
	default:
		t.Fatal("process still active")
	}
}
