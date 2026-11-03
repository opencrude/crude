package workspace

import "slices"

// SameGitSnapshot compares user-visible repository evidence. Read identities
// advance even when that evidence did not change, and must not rebuild readers.
func SameGitSnapshot(a, b GitSnapshot) bool {
	if a.RepositoryRoot != b.RepositoryRoot || a.Branch != b.Branch || a.Upstream != b.Upstream ||
		a.Head != b.Head || a.Ahead != b.Ahead || a.Behind != b.Behind ||
		a.Operation != b.Operation || a.OperationDetail != b.OperationDetail {
		return false
	}
	return slices.Equal(a.Changes, b.Changes) &&
		slices.Equal(a.Branches, b.Branches) && slices.Equal(a.Remotes, b.Remotes) &&
		slices.EqualFunc(a.RecentCommits, b.RecentCommits, func(x, y GitCommitSummary) bool {
			return x.Hash == y.Hash && x.ShortHash == y.ShortHash && x.Subject == y.Subject &&
				x.Author == y.Author && x.When.Equal(y.When)
		})
}

// OlderGitSnapshot fences a late response or publication from the same source.
// A new service has a distinct source identity, including after core restart.
func OlderGitSnapshot(candidate, current GitSnapshot) bool {
	return candidate.SourceID != "" && candidate.SourceID == current.SourceID &&
		candidate.Sequence < current.Sequence
}
