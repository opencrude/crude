package workspace

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// holdMetadataRead models the exact lifetime borrowed by catalog workers: the
// read lease outlives the real SQL handle, even when a caller has timed out.
func holdMetadataRead(t *testing.T, dbPath string) func() {
	t.Helper()
	release, err := lockWorkspaceStorage(t.Context(), false)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(dbPath)+"?mode=ro&_busy_timeout=1")
	if err != nil {
		release()
		t.Fatal(err)
	}
	var project string
	if err := db.QueryRowContext(t.Context(), "SELECT value FROM workspace_metadata WHERE key = 'project_dir'").Scan(&project); err != nil || project == "" {
		_ = db.Close()
		release()
		t.Fatalf("metadata fixture could not borrow the saved SQL handle: %q %v", project, err)
	}
	released := false
	closeRead := func() {
		if released {
			return
		}
		released = true
		if err := db.Close(); err != nil {
			t.Error(err)
		}
		release()
	}
	t.Cleanup(closeRead)
	return closeRead
}

func TestWorkspaceStorageMetadataLeaseBlocksMovesAndCancellationPreservesData(t *testing.T) {
	project, saved := savedWorkspaceFixture(t)
	dbPath := filepath.Join(saved, "workspace.db")
	before, err := os.ReadFile(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := PreviewWorkspaceDeletion(t.Context(), WorkspaceID(project), project, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	closeRead := holdMetadataRead(t, dbPath)
	// Shared reads remain concurrent; serializing the catalog behind itself
	// would hide the Windows move race at the cost of a new catalog deadlock.
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	secondRead, err := lockWorkspaceStorage(ctx, false)
	cancel()
	if err != nil {
		t.Fatalf("metadata read leases are not shared: %v", err)
	}
	secondRead()
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	_, err = PreviewWorkspaceDeletion(ctx, WorkspaceID(project), project, t.TempDir())
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("preview bypassed an outstanding metadata SQL lifetime: %v", err)
	}
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	deleted, err := DeleteSavedWorkspace(ctx, plan)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) || deleted.RecoveryPath != "" {
		t.Fatalf("delete did not cancel before moving leased storage: deleted=%+v err=%v", deleted, err)
	}
	after, err := os.ReadFile(dbPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("canceled delete altered its source database: %v", err)
	}
	recoveryRoot := filepath.Join(filepath.Dir(filepath.Dir(saved)), "deleted-workspaces")
	if _, err := os.Stat(recoveryRoot); !os.IsNotExist(err) {
		t.Fatalf("canceled storage admission created recovery artifacts: %v", err)
	}
	closeRead()
	// Cancellation must not consume the immutable preimage or leak the gate.
	deleted, err = DeleteSavedWorkspace(t.Context(), plan)
	if err != nil {
		t.Fatalf("released SQL handle did not permit the original move: %v", err)
	}
	recoveryDB := filepath.Join(deleted.RecoveryPath, "data", "workspace.db")
	closeRecoveryRead := holdMetadataRead(t, recoveryDB)
	ctx, cancel = context.WithTimeout(t.Context(), 100*time.Millisecond)
	err = RestoreDeletedWorkspace(ctx, deleted)
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Undo bypassed an outstanding metadata SQL lifetime: %v", err)
	}
	if _, err := os.Stat(saved); !os.IsNotExist(err) {
		t.Fatalf("canceled Undo recreated source storage: %v", err)
	}
	after, err = os.ReadFile(recoveryDB)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("canceled Undo altered recovery data: %v", err)
	}
	if _, err := os.Stat(filepath.Join(deleted.RecoveryPath, "workspace.json")); err != nil {
		t.Fatalf("canceled Undo lost its recovery manifest: %v", err)
	}
	closeRecoveryRead()
	if err := RestoreDeletedWorkspace(t.Context(), deleted); err != nil {
		t.Fatalf("released recovery SQL handle did not permit Undo: %v", err)
	}
	after, err = os.ReadFile(dbPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("delete/Undo changed the saved database: %v", err)
	}
	evidence, err := os.ReadFile(filepath.Join(saved, "sessions", "evidence.txt"))
	if err != nil || string(evidence) != "exact saved conversation\n" {
		t.Fatalf("cancellation lost saved conversation content: %q %v", evidence, err)
	}
	projectText, err := os.ReadFile(filepath.Join(project, "source.txt"))
	if err != nil || string(projectText) != "project stays untouched\n" {
		t.Fatalf("storage coordination changed project bytes: %q %v", projectText, err)
	}
}

func TestWorkspaceStorageFullAndLightMetadataReadsRespectMoveLease(t *testing.T) {
	project, saved := savedWorkspaceFixture(t)
	dbPath := filepath.Join(saved, "workspace.db")
	release, err := lockWorkspaceStorage(t.Context(), true)
	if err != nil {
		t.Fatal(err)
	}
	released := false
	unblock := func() {
		if !released {
			released = true
			release()
		}
	}
	t.Cleanup(unblock)
	readers := []struct {
		name string
		read func(context.Context, string) *WorkspaceInfo
	}{{"full", readWorkspaceMeta}, {"light", readWorkspaceMetaLight}}
	for _, reader := range readers {
		t.Run(reader.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			if info := reader.read(ctx, dbPath); info != nil {
				t.Fatalf("%s metadata reader opened storage reserved for a move: %+v", reader.name, info)
			}
			if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
				t.Fatalf("%s reader did not wait on the cancellable storage gate: %v", reader.name, ctx.Err())
			}
		})
	}
	unblock()
	for _, reader := range readers {
		if info := reader.read(t.Context(), dbPath); info == nil || info.ProjectDir != project {
			t.Fatalf("%s metadata reader did not recover after canceled admission: %+v", reader.name, info)
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err = lockWorkspaceStorage(ctx, true)
	if err != nil {
		t.Fatalf("canceled metadata readers leaked storage leases: %v", err)
	}
	release()
}
