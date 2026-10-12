package app

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/internal/testhome"
)

// TestMain lets this test binary act as the crude executable: spawnCore and
// the helpers below re-execute it with core flags.
func TestMain(m *testing.M) {
	if os.Getenv("CRUDE_APP_TEST_MAIN") == "1" {
		os.Exit(Main())
	}
	os.Exit(testhome.Run(m))
}

// coreTestHome isolates every core path (socket, config, sessions, logs). Unix
// socket paths are length-limited, so Unix homes live directly under /tmp.
func coreTestHome(t *testing.T) string {
	t.Helper()
	parent := "/tmp"
	if runtime.GOOS == "windows" {
		parent = ""
	}
	home, err := os.MkdirTemp(parent, "crc")
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_APP_TEST_MAIN", "1")
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = StopCore(ctx, home, true)
		waitCoreGone(t, home, 15*time.Second)
		_ = os.RemoveAll(home)
	})
	return home
}

func waitCoreGone(t *testing.T, home string, limit time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(limit)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := CoreStatus(ctx, home)
		cancel()
		if err != nil {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

func coreListenCount(t *testing.T, home string) int {
	t.Helper()
	raw, err := os.ReadFile(CoreLogPath(home))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "[CORE] listening")
}

func attach(t *testing.T, client *coreclient.Client, root string) coreprotocol.AttachResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var result coreprotocol.AttachResult
	if err := client.Call(ctx, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &result); err != nil {
		t.Fatalf("attach %s: %v", root, err)
	}
	return result
}

func TestConnectCoreAutostartConvergesOnOneOwner(t *testing.T) {
	home := coreTestHome(t)
	const shells = 6
	clients := make([]*coreclient.Client, shells)
	errs := make([]error, shells)
	var wg sync.WaitGroup
	for i := range shells {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			clients[i], errs[i] = ConnectCore(ctx, home, core.NewClientID())
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("shell %d: %v", i, err)
		}
		defer func() { _ = clients[i].Close() }()
	}
	if n := coreListenCount(t, home); n != 1 {
		t.Fatalf("expected exactly one core to own the endpoint, %d listened", n)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	status, err := CoreStatus(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	if status.Clients != shells {
		t.Fatalf("core sees %d shells, want %d", status.Clients, shells)
	}
}

func TestSecondShellSharesWorkspaceAndSurvivesFirstShellExit(t *testing.T) {
	home := coreTestHome(t)
	root := filepath.Join(home, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	first, err := ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	second, err := ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = second.Close() }()
	a, b := attach(t, first, root), attach(t, second, root)
	if a.WorkspaceID == "" || a.WorkspaceID != b.WorkspaceID {
		t.Fatalf("shells attached different workspaces: %q vs %q", a.WorkspaceID, b.WorkspaceID)
	}
	status, err := CoreStatus(ctx, home)
	if err != nil {
		t.Fatal(err)
	}
	if len(status.Workspaces) != 1 {
		t.Fatalf("one project must have one retained workspace, got %d", len(status.Workspaces))
	}

	_ = first.Close()
	var snapshot coreprotocol.WorkspaceSnapshot
	if err := second.Call(ctx, b.WorkspaceID, coreprotocol.Snapshot, struct{}{}, &snapshot); err != nil {
		t.Fatalf("remaining shell lost its workspace after the first exited: %v", err)
	}
	if snapshot.ID != b.WorkspaceID {
		t.Fatalf("snapshot for %q, want %q", snapshot.ID, b.WorkspaceID)
	}
	if status, err = CoreStatus(ctx, home); err != nil || status.Clients != 1 || len(status.Workspaces) != 1 {
		t.Fatalf("after first shell exit: status=%+v err=%v", status, err)
	}
}

func TestCoreStopRefusesAttachedShellsUnlessForced(t *testing.T) {
	home := coreTestHome(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	shell, err := ConnectCore(ctx, home, core.NewClientID())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = shell.Close() }()
	result, err := StopCore(ctx, home, false)
	if err != nil {
		t.Fatal(err)
	}
	if result.Accepted || result.Shells != 1 {
		t.Fatalf("stop with an attached shell must be refused: %+v", result)
	}
	if result, err = StopCore(ctx, home, true); err != nil || !result.Accepted {
		t.Fatalf("forced stop: %+v %v", result, err)
	}
	if !waitCoreGone(t, home, 15*time.Second) {
		t.Fatal("core still answering after accepted stop")
	}
}

func TestIdleCoreExitsAfterGraceAndServerModeStays(t *testing.T) {
	home := coreTestHome(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	idle := exec.Command(exe, "--core", "--core-grace", "500ms")
	if err := idle.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- idle.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("idle core exit: %v", err)
		}
	case <-time.After(20 * time.Second):
		_ = idle.Process.Kill()
		t.Fatal("idle core did not exit after its grace period")
	}

	server := exec.Command(exe, "--core", "--server", "--core-grace", "200ms")
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.Wait() }()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err := CoreStatus(ctx, home)
		cancel()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("server core never listened: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	status, err := CoreStatus(ctx, home)
	if err != nil || !status.Server {
		t.Fatalf("server core must stay up with zero shells: %+v %v", status, err)
	}
}

func TestCoreStdioRelaysFramedConnection(t *testing.T) {
	home := coreTestHome(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	relay := exec.Command(exe, "--core-stdio")
	stdin, err := relay.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := relay.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, _ := relay.StderrPipe()
	if err := relay.Start(); err != nil {
		t.Fatal(err)
	}
	go func() {
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			t.Log("relay:", scanner.Text())
		}
	}()
	defer func() { _ = relay.Process.Kill(); _ = relay.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	client, err := coreclient.Dial(ctx, stdioConn{Reader: stdout, WriteCloser: stdin}, core.NewClientID())
	if err != nil {
		t.Fatalf("dial through stdio relay: %v", err)
	}
	defer func() { _ = client.Close() }()
	root := filepath.Join(home, "remote-project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if result := attach(t, client, root); result.WorkspaceID == "" {
		t.Fatal("attach through relay returned no workspace")
	}
}

type stdioConn struct {
	io.Reader
	io.WriteCloser
}
