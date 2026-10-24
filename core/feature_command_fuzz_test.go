package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"crude/clientstate"
	"crude/coreprotocol"
	"crude/document"
	"crude/localhistory"
	"crude/provider"
	"crude/types"
	"crude/workspace"
)

// These are state/command fuzzers, not codec round trips. Every input owns a
// fresh runtime/profile; command arguments and filesystem paths stay bounded.
// Neither session creation nor an input command can launch a real model here.
const featureFuzzActions = 12

func featureRuntime(t *testing.T, configure func(*workspace.Workspace)) (*Runtime, string) {
	t.Helper()
	t.Setenv("CRUDE_HOME", t.TempDir())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "fuzzer", types.Settings{}, coreTestLogger{})
		if configure != nil {
			configure(ws)
		}
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := r.Close(); err != nil {
			t.Error(err)
		}
	})
	return r, t.TempDir()
}

func featureCall(t *testing.T, w *resourceWire, ws, id, method string, input any, out any) coreprotocol.Response {
	t.Helper()
	raw, err := testPayload(input)
	if err != nil {
		t.Fatal(err)
	}
	response, err := w.c.CallRequest(t.Context(), coreprotocol.Request{ID: id, WorkspaceID: ws, Method: method, Payload: raw})
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		if response.Error != nil {
			t.Fatalf("%s rejected: %s", method, response.Error.Code)
		}
		if err := testDecode(response.Payload, out); err != nil {
			t.Fatal(err)
		}
	}
	return response
}

func FuzzCoreFeatureDocuments(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 6, 7, 0, 2, 4})
	f.Add([]byte{1, 0, 3, 2, 5, 4, 6, 7})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		r, root := featureRuntime(t, nil)
		path := filepath.Join(root, "shared.txt")
		if err := os.WriteFile(path, []byte("A---B\n"), 0600); err != nil {
			t.Fatal(err)
		}
		peers := [2]*resourceWire{newResourceWire(t, r, "author-a"), newResourceWire(t, r, "author-b")}
		var attached coreprotocol.AttachResult
		peers[0].call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
		var other coreprotocol.AttachResult
		peers[1].call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &other)
		if attached.WorkspaceID != other.WorkspaceID {
			t.Fatal("same root forked workspace")
		}
		ws := attached.WorkspaceID
		var initial document.Snapshot
		peers[0].call(t, ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: "shared.txt"}, &initial)
		var shared document.Snapshot
		peers[1].call(t, ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}, &shared)
		if initial.ID == "" || initial.ID != shared.ID || initial.Revision != shared.Revision {
			t.Fatal("relative/absolute opens forked document")
		}
		want, saved := []byte("A---B\n"), []byte("A---B\n")
		revision := initial.Revision
		var undo, redo [2][]byte
		for i, action := range sequence[:min(len(sequence), featureFuzzActions)] {
			author := int(action & 1)
			w, pos := peers[author], author*4
			id := fmt.Sprintf("action-%d", i)
			switch action % 8 {
			case 0, 1:
				next := byte('a' + (int(action)+i)%26)
				var receipt document.Receipt
				featureCall(t, w, ws, id, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: initial.ID, Revision: revision, Group: id, Edits: []document.Edit{{Start: pos, End: pos + 1, Text: string(next)}}}, &receipt)
				if !receipt.Accepted || receipt.Before != revision || receipt.Revision != revision+1 || receipt.DocumentID != initial.ID || receipt.RequestID != id {
					t.Fatal("edit lost receipt/revision identity")
				}
				undo[author] = append(undo[author], want[pos])
				redo[author] = nil
				want[pos], revision = next, revision+1
			case 2, 3, 4, 5:
				from, to, method := &undo[author], &redo[author], coreprotocol.DocumentUndo
				if action%8 >= 4 {
					from, to, method = &redo[author], &undo[author], coreprotocol.DocumentRedo
				}
				var receipt document.Receipt
				out := featureCall(t, w, ws, id, method, coreprotocol.DocumentRevisionRequest{ID: initial.ID, Revision: revision}, nil)
				if len(*from) == 0 {
					if out.Error == nil {
						t.Fatal("empty author history accepted")
					}
					break
				}
				if out.Error != nil || testDecode(out.Payload, &receipt) != nil || !receipt.Accepted || receipt.Revision != revision+1 {
					t.Fatal("disjoint author history failed")
				}
				*to = append(*to, want[pos])
				want[pos] = (*from)[len(*from)-1]
				*from = (*from)[:len(*from)-1]
				revision++
			case 6:
				var point document.Snapshot
				featureCall(t, w, ws, id, coreprotocol.DocumentSave, coreprotocol.DocumentRevisionRequest{ID: initial.ID, Revision: revision}, &point)
				saved = bytes.Clone(want)
				if point.Dirty || point.Revision != revision || !bytes.Equal(point.Saved, saved) {
					t.Fatal("save acknowledged wrong savepoint")
				}
			case 7:
				out := featureCall(t, w, ws, id, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: initial.ID, Revision: revision - 1, Edits: []document.Edit{{Start: 0, End: 1, Text: "!"}}}, nil)
				if out.Error == nil {
					t.Fatal("stale edit accepted")
				}
			}
			for peer, observer := range peers {
				var got document.Snapshot
				featureCall(t, observer, ws, fmt.Sprintf("read-%d-%d", i, peer), coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: initial.ID}, &got)
				if got.ID != initial.ID || got.Revision != revision || !bytes.Equal(got.Text, want) || !bytes.Equal(got.Saved, saved) || got.Dirty != !bytes.Equal(want, saved) {
					t.Fatal("canonical bytes, savepoint, or peer identity diverged")
				}
			}
			if disk, err := os.ReadFile(path); err != nil || !bytes.Equal(disk, saved) {
				t.Fatal("disk diverged from acknowledged savepoint")
			}
		}
	})
}

