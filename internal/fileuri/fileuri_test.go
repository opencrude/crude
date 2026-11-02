package fileuri

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
	_ "modernc.org/sqlite"
)

func TestLiteralLocalPaths(t *testing.T) {
	path, want := "/tmp/space # %/雪.go", "file:///tmp/space%20%23%20%25/%E9%9B%AA.go"
	if runtime.GOOS == "windows" {
		path, want = `c:\space # %\雪.go`, "file:///C:/space%20%23%20%25/%E9%9B%AA.go"
	}
	if got := FromPath(path); got != want {
		t.Fatalf("FromPath = %q, want %q", got, want)
	}
	got, err := ToPath(want)
	wantPath := filepath.Clean(path)
	if runtime.GOOS == "windows" {
		wantPath = `C:\space # %\雪.go`
	}
	if err != nil || got != wantPath {
		t.Fatalf("ToPath = %q, %v", got, err)
	}
	if got := SQLiteDSN(path, "mode=ro&_pragma=busy_timeout(1)"); got != want+"?mode=ro&_pragma=busy_timeout(1)" {
		t.Fatalf("DSN = %q", got)
	}
	for _, bad := range []string{"file://remote/share/x", "file:relative", "https://example.com/x", "file:///x?", "file:///x#", "file:///x?q=1", "file:////server/share", "file:///x%00", "file:///x%5Cy", "file:///bad%zz"} {
		if _, err := ToPath(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestMatchesPowernap(t *testing.T) {
	for _, p := range []string{t.TempDir(), filepath.Join(t.TempDir(), "space # %", "雪.go"), ""} {
		if got, want := FromPath(p), string(protocol.URIFromPath(p)); got != want {
			t.Errorf("%q: got %q want %q", p, got, want)
		}
		if p != "" {
			got, err := ToPath(FromPath(p))
			if err != nil || !strings.EqualFold(got, filepath.Clean(p)) {
				t.Errorf("round trip %q: %q, %v", p, got, err)
			}
			got, err = ToPath(strings.Replace(FromPath(p), "file:///", "file://localhost/", 1))
			if err != nil || !strings.EqualFold(got, filepath.Clean(p)) {
				t.Errorf("localhost %q: %q, %v", p, got, err)
			}
		}
	}
}

func TestSQLiteDSNSpecialPath(t *testing.T) {
	name := "space # % 雪"
	if runtime.GOOS != "windows" {
		name += "?"
	}
	dir := filepath.Join(t.TempDir(), name)
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test.db")
	db, err := sql.Open("sqlite", SQLiteDSN(path, "mode=rwc"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("CREATE TABLE sample (value TEXT); INSERT INTO sample VALUES ('retained')"); err != nil {
		_ = db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = sql.Open("sqlite", SQLiteDSN(path, "mode=ro"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var got string
	if err := db.QueryRow("SELECT value FROM sample").Scan(&got); err != nil || got != "retained" {
		t.Fatalf("read: %q, %v", got, err)
	}
	if _, err := db.Exec("INSERT INTO sample VALUES ('forbidden')"); err == nil {
		t.Fatal("read-only query option was not enforced")
	}
}
