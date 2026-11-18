package worktree

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSharedExecutionDoesNotAllocateSnapshots(t *testing.T) {
	root, store, s := fixture(t)
	put(t, root, "link-target", []byte("shared content"))
	for i := range 80 {
		c, err := s.Create(t.Context(), Request{Owner: Owner{AgentID: "reader", RunID: fmt.Sprint(i)}, Mode: Auto})
		if err != nil || c.ID != "" || c.Root != root || c.Mode != Shared || len(c.Baseline) != 0 {
			t.Fatalf("shared binding %d: id=%s root=%s err=%v", i, c.ID, c.Root, err)
		}
	}
	if len(s.List()) != 0 {
		t.Fatal("shared launches accumulated execution records")
	}
	if entries, err := os.ReadDir(store); err != nil && !os.IsNotExist(err) || len(entries) != 0 {
		t.Fatal("shared binding wrote snapshot files", err)
	}
}

func TestRetainedInventoryDoesNotBlockCreateOrReopen(t *testing.T) {
	_, store, s := fixture(t)
	first, err := s.Create(t.Context(), Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Unpin(first.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(t.Context(), first.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	// Current-format retained history can legitimately exceed both old 16/64
	// ceilings. No additional Git checkout is needed to seed that history.
	for i := range 70 {
		c := s.contexts[first.ID]
		c.ID = fmt.Sprintf("%032x", i+1)
		c.Root = filepath.Join(store, c.ID, "checkout")
		if i < 20 {
			c.Removed = false
			c.Baseline = map[string]File{}
		}
		if err := s.save(c); err != nil {
			t.Fatal(err)
		}
	}
	reopened, err := Open(s.root, store, Options{})
	if err != nil || len(reopened.List()) != 71 {
		t.Fatalf("reopen retained inventory: %v", err)
	}
	c, err := reopened.Create(t.Context(), Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal("retained record count blocked new checkout", err)
	}
	if err := reopened.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if err := reopened.Cleanup(t.Context(), c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
}

func TestParentReviewedDiscardArchivesWithoutChangingLogicalProject(t *testing.T) {
	root, store, s := fixture(t)
	c, err := s.Create(t.Context(), Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	put(t, c.Root, "a.txt", []byte("unwanted child changes"))
	put(t, c.Root, "secret", []byte("ignored build artifact"))
	p, err := s.ReviewDiscard(t.Context(), c.ID)
	if err != nil || p.DiscardDigest == "" || len(p.IgnoredPaths) != 1 {
		t.Fatal("discard review", p, err)
	}
	if err := s.Cleanup(t.Context(), c.ID, testOwner, CleanupOptions{Discard: true, Digest: p.DiscardDigest}); err == nil {
		t.Fatal("active checkout removed")
	}
	if err := s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	put(t, c.Root, "secret", []byte("newer ignored bytes"))
	if err := s.Cleanup(t.Context(), c.ID, testOwner, CleanupOptions{Discard: true, Digest: p.DiscardDigest}); err == nil {
		t.Fatal("stale ignored-file review allowed discard")
	}
	p, err = s.ReviewDiscard(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(t.Context(), c.ID, testOwner, CleanupOptions{Discard: true, Digest: p.DiscardDigest}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.Root); !os.IsNotExist(err) {
		t.Fatal("discarded checkout remains", err)
	}
	got, err := os.ReadFile(filepath.Join(root, "a.txt"))
	if err != nil || string(got) != "committed\n" {
		t.Fatal("discard merged into logical project", string(got), err)
	}
	reopened, err := Open(root, store, Options{})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := reopened.Inspect(c.ID)
	if err != nil || !retained.Removed || retained.ArchiveCommit == "" || string(retained.ArchivedResult["secret"].Data) != "newer ignored bytes" {
		t.Fatal("discard archive missing", err)
	}
	if got := gitTest(t, root, "show", "refs/crude/executions/"+c.ID+":a.txt"); got != "unwanted child changes" {
		t.Fatal("Git undo archive missing", got)
	}
}

func TestSharedSavedRecordCleanupNeverRemovesSharedFiles(t *testing.T) {
	root, _, s := fixture(t)
	c := Context{Version: FormatVersion, ID: "11111111111111111111111111111111", Owner: testOwner, LogicalRoot: root, Root: root, Mode: Shared, Ready: true, Kept: true}
	if err := s.save(c); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(t.Context(), c.ID, testOwner); err == nil {
		t.Fatal("Keep ignored")
	}
	if err := s.Keep(c.ID, testOwner, false); err != nil {
		t.Fatal(err)
	}
	if err := s.Cleanup(t.Context(), c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "a.txt")); err != nil || string(got) != "committed\n" {
		t.Fatal("shared project removed", err)
	}
}

func TestFailedPreparationRemnantsCanBeReviewedAndArchived(t *testing.T) {
	for _, stage := range []string{"missing", "partial", "linked"} {
		t.Run(stage, func(t *testing.T) {
			_, _, s := fixture(t)
			c, err := s.Create(t.Context(), Request{Owner: testOwner, Mode: Isolated})
			if err != nil {
				t.Fatal(err)
			}
			if stage != "linked" {
				gitTest(t, s.root, "worktree", "remove", "--force", c.Root)
			}
			if stage == "partial" {
				put(t, c.Root, "remnant.txt", []byte("setup evidence"))
			}
			c.Ready = false
			if err := s.save(c); err != nil {
				t.Fatal(err)
			}
			plan, err := s.ReviewDiscard(t.Context(), c.ID)
			if err != nil || plan.DiscardDigest == "" {
				t.Fatal("failed setup not reviewable", err)
			}
			if err := s.Unpin(c.ID, testOwner); err != nil {
				t.Fatal(err)
			}
			if err := s.Cleanup(t.Context(), c.ID, testOwner, CleanupOptions{Discard: true, Digest: plan.DiscardDigest}); err != nil {
				t.Fatal(err)
			}
			archived, err := s.Inspect(c.ID)
			if err != nil || !archived.Removed {
				t.Fatal("failed setup not cleaned", err)
			}
			if stage == "partial" && string(archived.ArchivedResult["remnant.txt"].Data) != "setup evidence" {
				t.Fatal("remnants lost")
			}
		})
	}
}

func TestDiscardNeverExecutesNewlyAppliedGitFilters(t *testing.T) {
	root, _, s := fixture(t)
	gitTest(t, root, "config", "filter.hygiene.clean", "a-nonexistent-program-must-never-run")
	gitTest(t, root, "config", "filter.hygiene.required", "true")
	c, err := s.Create(t.Context(), Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	put(t, c.Root, ".gitattributes", []byte("new.dat filter=hygiene\n"))
	put(t, c.Root, "new.dat", []byte("new untracked file"))
	plan, err := s.ReviewDiscard(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	err = s.Cleanup(t.Context(), c.ID, testOwner, CleanupOptions{Discard: true, Digest: plan.DiscardDigest})
	if err == nil || !strings.Contains(err.Error(), "git filter") || strings.Contains(err.Error(), "nonexistent-program") {
		t.Fatal("cleanup executed a filter instead of refusing it", err)
	}
	if _, err := os.Stat(c.Root); err != nil {
		t.Fatal("refused checkout removed", err)
	}
}

func TestDiscardNeverExecutesTrackedGitFilterDuringStatus(t *testing.T) {
	root, _, s := fixture(t)
	marker := filepath.Join(root, "filter-ran")
	gitTest(t, root, "config", "filter.hygiene.clean", "echo invoked > '"+filepath.ToSlash(marker)+"'; cat")
	c, err := s.Create(t.Context(), Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	put(t, c.Root, ".gitattributes", []byte("a.txt filter=hygiene\n"))
	put(t, c.Root, "a.txt", []byte("changed tracked bytes"))
	plan, err := s.ReviewDiscard(t.Context(), c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Unpin(c.ID, testOwner); err != nil {
		t.Fatal(err)
	}
	err = s.Cleanup(t.Context(), c.ID, testOwner, CleanupOptions{Discard: true, Digest: plan.DiscardDigest})
	if err == nil || !strings.Contains(err.Error(), "git filter") {
		t.Fatal("cleanup did not refuse applied filter", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("cleanup ran a clean filter before its staging guard", err)
	}
}
