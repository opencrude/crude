package textindex

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/workspace/fileio"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func ready(t *testing.T, x *Index) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !x.Ready() {
		if time.Now().After(deadline) {
			t.Fatal("index did not finish its build")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// Find in Files over the index must return exactly what the disk walk
// returns: same files, same order, same truncation.
func TestFilesMatchFindInFilesWalk(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"a.go":                  "package a\nfunc Alpha() {}\n",
		"a/b.go":                "package a\n// alpha beta\n",
		"a-b/c.txt":             "alpha\n",
		"z/.hidden.go":          "alpha\n",
		"node_modules/x/y.js":   "alpha\n",
		".github/workflows/t.y": "alpha: true\n",
		"bin.dat":               "al\x00pha",
		"notes/ALPHA.md":        "# Alpha\n",
	})
	x := New(root, 0)
	x.Start()
	defer x.Close()
	ready(t, x)
	for _, query := range []string{"alpha", "Alpha", "lph", "a.go", "beta"} {
		for _, opts := range []fileio.ProjectSearchOptions{{}, {CaseSensitive: true}, {WholeWord: true}, {Regex: true}} {
			want, err := fileio.SearchProject(context.Background(), root, query, opts)
			if err != nil {
				t.Fatal(err)
			}
			got, err := fileio.SearchProjectFiles(context.Background(), x.Files(), query, opts)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("query %q %+v:\nindex %d results %+v\nwalk  %d results %+v", query, opts, len(got), got, len(want), want)
			}
		}
	}
}

func TestChangedFilesUpdateTheIndex(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"one.go": "package one\nfunc First() {}\n"})
	x := New(root, 0)
	x.Start()
	defer x.Close()
	ready(t, x)
	writeTree(t, root, map[string]string{"two/two.go": "package two\nfunc SecondWidget() {}\n"})
	x.Changed(filepath.Join(root, "two"))
	if err := os.Remove(filepath.Join(root, "one.go")); err != nil {
		t.Fatal(err)
	}
	x.Changed(filepath.Join(root, "one.go"))
	deadline := time.Now().Add(5 * time.Second)
	for {
		var rels []string
		for _, f := range x.Files() {
			rels = append(rels, f.Rel)
		}
		if strings.Join(rels, ",") == "two/two.go" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("files = %v, want only two/two.go", rels)
		}
		time.Sleep(5 * time.Millisecond)
	}
	got := x.Related("call SecondWidget here", 3, nil)
	if len(got) != 1 || got[0].Rel != "two/two.go" || got[0].Line != 1 {
		t.Fatalf("related = %+v", got)
	}
}

func TestRelatedRanksRareIdentifiersAndSkipsIgnoredFiles(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".gitignore":     "generated/\n",
		"generated/g.go": "func parseInvoiceTotal() {}\n",
	}
	for i := range 20 {
		files[fmt.Sprintf("common/f%d.go", i)] = "package common\nfunc helper() { return }\n"
	}
	files["billing/invoice.go"] = "package billing\n\nfunc parseInvoiceTotal(lines []Line) Money {\n\treturn sum(lines)\n}\n"
	writeTree(t, root, files)
	x := New(root, 0)
	x.Start()
	defer x.Close()
	ready(t, x)
	got := x.Related("total := parseInvoiceTotal(lines) // helper", 2, nil)
	if len(got) == 0 || got[0].Rel != "billing/invoice.go" {
		t.Fatalf("related = %+v, want billing/invoice.go first", got)
	}
	for _, s := range got {
		if strings.HasPrefix(s.Rel, "generated/") {
			t.Fatalf("an ignored file was ranked: %+v", s)
		}
	}
	skipped := x.Related("parseInvoiceTotal", 2, func(path string, _ int) bool { return strings.HasSuffix(path, "invoice.go") })
	if len(skipped) != 0 {
		t.Fatalf("skip was not honored: %+v", skipped)
	}
}

