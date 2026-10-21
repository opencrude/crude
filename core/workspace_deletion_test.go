package core

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/document"
	"crude/types"
	"crude/workspace"
)

func deletionRequest(t *testing.T, c *Connection, id, ws, method string, value any) coreprotocol.Request {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return coreprotocol.Request{ID: id, Owner: c.Owner, WorkspaceID: ws, Method: method, Payload: raw}
}

func deletionFault(t *testing.T, c *Connection, id, ws, method string, value any, code string) coreprotocol.Response {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	out := c.Call(ctx, deletionRequest(t, c, id, ws, method, value))
	if out.Error == nil || out.Error.Code != code {
		t.Fatalf("%s: want %s, got error=%+v payload=%s", method, code, out.Error, out.Payload)
	}
	return out
}

func deletionPreview(t *testing.T, c *Connection, ws, project string) workspace.WorkspaceDeletion {
	t.Helper()
	var plan workspace.WorkspaceDeletion
	decodeResource(t, callCore(t, c, "preview", ws, coreprotocol.WorkspaceDeletePreview, coreprotocol.WorkspaceDeletePreviewRequest{ID: workspace.WorkspaceID(project), ProjectDir: project}), &plan)
	if plan.Token == "" || plan.Issue != nil || plan.ID != workspace.WorkspaceID(project) || plan.ProjectDir != project {
		t.Fatalf("no actionable immutable deletion preview: %+v", plan)
	}
	return plan
}

func deletionActor(t *testing.T, r *Runtime, id string) *actor {
	t.Helper()
	r.mu.Lock()
	a := r.workspaces[id]
	r.mu.Unlock()
	if a == nil {
		t.Fatalf("workspace actor %s is missing", id)
	}
	return a
}

func inspectDeletionActor(t *testing.T, a *actor, fn func()) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if err := a.inspect(ctx, fn); err != nil {
		t.Fatal(err)
	}
}

func assertDeletionActorUsable(t *testing.T, r *Runtime, id string, want *actor) {
	t.Helper()
	if got := deletionActor(t, r, id); got != want {
		t.Fatal("refusal replaced the retained actor")
	}
	select {
	case <-want.done:
		t.Fatal("refusal stopped the retained actor")
	default:
	}
	inspectDeletionActor(t, want, func() {})
	if err := want.accessDB.DB.PingContext(t.Context()); err != nil {
		t.Fatalf("refusal closed workspace persistence: %v", err)
	}
}

func assertDeletionFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%s: got %q, want %q: %v", path, got, want, err)
	}
}

