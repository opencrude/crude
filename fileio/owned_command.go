package fileio

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// OwnedCommand describes an exact executable invocation. It never passes
// arguments through a shell and never inherits an implicit environment.
type OwnedCommand struct {
	Executable  string
	Arguments   []string
	Directory   string
	Environment []string
	OutputLimit int
}

// OwnedCommandResult contains the bounded combined output of the joined
// process. Truncated means further output was drained but not retained.
type OwnedCommandResult struct {
	Output    []byte
	Truncated bool
}

// CommandTreeUnverifiedError means process teardown could not be confirmed,
// including an escaped process retaining an output pipe. Callers must not treat
// the command as a success or discard mutable data that a process may hold.
type CommandTreeUnverifiedError struct{ Err error }

func (e *CommandTreeUnverifiedError) Error() string {
	return fmt.Sprintf("the command left an unverified process: %v", e.Err)
}

func (e *CommandTreeUnverifiedError) Unwrap() error { return e.Err }

type boundedCommandOutput struct {
	mu        sync.Mutex
	output    bytes.Buffer
	limit     int
	truncated bool
}

func (b *boundedCommandOutput) Write(data []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	remaining := max(0, b.limit-b.output.Len())
	keep := min(remaining, len(data))
	_, _ = b.output.Write(data[:keep])
	b.truncated = b.truncated || keep < len(data)
	return len(data), nil
}

func (b *boundedCommandOutput) result() OwnedCommandResult {
	b.mu.Lock()
	defer b.mu.Unlock()
	return OwnedCommandResult{Output: append([]byte(nil), b.output.Bytes()...), Truncated: b.truncated}
}

// The pipe is owned independently of exec.Cmd.Wait so an unsuccessful leader
// cannot hide an escaped holder behind its exit error.
func finishOwnedOutput(reader *os.File, done <-chan error) error {
	timer := time.NewTimer(commandPipeGrace)
	defer timer.Stop()
	select {
	case err := <-done:
		_ = reader.Close()
		if err != nil {
			return &CommandTreeUnverifiedError{Err: err}
		}
		return nil
	case <-timer.C:
		_ = reader.Close()
		<-done
		return &CommandTreeUnverifiedError{Err: exec.ErrWaitDelay}
	}
}

// RunOwnedCommand executes one exact program off the UI thread. Cancellation
// terminates its retained process tree and joins the leader before return.
// A detached pipe holder is reported as uncertain, not as successful completion.
// The caller owns authorization, the environment, network policy and staging.
func RunOwnedCommand(ctx context.Context, spec OwnedCommand) (OwnedCommandResult, error) {
	if ctx == nil {
		return OwnedCommandResult{}, errors.New("a command context is required")
	}
	if err := ctx.Err(); err != nil {
		return OwnedCommandResult{}, err
	}
	if !filepath.IsAbs(spec.Executable) || !filepath.IsAbs(spec.Directory) {
		return OwnedCommandResult{}, errors.New("the executable and working directory must be absolute")
	}
	if spec.Environment == nil {
		return OwnedCommandResult{}, errors.New("an explicit command environment is required")
	}
	if spec.OutputLimit <= 0 || spec.OutputLimit > 8<<20 {
		return OwnedCommandResult{}, errors.New("the command output limit must be between 1 byte and 8 MiB")
	}
	command := exec.Command(spec.Executable, spec.Arguments...)
	command.Dir = spec.Directory
	command.Env = append([]string{}, spec.Environment...)
	setCmdSysProcAttr(command)
	output := &boundedCommandOutput{limit: spec.OutputLimit}
	reader, writer, err := os.Pipe()
	if err != nil {
		return OwnedCommandResult{}, fmt.Errorf("create command output pipe: %w", err)
	}
	command.Stdout, command.Stderr = writer, writer
	if err := ctx.Err(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return OwnedCommandResult{}, err
	}
	if err := command.Start(); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return output.result(), fmt.Errorf("start the command: %w", err)
	}
	closeErr := writer.Close()
	outputDone := make(chan error, 1)
	go func() {
		_, err := io.Copy(output, reader)
		outputDone <- err
	}()
	settle := func(cause, stopErr, waitErr error) (OwnedCommandResult, error) {
		captureErr := finishOwnedOutput(reader, outputDone)
		if closeErr != nil {
			closeErr = &CommandTreeUnverifiedError{Err: closeErr}
		}
		return output.result(), errors.Join(commandStopError(cause, stopErr, waitErr), closeErr, captureErr)
	}
	tree, err := ownProcessTree(command)
	if err != nil {
		stopErr := cancelProcess(command)
		waitErr := command.Wait()
		return settle(err, stopErr, waitErr)
	}
	defer tree.close()
	if err := ctx.Err(); err != nil {
		stopErr, waitErr := tree.kill(), tree.wait()
		return settle(err, stopErr, waitErr)
	}
	if err := resumeOwnedProcess(command); err != nil {
		stopErr, waitErr := tree.kill(), tree.wait()
		return settle(err, stopErr, waitErr)
	}
	done := make(chan error, 1)
	go func() { done <- tree.wait() }()
	var waitErr, stopErr error
	select {
	case waitErr = <-done:
	case <-ctx.Done():
		stopErr = tree.kill()
		waitErr = <-done
	}
	return settle(ctx.Err(), stopErr, waitErr)
}

func commandStopError(cause, stopErr, waitErr error) error {
	if errors.Is(stopErr, os.ErrProcessDone) {
		stopErr = nil
	}
	if stopErr != nil {
		stopErr = &CommandTreeUnverifiedError{Err: stopErr}
	}
	if waitErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			waitErr = &CommandTreeUnverifiedError{Err: waitErr}
		}
	}
	if cause != nil {
		return errors.Join(cause, stopErr, waitErr)
	}
	return errors.Join(stopErr, waitErr)
}