func TestMemoryLimitKeepsPathsForDiskReads(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.txt": strings.Repeat("alpha ", 100), "b.txt": strings.Repeat("alpha ", 100)})
	x := New(root, 700)
	x.Start()
	defer x.Close()
	ready(t, x)
	if x.Complete() {
		t.Fatal("an index over its memory limit reports complete")
	}
	files := x.Files()
	if len(files) != 2 {
		t.Fatalf("files = %d, want both paths kept", len(files))
	}
	got, err := fileio.SearchProjectFiles(context.Background(), files, "alpha", fileio.ProjectSearchOptions{WholeWord: true})
	if err != nil {
		t.Fatal(err)
	}
	want, _ := fileio.SearchProject(context.Background(), root, "alpha", fileio.ProjectSearchOptions{WholeWord: true})
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("disk fallback changed results: %d vs %d", len(got), len(want))
	}
}

func BenchmarkRelated(b *testing.B) {
	root := b.TempDir()
	files := map[string]string{}
	for i := range 2000 {
		var text strings.Builder
		for j := range 120 {
			fmt.Fprintf(&text, "func handler%d_%d(request%d Request) Response { return serve%d(request%d) }\n", i, j, j%17, i%31, j%17)
		}
		files[fmt.Sprintf("pkg%d/file%d.go", i%40, i)] = text.String()
	}
	for name, text := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(text), 0o644)
	}
	x := New(root, 0)
	x.Start()
	defer x.Close()
	for !x.Ready() {
		time.Sleep(10 * time.Millisecond)
	}
	query := "func handler17_3(request3 Request) Response {\n\treturn serve17(request3)\n}"
	b.ResetTimer()
	for b.Loop() {
		x.Related(query, 4, nil)
	}
}

func related(x *Index, query string) []string {
	var rels []string
	for _, s := range x.Related(query, 8, nil) {
		rels = append(rels, s.Rel)
	}
	return rels
}

func until(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not reached", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A deeper ignore file overrides its parent, including a negation, however
// the parallel walk orders the directories.
func TestNestedIgnoreFilesFollowGitPrecedence(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		".gitignore":          "*.gen.go\n",
		"keep/.gitignore":     "!wanted.gen.go\n",
		"keep/wanted.gen.go":  "func zebraWanted() {}\n",
		"keep/other.gen.go":   "func zebraOther() {}\n",
		"top.gen.go":          "func zebraTop() {}\n",
		"plain/plain.go":      "func zebraPlain() {}\n",
		"deep/a/b/.gitignore": "hidden.go\n",
		"deep/a/b/hidden.go":  "func zebraHidden() {}\n",
		"deep/a/shown.go":     "func zebraShown() {}\n",
	}
	writeTree(t, root, files)
	for range 5 { // the walk is parallel; the result must not depend on its order
		x := New(root, 0)
		x.Start()
		ready(t, x)
		got := strings.Join(related(x, "zebraWanted zebraOther zebraTop zebraPlain zebraHidden zebraShown"), ",")
		x.Close()
		for _, want := range []string{"keep/wanted.gen.go", "plain/plain.go", "deep/a/shown.go"} {
			if !strings.Contains(got, want) {
				t.Fatalf("related %q misses %s", got, want)
			}
		}
		for _, ignored := range []string{"keep/other.gen.go", "top.gen.go", "deep/a/b/hidden.go"} {
			if strings.Contains(got, ignored) {
				t.Fatalf("related %q ranks ignored %s", got, ignored)
			}
		}
	}
}

