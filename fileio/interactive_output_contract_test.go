package fileio

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func interactiveContractCommand(mode string) (string, string) {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`set CRUDE_INTERACTIVE_CONTRACT_HELPER=%s&&"%s" -test.run=^TestInteractiveContractHelperProcess$`, mode, strings.ReplaceAll(os.Args[0], `"`, `""`)), "cmd.exe"
	}
	return fmt.Sprintf("CRUDE_INTERACTIVE_CONTRACT_HELPER=%s %q -test.run=^TestInteractiveContractHelperProcess$", mode, os.Args[0]), "/bin/sh"
}

func TestInteractiveContractHelperProcess(t *testing.T) {
	switch os.Getenv("CRUDE_INTERACTIVE_CONTRACT_HELPER") {
	case "large-fail":
		fmt.Fprint(os.Stdout, strings.Repeat("界", 12000))
		fmt.Fprint(os.Stderr, strings.Repeat("stderr-evidence-", 3000))
		os.Exit(7)
	case "empty":
		os.Exit(0)
	case "running-empty":
		time.Sleep(30 * time.Second)
	case "partial-block":
		fmt.Fprint(os.Stdout, "partial stdout before cancellation\n")
		fmt.Fprint(os.Stderr, "partial stderr before cancellation\n")
		time.Sleep(30 * time.Second)
	}
}

func TestInteractiveCancellationRetainsPartialEvidenceAndUnknownExit(t *testing.T) {
	mgr := NewAsyncCommandManager()
	defer mgr.CloseAndWait()
	command, shell := interactiveContractCommand("partial-block")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	result, err := mgr.StartInteractive(ctx, InteractiveOptions{Command: command, Shell: shell}, 5*time.Second, 1000)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v result=%+v", err, result)
	}
	if result.ExitCode != nil || result.OutputDescriptor.Status != AsyncKilled || result.OutputDescriptor.Handle == "" {
		t.Fatalf("cancelled result=%+v", result)
	}
	if !strings.Contains(result.Output, "partial stdout before cancellation") || !strings.Contains(result.Output, "partial stderr before cancellation") {
		t.Fatalf("partial evidence lost: %+v", result)
	}
	if !strings.Contains(result.Retrieval, "exit unavailable") || strings.Contains(result.Retrieval, "exit_code=-1") {
		t.Fatalf("retrieval=%q", result.Retrieval)
	}
	info, ok := mgr.Check(result.OutputDescriptor.Handle)
	if !ok || info.Command != command || info.Status != AsyncKilled {
		t.Fatalf("retained identity=%+v ok=%t", info, ok)
	}
}

func TestInteractiveOutputContractCrossPlatform(t *testing.T) {
	t.Run("large multibyte failure is byte bounded and pageable", func(t *testing.T) {
		mgr := NewAsyncCommandManager()
		defer mgr.CloseAndWait()
		command, shell := interactiveContractCommand("large-fail")
		result, err := mgr.StartInteractive(t.Context(), InteractiveOptions{Command: command, Shell: shell}, 5*time.Second, 100)
		if err != nil {
			t.Fatal(err)
		}
		if result.ExitCode == nil || *result.ExitCode != 7 || result.OutputDescriptor.Status != AsyncFailed {
			t.Fatalf("terminal result=%+v", result)
		}
		if len(result.Output) > 450 || !utf8.ValidString(result.Output) {
			t.Fatalf("visible output len=%d valid=%v", len(result.Output), utf8.ValidString(result.Output))
		}
		d := result.OutputDescriptor
		if d.TotalBytes <= MaxOutputChunkBytes || d.OmittedBytes <= 0 || len(d.ShownRanges) != 2 {
			t.Fatalf("descriptor=%+v", d)
		}
		firstOmitted := d.ShownRanges[0].EndByte
		for _, want := range []string{"status=failed", "exit_code=7", fmt.Sprintf("total_bytes=%d", d.TotalBytes), fmt.Sprintf("since_byte=%d", firstOmitted), fmt.Sprintf("max_bytes=%d", DefaultOutputChunkBytes), "do not rerun"} {
			if !strings.Contains(result.Retrieval, want) {
				t.Fatalf("retrieval %q lacks %q", result.Retrieval, want)
			}
		}
		page, err := mgr.ReadOutputPage(d.Handle, firstOmitted, DefaultOutputChunkBytes)
		if err != nil {
			t.Fatal(err)
		}
		visibleBytes := len(page.Output)
		if page.OutputEncoding == "base64" {
			decoded, decodeErr := base64.StdEncoding.DecodeString(page.Output)
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			visibleBytes = len(decoded)
		}
		if page.SinceByte != firstOmitted || visibleBytes > DefaultOutputChunkBytes || page.Descriptor.Status != AsyncFailed || !utf8.ValidString(page.Output) {
			t.Fatalf("page=%+v output_len=%d", page, len(page.Output))
		}
		encoded, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Index(string(encoded), `"retrieval"`) > strings.Index(string(encoded), `"output"`) {
			t.Fatalf("mandatory retrieval header follows output: %s", encoded)
		}
	})

	t.Run("running empty and finished empty are distinct", func(t *testing.T) {
		mgr := NewAsyncCommandManager()
		defer mgr.CloseAndWait()
		runningCommand, shell := interactiveContractCommand("running-empty")
		running, err := mgr.StartInteractive(t.Context(), InteractiveOptions{Command: runningCommand, Shell: shell}, 20*time.Millisecond, 100)
		if err != nil {
			t.Fatal(err)
		}
		if running.SessionID == 0 || running.ExitCode != nil || !strings.Contains(running.Retrieval, "No new bytes yet") {
			t.Fatalf("running=%+v", running)
		}
		if err := mgr.Kill(running.OutputDescriptor.Handle); err != nil {
			t.Fatal(err)
		}

		emptyCommand, shell := interactiveContractCommand("empty")
		finished, err := mgr.StartInteractive(context.Background(), InteractiveOptions{Command: emptyCommand, Shell: shell}, 5*time.Second, 100)
		if err != nil {
			t.Fatal(err)
		}
		if finished.ExitCode == nil || *finished.ExitCode != 0 || !strings.Contains(finished.Retrieval, "complete and empty") {
			t.Fatalf("finished=%+v", finished)
		}
	})
}
