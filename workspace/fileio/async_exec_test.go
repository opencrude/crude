package fileio

import (
	"bufio"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"
)

func asyncHelperCommand() string {
	command := fmt.Sprintf(`CRUDE_ASYNC_HELPER=1 %q -test.run=^TestAsyncCommandHelperProcess$`, os.Args[0])
	if runtime.GOOS == "windows" {
		command = fmt.Sprintf(`set CRUDE_ASYNC_HELPER=1&&"%s" -test.run=^TestAsyncCommandHelperProcess$`, strings.ReplaceAll(os.Args[0], `"`, `""`))
	}
	return command
}

func TestAsyncCommandHelperProcess(t *testing.T) {
	if os.Getenv("CRUDE_ASYNC_HELPER") != "1" {
		return
	}
	switch os.Getenv("CRUDE_ASYNC_ACTION") {
	case "large":
		_, _ = os.Stdout.WriteString(strings.Repeat("O", DefaultOutputChunkBytes+137))
		_, _ = os.Stderr.WriteString(strings.Repeat("E", DefaultOutputChunkBytes+211))
		return
	case "fail":
		_, _ = os.Stdout.WriteString("partial stdout\n")
		_, _ = os.Stderr.WriteString("partial stderr\n")
		os.Exit(7)
	case "utf8-binary":
		_, _ = os.Stdout.Write([]byte("A界B\xffC"))
		return
	case "detached-parent":
		child := exec.Command(os.Args[0], "-test.run=^TestAsyncCommandHelperProcess$")
		child.Env = append(os.Environ(), "CRUDE_ASYNC_ACTION=detached-child")
		ready, err := child.StdoutPipe()
		if err != nil {
			os.Exit(2)
		}
		child.Stderr = os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(2)
		}
		// The shell must outlive its child's actual connection to the test.
		// Start() or a guessed startup delay cannot establish that boundary.
		line, err := bufio.NewReader(ready).ReadString('\n')
		_ = ready.Close()
		_ = child.Process.Release()
		if err != nil || line != "ready\n" {
			os.Exit(3)
		}
		return
	case "detached-child":
		gate, err := net.DialTimeout("tcp", os.Getenv("CRUDE_ASYNC_GATE"), 5*time.Second)
		if err != nil {
			os.Exit(4)
		}
		defer gate.Close()
		if err := gate.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
			os.Exit(4)
		}
		if _, err := io.WriteString(gate, "ready\n"); err != nil {
			os.Exit(4)
		}
		_, _ = io.WriteString(os.Stdout, "ready\n")
		line, err := bufio.NewReader(gate).ReadString('\n')
		if err != nil || line != "write\n" {
			os.Exit(5)
		}
		if err := os.WriteFile(os.Getenv("CRUDE_ASYNC_TARGET"), []byte("late"), 0600); err != nil {
			os.Exit(5)
		}
		_, _ = io.WriteString(gate, "wrote\n")
		return
	}
	time.Sleep(30 * time.Second)
}

func TestRetainedOutputPagePreservesExactRawIntervalsWithValidPayload(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	if _, err := manager.Start("utf8-binary", asyncActionCommand("utf8-binary"), t.TempDir(), 10); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, ok := manager.Check("utf8-binary")
		if !ok {
			t.Fatal("command disappeared")
		}
		if info.Status != AsyncRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	want := []byte("A界B\xffC")
	var rebuilt []byte
	for cursor := 0; cursor < len(want); {
		page, err := manager.ReadOutputPage("utf8-binary", cursor, 1)
		if err != nil {
			t.Fatal(err)
		}
		if !utf8.ValidString(page.Output) || page.NextByte != cursor+1 || len(page.Descriptor.ShownRanges) != 1 || page.Descriptor.ShownRanges[0] != (OutputRange{StartByte: cursor, EndByte: cursor + 1}) {
			t.Fatalf("page at %d = %+v valid=%t", cursor, page, utf8.ValidString(page.Output))
		}
		payload := []byte(page.Output)
		if page.OutputEncoding == "base64" {
			payload, err = base64.StdEncoding.DecodeString(page.Output)
			if err != nil {
				t.Fatal(err)
			}
		} else if page.OutputEncoding != "" {
			t.Fatalf("unexpected encoding %q", page.OutputEncoding)
		}
		rebuilt = append(rebuilt, payload...)
		cursor = page.NextByte
	}
	if string(rebuilt) != string(want) {
		t.Fatalf("rebuilt=% x want=% x", rebuilt, want)
	}
}

