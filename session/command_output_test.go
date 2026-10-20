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
	"crude/tools"
)

func TestCommandFormattersDirectModelToRetainedContent(t *testing.T) {
	start := FormatCommandStart(fileio.AsyncCmdInfo{Handle: "build", PID: 42, Status: fileio.AsyncRunning, OutputBytes: 900})
	for _, want := range []string{"promoted to async command [build]", `"total_bytes":900`, `"status":"running"`, "read_async_output", "kill_async_command", "Do not rerun"} {
		if !strings.Contains(start, want) {
			t.Fatalf("start %q lacks %q", start, want)
		}
	}
	running := FormatCommandStatus(fileio.AsyncCmdInfo{Handle: "build", PID: 42, Status: fileio.AsyncRunning, OutputBytes: 900})
	if strings.Contains(running, "exit_code") || !strings.Contains(running, "Exit unknown") || !strings.Contains(running, "read_async_output") {
		t.Fatalf("running status misleads model: %q", running)
	}
	failed := FormatCommandStatus(fileio.AsyncCmdInfo{Handle: "build", PID: 42, Status: fileio.AsyncFailed, ExitCode: 7, Error: "exit status 7", OutputBytes: 900})
	for _, want := range []string{`"status":"failed"`, `"exit_code":7`, `"error":"exit status 7"`, `"total_bytes":900`} {
		if !strings.Contains(failed, want) {
			t.Fatalf("failed status %q lacks %q", failed, want)
		}
	}
	page := FormatCommandPage(fileio.OutputPage{Descriptor: fileio.OutputDescriptor{Handle: "build", Status: fileio.AsyncRunning, TotalBytes: 900}, SinceByte: 0, NextByte: 100, HasMore: true, Output: "chunk"})
	for _, want := range []string{`"since_byte":0`, `"next_byte":100`, "since_byte=100", "do not rerun", "chunk"} {
		if !strings.Contains(page, want) {
			t.Fatalf("page %q lacks %q", page, want)
		}
	}
	emptyRunning := FormatCommandPage(fileio.OutputPage{Descriptor: fileio.OutputDescriptor{Handle: "build", Status: fileio.AsyncRunning}})
	emptyFinished := FormatCommandPage(fileio.OutputPage{Descriptor: fileio.OutputDescriptor{Handle: "build", Status: fileio.AsyncCompleted}})
	if !strings.Contains(emptyRunning, "No new bytes yet") || !strings.Contains(emptyRunning, "still running") {
		t.Fatalf("running empty page=%q", emptyRunning)
	}
	if !strings.Contains(emptyFinished, "Output is complete") || strings.Contains(emptyFinished, "still running") {
		t.Fatalf("finished empty page=%q", emptyFinished)
	}
	binary := FormatCommandPage(fileio.OutputPage{Descriptor: fileio.OutputDescriptor{Handle: "binary", Status: fileio.AsyncCompleted, TotalBytes: 1}, SinceByte: 0, NextByte: 1, OutputEncoding: "base64", Output: "/w=="})
	for _, want := range []string{`"output_encoding":"base64"`, "Decode this returned page", "continue from next byte", "/w=="} {
		if !strings.Contains(binary, want) {
			t.Fatalf("binary page %q lacks %q", binary, want)
		}
	}
}

func commandOutputHelperCommand(mode string) string {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`set CRUDE_COMMAND_OUTPUT_HELPER=%s&&"%s" -test.run=^TestCommandOutputHelperProcess$`, mode, strings.ReplaceAll(os.Args[0], `"`, `""`))
	}
	return fmt.Sprintf("CRUDE_COMMAND_OUTPUT_HELPER=%s %q -test.run=^TestCommandOutputHelperProcess$", mode, os.Args[0])
}

func TestCommandOutputHelperProcess(t *testing.T) {
	switch os.Getenv("CRUDE_COMMAND_OUTPUT_HELPER") {
	case "partial":
		fmt.Fprint(os.Stdout, "partial stdout evidence\n")
		fmt.Fprint(os.Stderr, "partial stderr evidence\n")
		time.Sleep(30 * time.Second)
	case "large":
		fmt.Fprint(os.Stdout, strings.Repeat("x", fileio.DefaultOutputChunkBytes*3))
	}
}

func TestCancelledCommandOutcomePreservesPartialOutput(t *testing.T) {
	mgr := fileio.NewAsyncCommandManager()
	defer mgr.CloseAndWait()
	uc, err := mgr.StartUntracked(commandOutputHelperCommand("partial"), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for uc.Info().OutputBytes == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := awaitCommandWithPromotion(ctx, mgr, uc, nil, "")
	if err != nil {
		t.Fatalf("cancel result: %v", err)
	}
	for _, want := range []string{"Command cancelled.", "preserved the captured evidence", `"status":"killed"`, "partial stdout evidence", "partial stderr evidence"} {
		if !strings.Contains(result.Content, want) {
			t.Fatalf("cancel content %q lacks %q", result.Content, want)
		}
	}
	if evidence, ok := tools.ParseCommandResult("execute_command", result.Content); !ok || !evidence.Cancelled || evidence.Failed() || evidence.ExitCode != nil || evidence.OutputDescriptor.Handle != "" {
		t.Fatal("cancellation invented an exit or retained handle")
	}
}

func TestCommandOutcomeRetentionFailureIsExplicitAndDoesNotInventHandle(t *testing.T) {
	mgr := fileio.NewAsyncCommandManager()
	uc, err := mgr.StartUntracked(commandOutputHelperCommand("large"), t.TempDir(), 0)
	if err != nil {
		t.Fatal(err)
	}
	<-uc.Done()
	mgr.CloseAndWait()
	result, err := commandOutcome(mgr, uc)
	if err != nil {
		t.Fatal(err)
	}
	evidence, ok := tools.ParseCommandResult("execute_command", result.Content)
	if !ok || evidence.OutputDescriptor.Handle != "" || evidence.OutputDescriptor.RetentionError == "" {
		t.Fatal("retention failure not explicit or a handle was invented")
	}
	if strings.Contains(result.Content, "read_async_output(") {
		t.Fatalf("failed retention advertised a fake retrieval handle: %q", result.Content)
	}
}
