package viewmodel

import (
	"slices"
	"time"

	tea "charm.land/bubbletea/v2"

	"crude/workspace"
)

const (
	// gitSnapshotIdleDelay is how long repository events must pause before the
	// header identity reconciles. One save or commit touches several paths
	// (file, index, refs, HEAD), and an agent turn writes many files, so a read
	// per event would keep the Git service busy for no visible gain.
	gitSnapshotIdleDelay = 400 * time.Millisecond
	// gitSnapshotHoldLimit bounds how long a continuous burst (an agent writing
	// a whole tree) may hold the branch and change counts stale.
	gitSnapshotHoldLimit = 2 * time.Second
	// gitSnapshotFallback bounds staleness when no watcher event arrives at all:
	// an overflowed or dropped event must not freeze the header identity until
	// the user opens the Git panel.
	gitSnapshotFallback = 30 * time.Second
)

// requestGitSnapshot records one repository-relevant watcher event. The read
// itself starts from the tick loop so a burst collapses into a single snapshot,
// mirroring the editor's gutter Git projection.
func (m *App) requestGitSnapshot(now time.Time) {
	if m == nil || m.gitPanel == nil {
		return
	}
	m.gitSnapshotEventAt = now
	if m.gitSnapshotBurstAt.IsZero() {
		m.gitSnapshotBurstAt = now
	}
}

// gitSnapshotDue reports whether the pending reconciliation is due: the event
// burst has settled, a continuous burst has held the projection stale past the
// hold limit, or the fallback interval elapsed without any event.
func (m *App) gitSnapshotDue(now time.Time) bool {
	if m == nil || m.gitPanel == nil {
		return false
	}
	if !m.gitSnapshotEventAt.IsZero() {
		if now.Sub(m.gitSnapshotEventAt) >= gitSnapshotIdleDelay {
			return true
		}
		return !m.gitSnapshotBurstAt.IsZero() && now.Sub(m.gitSnapshotBurstAt) >= gitSnapshotHoldLimit
	}
	return !m.gitSnapshotAt.IsZero() && now.Sub(m.gitSnapshotAt) >= gitSnapshotFallback
}

// gitSnapshotReady reports whether the tick loop may start the reconciliation
// read: a read is due and no Git read of any kind is in flight. Reconciliation
// never supersedes an explicit refresh or a source-control mutation, because a
// superseded result loses that operation's notice, commit-field clear, editor
// invalidation, and error report.
func (m *App) gitSnapshotReady(now time.Time) bool {
	if m == nil || m.gitPanel == nil || m.gitReadsInFlight > 0 {
		return false
	}
	return m.gitSnapshotDue(now)
}

// startGitSnapshotRead launches the silent reconciliation read. Callers check
// gitSnapshotReady first; this consumes the pending event burst and owns one
// in-flight slot until its result settles.
func (m *App) startGitSnapshotRead() tea.Cmd {
	if m == nil || m.gitPanel == nil || m.gitReadsInFlight > 0 {
		return nil
	}
	m.gitSnapshotEventAt = time.Time{}
	m.gitSnapshotBurstAt = time.Time{}
	if m.Git == nil {
		m.Git = workspace.NewGitService(m.RootPath)
	}
	service := m.Git
	gen := m.gitRefreshGen.Add(1)
	m.gitReadsInFlight++
	return func() tea.Msg {
		ctx, cancel := m.sourceControlContext()
		defer cancel()
		snapshot, err := service.Snapshot(ctx)
		return sourceControlResultMsg{gen: gen, snapshot: snapshot, err: err, silent: true}
	}
}

// sameGitSnapshot reports whether two projections paint the same header
// identity and Git panel. A silent reconciliation that finds no
// change must not rebuild the panel list or invalidate the editor's gutter
// annotation (a Git read of its own) on every fallback pass.
func sameGitSnapshot(a, b workspace.GitSnapshot) bool {
	if a.RepositoryRoot != b.RepositoryRoot || a.Branch != b.Branch || a.Upstream != b.Upstream ||
		a.Head != b.Head || a.Ahead != b.Ahead || a.Behind != b.Behind {
		return false
	}
	return slices.Equal(a.Changes, b.Changes) &&
		slices.Equal(a.Branches, b.Branches) &&
		slices.EqualFunc(a.RecentCommits, b.RecentCommits, func(x, y workspace.GitCommitSummary) bool {
			return x.Hash == y.Hash && x.ShortHash == y.ShortHash && x.Subject == y.Subject &&
				x.Author == y.Author && x.When.Equal(y.When)
		})
}
