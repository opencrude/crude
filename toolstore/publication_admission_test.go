package toolstore

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInstallPublicationAdmission(t *testing.T) {
	t.Run("revocation before admission refuses publication", func(t *testing.T) {
		store := newStore(t)
		server := serve(t, map[string][]byte{"/tool": []byte("binary")})
		req := request(server.URL+"/tool", ArchiveRaw, "tool")
		revoked := errors.New("review revoked")
		calls := 0
		req.AdmitPublication = func(ctx context.Context) error {
			calls++
			if err := ctx.Err(); err != nil {
				t.Fatal(err)
			}
			return revoked
		}
		tool, err := store.Install(t.Context(), req)
		if !errors.Is(err, revoked) || tool.ID != "" || calls != 1 {
			t.Fatalf("install = %+v, %v; admission calls %d", tool, err, calls)
		}
		if _, ok := store.Lookup(req.Package, req.Version); ok {
			t.Fatal("revoked artifact was published")
		}
		history := events(t, store)
		if len(history) != 1 || history[0].Kind != EventInstallFailed {
			t.Fatalf("refusal was not recorded: %+v", history)
		}
		requireEmptyStaging(t, store)
	})

	t.Run("revocation after admission permits only publication", func(t *testing.T) {
		store := newStore(t)
		server := serve(t, map[string][]byte{"/tool": []byte("binary")})
		req := request(server.URL+"/tool", ArchiveRaw, "tool")
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		calls := 0
		req.AdmitPublication = func(ctx context.Context) error {
			calls++
			if err := ctx.Err(); err != nil {
				return err
			}
			// Admission wins before revocation. Publication is inert: the
			// store does not mark a workspace as using the artifact.
			cancel()
			return nil
		}
		tool, err := store.Install(ctx, req)
		if err != nil || tool.ID == "" || calls != 1 || ctx.Err() == nil {
			t.Fatalf("install = %+v, %v; admission calls %d", tool, err, calls)
		}
		if len(tool.Workspaces) != 0 || !tool.LastUsed.IsZero() {
			t.Fatalf("publication recorded a use: %+v", tool)
		}
		history := events(t, store)
		if len(history) != 1 || history[0].Kind != EventInstall || history[0].Approval != req.Approval {
			t.Fatalf("admitted publication was not audited: %+v", history)
		}
		requireEmptyStaging(t, store)
	})

	t.Run("existing artifact is not a new publication", func(t *testing.T) {
		store := newStore(t)
		server := serve(t, map[string][]byte{"/tool": []byte("binary")})
		req := request(server.URL+"/tool", ArchiveRaw, "tool")
		first, err := store.Install(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		req.AdmitPublication = func(context.Context) error {
			t.Error("existing artifact requested publication admission")
			return errors.New("unexpected admission")
		}
		again, err := store.Install(t.Context(), req)
		if err != nil || again.ID != first.ID || server.hits.Load() != 1 {
			t.Fatalf("existing artifact = %+v, %v; downloads %d", again, err, server.hits.Load())
		}
	})

	t.Run("cancellation before admission never calls the gate", func(t *testing.T) {
		store := newStore(t)
		tree := filepath.Join(store.root, stagingName, "candidate")
		if err := os.Mkdir(tree, 0700); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		called := false
		attempted, _, err := store.moveIntoPlace(ctx, func(context.Context) error {
			called = true
			return nil
		}, filepath.Join(store.root, "pkg"), tree, filepath.Join(store.root, "pkg", "version"))
		if attempted || called || !errors.Is(err, context.Canceled) {
			t.Fatalf("attempted=%v called=%v error=%v", attempted, called, err)
		}
		if _, err := os.Stat(tree); err != nil {
			t.Fatalf("cancelled publication moved the tree: %v", err)
		}
	})

	t.Run("cancellation while waiting for the store refuses admission", func(t *testing.T) {
		store := newStore(t)
		store.mu.Lock()
		defer store.mu.Unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		called := false
		attempted, _, err := store.moveIntoPlace(ctx, func(context.Context) error {
			called = true
			return nil
		}, filepath.Join(store.root, "pkg"), "unused", "unused")
		if attempted || called || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("attempted=%v called=%v error=%v", attempted, called, err)
		}
	})

	t.Run("install cancellation while the store is busy returns", func(t *testing.T) {
		store := newStore(t)
		server := serve(t, map[string][]byte{"/tool": []byte("binary")})
		store.mu.Lock()
		defer store.mu.Unlock()
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
		defer cancel()
		req := request(server.URL+"/tool", ArchiveRaw, "tool")
		req.AdmitPublication = func(context.Context) error {
			t.Error("cancelled install reached publication admission")
			return nil
		}
		tool, err := store.Install(ctx, req)
		if tool.ID != "" || !errors.Is(err, context.DeadlineExceeded) || server.hits.Load() != 0 {
			t.Fatalf("tool=%+v error=%v downloads=%d", tool, err, server.hits.Load())
		}
	})

	t.Run("history failure after rename is unconfirmed", func(t *testing.T) {
		store := newStore(t)
		server := serve(t, map[string][]byte{"/tool": []byte("binary")})
		if err := os.Mkdir(filepath.Join(store.root, historyName), 0700); err != nil {
			t.Fatal(err)
		}
		req := request(server.URL+"/tool", ArchiveRaw, "tool")
		req.AdmitPublication = func(ctx context.Context) error { return ctx.Err() }
		tool, err := store.Install(t.Context(), req)
		var unconfirmed *PublicationUnconfirmedError
		if !errors.As(err, &unconfirmed) || tool.ID == "" || unconfirmed.ToolID != tool.ID {
			t.Fatalf("published tool = %+v, error = %v", tool, err)
		}
		if _, err := os.Stat(tool.Executable); err != nil {
			t.Fatalf("test did not reach a real rename: %v", err)
		}
		if len(tool.Workspaces) != 0 || !tool.LastUsed.IsZero() {
			t.Fatalf("unconfirmed publication recorded a use: %+v", tool)
		}
	})

	t.Run("rename conflict after admission is unconfirmed", func(t *testing.T) {
		store := newStore(t)
		artifact := []byte("binary")
		server := serve(t, map[string][]byte{"/tool": artifact})
		req := request(server.URL+"/tool", ArchiveRaw, "tool")
		final := filepath.Join(store.root, req.Package, versionDir(req.Version, digestOf(artifact)))
		admitted := make(chan struct{})
		conflict := make(chan error, 1)
		go func() {
			<-admitted
			if err := os.Mkdir(final, 0700); err != nil {
				conflict <- err
				return
			}
			conflict <- os.WriteFile(filepath.Join(final, "foreign"), []byte("keep"), 0600)
		}()
		req.AdmitPublication = func(context.Context) error {
			close(admitted)
			return <-conflict
		}
		tool, err := store.Install(t.Context(), req)
		var unconfirmed *PublicationUnconfirmedError
		wantID := req.Package + "/" + versionDir(req.Version, digestOf(artifact))
		if !errors.As(err, &unconfirmed) || unconfirmed.ToolID != wantID || tool.ID != "" {
			t.Fatalf("tool = %+v, error = %v", tool, err)
		}
		if got := readFile(t, filepath.Join(final, "foreign")); got != "keep" {
			t.Fatalf("conflicting data changed: %q", got)
		}
		requireEmptyStaging(t, store)
	})
}
