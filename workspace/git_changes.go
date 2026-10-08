package workspace

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"crude/fileio"
)

// GitChangesLimit bounds how many files one batched comparison prepares. A
// larger change set still lists every path; only the first files get text.
const GitChangesLimit = 400

// GitChangeComparison pairs one requested change with its comparison or the
// reason that file has none (an unresolved conflict, an unreadable path). One
// failing file never hides the rest of the change set.
type GitChangeComparison struct {
	Change     GitChange
	Comparison GitComparison
	Err        error
}

// CompareChanges reads repository state once and compares every requested
// change against it, so a multi-file review is one consistent snapshot rather
// than N independently timed reads.
func (s *GitService) CompareChanges(ctx context.Context, changes []GitChange) ([]GitChangeComparison, error) {
	if s == nil {
		return nil, errors.New("git service is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readReviewState(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]GitChangeComparison, 0, min(len(changes), GitChangesLimit))
	var total int64
	for index, change := range changes {
		if index >= GitChangesLimit {
			break
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		item := GitChangeComparison{Change: change}
		if total > gitComparisonLimit {
			item.Err = errors.New("review size limit reached; open this file on its own")
		} else {
			item.Comparison, item.Err = state.compare(ctx, change)
			total += int64(len(item.Comparison.Before) + len(item.Comparison.After))
		}
		out = append(out, item)
	}
	return out, nil
}

// StagedPaths is the exact set of repository paths a commit would record,
// sorted. It is what the Git panel displays and what CommitStaged verifies.
func (snapshot GitSnapshot) StagedPaths() []string {
	var paths []string
	for _, change := range snapshot.Changes {
		if change.Area == GitAreaStaged {
			paths = append(paths, change.Path)
		}
	}
	slices.Sort(paths)
	return slices.Compact(paths)
}

// CommitStaged commits the index with Git itself, so hooks, signing and
// commit templates behave exactly as they do in a terminal. It fails closed
// with ErrGitReviewStale when the staged path set differs from expected, the
// set the user was shown; nothing is committed in that case.
func (s *GitService) CommitStaged(ctx context.Context, expected []string, message string) error {
	if s == nil {
		return errors.New("git service is unavailable")
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("commit message is empty")
	}
	if len(expected) == 0 {
		return errors.New("nothing is staged; stage changes before committing")
	}
	unlock, err := fileio.LockMutationContext(ctx)
	if err != nil {
		return err
	}
	defer unlock()
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.readReviewState(ctx)
	if err != nil {
		return err
	}
	review, err := state.commitReview(ctx)
	if err != nil {
		return err
	}
	actual := make([]string, 0, len(review.Comparisons))
	for _, comparison := range review.Comparisons {
		actual = append(actual, comparison.Path)
	}
	want := slices.Clone(expected)
	slices.Sort(want)
	if !slices.Equal(actual, slices.Compact(want)) {
		return fmt.Errorf("%w (staged %d files, expected %d)", ErrGitReviewStale, len(actual), len(want))
	}
	return s.run(ctx, "commit", "-m", message)
}
