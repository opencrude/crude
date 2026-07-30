package editor

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/tui/theme"
)

func TestMotionGeomVsChromaCost(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.go")
	var b strings.Builder
	b.WriteString("package main\n")
	for i := 0; i < 2000; i++ {
		fmt.Fprintf(&b, "func f%d() int { return %d /* c */ }\n", i, i)
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	ed := New(theme.DefaultDark())
	if err := ed.Open(path); err != nil {
		t.Fatal(err)
	}
	defer ed.Close()
	ed.viewW, ed.viewH = 120, 40
	ed.cursor = Position{Line: 100, Col: 5}

	ed.chromaLineCache = nil
	start := time.Now()
	for i := 0; i < 500; i++ {
		ed.moveDown()
		ed.moveUp()
	}
	geom := time.Since(start)

	ed.chromaLineCache = nil
	start = time.Now()
	for i := 0; i < 500; i++ {
		line := 100 + (i % 40)
		ed.ensureChromaColors(line, line+ed.viewH)
		_ = ed.lineVisualWidth(line)
	}
	chroma := time.Since(start)

	t.Logf("AFTER  geometry-only 1000 vertical moves: %v (%.2f µs/move)", geom, float64(geom.Microseconds())/1000)
	t.Logf("BEFORE-proxy chroma-on-each-motion 500 iters: %v (%.2f µs/iter)", chroma, float64(chroma.Microseconds())/500)
	if geom >= chroma {
		t.Fatalf("geometry path (%v) should be faster than chroma path (%v)", geom, chroma)
	}
}
