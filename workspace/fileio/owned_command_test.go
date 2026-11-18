package fileio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func ownedCommandFixture(t *testing.T, mode string, args ...string) OwnedCommand {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return OwnedCommand{
		Executable: executable,
		Arguments:  append([]string{"-test.run=^TestOwnedCommandHelper$", "--"}, args...),
		Directory:  t.TempDir(),
		Environment: []string{
			"CRUDE_OWNED_COMMAND_HELPER=" + mode,
			"CRUDE_OWNED_COMMAND_VALUE=explicit value",
		},
		OutputLimit: 4096,
	}
}

func TestOwnedCommandHelper(t *testing.T) {
	mode := os.Getenv("CRUDE_OWNED_COMMAND_HELPER")
	if mode == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) > 0 {
		args = args[1:]
	}
	switch mode {
	case "literal":
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Args  []string
			Value string
		}{args, os.Getenv("CRUDE_OWNED_COMMAND_VALUE")})
	case "output":
		fmt.Print(strings.Repeat("x", 32768))
	case "failure":
		os.Exit(42)
	case "wait":
		if err := os.WriteFile(args[0], []byte("ready"), 0o600); err != nil {
			os.Exit(1)
		}
		for {
			time.Sleep(time.Second)
		}
	case "parent":
		child := exec.Command(os.Args[0], "-test.run=^TestOwnedCommandHelper$", "--", args[0], args[1])
		child.Env = append(os.Environ(), "CRUDE_OWNED_COMMAND_HELPER=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Run(); err != nil {
			os.Exit(1)
		}
	case "child":
		if err := os.WriteFile(args[0], []byte("ready"), 0o600); err != nil {
			os.Exit(1)
		}
		time.Sleep(1500 * time.Millisecond)
		if err := os.WriteFile(args[1], []byte("finished"), 0o600); err != nil {
			os.Exit(1)
		}
	case "escape-parent":
		child := exec.Command(args[0], os.Args[0], "-test.run=^TestOwnedCommandHelper$", "--", args[1], args[2], args[3])
		child.Env = append(os.Environ(), "CRUDE_OWNED_COMMAND_HELPER=escape-child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
			if _, err := os.Stat(args[1]); err == nil {
				if len(args) > 4 && args[4] == "fail" {
					os.Exit(7)
				}
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(1)
	case "escape-child":
		if err := os.WriteFile(args[0], []byte("ready"), 0o600); err != nil {
			os.Exit(1)
		}
		for deadline := time.Now().Add(30 * time.Second); time.Now().Before(deadline); {
			if _, err := os.Stat(args[1]); err == nil {
				_ = os.WriteFile(args[2], []byte("stopped"), 0o600)
				os.Exit(0)
			}
			time.Sleep(5 * time.Millisecond)
		}
		os.Exit(1)
	default:
		os.Exit(2)
	}
	os.Exit(0)
}

func TestOwnedCommandCancellationStopsDescendantsOnly(t *testing.T) {
	dir := t.TempDir()
	selectedReady, selectedDone := filepath.Join(dir, "selected-ready"), filepath.Join(dir, "selected-done")
	neighborReady, neighborDone := filepath.Join(dir, "neighbor-ready"), filepath.Join(dir, "neighbor-done")
	selected := ownedCommandFixture(t, "parent", selectedReady, selectedDone)
	neighbor := ownedCommandFixture(t, "child", neighborReady, neighborDone)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	selectedResult, neighborResult := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := RunOwnedCommand(ctx, selected)
		selectedResult <- err
	}()
	go func() {
		_, err := RunOwnedCommand(t.Context(), neighbor)
		neighborResult <- err
	}()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		_, a := os.Stat(selectedReady)
		_, b := os.Stat(neighborReady)
		if a == nil && b == nil {
			break
		}
		select {
		case err := <-selectedResult:
			t.Fatalf("selected command stopped before readiness: %v", err)
		case err := <-neighborResult:
			t.Fatalf("neighbor stopped before readiness: %v", err)
		case <-deadline.C:
			t.Fatal("commands did not become ready")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-selectedResult:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("selected command did not report cancellation: %v", err)
		}
	case <-deadline.C:
		t.Fatal("selected command did not join")
	}
	select {
	case err := <-neighborResult:
		if err != nil {
			t.Fatalf("independent command was affected: %v", err)
		}
	case <-deadline.C:
		t.Fatal("independent command did not finish")
	}
	if _, err := os.Stat(selectedDone); !os.IsNotExist(err) {
		t.Fatalf("selected descendant survived cancellation: %v", err)
	}
	if _, err := os.Stat(neighborDone); err != nil {
		t.Fatalf("independent command did not complete: %v", err)
	}
}