func FuzzCoreFeatureFilesHistory(f *testing.F) {
	f.Add([]byte{0, 4, 1, 5, 2, 6, 3, 7, 8, 9, 10, 11})
	f.Add([]byte{0, 0, 4, 8, 2, 7})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		r, root := featureRuntime(t, nil)
		w := newResourceWire(t, r, "file-owner")
		var attached coreprotocol.AttachResult
		w.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
		ws := attached.WorkspaceID
		names := [3]string{"a.txt", "b.txt", "c.txt"}
		var exists [3]bool
		var contents [3]string
		for i, action := range sequence[:min(len(sequence), featureFuzzActions)] {
			slot := int(action/4) % len(names)
			input := coreprotocol.FilePathRequest{Path: names[slot]}
			method := coreprotocol.FilesCreate
			var value any = input
			switch action % 4 {
			case 0:
				if exists[slot] {
					continue
				}
				exists[slot], contents[slot] = true, ""
			case 1:
				if !exists[slot] {
					continue
				}
				method = coreprotocol.FilesEdit
				next := fmt.Sprintf("authored-%d\n", action)
				value = coreprotocol.FileEditsRequest{Edits: []workspace.FileEdit{{Path: names[slot], OldContent: contents[slot], NewContent: next}}}
				contents[slot] = next
			case 2:
				target := (slot + 1) % len(names)
				if !exists[slot] || exists[target] {
					continue
				}
				method = coreprotocol.FilesRename
				input.NewPath = names[target]
				value = input
				exists[slot], exists[target] = false, true
				contents[target] = contents[slot]
			case 3:
				if !exists[slot] {
					continue
				}
				method, exists[slot] = coreprotocol.FilesDelete, false
			}
			var result coreprotocol.FileOperationResult
			featureCall(t, w, ws, fmt.Sprintf("file-%d", i), method, value, &result)
			if result.Error != "" {
				t.Fatal("valid bounded file mutation failed")
			}
			for n, name := range names {
				data, err := os.ReadFile(filepath.Join(root, name))
				if exists[n] && (err != nil || string(data) != contents[n]) || !exists[n] && !errors.Is(err, os.ErrNotExist) {
					t.Fatal("file existence/content oracle diverged")
				}
			}
		}

		// Public history-owner commands exercise immutable preview, stale refusal,
		// mandatory safety capture and its inverse. Raw history.apply RPC correctly
		// refuses unleased restores; do not bypass that admission with actor internals.
		historyRoot := t.TempDir()
		store, err := localhistory.Open(t.TempDir(), historyRoot)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.Close() }()
		path := filepath.Join(historyRoot, "review.txt")
		write := func(text string) {
			t.Helper()
			if err := os.WriteFile(path, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
		}
		ctx := t.Context()
		opts := localhistory.SnapshotOptions{DisableGitIgnore: true, IncludePaths: []string{"review.txt"}}
		write("original\n")
		target, err := store.Snapshot(ctx, opts)
		if err != nil {
			t.Fatal(err)
		}
		later := fmt.Sprintf("later-%x\n", sequence[:min(len(sequence), featureFuzzActions)])
		write(later)
		plan, err := store.PlanRestore(ctx, target.ID, opts)
		if err != nil || len(plan.Changes) != 1 {
			t.Fatal("preview omitted real change")
		}
		write(later + "newer\n")
		if _, err := store.ApplyRestore(ctx, plan.ID, []string{"review.txt"}); err == nil {
			t.Fatal("stale preview overwrote later work")
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != later+"newer\n" {
			t.Fatal("stale restore changed bytes")
		}
		plan, err = store.PlanRestore(ctx, target.ID, opts)
		if err != nil {
			t.Fatal(err)
		}
		result, err := store.ApplyRestore(ctx, plan.ID, []string{"review.txt"})
		if err != nil || result.PlanID != plan.ID || result.TargetSnapshotID != target.ID || result.SafetySnapshotID == "" {
			t.Fatal("restore lost safety/target identity")
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != "original\n" {
			t.Fatal("restore did not install reviewed bytes")
		}
		inverse, err := store.PlanRestore(ctx, result.SafetySnapshotID, opts)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.ApplyRestore(ctx, inverse.ID, []string{"review.txt"}); err != nil {
			t.Fatal(err)
		}
		if data, err := os.ReadFile(path); err != nil || string(data) != later+"newer\n" {
			t.Fatal("safety inverse lost pre-restore bytes")
		}
	})
}

