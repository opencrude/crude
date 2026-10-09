package core

import (
	"os"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
	"crude/document"
	"crude/workspace"
)

func TestSharedFilesProtectPeerUnsavedTextAndRetainedPaths(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	path := filepath.Join(root, "shared.txt")
	if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
		t.Fatal(err)
	}
	a, ws := resourceConnection(t, r, root, "file-author")
	b, _ := resourceConnection(t, r, root, "file-peer")
	var snap document.Snapshot
	decodeResource(t, callCore(t, a, "open-a", ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	decodeResource(t, callCore(t, b, "open-b", ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
	callCore(t, b, "peer-edit", ws, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: snap.ID, Revision: snap.Revision, Edits: []document.Edit{{Start: 4, End: 4, Text: " peer"}}})
	var out coreprotocol.FileOperationResult
	decodeResource(t, callCore(t, a, "disk-edit", ws, coreprotocol.FilesEdit, coreprotocol.FileEditsRequest{Edits: []workspace.FileEdit{{Path: path, OldContent: "disk", NewContent: "overwrite"}}}), &out)
	if out.Error == "" {
		t.Fatal("disk edit discarded peer work")
	}
	out = coreprotocol.FileOperationResult{}
	decodeResource(t, callCore(t, a, "rename", ws, coreprotocol.FilesRename, coreprotocol.FilePathRequest{Path: path, NewPath: filepath.Join(root, "renamed.txt")}), &out)
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	renamed := filepath.Join(root, "renamed.txt")
	originalID := snap.ID
	decodeResource(t, callCore(t, b, "read-renamed", ws, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: originalID}), &snap)
	if snap.ID != originalID || snap.Path != renamed || string(snap.Text) != "disk peer" {
		t.Fatal(snap)
	}
	path = renamed
	out = coreprotocol.FileOperationResult{}
	decodeResource(t, callCore(t, a, "delete", ws, coreprotocol.FilesDelete, coreprotocol.FilePathRequest{Path: path}), &out)
	if out.Error == "" {
		t.Fatal("retained path deleted")
	}
	decodeResource(t, callCore(t, b, "read", ws, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: snap.ID}), &snap)
	if string(snap.Text) != "disk peer" || !snap.Dirty {
		t.Fatalf("peer text lost: %+v", snap)
	}
	disk, err := os.ReadFile(path)
	if err != nil || string(disk) != "disk" {
		t.Fatalf("disk changed: %q %v", disk, err)
	}
	decodeResource(t, callCore(t, b, "save-renamed", ws, coreprotocol.DocumentSave, coreprotocol.DocumentRevisionRequest{ID: snap.ID, Revision: snap.Revision}), &snap)
	if snap.Path != renamed || snap.Dirty {
		t.Fatal(snap)
	}
	disk, err = os.ReadFile(renamed)
	if err != nil || string(disk) != "disk peer" {
		t.Fatal(string(disk), err)
	}
	callCore(t, b, "undo", ws, coreprotocol.DocumentUndo, coreprotocol.DocumentRevisionRequest{ID: snap.ID, Revision: snap.Revision})
	decodeResource(t, callCore(t, b, "read-undo", ws, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: snap.ID}), &snap)
	if string(snap.Text) != "disk" || !snap.Dirty {
		t.Fatalf("author undo damaged: %+v", snap)
	}
	decodeResource(t, callCore(t, b, "save-undo", ws, coreprotocol.DocumentSave, coreprotocol.DocumentRevisionRequest{ID: snap.ID, Revision: snap.Revision}), &snap)
	out = coreprotocol.FileOperationResult{}
	decodeResource(t, callCore(t, a, "clean-edit", ws, coreprotocol.FilesEdit, coreprotocol.FileEditsRequest{Edits: []workspace.FileEdit{{Path: path, OldContent: "disk", NewContent: "reviewed"}}}), &out)
	if out.Error != "" {
		t.Fatal(out.Error)
	}
	decodeResource(t, callCore(t, b, "read-clean-edit", ws, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: snap.ID}), &snap)
	if string(snap.Text) != "reviewed" || string(snap.Saved) != "reviewed" || snap.Dirty {
		t.Fatalf("clean edit not canonically reconciled: %+v", snap)
	}
}

func TestSharedFilesDirectoryRenameTwoOwnersSaveUndo(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	dir, target := filepath.Join(root, "old"), filepath.Join(root, "new")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	a, ws := resourceConnection(t, r, root, "directory-author")
	b, _ := resourceConnection(t, r, root, "directory-peer")
	ids := []string{}
	for _, name := range []string{"one.txt", "two.txt"} {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("disk"), 0600); err != nil {
			t.Fatal(err)
		}
		var snap document.Snapshot
		decodeResource(t, callCore(t, a, "open-a-"+name, ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
		decodeResource(t, callCore(t, b, "open-b-"+name, ws, coreprotocol.DocumentOpen, coreprotocol.DocumentOpenRequest{Path: path}), &snap)
		callCore(t, b, "edit-"+name, ws, coreprotocol.DocumentEdit, coreprotocol.DocumentEditRequest{ID: snap.ID, Revision: snap.Revision, Edits: []document.Edit{{Start: 4, End: 4, Text: " peer"}}})
		ids = append(ids, snap.ID)
	}
	var result coreprotocol.FileOperationResult
	decodeResource(t, callCore(t, a, "rename-directory", ws, coreprotocol.FilesRename, coreprotocol.FilePathRequest{Path: dir, NewPath: target}), &result)
	if result.Error != "" || !result.IsDir {
		t.Fatal(result)
	}
	for _, id := range ids {
		var snap document.Snapshot
		decodeResource(t, callCore(t, a, "read-a-"+id, ws, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: id}), &snap)
		if snap.ID != id || snap.Path != filepath.Join(target, filepath.Base(id)) || string(snap.Text) != "disk peer" || !snap.Dirty {
			t.Fatal(snap)
		}
		decodeResource(t, callCore(t, b, "save-"+id, ws, coreprotocol.DocumentSave, coreprotocol.DocumentRevisionRequest{ID: id, Revision: snap.Revision}), &snap)
		callCore(t, b, "undo-"+id, ws, coreprotocol.DocumentUndo, coreprotocol.DocumentRevisionRequest{ID: id, Revision: snap.Revision})
		decodeResource(t, callCore(t, a, "read-undo-"+id, ws, coreprotocol.DocumentRead, coreprotocol.DocumentReadRequest{ID: id}), &snap)
		if string(snap.Text) != "disk" || !snap.Dirty {
			t.Fatal(snap)
		}
		decodeResource(t, callCore(t, b, "save-undo-"+id, ws, coreprotocol.DocumentSave, coreprotocol.DocumentRevisionRequest{ID: id, Revision: snap.Revision}), &snap)
		text, err := os.ReadFile(snap.Path)
		if err != nil || string(text) != "disk" {
			t.Fatal(string(text), err)
		}
	}
}
