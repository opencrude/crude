package workspace

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The reported defect: ahead/behind only described the remote right after a
// manual fetch. A background fetch must move the snapshot's behind count to
// what the remote actually holds, without anyone pressing Fetch.
func TestGitAutoFetchRevealsRemoteCommits(t *testing.T) {
	_, ours, _ := remoteFixture(t)
	service := NewGitService(ours)

	before, err := service.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if before.Ahead != 1 || before.Behind != 0 {
		t.Fatalf("before fetch: ↑%d ↓%d, want stale ↑1 ↓0", before.Ahead, before.Behind)
	}
	if err := service.autoFetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	after, err := service.Snapshot(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if after.Ahead != 1 || after.Behind != 1 {
		t.Fatalf("after auto-fetch: ↑%d ↓%d, want ↑1 ↓1", after.Ahead, after.Behind)
	}
}

// A background fetch never queues: a remote operation in flight makes the
// round a no-op, and status reads never wait for the remote lane.
func TestGitAutoFetchNeverBlocksOrQueues(t *testing.T) {
	_, ours, _ := remoteFixture(t)
	service := NewGitService(ours)
	service.remote <- struct{}{}
	defer func() { <-service.remote }()

	done := make(chan error, 1)
	go func() { done <- service.autoFetch(t.Context()) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("auto-fetch waited for a busy remote lane")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	snapshot, err := service.Snapshot(ctx)
	if err != nil {
		t.Fatalf("status read waited behind the remote lane: %v", err)
	}
	if snapshot.Behind != 0 {
		t.Fatalf("a skipped round fetched anyway: ↓%d", snapshot.Behind)
	}
}

// A user-started remote operation waits for an in-flight background fetch
// instead of racing it for the same ref locks.
func TestGitManualFetchWaitsForRemoteLane(t *testing.T) {
	_, ours, _ := remoteFixture(t)
	service := NewGitService(ours)
	service.remote <- struct{}{}
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	if err := service.Fetch(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("manual fetch err = %v, want deadline while the lane is held", err)
	}
	<-service.remote
	if err := service.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
}

// Credentials are never prompted for in the background: a remote that asks
// for them pauses auto-fetch until a remote operation the user started works.
func TestGitAutoFetchPausesOnCredentialFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Basic realm="crude"`)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	origin, ours, _ := remoteFixture(t)
	t.Setenv("GIT_CONFIG_GLOBAL", t.TempDir()+"/none")
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	gitMust(t, ours, "remote", "set-url", "origin", server.URL+"/repo.git")
	service := NewGitService(ours)

	if err := service.autoFetch(t.Context()); !errors.Is(err, ErrGitAuthentication) {
		t.Fatalf("err = %v, want a credential failure", err)
	}
	if !service.autoFetchPaused.Load() {
		t.Fatal("auto-fetch keeps retrying a remote that needs credentials")
	}
	if err := service.autoFetch(t.Context()); err != nil {
		t.Fatalf("paused auto-fetch still ran: %v", err)
	}
	gitMust(t, ours, "remote", "set-url", "origin", origin)
	if err := service.Fetch(t.Context()); err != nil {
		t.Fatal(err)
	}
	if service.autoFetchPaused.Load() {
		t.Fatal("a working manual fetch did not resume auto-fetch")
	}
}

// Branches without a fetchable upstream, and option-shaped remote names from
// repository config, are never passed to git fetch.
func TestGitAutoFetchSkipsUntrackedAndUnsafeRemotes(t *testing.T) {
	_, ours, _ := remoteFixture(t)
	service := NewGitService(ours)
	if remote, ok := service.upstreamRemote(); !ok || remote != "origin" {
		t.Fatalf("upstream remote = %q, %v", remote, ok)
	}
	gitMust(t, ours, "config", "branch.main.remote", "--upload-pack=touch pwned")
	if remote, ok := service.upstreamRemote(); ok {
		t.Fatalf("option-shaped remote %q accepted", remote)
	}
	gitMust(t, ours, "switch", "-c", "local-only")
	if remote, ok := service.upstreamRemote(); ok {
		t.Fatalf("untracked branch fetched from %q", remote)
	}
}

func TestGitAutoFetchBackoffIsBounded(t *testing.T) {
	if got := gitAutoFetchDelay(0); got != gitAutoFetchInterval {
		t.Fatalf("healthy delay = %v", got)
	}
	if got := gitAutoFetchDelay(1); got != 2*gitAutoFetchInterval {
		t.Fatalf("first backoff = %v", got)
	}
	if got := gitAutoFetchDelay(50); got != gitAutoFetchMaxBackoff {
		t.Fatalf("backoff ceiling = %v", got)
	}
}

// Stop cancels the worker and joins it; a second stop is harmless.
func TestGitAutoFetchStopJoins(t *testing.T) {
	_, ours, _ := remoteFixture(t)
	stop := NewGitService(ours).StartAutoFetch()
	done := make(chan struct{})
	go func() { stop(); stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not join the auto-fetch worker")
	}
}
