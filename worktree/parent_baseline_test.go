package worktree

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParentCheckoutIsExactChildBaseline(t *testing.T) {
	root, store, s := fixture(t)
	put(t, root, "a.txt", []byte("logical dirty\ntwo\n"))
	before := gitTest(t, root, "status", "--porcelain=v1")
	a, err := s.Create(t.Context(), Request{Owner: testOwner, Mode: Isolated})
	if err != nil {
		t.Fatal(err)
	}
	put(t, a.Root, "a.txt", []byte("parent A\ntwo\n"))
	for _, mode := range []Mode{Shared, Isolated} {
		c, err := s.Create(t.Context(), Request{Owner: Owner{AgentID: "child", RunID: string(mode)}, Mode: mode, SourceContextID: a.ID, Documents: map[string]Document{"a.txt": {Revision: 9, Exists: true, Data: []byte("STALE logical overlay")}}})
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(c.Root, "a.txt"))
		if err != nil || string(got) != "parent A\ntwo\n" {
			t.Fatalf("child bytes %q: %v", got, err)
		}
		if mode == Shared && c.Root != a.Root {
			t.Fatal("shared child not on parent")
		}
		if mode == Isolated {
			put(t, c.Root, "a.txt", []byte("parent A\nchild B\n"))
			p, err := s.Review(t.Context(), c.ID)
			if err != nil || len(p.Changes) != 1 || string(p.Changes[0].Before.Data) != "parent A\ntwo\n" {
				t.Fatalf("child delta %+v %v", p, err)
			}
		}
	}
	reopened, err := Open(root, store, Options{})
	if err != nil || len(reopened.List()) != 3 {
		t.Fatalf("passive reopen %v", err)
	}
	if _, err := s.Create(t.Context(), Request{Owner: testOwner, Mode: Shared, SourceContextID: strings.Repeat("0", 32)}); err == nil {
		t.Fatal("unknown source accepted")
	}
	if got := gitTest(t, root, "status", "--porcelain=v1"); got != before {
		t.Fatalf("logical status changed %q -> %q", before, got)
	}
	got, _ := os.ReadFile(filepath.Join(root, "a.txt"))
	if string(got) != "logical dirty\ntwo\n" {
		t.Fatal("logical bytes changed")
	}
}
