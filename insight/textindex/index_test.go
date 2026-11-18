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

	"crude/fileio"
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
