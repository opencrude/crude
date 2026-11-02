package toolstore

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInstallEachArchiveKind(t *testing.T) {
	tree := []entry{
		dir("demo/"),
		dir("demo/bin/"),
		exe("demo/bin/demo-server", "#!/bin/sh\necho demo\n"),
		file("demo/README.txt", "readme"),
		file("demo/lib/data.txt", "data"),
	}
	cases := []struct {
		name, archive, bin string
		data               []byte
		files              map[string]string
	}{
		{"zip", ArchiveZip, "demo/bin/demo-server", zipBytes(t, tree), map[string]string{"demo/README.txt": "readme", "demo/lib/data.txt": "data"}},
		{"tar.gz", ArchiveTarGz, "demo/bin/demo-server", gzBytes(t, tarBytes(t, tree)), map[string]string{"demo/README.txt": "readme", "demo/lib/data.txt": "data"}},
		{"tar.xz", ArchiveTarXz, "demo/bin/demo-server", xzBytes(t, tarBytes(t, tree)), map[string]string{"demo/README.txt": "readme", "demo/lib/data.txt": "data"}},
		{"gz", ArchiveGz, "demo-server", gzBytes(t, []byte("#!/bin/sh\necho demo\n")), nil},
		{"raw", ArchiveRaw, "demo-server", []byte("#!/bin/sh\necho demo\n"), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := newStore(t)
			server := serve(t, map[string][]byte{"/artifact": tc.data})
			tool, err := store.Install(context.Background(), request(server.URL+"/artifact", tc.archive, tc.bin))
			if err != nil {
				t.Fatal(err)
			}
			digest := digestOf(tc.data)
			wantDir := filepath.Join(store.root, "demo-server", "1.2.3-"+digest[:12])
			if tool.ID != "demo-server/1.2.3-"+digest[:12] || tool.Dir != wantDir || tool.SHA256 != digest {
				t.Fatalf("tool identity = %q %q %q", tool.ID, tool.Dir, tool.SHA256)
			}
			if tool.DigestOrigin != DigestFirstDownload || tool.InstalledBy != "user" || tool.Approval != "approval-1" || tool.Source != "test-registry" {
				t.Fatalf("tool provenance = %+v", tool)
			}
			if got := readFile(t, tool.Executable); got != "#!/bin/sh\necho demo\n" {
				t.Fatalf("executable content = %q", got)
			}
			for name, body := range tc.files {
				if got := readFile(t, filepath.Join(tool.Dir, contentDir, filepath.FromSlash(name))); got != body {
					t.Fatalf("%s = %q, want %q", name, got, body)
				}
			}
			if _, err := os.Stat(filepath.Join(tool.Dir, manifestName)); err != nil {
				t.Fatalf("manifest is missing: %v", err)
			}
			if runtime.GOOS != "windows" {
				info, err := os.Stat(tool.Executable)
				if err != nil || info.Mode().Perm() != 0o555 {
					t.Fatalf("executable mode = %v, %v; want 0555", info.Mode(), err)
				}
			}
			found, ok := store.Lookup("demo-server", "1.2.3")
			if !ok || found.ID != tool.ID {
				t.Fatalf("Lookup = %+v, %v", found, ok)
			}
			tools, total, err := store.List()
			if err != nil || len(tools) != 1 || total != tool.Size || tool.Size == 0 {
				t.Fatalf("List = %d tools, total %d, size %d, %v", len(tools), total, tool.Size, err)
			}
			// A second install of the same version downloads nothing.
			again, err := store.Install(context.Background(), request(server.URL+"/artifact", tc.archive, tc.bin))
			if err != nil || again.ID != tool.ID || server.hits.Load() != 1 {
				t.Fatalf("second install = %q, %v, hits %d", again.ID, err, server.hits.Load())
			}
			history := events(t, store)
			if len(history) != 1 || history[0].Kind != EventInstall || history[0].ToolID != tool.ID || history[0].Approval != "approval-1" {
				t.Fatalf("history = %+v", history)
			}
			requireEmptyStaging(t, store)
			// Reopen reads the same tool from its manifest.
			reopened, err := Open(store.root)
			if err != nil {
				t.Fatal(err)
			}
			if found, ok := reopened.Lookup("demo-server", "1.2.3"); !ok || found.Executable != tool.Executable || !found.InstalledAt.Equal(tool.InstalledAt) {
				t.Fatalf("reopened Lookup = %+v, %v", found, ok)
			}
		})
	}
}

