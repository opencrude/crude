package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/localhistory"
	"crude/textdiff"
)

// lineEndingRepository commits README.md as LF under an explicit line-ending
// policy and leaves the worktree in the form Git checks out for it.
func lineEndingRepository(t *testing.T, autocrlf, attributes string) (string, func(...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git executable is required")
	}
	root := t.TempDir()
	gitRun := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", root}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
		return string(out)
	}
	gitRun("init", "-q")
	gitRun("config", "user.name", "Crude Test")
	gitRun("config", "user.email", "crude@example.test")
	gitRun("config", "commit.gpgsign", "false")
	gitRun("config", "core.autocrlf", autocrlf)
	if attributes != "" {
		if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte(attributes), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Title\n\none\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	gitRun("add", ".")
	gitRun("commit", "-q", "-m", "initial")
	// Re-checkout so the worktree holds Git's checkout representation.
	if err := os.Remove(filepath.Join(root, "README.md")); err != nil {
		t.Fatal(err)
	}
	gitRun("checkout", "--", "README.md")
	return root, gitRun
}

func readmeBytes(t *testing.T, root string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestGitStatusMatchesGitForCheckoutLineEndings(t *testing.T) {
	for _, tc := range []struct{ name, autocrlf, attributes, wantDisk string }{
		{"autocrlf_checkout_crlf", "true", "", "\r\n"},
		{"eol_attribute_crlf", "false", "*.md text eol=crlf\n", "\r\n"},
		{"plain_lf", "false", "", "\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, gitRun := lineEndingRepository(t, tc.autocrlf, tc.attributes)
			if disk := readmeBytes(t, root); !strings.Contains(disk, tc.wantDisk) || tc.wantDisk == "\n" && strings.Contains(disk, "\r") {
				t.Fatalf("fixture checkout bytes = %q", disk)
			}
			// Editors, agents and sync tools touch files without changing bytes;
			// stale index stat data must make status compare content through
			// Git's conversion, never hash raw checkout bytes.
			later := time.Now().Add(time.Hour)
			if err := os.Chtimes(filepath.Join(root, "README.md"), later, later); err != nil {
				t.Fatal(err)
			}
			if got := gitRun("status", "--porcelain"); got != "" {
				t.Fatalf("fixture is not Git-clean: %q", got)
			}
			service := NewGitService(root)
			snapshot, err := service.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Changes) != 0 {
				t.Fatalf("Git-clean checkout reported as changed: %#v", snapshot.Changes)
			}

			// A real edit is reported once, in the working area only.
			disk := readmeBytes(t, root)
			if err := os.WriteFile(filepath.Join(root, "README.md"), []byte(strings.Replace(disk, "two", "TWO", 1)), 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot, err = service.Snapshot(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			if len(snapshot.Changes) != 1 || snapshot.Changes[0].Path != "README.md" || snapshot.Changes[0].Area != GitAreaWorking {
				t.Fatalf("edited file changes = %#v", snapshot.Changes)
			}
		})
	}
}

func TestGitStatusLFCheckoutUnderAutoCRLFIsClean(t *testing.T) {
	// Agents and cross-platform tools write LF files on Windows; Git cleans
	// them to the same blob, so neither status nor the gutter may flag them.
	root, gitRun := lineEndingRepository(t, "true", "")
	path := filepath.Join(root, "README.md")
	if err := os.WriteFile(path, []byte("# Title\n\none\ntwo\nthree\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// Git's verdict is authoritative: until its index is refreshed it may list
	// the file (stat size changed), with an empty diff. Status mirrors Git.
	gitStatus := gitRun("status", "--porcelain")
	service := NewGitService(root)
	snapshot, err := service.Snapshot(t.Context())
	if err != nil || (gitStatus == "") != (len(snapshot.Changes) == 0) {
		t.Fatalf("LF checkout changes = %#v, %v; git status %q", snapshot.Changes, err, gitStatus)
	}
	gitRun("diff", "--quiet", "--", "README.md") // exit 1 (a content diff) fails the test
	gitRun("add", "README.md")
	if snapshot, err = service.Snapshot(t.Context()); err != nil || len(snapshot.Changes) != 0 {
		t.Fatalf("refreshed LF checkout changes = %#v, %v", snapshot.Changes, err)
	}
	annotation, err := service.AnnotateFile(t.Context(), path, readmeBytes(t, root))
	if err != nil || len(annotation.Hunks) != 0 {
		t.Fatalf("LF checkout gutter hunks = %#v, %v", annotation.Hunks, err)
	}
}

func TestGitComparisonStageAndDiscardUseGitConversion(t *testing.T) {
	root, gitRun := lineEndingRepository(t, "true", "")
	path := filepath.Join(root, "README.md")
	edited := strings.Replace(readmeBytes(t, root), "two", "TWO", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewGitService(root)
	change := GitChange{Path: "README.md", AbsolutePath: path, Area: GitAreaWorking}
	comparison, err := service.Compare(t.Context(), change)
	if err != nil {
		t.Fatal(err)
	}
	if comparison.Before != "# Title\r\n\r\none\r\ntwo\r\nthree\r\n" || comparison.After != edited {
		t.Fatalf("working comparison is not in checkout form: before=%q after=%q", comparison.Before, comparison.After)
	}
	if hunks := textdiff.ReviewHunks(comparison.Before, comparison.After); len(hunks) != 1 || hunks[0].OldStart != 3 ||
		strings.Join(hunks[0].OldLines, "") != "two\r\n" || strings.Join(hunks[0].NewLines, "") != "TWO\r\n" {
		t.Fatalf("one-line edit review hunks = %#v", hunks)
	}

	// Staging cleans exactly as `git add`: the index blob stays LF, so the
	// worktree is clean against it and only the staged change remains.
	if err := service.StageReviewed(t.Context(), comparison); err != nil {
		t.Fatal(err)
	}
	if got := gitRun("show", ":README.md"); got != "# Title\n\none\nTWO\nthree\n" {
		t.Fatalf("staged blob = %q", got)
	}
	if got := gitRun("status", "--porcelain"); got != "M  README.md\n" {
		t.Fatalf("status after reviewed stage = %q", got)
	}

	// Unstage, then discard: disk receives the checkout form Git would write.
	staged, err := service.Compare(t.Context(), GitChange{Path: "README.md", AbsolutePath: path, Area: GitAreaStaged})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.UnstageReviewed(t.Context(), staged); err != nil {
		t.Fatal(err)
	}
	if got := gitRun("status", "--porcelain"); got != " M README.md\n" {
		t.Fatalf("status after reviewed unstage = %q", got)
	}
	store, err := localhistory.Open(filepath.Join(t.TempDir(), "history"), root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	owner := &Workspace{RootPath: root, Git: service, LocalHistory: store}
	preview, err := owner.PreviewGitDiscard(t.Context(), change)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := owner.DiscardGitReviewed(t.Context(), preview); err != nil {
		t.Fatal(err)
	}
	if got := readmeBytes(t, root); got != "# Title\r\n\r\none\r\ntwo\r\nthree\r\n" {
		t.Fatalf("discard wrote %q, want Git's checkout bytes", got)
	}
	if got := gitRun("status", "--porcelain"); got != "" {
		t.Fatalf("status after discard = %q", got)
	}
}

func TestGitHunkStageKeepsRepositoryLineEndings(t *testing.T) {
	root, gitRun := lineEndingRepository(t, "true", "")
	path := filepath.Join(root, "README.md")
	edited := strings.Replace(strings.Replace(readmeBytes(t, root), "one", "ONE", 1), "three", "THREE", 1)
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	service := NewGitService(root)
	comparison, err := service.Compare(t.Context(), GitChange{Path: "README.md", AbsolutePath: path, Area: GitAreaWorking})
	if err != nil {
		t.Fatal(err)
	}
	hunks := textdiff.ReviewHunks(comparison.Before, comparison.After)
	if len(hunks) != 2 {
		t.Fatalf("hunks = %#v", hunks)
	}
	first := GitHunkRange{OldStart: hunks[0].OldStart, OldEnd: hunks[0].OldStart + len(hunks[0].OldLines), NewStart: hunks[0].NewStart, NewEnd: hunks[0].NewStart + len(hunks[0].NewLines)}
	if err := service.StageHunk(t.Context(), comparison, first); err != nil {
		t.Fatal(err)
	}
	if got := gitRun("show", ":README.md"); got != "# Title\n\nONE\ntwo\nthree\n" {
		t.Fatalf("hunk-staged blob = %q", got)
	}
	if got := gitRun("status", "--porcelain"); got != "MM README.md\n" {
		t.Fatalf("status after hunk stage = %q", got)
	}
}

func TestParseGitStatusZ(t *testing.T) {
	status, err := parseGitStatusZ([]byte("R  new.go\x00old.go\x00 M a b.txt\x00?? dir/u.txt\x00UU c.go\x00AA d.go\x00!! ignored\x00"))
	if err != nil {
		t.Fatal(err)
	}
	if file := status["new.go"]; file == nil || file.Staging != 'R' || file.Extra != "old.go" {
		t.Fatalf("rename = %#v", file)
	}
	if file := status["a b.txt"]; file == nil || file.Staging != ' ' || file.Worktree != 'M' {
		t.Fatalf("modified = %#v", file)
	}
	if file := status["dir/u.txt"]; file == nil || file.Staging != '?' || file.Worktree != '?' {
		t.Fatalf("untracked = %#v", file)
	}
	for _, name := range []string{"c.go", "d.go"} {
		if file := status[name]; file == nil || file.Staging != 'U' || file.Worktree != 'U' {
			t.Fatalf("unmerged %s = %#v", name, file)
		}
	}
	if _, ok := status["ignored"]; ok || len(status) != 5 {
		t.Fatalf("status = %#v", status)
	}
	if _, err := parseGitStatusZ([]byte("R  new.go\x00")); err == nil {
		t.Fatal("rename without source accepted")
	}
}

func TestGitWithoutExecutableFailsWithInstallGuidance(t *testing.T) {
	root, _ := lineEndingRepository(t, "false", "")
	t.Setenv("PATH", t.TempDir())
	service := NewGitService(root)
	if _, err := service.Snapshot(t.Context()); err != ErrGitNotInstalled {
		t.Fatalf("snapshot without git: %v", err)
	}
	if _, err := service.AnnotateFile(t.Context(), filepath.Join(root, "README.md"), "x\n"); err != ErrGitNotInstalled {
		t.Fatalf("annotation without git: %v", err)
	}
}
