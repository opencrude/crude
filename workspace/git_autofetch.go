package workspace

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

const (
	// gitAutoFetchFirst lets workspace startup settle before the first fetch.
	gitAutoFetchFirst = 10 * time.Second
	// gitAutoFetchInterval matches the common editor auto-fetch cadence.
	gitAutoFetchInterval = 3 * time.Minute
	// gitAutoFetchMaxBackoff bounds retries while the remote is unreachable.
	gitAutoFetchMaxBackoff = 30 * time.Minute
	// gitAutoFetchTimeout bounds one background fetch.
	gitAutoFetchTimeout = time.Minute
)

// gitAutoFetchEnv makes a background fetch fail instead of asking anyone for
// credentials: nobody started it, so nobody is there to answer.
var gitAutoFetchEnv = []string{"GIT_ASKPASS=", "SSH_ASKPASS=", "SSH_ASKPASS_REQUIRE=never"}

// StartAutoFetch keeps the checked-out branch's remote-tracking ref current,
// so the snapshot's ahead/behind counts describe the remote rather than
// whenever someone last fetched. The fetch moves only refs under .git, which
// the workspace watcher already turns into a snapshot reconcile. The returned
// stop cancels an in-flight fetch and joins the worker.
func (s *GitService) StartAutoFetch() (stop func()) {
	if s == nil || s.remote == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(gitAutoFetchFirst)
		defer timer.Stop()
		failures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			if err := s.autoFetch(ctx); err == nil {
				failures = 0
			} else if ctx.Err() != nil {
				return
			} else {
				failures++
			}
			timer.Reset(gitAutoFetchDelay(failures))
		}
	}()
	var once sync.Once
	return func() { once.Do(func() { cancel(); <-done }) }
}

func gitAutoFetchDelay(failures int) time.Duration {
	delay := gitAutoFetchInterval
	for range failures {
		if delay >= gitAutoFetchMaxBackoff {
			break
		}
		delay *= 2
	}
	return min(delay, gitAutoFetchMaxBackoff)
}

// autoFetch fetches the upstream remote of the checked-out branch. It never
// waits: a remote operation already in flight makes this round a no-op. It
// never takes the repository lane, so status reads do not queue behind the
// network.
func (s *GitService) autoFetch(ctx context.Context) error {
	if s.autoFetchPaused.Load() {
		return nil
	}
	remote, ok := s.upstreamRemote()
	if !ok {
		return nil
	}
	select {
	case s.remote <- struct{}{}:
	default:
		return nil
	}
	defer func() { <-s.remote }()
	ctx, cancel := context.WithTimeout(ctx, gitAutoFetchTimeout)
	defer cancel()
	_, err := s.execRemote(ctx, gitAutoFetchEnv, "fetch", "--prune", remote)
	if errors.Is(err, ErrGitAuthentication) {
		s.autoFetchPaused.Store(true)
	}
	return err
}

// upstreamRemote names the remote the checked-out branch tracks. Detached
// heads, local upstreams and untracked branches have nothing to fetch.
func (s *GitService) upstreamRemote() (string, bool) {
	repo, _, err := s.open()
	if err != nil {
		return "", false
	}
	defer closeGitRepository(repo)
	head, err := repo.Head()
	if err != nil || !head.Name().IsBranch() {
		return "", false
	}
	cfg, err := repo.Config()
	if err != nil {
		return "", false
	}
	branch := cfg.Branches[head.Name().Short()]
	if branch == nil || branch.Merge == "" || branch.Remote == "" || branch.Remote == "." {
		return "", false
	}
	// A configured name is untrusted input to a command line.
	if strings.HasPrefix(branch.Remote, "-") {
		return "", false
	}
	return branch.Remote, true
}