// Editing an ignore file rebuilds the rules: a newly ignored file stops
// ranking and the index becomes complete again.
func TestIgnoreFileChangeRebuildsTheRules(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"gen/out.go": "func quokkaOutput() {}\n", "src/in.go": "func quokkaInput() {}\n"})
	x := New(root, 0)
	x.Start()
	defer x.Close()
	ready(t, x)
	if got := related(x, "quokkaOutput"); len(got) != 1 {
		t.Fatalf("before: %v", got)
	}
	writeTree(t, root, map[string]string{".gitignore": "gen/\n"})
	x.Changed(filepath.Join(root, ".gitignore"))
	until(t, "rebuilt with the new rule", func() bool { return x.Complete() && x.Stats().Builds >= 2 })
	if got := related(x, "quokkaOutput quokkaInput"); strings.Join(got, ",") != "src/in.go" {
		t.Fatalf("after the ignore change: %v", got)
	}
	if err := os.Remove(filepath.Join(root, ".gitignore")); err != nil {
		t.Fatal(err)
	}
	x.Changed(filepath.Join(root, ".gitignore"))
	until(t, "rebuilt without the rule", func() bool { return x.Complete() && x.Stats().Builds >= 3 })
	if got := related(x, "quokkaOutput"); strings.Join(got, ",") != "gen/out.go" {
		t.Fatalf("after removing the ignore file: %v", got)
	}
}

// Changes lost to a full queue are recovered by a build, and a stale index
// ranks nothing until the build lands.
func TestLostChangesRebuildAndStayIncompleteUntilThen(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.go": "func walrusAlpha() {}\n"})
	x := New(root, 0)
	// Before Start no worker drains the queue, so it overflows.
	for range cap(x.updates) + 1 {
		x.Changed(filepath.Join(root, "a.go"))
	}
	if !x.Stats().Incomplete {
		t.Fatal("a full queue did not mark the index incomplete")
	}
	writeTree(t, root, map[string]string{"b.go": "func walrusBeta() {}\n"})
	x.Start()
	defer x.Close()
	ready(t, x)
	until(t, "complete after the build", x.Complete)
	if got := related(x, "walrusBeta"); strings.Join(got, ",") != "b.go" {
		t.Fatalf("the build missed a file that changed while changes were lost: %v", got)
	}
	// While stale, nothing ranks.
	x.mu.Lock()
	x.stale = true
	x.mu.Unlock()
	if x.Complete() || x.Related("walrusBeta", 1, nil) != nil {
		t.Fatal("a stale index ranked results")
	}
	x.Rescan()
	until(t, "complete after rescan", x.Complete)
	if got := related(x, "walrusBeta"); strings.Join(got, ",") != "b.go" {
		t.Fatalf("after rescan: %v", got)
	}
}

// Start, Close and Changed in any order never panic, leak or block.
func TestLifetimeIsIdempotent(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.go": "func a() {}\n"})
	never := New(root, 0)
	never.Close()
	never.Close()
	never.Changed(filepath.Join(root, "a.go"))
	never.Start() // after Close: does nothing
	if never.Ready() {
		t.Fatal("an index started after Close became ready")
	}
	x := New(root, 0)
	x.Start()
	x.Start()
	ready(t, x)
	if x.Stats().Builds != 1 {
		t.Fatalf("Start twice built %d times", x.Stats().Builds)
	}
	x.Close()
	x.Close()
	x.Changed(filepath.Join(root, "a.go"))
	x.Rescan()
	if TotalMemory() < 0 {
		t.Fatalf("memory accounting went negative: %d", TotalMemory())
	}
}

// Close stops a running build promptly.
func TestCloseCancelsABuild(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{}
	for i := range 3000 {
		files[fmt.Sprintf("d%d/f%d.txt", i%50, i)] = strings.Repeat("ocelot ", 200)
	}
	writeTree(t, root, files)
	x := New(root, 0)
	x.Start()
	began := time.Now()
	x.Close()
	if took := time.Since(began); took > 3*time.Second {
		t.Fatalf("Close waited %v for a build", took)
	}
}

