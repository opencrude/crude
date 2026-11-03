package toolstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPreparePublicationRunsOffLockAndCancellationPreventsPublish(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool": []byte("binary")})
	req := request(server.URL+"/tool", ArchiveRaw, "tool")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	prepared := false
	req.PreparePublication = func(context.Context) error {
		prepared = true
		if !store.mu.TryLock() {
			t.Fatal("expensive publication preparation ran under store.mu")
		}
		store.mu.Unlock()
		cancel()
		return nil
	}
	req.AdmitPublication = func(context.Context) error {
		t.Error("cancelled preparation reached publication admission")
		return nil
	}
	tool, err := store.Install(ctx, req)
	if !prepared || !errors.Is(err, context.Canceled) || tool.ID != "" {
		t.Fatalf("prepared=%v tool=%+v error=%v", prepared, tool, err)
	}
	if _, ok := store.Lookup(req.Package, req.Version); ok {
		t.Fatal("cancelled preparation published an artifact")
	}
	history := events(t, store)
	if len(history) != 1 || history[0].Kind != EventInstallFailed {
		t.Fatalf("cancelled preparation history = %+v", history)
	}
	requireEmptyStaging(t, store)
}

func TestPreparePublicationRefusalAndExistingArtifact(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool": []byte("binary")})
	req := request(server.URL+"/tool", ArchiveRaw, "tool")
	refused := errors.New("the reviewed project changed")
	req.PreparePublication = func(context.Context) error { return refused }
	if _, err := store.Install(t.Context(), req); !errors.Is(err, refused) {
		t.Fatalf("preparation refusal = %v", err)
	}
	req.PreparePublication = nil
	first, err := store.Install(t.Context(), req)
	if err != nil {
		t.Fatal(err)
	}
	req.PreparePublication = func(context.Context) error {
		t.Error("existing artifact requested new publication preparation")
		return refused
	}
	if again, err := store.Install(t.Context(), req); err != nil || again.ID != first.ID {
		t.Fatalf("existing artifact = %+v, %v", again, err)
	}
}

func TestMarkUsedContextQueuedCancellationAndRefusalRecordNothing(t *testing.T) {
	for _, mode := range []string{"cancelled", "refused"} {
		t.Run(mode, func(t *testing.T) {
			store := newStore(t)
			server := serve(t, map[string][]byte{"/tool": []byte("binary")})
			req := request(server.URL+"/tool", ArchiveRaw, "tool")
			tool, err := store.Install(t.Context(), req)
			if err != nil {
				t.Fatal(err)
			}
			store.mu.Lock()
			ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
			defer cancel()
			refused := errors.New("the original effect is no longer current")
			called := false
			done := make(chan error, 1)
			go func() {
				done <- store.MarkUsedContext(ctx, tool.ID, "workspace", func(context.Context) error {
					called = true
					if store.mu.TryLock() {
						store.mu.Unlock()
						t.Error("usage admission did not hold store.mu")
					}
					return refused
				})
			}()
			if mode == "cancelled" {
				err = <-done
				store.mu.Unlock()
				if !errors.Is(err, context.DeadlineExceeded) || called {
					t.Fatalf("cancelled queued use = %v, called=%v", err, called)
				}
			} else {
				store.mu.Unlock()
				err = <-done
				if !errors.Is(err, refused) || !called {
					t.Fatalf("refused queued use = %v, called=%v", err, called)
				}
			}
			current, ok := store.Lookup(req.Package, req.Version)
			if !ok || len(current.Workspaces) != 0 || !current.LastUsed.IsZero() {
				t.Fatalf("refused use changed accounting: %+v", current)
			}
			history := events(t, store)
			if len(history) != 1 || history[0].Kind != EventInstall {
				t.Fatalf("refused use changed history: %+v", history)
			}
			if _, err := os.Stat(filepath.Join(store.root, usageName)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("refused use wrote usage file: %v", err)
			}
		})
	}
}

