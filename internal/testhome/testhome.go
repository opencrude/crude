// Package testhome isolates a test binary from the developer's user profile.
package testhome

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// Run points every profile root Crude resolves at one fresh directory, runs the
// package tests and removes it. Data paths come from os.UserHomeDir (USERPROFILE
// on Windows, HOME elsewhere) and the platform data dirs, so a test that only
// overrides HOME still wrote workspaces, sessions and history into the real
// Windows profile. Tests may still t.Setenv narrower homes of their own.
//
// Go toolchain state that defaults beneath those roots stays pinned to the
// developer's real locations: tests that run `go` in fixture modules must not
// pay a cold build cache or reconfigure the toolchain per package.
func Run(m *testing.M) int {
	if err := isolate(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "testhome:", err)
		return 1
	}
	home := os.Getenv("HOME")
	code := m.Run()
	_ = os.RemoveAll(home)
	return code
}

func isolate() error {
	pinned := map[string]string{}
	if realHome, err := os.UserHomeDir(); err == nil {
		pinned["GOPATH"] = filepath.Join(realHome, "go")
	}
	if cache, err := os.UserCacheDir(); err == nil {
		pinned["GOCACHE"] = filepath.Join(cache, "go-build")
	}
	if config, err := os.UserConfigDir(); err == nil {
		pinned["GOENV"] = filepath.Join(config, "go", "env")
	}
	for key, value := range pinned {
		if os.Getenv(key) != "" {
			continue
		}
		if err := os.Setenv(key, value); err != nil {
			return err
		}
	}
	home, err := os.MkdirTemp("", "crude-test-home-")
	if err != nil {
		return err
	}
	for _, key := range []string{"HOME", "USERPROFILE", "APPDATA", "XDG_DATA_HOME"} {
		if err := os.Setenv(key, home); err != nil {
			return err
		}
	}
	// A developer's dev profile must not leak in, and per-test HOME overrides
	// must keep deciding where crude data lives.
	return os.Unsetenv("CRUDE_HOME")
}
