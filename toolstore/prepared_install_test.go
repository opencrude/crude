package toolstore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"crude/fileio"
)

func preparedRequest() Request {
	return Request{
		Package: "prepared-fixture", Version: "1.0.0", Source: "fixture-registry",
		URL: "https://example.invalid/fixture", Archive: ArchivePrepared,
		Bin: "bin/server", Command: "server", InstalledBy: "user", Approval: "review",
		MaxDownloadBytes: 1 << 20, MaxExtractedBytes: 1 << 20, MaxEntries: 20,
	}
}

func TestPreparedInstallChecksAndPublishesExactTree(t *testing.T) {
	store := newStore(t)
	req := preparedRequest()
	prepareCalls, evaluations, admissions := 0, 0, 0
	req.Prepare = func(ctx context.Context, output, scratch string) error {
		prepareCalls++
		if _, err := os.Stat(filepath.Join(filepath.Dir(output), preparationPendingName)); err != nil {
			t.Fatalf("preparation has no durable pending marker: %v", err)
		}
		if !store.mu.TryLock() {
			t.Fatal("preparation ran under the store lock")
		}
		store.mu.Unlock()
		if err := os.MkdirAll(filepath.Join(output, "bin"), 0o700); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(output, "bin", "server"), []byte("compiled executable"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(output, "integrity.txt"), []byte("fixture checksums"), 0o600)
	}
	req.Evaluate = func(ctx context.Context, tool Tool) error {
		evaluations++
		if _, ok := store.Lookup(req.Package, req.Version); ok {
			t.Fatal("unpublished prepared tool became available")
		}
		if readFile(t, tool.Executable) != "compiled executable" ||
			readFile(t, filepath.Join(tool.Dir, contentDir, "integrity.txt")) != "fixture checksums" {
			t.Fatal("evaluation did not receive the exact prepared bytes")
		}
		return ctx.Err()
	}
	req.AdmitPublication = func(ctx context.Context) error {
		admissions++
		return ctx.Err()
	}
	tool, err := store.Install(t.Context(), req)
	if err != nil || prepareCalls != 1 || evaluations != 1 || admissions != 1 {
		t.Fatalf("prepared install: %+v calls=%d/%d/%d err=%v", tool, prepareCalls, evaluations, admissions, err)
	}
	if tool.DigestOrigin != DigestPreparedTree || !validDigest(tool.SHA256) ||
		readFile(t, tool.Executable) != "compiled executable" {
		t.Fatalf("prepared identity or content is invalid: %+v", tool)
	}
	digest, err := preparedTreeDigest(t.Context(), filepath.Join(tool.Dir, contentDir),
		extractLimits{bytes: req.MaxExtractedBytes, entries: req.MaxEntries})
	if err != nil || digest != tool.SHA256 {
		t.Fatalf("sealing changed the tree identity: %q %v", digest, err)
	}
	requireEmptyStaging(t, store)
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	found, ok := reopened.Lookup(req.Package, req.Version)
	if !ok || found.ID != tool.ID || found.DigestOrigin != DigestPreparedTree {
		t.Fatalf("prepared tool was not recovered: %+v %v", found, ok)
	}
	again, err := reopened.Install(t.Context(), req)
	if err != nil || again.ID != tool.ID || prepareCalls != 1 {
		t.Fatalf("matching artifact ran the installer twice: %+v %v", again, err)
	}
}

func TestPreparedInstallConfirmedFailureCanBeRetried(t *testing.T) {
	for _, phase := range []string{"prepare", "evaluate"} {
		t.Run(phase, func(t *testing.T) {
			store := newStore(t)
			req := preparedRequest()
			req.Prepare = func(_ context.Context, output, _ string) error {
				if phase == "prepare" {
					return errors.New("preparation failed")
				}
				if err := os.Mkdir(filepath.Join(output, "bin"), 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(output, "bin", "server"), []byte("fixture"), 0o700)
			}
			req.Evaluate = func(context.Context, Tool) error { return errors.New("evaluation failed") }
			if _, err := store.Install(t.Context(), req); err == nil {
				t.Fatal("failed preparation or evaluation was published")
			}
			if _, ok := store.Lookup(req.Package, req.Version); ok {
				t.Fatal("failed artifact became available")
			}
			requireEmptyStaging(t, store)
			req.Prepare = func(_ context.Context, output, _ string) error {
				if err := os.Mkdir(filepath.Join(output, "bin"), 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(output, "bin", "server"), []byte("fixed"), 0o700)
			}
			req.Evaluate = nil
			tool, err := store.Install(t.Context(), req)
			if err != nil || readFile(t, tool.Executable) != "fixed" {
				t.Fatalf("confirmed failure could not be retried: %+v %v", tool, err)
			}
		})
	}
}

