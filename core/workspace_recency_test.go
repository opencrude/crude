package core

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
	"crude/types"
	"crude/workspace"
)

func TestWorkspaceReturnUpdatesDurableRecencyWithoutNewSession(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	created := 0
	rt, err := New(func(root string) (*workspace.Workspace, error) {
		created++
		return workspace.NewWorkspace(root, "", "tester", types.Settings{}, coreTestLogger{}), nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	c, err := rt.Connect("recency")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	a, b := t.TempDir(), t.TempDir()
	for i, root := range []string{a, b, a} {
		var attached coreprotocol.AttachResult
		out := callCore(t, c, fmt.Sprintf("attach-%d", i), "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: root})
		if err := json.Unmarshal(out.Payload, &attached); err != nil {
			t.Fatal(err)
		}
		var infos []workspace.WorkspaceInfo
		out = callCore(t, c, fmt.Sprintf("list-%d", i), attached.WorkspaceID, coreprotocol.SavedWorkspaces, struct{}{})
		if err := json.Unmarshal(out.Payload, &infos); err != nil {
			t.Fatal(err)
		}
		if len(infos) == 0 || infos[0].ProjectDir != root || infos[0].LastAccessed == 0 || infos[0].SessionCount != 0 {
			t.Fatalf("visit %d: %+v", i, infos)
		}
	}
	if created != 2 {
		t.Fatalf("return recreated actor: %d", created)
	}
}
