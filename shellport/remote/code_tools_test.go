package remote

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/buildinfo"
	"crude/core"
	"crude/coreclient"
	"crude/types"
	"crude/workspace"
)

func TestCodeToolsConnectedInventoryAndOwnerBoundRemoval(t *testing.T) {
	t.Setenv("CRUDE_HOME", t.TempDir())
	home, err := buildinfo.Home()
	if err != nil {
		t.Fatal(err)
	}
	digest := strings.Repeat("b", 64)
	id := "wire-fixture/v1-" + digest[:12]
	dir := filepath.Join(home, buildinfo.DataDir(), "tools", filepath.FromSlash(id))
	if err := os.MkdirAll(filepath.Join(dir, "files"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "files", "server"), []byte("wire fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{"id": id, "package": "wire-fixture", "version": "v1", "sha256": digest, "digest_origin": "expected",
		"executable": "server", "size": 12, "files": 1, "archive": "raw", "source": "fixture", "url": "https://example.test/tool", "installed_by": "user", "approval": "wire-review"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "", "tester", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		return ws, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	dial := func(ctx context.Context, id string) (*coreclient.Client, error) {
		a, b := net.Pipe()
		go func() { _ = core.ServeConnection(t.Context(), r, a) }()
		return coreclient.Dial(ctx, coreclient.SingleCarrier(b), id)
	}
	root := t.TempDir()
	first, err := Attach(t.Context(), Options{Root: root, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Attach(t.Context(), Options{Root: root, Dial: dial})
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	inventory, err := first.ReadCodeToolInventory(t.Context(), 0, 50)
	if err != nil || len(inventory.Tools) != 1 || len(inventory.Tools[0].FileEntries) != 3 {
		t.Fatalf("inventory roundtrip: %+v %v", inventory, err)
	}
	preview, err := first.PreviewCodeToolRemoval(t.Context(), nil, true)
	if err != nil || len(preview.Tools) != 1 || preview.Paths[0] != dir {
		t.Fatalf("preview roundtrip: %+v %v", preview, err)
	}
	if err := second.RemoveCodeTools(t.Context(), preview); err == nil {
		t.Fatal("another shell consumed native review")
	}
	if err := first.RemoveCodeTools(t.Context(), preview); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatalf("exact reviewed folder remains: %v", err)
	}
	if err := first.RemoveCodeTools(t.Context(), preview); err == nil {
		t.Fatal("review replay removed again")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := first.ReadCodeToolInventory(ctx, 0, 50); err == nil {
		t.Fatal("cancelled request succeeded")
	}
}