// Memory counts chunks and postings, not only text, and goes back to the
// same total when a file is removed.
func TestMemoryCountsChunksAndPostings(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.go": "func badgerAlpha() {}\n"})
	x := New(root, 0)
	x.Start()
	defer x.Close()
	ready(t, x)
	base := x.Stats()
	if base.MemoryBytes <= base.TextBytes {
		t.Fatalf("memory %d does not exceed text %d", base.MemoryBytes, base.TextBytes)
	}
	var text strings.Builder
	for i := range 400 {
		fmt.Fprintf(&text, "func distinctName%d(value%d int) int { return value%d }\n", i, i, i)
	}
	writeTree(t, root, map[string]string{"b.go": text.String()})
	x.Changed(filepath.Join(root, "b.go"))
	until(t, "b.go indexed", func() bool { return x.Stats().Files == 2 })
	grown := x.Stats()
	if extra := grown.MemoryBytes - base.MemoryBytes; extra < 3*int64(text.Len()) {
		t.Fatalf("memory grew by %d for %d bytes of text with %d new terms", extra, text.Len(), grown.Tokens-base.Tokens)
	}
	if err := os.Remove(filepath.Join(root, "b.go")); err != nil {
		t.Fatal(err)
	}
	x.Changed(filepath.Join(root, "b.go"))
	until(t, "b.go removed", func() bool { return x.Stats().Files == 1 })
	if after := x.Stats(); after.MemoryBytes != base.MemoryBytes || after.Tokens != base.Tokens {
		t.Fatalf("memory after removal = %d (tokens %d), want %d (tokens %d)", after.MemoryBytes, after.Tokens, base.MemoryBytes, base.Tokens)
	}
}

// A limit that text alone fits but chunks do not marks the index incomplete,
// so Related returns nothing rather than a partial ranking.
func TestMemoryLimitCountsPostings(t *testing.T) {
	root := t.TempDir()
	var text strings.Builder
	for i := range 200 {
		fmt.Fprintf(&text, "ident%d ", i)
	}
	writeTree(t, root, map[string]string{"a.go": text.String()})
	x := New(root, int64(text.Len())+2048)
	x.Start()
	defer x.Close()
	ready(t, x)
	if x.Complete() || x.Related("ident7", 1, nil) != nil {
		t.Fatal("an index whose postings exceed the limit ranks results")
	}
	if s := x.Stats(); s.MemoryBytes > int64(text.Len())+2048 {
		t.Fatalf("memory %d exceeds the limit", s.MemoryBytes)
	}
}

// A renamed directory leaves its old paths and indexes the new ones with the
// build's hidden-file and ignore rules.
func TestRenamedDirectoryMovesItsFiles(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		".gitignore":     "*.log\n",
		"old/lynx.go":    "func lynxMove() {}\n",
		"old/.hidden.go": "func lynxHidden() {}\n",
		"old/trace.log":  "lynxLog\n",
		"stay/stay.go":   "func lynxStay() {}\n",
	})
	x := New(root, 0)
	x.Start()
	defer x.Close()
	ready(t, x)
	if err := os.Rename(filepath.Join(root, "old"), filepath.Join(root, "new")); err != nil {
		t.Fatal(err)
	}
	x.Changed(filepath.Join(root, "old"))
	x.Changed(filepath.Join(root, "new"))
	until(t, "rename applied", func() bool {
		var rels []string
		for _, f := range x.Files() {
			rels = append(rels, f.Rel)
		}
		return strings.Join(rels, ",") == "new/lynx.go,new/trace.log,stay/stay.go"
	})
	if got := strings.Join(related(x, "lynxMove lynxHidden lynxLog"), ","); got != "new/lynx.go" {
		t.Fatalf("related after rename = %q", got)
	}
}

func TestUnreadableDirectoryKeepsRankingButNotCompleteCoverage(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"blocked/one.txt": "blockedIdentifier", "visible.txt": "visibleIdentifier"})
	blocked := filepath.Join(root, "blocked")
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(blocked, 0700) }()
	if _, err := os.ReadDir(blocked); !os.IsPermission(err) {
		t.Skip("fixture needs an enforced directory permission denial")
	}
	x := New(root, 0)
	x.Start()
	defer x.Close()
	ready(t, x)
	if x.Complete() || !x.Stats().Incomplete || len(x.Related("visibleIdentifier", 1, nil)) != 1 {
		t.Fatalf("readable ranking must not imply complete coverage: %+v", x.Stats())
	}
}
