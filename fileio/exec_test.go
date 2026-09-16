package fileio

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Agent command lines are written for bash (`source`/`.` with BASH_SOURCE,
// `[[ ]]`, arrays) and the repository's own scripts are bash. Running them
// under a POSIX sh such as dash fails on syntax the user's shell accepts.
func TestLocalExecRunsBashSemantics(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell semantics")
	}
	out, err := NewLocalExec().Execute(context.Background(),
		`[[ -n "x" ]] && echo bracket_ok && echo "bash_version=${BASH_VERSION:+set}"`, ExecOptions{})
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput: %s", err, out)
	}
	if !strings.Contains(out, "bracket_ok") {
		t.Fatalf("Execute() output = %q, want bash `[[ ]]` to run", out)
	}
	if !strings.Contains(out, "bash_version=set") {
		t.Fatalf("Execute() output = %q, want BASH_VERSION to be set", out)
	}
}

// Sourcing a Makefile-style helper (BASH_SOURCE) is the exact shape that broke
// when the executor used /bin/sh.
func TestLocalExecSourcesBashHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell semantics")
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "env.sh"), []byte("ROOT=\"$(cd \"$(dirname \"${BASH_SOURCE[0]}\")/..\" && pwd)\"\necho \"root=$ROOT\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := NewLocalExec().Execute(context.Background(), ". ./env.sh", ExecOptions{CWD: dir})
	if err != nil {
		t.Fatalf("Execute() error = %v\noutput: %s", err, out)
	}
	if strings.Contains(out, "Bad substitution") {
		t.Fatalf("Execute() ran a shell without BASH_SOURCE: %q", out)
	}
	if !strings.Contains(out, "root="+filepath.Dir(dir)) {
		t.Fatalf("Execute() output = %q, want sourced helper to resolve %q", out, filepath.Dir(dir))
	}
}

func TestShellForExecPrefersBash(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell semantics")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash is not installed on this host")
	}
	shell, args := shellForExec()
	if !strings.HasSuffix(shell, "bash") {
		t.Fatalf("shellForExec() = %q, want a bash interpreter", shell)
	}
	if len(args) != 1 || args[0] != "-c" {
		t.Fatalf("shellForExec() args = %v, want [-c]", args)
	}
}
