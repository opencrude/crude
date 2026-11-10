package workspace

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGitLFSStagesPointersAndReviewsLargeFilesWithoutSmudging(t *testing.T) {
	if out, err := exec.Command("git", "lfs", "version").CombinedOutput(); err != nil {
		t.Skipf("native Git LFS is required: %v %s", err, out)
	}
	root, git := lineEndingRepository(t, "false", "")
	service := NewGitService(root)
	if err := service.LFS(t.Context(), "install"); err != nil {
		t.Fatal(err)
	}
	name := "model [weights].bin"
	if err := service.LFS(t.Context(), "track", name); err != nil {
		t.Fatal(err)
	}
	attributes := git("check-attr", "filter", "--", name)
	if !strings.Contains(attributes, ": filter: lfs") {
		t.Fatalf("literal filename was not tracked: %q", attributes)
	}
	if got := git("config", "--local", "--get", "filter.lfs.required"); strings.TrimSpace(got) != "true" {
		t.Fatal("LFS was not configured locally", got)
	}
	path := filepath.Join(root, name)
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	size := gitComparisonLimit + 4096
	if err := file.Truncate(size); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	change := GitChange{Path: name, Area: GitAreaUntracked}
	check := func(change GitChange) GitComparison {
		t.Helper()
		comparison, err := service.Compare(t.Context(), change)
		if err != nil {
			t.Fatal(err)
		}
		if !comparison.LFS || !comparison.MetadataOnly || len(comparison.Before)+len(comparison.After) != 0 ||
			comparison.Fingerprint != "" {
			t.Fatalf("LFS content was expanded or falsely reviewed: %+v", comparison)
		}
		return comparison
	}
	if comparison := check(change); comparison.AfterSize != size {
		t.Fatal("working file size was lost", comparison.AfterSize)
	}
	if err := service.Stage(t.Context(), name, ".gitattributes"); err != nil {
		t.Fatal(err)
	}
	pointer := git("show", ":"+name)
	if !strings.HasPrefix(pointer, "version https://git-lfs.github.com/spec/v1\n") || len(pointer) > 1024 ||
		!strings.Contains(pointer, fmt.Sprintf("size %d\n", size)) {
		t.Fatalf("staged object is not an LFS pointer: %q", pointer)
	}
	check(GitChange{Path: name, Area: GitAreaStaged})
	git("commit", "-qm", "add model")
	evidence, err := service.InspectCommit(t.Context(), strings.TrimSpace(git("rev-parse", "HEAD")))
	if err != nil {
		t.Fatal(err)
	}
	foundModel := false
	for _, file := range evidence.Comparisons {
		if file.Path == name {
			foundModel = true
			if !file.LFS || !file.MetadataOnly || file.AfterSize != size || len(file.After) != 0 {
				t.Fatal("history rendered an LFS pointer as source text", file)
			}
		}
	}
	if !foundModel {
		t.Fatal("model disappeared from commit review")
	}
	// A failing smudge filter must not run merely to paint the change list.
	git("config", "filter.lfs.process", "")
	git("config", "filter.lfs.smudge", "crude-test-smudge-must-not-run")
	file, err = os.OpenFile(path, os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteAt([]byte("new"), 0); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	check(GitChange{Path: name, Area: GitAreaWorking})
	if annotation, err := service.AnnotateFile(t.Context(), path, ""); err != nil || len(annotation.Hunks) != 0 {
		t.Fatalf("LFS annotation expanded a pointer: %+v %v", annotation, err)
	}
	if err := service.LFS(t.Context(), "track", "../outside"); err == nil {
		t.Fatal("tracking escaped the repository")
	}
	if err := service.LFS(t.Context(), "migrate"); err == nil {
		t.Fatal("history migration was exposed")
	}
}

func TestGitLargeUntrackedFileIsMetadataNotAReadFailure(t *testing.T) {
	root, _ := lineEndingRepository(t, "false", "")
	file, err := os.Create(filepath.Join(root, "large"))
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Truncate(gitComparisonLimit + 1); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	comparison, err := NewGitService(root).Compare(t.Context(), GitChange{Path: "large", Area: GitAreaUntracked})
	if err != nil || !comparison.MetadataOnly || comparison.AfterSize != gitComparisonLimit+1 ||
		len(comparison.After) != 0 || comparison.Fingerprint != "" {
		t.Fatalf("large file cannot be listed safely: %+v %v", comparison, err)
	}
}

func TestGitOutputBoundCannotBeBypassedByIOCopy(t *testing.T) {
	out := &gitBoundedOutput{limit: 32}
	source := bytes.NewReader(bytes.Repeat([]byte("x"), 4096))
	if n, err := io.Copy(out, source); err != nil || n != 4096 || out.Len() != 32 || !out.overflow {
		t.Fatalf("unbounded or undrained Git output: bytes=%d stored=%d overflow=%v err=%v", n, out.Len(), out.overflow, err)
	}
}
