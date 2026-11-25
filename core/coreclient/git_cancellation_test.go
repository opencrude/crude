package coreclient

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"crude/core/coreprotocol"
	"crude/core/corewire"
	"crude/workspace"
)

func TestGitReaderCancellationRetiresTheCoreJob(t *testing.T) {
	for _, read := range []struct {
		name string
		run  func(context.Context, *GitService) error
	}{
		{"snapshot", func(ctx context.Context, git *GitService) error { _, err := git.Snapshot(ctx); return err }},
		{"comparison", func(ctx context.Context, git *GitService) error {
			_, err := git.Compare(ctx, workspace.GitChange{})
			return err
		}},
		{"changes", func(ctx context.Context, git *GitService) error { _, err := git.CompareChanges(ctx, nil); return err }},
		{"annotation", func(ctx context.Context, git *GitService) error {
			_, err := git.AnnotateFile(ctx, "file", "text")
			return err
		}},
		{"history", func(ctx context.Context, git *GitService) error {
			_, err := git.InspectCommit(ctx, "commit")
			return err
		}},
		{"conflict", func(ctx context.Context, git *GitService) error { _, err := git.ConflictFile(ctx, "file"); return err }},
		{"discard preview", func(ctx context.Context, git *GitService) error {
			view := WorkspaceView{Client: git.client, ID: git.workspaceID}
			_, err := view.PreviewGitDiscard(ctx, workspace.GitChange{})
			return err
		}},
	} {
		t.Run(read.name, func(t *testing.T) {
			opened := make(chan coreprotocol.Open, 1)
			release, retired, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var once sync.Once
			var cancellations atomic.Int32
			var original coreprotocol.Open
			client := dial(t, &fakeCore{t: t, handle: func(open coreprotocol.Open, payload []byte, stream corewire.Stream) {
				if open.Method == coreprotocol.CancelRequest {
					var in coreprotocol.CancelRequestPayload
					if err := coreprotocol.Decode(payload, &in); err != nil {
						t.Error(err)
					}
					if open.WorkspaceID != original.WorkspaceID || in.RequestID != original.ID {
						t.Error("Git cancellation targeted another admitted job")
					}
					cancellations.Add(1)
					once.Do(func() { close(release) })
					if joinedReply(t, stream, &coreprotocol.BoolResult{OK: true}, nil) {
						_ = stream.CloseWrite()
					}
					return
				}
				defer close(done)
				_ = sendValues(stream, &coreprotocol.Reply{Admitted: true})
				opened <- open
				<-release
				close(retired)
				if cancellations.Load() == 0 {
					return // failed cancellation must still release the fixture
				}
				if joinedReply(t, stream, nil, &coreprotocol.Fault{Code: "cancelled"}) {
					_ = stream.CloseWrite()
				}
			}})
			t.Cleanup(func() {
				once.Do(func() { close(release) })
				<-done
			})
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- read.run(ctx, NewGitService(client, "repository")) }()
			original = <-opened
			cancel()
			err := cancellableResult(t, result)
			if !errors.Is(err, context.Canceled) || cancellations.Load() != 1 {
				t.Fatalf("cancelled Git read left the core job running: %v, cancellations=%d", err, cancellations.Load())
			}
			select {
			case <-retired:
			default:
				t.Fatal("Git read returned before its repository job retired")
			}
		})
	}
}

// Stop can lose the race with the actual push. The command result, not the
// cancelled wait in the shell, decides whether the push happened.
func TestGitPushStopKeepsTheCoreResult(t *testing.T) {
	for _, tc := range []struct {
		name  string
		fault *coreprotocol.Fault
	}{
		{"stopped", &coreprotocol.Fault{Code: "cancelled"}},
		{"already pushed", nil},
		{"hook refused", &coreprotocol.Fault{Code: "service_failed", Message: "pre-push: tests failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			entered := make(chan coreprotocol.Open, 1)
			stopped := make(chan string, 1)
			end := make(chan struct{})
			defer close(end)
			var calls atomic.Int32
			c := dial(t, &fakeCore{t: t, handle: func(o coreprotocol.Open, payload []byte, s corewire.Stream) {
				switch o.Method {
				case coreprotocol.GitPush:
					calls.Add(1)
					_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
					entered <- o
					select {
					case <-end:
						return
					case id := <-stopped:
						if id != o.ID {
							t.Errorf("Stop targeted %s, not the push %s", id, o.ID)
						}
					}
					if joinedReply(t, s, &coreprotocol.GitRemoteResult{}, tc.fault) {
						_ = s.CloseWrite()
					}
				case coreprotocol.CancelRequest:
					var request coreprotocol.CancelRequestPayload
					if err := coreprotocol.Decode(payload, &request); err != nil || o.WorkspaceID != "repo" {
						t.Errorf("wrong cancellation: %+v, %v", o, err)
					}
					stopped <- request.RequestID
					if joinedReply(t, s, &coreprotocol.BoolResult{OK: true}, nil) {
						_ = s.CloseWrite()
					}
				default:
					t.Errorf("unexpected method %s", o.Method)
				}
			}})
			ctx, stop := context.WithCancel(t.Context())
			defer stop()
			done := make(chan error, 1)
			go func() { done <- NewGitService(c, "repo").Push(ctx) }()
			<-entered
			stop()
			err := cancellableResult(t, done)
			var uncertain *UncertainError
			if errors.As(err, &uncertain) || calls.Load() != 1 {
				t.Fatalf("Stop did not settle the one original push: err=%v pushes=%d", err, calls.Load())
			}
			switch {
			case tc.fault == nil && err != nil:
				t.Fatalf("reported a completed push as unsuccessful: %v", err)
			case tc.fault != nil && tc.fault.Code == "cancelled" && !errors.Is(err, context.Canceled):
				t.Fatalf("lost confirmed cancellation: %v", err)
			case tc.fault != nil && tc.fault.Code == "service_failed":
				var got *coreprotocol.Fault
				if !errors.As(err, &got) || *got != *tc.fault {
					t.Fatalf("lost the hook's refusal: %v", err)
				}
			}
		})
	}
}

func TestGitPushKeepsSuccessWhenCancelCleanupFails(t *testing.T) {
	started, stopping, pushed := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c := dial(t, &fakeCore{t: t, handle: func(o coreprotocol.Open, _ []byte, s corewire.Stream) {
		_ = sendValues(s, &coreprotocol.Reply{Admitted: true})
		if o.Method == coreprotocol.GitPush {
			close(started)
			<-stopping
			if joinedReply(t, s, &coreprotocol.GitRemoteResult{}, nil) {
				_ = s.CloseWrite()
			}
			close(pushed)
			return
		}
		close(stopping)
		<-pushed
		if joinedReply(t, s, nil, &coreprotocol.Fault{Code: "cancel_failed"}) {
			_ = s.CloseWrite()
		}
	}})
	ctx, stop := context.WithCancel(t.Context())
	defer stop()
	done := make(chan error, 1)
	go func() { done <- NewGitService(c, "repo").Push(ctx) }()
	<-started
	stop()
	err := cancellableResult(t, done)
	var uncertain *UncertainError
	var cleanup *coreprotocol.Fault
	if !errors.As(err, &uncertain) || !uncertain.ResultKnown || uncertain.ResultFault != nil ||
		!errors.As(err, &cleanup) || cleanup.Code != "cancel_failed" {
		t.Fatalf("lost the distinction between the successful push and the failed stop: %v", err)
	}
}
