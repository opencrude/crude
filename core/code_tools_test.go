package core

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/toolstore"
	"crude/types"
)

func codeToolsStoreFixture(t *testing.T) (*toolstore.Store, string, string) {
	t.Helper()
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	id := "fixture/v1-" + digest[:12]
	dir := filepath.Join(root, filepath.FromSlash(id))
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "server"), []byte("fixture executable"), 0600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"id": id, "package": "fixture", "version": "v1", "sha256": digest, "digest_origin": "expected",
		"executable": "server", "command": "fixture-command", "size": 18, "files": 1, "archive": "raw",
		"source": "fixture registry", "url": "https://example.test/fixture", "licenses": []string{"MIT"},
		"installed_at": time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), "installed_by": "user", "approval": "review-one"}
	raw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	// Model a published, immutable artifact rather than a tree whose final
	// creation metadata can still be updated by the Windows filesystem.
	var paths []string
	if err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		if !entry.IsDir() {
			return os.Chmod(path, 0444)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	stamp := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	for i := len(paths) - 1; i >= 0; i-- {
		if err := os.Chtimes(paths[i], stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && !entry.IsDir() {
				_ = os.Chmod(path, 0600)
			}
			return nil
		})
	})
	store, err := toolstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	tools, _, err := store.List(t.Context())
	if err != nil || len(tools) != 1 {
		t.Fatalf("fixture manifest was not recognized: %+v %v", tools, err)
	}
	return store, id, dir
}

func TestCodeToolsNativePreviewIsExactOwnerBoundAndOneShot(t *testing.T) {
	for _, change := range []string{"owner", "workspace", "metadata", "expired", "detach", "once"} {
		t.Run(change, func(t *testing.T) {
			var state codeToolsState
			key := codeToolsOwner{coreprotocol.Owner{ClientID: "shell", Epoch: 1}, "project"}
			scope := state.attach(key)
			view, err := state.retain(key, scope, toolstore.RemovalPreview{Token: "store-token"}, types.CodeToolRemovalPreview{Paths: []string{"exact-folder"}, Tools: []types.CodeTool{{ID: "tool", Licenses: []string{"MIT"}}}})
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "owner":
				key.owner.Epoch++
			case "workspace":
				key.workspace = "other"
			case "metadata":
				view.Tools[0].Licenses[0] = "changed"
			case "expired":
				p := state.previews[view.Token]
				p.expires = time.Now().Add(-time.Second)
				state.previews[view.Token] = p
			case "detach":
				state.detach(key.owner, key.workspace)
			case "once":
				if _, err := state.consume(key, view); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := state.consume(key, view); err == nil {
				t.Fatal("stale or wrong-owner preview accepted")
			}
		})
	}
}

