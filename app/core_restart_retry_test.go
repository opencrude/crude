package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func installRestartRefusalHelper(t *testing.T, fixture *restartFixture, body string) coreOwner {
	t.Helper()
	if err := removeCoreOwner(fixture.host.home, fixture.old.Generation); err != nil {
		t.Fatal(err)
	}
	path := restartScript(t, body)
	digest, _, err := restartImageDigest(path)
	if err != nil {
		t.Fatal(err)
	}
	image, err := stageCoreImage(fixture.host.home, path, digest[:16])
	if err != nil {
		t.Fatal(err)
	}
	owner := fixture.old
	owner.Digest, owner.Executable = digest, image
	if err := publishCoreOwner(coreOwnerPath(fixture.host.home), owner); err != nil {
		t.Fatal(err)
	}
	return owner
}

func TestCoreRestartScheduledHelperRetriesOnlyExplicitNoEffectRefusal(t *testing.T) {
	for _, kind := range []string{"refused-then-accepted", "unknown-failure", "panic-exit-two", "permanent-refusal"} {
		t.Run(kind, func(t *testing.T) {
			f := newRestartFixture(t)
			attempts := filepath.Join(f.host.home, "attempts")
			marker := filepath.Join(f.host.home, "first-refusal")
			body := fmt.Sprintf("printf 'attempt\\n' >> %q\n", attempts)
			switch kind {
			case "refused-then-accepted":
				body += fmt.Sprintf("if [ ! -f %q ]; then : > %q; exit %d; fi\n/bin/rm -- %q\n", marker, marker, coreHandoverRefusedExit, coreOwnerPath(f.host.home))
			case "unknown-failure":
				body += "exit 1\n"
			case "panic-exit-two":
				body += "printf 'panic: uncertain result\\n' >&2\nexit 2\n"
			case "permanent-refusal":
				body += fmt.Sprintf("exit %d\n", coreHandoverRefusedExit)
			}
			owner := installRestartRefusalHelper(t, f, body)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			err := handoverOwnedCore(ctx, f.host.home, owner)
			raw, readErr := os.ReadFile(attempts)
			if readErr != nil {
				t.Fatal(readErr)
			}
			want := map[string]int{"refused-then-accepted": 2, "unknown-failure": 1, "panic-exit-two": 1, "permanent-refusal": 8}[kind]
			if got := strings.Count(string(raw), "attempt\n"); got != want {
				t.Fatalf("wrong replay boundary: %s ran %d times, want %d", kind, got, want)
			}
			if (err == nil) != (kind == "refused-then-accepted") {
				t.Fatalf("wrong stop outcome: %s %v", kind, err)
			}
			if err != nil {
				if current, readErr := readCoreOwner(f.host.home); readErr != nil || current != owner {
					t.Fatal("refused/unknown helper changed owner", current, readErr)
				}
			}
		})
	}
}

func TestCoreRestartScheduledRefusalCannotStopReplacement(t *testing.T) {
	f := newRestartFixture(t)
	marker, proceed := filepath.Join(f.host.home, "refused"), filepath.Join(f.host.home, "replacement-published")
	attempts := filepath.Join(f.host.home, "attempts")
	body := fmt.Sprintf("printf 'attempt\\n' >> %q\n: > %q\nwhile [ ! -f %q ]; do /bin/sleep 0.01; done\nexit %d\n", attempts, marker, proceed, coreHandoverRefusedExit)
	owner := installRestartRefusalHelper(t, f, body)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	stopped := make(chan error, 1)
	go func() { stopped <- handoverOwnedCore(ctx, f.host.home, owner) }()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("fake helper did not reach its no-effect refusal")
		case <-time.After(time.Millisecond):
		}
	}
	if err := removeCoreOwner(f.host.home, owner.Generation); err != nil {
		t.Fatal(err)
	}
	replacement := owner
	replacement.Generation = strings.Repeat("7", 64)
	if err := publishCoreOwner(coreOwnerPath(f.host.home), replacement); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(proceed, nil, 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stopped:
		if err == nil {
			t.Fatal("changed owner was reported stopped")
		}
	case <-ctx.Done():
		t.Fatal("changed owner did not stop the retry sequence")
	}
	raw, err := os.ReadFile(attempts)
	if err != nil || strings.Count(string(raw), "attempt\n") != 1 {
		t.Fatal("retry invoked the predecessor against a replacement", string(raw), err)
	}
	if current, err := readCoreOwner(f.host.home); err != nil || current != replacement {
		t.Fatal("replacement owner changed", current, err)
	}
}
