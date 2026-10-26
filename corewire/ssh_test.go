package corewire

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

type failedSSHWrite struct{}

func (failedSSHWrite) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
func (failedSSHWrite) Close() error              { return nil }

// Model a failed negotiation write racing the process owner's final stderr
// capture. The transport must join that owner before freezing its error text.
func TestSSHWriteFailureJoinsDiagnosticCapture(t *testing.T) {
	diagnostic := &sshDiagnostic{}
	conn := &sshConn{in: failedSSHWrite{}, done: make(chan struct{}), diagnostic: diagnostic}
	// Process shutdown is already owned by another caller; Close must still
	// await done, including a concurrent write-error path.
	conn.once.Do(func() {})
	result := make(chan error, 1)
	started := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		close(started)
		_, err := conn.Write([]byte("hello"))
		result <- err
	}()
	<-started
	select {
	case err := <-result:
		close(conn.done)
		wg.Wait()
		t.Fatalf("write returned before stderr was captured: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	_, _ = diagnostic.Write([]byte("Host key verification failed. Repair known_hosts."))
	close(conn.done)
	wg.Wait()
	err := <-result
	if !errors.Is(err, io.ErrClosedPipe) || !strings.Contains(err.Error(), "Host key verification failed") {
		t.Fatalf("write lost transport cause or actionable diagnostic: %v", err)
	}
}

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

func TestSSHFastExitPreservesStdoutUntilRead(t *testing.T) {
	for _, payload := range []string{"", "matching image", strings.Repeat("framed payload\n", 128)} {
		t.Run(fmt.Sprintf("bytes=%d", len(payload)), func(t *testing.T) {
			program := sshTestProgram(t, "printf '%s' '"+payload+"'\n")
			conn, err := dialSSH(t.Context(), program, SSHOptions{Host: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			// Let normal process exit and Wait finish before the first read. This
			// deterministically exposes Wait closing a StdoutPipe prematurely.
			select {
			case <-conn.(*sshConn).done:
			case <-time.After(3 * time.Second):
				t.Fatal("fast SSH process did not exit before its stdout was read")
			}
			// Small reads also prove that the unread suffix survives process exit.
			got := make([]byte, len(payload))
			for offset := 0; offset < len(got); {
				n, err := conn.Read(got[offset : offset+1])
				if err != nil {
					t.Fatalf("stdout ended at %d/%d bytes: %v", offset+n, len(got), err)
				}
				offset += n
			}
			if string(got) != payload {
				t.Fatalf("stdout changed: got %q, want %q", got, payload)
			}
			if n, err := conn.Read(make([]byte, 1)); n != 0 || err != io.EOF {
				t.Fatalf("normal exit must end with EOF, got %d bytes and %v", n, err)
			}
		})
	}
}

func TestSSHUnreadStdoutShutdownJoins(t *testing.T) {
	// Produce more than the OS pipe can retain without ever draining stdout.
	// Shutdown must not wait on a stdout-copy goroutine blocked on its reader.
	program := sshTestProgram(t, "while :; do printf '%s' '"+strings.Repeat("x", 4096)+"'; done\n")
	for _, shutdown := range []string{"cancel", "close"} {
		t.Run(shutdown, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			conn, err := dialSSH(ctx, program, SSHOptions{Host: "fixture"})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = conn.Close() })
			select {
			case <-conn.(*sshConn).done:
				t.Fatal("stdout producer exited before shutdown")
			case <-time.After(20 * time.Millisecond):
			}
			closed := make(chan struct{})
			go func() {
				if shutdown == "cancel" {
					cancel()
					<-conn.(*sshConn).done
				}
				_ = conn.Close()
				close(closed)
			}()
			select {
			case <-closed:
			case <-time.After(3 * time.Second):
				t.Fatal("shutdown did not join SSH with unread stdout")
			}
			if conn.(*sshConn).cmd.ProcessState == nil {
				t.Fatal("SSH process was not reaped")
			}
		})
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

func sshHelperFixture(t *testing.T) (SSHOptions, string) {
	t.Helper()
	program := sshTestProgram(t, "for arg do command=$arg; done\nexec sh -c \"$command\"\n")
	t.Setenv("PATH", filepath.Dir(program)+string(os.PathListSeparator)+os.Getenv("PATH"))
	home := filepath.Join(t.TempDir(), "remote ' $HOME ; data")
	if err := os.MkdirAll(home, 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRUDE_HOME", home)
	return SSHOptions{Host: "fixture", User: "alice", IdentityFile: "/tmp/a key"}, home
}

func TestSSHInstallVerifiedImmutableAndPrivate(t *testing.T) {
	o, home := sshHelperFixture(t)
	host, err := ProbeSSHHost(t.Context(), o, "crude")
	if err != nil {
		t.Fatal(err)
	}
	if host.DataDir != filepath.Join(home, ".crude") {
		t.Fatalf("probe: %+v", host)
	}
	image := []byte("#!/bin/sh\nprintf 'matching image'\n")
	sum := fmt.Sprintf("%x", sha256.Sum256(image))
	cohort := strings.Repeat("c", 64) // Source identity is not the native image hash.
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, image, 0600); err != nil {
		t.Fatal(err)
	}
	install := func(digest string) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		return InstallSSHImage(t.Context(), o, "crude", cohort, digest, host, f)
	}
	bad := strings.Repeat("a", 64)
	if err := install(bad); err == nil || !strings.Contains(err.Error(), "digest mismatch") {
		t.Fatalf("bad digest: %v", err)
	}
	if _, err := os.Stat(filepath.Join(host.DataDir, "remote-bin", "crude-"+cohort)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("published bad image")
	}
	if err := install(sum); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(host.DataDir, "remote-bin", "crude-"+cohort)
	before, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if before.Mode().Perm() != 0700 {
		t.Fatalf("image mode: %s", before.Mode())
	}
	if err := install(sum); err != nil {
		t.Fatal(err)
	}
	after, _ := os.Stat(target)
	if !os.SameFile(before, after) {
		t.Fatal("replaced immutable executable inode")
	}
	got, err := os.ReadFile(target)
	if err != nil || string(got) != string(image) {
		t.Fatal("wrong image", err)
	}
	// The selector launches the explicitly installed image even though its digest
	// is unrelated to the source cohort. No content-hash name or PATH is needed.
	conn, err := DialSSHBuild(t.Context(), o, "crude", cohort)
	if err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(conn)
	_ = conn.Close()
	if err != nil || string(out) != "matching image" {
		t.Fatalf("cohort selector: %q %v", out, err)
	}
	// A different, fully verified image cannot replace the first native image for
	// the same cohort, even if both were explicitly reviewed.
	replacement := []byte("#!/bin/sh\nprintf 'another native image'\n")
	if err := os.WriteFile(path, replacement, 0600); err != nil {
		t.Fatal(err)
	}
	if err := install(fmt.Sprintf("%x", sha256.Sum256(replacement))); err == nil || !strings.Contains(err.Error(), "Immutable image conflict") {
		t.Fatalf("replaced cohort image: %v", err)
	}
	retained, _ := os.ReadFile(target)
	if string(retained) != string(image) {
		t.Fatal("conflicting install changed first image")
	}
	if err := os.WriteFile(path, image, 0600); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Dir(target))
	if len(entries) != 1 {
		t.Fatalf("staging not cleaned: %v", entries)
	}
	if err := os.WriteFile(target, []byte("existing unrelated image"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := install(sum); err == nil {
		t.Fatal("overwrote conflicting immutable image")
	}
	got, _ = os.ReadFile(target)
	if string(got) != "existing unrelated image" {
		t.Fatal("conflict lost data")
	}
}

func TestSSHInstallHostChangedAndArgumentsAreLiteral(t *testing.T) {
	o, _ := sshHelperFixture(t)
	host, err := ProbeSSHHost(t.Context(), o, "crude")
	if err != nil {
		t.Fatal(err)
	}
	host.DataDir += "/changed"
	path := filepath.Join(t.TempDir(), "image")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sum := fmt.Sprintf("%x", sha256.Sum256([]byte("x")))
	if err := InstallSSHImage(t.Context(), o, "crude", strings.Repeat("c", 64), sum, host, f); err == nil || !strings.Contains(err.Error(), "directory changed") {
		t.Fatalf("changed host: %v", err)
	}
	for _, value := range []string{"../escape", "x;touch /tmp/not-allowed", "$(id)", "x\n", strings.Repeat("a", 16), strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("A", 64), "*"} {
		if _, err := DialSSHBuild(t.Context(), o, "crude", value); err == nil {
			t.Fatalf("accepted build %q", value)
		}
		if err := InstallSSHImage(t.Context(), o, "crude", value, sum, host, f); err == nil || !strings.Contains(err.Error(), "invalid SSH install identity") {
			t.Fatalf("accepted install cohort %q: %v", value, err)
		}
	}
	for _, value := range []string{"../escape", "x;touch /tmp/not-allowed", "$(id)", "x\n", "*"} {
		if _, err := ProbeSSHHost(t.Context(), o, value); err == nil {
			t.Fatalf("accepted namespace %q", value)
		}
	}
	args, err := sshHelperArgs(o, sshProbeHost, "crude")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(args, "|"), "StrictHostKeyChecking=yes") || args[len(args)-2] != "fixture" {
		t.Fatal("helper weakened SSH authority")
	}
}

func TestSSHHelperCancellationJoinsTransfer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	program := sshTestProgram(t, "exec sleep 60\n")
	t.Setenv("PATH", filepath.Dir(program)+string(os.PathListSeparator)+os.Getenv("PATH"))
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := ProbeSSHHost(ctx, SSHOptions{Host: "fixture"}, "crude")
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("cancellation not joined: %v", err)
	}
	path := filepath.Join(t.TempDir(), "large-image")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(16 << 20); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	start = time.Now()
	err = InstallSSHImage(ctx, SSHOptions{Host: "fixture"}, "crude", strings.Repeat("c", 64), strings.Repeat("a", 64), SSHHost{OS: "Linux", Arch: "x86_64", DataDir: "/tmp/unreachable"}, f)
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > 2*time.Second {
		t.Fatalf("blocked transfer not joined: %v", err)
	}
}

func TestSSHBuildSelectionExecutesOnlyMatchingImage(t *testing.T) {
	o, home := sshHelperFixture(t)
	build := strings.Repeat("1234567890abcdef", 4)
	dir := filepath.Join(home, ".crude", "remote-bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "crude-"+build), []byte("#!/bin/sh\nprintf '%s' \"$1\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	args, err := sshHelperArgs(o, sshRunBuild, "crude", build)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), "ssh", args...)
	out, err := cmd.Output()
	if err != nil || string(out) != "--core-stdio" {
		t.Fatalf("selected image: %q %v", out, err)
	}
}
