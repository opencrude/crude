package remote

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"crude/core"
	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/types/setting"
	"crude/workspace"
)

func TestCanonicalClientPathKeepsHostVolume(t *testing.T) {
	for _, tc := range []struct{ root, input, want string }{
		{`C:\`, "", `C:/`}, {`C:\project`, `C:\`, `C:/`}, {`C:\`, `a/..`, `C:/`},
		{`C:\project`, `D:\..\`, `D:/`}, {`\\server\share`, "", `//server/share`},
		{`\\server\share`, `../file`, `//server/share/file`},
		{`C:\project`, `\\server\share\..\file`, `//server/share/file`},
		{`\\?\UNC\server\share\project`, `../../file`, `//?/UNC/server/share/file`},
		{`\\?\C:\project`, `../..`, `//?/C:/`},
		{`/srv/project`, `dir/../note`, `/srv/project/note`},
		{`/srv/project`, `a\b`, `/srv/project/a\b`},
		{`/srv/project`, `C:/file`, `/srv/project/C:/file`},
		{`/\host/project`, `a\b`, `/\host/project/a\b`},
	} {
		w := &Workspace{root: tc.root}
		if got := w.CanonicalClientPath(tc.input); got != tc.want {
			t.Errorf("root %q path %q: got %q, want %q", tc.root, tc.input, got, tc.want)
		}
	}
}

func TestBrowseDirectoryAtAttachedVolumeRoot(t *testing.T) {
	// Read the actual root without creating entries there. The ancestry of
	// our temp folder supplies a known child on this host (including Windows).
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.VolumeName(dir) + string(filepath.Separator)
	rel, err := filepath.Rel(root, dir)
	if err != nil {
		t.Fatal(err)
	}
	wantName, _, _ := strings.Cut(rel, string(filepath.Separator))
	server, err := core.New(func(path string) (*workspace.Workspace, error) {
		return workspace.NewWorkspace(path, "", "", setting.Settings{}, nil), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	dial := func(callCtx context.Context, id string) (*coreclient.Client, error) {
		a, b := net.Pipe()
		workers.Add(1)
		go func() { defer workers.Done(); _ = core.ServeConnection(ctx, server, a) }()
		return coreclient.Dial(callCtx, coreclient.SingleCarrier(b), id)
	}
	w, err := Attach(t.Context(), Options{Root: root, Dial: dial, Entry: coreprotocol.EntryIntent{Explicit: true}})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	entries, err := w.BrowseDirectory(t.Context(), w.RootPath())
	if err != nil {
		t.Fatalf("browse attached root %q: %v", w.RootPath(), err)
	}
	found := false
	for _, entry := range entries {
		if entry.Path != filepath.Join(w.RootPath(), entry.Name) {
			t.Fatalf("unexpected child path %q for name %q", entry.Path, entry.Name)
		}
		if entry.Name == wantName && entry.IsDir {
			found = true
		}
	}
	if !found {
		t.Fatalf("attached root %q did not list %q", w.RootPath(), wantName)
	}
}