func TestCodeToolsPreviewRetentionIsBoundedAndDetachFencesQueuedPreview(t *testing.T) {
	var state codeToolsState
	key := codeToolsOwner{coreprotocol.Owner{ClientID: "shell", Epoch: 1}, "project"}
	scope := state.attach(key)
	for range codeToolsPreviewLimit {
		if _, err := state.retain(key, scope, toolstore.RemovalPreview{}, types.CodeToolRemovalPreview{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := state.retain(key, scope, toolstore.RemovalPreview{}, types.CodeToolRemovalPreview{}); err == nil {
		t.Fatal("unbounded previews")
	}
	state.detach(key.owner, key.workspace)
	replacement := state.attach(key)
	if replacement == scope {
		t.Fatal("attachment lifetime not distinct")
	}
	if _, err := state.retain(key, scope, toolstore.RemovalPreview{}, types.CodeToolRemovalPreview{}); err == nil {
		t.Fatal("detached preview worker republished authority")
	}
}

func TestCodeToolsNativeRPCRemovalRevalidatesAndRejectsSemanticCalls(t *testing.T) {
	for _, change := range []string{"none", "tree", "used", "running"} {
		t.Run(change, func(t *testing.T) {
			store, id, dir := codeToolsStoreFixture(t)
			r := &Runtime{tools: store}
			r.toolsOnce.Do(func() {})
			a := &actor{id: "project", runtime: r}
			owner := coreprotocol.Owner{ClientID: "shell", Epoch: 1}
			call := func(method string, input coreprotocol.Value, output coreprotocol.Target) coreprotocol.Response {
				t.Helper()
				payload, err := coreprotocol.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				request := coreprotocol.Request{ID: "request-" + method, Method: method, Owner: owner, WorkspaceID: "project", Payload: payload}
				run, known := a.prepareCodeTools(request)
				if !known {
					t.Fatal("native route absent")
				}
				out := run(t.Context())
				if out.Error == nil && output != nil {
					if err := coreprotocol.Decode(out.Payload, output); err != nil {
						t.Fatal(err)
					}
				}
				return out
			}
			for _, method := range []string{coreprotocol.RemoveCodeTools, coreprotocol.PreviewCodeToolRemoval} {
				run, known := a.prepareCodeTools(coreprotocol.Request{Method: method, JSON: true, Owner: owner})
				if !known || run(t.Context()).Error == nil {
					t.Fatal("semantic removal created human consent")
				}
			}
			var out coreprotocol.PreviewCodeToolRemovalResult
			if got := call(coreprotocol.PreviewCodeToolRemoval, &coreprotocol.PreviewCodeToolRemovalRequest{IDs: []string{id}}, &out); got.Error != nil {
				t.Fatal(got.Error)
			}
			if len(out.Preview.Tools) != 1 || len(out.Preview.Tools[0].FileEntries) != 3 || out.Preview.Bytes <= 18 || out.Preview.Paths[0] != dir {
				t.Fatalf("incomplete removal review: %+v", out.Preview)
			}
			retained := r.codeTools.previews[out.Preview.Token].store
			current, err := store.PreviewRemove(t.Context(), []string{id})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(retained, current) {
				t.Fatalf("review inspection changed store preimage:\nretained=%#v\ncurrent=%#v", retained, current)
			}
			switch change {
			case "tree":
				if err := os.WriteFile(filepath.Join(dir, "new-file"), []byte("later work"), 0600); err != nil {
					t.Fatal(err)
				}
			case "used":
				if err := store.MarkUsed(id, "other-workspace"); err != nil {
					t.Fatal(err)
				}
			case "running":
				release, managed, err := store.AcquireExecutable(t.Context(), out.Preview.Tools[0].Executable)
				if err != nil || !managed {
					t.Fatalf("lease: %v %v", managed, err)
				}
				defer release()
			}
			result := call(coreprotocol.RemoveCodeTools, &coreprotocol.RemoveCodeToolsRequest{Preview: out.Preview}, &coreprotocol.RemoveCodeToolsResult{})
			_, statErr := os.Stat(dir)
			if change == "none" {
				if result.Error != nil || !errors.Is(statErr, os.ErrNotExist) {
					t.Fatalf("reviewed removal failed: %v %v", result.Error, statErr)
				}
			} else if result.Error == nil || statErr != nil {
				t.Fatalf("changed/in-use tool was removed: %v %v", result.Error, statErr)
			}
			if replay := call(coreprotocol.RemoveCodeTools, &coreprotocol.RemoveCodeToolsRequest{Preview: out.Preview}, nil); replay.Error == nil {
				t.Fatal("consumed preview reused")
			}
		})
	}
}

func TestCodeToolsInventoryIncludesExactFilesAndCancellation(t *testing.T) {
	store, _, dir := codeToolsStoreFixture(t)
	out, err := readCodeToolInventory(t.Context(), store, types.AssemblyPlan{}, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Tools) != 1 || len(out.Tools[0].FileEntries) != 3 || out.Tools[0].Dir != dir || out.Tools[0].Approval != "review-one" || out.TotalBytes != 18 {
		t.Fatalf("inventory accountability incomplete: %+v", out)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := inspectCodeTool(ctx, toolstore.Tool{Dir: dir}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
}

func TestCodeToolsDetachCancelsConsumedRemovalBeforeStoreEffect(t *testing.T) {
	store, id, dir := codeToolsStoreFixture(t)
	r := &Runtime{tools: store}
	r.toolsOnce.Do(func() {})
	a := &actor{id: "project", runtime: r}
	key := codeToolsOwner{coreprotocol.Owner{ClientID: "shell", Epoch: 1}, a.id}
	scope := r.codeTools.attach(key)
	preview, err := store.PreviewRemove(t.Context(), []string{id})
	if err != nil {
		t.Fatal(err)
	}
	view, err := r.codeTools.retain(key, scope, preview, types.CodeToolRemovalPreview{Paths: preview.Paths})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := coreprotocol.Marshal(&coreprotocol.RemoveCodeToolsRequest{Preview: view})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := a.prepareCodeTools(coreprotocol.Request{Method: coreprotocol.RemoveCodeTools, Owner: key.owner, Payload: payload})
	locked, release, accounting := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		accounting <- store.MarkUsedContext(t.Context(), id, "not-recorded", func(context.Context) error {
			close(locked)
			<-release
			return errors.New("accounting not authorized")
		})
	}()
	<-locked
	done := make(chan coreprotocol.Response, 1)
	go func() { done <- run(t.Context()) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	// Observe consumption without depending on the implementation's lock wait.
	for {
		r.codeTools.mu.Lock()
		_, retained := r.codeTools.previews[view.Token]
		r.codeTools.mu.Unlock()
		if !retained {
			break
		}
		select {
		case <-deadline.C:
			close(release)
			if cleanupErr := <-accounting; cleanupErr != nil {
				t.Logf("Cleanup join: %v", cleanupErr)
			}
			<-done
			t.Fatal("removal did not consume the native review")
		case <-time.After(time.Millisecond):
		}
	}
	r.codeTools.detach(key.owner, key.workspace)
	close(release)
	if cleanupErr := <-accounting; cleanupErr != nil {
		t.Logf("Cleanup join: %v", cleanupErr)
	}
	select {
	case result := <-done:
		if result.Error == nil {
			t.Fatal("detached consumed removal completed")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("detached removal did not join")
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("detached removal changed reviewed files: %v", err)
	}
}
