// Adapted for Crude from fgn/applypatch 95ed66f6d36a575b00001f2e27aedc1ec7455812.
// See NOTICE for provenance and modification details.
package codexio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeCodexPrefix marks a run of the test binary as a stand-in for the Codex
// binary. runOracle passes the patch as the only argument, so the patch
// names the behavior. Running the test binary itself, rather than writing a
// script, avoids "text file busy" errors when parallel tests fork.
const fakeCodexPrefix = "fake-codex:"

func TestMain(m *testing.M) {
	if len(os.Args) == 2 && strings.HasPrefix(os.Args[1], fakeCodexPrefix) {
		os.Exit(fakeCodex(strings.TrimPrefix(os.Args[1], fakeCodexPrefix)))
	}
	os.Exit(m.Run())
}

func fakeCodex(behavior string) int {
	switch behavior {
	case "success":
		if os.Getenv("CODEX_APPLY_PATCH_PRESERVE_LINE_ENDINGS") != "1" {
			return 3
		}
		_ = os.WriteFile("a.txt", []byte("b\n"), 0o644)
		_ = os.WriteFile("p.txt", []byte(os.Args[1]), 0o644)
		return 0
	case "rejection":
		_ = os.WriteFile("a.txt", []byte("b\n"), 0o644)
		fmt.Fprintln(os.Stderr, "Failed to find expected lines in a.txt:")
		return 1
	case "startup":
		fmt.Fprintln(os.Stderr, "Error: Failed to determine current directory.")
		return 1
	case "usage":
		fmt.Fprintln(os.Stderr, "Error: apply_patch accepts exactly one argument.")
		return 2
	case "panic":
		fmt.Fprintln(os.Stderr, "thread 'main' panicked")
		return 101
	case "signal":
		self, _ := os.FindProcess(os.Getpid())
		_ = self.Kill()
		time.Sleep(time.Second)
	case "timeout":
		time.Sleep(10 * time.Second)
	case "noisy":
		_, _ = os.Stderr.Write(bytes.Repeat([]byte("x"), 100000))
		return 1
	}
	return 3
}

func testBinary(t *testing.T) string {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return bin
}

func TestOracleRunner(t *testing.T) {
	t.Parallel()
	bin := testBinary(t)
	files := map[string]string{"a.txt": "a\n"}
	for _, tc := range []struct {
		behavior string
		ok       bool
		err      string
	}{
		{"success", true, ""},
		{"rejection", false, ""},
		{"startup", false, "failed to start"},
		{"usage", false, "crashed"},
		{"panic", false, "crashed"},
		{"signal", false, "crashed"},
		{"timeout", false, "running"},
	} {
		t.Run(tc.behavior, func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			result, err := runOracle(ctx, bin, files, fakeCodexPrefix+tc.behavior)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("got %+v, %v; want error containing %q", result, err, tc.err)
				}
				return
			}
			if err != nil || result.ok != tc.ok || result.files["a.txt"] != "b\n" {
				t.Fatalf("got %+v, %v", result, err)
			}
			if tc.ok && result.files["p.txt"] != fakeCodexPrefix+tc.behavior {
				t.Fatalf("the patch was not passed as the argument: %q", result.files["p.txt"])
			}
			if !tc.ok && errorCategory(result.stderr) != "lines" {
				t.Fatalf("stderr %q", result.stderr)
			}
		})
	}
}

func TestOracleMissingBinary(t *testing.T) {
	t.Parallel()
	_, err := runOracle(t.Context(), filepath.Join(t.TempDir(), "missing"), nil, "patch")
	if err == nil {
		t.Fatal("a missing binary was not an error")
	}
}

func TestOracleBoundsStderr(t *testing.T) {
	t.Parallel()
	result, err := runOracle(t.Context(), testBinary(t), nil, fakeCodexPrefix+"noisy")
	if err != nil || len(result.stderr) != maxStderr {
		t.Fatalf("kept %d bytes of stderr, %v", len(result.stderr), err)
	}
}

func TestOracleRefusesUnsafePaths(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		files map[string]string
		patch string
	}{
		{map[string]string{"../x.txt": ""}, "p"},
		{map[string]string{"/etc/x": ""}, "p"},
		{nil, "*** Begin Patch\n*** Add File: ../x.txt\n+x\n*** End Patch"},
		{nil, "*** Begin Patch\n*** Delete File: /etc/passwd\n*** End Patch"},
		{nil, "*** Begin Patch\n*** Update File: a.txt\n*** Move to: a/../../b\n*** End Patch"},
		{nil, "  *** Update File: C:\\x"},
		{nil, "p\x00"},
	} {
		_, err := runOracle(t.Context(), "/bin/false", tc.files, tc.patch)
		if err == nil || !strings.Contains(err.Error(), "unsafe") && !strings.Contains(err.Error(), "NUL") {
			t.Fatalf("%q, %q: got %v", tc.files, tc.patch, err)
		}
	}
}

func TestErrorCategory(t *testing.T) {
	t.Parallel()
	for message, want := range map[string]string{
		"Invalid patch hunk on line 2: x":               "parse",
		"invalid patch: x":                              "parse",
		"Failed to find expected lines in a:\nx":        "lines",
		"Failed to find context 'x' in a":               "context",
		"Failed to read file to update a: No such file": "read",
		"Failed to delete file a":                       "delete",
		"No files were modified.":                       "empty",
		"something else\nmore":                          "other: something else",
	} {
		got := errorCategory(message)
		if got != want {
			t.Errorf("%q: got %q, want %q", message, got, want)
		}
	}
}
