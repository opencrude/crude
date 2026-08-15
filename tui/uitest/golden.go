// Package uitest provides test-support utilities for cell-buffer golden
// regression checks. It is a TEST-ONLY package: production code must never
// import it. Screen packages use it in their _test.go files to assert
// byte-identical paint output on top of property-based checks.
package uitest

import (
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"crude/tui/cell"
	"crude/tui/surface"
)

// updateGoldens is set via `go test ./tui/... -update` to (re)generate
// golden files. CI never passes it, so any drift fails there.
var updateGoldens = flag.Bool("update", false, "update golden cell-buffer hashes")

// GoldenHash serializes every cell of buf deterministically and returns a
// stable SHA-256 hex digest. Only cell content participates: coordinates,
// char, width, fg/bg colors, attrs, transparency. Time-dependent paint must
// be given fixed inputs by the caller (fixed StartTime / spinFrame).
func GoldenHash(buf *surface.CellBuffer) string {
	h := sha256.New()
	for y := 0; y < buf.Height(); y++ {
		for x := 0; x < buf.Width(); x++ {
			c := buf.Get(x, y)
			fmt.Fprintf(h, "%d,%d,%d,%d,%s,%s,%d,%t;",
				x, y, c.Char, c.Width,
				colorKey(c.FG), colorKey(c.BG),
				c.Attr, c.Transparent)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

// colorKey renders a cell.Color deterministically.
func colorKey(c cell.Color) string {
	switch c.Type {
	case cell.ColorDefault:
		return "def"
	case cell.ColorPalette:
		return fmt.Sprintf("p%d", c.Palette)
	case cell.ColorTrueColor:
		return fmt.Sprintf("t%02x%02x%02x", c.R, c.G, c.B)
	default:
		return "?"
	}
}

// CheckGolden asserts buf's hash matches the checked-in golden file
// testdata/golden/<name>.sha256 (relative to the importing package, since
// test binaries run with cwd = package dir). Pass -update to regenerate when
// a paint change is INTENTIONAL.
//
// When a platform-specific variant testdata/golden/<name>_<goos>.sha256
// exists, it takes precedence over the default. This lets screens that
// render platform-dependent text (e.g. filepath.Abs-converted paths) keep
// separate goldens per OS without changing rendering code.
func CheckGolden(t *testing.T, name string, buf *surface.CellBuffer) {
	t.Helper()
	got := GoldenHash(buf)
	defaultPath := filepath.Join("testdata", "golden", name+".sha256")
	platformPath := filepath.Join("testdata", "golden",
		fmt.Sprintf("%s_%s.sha256", name, runtime.GOOS))

	if *updateGoldens {
		// -update writes the platform-specific file when a platform variant
		// already exists, otherwise the default. This prevents accidentally
		// overwriting the Linux golden when running -update on Windows.
		path := defaultPath
		if _, err := os.Stat(platformPath); err == nil {
			path = platformPath
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Logf("updated golden %s -> %s", name, got)
		return
	}

	// Prefer the platform-specific golden when it exists.
	path := defaultPath
	if _, err := os.Stat(platformPath); err == nil {
		path = platformPath
	}

	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		t.Fatalf("golden %s missing; run `go test ./tui/... -update` to create it (hash=%s)", path, got)
	}
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(string(raw))
	if got != want {
		t.Fatalf("golden %s mismatch:\n  got  %s\n  want %s\n  Run `go test ./tui/... -update` only if the paint change is INTENTIONAL.",
			name, got, want)
	}
}
