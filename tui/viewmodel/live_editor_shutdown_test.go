package viewmodel

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/document"
	"crude/shellport"
)

var errRepeatedShutdownSync = errors.New("shutdown reread synchronized source instead of submitting pending text")

// Keep the real framed document owner. An independent RPC bound turns the old
// 35-second retry loop into an immediate, outcome-specific regression failure.
type shutdownSyncPort struct {
	shellport.Workspace
	shellport.LiveDocuments
	syncs, edits int
	failSync     error
}

func (p *shutdownSyncPort) SyncDocument(ctx context.Context, id string, after uint64) (document.SyncResult, error) {
	p.syncs++
	if p.failSync != nil {
		return document.SyncResult{}, p.failSync
	}
	if p.syncs > 2 {
		return document.SyncResult{}, errRepeatedShutdownSync
	}
	return p.LiveDocuments.SyncDocument(ctx, id, after)
}

func (p *shutdownSyncPort) EditDocument(ctx context.Context, change document.Change) (document.Receipt, error) {
	p.edits++
	return p.LiveDocuments.EditDocument(ctx, change)
}

func TestLiveEditorShutdownReconcilesPendingTextWithoutTopicReadiness(t *testing.T) {
	for _, refused := range []bool{false, true} {
		name := "accepted"
		if refused {
			name = "refused"
		}
		t.Run(name, func(t *testing.T) {
			runtime := remoteJourneyRuntime(t, "http://127.0.0.1:1")
			root := t.TempDir()
			path := filepath.Join(root, "shutdown.txt")
			if err := os.WriteFile(path, []byte("base"), 0600); err != nil {
				t.Fatal(err)
			}
			shell := newRemoteJourneyShell(t, runtime, root)
			ed := liveTestOpen(t, shell.app, path)
			app, original := shell.app, shell.app.ws
			live := original.(shellport.LiveDocuments)
			// This is the real post-program boundary, not a fabricated topic ACK.
			app.fenceLiveDocumentTopics()
			ed.InsertRune('X')
			port := &shutdownSyncPort{Workspace: original, LiveDocuments: live}
			if refused {
				port.failSync = errors.New("independent canonical read refusal")
			}
			app.ws = port
			t.Cleanup(func() { app.ws = original })
			if cmd := app.drainLiveEditorCommands(); cmd != nil || port.edits != 0 || port.syncs != 0 {
				t.Fatal("ordinary command admission bypassed its missing publication")
			}
			err := app.FlushLiveEditors()
			if refused {
				if !errors.Is(err, port.failSync) || port.edits != 0 || port.syncs != 1 {
					t.Fatalf("failed canonical read acquired shutdown write authority: syncs=%d edits=%d err=%v", port.syncs, port.edits, err)
				}
				recovery, retained := ed.ExportDocumentRecovery()
				if !retained || len(recovery.Pending) == 0 || ed.Value() != "Xbase" {
					t.Fatal("refused shutdown consumed private pending text")
				}
			} else {
				if err != nil || port.edits != 1 || port.syncs != 2 {
					t.Fatalf("shutdown did not submit/reconcile the exact pending text: syncs=%d edits=%d err=%v", port.syncs, port.edits, err)
				}
				if ed.Value() != "Xbase" || ed.DocumentState().Revision != 2 || ed.DocumentState().PendingEdits != 0 {
					t.Fatalf("shutdown lost its exact accepted projection: %q %+v", ed.Value(), ed.DocumentState())
				}
				if app.liveEditors[ed].topicReady {
					t.Fatal("shutdown read manufactured a topic-applied ACK")
				}
				ed.InsertRune('Y')
				if cmd := app.drainLiveEditorCommands(); cmd != nil || port.edits != 1 {
					t.Fatal("exclusive shutdown read leaked normal command authority")
				}
			}
			current, readErr := live.SyncDocument(t.Context(), path, 0)
			want := "Xbase"
			if refused {
				want = "base"
			}
			if readErr != nil || string(current.Snapshot.Text) != want {
				t.Fatalf("canonical shutdown text=%q want=%q err=%v", current.Snapshot.Text, want, readErr)
			}
			if data, readErr := os.ReadFile(path); readErr != nil || string(data) != "base" {
				t.Fatalf("flush implicitly saved disk: %q %v", data, readErr)
			}
		})
	}
}
