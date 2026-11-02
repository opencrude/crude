package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCoreRestartIdentityProcessRealSelfDigestAndEmptyEnvironment(t *testing.T) {
	f := newRestartFixture(t)
	t.Setenv("CRUDE_HOME", f.host.home)
	t.Setenv("CRUDE_RESTART_PRIVATE_SENTINEL", "must-not-inherit")
	f.input.Executable = writeRestartFixture(t, t.TempDir(), "self-digest")
	digest, _, err := restartImageDigest(f.input.Executable)
	if err != nil {
		t.Fatal(err)
	}
	f.input.SHA256 = digest
	f.setInput(t)
	f.host.identity = inspectRestartCandidate
	f.schedule(t)
	m, _, _, err := readRestartManifest(f.host.home, f.host.manifestPath(f.request.ID))
	if err != nil {
		t.Fatal(err)
	}
	if m.Identity.SHA256 != digest || m.Identity.BuildID != digest[:16] || m.Image == f.input.Executable {
		t.Fatal("real metadata probe did not verify the retained exact executable")
	}
}

func TestCoreRestartIdentityProcessBoundedStrictAndCancellable(t *testing.T) {
	for _, test := range []string{"valid", "unknown-field", "duplicate-key", "trailing-value", "oversized", "stderr-oversized", "exit", "cancelled"} {
		t.Run(test, func(t *testing.T) {
			path := writeRestartFixture(t, t.TempDir(), test)
			ctx := t.Context()
			var cancel context.CancelFunc
			if test == "cancelled" {
				ctx, cancel = context.WithTimeout(ctx, 30*time.Millisecond)
				defer cancel()
			}
			identity, err := inspectRestartCandidate(ctx, path)
			if test == "valid" {
				if err != nil || identity.BuildID != "0123456789abcdef" {
					t.Fatalf("read-only handshake rejected: %+v %v", identity, err)
				}
			} else if err == nil || strings.Contains(err.Error(), "private-secret-error") {
				t.Fatalf("malformed/failed metadata process accepted or leaked stderr: %+v %v", identity, err)
			}
		})
	}
}

func TestCoreRestartExactOwnerHelperNeverSelectsReplacement(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "matching-harmless-helper", true: "replacement-refused"}[replaced], func(t *testing.T) {
			f := newRestartFixture(t)
			marker := filepath.Join(f.host.home, "harmless-helper-ran")
			helper := writeRestartFixture(t, t.TempDir(), "exact-owner")
			digest, _, err := restartImageDigest(helper)
			if err != nil {
				t.Fatal(err)
			}
			image, err := stageCoreImage(f.host.home, helper, digest[:16])
			if err != nil {
				t.Fatal(err)
			}
			old := f.old
			old.Executable, old.Digest = image, digest
			replaceRestartOwner(t, f.host.home, old)
			if replaced {
				replacement := old
				replacement.Generation = strings.Repeat("f", 64)
				replaceRestartOwner(t, f.host.home, replacement)
			}
			err = handoverOwnedCore(t.Context(), f.host.home, old)
			if replaced {
				if err == nil {
					t.Fatal("stopped replacement owner")
				}
				if _, statErr := os.Stat(marker); !errors.Is(statErr, os.ErrNotExist) {
					t.Fatal("replacement race executed a helper")
				}
				if owner, readErr := readCoreOwner(f.host.home); readErr != nil || owner.Generation != strings.Repeat("f", 64) {
					t.Fatalf("replacement owner record changed: %+v %v", owner, readErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(marker); err != nil {
				t.Fatal("matching original helper was not joined")
			}
			if err := settleRestartLifetime(t.Context(), f.host.home); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCoreRestartForegroundSuccessorWaitIsJoined(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "joined-result")
	path := writeRestartFixture(t, t.TempDir(), "successor")
	if err := runRestartSuccessor(t.Context(), path, []string{"--core-handover", marker}, []string{"CRUDE_RESTART_JOIN_FIXTURE=one"}, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "foreground child completed" {
		t.Fatalf("foreground runner returned before child finished: %q %v", data, err)
	}
}