func TestPreparedInstallRejectsChangedTreeAfterCheck(t *testing.T) {
	for _, phase := range []string{"evaluate", "preflight", "manifest"} {
		t.Run(phase, func(t *testing.T) {
			store := newStore(t)
			req := preparedRequest()
			req.Prepare = func(_ context.Context, output, _ string) error {
				if err := os.Mkdir(filepath.Join(output, "bin"), 0o700); err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(output, "bin", "server"), []byte("original"), 0o700)
			}
			var staged string
			change := func() error {
				if err := os.Chmod(staged, 0o700); err != nil {
					return err
				}
				return os.WriteFile(staged, []byte("modified"), 0o700)
			}
			req.Evaluate = func(_ context.Context, tool Tool) error {
				staged = tool.Executable
				if phase == "evaluate" {
					return change()
				}
				if phase == "manifest" {
					name := filepath.Join(tool.Dir, manifestName)
					if err := os.Chmod(name, 0o600); err != nil {
						return err
					}
					return os.WriteFile(name, []byte("{}\n"), 0o600)
				}
				return nil
			}
			req.PreparePublication = func(context.Context) error {
				if phase == "preflight" {
					return change()
				}
				return nil
			}
			tool, err := store.Install(t.Context(), req)
			if err == nil || tool.ID != "" {
				t.Fatalf("changed checked bytes were published: %+v %v", tool, err)
			}
			if _, ok := store.Lookup(req.Package, req.Version); ok {
				t.Fatal("changed checked bytes became resolvable")
			}
		})
	}
}

