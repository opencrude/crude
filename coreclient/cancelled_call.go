package coreclient

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"crude/coreprotocol"
	"crude/corewire"
)

const cancelledCallCleanupTimeout = 5 * time.Second

// CallCancellable performs an explicit read-only job. Unlike Call, cancellation
// cancels the exact admitted provider job, joins its original final result and
// confirms the result ACK before returning. Cleanup is bounded by the client
// lifetime and five seconds after caller cancellation. Failure to confirm that
// boundary returns UncertainError with the original identity, never a replay
// under another identity or attachment.
func (c *Client) CallCancellable(ctx context.Context, workspaceID, method string, in coreprotocol.Value, out coreprotocol.Target) error {
	return c.callCancellable(ctx, workspaceID, method, in, out, cancelledCallCleanupTimeout)
}

func (c *Client) callCancellable(ctx context.Context, workspaceID, method string, in coreprotocol.Value, out coreprotocol.Target, cleanupTimeout time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return err
	}
	c.wg.Add(1)
	c.mu.Unlock()
	defer c.wg.Done()

	id := randomID()
	runCtx, cancelRun := context.WithCancel(c.lifetime)
	defer cancelRun()
	admitted, finished := make(chan struct{}), make(chan struct{})
	helperDone := make(chan error, 1)
	go func() {
		helperDone <- c.cancelRead(ctx, runCtx, cancelRun, workspaceID, id, admitted, finished, cleanupTimeout)
	}()
	seenAdmission := false
	err := c.callJoined(runCtx, workspaceID, method, id, in, out, func() {
		if !seenAdmission {
			seenAdmission = true
			close(admitted)
		}
	})
	close(finished)
	cancelErr := <-helperDone // no cancellation helper or timer outlives the call
	if cancelErr != nil {
		known := err == nil
		var uncertain *UncertainError
		var fault *coreprotocol.Fault
		if errors.As(err, &uncertain) {
			known = uncertain.ResultKnown
		} else if errors.As(err, &fault) {
			known = true
		}
		state := ""
		if known {
			state = "completed_cleanup_unconfirmed"
		}
		return &UncertainError{RequestID: id, Cause: fmt.Errorf("cancel and join: %w", errors.Join(cancelErr, err)), State: state, ResultKnown: known}
	}
	if err != nil {
		var uncertain *UncertainError
		if ctx.Err() != nil && !errors.As(err, &uncertain) {
			return ctx.Err()
		}
		return err
	}
	return ctx.Err()
}

// cancelRead does not infer admission from progress or a delay. The actor FIFO
// sees cancellation only after the original request has actually been enqueued.
func (c *Client) cancelRead(caller, run context.Context, cancelRun context.CancelFunc, workspaceID, id string, admitted, finished <-chan struct{}, cleanupTimeout time.Duration) error {
	select {
	case <-finished:
		return nil
	case <-caller.Done():
	case <-run.Done():
		return run.Err()
	}
	cleanup, cancel := context.WithTimeout(c.lifetime, cleanupTimeout)
	defer cancel()
	stop := joinedAfterFunc(cleanup, cancelRun)
	defer stop()
	select {
	case <-finished:
		return nil
	case <-cleanup.Done():
		cancelRun()
		return cleanup.Err()
	case <-admitted:
	}
	// A final reply can win the race with admission/caller cancellation; it
	// already establishes the provider's retirement without a cancel command.
	select {
	case <-finished:
		return nil
	default:
	}
	var result coreprotocol.BoolResult
	err := c.callJoined(cleanup, workspaceID, coreprotocol.CancelRequest, randomID(),
		&coreprotocol.CancelRequestPayload{RequestID: id}, &result, nil)
	if err == nil && result.Error != "" {
		err = errors.New(result.Error)
	}
	if err != nil {
		cancelRun()
		return err
	}
	select {
	case <-finished:
		return nil
	case <-cleanup.Done():
		cancelRun()
		return cleanup.Err()
	}
}

// joinedAfterFunc stops a callback that has not begun or joins the one that has.
// In particular a late stream-close callback cannot race a successful return.
func joinedAfterFunc(ctx context.Context, fn func()) func() {
	done := make(chan struct{})
	stop := context.AfterFunc(ctx, func() {
		defer close(done)
		fn()
	})
	return func() {
		if !stop() {
			<-done
		}
	}
}

// callJoined reuses ordinary command exchange with a confirmed-release boundary.
// A known final result is never reattached after possible server release.
func (c *Client) callJoined(ctx context.Context, workspaceID, method, id string, in coreprotocol.Value, out coreprotocol.Target, admitted func()) error {
	_, err := c.callWith(ctx, workspaceID, method, in, out, CallOptions{ID: id},
		commandBoundary{admitted: admitted, acknowledge: func(s corewire.Stream, reader *corewire.Reader) error {
			return acknowledgeReadResult(ctx, s, reader)
		}})
	return err
}

type finalCleanupError struct{ cause error }

func (e *finalCleanupError) Error() string {
	return "known final result; cleanup unconfirmed: " + e.cause.Error()
}
func (e *finalCleanupError) Unwrap() error { return e.cause }

func acknowledgeReadResult(ctx context.Context, s corewire.Stream, reader *corewire.Reader) error {
	if err := sendValues(s, &coreprotocol.TopicAck{Seq: 1}); err != nil {
		return fmt.Errorf("final result ACK: %w", err)
	}
	if err := s.CloseWrite(); err != nil {
		return fmt.Errorf("final result FIN: %w", err)
	}
	if _, err := reader.Next(); !errors.Is(err, io.EOF) {
		if ctx.Err() != nil {
			err = ctx.Err()
		} else if err == nil {
			err = errors.New("unexpected message after final result")
		}
		return fmt.Errorf("final result ACK confirmation: %w", err)
	}
	return nil
}
