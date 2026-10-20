package session

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/fileio"
	"crude/logger"
	"crude/tools"
)

// promotionHelperCommand re-executes this test binary so promotion tests use
// the same portable helper-process technique as fileio's async tests instead of
// depending on a POSIX shell.
func promotionHelperCommand(mode string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`set CRUDE_PROMOTION_HELPER=%s&&"%s" -test.run=^TestCommandPromotionHelperProcess$`,
			mode, strings.ReplaceAll(os.Args[0], `"`, `""`))
	}
	return fmt.Sprintf("CRUDE_PROMOTION_HELPER=%s %q -test.run=^TestCommandPromotionHelperProcess$", mode, os.Args[0])
}

func TestCommandPromotionHelperProcess(t *testing.T) {
	switch os.Getenv("CRUDE_PROMOTION_HELPER") {
	case "sleep":
		time.Sleep(30 * time.Second)
	case "echo":
		fmt.Println("fast helper output")
	case "fail":
		fmt.Fprintln(os.Stderr, "helper exploded")
		os.Exit(7)
	case "large":
		fmt.Print(strings.Repeat("A", fileio.DefaultOutputChunkBytes*4))
		fmt.Print("TAIL_MARKER")
	}
}

func setPromotionThreshold(t *testing.T, d time.Duration) {
	t.Helper()
	previous := asyncPromotionThreshold
	asyncPromotionThreshold = d
	t.Cleanup(func() { asyncPromotionThreshold = previous })
}

func promotionTestManager(t *testing.T) (*fileio.AsyncCommandManager, *logger.Logger) {
	t.Helper()
	lg, err := logger.New(logger.Options{HomeDir: t.TempDir(), SessionID: "promotion"})
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	t.Cleanup(func() { _ = lg.Close() })
	mgr := fileio.NewAsyncCommandManagerWithLogger(lg)
	t.Cleanup(mgr.CloseAndWait)
	return mgr, lg
}

func promotedHandle(t *testing.T, content string) string {
	t.Helper()
	result, ok := tools.ParseCommandResult("execute_command", content)
	if !ok || result.OutputDescriptor.Handle == "" {
		t.Fatal("promotion result carries no retained handle")
	}
	return result.OutputDescriptor.Handle
}