func TestInstallVerifiesExpectedDigest(t *testing.T) {
	data := []byte("binary")
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool": data})

	wrong := request(server.URL+"/tool", ArchiveRaw, "tool")
	wrong.ExpectedSHA256 = strings.Repeat("0", 64)
	_, err := store.Install(context.Background(), wrong)
	requireErrorContains(t, err, "differs from the expected digest")
	if _, ok := store.Lookup("demo-server", "1.2.3"); ok {
		t.Fatal("a tool with the wrong digest was installed")
	}
	requireEmptyStaging(t, store)
	history := events(t, store)
	if len(history) != 1 || history[0].Kind != EventInstallFailed || !strings.Contains(history[0].Detail, "differs") {
		t.Fatalf("history = %+v", history)
	}

	right := request(server.URL+"/tool", ArchiveRaw, "tool")
	right.ExpectedSHA256 = strings.ToUpper(digestOf(data))
	tool, err := store.Install(context.Background(), right)
	if err != nil || tool.DigestOrigin != DigestExpected {
		t.Fatalf("install = %+v, %v", tool, err)
	}

	// A different expected digest for the installed version fails closed.
	_, err = store.Install(context.Background(), wrong)
	requireErrorContains(t, err, "already installed with the digest")
}

func TestInstallValidatesRequest(t *testing.T) {
	base := request("https://example.invalid/tool", ArchiveRaw, "tool")
	cases := map[string]func(*Request){
		"package with slash":    func(r *Request) { r.Package = "a/b" },
		"package with dot":      func(r *Request) { r.Package = ".hidden" },
		"version traversal":     func(r *Request) { r.Version = ".." },
		"empty version":         func(r *Request) { r.Version = "" },
		"unknown archive":       func(r *Request) { r.Archive = "rar" },
		"absolute bin":          func(r *Request) { r.Bin = "/bin/sh" },
		"escaping bin":          func(r *Request) { r.Bin = "../tool" },
		"nested raw bin":        func(r *Request) { r.Bin = "bin/tool" },
		"bad digest":            func(r *Request) { r.ExpectedSHA256 = "abc" },
		"no download budget":    func(r *Request) { r.MaxDownloadBytes = 0 },
		"no extraction budget":  func(r *Request) { r.MaxExtractedBytes = 0 },
		"no approval":           func(r *Request) { r.Approval = "" },
		"no installer":          func(r *Request) { r.InstalledBy = " " },
		"plain http":            func(r *Request) { r.URL = "http://example.invalid/tool" },
		"credentials in url":    func(r *Request) { r.URL = "https://user:secret@example.invalid/tool" },
		"negative entry budget": func(r *Request) { r.MaxEntries = -1 },
	}
	store := newStore(t)
	store.allowLoopbackHTTP = false
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := base
			mutate(&r)
			if _, err := store.Install(context.Background(), r); err == nil {
				t.Fatal("Install accepted an invalid request")
			}
		})
	}
	if history := events(t, store); len(history) != len(cases) {
		t.Fatalf("history has %d failures, want %d", len(history), len(cases))
	}
	requireEmptyStaging(t, store)
}

func TestInstallRefusesLoopbackHTTPOutsideTests(t *testing.T) {
	store := newStore(t)
	store.allowLoopbackHTTP = false
	server := serve(t, map[string][]byte{"/tool": []byte("x")})
	_, err := store.Install(context.Background(), request(server.URL+"/tool", ArchiveRaw, "tool"))
	requireErrorContains(t, err, "must use HTTPS")
	if server.hits.Load() != 0 {
		t.Fatal("the store contacted a plain HTTP server")
	}
}