func asyncActionCommand(action string) string {
	command := fmt.Sprintf(`CRUDE_ASYNC_HELPER=1 CRUDE_ASYNC_ACTION=%s %q -test.run=^TestAsyncCommandHelperProcess$`, action, os.Args[0])
	if runtime.GOOS == "windows" {
		command = fmt.Sprintf(`set CRUDE_ASYNC_HELPER=1&&set CRUDE_ASYNC_ACTION=%s&&"%s" -test.run=^TestAsyncCommandHelperProcess$`, action, strings.ReplaceAll(os.Args[0], `"`, `""`))
	}
	return command
}

func TestAsyncCommandCloseKillsAndJoinsProcess(t *testing.T) {
	manager := NewAsyncCommandManager()
	if _, err := manager.Start("liveness", asyncHelperCommand(), t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	if !manager.Running() {
		t.Fatal("started command was not included in the activity gate")
	}
	started := time.Now()
	manager.CloseAndWait()
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("command tree did not terminate promptly: %s", elapsed)
	}
	if manager.Running() {
		t.Fatal("command remained active after CloseAndWait")
	}
}

func TestConcurrentDuplicateHandleStartsOnlyOneProcess(t *testing.T) {
	manager := NewAsyncCommandManager()
	var wg sync.WaitGroup
	results := make(chan error, 2)
	start := make(chan struct{})
	for range 2 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := manager.Start("same", asyncHelperCommand(), t.TempDir(), 0)
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	succeeded := 0
	for err := range results {
		if err == nil {
			succeeded++
		}
	}
	if succeeded != 1 {
		t.Fatalf("successful starts=%d, want 1", succeeded)
	}
	manager.CloseAndWait()
}

func TestCompletedShellCannotLeaveDetachedWriter(t *testing.T) {
	manager := NewAsyncCommandManager()
	settled := settleObserver(t, manager)
	dir := t.TempDir()
	target := filepath.Join(dir, "late")
	gate, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer gate.Close()
	if err := gate.(*net.TCPListener).SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRUDE_ASYNC_TARGET", target)
	t.Setenv("CRUDE_ASYNC_GATE", gate.Addr().String())
	if _, err := manager.Start("detached", asyncActionCommand("detached-parent"), dir, 0); err != nil {
		t.Fatal(err)
	}
	child, err := gate.Accept()
	if err != nil {
		t.Fatal("descendant never connected:", err)
	}
	defer child.Close()
	if err := child.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(child)
	if ready, err := reader.ReadString('\n'); err != nil || ready != "ready\n" {
		t.Fatalf("descendant did not reach its write barrier: %q, %v", ready, err)
	}
	info := awaitSettled(t, settled)
	if info.Handle != "detached" || info.Status != AsyncCompleted || manager.Running() {
		t.Fatalf("shell did not complete after its descendant reached the barrier: %+v", info)
	}
	// A surviving descendant would write immediately on this signal. A real
	// ownership teardown closes the connection, so no elapsed-time guess is
	// needed to establish that it cannot act after command completion.
	_, _ = io.WriteString(child, "write\n")
	if response, err := reader.ReadString('\n'); err == nil || response != "" {
		t.Fatalf("descendant survived command settlement: %q, %v", response, err)
	} else if timeout, ok := err.(net.Error); ok && timeout.Timeout() {
		t.Fatal("descendant remained live after command settlement:", err)
	}
	if _, err := os.Lstat(target); !os.IsNotExist(err) {
		t.Fatalf("detached child survived command ownership: %v", err)
	}
}

func TestNextAutoHandleSkipsLiveHandles(t *testing.T) {
	manager := NewAsyncCommandManager()
	if _, err := manager.Start("auto-1", asyncHelperCommand(), t.TempDir(), 0); err != nil {
		t.Fatal(err)
	}
	if handle := manager.NextAutoHandle(); handle != "auto-2" {
		t.Fatalf("handle=%q, want auto-2 because auto-1 is already in use", handle)
	}
	manager.CloseAndWait()
}