func FuzzCoreFeatureSettings(f *testing.F) {
	f.Add([]byte{0, 1, 0, 2, 3, 1, 2})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		const sentinel = "fuzz-private-credential"
		want, commits := sentinel, 0
		r, root := featureRuntime(t, func(ws *workspace.Workspace) {
			ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{"one": {ID: "one", ProviderID: "custom", APIKey: sentinel}}})
			ws.OnAppAccountCommit = func(change types.AccountChange) (types.AppSettings, error) {
				if change.ID != "one" || change.After == nil || change.After.APIKey != want {
					t.Error("secret update or account identity diverged")
				}
				commits++
				return types.AppSettings{Accounts: map[string]provider.Account{"one": *change.After}}, nil
			}
		})
		w := newResourceWire(t, r, "settings-owner")
		var attached coreprotocol.AttachResult
		w.call(t, "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root}, &attached)
		for i, action := range sequence[:min(len(sequence), featureFuzzActions)] {
			secret := coreprotocol.SecretString{Keep: true}
			beforeCommits := commits
			old := want
			switch action % 4 {
			case 1:
				want = fmt.Sprintf("%s-%d", sentinel, i)
				secret = coreprotocol.SecretString{Value: &want}
			case 2:
				want, secret = "", coreprotocol.SecretString{}
			case 3:
				secret.Value = &want // contradictory keep+replace must not commit
			}
			out := featureCall(t, w, attached.WorkspaceID, fmt.Sprintf("account-%d", i), coreprotocol.AccountChange, coreprotocol.AccountChangeRequest{ID: "one", After: &coreprotocol.AccountDraft{ID: "one", ProviderID: "custom", Label: fmt.Sprintf("label-%d", i), APIKey: secret}}, nil)
			if action%4 == 3 {
				if out.Error == nil || commits != beforeCommits {
					t.Fatal("ambiguous secret update committed")
				}
				want = old
			} else if out.Error != nil || commits != beforeCommits+1 {
				t.Fatal("valid secret update rejected or committed twice")
			}
			if bytes.Contains(out.Payload, []byte(sentinel)) {
				t.Fatal("write-only credential escaped mutation response")
			}
			var fetched coreprotocol.SettingsFetchResult
			response := featureCall(t, w, attached.WorkspaceID, fmt.Sprintf("fetch-%d", i), coreprotocol.SettingsFetch, coreprotocol.Empty{}, &fetched)
			if bytes.Contains(response.Payload, []byte(sentinel)) || fetched.Accounts["one"].APIKeyPresent != (want != "") || fetched.Accounts["one"].ID != "one" {
				t.Fatal("redacted settings diverged or leaked credential")
			}
		}
	})
}