func TestWorkspaceDeletionRetiresDetachedActorAndUndoRestoresAuthorHistory(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	path := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(path, []byte("alpha\n"), 0600); err != nil {
		t.Fatal(err)
	}
	author, target := resourceConnection(t, r, root, "deletion-author")
	peer, _ := resourceConnection(t, r, root, "deletion-peer")
	oldActor := deletionActor(t, r, target)
	var snap document.Snapshot
	decodeResource(t, callCore(t, author, "open-author", target, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	decodeResource(t, callCore(t, peer, "open-peer", target, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	documentID := snap.ID
	var edit document.Receipt
	decodeResource(t, callCore(t, author, "author-edit", target, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: documentID, Revision: snap.Revision, Edits: []document.Edit{{Start: 0, End: 5, Text: "βeta"}}}), &edit)
	decodeResource(t, callCore(t, peer, "peer-edit", target, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: documentID, Revision: edit.Revision, Edits: []document.Edit{{Start: len("βeta\n"), End: len("βeta\n"), Text: "peer\n"}}}), &edit)
	if !edit.Accepted {
		t.Fatalf("fixture did not durably accept dirty text: %+v", edit)
	}
	saved, err := workspace.WorkspaceDir(root)
	if err != nil {
		t.Fatal(err)
	}
	currentRoot := t.TempDir()
	current, currentID := resourceConnection(t, r, currentRoot, "deletion-current")
	callCore(t, author, "detach-author", target, coreprotocol.Detach, struct{}{})
	callCore(t, peer, "detach-peer", target, coreprotocol.Detach, struct{}{})
	author.Close()
	peer.Close()

	plan := deletionPreview(t, current, currentID, root)
	select {
	case <-oldActor.done:
	default:
		t.Fatal("preview did not retire the detached actor before reviewing its files")
	}
	r.mu.Lock()
	retained := r.workspaces[target]
	r.mu.Unlock()
	if retained != nil {
		t.Fatal("preview left the closed actor available to a later attach")
	}
	// The normal catalog opens SQLite between preview and confirmation. Its
	// transient read handles/sidecars must not make a quiescent plan stale.
	var catalog []workspace.WorkspaceInfo
	decodeResource(t, callCore(t, current, "catalog-between-preview-and-delete", currentID, coreprotocol.SavedWorkspaces, struct{}{}), &catalog)
	found := false
	for _, info := range catalog {
		found = found || info.ID == plan.ID && info.ProjectDir == root
	}
	if !found {
		t.Fatalf("previewed saved workspace vanished from the catalog: %+v", catalog)
	}
	stranger, strangerID := resourceConnection(t, r, currentRoot, "deletion-stranger")
	input := coreprotocol.WorkspaceDeleteRequest{Token: plan.Token}
	deletionFault(t, stranger, "foreign-preview", strangerID, coreprotocol.WorkspaceDelete, input, "preview_expired")
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("foreign preview moved saved data: %v", err)
	}
	response := callCore(t, current, "delete-exactly-once", currentID, coreprotocol.WorkspaceDelete, input)
	var result coreprotocol.WorkspaceDeletionResult
	decodeResource(t, response, &result)
	if result.Deleted.ID != plan.ID || result.Deleted.ProjectDir != root || result.Deleted.RecoveryPath == "" {
		t.Fatalf("deletion lost its recovery identity: %+v", result)
	}
	if _, err := os.Stat(saved); !os.IsNotExist(err) {
		t.Fatalf("deleted workspace still has source storage: %v", err)
	}
	for _, name := range []string{"workspace.json", filepath.Join("data", "workspace.db")} {
		if _, err := os.Stat(filepath.Join(result.Deleted.RecoveryPath, name)); err != nil {
			t.Fatal(err)
		}
	}
	assertDeletionFile(t, path, "alpha\n")
	callCore(t, current, "current-still-live", currentID, coreprotocol.Snapshot, struct{}{})
	var receipt coreprotocol.Receipt
	decodeResource(t, callCore(t, current, "delete-receipt", "", coreprotocol.GetReceipt, coreprotocol.ReceiptRequest{RequestID: "delete-exactly-once"}), &receipt)
	if receipt.ClientID != current.Owner.ClientID || receipt.Method != coreprotocol.WorkspaceDelete || receipt.WorkspaceID != currentID || receipt.State != "completed" || !bytes.Equal(receipt.Response.Payload, response.Payload) {
		t.Fatalf("deletion result is not durably observable: %+v", receipt)
	}
	deletionFault(t, current, "reuse-consumed-preview", currentID, coreprotocol.WorkspaceDelete, input, "preview_expired")
	callCore(t, current, "undo-delete", currentID, coreprotocol.WorkspaceRestore, result.Deleted)
	// A reconnected client replays the old receipt, not a second move of the
	// just-restored directory. The connection epoch is not operation identity.
	oldEpoch := current.Owner.Epoch
	current.Close()
	current, currentID = resourceConnection(t, r, currentRoot, "deletion-current")
	if current.Owner.Epoch <= oldEpoch {
		t.Fatal("reconnect reused the deletion owner's epoch")
	}
	replayed := callCore(t, current, "delete-exactly-once", currentID, coreprotocol.WorkspaceDelete, input)
	if !bytes.Equal(replayed.Payload, response.Payload) || replayed.Revision != response.Revision {
		t.Fatalf("deletion receipt replay changed its outcome: %+v", replayed)
	}
	if _, err := os.Stat(saved); err != nil {
		t.Fatalf("receipt replay moved restored work a second time: %v", err)
	}
	author, restoredID := resourceConnection(t, r, root, "deletion-author")
	if restoredID != target || deletionActor(t, r, target) == oldActor {
		t.Fatal("Undo did not cold-open the original workspace identity")
	}
	decodeResource(t, callCore(t, author, "cold-open", target, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	if snap.ID != documentID || string(snap.Text) != "βeta\npeer\n" || string(snap.Saved) != "alpha\n" || !snap.Dirty || snap.Revision != edit.Revision {
		t.Fatalf("delete/Undo lost accepted dirty document state: %+v", snap)
	}
	callCore(t, author, "cold-author-undo", target, coreprotocol.DocumentUndo, coreprotocol.DocumentRevisionRequest{ID: documentID, Revision: snap.Revision})
	decodeResource(t, callCore(t, author, "read-author-undo", target, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: documentID}), &snap)
	if string(snap.Text) != "alpha\npeer\n" || !snap.Dirty {
		t.Fatalf("cold author Undo removed another author's work: %+v", snap)
	}
	peer, _ = resourceConnection(t, r, root, "deletion-peer")
	decodeResource(t, callCore(t, peer, "cold-peer-open", target, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	callCore(t, peer, "cold-peer-undo", target, coreprotocol.DocumentUndo, coreprotocol.DocumentRevisionRequest{ID: documentID, Revision: snap.Revision})
	decodeResource(t, callCore(t, peer, "read-peer-undo", target, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: documentID}), &snap)
	if string(snap.Text) != "alpha\n" || snap.Dirty {
		t.Fatalf("cold peer Undo lost the original savepoint/history: %+v", snap)
	}
	assertDeletionFile(t, path, "alpha\n")
}

func TestWorkspaceDeletionRefusesCurrentPeerAndBackgroundViewsWithoutClosing(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	a, target := resourceConnection(t, r, root, "in-use-origin")
	peer, _ := resourceConnection(t, r, root, "in-use-peer")
	current, currentID := resourceConnection(t, r, t.TempDir(), "in-use-current")
	targetActor := deletionActor(t, r, target)
	input := coreprotocol.WorkspaceDeletePreviewRequest{ID: workspace.WorkspaceID(root), ProjectDir: root}
	out := deletionFault(t, a, "refuse-current", target, coreprotocol.WorkspaceDeletePreview, input, "workspace_in_use")
	if !strings.Contains(out.Error.Message, "current workspace") {
		t.Fatalf("current-workspace refusal is not actionable: %+v", out.Error)
	}
	checkIssue := func(id, summary string) {
		t.Helper()
		var plan workspace.WorkspaceDeletion
		decodeResource(t, callCore(t, current, id, currentID, coreprotocol.WorkspaceDeletePreview, input), &plan)
		if plan.Token != "" || plan.Issue == nil || !strings.Contains(plan.Issue.Summary, summary) || plan.Issue.Guidance == "" {
			t.Fatalf("unsafe workspace produced a deletable/nonactionable preview: %+v", plan)
		}
		assertDeletionActorUsable(t, r, target, targetActor)
	}
	checkIssue("refuse-two-views", "2 view(s)")
	callCore(t, a, "switch-away", target, coreprotocol.Detach, struct{}{})
	checkIssue("refuse-peer-view", "1 view(s)")
	callCore(t, peer, "peer-still-live", target, coreprotocol.Snapshot, struct{}{})
	callCore(t, peer, "peer-switch-away", target, coreprotocol.Detach, struct{}{})
	inspectDeletionActor(t, targetActor, func() {
		state := workspace.NewAgentState("background-agent", workspace.NewConversation(), coreTestLogger{})
		state.Running = true // A provider/tool gap: neither Thinking nor Streaming.
		targetActor.ws.AgentMgr.AddOrchestrator(state.ID, state)
	})
	checkIssue("refuse-running-background-turn", "background work")
	var running bool
	inspectDeletionActor(t, targetActor, func() { running = targetActor.ws.AgentMgr.Get("background-agent").State.Running })
	if !running {
		t.Fatal("reviewing deletion stopped a background turn")
	}
	observer, _ := resourceConnection(t, r, root, "background-observer")
	var snapshot coreprotocol.WorkspaceSnapshot
	decodeResource(t, callCore(t, observer, "background-still-visible", target, coreprotocol.Snapshot, struct{}{}), &snapshot)
	if len(snapshot.Agents) != 1 || snapshot.Agents[0].ID != "background-agent" || !snapshot.Agents[0].Running {
		t.Fatalf("refusal changed the still-running agent view: %+v", snapshot.Agents)
	}
	inspectDeletionActor(t, targetActor, func() { targetActor.ws.AgentMgr.Get("background-agent").State.Running = false })
}

func TestWorkspaceDeletionRefusesInFlightResourceWithoutClosing(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	a, target := resourceConnection(t, r, root, "operation-owner")
	current, currentID := resourceConnection(t, r, t.TempDir(), "operation-current")
	targetActor := deletionActor(t, r, target)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	if !targetActor.resourceLane.submit(func(context.Context) { close(entered); <-release }) {
		t.Fatal("could not hold the real resource lane")
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("resource lane did not start")
	}
	result := a.Dispatch(t.Context(), deletionRequest(t, a, "pending-open", target, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "pending.txt"}))
	callCore(t, a, "operation-admission-barrier", target, coreprotocol.Snapshot, struct{}{})
	callCore(t, a, "operation-switch-away", target, coreprotocol.Detach, struct{}{})
	var plan workspace.WorkspaceDeletion
	decodeResource(t, callCore(t, current, "pending-operation-preview", currentID, coreprotocol.WorkspaceDeletePreview, coreprotocol.WorkspaceDeletePreviewRequest{ID: workspace.WorkspaceID(root), ProjectDir: root}), &plan)
	if plan.Token != "" || plan.Issue == nil || !strings.Contains(plan.Issue.Summary, "operations are still finishing") || plan.Issue.Guidance == "" {
		t.Fatalf("in-flight resource was not safely refused: %+v", plan)
	}
	assertDeletionActorUsable(t, r, target, targetActor)
	unblock()
	select {
	case out := <-result:
		if out.Error == nil || !strings.Contains(out.Error.Message, document.ErrOwner.Error()) {
			t.Fatalf("detached resource unexpectedly acquired document ownership: %+v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("refusal stranded the admitted resource operation")
	}
	assertDeletionActorUsable(t, r, target, targetActor)
}

func TestWorkspaceDeletionLateAttachWriteDetachInvalidatesPreview(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	path := filepath.Join(root, "later.txt")
	if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	a, target := resourceConnection(t, r, root, "early-view")
	current, currentID := resourceConnection(t, r, t.TempDir(), "late-write-current")
	callCore(t, a, "early-detach", target, coreprotocol.Detach, struct{}{})
	plan := deletionPreview(t, current, currentID, root)
	late, _ := resourceConnection(t, r, root, "late-writer")
	var snap document.Snapshot
	decodeResource(t, callCore(t, late, "late-open", target, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	callCore(t, late, "late-edit", target, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: snap.ID, Revision: snap.Revision, Edits: []document.Edit{{Start: 4, End: 4, Text: " accepted later"}}})
	callCore(t, late, "late-detach", target, coreprotocol.Detach, struct{}{})
	late.Close()
	out := deletionFault(t, current, "stale-delete", currentID, coreprotocol.WorkspaceDelete, coreprotocol.WorkspaceDeleteRequest{Token: plan.Token}, "workspace_storage_failed")
	if !strings.Contains(out.Error.Message, "changed since preview") {
		t.Fatalf("later accepted work did not invalidate the exact preimage: %+v", out.Error)
	}
	late, _ = resourceConnection(t, r, root, "late-writer")
	decodeResource(t, callCore(t, late, "recover-later-draft", target, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	if string(snap.Text) != "disk accepted later" || string(snap.Saved) != "disk" || !snap.Dirty {
		t.Fatalf("stale confirmation lost later accepted work: %+v", snap)
	}
	assertDeletionFile(t, path, "disk")
}

func TestWorkspaceDeletionCanceledFactoryStillOpeningIsActionablyRefused(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	root := t.TempDir()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	r, err := New(func(project string) (*workspace.Workspace, error) {
		if project == root {
			close(entered)
			<-release // A factory is allowed to finish after its caller cancels.
		}
		return workspace.NewWorkspace(project, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	t.Cleanup(unblock)
	opener, err := r.Connect("canceled-opener")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(opener.Close)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := deletionRequest(t, opener, "canceled-attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
	result := make(chan coreprotocol.Response, 1)
	go func() { result <- opener.Call(ctx, request) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("factory did not start")
	}
	cancel()
	select {
	case out := <-result:
		// Cancellation can win the caller's await before attach publishes its
		// cancellation result. Neither outcome means the factory has stopped.
		if out.Error == nil || (out.Error.Code != "workspace_unavailable" && out.Error.Code != "outcome_pending") {
			t.Fatalf("canceled attach did not settle: %+v", out)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled attach waited for its context-ignoring factory")
	}
	r.mu.Lock()
	var opening *workspaceCreation
	for _, creation := range r.creating {
		if creation.savedID == workspace.WorkspaceID(root) {
			opening = creation
		}
	}
	r.mu.Unlock()
	if opening == nil {
		t.Fatal("canceled factory lost its saved-storage identity while still running")
	}
	current, currentID := resourceConnection(t, r, t.TempDir(), "opening-current")
	var plan workspace.WorkspaceDeletion
	decodeResource(t, callCore(t, current, "opening-preview", currentID, coreprotocol.WorkspaceDeletePreview, coreprotocol.WorkspaceDeletePreviewRequest{ID: workspace.WorkspaceID(root), ProjectDir: root}), &plan)
	if plan.Token != "" || plan.Issue == nil || !strings.Contains(plan.Issue.Summary, "still opening") || !strings.Contains(plan.Issue.Guidance, "Wait") {
		t.Fatalf("still-opening storage was not actionably refused: %+v", plan)
	}
	callCore(t, current, "opening-current-live", currentID, coreprotocol.Snapshot, struct{}{})
	unblock()
	select {
	case <-opening.done:
	case <-time.After(3 * time.Second):
		t.Fatal("refused factory could not finish normally")
	}
	later, target := resourceConnection(t, r, root, "opening-later-view")
	callCore(t, later, "opening-later-live", target, coreprotocol.Snapshot, struct{}{})
}

func TestWorkspaceDeletionUndoRefusesReplacementWithoutClosingIt(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	path := filepath.Join(root, "replace.txt")
	if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	a, target := resourceConnection(t, r, root, "before-replacement")
	current, currentID := resourceConnection(t, r, t.TempDir(), "replacement-current")
	callCore(t, a, "before-replacement-detach", target, coreprotocol.Detach, struct{}{})
	plan := deletionPreview(t, current, currentID, root)
	var deleted coreprotocol.WorkspaceDeletionResult
	decodeResource(t, callCore(t, current, "before-replacement-delete", currentID, coreprotocol.WorkspaceDelete, coreprotocol.WorkspaceDeleteRequest{Token: plan.Token}), &deleted)
	recoveryDB := filepath.Join(deleted.Deleted.RecoveryPath, "data", "workspace.db")
	before, err := os.ReadFile(recoveryDB)
	if err != nil {
		t.Fatal(err)
	}
	replacement, replacementID := resourceConnection(t, r, root, "replacement-owner")
	replacementActor := deletionActor(t, r, replacementID)
	var snap document.Snapshot
	decodeResource(t, callCore(t, replacement, "replacement-open", replacementID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	callCore(t, replacement, "replacement-edit", replacementID, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: snap.ID, Revision: snap.Revision, Edits: []document.Edit{{Start: 4, End: 4, Text: " replacement"}}})
	callCore(t, replacement, "replacement-detach", replacementID, coreprotocol.Detach, struct{}{})
	deletionFault(t, current, "refuse-replacement-undo", currentID, coreprotocol.WorkspaceRestore, deleted.Deleted, "workspace_replaced")
	assertDeletionActorUsable(t, r, replacementID, replacementActor)
	after, err := os.ReadFile(recoveryDB)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatalf("refused Undo altered original recovery data: %v", err)
	}
	replacement.Close()
	replacement, _ = resourceConnection(t, r, root, "replacement-owner")
	decodeResource(t, callCore(t, replacement, "replacement-reopen", replacementID, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	if string(snap.Text) != "disk replacement" || !snap.Dirty {
		t.Fatalf("Undo refusal lost the replacement's dirty text: %+v", snap)
	}
	callCore(t, replacement, "replacement-still-writable", replacementID, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: snap.ID, Revision: snap.Revision, Edits: []document.Edit{{Start: len(snap.Text), End: len(snap.Text), Text: " usable"}}})
	decodeResource(t, callCore(t, replacement, "replacement-read", replacementID, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: snap.ID}), &snap)
	if string(snap.Text) != "disk replacement usable" {
		t.Fatalf("refusal left a dead replacement document service: %+v", snap)
	}
	assertDeletionFile(t, path, "disk")
}

func TestWorkspaceDeletionRefusesLiveTerminalWithoutStoppingIt(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	t.Setenv("CRUDE_TEST_SILENT_TERMINAL_CHILD", "1")
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	owner, target := resourceConnection(t, r, root, "deletion-terminal-owner")
	current, currentID := resourceConnection(t, r, t.TempDir(), "deletion-terminal-current")
	var terminal workspace.TerminalInfo
	decodeResource(t, callCore(t, owner, "create-terminal", target, coreprotocol.TerminalCreate, workspace.TerminalCreateOpts{
		Command: executable, Args: []string{"-test.run=^TestResourceEventsSilentTerminalChild$"}, CWD: root, Cols: 40, Rows: 4,
	}), &terminal)
	targetActor := deletionActor(t, r, target)
	callCore(t, owner, "detach-terminal-owner", target, coreprotocol.Detach, struct{}{})
	var plan workspace.WorkspaceDeletion
	decodeResource(t, callCore(t, current, "terminal-use-preview", currentID, coreprotocol.WorkspaceDeletePreview, coreprotocol.WorkspaceDeletePreviewRequest{ID: workspace.WorkspaceID(root), ProjectDir: root}), &plan)
	if plan.Token != "" || plan.Issue == nil || !strings.Contains(plan.Issue.Summary, "terminal is still running") || !strings.Contains(plan.Issue.Guidance, "No terminal was stopped") {
		t.Fatalf("live terminal was not actionably refused: %+v", plan)
	}
	assertDeletionActorUsable(t, r, target, targetActor)
	owner, _ = resourceConnection(t, r, root, "deletion-terminal-reopened")
	var inventory []workspace.TerminalInfo
	decodeResource(t, callCore(t, owner, "terminal-survives-refusal", target, coreprotocol.TerminalList, struct{}{}), &inventory)
	if len(inventory) != 1 || inventory[0].ID != terminal.ID || inventory[0].PID != terminal.PID || !inventory[0].Alive {
		t.Fatalf("deletion use-check stopped the terminal: %+v", inventory)
	}
	callCore(t, owner, "close-terminal-deliberately", target, coreprotocol.TerminalClose, coreprotocol.TerminalIDRequest{ID: terminal.ID})
	callCore(t, owner, "detach-after-terminal-close", target, coreprotocol.Detach, struct{}{})
	plan = deletionPreview(t, current, currentID, root)
	if plan.Token == "" {
		t.Fatal("closing terminal did not unblock an ordinary reviewed deletion")
	}
}
