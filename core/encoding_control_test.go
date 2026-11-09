package core

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/coreclient"
	"crude/coreprotocol"
	"crude/document"
	"crude/textencoding"
	"crude/types/control"
)

func TestControlUnnamedEncodingJourney(t *testing.T) {
	r, root := sharedDocumentRuntime(t)
	c := controlWireClient(t, r, "encoding-control")
	var attached coreprotocol.AttachResult
	if err := c.Call(t.Context(), "", coreprotocol.Attach, &coreprotocol.AttachRequest{Root: root}, &attached); err != nil {
		t.Fatal(err)
	}
	if err := c.Call(t.Context(), attached.WorkspaceID, coreprotocol.ControlClientRegister, &coreprotocol.ControlClientGrant{Effects: []control.Effect{control.Read, control.Write}}, nil); err != nil {
		t.Fatal(err)
	}
	call := func(id, command, resource string, input any, output any) control.Result {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		req := control.Request{ID: id, CommandID: command, Target: control.Target{ResourceID: resource}, Input: raw}
		var out control.Result
		if _, err := c.CallWith(t.Context(), attached.WorkspaceID, coreprotocol.ControlExecute, &req, &out, coreclient.CallOptions{ID: id}); err != nil {
			t.Fatal(err)
		}
		if out.Fault != nil {
			t.Fatalf("%s: %+v", command, out)
		}
		if output != nil {
			if err := json.Unmarshal(out.Output, output); err != nil {
				t.Fatalf("%s: %v", command, err)
			}
		}
		return out
	}
	call("preference", "settings.set", "", map[string]any{"scope": "workspace", "path": "editor.new_file_encoding", "value": "utf-16-le"}, nil)
	var draft document.Snapshot
	call("new", "document.new", "", struct{}{}, &draft)
	if draft.Path != "" || draft.Dirty || draft.Encoding != textencoding.UTF16LE {
		t.Fatalf("default was not applied to the unnamed document: %+v", draft)
	}
	var edit document.Receipt
	call("edit", "document.edit", draft.ID, coreprotocol.DocumentEditRequest{ID: draft.ID, Revision: draft.Revision, Edits: []document.Edit{{Text: "café 😀\r\n"}}}, &edit)
	var saved document.Snapshot
	call("save", "document.save_untitled", draft.ID, coreprotocol.DocumentSaveUntitledRequest{ID: draft.ID, Revision: edit.Revision, Directory: root, Name: "new.txt", Encoding: draft.Encoding}, &saved)
	encoding := textencoding.UTF8BOM
	result := call("convert", "document.save", draft.ID, coreprotocol.DocumentRevisionRequest{ID: draft.ID, Revision: saved.Revision, Encoding: &encoding}, &saved)
	if result.State != control.Committed || saved.ID != draft.ID || saved.Encoding != encoding || saved.Dirty {
		t.Fatalf("conversion not committed: %+v %+v", result, saved)
	}
	want := []byte("\xef\xbb\xbfcafé 😀\r\n")
	actual, err := os.ReadFile(filepath.Join(root, "new.txt"))
	if err != nil || !bytes.Equal(actual, want) {
		t.Fatalf("control saved wrong bytes: %x, %v", actual, err)
	}
	call("reload", "document.reload", draft.ID, coreprotocol.DocumentRevisionRequest{ID: draft.ID, Revision: saved.Revision}, &saved)
	if saved.Encoding != encoding || string(saved.Text) != "café 😀\r\n" {
		t.Fatal("reload changed the text or encoding")
	}
}