func TestPreparedInstallRetainsUncertainTreeAndRefusesRetry(t *testing.T) {
	store := newStore(t)
	req := preparedRequest()
	var output string
	req.Prepare = func(_ context.Context, dest, _ string) error {
		output = dest
		if err := os.WriteFile(filepath.Join(dest, "still-owned"), []byte("bytes"), 0o600); err != nil {
			return err
		}
		return &fileio.CommandTreeUnverifiedError{Err: errors.New("escaped process")}
	}
	_, err := store.Install(t.Context(), req)
	var uncertain *PreparationUnverifiedError
	if !errors.As(err, &uncertain) {
		t.Fatalf("uncertain preparation was not reported: %v", err)
	}
	if readFile(t, filepath.Join(output, "still-owned")) != "bytes" {
		t.Fatal("uncertain preparation lost its staged bytes")
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := reopened.Unrecognized(t.Context())
	var staged, reserved bool
	for _, item := range unknown {
		staged = staged || strings.HasPrefix(item, ".staging/")
		reserved = reserved || strings.HasPrefix(item, preparationPendingDir+"/")
	}
	if err != nil || !staged || !reserved {
		t.Fatalf("uncertain staging is not inspectable: %v %v", unknown, err)
	}
	called := false
	req.Prepare = func(context.Context, string, string) error { called = true; return nil }
	if _, err := reopened.Install(t.Context(), req); err == nil || called {
		t.Fatal("an uncertain preparation was retried")
	}
	if _, ok := reopened.Lookup(req.Package, req.Version); ok {
		t.Fatal("an uncertain preparation was published")
	}
}

func TestPreparedInstallRejectsMissingProgramAndUnsafeTree(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		write      func(string) error
	}{
		{"missing program", "no regular file", func(dir string) error {
			return os.WriteFile(filepath.Join(dir, "other"), []byte("other"), 0o600)
		}},
		{"budget exceeded", "budget", func(dir string) error {
			if err := os.Mkdir(filepath.Join(dir, "bin"), 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(dir, "bin", "server"), []byte(strings.Repeat("x", 128)), 0o700)
		}},
		{"escaping link", "points outside", func(dir string) error {
			return os.Symlink("../outside", filepath.Join(dir, "escape"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "escaping link" {
				check := t.TempDir()
				if err := os.Symlink("target", filepath.Join(check, "link")); err != nil {
					t.Skipf("native symlinks are unavailable: %v", err)
				}
			}
			store := newStore(t)
			req := preparedRequest()
			if tc.name == "budget exceeded" {
				req.MaxExtractedBytes = 32
			}
			req.Prepare = func(_ context.Context, output, _ string) error {
				return tc.write(output)
			}
			_, err := store.Install(t.Context(), req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("invalid prepared tree: %v", err)
			}
			if _, ok := store.Lookup(req.Package, req.Version); ok {
				t.Fatal("invalid prepared tool became available")
			}
			requireEmptyStaging(t, store)
		})
	}
}

func TestPreparedInstallCrashAndMalformedMarkerFailClosed(t *testing.T) {
	store := newStore(t)
	req := preparedRequest()
	var output string
	req.Prepare = func(_ context.Context, dest, _ string) error {
		output = dest
		if err := os.WriteFile(filepath.Join(dest, "bytes"), []byte("retained"), 0o600); err != nil {
			return err
		}
		panic("interrupted preparation")
	}
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_, _ = store.Install(t.Context(), req)
	}()
	if recovered == nil || readFile(t, filepath.Join(output, "bytes")) != "retained" {
		t.Fatal("interrupted preparation did not retain its bytes")
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	req.Prepare = func(context.Context, string, string) error { t.Fatal("replayed interrupted work"); return nil }
	if _, err := reopened.Install(t.Context(), req); err == nil {
		t.Fatal("interrupted preparation was replayed")
	}
	marker := filepath.Join(filepath.Dir(output), preparationPendingName)
	data, err := os.ReadFile(marker)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"package": "prepared-fixture",`,
		`"package": "prepared-fixture", "package": "other",`, 1))
	if err := os.WriteFile(marker, data, 0o600); err != nil {
		t.Fatal(err)
	}
	req.Package = "other"
	if _, err := reopened.Install(t.Context(), req); err == nil {
		t.Fatal("ambiguous preparation marker permitted another install")
	}
}

func TestPreparedInstallCopiesHardLinksBeforeSealing(t *testing.T) {
	store := newStore(t)
	source := filepath.Join(t.TempDir(), "source")
	if err := os.WriteFile(source, []byte("original"), 0o700); err != nil {
		t.Fatal(err)
	}
	req := preparedRequest()
	req.Prepare = func(_ context.Context, output, _ string) error {
		if err := os.Mkdir(filepath.Join(output, "bin"), 0o700); err != nil {
			return err
		}
		if err := os.Link(source, filepath.Join(output, "bin", "server")); err != nil {
			t.Skipf("hard links are unavailable: %v", err)
		}
		return nil
	}
	tool, err := store.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("modified"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, tool.Executable); got != "original" {
		t.Fatalf("published bytes still share an external hard link: %q", got)
	}
}

// pinnedFolderRefusesReplacement reports whether the platform refuses to move
// a folder the store holds open (prepareTree's os.Root on the output folder).
// Windows does: a preparation cannot replace the output or staging folder at
// all, so the attempt fails inside Prepare. Elsewhere the move succeeds and the
// store must detect the replacement afterwards.
func pinnedFolderRefusesReplacement() bool { return runtime.GOOS == "windows" }

func TestPreparedInstallRejectsReplacedOutputFolder(t *testing.T) {
	store := newStore(t)
	req := preparedRequest()
	var moveErr error
	req.Prepare = func(_ context.Context, output, _ string) error {
		if moveErr = os.Rename(output, output+"-old"); moveErr != nil {
			return moveErr
		}
		if err := os.MkdirAll(filepath.Join(output, "bin"), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(output, "bin", "server"), []byte("replacement"), 0o700)
	}
	_, err := store.Install(t.Context(), req)
	if pinnedFolderRefusesReplacement() {
		if moveErr == nil || !errors.Is(err, moveErr) {
			t.Fatalf("the open output folder could be replaced: move=%v install=%v", moveErr, err)
		}
	} else if err == nil || !strings.Contains(err.Error(), "output folder changed") {
		t.Fatalf("replacement output was not rejected: %v", err)
	}
	if _, ok := store.Lookup(req.Package, req.Version); ok {
		t.Fatal("replacement output was published")
	}
	requireEmptyStaging(t, store)
}

func TestPreparedInstallRetainsReservationIfStagingIsReplaced(t *testing.T) {
	store := newStore(t)
	req := preparedRequest()
	var staging string
	var moveErr error
	req.Prepare = func(_ context.Context, output, _ string) error {
		staging = filepath.Dir(output)
		if moveErr = os.Rename(staging, staging+"-moved"); moveErr != nil {
			return moveErr
		}
		if err := os.Mkdir(staging, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(staging, "unrelated"), []byte("keep"), 0o600)
	}
	_, err := store.Install(t.Context(), req)
	if pinnedFolderRefusesReplacement() {
		// The staging folder cannot move, so the failed preparation is
		// verified, removed, and a new attempt is admitted.
		if moveErr == nil || !errors.Is(err, moveErr) {
			t.Fatalf("the open staging folder could be replaced: move=%v install=%v", moveErr, err)
		}
		if _, ok := store.Lookup(req.Package, req.Version); ok {
			t.Fatal("failed preparation was published")
		}
		requireEmptyStaging(t, store)
		req.Prepare = func(_ context.Context, output, _ string) error {
			if err := os.MkdirAll(filepath.Join(output, "bin"), 0o700); err != nil {
				return err
			}
			return os.WriteFile(filepath.Join(output, "bin", "server"), []byte("compiled executable"), 0o700)
		}
		if _, err := store.Install(t.Context(), req); err != nil {
			t.Fatalf("a verified failure blocked the next attempt: %v", err)
		}
		return
	}
	var uncertain *PreparationUnverifiedError
	if !errors.As(err, &uncertain) {
		t.Fatalf("replaced staging was not reported as uncertain: %v", err)
	}
	if readFile(t, filepath.Join(staging, "unrelated")) != "keep" {
		t.Fatal("replaced staging data was deleted")
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	req.Prepare = func(context.Context, string, string) error { t.Fatal("replayed uncertain work"); return nil }
	if _, err := reopened.Install(t.Context(), req); err == nil {
		t.Fatal("replaced staging permitted another attempt")
	}
	if readFile(t, filepath.Join(staging, "unrelated")) != "keep" {
		t.Fatal("reopening deleted the replacement staging folder")
	}
}

func TestPreparedInstallDoesNotRemoveReplacedStageMarker(t *testing.T) {
	store := newStore(t)
	req := preparedRequest()
	var marker string
	req.Prepare = func(_ context.Context, output, _ string) error {
		marker = filepath.Join(filepath.Dir(output), preparationPendingName)
		if err := os.Remove(marker); err != nil {
			return err
		}
		return os.WriteFile(marker, []byte("replacement data"), 0o600)
	}
	_, err := store.Install(t.Context(), req)
	var uncertain *PreparationUnverifiedError
	if !errors.As(err, &uncertain) {
		t.Fatalf("replacement marker was not treated as uncertain: %v", err)
	}
	if got := readFile(t, marker); got != "replacement data" {
		t.Fatalf("replacement marker was destroyed: %q", got)
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	req.Prepare = func(context.Context, string, string) error {
		t.Fatal("replayed uncertain work")
		return nil
	}
	if _, err := reopened.Install(t.Context(), req); err == nil {
		t.Fatal("replacement marker permitted another attempt")
	}
}

func TestMalformedPreparationReservationKeepsStagingInspectable(t *testing.T) {
	store := newStore(t)
	staging, err := os.MkdirTemp(filepath.Join(store.root, stagingName), "install-")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(staging, "work"), []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(store.root, preparationPendingDir, "fixture")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "1"), []byte("{bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatalf("a malformed record hid the store from inspection: %v", err)
	}
	if readFile(t, filepath.Join(staging, "work")) != "keep" {
		t.Fatal("ambiguous preparation caused staging deletion")
	}
	unknown, err := reopened.Unrecognized(t.Context())
	if err != nil || len(unknown) == 0 || !strings.Contains(strings.Join(unknown, "\n"), preparationPendingDir+"/fixture/1") {
		t.Fatalf("malformed record was not exposed for inspection: %v %v", unknown, err)
	}
	req := preparedRequest()
	called := false
	req.Prepare = func(context.Context, string, string) error { called = true; return nil }
	_, err = reopened.Install(t.Context(), req)
	var unverified *PreparationUnverifiedError
	if !errors.As(err, &unverified) || called {
		t.Fatalf("an ambiguous record allowed a new installation or a retry: %v", err)
	}
}

func TestPreparedFixtureProcess(t *testing.T) {
	mode := os.Getenv("CRUDE_PREPARED_FIXTURE")
	if mode == "" {
		return
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	if len(args) > 0 {
		args = args[1:]
	}
	switch mode {
	case "prepare":
		if len(args) != 1 {
			t.Fatal("missing output directory")
		}
		if err := os.MkdirAll(filepath.Join(args[0], "bin"), 0o700); err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		source, err := os.Open(executable)
		if err != nil {
			t.Fatal(err)
		}
		defer source.Close()
		target, err := os.Create(filepath.Join(args[0], filepath.FromSlash(preparedFixtureBin())))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(target, source); err != nil {
			_ = target.Close()
			t.Fatal(err)
		}
		if err := target.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(filepath.Join(args[0], filepath.FromSlash(preparedFixtureBin())), 0o700); err != nil {
			t.Fatal(err)
		}
	case "evaluate":
		fmt.Println("PREPARED FIXTURE READY")
	default:
		t.Fatal("unknown fixture mode")
	}
}

func preparedFixtureBin() string {
	if runtime.GOOS == "windows" {
		return "bin/server.exe"
	}
	return "bin/server"
}

func TestPreparedInstallRunsAndReopensOwnedProcess(t *testing.T) {
	store := newStore(t)
	req := preparedRequest()
	req.Bin = preparedFixtureBin()
	req.MaxExtractedBytes = 256 << 20
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func(ctx context.Context, executable, mode, directory string, args ...string) error {
		command := fileio.OwnedCommand{
			Executable: executable, Arguments: append([]string{"-test.run=^TestPreparedFixtureProcess$", "--"}, args...),
			Directory: directory, Environment: append(os.Environ(), "CRUDE_PREPARED_FIXTURE="+mode),
			OutputLimit: 64 << 10,
		}
		result, err := fileio.RunOwnedCommand(ctx, command)
		if err != nil {
			return err
		}
		if mode == "evaluate" && !strings.Contains(string(result.Output), "PREPARED FIXTURE READY") {
			return fmt.Errorf("fixture did not execute: %q", result.Output)
		}
		return nil
	}
	req.Prepare = func(ctx context.Context, output, scratch string) error {
		return run(ctx, executable, "prepare", scratch, output)
	}
	req.Evaluate = func(ctx context.Context, tool Tool) error {
		return run(ctx, tool.Executable, "evaluate", tool.Dir)
	}
	req.AdmitPublication = func(ctx context.Context) error { return ctx.Err() }
	tool, err := store.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(t.Context(), tool.Executable, "evaluate", tool.Dir); err != nil {
		t.Fatalf("published executable cannot start: %v", err)
	}
	reopened, err := Open(store.root)
	if err != nil {
		t.Fatal(err)
	}
	found, ok := reopened.Lookup(req.Package, req.Version)
	if !ok || found.ID != tool.ID {
		t.Fatal("reopened store lost the prepared executable")
	}
	if err := run(t.Context(), found.Executable, "evaluate", found.Dir); err != nil {
		t.Fatalf("reopened executable cannot start: %v", err)
	}
}

func TestPreparedTreeDigestBindsContentsWithoutTimes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "program")
	if err := os.WriteFile(path, []byte("first"), 0o700); err != nil {
		t.Fatal(err)
	}
	limits := extractLimits{bytes: 100, entries: 10}
	first, err := preparedTreeDigest(t.Context(), dir, limits)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("other"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}
	changed, err := preparedTreeDigest(t.Context(), dir, limits)
	if err != nil || changed == first {
		t.Fatalf("digest did not bind changed same-size contents: %q %v", changed, err)
	}
}
