package agent

import "testing"

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
