package coreclient

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"crude/coreprotocol"
	"crude/corewire"
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