func TestExecutableLeasesSerializeRemovalAndRetainBothWorkspaces(t *testing.T) {
	store := newStore(t)
	server := serve(t, map[string][]byte{"/tool": []byte("binary")})
	tool, err := store.Install(t.Context(), request(server.URL+"/tool", ArchiveRaw, "tool"))
	if err != nil {
		t.Fatal(err)
	}
	first, managed, err := store.AcquireExecutable(t.Context(), tool.Executable)
	if err != nil || !managed || first == nil {
		t.Fatalf("first executable lease = %v, %v", managed, err)
	}
	defer first()
	second, managed, err := store.AcquireExecutable(t.Context(), tool.Executable)
	if err != nil || !managed || second == nil {
		t.Fatalf("second executable lease = %v, %v", managed, err)
	}
	defer second()
	for _, workspace := range []string{"workspace-a", "workspace-b"} {
		if err := store.MarkExecutableUsedContext(t.Context(), tool.Executable, workspace, nil); err != nil {
			t.Fatal(err)
		}
	}
	p, err := store.PreviewRemove(t.Context(), []string{tool.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Tools[0].Workspaces) != 2 {
		t.Fatalf("usage did not retain both workspaces: %+v", p.Tools[0])
	}
	if err := store.Remove(t.Context(), p, "user"); !errors.Is(err, ErrExecutableInUse) {
		t.Fatalf("leased tool removal = %v", err)
	}
	first()
	first() // An old duplicate release cannot release the other workspace's pin.
	if err := store.Remove(t.Context(), p, "user"); !errors.Is(err, ErrExecutableInUse) {
		t.Fatalf("second workspace pin lost: %v", err)
	}
	second()
	if err := store.Remove(t.Context(), p, "user"); err != nil {
		t.Fatal(err)
	}
	if release, managed, err := store.AcquireExecutable(t.Context(), tool.Executable); err == nil || !managed || release != nil {
		t.Fatalf("removed executable was accepted: managed=%v error=%v", managed, err)
	}
}

func TestExecutableLeaseRejectsUnconfirmedUnknownAndMissing(t *testing.T) {
	for _, mode := range []string{"unconfirmed", "unknown", "missing", "manifest"} {
		t.Run(mode, func(t *testing.T) {
			store := newStore(t)
			server := serve(t, map[string][]byte{"/tool": []byte("binary")})
			tool, err := store.Install(t.Context(), request(server.URL+"/tool", ArchiveRaw, "tool"))
			if err != nil {
				t.Fatal(err)
			}
			executable := tool.Executable
			switch mode {
			case "unconfirmed":
				store.unconfirmed[tool.ID] = true
			case "unknown":
				executable = filepath.Join(store.root, "foreign", "tool")
			case "missing":
				if err := removeTree(tool.Executable); err != nil {
					t.Fatal(err)
				}
			case "manifest":
				if err := removeTree(filepath.Join(tool.Dir, manifestName)); err != nil {
					t.Fatal(err)
				}
			}
			if release, managed, err := store.AcquireExecutable(t.Context(), executable); err == nil || !managed || release != nil {
				t.Fatalf("%s executable was accepted: managed=%v error=%v", mode, managed, err)
			}
		})
	}
	store := newStore(t)
	if release, managed, err := store.AcquireExecutable(t.Context(), filepath.Join(t.TempDir(), "external")); err != nil || managed || release != nil {
		t.Fatalf("external executable = managed=%v error=%v", managed, err)
	}
}

func TestExecutableLeaseWaitIsCancellable(t *testing.T) {
	store := newStore(t)
	store.mu.Lock()
	defer store.mu.Unlock()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	release, managed, err := store.AcquireExecutable(ctx, filepath.Join(store.root, "unknown"))
	if !errors.Is(err, context.DeadlineExceeded) || !managed || release != nil {
		t.Fatalf("queued lease = managed=%v error=%v", managed, err)
	}
}
