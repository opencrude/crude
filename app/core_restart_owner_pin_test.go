package app

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestCoreRestartOriginalDialerNeverAdoptsReplacementOrRespawns(t *testing.T) {
	for _, mode := range []string{"absent", "same-image-replacement"} {
		t.Run(mode, func(t *testing.T) {
			home := coreTestHome(t)
			old := coreOwner{Generation: strings.Repeat("8", 64)}
			path, _ := makeRestartStartupManifest(t, home, old)
			dial := shellDialer(home, CLIFlags{CoreHandover: path})
			ctx, cancel := context.WithTimeout(t.Context(), 25*time.Second)
			defer cancel()
			first, err := dial(ctx, "initial-navigation")
			if err != nil {
				t.Fatal(err)
			}
			admitted, err := readCoreOwner(home)
			if err != nil {
				t.Fatal(err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			stop, err := StopCore(ctx, home, false)
			if err != nil || !stop.Accepted {
				t.Fatal("private fixture predecessor did not settle", stop, err)
			}
			if err := settleRestartLifetime(ctx, home); err != nil {
				t.Fatal(err)
			}
			var replacement coreOwner
			if mode == "same-image-replacement" {
				other, err := ConnectCore(ctx, home, "ordinary-replacement")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = other.Close() }()
				replacement, err = readCoreOwner(home)
				if err != nil || replacement.Generation == admitted.Generation || replacement.Digest != admitted.Digest {
					t.Fatal("fixture did not produce a distinct same-image core", replacement, err)
				}
			}
			again, err := dial(ctx, "workspace-attachment-or-redial")
			if err == nil {
				_ = again.Close()
				t.Fatal("private shell silently adopted or spawned another successor")
			}
			actual, readErr := readCoreOwner(home)
			if mode == "absent" && readErr == nil {
				t.Fatal("failed redial replayed successor launch", actual)
			}
			if mode == "same-image-replacement" && (readErr != nil || actual != replacement) {
				t.Fatal("refused redial changed replacement ownership", actual, readErr)
			}
		})
	}
}

func TestCoreRestartContinueRejectsSameImageOwnerChangedAfterConstruction(t *testing.T) {
	f := newRestartFixture(t)
	next := prepareRestartSuccessor(t, f)
	replacement := next.owner
	replacement.Generation = strings.Repeat("9", 64)
	replaceRestartOwner(t, f.host.home, replacement)
	if _, err := next.Continue(t.Context()); err == nil || f.port.continues != 0 {
		t.Fatal("continuation silently moved to a different same-image successor", err, f.port.continues)
	}
}

func TestCoreRestartOwnedHelperRefusalUsesNonPanicStatus(t *testing.T) {
	home := coreTestHome(t)
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	attached, err := ConnectCore(ctx, home, "fixture-shell-keeps-core-busy")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = attached.Close() }()
	owner, err := readCoreOwner(home)
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.CommandContext(ctx, owner.Executable, "--core-stop")
	helper.Env = coreProfileEnv(home, owner.Generation)
	out, err := helper.CombinedOutput()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != coreHandoverRefusedExit {
		t.Fatalf("decoded no-effect refusal did not use dedicated helper status: %v %s", err, out)
	}
	current, err := readCoreOwner(home)
	if err != nil || current != owner {
		t.Fatal("refused helper changed its busy owner", current, err)
	}
}
