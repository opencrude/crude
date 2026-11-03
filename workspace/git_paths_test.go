package workspace

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func pathTestGit(t *testing.T, root string, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "git", append([]string{"-C", root}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return out
}

func pathTestRepository(t *testing.T) (string, *GitService) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required")
	}
	root := t.TempDir()
	pathTestGit(t, root, "init", "-q")
	pathTestGit(t, root, "config", "user.name", "Path Test")
	pathTestGit(t, root, "config", "user.email", "paths@example.test")
	pathTestGit(t, root, "config", "core.autocrlf", "false")
	return root, NewGitService(root)
}

func pathTestSet(t *testing.T, root string, want []string, args ...string) {
	t.Helper()
	out := pathTestGit(t, root, args...)
	var got []string
	if len(out) > 0 {
		got = strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00")
	}
	got = slices.Sorted(slices.Values(got))
	expected := slices.Sorted(slices.Values(want))
	if !slices.Equal(got, expected) {
		t.Fatalf("Git path set mismatch: got %d paths, want %d; first difference: %s", len(got), len(expected), pathTestDifference(got, expected))
	}
}

func pathTestDifference(got, want []string) string {
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			return fmt.Sprintf("got %q, want %q", got[i], want[i])
		}
	}
	return "different lengths"
}

func pathTestMutation(t *testing.T, label string, mutate func() error) {
	t.Helper()
	start := time.Now()
	if err := mutate(); err != nil {
		text := err.Error()
		if len(text) > 400 {
			text = text[:200] + " ... " + text[len(text)-200:]
		}
		t.Fatalf("%s after %s: %s", label, time.Since(start), text)
	}
	t.Logf("%s: %s", label, time.Since(start))
}

