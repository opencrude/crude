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
	external := filepath.Join(t.TempDir(), "server")
	if err := os.WriteFile(external, []byte("external"), 0600); err != nil {
		t.Fatal(err)
	}
	plan := types.AssemblyPlan{Languages: []types.AssemblyLanguage{
		{ServerID: "server", Command: "server", Executable: external, Source: types.ToolSourcePath},
		{ServerID: "server", Command: "server", Executable: external, Source: types.ToolSourcePath},
		{Executable: "/managed", Source: types.ToolSourceStore},
	}}
	plan.State = types.AssemblyReady
	plan.Generation = 42
	got, err := readCodeToolInventory(t.Context(), store, plan, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 0 || got.TotalBytes != 0 || len(got.Unmanaged) != 1 || got.Unmanaged[0].Executable != external {
		t.Fatalf("inventory = %+v", got)
	}
	if got.Project.State != types.AssemblyReady || got.Project.Generation != 42 || len(got.Project.Languages) != 3 {
		t.Fatalf("inventory lost its project assembly snapshot: %+v", got.Project)
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

func TestCodeToolInventoryDoesNotLabelManagedExecutableExternal(t *testing.T) {
	store, _, dir := codeToolsStoreFixture(t)
	executable := filepath.Join(dir, "files", "server")
	plan := types.AssemblyPlan{Languages: []types.AssemblyLanguage{
		{Command: executable, Executable: executable, Source: types.ToolSourceSettings},
	}}
	alias := filepath.Join(t.TempDir(), "alias")
	if os.Symlink(executable, alias) == nil {
		plan.Languages = append(plan.Languages, types.AssemblyLanguage{
			Command: alias, Executable: alias, Source: types.ToolSourcePath,
		})
	}
	got, err := readCodeToolInventory(t.Context(), store, plan, 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Tools) != 1 || len(got.Unmanaged) != 0 {
		t.Fatalf("managed executable was reported as external: %+v", got)
	}
}
