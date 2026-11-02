//go:build !windows

package fileio

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func testDesktopNativeLauncherArguments(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	output := filepath.Join(dir, "arguments")
	name := "xdg-open"
	if runtime.GOOS == "darwin" {
		name = "open"
	}
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$CRUDE_TEST_DESKTOP_ARGS\"\n"
	if err := os.WriteFile(filepath.Join(dir, name), []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CRUDE_TEST_DESKTOP_ARGS", output)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	file := filepath.Join(dir, "a&b.txt")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path   string
		reveal bool
		want   string
	}{
		{file, false, file + "\n"},
		{file, true, func() string {
			if runtime.GOOS == "darwin" {
				return "-R\n" + file + "\n"
			}
			return dir + "\n"
		}()},
		{dir, true, dir + "\n"},
	} {
		if err := OpenDesktopPathContext(context.Background(), tc.path, tc.reveal); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(output)
		if err != nil || string(got) != tc.want {
			t.Fatalf("launch args = %q, want %q; read error: %v", got, tc.want, err)
		}
	}
}
