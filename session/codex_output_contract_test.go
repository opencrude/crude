package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	"crude/fileio"
	"crude/safety"
)

func TestCodexOutputContractHelperProcess(t *testing.T) {
	if os.Getenv("CRUDE_CODEX_OUTPUT_HELPER") != "1" {
		return
	}
	fmt.Fprint(os.Stdout, "codex partial stdout\n")
	fmt.Fprint(os.Stderr, "codex partial stderr\n")
	time.Sleep(30 * time.Second)
}

func codexOutputContractCommand() (string, string) {
	if runtime.GOOS == "windows" {
		return fmt.Sprintf(`set CRUDE_CODEX_OUTPUT_HELPER=1&&"%s" -test.run=^TestCodexOutputContractHelperProcess$`, strings.ReplaceAll(os.Args[0], `"`, `""`)), "cmd.exe"
	}
	return fmt.Sprintf("CRUDE_CODEX_OUTPUT_HELPER=1 %q -test.run=^TestCodexOutputContractHelperProcess$", os.Args[0]), "/bin/sh"
}

func TestExecuteCodexCommandCancellationReturnsPartialEvidenceWithoutSyntheticExit(t *testing.T) {
	mgr := fileio.NewAsyncCommandManager()
	defer mgr.CloseAndWait()
	command, shell := codexOutputContractCommand()
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	toolResult, err := executeCodexCommand(ctx, mgr, t.TempDir(), "exec_command", map[string]any{
		"cmd": command, "shell": shell, "login": false, "yield_time_ms": 5000,
	}, safety.DefaultCommandPolicy())
	if !errors.Is(err, context.DeadlineExceeded) || strings.Contains(err.Error(), "exited with code -1") {
		t.Fatalf("error=%v content=%s", err, toolResult.Content)
	}
	var result fileio.InteractiveResult
	if unmarshalErr := json.Unmarshal([]byte(toolResult.Content), &result); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	if result.ExitCode != nil || result.OutputDescriptor.Status != fileio.AsyncKilled ||
		!strings.Contains(result.Output, "codex partial stdout") || !strings.Contains(result.Output, "codex partial stderr") {
		t.Fatalf("result=%+v", result)
	}
}