func TestAwaitCommandPromotesLongRunningCommand(t *testing.T) {
	setPromotionThreshold(t, 300*time.Millisecond)
	mgr, lg := promotionTestManager(t)

	uc, err := mgr.StartUntracked(promotionHelperCommand("sleep"), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if _, ok := mgr.Check("auto-1"); ok {
		t.Fatal("command was tracked before promotion")
	}

	result, err := awaitCommandWithPromotion(context.Background(), mgr, uc, lg, "")
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if !strings.Contains(result.Content, "promoted to async command") {
		t.Fatalf("content = %q, want a promotion notice", result.Content)
	}
	if !strings.Contains(result.Content, "check_async_command") || !strings.Contains(result.Content, "kill_async_command") {
		t.Fatalf("promotion notice does not tell the model how to manage the command: %q", result.Content)
	}

	handle := promotedHandle(t, result.Content)
	info, ok := mgr.Check(handle)
	if !ok {
		t.Fatalf("promoted handle %q is not tracked by the manager", handle)
	}
	if info.Status != fileio.AsyncRunning || info.PID != uc.PID() {
		t.Fatalf("promoted info = %+v, want running pid %d", info, uc.PID())
	}
	if out, _, err := mgr.ReadOutput(handle, 0); err != nil {
		t.Fatalf("read promoted output: %v", err)
	} else if out != "" {
		t.Fatalf("silent helper produced output: %q", out)
	}

	if err := mgr.Kill(handle); err != nil {
		t.Fatalf("kill promoted command: %v", err)
	}
	<-uc.Done()
	if got := uc.Status(); got != fileio.AsyncKilled {
		t.Fatalf("status=%s, want %s", got, fileio.AsyncKilled)
	}
}

func TestAwaitCommandReturnsFastCommandInline(t *testing.T) {
	// Generous threshold: the helper re-execs the test binary, which starts
	// slowly under the race detector and on loaded machines.
	setPromotionThreshold(t, 5*time.Second)
	mgr, lg := promotionTestManager(t)

	uc, err := mgr.StartUntracked(promotionHelperCommand("echo"), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	result, err := awaitCommandWithPromotion(context.Background(), mgr, uc, lg, "")
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if !strings.Contains(result.Content, "fast helper output") {
		t.Fatalf("content = %q, want the command output inline", result.Content)
	}
	if strings.Contains(result.Content, "promoted to async") {
		t.Fatalf("fast command must not be promoted: %q", result.Content)
	}
	if list := mgr.List(); len(list) != 0 {
		t.Fatalf("fast command left async handles behind: %+v", list)
	}
}

func TestAwaitCommandBoundsLargeCompletedOutputAndRetainsHandle(t *testing.T) {
	setPromotionThreshold(t, 5*time.Second)
	mgr, lg := promotionTestManager(t)

	uc, err := mgr.StartUntracked(promotionHelperCommand("large"), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	result, err := awaitCommandWithPromotion(context.Background(), mgr, uc, lg, "")
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if len(result.Content) > fileio.DefaultOutputChunkBytes+4096 {
		t.Fatalf("large command flooded inline context: %d bytes", len(result.Content))
	}
	if !strings.Contains(result.Content, "TAIL_MARKER") ||
		!strings.Contains(result.Content, `"handle":"auto-`) ||
		!strings.Contains(result.Content, "First omitted cursor: read_async_output") {
		t.Fatal("bounded result lost tail or retained handle")
	}
	handle := promotedHandle(t, result.Content)
	page, next, total, err := mgr.ReadOutputLimit(handle, 0, 1024)
	if err != nil {
		t.Fatal(err)
	}
	if len(page) != 1024 || next != 1024 || total <= fileio.DefaultOutputChunkBytes {
		t.Fatalf("retained output page len=%d next=%d total=%d", len(page), next, total)
	}
}

func TestAwaitCommandTimeoutKillsInsteadOfPromoting(t *testing.T) {
	setPromotionThreshold(t, 10*time.Second)
	mgr, lg := promotionTestManager(t)

	uc, err := mgr.StartUntracked(promotionHelperCommand("sleep"), t.TempDir(), 1)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	started := time.Now()
	result, err := awaitCommandWithPromotion(context.Background(), mgr, uc, lg, "")
	if err == nil {
		t.Fatalf("timed out command reported success: %q", result.Content)
	}
	if !strings.Contains(result.Content, "timed out") {
		t.Fatalf("content = %q, want a timeout failure", result.Content)
	}
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Fatalf("timeout was not enforced: %s", elapsed)
	}
	if list := mgr.List(); len(list) != 0 {
		t.Fatalf("killed command was promoted: %+v", list)
	}
}

func TestAwaitCommandFailurePropagatesResult(t *testing.T) {
	setPromotionThreshold(t, 10*time.Second)
	mgr, lg := promotionTestManager(t)

	uc, err := mgr.StartUntracked(promotionHelperCommand("fail"), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	result, err := awaitCommandWithPromotion(context.Background(), mgr, uc, lg, "")
	if err == nil {
		t.Fatalf("failing command reported success: %q", result.Content)
	}
	if !strings.Contains(result.Content, `"status":"failed"`) || !strings.Contains(result.Content, "helper exploded") {
		t.Fatalf("content = %q, want the failure plus captured stderr", result.Content)
	}
}

func TestAwaitCommandCancelsWithTurnContext(t *testing.T) {
	setPromotionThreshold(t, 10*time.Second)
	mgr, lg := promotionTestManager(t)

	uc, err := mgr.StartUntracked(promotionHelperCommand("sleep"), t.TempDir(), 0)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(150 * time.Millisecond)
		cancel()
	}()

	result, err := awaitCommandWithPromotion(ctx, mgr, uc, lg, "")
	if err != nil {
		t.Fatalf("await: %v", err)
	}
	if !strings.Contains(result.Content, "Command cancelled.") {
		t.Fatalf("content = %q, want a cancellation notice", result.Content)
	}
	<-uc.Done()
	if got := uc.Status(); got != fileio.AsyncKilled {
		t.Fatalf("cancelled command status=%s, want %s", got, fileio.AsyncKilled)
	}
}