func TestDownloadBudgetsRedirectsAndStatus(t *testing.T) {
	big := []byte(strings.Repeat("x", 4096))
	mux := http.NewServeMux()
	mux.HandleFunc("/big", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(big) })
	mux.HandleFunc("/chunked", func(w http.ResponseWriter, r *http.Request) {
		for range 4 {
			_, _ = w.Write(big[:1024])
			w.(http.Flusher).Flush()
		}
	})
	mux.HandleFunc("/missing", func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) })
	mux.HandleFunc("/unsafe", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://example.invalid/tool", http.StatusFound)
	})
	mux.HandleFunc("/loop", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	})
	mux.HandleFunc("/moved", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/small", http.StatusFound)
	})
	mux.HandleFunc("/small", func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("small")) })
	server := httptest.NewServer(mux)
	defer server.Close()

	cases := []struct{ path, want string }{
		{"/big", "more than its budget"},
		{"/chunked", "more than its budget"},
		{"/missing", "HTTP 404"},
		{"/unsafe", "unsafe address"},
		{"/loop", "redirected more than 5 times"},
	}
	for _, tc := range cases {
		t.Run(tc.path, func(t *testing.T) {
			store := newStore(t)
			r := request(server.URL+tc.path, ArchiveRaw, "tool")
			r.MaxDownloadBytes = 2048
			_, err := store.Install(context.Background(), r)
			requireErrorContains(t, err, tc.want)
			requireEmptyStaging(t, store)
		})
	}
	store := newStore(t)
	tool, err := store.Install(context.Background(), request(server.URL+"/moved", ArchiveRaw, "tool"))
	if err != nil || readFile(t, tool.Executable) != "small" {
		t.Fatalf("redirected install = %+v, %v", tool, err)
	}
}

func TestInstallCancelledDuringDownload(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100000")
		_, _ = w.Write([]byte("partial"))
		w.(http.Flusher).Flush()
		close(started)
		<-r.Context().Done()
	}))
	defer server.Close()
	store := newStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-started
		cancel()
	}()
	_, err := store.Install(ctx, request(server.URL+"/tool", ArchiveRaw, "tool"))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want cancellation", err)
	}
	requireErrorContains(t, err, "cancelled")
	requireEmptyStaging(t, store)
	if tools, _, _ := store.List(); len(tools) != 0 {
		t.Fatalf("a cancelled install left tools: %+v", tools)
	}
	history := events(t, store)
	if len(history) != 1 || history[0].Kind != EventInstallFailed {
		t.Fatalf("history = %+v", history)
	}
}

func TestInstallStopsStalledDownload(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		_, _ = w.Write([]byte("x"))
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	store := newStore(t)
	store.stallTimeout = 100 * time.Millisecond
	_, err := store.Install(context.Background(), request(server.URL+"/tool", ArchiveRaw, "tool"))
	if !errors.Is(err, errStalled) {
		t.Fatalf("error = %v, want a stall", err)
	}
	requireErrorContains(t, err, "sent no data")
}

func TestConcurrentInstallsOfOneVersionDownloadOnce(t *testing.T) {
	data := gzBytes(t, tarBytes(t, []entry{exe("bin/tool", "tool")}))
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool.tar.gz": data})
	const installs = 8
	var wait sync.WaitGroup
	results := make([]Tool, installs)
	errs := make([]error, installs)
	for i := range installs {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results[i], errs[i] = store.Install(context.Background(), request(server.URL+"/tool.tar.gz", ArchiveTarGz, "bin/tool"))
		}()
	}
	wait.Wait()
	for i := range installs {
		if errs[i] != nil || results[i].ID != results[0].ID {
			t.Fatalf("install %d = %q, %v", i, results[i].ID, errs[i])
		}
	}
	if hits := server.hits.Load(); hits != 1 {
		t.Fatalf("downloads = %d, want 1", hits)
	}
	if history := events(t, store); len(history) != 1 {
		t.Fatalf("history = %+v, want one install", history)
	}
	if len(store.keys) != 0 {
		t.Fatalf("install locks leaked: %d", len(store.keys))
	}
}

func TestInstalledToolIsReadOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows marks only files read-only; covered by the file check below on Unix")
	}
	if os.Geteuid() == 0 {
		t.Skip("root ignores file permissions")
	}
	data := gzBytes(t, tarBytes(t, []entry{exe("bin/tool", "tool"), file("share/data.txt", "data")}))
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool.tar.gz": data})
	tool, err := store.Install(context.Background(), request(server.URL+"/tool.tar.gz", ArchiveTarGz, "bin/tool"))
	if err != nil {
		t.Fatal(err)
	}
	dataFile := filepath.Join(tool.Dir, contentDir, "share", "data.txt")
	info, err := os.Stat(dataFile)
	if err != nil || info.Mode().Perm() != 0o444 {
		t.Fatalf("data file mode = %v, %v; want 0444", info.Mode(), err)
	}
	for _, folder := range []string{tool.Dir, filepath.Join(tool.Dir, contentDir), filepath.Join(tool.Dir, contentDir, "share")} {
		info, err := os.Stat(folder)
		if err != nil || info.Mode().Perm() != 0o555 {
			t.Fatalf("%s mode = %v, %v; want 0555", folder, info.Mode(), err)
		}
	}
	if err := os.WriteFile(dataFile, []byte("changed"), 0o644); err == nil {
		t.Fatal("a tool file accepted a write")
	}
	if err := os.WriteFile(filepath.Join(tool.Dir, contentDir, "new.txt"), []byte("x"), 0o644); err == nil {
		t.Fatal("a tool folder accepted a new file")
	}
	if err := os.Remove(filepath.Join(tool.Dir, manifestName)); err == nil {
		t.Fatal("the manifest was deleted")
	}
}

func TestMarkUsedRecordsWorkspacesAndFirstUse(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool": []byte("tool")})
	tool, err := store.Install(context.Background(), request(server.URL+"/tool", ArchiveRaw, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	if err := store.MarkUsed(tool.ID, "/work/b"); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkUsed(tool.ID, "/work/a"); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	if err := store.MarkUsed(tool.ID, "/work/a"); err != nil {
		t.Fatal(err)
	}
	found, _ := store.Lookup("demo-server", "1.2.3")
	if strings.Join(found.Workspaces, ",") != "/work/a,/work/b" || !found.LastUsed.Equal(now) {
		t.Fatalf("usage = %v at %v", found.Workspaces, found.LastUsed)
	}
	uses := 0
	for _, event := range events(t, store) {
		if event.Kind == EventUse {
			uses++
		}
	}
	if uses != 2 {
		t.Fatalf("use events = %d, want one per workspace", uses)
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	if again, _ := reopened.Lookup("demo-server", "1.2.3"); len(again.Workspaces) != 2 || !again.LastUsed.Equal(now) {
		t.Fatalf("reopened usage = %+v", again)
	}
	if err := store.MarkUsed("missing/1-000000000000", "/work/a"); err == nil {
		t.Fatal("MarkUsed accepted an unknown tool")
	}
	if err := store.MarkUsed(tool.ID, ""); err == nil {
		t.Fatal("MarkUsed accepted an empty workspace")
	}
}

func TestUnrecognizedEntriesAreListedNotRemoved(t *testing.T) {
	store := newStore(t)
	foreign := filepath.Join(store.root, "other", "1.0-abc")
	if err := os.MkdirAll(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store.root, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	unknown, err := store.Unrecognized()
	if err != nil || strings.Join(unknown, ",") != "notes.txt,other/1.0-abc" {
		t.Fatalf("Unrecognized = %v, %v", unknown, err)
	}
	if tools, _, _ := store.List(); len(tools) != 0 {
		t.Fatalf("List = %+v", tools)
	}
	if _, err := Open(store.root); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(foreign); err != nil {
		t.Fatalf("Open removed an unrecognized folder: %v", err)
	}
}
