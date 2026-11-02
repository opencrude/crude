package toolstore

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ulikunitz/xz"

	"crude/internal/testhome"
)

func TestMain(m *testing.M) { os.Exit(testhome.Run(m)) }

// entry describes one archive member for the test archive builders.
type entry struct {
	name string
	body string
	mode int64
	typ  byte // tar type flag; 0 means a regular file
	link string
}

func file(name, body string) entry { return entry{name: name, body: body, mode: 0o644} }
func exe(name, body string) entry  { return entry{name: name, body: body, mode: 0o755} }
func dir(name string) entry        { return entry{name: name, mode: 0o755, typ: tar.TypeDir} }
func symlink(name, target string) entry {
	return entry{name: name, mode: 0o777, typ: tar.TypeSymlink, link: target}
}

func tarBytes(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := tar.NewWriter(&buffer)
	for _, e := range entries {
		header := &tar.Header{Name: e.name, Mode: e.mode, Typeflag: e.typ, Linkname: e.link, ModTime: time.Unix(1700000000, 0)}
		if e.typ == tar.TypeXGlobalHeader {
			header = &tar.Header{Typeflag: tar.TypeXGlobalHeader, PAXRecords: map[string]string{"comment": e.body}}
		}
		if e.typ == 0 {
			header.Typeflag = tar.TypeReg
			header.Size = int64(len(e.body))
		}
		if err := writer.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if header.Typeflag == tar.TypeReg {
			if _, err := writer.Write([]byte(e.body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func gzBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := gzip.NewWriter(&buffer)
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func xzBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer, err := xz.NewWriter(&buffer)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func zipBytes(t *testing.T, entries []entry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	for _, e := range entries {
		header := &zip.FileHeader{Name: e.name, Method: zip.Deflate}
		switch e.typ {
		case tar.TypeDir:
			header.SetMode(fs.ModeDir | 0o755)
		case tar.TypeSymlink:
			header.SetMode(fs.ModeSymlink | 0o777)
		default:
			header.SetMode(fs.FileMode(e.mode))
		}
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		body := e.body
		if e.typ == tar.TypeSymlink {
			body = e.link
		}
		if e.typ != tar.TypeDir {
			if _, err := member.Write([]byte(body)); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func digestOf(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// artifactServer is a loopback HTTP server for test artifacts.
type artifactServer struct {
	*httptest.Server
	hits  atomic.Int64
	mu    sync.Mutex
	files map[string][]byte
}

func (s *artifactServer) put(path string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.files[path] = data
}

func serve(t *testing.T, files map[string][]byte) *artifactServer {
	t.Helper()
	server := &artifactServer{files: files}
	server.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		server.mu.Lock()
		data, ok := server.files[r.URL.Path]
		server.mu.Unlock()
		if !ok {
			http.NotFound(w, r)
			return
		}
		server.hits.Add(1)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	t.Cleanup(server.Close)
	return server
}

// newStore opens a store in a temporary folder that accepts loopback HTTP.
// The cleanup restores write permission so the folder can be deleted.
func newStore(t *testing.T) *Store {
	t.Helper()
	root := filepath.Join(t.TempDir(), "tools")
	t.Cleanup(func() { _ = removeTree(root) })
	store, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	store.allowLoopbackHTTP = true
	return store
}

func request(url, archive, bin string) Request {
	return Request{
		Package:           "demo-server",
		Version:           "1.2.3",
		URL:               url,
		Archive:           archive,
		Bin:               bin,
		MaxDownloadBytes:  1 << 20,
		MaxExtractedBytes: 1 << 20,
		Source:            "test-registry",
		Licenses:          []string{"MIT"},
		InstalledBy:       "user",
		Approval:          "approval-1",
	}
}

func events(t *testing.T, store *Store) []Event {
	t.Helper()
	page, _, err := store.History(0, MaxHistoryPage)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func requireEmptyStaging(t *testing.T, store *Store) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(store.root, stagingName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging folder is not empty: %v", entries)
	}
}

func requireErrorContains(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want one that contains %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want one that contains %q", err, want)
	}
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func isNotExist(err error) bool { return errors.Is(err, fs.ErrNotExist) }
