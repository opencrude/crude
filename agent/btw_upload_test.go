package agent

import (
	"context"
	"errors"
	"testing"
)

func TestBtwUploadControllerRestartsAtOrBelowThreshold(t *testing.T) {
	tests := map[string]int64{
		"before upload":      0,
		"exactly 90 percent": 90,
	}
	for name, uploaded := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			controller := newBtwUploadController(cancel)
			controller.Progress(uploaded, 100)
			controller.Queue(BtwMessage{})

			select {
			case <-ctx.Done():
			default:
				t.Fatalf("upload at %d%% was not restarted", uploaded)
			}
			if !errors.Is(context.Cause(ctx), errBtwUploadRestart) {
				t.Fatalf("cause = %v, want BTW upload restart", context.Cause(ctx))
			}
		})
	}
}

func TestBtwUploadControllerKeepsLateMessageForNextRequest(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	controller := newBtwUploadController(cancel)
	controller.Progress(91, 100)
	controller.Queue(BtwMessage{})

	select {
	case <-ctx.Done():
		t.Fatal("upload past 90% was restarted")
	default:
	}
	if controller.RestartRequested() {
		t.Fatal("late BTW marked the request for restart")
	}
	if queued := controller.TakeQueued(); len(queued) != 1 {
		t.Fatalf("queued messages = %d, want 1", len(queued))
	}
}

func TestBtwUploadControllerRechecksQueuedMessageOnTransportRetry(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	controller := newBtwUploadController(cancel)
	controller.Progress(100, 100)
	controller.Queue(BtwMessage{})

	select {
	case <-ctx.Done():
		t.Fatal("completed upload was restarted before the transport retried")
	default:
	}

	controller.Progress(0, 100)
	select {
	case <-ctx.Done():
	default:
		t.Fatal("queued BTW did not restart the next stale upload attempt")
	}
	if !errors.Is(context.Cause(ctx), errBtwUploadRestart) {
		t.Fatalf("cause = %v, want BTW upload restart", context.Cause(ctx))
	}
}

func TestBtwUploadControllerDoesNotRestartAcceptedOrStreamingRequest(t *testing.T) {
	tests := map[string]func(*btwUploadController){
		"accepted": func(controller *btwUploadController) {
			controller.Accepted()
		},
		"output": func(controller *btwUploadController) {
			controller.OutputStarted()
		},
	}
	for name, prepare := range tests {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			controller := newBtwUploadController(cancel)
			controller.Progress(50, 100)
			prepare(controller)
			controller.Queue(BtwMessage{})

			select {
			case <-ctx.Done():
				t.Fatalf("%s request was restarted", name)
			default:
			}
		})
	}
}
