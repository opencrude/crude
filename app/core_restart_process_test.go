package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func restartScript(t *testing.T, body string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX harmless-process fixture; native Windows process acceptance remains separate")
	}
	path := filepath.Join(t.TempDir(), "harmless-handover-helper")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCoreRestartIdentityProcessRealSelfDigestAndEmptyEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX harmless-process fixture")
	}
	sha, err := exec.LookPath("sha256sum")
	if err != nil {
		t.Skip("sha256sum is required by the harmless self-digest fixture")
	}
	cut, err := exec.LookPath("cut")
	if err != nil {
		t.Skip("cut is required by the harmless self-digest fixture")
	}
	f := newRestartFixture(t)
	t.Setenv("CRUDE_HOME", f.host.home)
	t.Setenv("CRUDE_RESTART_PRIVATE_SENTINEL", "must-not-inherit")
	body := fmt.Sprintf(`
[ "$1" = "--core-handover-identity" ] || exit 41
[ -z "$CRUDE_HOME" ] && [ -z "$CRUDE_RESTART_PRIVATE_SENTINEL" ] || exit 42
digest=$(%q "$0") || exit 43
digest=${digest%%%% *}
build=$(printf '%%s' "$digest" | %q -c1-16) || exit 44
printf '{"build_id":"%%s","cohort":"%s","schema":"new-schema","sha256":"%%s","goos":"%s","goarch":"%s"}\n' "$build" "$digest"
`, sha, cut, strings.Repeat("3", 64), runtime.GOOS, runtime.GOARCH)
	f.input.Executable = restartScript(t, body)
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
	valid := fmt.Sprintf(`{"build_id":"0123456789abcdef","cohort":"%s","schema":"schema","sha256":"%s","goos":"%s","goarch":"%s"}`,
		strings.Repeat("2", 64), strings.Repeat("1", 64), runtime.GOOS, runtime.GOARCH)
	for _, test := range []string{"valid", "unknown-field", "duplicate-key", "trailing-value", "oversized", "stderr-oversized", "exit", "cancelled"} {
		t.Run(test, func(t *testing.T) {
			body := "printf '%s' '" + valid + "'"
			switch test {
			case "unknown-field":
				body = "printf '%s' '" + strings.TrimSuffix(valid, "}") + `,"unexpected":true}'`
			case "duplicate-key":
				body = "printf '%s' '" + strings.TrimSuffix(valid, "}") + `,"schema":"another"}'`
			case "trailing-value":
				body += "\nprintf '{}'"
			case "oversized":
				body = "printf '%9000s' x"
			case "stderr-oversized":
				body += "\nprintf '%9000s' x >&2"
			case "exit":
				body = "echo private-secret-error >&2\nexit 73"
			case "cancelled":
				body = "exec /bin/sleep 60"
			}
			path := restartScript(t, body)
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
			helper := restartScript(t, fmt.Sprintf(`
[ "$#" -eq 1 ] && [ "$1" = "--core-stop" ] || exit 61
[ "$CRUDE_EXPECT_CORE_OWNER" = "%s" ] || exit 62
printf 'exact original helper' > %q
/bin/rm -- %q
`, f.old.Generation, marker, coreOwnerPath(f.host.home)))
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
	marker := filepath.Join(t.TempDir(), "joined-result")
	path := restartScript(t, `
[ "$CRUDE_RESTART_JOIN_FIXTURE" = "one" ] || exit 51
[ "$PWD" = "$2" ] || exit 52
printf 'foreground child completed' > "$1"
`)
	dir := t.TempDir()
	if err := runRestartSuccessor(t.Context(), path, []string{marker, dir}, []string{"CRUDE_RESTART_JOIN_FIXTURE=one"}, dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "foreground child completed" {
		t.Fatalf("foreground runner returned before child finished: %q %v", data, err)
	}
}
