package buildinfo

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The Windows native build compiles cgo with `zig cc`, which — unlike gcc —
// defaults to the build host's CPU. Without an explicit baseline, the
// tree-sitter grammars compiled on a newer CI runner execute an illegal
// instruction inside ts_parser_new on older user machines (observed on a Kaby
// Lake i5-7300U), which crash-loops the core. The canonical build environment
// must pin the cgo compile to the x86-64 baseline, matching the already pinned
// `-Dcpu=baseline` libghostty-vt library, so one release binary runs on every
// supported x86-64 machine. This test guards that invariant.
func TestWindowsBuildPinsBaselineCPU(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source path")
	}
	root := filepath.Dir(filepath.Dir(file))
	script := filepath.Join(root, "scripts", "libghostty_env.ps1")
	data, err := os.ReadFile(script)
	if err != nil {
		t.Fatalf("read %s: %v", script, err)
	}
	text := string(data)
	if !strings.Contains(text, "-mcpu=baseline") {
		t.Error("scripts/libghostty_env.ps1 must pin the cgo compile to -mcpu=baseline so tree-sitter runs on older x86-64 CPUs")
	}
	if !strings.Contains(text, "CGO_CFLAGS") {
		t.Error("scripts/libghostty_env.ps1 must export CGO_CFLAGS for the cgo build")
	}
}
