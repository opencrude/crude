package agent

import (
	"errors"
	"testing"
)

func TestFileOwnershipUsesPathBoundaries(t *testing.T) {
	ownership := NewFileOwnership()
	if err := ownership.PreClaim("first", "First", []string{"src"}); err != nil {
		t.Fatalf("PreClaim: %v", err)
	}
	if err := ownership.CheckOrClaim("second", "Second", "src_backup/file.go", true); err != nil {
		t.Fatalf("sibling prefix was treated as overlap: %v", err)
	}
	if err := ownership.CheckOrClaim("second", "Second", "src/file.go", true); err == nil {
		t.Fatal("child path did not conflict with claimed directory")
	}
}

func TestFileOwnershipNormalizesRelativeAndAbsolutePaths(t *testing.T) {
	ownership := NewFileOwnership()
	if err := ownership.PreClaim("first", "First", []string{"src"}); err != nil {
		t.Fatalf("PreClaim: %v", err)
	}
	absolute := normalizeClaimPath("src/main.go")
	if err := ownership.CheckOrClaim("second", "Second", absolute, true); err == nil {
		t.Fatal("absolute child bypassed relative directory claim")
	}
}

func TestFileOwnershipTracksReadsWithoutBlockingWriters(t *testing.T) {
	ownership := NewFileOwnership()
	ownership.RecordRead("reader", "src/main.go")
	if err := ownership.CheckOrClaim("writer", "Writer", "src/main.go", true); err != nil {
		t.Fatalf("read tracking blocked writer: %v", err)
	}
	reads := ownership.ReadClaimsByAgent("reader")
	if len(reads) != 1 || reads[0] != normalizeClaimPath("src/main.go") {
		t.Fatalf("reads = %v", reads)
	}
	ownership.Release("reader")
	if reads := ownership.ReadClaimsByAgent("reader"); len(reads) != 0 {
		t.Fatalf("reads survived release: %v", reads)
	}
}

func TestFileOwnershipReplaceClaimsIsAtomicAndDropsBroadProjection(t *testing.T) {
	ownership := NewFileOwnership()
	if err := ownership.PreClaim("child", "Child", []string{"src"}); err != nil {
		t.Fatal(err)
	}
	if err := ownership.PreClaim("other", "Other", []string{"docs"}); err != nil {
		t.Fatal(err)
	}
	if err := ownership.ReplaceClaims("child", "Child", []string{"docs/readme.md"}); err == nil {
		t.Fatal("conflicting replacement succeeded")
	}
	if got := ownership.WriteClaimsByAgent("child"); len(got) != 1 || got[0] != normalizeClaimPath("src") {
		t.Fatalf("failed replacement changed projection: %v", got)
	}
	if err := ownership.ReplaceClaims("child", "Child", []string{"src/one.go", "src/two.go"}); err != nil {
		t.Fatal(err)
	}
	if err := ownership.CheckOrClaim("other", "Other", "src/three.go", true); err != nil {
		t.Fatalf("broad local claim survived replacement: %v", err)
	}
}

func TestFileOwnershipCommitReplaceClaimsHoldsProjectionUntilCommit(t *testing.T) {
	ownership := NewFileOwnership()
	if err := ownership.PreClaim("parent", "Parent", []string{"src"}); err != nil {
		t.Fatal(err)
	}
	commitErr := errors.New("stale durable preimage")
	if err := ownership.CommitReplaceClaims("parent", "Parent", []string{"src/owned.go"}, func() error {
		return commitErr
	}); !errors.Is(err, commitErr) {
		t.Fatalf("commit error = %v", err)
	}
	if got := ownership.WriteClaimsByAgent("parent"); len(got) != 1 || got[0] != normalizeClaimPath("src") {
		t.Fatalf("failed commit changed projection: %v", got)
	}
	if err := ownership.CommitReplaceClaims("parent", "Parent", nil, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if got := ownership.WriteClaimsByAgent("parent"); len(got) != 0 {
		t.Fatalf("explicit relinquish retained claims: %v", got)
	}
}