func TestGitExactPathsBulkStageUnstageAndCommit(t *testing.T) {
	root, service := pathTestRepository(t)
	paths := make([]string, 0, 2050)
	work := make(map[string]string)
	for i := range 2048 {
		name := fmt.Sprintf("selected %04d 世界 long filename for command line.txt", i)
		paths = append(paths, name)
		work[name] = "initial " + name + "\n"
	}
	paths = append(paths, "delete.txt", "rename before.txt")
	work["delete.txt"], work["rename before.txt"] = "delete me\n", "rename me\n"
	work["other staged.txt"], work["not selected.txt"] = "keep staged\n", "keep untracked\n"
	for name, content := range work {
		reviewWrite(t, root, name, content)
	}
	if size := len(strings.Join(paths, "\x00")); size <= 32767 {
		t.Fatalf("fixture does not exceed Windows command line: %d bytes", size)
	} else {
		t.Logf("%d selected paths, %d path bytes", len(paths), size)
	}
	if err := service.Stage(t.Context(), "other staged.txt"); err != nil {
		t.Fatal(err)
	}
	allStaged := append(slices.Clone(paths), "other staged.txt")
	pathTestMutation(t, "stage bulk unborn", func() error { return service.Stage(t.Context(), paths...) })
	pathTestSet(t, root, allStaged, "ls-files", "-z")
	pathTestMutation(t, "unstage bulk unborn", func() error { return service.Unstage(t.Context(), paths...) })
	pathTestSet(t, root, []string{"other staged.txt"}, "ls-files", "-z")
	if got := string(pathTestGit(t, root, "show", ":other staged.txt")); got != work["other staged.txt"] {
		t.Fatalf("unborn unstage changed unrelated staged bytes: %q", got)
	}
	pathTestWorkBytes(t, root, work, nil)

	// Commit-all uses Stage followed by CommitStaged, with the exact painted set.
	pathTestMutation(t, "stage bulk for commit", func() error { return service.Stage(t.Context(), paths...) })
	pathTestMutation(t, "commit bulk exact set", func() error {
		return service.CommitStaged(t.Context(), allStaged, "bulk initial")
	})
	pathTestSet(t, root, allStaged, "ls-tree", "-r", "--name-only", "-z", "HEAD")

	for _, name := range paths[:2048] {
		work[name] = "changed " + name + "\n"
		reviewWrite(t, root, name, work[name])
	}
	if err := os.Remove(filepath.Join(root, "delete.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(root, "rename before.txt"), filepath.Join(root, "rename after.txt")); err != nil {
		t.Fatal(err)
	}
	delete(work, "delete.txt")
	delete(work, "rename before.txt")
	work["rename after.txt"] = "rename me\n"
	paths = append(paths, "rename after.txt")
	work["other staged.txt"] = "unrelated staged change\n"
	reviewWrite(t, root, "other staged.txt", work["other staged.txt"])
	if err := service.Stage(t.Context(), "other staged.txt"); err != nil {
		t.Fatal(err)
	}
	allStaged = append(slices.Clone(paths), "other staged.txt")
	pathTestMutation(t, "stage bulk tracked edits deletions rename", func() error { return service.Stage(t.Context(), paths...) })
	pathTestSet(t, root, allStaged, "diff", "--cached", "--no-renames", "--name-only", "-z")
	pathTestMutation(t, "unstage bulk tracked", func() error { return service.Unstage(t.Context(), paths...) })
	pathTestSet(t, root, []string{"other staged.txt"}, "diff", "--cached", "--no-renames", "--name-only", "-z")
	if got := string(pathTestGit(t, root, "show", ":other staged.txt")); got != work["other staged.txt"] {
		t.Fatalf("unstage changed unrelated staged bytes: %q", got)
	}
	pathTestWorkBytes(t, root, work, []string{"delete.txt", "rename before.txt"})
}

func pathTestWorkBytes(t *testing.T, root string, want map[string]string, absent []string) {
	t.Helper()
	for name, content := range want {
		got, err := os.ReadFile(filepath.Join(root, name))
		if err != nil || string(got) != content {
			t.Fatalf("working bytes changed for %q: err=%v", name, err)
		}
	}
	for _, name := range absent {
		if _, err := os.Stat(filepath.Join(root, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("removed path %q reappeared: %v", name, err)
		}
	}
}

func TestGitExactPathsAreLiteral(t *testing.T) {
	root, service := pathTestRepository(t)
	selected := []string{"[ab].txt", "-leading.txt", " leading space.txt", "space 世界.txt", `"quoted".txt`}
	if runtime.GOOS == "windows" {
		selected = selected[:len(selected)-1] // Windows forbids double quotes in filenames.
	} else {
		selected = append(selected, ":(glob)*.txt", "star*.txt", "question?.txt", "trailing space.txt ", "line\nbreak.txt")
	}
	work := map[string]string{"a.txt": "must stay out\n", "b.txt": "must stay out too\n", "star-other.txt": "not selected\n"}
	for _, name := range selected {
		work[name] = "literal " + name + "\n"
	}
	for name, content := range work {
		reviewWrite(t, root, name, content)
	}
	if err := service.Stage(t.Context(), selected...); err != nil {
		t.Fatal(err)
	}
	pathTestSet(t, root, selected, "ls-files", "-z")
	if err := service.Unstage(t.Context(), selected...); err != nil {
		t.Fatal(err)
	}
	pathTestSet(t, root, nil, "ls-files", "-z")
	pathTestWorkBytes(t, root, work, nil)

	if err := service.Stage(t.Context(), selected...); err != nil {
		t.Fatal(err)
	}
	if err := service.CommitStaged(t.Context(), selected, "literal initial"); err != nil {
		t.Fatal(err)
	}
	for _, name := range selected {
		work[name] = "changed literal " + name + "\n"
		reviewWrite(t, root, name, work[name])
	}
	if err := service.Stage(t.Context(), "a.txt"); err != nil {
		t.Fatal(err)
	}
	if err := service.Stage(t.Context(), selected...); err != nil {
		t.Fatal(err)
	}
	pathTestSet(t, root, append(slices.Clone(selected), "a.txt"), "diff", "--cached", "--name-only", "-z")
	if err := service.Unstage(t.Context(), selected...); err != nil {
		t.Fatal(err)
	}
	pathTestSet(t, root, []string{"a.txt"}, "diff", "--cached", "--name-only", "-z")
	pathTestWorkBytes(t, root, work, nil)
}

func TestGitExactPathsCancellationPreservesIndex(t *testing.T) {
	for _, unstage := range []bool{false, true} {
		for _, queued := range []bool{false, true} {
			t.Run(fmt.Sprintf("unstage=%v/queued=%v", unstage, queued), func(t *testing.T) {
				root, service := pathTestRepository(t)
				reviewWrite(t, root, "selected.txt", "selected\n")
				reviewWrite(t, root, "other.txt", "other\n")
				if err := service.Stage(t.Context(), "other.txt"); err != nil {
					t.Fatal(err)
				}
				if unstage {
					if err := service.Stage(t.Context(), "selected.txt"); err != nil {
						t.Fatal(err)
					}
				}
				before, err := os.ReadFile(filepath.Join(root, ".git", "index"))
				if err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				if queued {
					if err := service.lock(t.Context()); err != nil {
						t.Fatal(err)
					}
					defer service.unlock()
				} else {
					cancel()
				}
				done := make(chan error, 1)
				go func() {
					if unstage {
						done <- service.Unstage(ctx, "selected.txt")
					} else {
						done <- service.Stage(ctx, "selected.txt")
					}
				}()
				cancel()
				select {
				case err := <-done:
					if !errors.Is(err, context.Canceled) {
						t.Fatalf("cancel error=%v", err)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("cancelled exact-path mutation did not leave repository queue")
				}
				after, err := os.ReadFile(filepath.Join(root, ".git", "index"))
				if err != nil || !bytes.Equal(before, after) {
					t.Fatalf("cancelled operation changed index: %v", err)
				}
				pathTestWorkBytes(t, root, map[string]string{"selected.txt": "selected\n", "other.txt": "other\n"}, nil)
			})
		}
	}
}

func TestGitExactPathsRejectNullBeforeEffects(t *testing.T) {
	root, service := pathTestRepository(t)
	reviewWrite(t, root, "selected.txt", "selected\n")
	reviewWrite(t, root, "other.txt", "other\n")
	if err := service.Stage(t.Context(), "other.txt"); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	for _, operation := range []func(context.Context, ...string) error{service.Stage, service.Unstage} {
		for _, bad := range []string{"selected.txt\x00other.txt", "null\x00/../selected.txt"} {
			if err := operation(t.Context(), "selected.txt", bad); err == nil {
				t.Fatal("accepted a NUL-delimited path injection")
			}
			after, err := os.ReadFile(filepath.Join(root, ".git", "index"))
			if err != nil || !bytes.Equal(before, after) {
				t.Fatalf("invalid path changed index: %v", err)
			}
		}
	}
	pathTestWorkBytes(t, root, map[string]string{"selected.txt": "selected\n", "other.txt": "other\n"}, nil)
}

func TestCleanGitPathsPreservesFilenameWhitespace(t *testing.T) {
	got := cleanGitPaths([]string{"", ".", "./a.txt", "a.txt", " leading.txt", "trailing.txt ", "line\nbreak.txt"})
	want := []string{"a.txt", " leading.txt", "trailing.txt ", "line\nbreak.txt"}
	if !slices.Equal(got, want) {
		t.Fatalf("paths=%q want=%q", got, want)
	}
}

func TestGitSnapshotReadIdentity(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required")
	}
	root := t.TempDir()
	service := NewGitService(root)
	first, err := service.Snapshot(t.Context())
	if err != nil || first.SourceID == "" || first.Sequence != 1 || first.RepositoryRoot != "" {
		t.Fatalf("first non-repository snapshot=%+v err=%v", first, err)
	}
	second, err := service.Snapshot(t.Context())
	if err != nil || second.SourceID != first.SourceID || second.Sequence != 2 {
		t.Fatalf("second snapshot=%+v err=%v", second, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Snapshot(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled snapshot error=%v", err)
	}
	pathTestGit(t, root, "init", "-q")
	third, err := service.Snapshot(t.Context())
	if err != nil || third.SourceID != first.SourceID || third.Sequence != 3 || third.RepositoryRoot == "" {
		t.Fatalf("repository snapshot=%+v err=%v", third, err)
	}
	replacement, err := NewGitService(root).Snapshot(t.Context())
	if err != nil || replacement.SourceID == "" || replacement.SourceID == first.SourceID || replacement.Sequence != 1 {
		t.Fatalf("replacement snapshot=%+v err=%v", replacement, err)
	}
}
