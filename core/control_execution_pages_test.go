package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"crude/agent/worktree"
	"crude/core/coreprotocol"
	"crude/types/control"
	"crude/workspace"
)

// framedExecutionControl sends control requests through the core's real wire
// gateway, so every result passes the published 64 KiB result bound.
func framedExecutionControl(t *testing.T, a *actor, ctx context.Context) func(command, id string, input any) control.Result {
	t.Helper()
	r, err := New(func(string) (*workspace.Workspace, error) { return a.ws, nil }, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	c := controlWireClient(t, r, "pages")
	var attached coreprotocol.AttachResult
	if err := c.Call(ctx, "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: a.ws.RootPath}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(ctx, attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write, control.Execute, control.Delete}}, nil); err != nil {
		t.Fatal(err)
	}
	sequence := 0
	return func(command, id string, input any) control.Result {
		t.Helper()
		sequence++
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		request := control.Request{ID: fmt.Sprintf("pages-%d", sequence), CommandID: command, Target: control.Target{WorkspaceID: attached.WorkspaceID, ResourceID: id}, Input: raw}
		payload, err := coreprotocol.Marshal(coreprotocol.Ptr(request))
		if err != nil {
			t.Fatal(err)
		}
		response, err := c.CallRequest(ctx, coreprotocol.Request{ID: request.ID, Owner: c.Owner(), WorkspaceID: attached.WorkspaceID, Method: coreprotocol.ControlExecute, Payload: payload})
		if err != nil || response.Error != nil {
			t.Fatalf("framed %s: %v %v", command, err, response.Error)
		}
		var result control.Result
		if err := coreprotocol.Decode(response.Payload, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
}

func writeExecutionFiles(t *testing.T, root, dir, name string, count, size int) []string {
	t.Helper()
	paths := make([]string, 0, count)
	for i := range count {
		// Long nested names make every page record large, as real build trees do.
		path := filepath.ToSlash(filepath.Join(dir, "nested", "deeper", fmt.Sprintf("%s-%03d-%s.txt", name, i, strings.Repeat("x", 80))))
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, bytes.Repeat([]byte{'a' + byte(i%26)}, size), 0o644); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, path)
	}
	return paths
}

// readExecutionReview follows next_cursor through one review and returns every
// reviewed record. Each page must fit the control result bound.
func readExecutionReview(t *testing.T, call func(command, id string, input any) control.Result, id string, discard bool) (executionReview, []executionReviewChange, int) {
	t.Helper()
	var first executionReview
	var all []executionReviewChange
	pages := 0
	cursor := ""
	for {
		input := map[string]any{"id": id, "discard": discard}
		if cursor != "" {
			input["cursor"] = cursor
		}
		result := call("execution.review", id, input)
		if result.Fault != nil || result.State != control.Committed {
			t.Fatalf("review page %d was not delivered: %+v", pages+1, result)
		}
		if len(result.Output) > control.MaxValueBytes || bytes.Contains(result.Output, []byte(`"data"`)) {
			t.Fatalf("review page %d carries %d bytes or file bytes", pages+1, len(result.Output))
		}
		var page executionReview
		if err := json.Unmarshal(result.Output, &page); err != nil {
			t.Fatal(err)
		}
		if pages == 0 {
			first = page
		} else if page.Plan.Digest != first.Plan.Digest || page.Plan.DiscardDigest != first.Plan.DiscardDigest || page.Summary != first.Summary {
			t.Fatalf("page %d belongs to another review", pages+1)
		}
		if len(page.Changes) == 0 {
			t.Fatalf("page %d did not advance", pages+1)
		}
		all = append(all, page.Changes...)
		pages++
		if page.NextCursor == "" {
			return first, all, pages
		}
		cursor = page.NextCursor
	}
}

func reviewedPaths(changes []executionReviewChange, kind string) []string {
	var paths []string
	for _, change := range changes {
		if change.Change == kind {
			paths = append(paths, change.Path)
		}
	}
	sort.Strings(paths)
	return paths
}

// An execution with hundreds of files, ignored build output and a long
// integration history must stay reviewable, applicable and removable through
// control. Before paging, the discard review sent every file's bytes and the
// result bound rejected it, so the unwanted checkout could never be removed.
func TestControlExecutionPagesStayInsideTheResultBound(t *testing.T) {
	a := controlExecutionFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	call := framedExecutionControl(t, a, ctx)
	create := func() worktree.Context {
		t.Helper()
		created := call("execution.create", "", map[string]any{"mode": "worktree"})
		var c worktree.Context
		if created.Fault != nil || json.Unmarshal(created.Output, &c) != nil || c.ID == "" {
			t.Fatalf("create: %+v", created)
		}
		return c
	}

	unwanted := create()
	added := writeExecutionFiles(t, unwanted.Root, "generated", "source", 600, 1024)
	if err := os.WriteFile(filepath.Join(unwanted.Root, ".gitignore"), []byte("build/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	added = append(added, ".gitignore")
	sort.Strings(added)
	ignored := writeExecutionFiles(t, unwanted.Root, "build", "object", 300, 512)
	sort.Strings(ignored)
	// The old contract returned this whole plan, which the result bound refused.
	full, err := a.ws.Executions.ReviewDiscard(ctx, unwanted.ID)
	if err != nil {
		t.Fatal(err)
	}
	if encoded, _ := json.Marshal(full); len(encoded) <= control.MaxValueBytes {
		t.Fatalf("the fixture is not oversized: %d bytes", len(encoded))
	}

	review, changes, pages := readExecutionReview(t, call, unwanted.ID, true)
	if pages < 2 {
		t.Fatalf("the fixture fits one page (%d records); it does not test paging", len(changes))
	}
	if review.Plan.DiscardDigest == "" || review.Plan.ContextID != unwanted.ID || len(review.Plan.Changes) != 0 {
		t.Fatalf("discard review has no compact plan: %+v", review.Plan)
	}
	if review.Summary.Added != len(added) || review.Summary.Changes != len(added) || review.Summary.Ignored != len(ignored) || review.Summary.Bytes != int64(600*1024+len("build/\n")) {
		t.Fatalf("summary %+v", review.Summary)
	}
	if got := reviewedPaths(changes, "added"); strings.Join(got, "\n") != strings.Join(added, "\n") {
		t.Fatalf("reviewed %d added paths, want %d", len(got), len(added))
	}
	if got := reviewedPaths(changes, "ignored"); strings.Join(got, "\n") != strings.Join(ignored, "\n") {
		t.Fatalf("reviewed %d ignored paths, want %d", len(got), len(ignored))
	}
	if len(changes) != len(added)+len(ignored) {
		t.Fatalf("pages repeated or dropped records: %d", len(changes))
	}
	one := call("execution.review", unwanted.ID, map[string]any{"id": unwanted.ID, "discard": true, "limit": 1})
	var single executionReview
	if one.Fault != nil || json.Unmarshal(one.Output, &single) != nil || len(single.Changes) != 1 || single.NextCursor == "" {
		t.Fatalf("limit 1: %+v", one)
	}
	for _, input := range []map[string]any{
		{"id": unwanted.ID, "limit": control.MaxPageLimit + 1},
		{"id": unwanted.ID, "cursor": "not-a-cursor"},
		{"id": unwanted.ID, "digest": review.Plan.DiscardDigest},
	} {
		if refused := call("execution.review", unwanted.ID, input); refused.Fault == nil || refused.Fault.Code != control.Invalid {
			t.Fatalf("review admitted %v: %+v", input, refused)
		}
	}
	removed := call("execution.cleanup", unwanted.ID, map[string]any{"id": unwanted.ID, "discard": true, "digest": review.Plan.DiscardDigest})
	if removed.Fault != nil {
		t.Fatalf("cleanup: %+v", removed.Fault)
	}
	if _, err := os.Stat(unwanted.Root); !os.IsNotExist(err) {
		t.Fatal("the unwanted checkout remains", err)
	}

	wanted := create()
	changed := writeExecutionFiles(t, wanted.Root, "feature", "module", 400, 256)
	sort.Strings(changed)
	review, changes, pages = readExecutionReview(t, call, wanted.ID, false)
	if pages < 2 || review.Plan.Digest == "" || review.Plan.SourceDigest == "" || review.Plan.DiscardDigest != "" {
		t.Fatalf("integration review: pages=%d plan=%+v", pages, review.Plan)
	}
	if got := reviewedPaths(changes, "added"); strings.Join(got, "\n") != strings.Join(changed, "\n") {
		t.Fatalf("reviewed %d paths, want %d", len(got), len(changed))
	}
	applied := call("execution.apply", wanted.ID, map[string]any{"plan": review.Plan})
	var receipt worktree.Receipt
	if applied.Fault != nil || json.Unmarshal(applied.Output, &receipt) != nil || receipt.OperationID == "" {
		t.Fatalf("apply: %+v", applied)
	}
	for _, path := range changed {
		if _, err := os.Stat(filepath.Join(a.ws.RootPath, filepath.FromSlash(path))); err != nil {
			t.Fatal("applied file missing", path, err)
		}
	}
	journal := call("execution.integrations", "", map[string]any{})
	var integrations executionIntegrations
	if journal.Fault != nil || len(journal.Output) > control.MaxValueBytes || bytes.Contains(journal.Output, []byte(`"data"`)) || json.Unmarshal(journal.Output, &integrations) != nil {
		t.Fatalf("integration journal: %d bytes %+v", len(journal.Output), journal.Fault)
	}
	if len(integrations.Integrations) != 1 || integrations.Integrations[0].Receipt != receipt || integrations.Integrations[0].Changes != len(changed) || integrations.Integrations[0].State != "committed" {
		t.Fatalf("journal entries %+v", integrations.Integrations)
	}

	want := []string{wanted.ID}
	for range 3 {
		want = append(want, create().ID)
	}
	sort.Strings(want)
	var listed []string
	cursor := ""
	for {
		input := map[string]any{"limit": 1}
		if cursor != "" {
			input["cursor"] = cursor
		}
		result := call("execution.list", "", input)
		var inventory executionInventory
		if result.Fault != nil || json.Unmarshal(result.Output, &inventory) != nil || len(inventory.Executions) != 1 {
			t.Fatalf("list page: %+v", result)
		}
		listed = append(listed, inventory.Executions[0].ID)
		if inventory.NextCursor == "" {
			break
		}
		cursor = inventory.NextCursor
	}
	if strings.Join(listed, ",") != strings.Join(want, ",") {
		t.Fatalf("listed %v, want %v", listed, want)
	}
}

// A later page belongs to the review its cursor names. When that review is no
// longer known and the execution changed since, the page is refused, never
// served from the newer state.
func TestControlExecutionReviewCursorRefusesAChangedExecution(t *testing.T) {
	a := controlExecutionFixture(t)
	owner := coreprotocol.Owner{ClientID: "trusted", Epoch: 1}
	call := func(id string, input any) control.Result {
		t.Helper()
		raw, _ := json.Marshal(input)
		run, _, ok := a.prepareControlDomain(control.Request{ID: "review", CommandID: "execution.review", Target: control.Target{ResourceID: id}, Input: raw}, owner)
		if !ok {
			t.Fatal("missing route")
		}
		return run(t.Context())
	}
	c, err := a.ws.Executions.Create(t.Context(), worktree.Request{Owner: worktree.Owner{AgentID: "client:trusted", RunID: "epoch:1"}, Mode: worktree.Isolated})
	if err != nil {
		t.Fatal(err)
	}
	writeExecutionFiles(t, c.Root, "src", "file", 4, 16)
	var first executionReview
	if result := call(c.ID, map[string]any{"id": c.ID, "limit": 1}); result.Fault != nil || json.Unmarshal(result.Output, &first) != nil || first.NextCursor == "" {
		t.Fatalf("first page: %+v", result)
	}
	writeExecutionFiles(t, c.Root, "src", "later", 1, 16)
	var second executionReview
	if result := call(c.ID, map[string]any{"id": c.ID, "cursor": first.NextCursor, "limit": 1}); result.Fault != nil || json.Unmarshal(result.Output, &second) != nil || second.Plan.Digest != first.Plan.Digest || second.Summary.Added != 4 {
		t.Fatalf("a retained review did not serve its own next page: %+v", result)
	}
	a.executionReviews.forget(c.ID)
	if result := call(c.ID, map[string]any{"id": c.ID, "cursor": first.NextCursor}); result.Fault == nil || result.Fault.Code != control.Stale {
		t.Fatalf("changed execution served an old cursor: %+v", result)
	}
}