func FuzzCoreFeatureDraftCAS(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5, 0, 2})
	f.Fuzz(func(t *testing.T, sequence []byte) {
		ctx := context.Background()
		store, err := clientstate.Open(filepath.Join(t.TempDir(), "drafts.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = store.Close() }()
		attachment, err := store.Acquire(ctx, "private", clientstate.AcquireOptions{})
		if err != nil {
			t.Fatal(err)
		}
		key := clientstate.Key{ClientID: "private", WorkspaceID: "workspace", SessionID: "session"}
		var revision uint64
		want := ""
		var last clientstate.Put
		for i, action := range sequence[:min(len(sequence), featureFuzzActions)] {
			put := clientstate.Put{ClientID: "private", Epoch: attachment.Epoch, Key: key, RequestID: fmt.Sprintf("draft-%d", i), ExpectedRevision: revision, Draft: clientstate.Draft{Text: fmt.Sprintf("draft-%d-%d", i, action), Revision: uint64(i + 1)}}
			switch action % 6 {
			case 0, 1:
				record, err := store.Save(ctx, put)
				if err != nil || record.Revision != revision+1 || record.Key != key || record.Draft.Text != put.Draft.Text {
					t.Fatal("draft CAS lost bytes or identity")
				}
				revision, want, last = revision+1, put.Draft.Text, put
			case 2:
				put.ExpectedRevision = revision + 1
				if _, err := store.Save(ctx, put); !errors.Is(err, clientstate.ErrConflict) {
					t.Fatal("stale draft CAS accepted")
				}
			case 3:
				if last.RequestID == "" {
					continue
				}
				last.Epoch = attachment.Epoch
				record, err := store.Save(ctx, last)
				if err != nil || record.Draft.Text != last.Draft.Text || record.Revision != last.ExpectedRevision+1 {
					t.Fatal("exact draft receipt replay changed")
				}
				observed, err := store.LookupReceipt(ctx, "private", attachment.Epoch, last.RequestID)
				if err != nil || observed.Key != key || observed.Revision != record.Revision || observed.Draft.Text != last.Draft.Text {
					t.Fatal("receipt observation lost original result")
				}
			case 4:
				if last.RequestID == "" {
					continue
				}
				put.RequestID = last.RequestID
				if _, err := store.Save(ctx, put); !errors.Is(err, clientstate.ErrRequestMismatch) {
					t.Fatal("changed receipt preimage accepted")
				}
			case 5:
				old := attachment.Epoch
				attachment, err = store.Acquire(ctx, "private", clientstate.AcquireOptions{Takeover: true, ExpectedEpoch: old})
				if err != nil || attachment.Epoch != old+1 {
					t.Fatal("takeover lost epoch identity")
				}
				put.Epoch = old
				if _, err := store.Save(ctx, put); !errors.Is(err, clientstate.ErrStaleEpoch) {
					t.Fatal("detached draft author wrote after takeover")
				}
			}
			got, err := store.Load("private", attachment.Epoch, key)
			if revision == 0 {
				if !errors.Is(err, clientstate.ErrNotFound) {
					t.Fatal("rejected action created draft")
				}
			} else if err != nil || got.Revision != revision || got.Draft.Text != want || got.Key != key {
				t.Fatal("replay/rejection consumed a newer private draft")
			}
		}
	})
}