func TestPromoteRegistersRunningCommandUnderNewHandle(t *testing.T) {
	manager := NewAsyncCommandManager()
	uc, err := manager.StartUntracked(asyncHelperCommand(), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	handle := manager.NextAutoHandle()
	info, err := manager.Promote(uc, handle)
	if err != nil {
		t.Fatalf("promote: %v", err)
	}
	if info.Handle != handle || info.Status != AsyncRunning || info.PID != uc.PID() {
		t.Fatalf("promoted info = %+v, want handle %q running with pid %d", info, handle, uc.PID())
	}
	if _, ok := manager.Check(handle); !ok {
		t.Fatal("promoted handle is not addressable through the manager")
	}
	if !manager.Running() {
		t.Fatal("promoted command is not covered by the activity gate")
	}
	if _, err := manager.Promote(uc, handle); err == nil {
		t.Fatal("re-promoting an in-use handle must fail")
	}
	if err := manager.Kill(handle); err != nil {
		t.Fatalf("kill: %v", err)
	}
	<-uc.Done()
	if got := uc.Status(); got != AsyncKilled {
		t.Fatalf("status=%s, want %s", got, AsyncKilled)
	}
	manager.CloseAndWait()
}

func TestAgentCommandInheritsConfiguredCrossPlatformTempDirectory(t *testing.T) {
	manager := NewAsyncCommandManager()
	tempDir := filepath.Join(t.TempDir(), "agent-temp")
	if err := manager.SetTempDir(tempDir); err != nil {
		t.Fatal(err)
	}
	command := `printf "%s" "$CRUDE_TEMP_DIR"`
	if runtime.GOOS == "windows" {
		command = `echo %CRUDE_TEMP_DIR%`
	}
	if _, err := manager.Start("temp-env", command, t.TempDir(), 10); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		info, ok := manager.Check("temp-env")
		if !ok {
			t.Fatal("command disappeared")
		}
		if info.Status != AsyncRunning {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	output, _, _, err := manager.ReadOutputLimit("temp-env", 0, DefaultOutputChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(output) != tempDir {
		t.Fatalf("CRUDE_TEMP_DIR=%q want %q", strings.TrimSpace(output), tempDir)
	}
	manager.CloseAndWait()
}

func TestRetainedOutputPageReportsExactRangesStatusAndCursor(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	if _, err := manager.Start("large", asyncActionCommand("large"), t.TempDir(), 10); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	totalBytes := 0
	for {
		info, ok := manager.Check("large")
		if !ok {
			t.Fatal("command disappeared")
		}
		if info.Status != AsyncRunning {
			if info.Status != AsyncCompleted || info.ExitCode != 0 {
				t.Fatalf("terminal info=%+v", info)
			}
			if info.OutputBytes < DefaultOutputChunkBytes*2+348 {
				t.Fatalf("output bytes=%d", info.OutputBytes)
			}
			totalBytes = info.OutputBytes
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("command did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}

	page, err := manager.ReadOutputPage("large", 11, 97)
	if err != nil {
		t.Fatal(err)
	}
	if page.SinceByte != 11 || page.NextByte != 108 || len(page.Output) != 97 || !page.HasMore ||
		page.Descriptor.Status != AsyncCompleted || page.Descriptor.TotalBytes != totalBytes ||
		len(page.Descriptor.ShownRanges) != 1 || page.Descriptor.ShownRanges[0] != (OutputRange{StartByte: 11, EndByte: 108}) {
		t.Fatalf("page=%+v", page)
	}

	finished, err := manager.ReadOutputPage("large", page.Descriptor.TotalBytes, 10)
	if err != nil {
		t.Fatal(err)
	}
	if finished.Output != "" || finished.HasMore || finished.NextByte != page.Descriptor.TotalBytes || finished.Descriptor.Status != AsyncCompleted {
		t.Fatalf("finished page=%+v", finished)
	}
	if _, err := manager.ReadOutputPage("missing", 0, 10); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing handle error=%v", err)
	}
	if _, err := manager.ReadOutputPage("large", -1, 10); err == nil || !strings.Contains(err.Error(), "negative") {
		t.Fatalf("negative cursor error=%v", err)
	}
}

func TestFailedCommandRetainsPartialOutputAndExitIdentity(t *testing.T) {
	manager := NewAsyncCommandManager()
	defer manager.CloseAndWait()
	if _, err := manager.Start("failed", asyncActionCommand("fail"), t.TempDir(), 10); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	var info *AsyncCmdInfo
	for time.Now().Before(deadline) {
		info, _ = manager.Check("failed")
		if info != nil && info.Status != AsyncRunning {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if info == nil || info.Status != AsyncFailed || info.ExitCode != 7 || info.OutputBytes == 0 {
		t.Fatalf("failed info=%+v", info)
	}
	page, err := manager.ReadOutputPage("failed", 0, DefaultOutputChunkBytes)
	if err != nil {
		t.Fatal(err)
	}
	if page.Descriptor.Status != AsyncFailed || !strings.Contains(page.Output, "partial stdout") || !strings.Contains(page.Output, "partial stderr") {
		t.Fatalf("failed page=%+v", page)
	}
	manager.Cleanup("failed")
	if _, err := manager.ReadOutputPage("failed", 0, 10); err == nil {
		t.Fatal("cleaned handle remained readable")
	}
}