func TestOwnedCommandUsesExactArgumentsAndEnvironment(t *testing.T) {
	args := []string{"a b", `"; touch unexpected; #`, "$(echo injected)"}
	spec := ownedCommandFixture(t, "literal", args...)
	result, err := RunOwnedCommand(t.Context(), spec)
	if err != nil || result.Truncated {
		t.Fatalf("literal process failed: %+v %v", result, err)
	}
	var got struct {
		Args  []string
		Value string
	}
	if err := json.Unmarshal(result.Output, &got); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got.Args, "\x00") != strings.Join(args, "\x00") || got.Value != "explicit value" {
		t.Fatalf("arguments or environment changed: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(spec.Directory, "unexpected")); !os.IsNotExist(err) {
		t.Fatalf("arguments were interpreted as shell code: %v", err)
	}
}

func TestOwnedCommandBoundsOutputAndReportsFailure(t *testing.T) {
	spec := ownedCommandFixture(t, "output")
	spec.OutputLimit = 1024
	result, err := RunOwnedCommand(t.Context(), spec)
	if err != nil || !result.Truncated || len(result.Output) != 1024 {
		t.Fatalf("output limit: len=%d truncated=%v err=%v", len(result.Output), result.Truncated, err)
	}
	spec = ownedCommandFixture(t, "failure")
	if _, err := RunOwnedCommand(t.Context(), spec); err == nil {
		t.Fatal("failed command reported success")
	}
	spec.Executable = "relative"
	if _, err := RunOwnedCommand(t.Context(), spec); err == nil {
		t.Fatal("a relative executable was accepted")
	}
	spec = ownedCommandFixture(t, "literal")
	spec.Environment = nil
	if _, err := RunOwnedCommand(t.Context(), spec); err == nil {
		t.Fatal("an inherited environment was accepted")
	}
	spec.Environment = []string{}
	spec.OutputLimit = 0
	if _, err := RunOwnedCommand(t.Context(), spec); err == nil {
		t.Fatal("an unbounded output was accepted")
	}
}

func TestOwnedCommandUncertainStopIsNotSuccess(t *testing.T) {
	failedStop := errors.New("cannot terminate")
	err := commandStopError(context.Canceled, failedStop, nil)
	var uncertain *CommandTreeUnverifiedError
	if !errors.Is(err, context.Canceled) || !errors.Is(err, failedStop) || !errors.As(err, &uncertain) {
		t.Fatalf("uncertain cancellation: %v", err)
	}
	failedWait := errors.New("cannot establish process exit")
	err = commandStopError(nil, nil, failedWait)
	if !errors.Is(err, failedWait) || !errors.As(err, &uncertain) {
		t.Fatalf("uncertain wait: %v", err)
	}
	err = joinTreeStop(exec.ErrWaitDelay, failedStop)
	var state asyncCmdState
	err = state.settle(err)
	if !errors.Is(err, failedStop) || !errors.As(err, &uncertain) {
		t.Fatalf("output settlement hid an uncertain tree stop: %v", err)
	}
}

func TestAsyncOutcomeDoesNotHideUncertainStopOnCancellation(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		var state asyncCmdState
		stop := errors.New("process group could not be stopped")
		state.finishOutcome(cause, &CommandTreeUnverifiedError{Err: stop})
		if state.info.Status != AsyncFailed || !strings.Contains(state.info.Error, stop.Error()) {
			t.Fatalf("uncertain stop was reported as killed: %+v", state.info)
		}
	}
}

func TestOwnedCommandCancellationJoinsProcess(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	spec := ownedCommandFixture(t, "wait", ready)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := RunOwnedCommand(ctx, spec)
		done <- err
	}()
	t.Cleanup(cancel)
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("command stopped before readiness: %v", err)
		case <-deadline.C:
			t.Fatal("command did not become ready")
		case <-ticker.C:
		}
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancellation result: %v", err)
		}
	case <-deadline.C:
		t.Fatal("cancellation did not join the command")
	}
	ctx, cancel = context.WithCancel(t.Context())
	cancel()
	if _, err := RunOwnedCommand(ctx, spec); !errors.Is(err, context.Canceled) {
		t.Fatalf("already cancelled command: %v", err)
	}
}
