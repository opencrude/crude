package agent

import (
	"context"
	"errors"
	"testing"
)

func TestRuntimeUploadControllerRestartsHumanSteeringAtOrBelowThreshold(t *testing.T) {
	tests := map[string]int64{
		"before upload":      0,
		"exactly 90 percent": 90,
	}
	for name, uploaded := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			controller := newRuntimeUploadController(cancel)
			controller.Progress(uploaded, 100)
			controller.Queue(RuntimeMessage{})

			select {
			case <-ctx.Done():
			default:
				t.Fatalf("upload at %d%% was not restarted", uploaded)
			}
			if !errors.Is(context.Cause(ctx), errRuntimeUploadRestart) {
				t.Fatalf("cause = %v, want runtime upload restart", context.Cause(ctx))
			}
		})
	}
}

func TestRuntimeUploadControllerKeepsLateMessageForNextRequest(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	controller := newRuntimeUploadController(cancel)
	controller.Progress(91, 100)
	controller.Queue(RuntimeMessage{})

	select {
	case <-ctx.Done():
		t.Fatal("upload past 90% was restarted")
	default:
	}
	if controller.RestartRequested() {
		t.Fatal("late human steering marked the request for restart")
	}
	if queued := controller.TakeQueued(); len(queued) != 1 {
		t.Fatalf("queued messages = %d, want 1", len(queued))
	}
}

func TestRuntimeUploadControllerRechecksQueuedMessageOnTransportRetry(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	controller := newRuntimeUploadController(cancel)
	controller.Progress(100, 100)
	controller.Queue(RuntimeMessage{})

	select {
	case <-ctx.Done():
		t.Fatal("completed upload was restarted before the transport retried")
	default:
	}

	controller.Progress(0, 100)
	select {
	case <-ctx.Done():
	default:
		t.Fatal("queued human steering did not restart the next stale upload attempt")
	}
	if !errors.Is(context.Cause(ctx), errRuntimeUploadRestart) {
		t.Fatalf("cause = %v, want runtime upload restart", context.Cause(ctx))
	}
}

func TestRuntimeUploadControllerDoesNotRestartAcceptedOrStreamingRequest(t *testing.T) {
	tests := map[string]func(*runtimeUploadController){
		"accepted": func(controller *runtimeUploadController) {
			controller.Accepted()
		},
		"output": func(controller *runtimeUploadController) {
			controller.OutputStarted()
		},
	}
	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			controller := newRuntimeUploadController(cancel)
			controller.Progress(50, 100)
			prepare(controller)
			controller.Queue(RuntimeMessage{})

			select {
			case <-ctx.Done():
				t.Fatalf("%s request was restarted", name)
			default:
			}
		})
	}
}
