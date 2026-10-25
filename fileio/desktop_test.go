package fileio

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
)

func TestDesktopCommandKeepsPathsAsArguments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image & notes; $(whoami).png")
	for _, tc := range []struct {
		os      string
		reveal  bool
		command string
		args    []string
	}{
		{"linux", false, "xdg-open", []string{path}},
		{"linux", true, "xdg-open", []string{filepath.Dir(path)}},
		{"darwin", false, "open", []string{path}},
		{"darwin", true, "open", []string{"-R", path}},
	} {
		command, args := desktopCommand(tc.os, path, tc.reveal)
		if command != tc.command || !reflect.DeepEqual(args, tc.args) {
			t.Fatalf("unsafe %s launch: %s %q", tc.os, command, args)
		}
	}
	if err := OpenDesktopPath(path, false); err == nil {
		t.Fatal("missing file must fail before launching an application")
	}
}

func TestDesktopOpenCancelledBeforePathInspection(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := OpenDesktopPathContext(ctx, filepath.Join(t.TempDir(), "not-there"), true); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled desktop open inspected/launched a path: %v", err)
	}
}
