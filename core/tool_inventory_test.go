package core

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"crude/toolstore"
	"crude/types"
)

func TestCodeToolInventoryReportsHistoryAndUnmanaged(t *testing.T) {
	root := t.TempDir()
	store, err := toolstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "history.jsonl"), []byte("{\"kind\":\"install-failed\",\"package\":\"demo\",\"detail\":\"download failed\"}\n{\"kind\":\"remove\",\"package\":\"old\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "unknown", "version"), 0700); err != nil {
		t.Fatal(err)
	}
	plan := types.AssemblyPlan{Languages: []types.AssemblyLanguage{
		{ServerID: "server", Command: "server", Executable: "/usr/bin/server", Source: types.ToolSourcePath},
		{ServerID: "server", Command: "server", Executable: "/usr/bin/server", Source: types.ToolSourcePath},
		{Executable: "/managed", Source: types.ToolSourceStore},
	}}
	got, err := readCodeToolInventory(t.Context(), store, plan, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 0 || got.TotalBytes != 0 || len(got.Unmanaged) != 1 || got.Unmanaged[0].Executable != "/usr/bin/server" {
		t.Fatalf("inventory = %+v", got)
	}
	if len(got.Unrecognized) != 1 || got.HistoryTotal != 2 || len(got.History) != 1 || got.History[0].Detail != "download failed" {
		t.Fatalf("accounting = %+v", got)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := readCodeToolInventory(ctx, store, plan, 0, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled read: %v", err)
	}
}
