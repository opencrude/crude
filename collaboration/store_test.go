package collaboration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const testWorkspace = "/workspace/project"

func TestRuntimeOwnershipAcrossProcessExit(t *testing.T) {
	for _, abrupt := range []bool{false, true} {
		t.Run(fmt.Sprintf("abrupt=%t", abrupt), func(t *testing.T) {
			root := t.TempDir()
			db, err := sql.Open("sqlite", filepath.Join(root, "workspace.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			store, err := Attach(db, testWorkspace, filepath.Join(root, "locks"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCollaborationRuntimeOwnerProcess$")
			command.Env = append(os.Environ(), "CRUDE_COLLAB_OWNER_PROCESS=1", "CRUDE_COLLAB_ROOT="+root)
			stdout, err := command.StdoutPipe()
			if err != nil {
				t.Fatal(err)
			}
			stdin, err := command.StdinPipe()
			if err != nil {
				t.Fatal(err)
			}
			defer stdin.Close()
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if command.ProcessState == nil {
					_ = command.Process.Kill()
					_ = command.Wait()
				}
			}()
			var original Agent
			if err := json.NewDecoder(stdout).Decode(&original); err != nil {
				t.Fatalf("runtime owner did not become ready: %v", err)
			}
			if _, err := db.Exec(`UPDATE collaboration_agents SET heartbeat_at=1 WHERE id=?`, original.ID); err != nil {
				t.Fatal(err)
			}
			registration := AgentRegistration{ID: original.ID, Name: original.Name, SessionID: original.SessionID, Category: original.Category, OwnerInstance: "replacement"}
			if _, err := store.RegisterAgent(ctx, registration); !errors.Is(err, ErrConflict) {
				t.Fatalf("old heartbeat allowed another process to steal the runtime: %v", err)
			}
			live, err := store.Agent(ctx, original.ID)
			if err != nil || live.OwnerInstance != original.OwnerInstance || live.RuntimeEpoch != original.RuntimeEpoch {
				t.Fatalf("live process ownership changed: %+v err=%v", live, err)
			}
			if abrupt {
				if err := command.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else if err := stdin.Close(); err != nil {
				t.Fatal(err)
			}
			waitErr := command.Wait()
			if abrupt {
				var exit *exec.ExitError
				if !errors.As(waitErr, &exit) {
					t.Fatalf("terminated fixture did not exit: %v", waitErr)
				}
			} else if waitErr != nil {
				t.Fatal(waitErr)
			}
			replacement, err := store.RegisterAgent(ctx, registration)
			if err != nil || replacement.RuntimeEpoch != original.RuntimeEpoch+1 || replacement.OwnerInstance != "replacement" {
				t.Fatalf("runtime did not recover after process exit: %+v err=%v", replacement, err)
			}
			if err := store.Heartbeat(ctx, original.ID, original.OwnerInstance, original.RuntimeEpoch); !errors.Is(err, ErrStale) {
				t.Fatalf("old process epoch remained authoritative: %v", err)
			}
		})
	}
}

func TestCollaborationRuntimeOwnerProcess(t *testing.T) {
	if os.Getenv("CRUDE_COLLAB_OWNER_PROCESS") != "1" {
		return
	}
	root := os.Getenv("CRUDE_COLLAB_ROOT")
	db, err := sql.Open("sqlite", filepath.Join(root, "workspace.db")+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := Attach(db, testWorkspace, filepath.Join(root, "locks"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	owner, err := store.RegisterAgent(t.Context(), AgentRegistration{ID: "process-owned", Name: "Jeff", SessionID: "session", Category: "crafter", OwnerInstance: "original"})
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(owner); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}

func openTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "workspace.db")
	database, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)")
	if err != nil {
		t.Fatal(err)
	}
	database.SetMaxOpenConns(8)
	t.Cleanup(func() { _ = database.Close() })
	store, err := Attach(database, testWorkspace, filepath.Join(filepath.Dir(path), "locks"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store, database
}

func registerTestAgents(t *testing.T, store *Store) (Agent, Agent) {
	t.Helper()
	root, err := store.RegisterAgent(t.Context(), AgentRegistration{
		ID: "root", Name: "Rebecca", SessionID: "session-root",
		Category: "crafter", OwnerInstance: "process-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	child, err := store.RegisterAgent(t.Context(), AgentRegistration{
		ID: "child", Name: "Asha sub Rebecca", SessionID: "session-child",
		ParentID: root.ID, Category: "researcher", OwnerInstance: "process-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	return root, child
}

func TestWorkspaceAgentsUseStableEpochsAndOneRuntimeOwner(t *testing.T) {
	store, database := openTestStore(t)
	root, child := registerTestAgents(t, store)
	if root.RuntimeEpoch != 1 || child.ParentID != root.ID || child.Presence != RuntimeAttached {
		t.Fatalf("registration = root=%+v child=%+v", root, child)
	}
	if _, err := store.RegisterAgent(t.Context(), AgentRegistration{
		ID: root.ID, Name: root.Name, SessionID: root.SessionID,
		Category: root.Category, OwnerInstance: "process-b",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second runtime owner = %v", err)
	}
	if err := store.DetachAgent(t.Context(), root.ID, "process-a", root.RuntimeEpoch); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.RegisterAgent(t.Context(), AgentRegistration{
		ID: root.ID, Name: root.Name, SessionID: root.SessionID,
		Category: root.Category, OwnerInstance: "process-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reopened.RuntimeEpoch != root.RuntimeEpoch+1 || reopened.OwnerInstance != "process-b" {
		t.Fatalf("new runtime epoch = %+v", reopened)
	}
	if err := store.Heartbeat(t.Context(), root.ID, "process-a", root.RuntimeEpoch); !errors.Is(err, ErrStale) {
		t.Fatalf("stale heartbeat = %v", err)
	}
	if _, err := Attach(database, "/different/workspace", t.TempDir()); !errors.Is(err, ErrWorkspace) {
		t.Fatalf("cross-workspace attach = %v", err)
	}
	agents, err := store.ListAgents(t.Context(), AgentQuery{Query: "asha"})
	if err != nil || len(agents) != 1 || agents[0].ID != child.ID {
		t.Fatalf("agent query = %+v, %v", agents, err)
	}
}

func TestRuntimeOwnershipUsesProcessLockAndEpochTakeover(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workspace.db")
	open := func() *sql.DB {
		database, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)")
		if err != nil {
			t.Fatal(err)
		}
		database.SetMaxOpenConns(4)
		t.Cleanup(func() { _ = database.Close() })
		return database
	}
	lockDir := filepath.Join(dir, "locks")
	first, err := Attach(open(), testWorkspace, lockDir)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Attach(open(), testWorkspace, lockDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close(); _ = second.Close() })

	original, err := first.RegisterAgent(t.Context(), AgentRegistration{
		ID: "root", Name: "Rebecca", SessionID: "session-root",
		Category: "crafter", OwnerInstance: "process-a",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.RegisterAgent(t.Context(), AgentRegistration{
		ID: "root", Name: "Rebecca", SessionID: "session-root",
		Category: "crafter", OwnerInstance: "process-b",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("second live process acquired the same agent: %v", err)
	}

	// Simulate process exit without a database detach. Releasing the advisory
	// lock permits a new owner; the durable epoch fences the stale runtime.
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	takeover, err := second.RegisterAgent(t.Context(), AgentRegistration{
		ID: "root", Name: "Rebecca", SessionID: "session-root",
		Category: "crafter", OwnerInstance: "process-b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if takeover.RuntimeEpoch != original.RuntimeEpoch+1 || takeover.OwnerInstance != "process-b" {
		t.Fatalf("takeover=%+v original=%+v", takeover, original)
	}
	if err := first.Heartbeat(t.Context(), original.ID, "process-a", original.RuntimeEpoch); !errors.Is(err, ErrStale) {
		t.Fatalf("stale process heartbeat = %v", err)
	}
}

func TestRunLifecycleUsesVersionAndEpochCAS(t *testing.T) {
	store, _ := openTestStore(t)
	root, _ := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{
		ID: "run-root-1", AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch,
		RequestID: "request-1", Origin: OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.State != RunRunning || run.Version != 1 {
		t.Fatalf("started run = %+v", run)
	}
	if _, err := store.StartRun(t.Context(), StartRunRequest{
		ID: "run-root-2", AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch, Origin: OriginPeer,
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("parallel run = %v", err)
	}
	paused, err := store.TransitionRun(t.Context(), run.ID, root.ID, root.RuntimeEpoch, run.Version, RunPausing)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(t.Context(), FinishRunRequest{
		RunID: run.ID, AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch,
		ExpectedVersion: run.Version, State: RunCancelled,
	}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale terminal update = %v", err)
	}
	finished, err := store.FinishRun(t.Context(), FinishRunRequest{
		RunID: run.ID, AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch,
		ExpectedVersion: paused.Version, State: RunCancelled, Outcome: "stopped by human",
		ContextCommitID: "context-commit-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !finished.State.Terminal() || finished.Outcome != "stopped by human" {
		t.Fatalf("finished run = %+v", finished)
	}
	agent, err := store.Agent(t.Context(), root.ID)
	if err != nil || agent.CurrentRunID != "" {
		t.Fatalf("terminal run retained owner = %+v, %v", agent, err)
	}
}

func TestDeliveryReservationPreventsFalseWithdrawalAndDuplicateAdmission(t *testing.T) {
	store, _ := openTestStore(t)
	root, child := registerTestAgents(t, store)
	first, err := store.Publish(t.Context(), PublishRequest{
		SenderID: root.ID, IdempotencyKey: "message-1", RecipientIDs: []string{child.ID},
		Origin: OriginPeer, Kind: EventPeerNote, Body: "Review the parser ownership.",
	})
	if err != nil {
		t.Fatal(err)
	}
	withdrawn, err := store.Withdraw(t.Context(), WithdrawRequest{
		EventID: first.Event.ID, SenderID: root.ID, IdempotencyKey: "message-1", Reason: "superseded before admission",
	})
	if err != nil || len(withdrawn) != 1 || withdrawn[0].State != DeliveryWithdrawn {
		t.Fatalf("pending withdrawal = %+v, %v", withdrawn, err)
	}

	second, err := store.Publish(t.Context(), PublishRequest{
		SenderID: root.ID, IdempotencyKey: "message-2", RecipientIDs: []string{child.ID},
		Origin: OriginPeer, Kind: EventWorkRequest, Body: "Verify the exact parser result.",
		RequestID: "request-parser", RequestRevision: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.Publish(t.Context(), PublishRequest{
		SenderID: root.ID, IdempotencyKey: "message-2", RecipientIDs: []string{child.ID},
		Origin: OriginPeer, Kind: EventWorkRequest, Body: "Verify the exact parser result.",
		RequestID: "request-parser", RequestRevision: 1,
	})
	if err != nil || !retry.Duplicate || retry.Event.ID != second.Event.ID {
		t.Fatalf("unchanged retry = %+v, %v", retry, err)
	}
	reserved, err := store.ReservePending(t.Context(), ReserveRequest{
		RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch, Limit: 10,
	})
	if err != nil || len(reserved) != 1 || reserved[0].Event.ID != second.Event.ID ||
		reserved[0].Delivery.State != DeliveryAdmitting {
		t.Fatalf("reservation = %+v, %v", reserved, err)
	}
	if _, err := store.Withdraw(t.Context(), WithdrawRequest{
		EventID: second.Event.ID, SenderID: root.ID, IdempotencyKey: "message-2",
	}); !errors.Is(err, ErrAlreadyAdmitting) {
		t.Fatalf("withdraw during admission = %v", err)
	}
	admitted, err := store.Admit(t.Context(), AdmitRequest{
		DeliveryID: reserved[0].Delivery.ID, RecipientID: child.ID,
		RuntimeEpoch: child.RuntimeEpoch, ExpectedVersion: reserved[0].Delivery.Version,
		ContextCommitID: "context-child-1",
	})
	if err != nil || admitted.State != DeliveryAdmitted {
		t.Fatalf("admission = %+v, %v", admitted, err)
	}
	if _, err := store.Admit(t.Context(), AdmitRequest{
		DeliveryID: admitted.ID, RecipientID: child.ID, RuntimeEpoch: child.RuntimeEpoch,
		ExpectedVersion: admitted.Version, ContextCommitID: "context-child-2",
	}); !errors.Is(err, ErrStale) {
		t.Fatalf("duplicate admission = %v", err)
	}
}

func TestReportRevisionsAreImmutableAndDeliveryIsOnePublication(t *testing.T) {
	store, _ := openTestStore(t)
	root, child := registerTestAgents(t, store)
	run, err := store.StartRun(t.Context(), StartRunRequest{
		ID: "run-child-1", AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch,
		RequestID: "request-review", Origin: OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.PublishReport(t.Context(), PublishReportRequest{
		ArtifactID: "report-review", AuthorID: child.ID, IdempotencyKey: "report-call-1",
		RecipientIDs: []string{root.ID}, RunID: run.ID, RequestID: run.RequestID,
		Kind: ReportInterim, Summary: "Parser ownership verified",
		Body:       "The parser owns canonical normalization.\n\nFocused checks passed.",
		References: []Reference{{Kind: "test", ID: "parser-owner"}},
		Metadata:   map[string]any{"checks": 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.Artifact.Revision != 1 || len(first.Publish.Deliveries) != 1 ||
		first.Publish.Event.References[0].ID != first.Artifact.ID {
		t.Fatalf("first report = %+v", first)
	}
	retry, err := store.PublishReport(t.Context(), PublishReportRequest{
		ArtifactID: "report-review", AuthorID: child.ID, IdempotencyKey: "report-call-1",
		RecipientIDs: []string{root.ID}, RunID: run.ID, RequestID: run.RequestID,
		Kind: ReportInterim, Summary: "Parser ownership verified",
		Body:       "The parser owns canonical normalization.\n\nFocused checks passed.",
		References: []Reference{{Kind: "test", ID: "parser-owner"}},
		Metadata:   map[string]any{"checks": 1},
	})
	if err != nil || !retry.Publish.Duplicate || retry.Artifact.Digest != first.Artifact.Digest {
		t.Fatalf("report retry = %+v, %v", retry, err)
	}
	second, err := store.PublishReport(t.Context(), PublishReportRequest{
		ArtifactID: first.Artifact.ID, AuthorID: child.ID, IdempotencyKey: "report-call-2",
		RecipientIDs: []string{root.ID}, RunID: run.ID, RequestID: run.RequestID,
		Kind: ReportFinal, Summary: "Parser ownership and edge cases verified",
		Body:               "The parser owns canonical normalization.\n\nAll edge cases passed.",
		SupersedesRevision: first.Artifact.Revision,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Artifact.Revision != 2 || second.Artifact.Digest == first.Artifact.Digest {
		t.Fatalf("report revision = %+v", second.Artifact)
	}
	old, err := store.Artifact(t.Context(), first.Artifact.ID, 1)
	if err != nil || old.Body != first.Artifact.Body {
		t.Fatalf("old report mutated = %+v, %v", old, err)
	}
	if _, err := store.PublishReport(t.Context(), PublishReportRequest{
		ArtifactID: first.Artifact.ID, AuthorID: child.ID, IdempotencyKey: "report-call-3",
		RecipientIDs: []string{root.ID}, RunID: run.ID, Kind: ReportFinal,
		Summary: "stale correction", Body: "must fail", SupersedesRevision: 1,
	}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale report correction = %v", err)
	}
}

func TestClaimsConflictAcrossAgentsAndReleaseByVersion(t *testing.T) {
	store, _ := openTestStore(t)
	root, child := registerTestAgents(t, store)
	rootRun, err := store.StartRun(t.Context(), StartRunRequest{
		ID: "run-claim-root", AgentID: root.ID, RuntimeEpoch: root.RuntimeEpoch, Origin: OriginHuman,
	})
	if err != nil {
		t.Fatal(err)
	}
	childRun, err := store.StartRun(t.Context(), StartRunRequest{
		ID: "run-claim-child", AgentID: child.ID, RuntimeEpoch: child.RuntimeEpoch, Origin: OriginPeer,
	})
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	claims, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{
		AgentID: root.ID, RunID: rootRun.ID, RequestID: "request-root",
		Paths: []string{filepath.Join(base, "pkg")},
	})
	if err != nil || len(claims) != 1 {
		t.Fatalf("root claim = %+v, %v", claims, err)
	}
	if _, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{
		AgentID: child.ID, RunID: childRun.ID, RequestID: "request-child",
		Paths: []string{filepath.Join(base, "pkg", "file.go")},
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("overlap claim = %v", err)
	}
	if _, err := store.ReleaseClaims(t.Context(), ReleaseClaimsRequest{
		AgentID: root.ID, RunID: rootRun.ID, ClaimIDs: []string{claims[0].ID},
		ExpectedVersions: map[string]uint64{claims[0].ID: claims[0].Version + 1},
	}); !errors.Is(err, ErrStale) {
		t.Fatalf("stale release = %v", err)
	}
	released, err := store.ReleaseClaims(t.Context(), ReleaseClaimsRequest{
		AgentID: root.ID, RunID: rootRun.ID, ClaimIDs: []string{claims[0].ID},
		ExpectedVersions: map[string]uint64{claims[0].ID: claims[0].Version},
	})
	if err != nil || released[0].ReleasedAt.IsZero() {
		t.Fatalf("claim release = %+v, %v", released, err)
	}
	childClaims, err := store.AcquireClaims(t.Context(), AcquireClaimsRequest{
		AgentID: child.ID, RunID: childRun.ID, RequestID: "request-child",
		Paths: []string{filepath.Join(base, "pkg", "file.go")},
	})
	if err != nil || len(childClaims) != 1 {
		t.Fatalf("claim after release = %+v, %v", childClaims, err)
	}
}

func TestSnapshotPaginationExcludesConcurrentEvents(t *testing.T) {
	store, _ := openTestStore(t)
	root, child := registerTestAgents(t, store)
	for index := range 3 {
		if _, err := store.Publish(t.Context(), PublishRequest{
			SenderID: root.ID, IdempotencyKey: fmt.Sprintf("page-%d", index),
			RecipientIDs: []string{child.ID}, Origin: OriginPeer,
			Kind: EventPeerNote, Body: fmt.Sprintf("Message %d", index),
		}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := store.Page(t.Context(), PageQuery{Limit: 2})
	if err != nil || len(first.Events) != 2 || !first.HasMore || first.Snapshot != 3 {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	if _, err := store.Publish(t.Context(), PublishRequest{
		SenderID: root.ID, IdempotencyKey: "page-concurrent",
		RecipientIDs: []string{child.ID}, Origin: OriginPeer,
		Kind: EventPeerNote, Body: "Concurrent message",
	}); err != nil {
		t.Fatal(err)
	}
	second, err := store.Page(t.Context(), PageQuery{
		Since: first.Cursor, Snapshot: first.Snapshot, Limit: 2,
	})
	if err != nil || len(second.Events) != 1 || second.Events[0].Sequence != 3 || second.HasMore {
		t.Fatalf("snapshot continuation = %+v, %v", second, err)
	}
	latest, err := store.Page(t.Context(), PageQuery{Since: 3, Limit: 2})
	if err != nil || len(latest.Events) != 1 || latest.Events[0].Sequence != 4 {
		t.Fatalf("new snapshot = %+v, %v", latest, err)
	}
	if unread, err := store.HumanUnread(t.Context()); err != nil || unread != 4 {
		t.Fatalf("human unread = %d, %v", unread, err)
	}
	if err := store.MarkHumanRead(t.Context(), latest.Snapshot); err != nil {
		t.Fatal(err)
	}
	if unread, err := store.HumanUnread(t.Context()); err != nil || unread != 0 {
		t.Fatalf("marked human unread = %d, %v", unread, err)
	}
}

func TestConcurrentPublicationDeduplicatesAcrossConnections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.db")
	open := func() (*Store, *sql.DB) {
		database, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)")
		if err != nil {
			t.Fatal(err)
		}
		database.SetMaxOpenConns(4)
		store, err := Attach(database, testWorkspace, filepath.Join(filepath.Dir(path), "locks"))
		if err != nil {
			_ = database.Close()
			t.Fatal(err)
		}
		return store, database
	}
	first, firstDB := open()
	defer firstDB.Close()
	defer first.Close()
	second, secondDB := open()
	defer secondDB.Close()
	defer second.Close()
	root, child := registerTestAgents(t, first)
	const workers = 20
	start := make(chan struct{})
	results := make(chan PublishResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			<-start
			store := first
			if index%2 == 1 {
				store = second
			}
			result, err := store.Publish(context.Background(), PublishRequest{
				SenderID: root.ID, IdempotencyKey: "concurrent-same",
				RecipientIDs: []string{child.ID}, Origin: OriginPeer,
				Kind: EventPeerNote, Body: "One durable publication",
			})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}(index)
	}
	close(start)
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("publication: %v", err)
	}
	originals := 0
	var eventID string
	for result := range results {
		if eventID == "" {
			eventID = result.Event.ID
		}
		if result.Event.ID != eventID || len(result.Deliveries) != 1 {
			t.Errorf("dedupe result = %+v", result)
		}
		if !result.Duplicate {
			originals++
		}
	}
	if originals != 1 {
		t.Fatalf("original publications = %d", originals)
	}
}
