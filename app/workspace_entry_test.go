package app

import (
	"context"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"crude/core/coreclient"
	"crude/core/coreprotocol"
	"crude/types/setting"
	"crude/workspace"
)

func TestPositionalEditorPathReachesStartup(t *testing.T) {
	before, args := flag.CommandLine, os.Args
	defer func() { flag.CommandLine, os.Args = before, args }()
	path := filepath.Join(t.TempDir(), "new note.txt")
	flag.CommandLine = flag.NewFlagSet("crude", flag.ContinueOnError)
	os.Args = []string{"crude", path}
	got, err := invocationRoot(ParseFlags())
	if err != nil || got != path {
		t.Fatalf("startup got %q, want file %q: %v", got, path, err)
	}
}

func TestWireEntryAdmissionAndSharedLanguageDemand(t *testing.T) {
	profile := coreTestHome(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	child := filepath.Join(home, "project")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	connect := func(id string) *coreclient.Client {
		ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
		defer cancel()
		c, err := ConnectCore(ctx, profile, id)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = c.Close() })
		return c
	}
	file, project := connect("file"), connect("project")
	attach := func(t *testing.T, c *coreclient.Client, root string, only bool) string {
		t.Helper()
		var out coreprotocol.AttachResult
		in := coreprotocol.AttachRequest{Root: root, Entry: coreprotocol.EntryIntent{Explicit: true, FileOnly: only}}
		if err := c.Call(t.Context(), "", coreprotocol.Attach, &in, &out); err != nil {
			t.Fatalf("explicit opening %s: %v", root, err)
		}
		if out.View.Root != root {
			t.Fatalf("attached %q, want %q", out.View.Root, root)
		}
		return out.WorkspaceID
	}
	fetch := func(t *testing.T, c *coreclient.Client, id string) setting.App {
		t.Helper()
		var out coreprotocol.SettingsFetchResult
		if err := c.Call(t.Context(), id, coreprotocol.SettingsFetch, &coreprotocol.Empty{}, &out); err != nil {
			t.Fatal(err)
		}
		return out.Effective
	}
	t.Run("explicit-home", func(t *testing.T) {
		id := attach(t, project, home, false)
		effective := fetch(t, project, id)
		if effective.LSP.Enabled || effective.LocalHistory.EnabledValue() {
			t.Fatal("home started with project defaults")
		}
		dir, err := workspace.Dir(home)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(dir, "local-history")); !os.IsNotExist(err) {
			t.Fatalf("history store exists before opt-in: %v", err)
		}
		var result coreprotocol.WorkspacePreferencesResult
		edits := []setting.Edit{{Path: []string{"local_history", "enabled"}, After: json.RawMessage(`"on"`)}, {Path: []string{"lsp", "enabled"}, After: json.RawMessage(`true`)}}
		if err := project.Call(t.Context(), id, coreprotocol.WorkspaceOverridesSet, &coreprotocol.WorkspacePreferencesRequest{Edits: edits}, &result); err != nil {
			t.Fatal(err)
		}
		if !result.Effective.LocalHistory.EnabledValue() || !result.Effective.LSP.Enabled {
			t.Fatal("explicit settings lost to folder defaults")
		}
		if _, err := os.Stat(filepath.Join(dir, "local-history", "history.db")); err != nil {
			t.Fatal("history opt-in did not open its store", err)
		}
	})
	t.Run("file-then-project", func(t *testing.T) {
		var resolved coreprotocol.StartupSnapshot
		path := filepath.Join(child, "new note.txt")
		if err := file.Call(t.Context(), "", coreprotocol.StartupInspect, &coreprotocol.StartupInspectRequest{Root: path, Explicit: true, AllowFile: true}, &resolved); err != nil {
			t.Fatal(err)
		}
		if resolved.Root != child || resolved.FilePath != path || resolved.ChooseWorkspace {
			t.Fatalf("file inspected incorrectly: %+v", resolved)
		}
		id := attach(t, file, resolved.Root, true)
		s := fetch(t, file, id)
		if s.LSP.Enabled || !s.LocalHistory.EnabledValue() {
			t.Fatalf("file opening did not preserve history and turn off language servers: LSP=%t history=%s", s.LSP.Enabled, s.LocalHistory.Enabled)
		}
		if next := attach(t, project, child, false); next != id || !fetch(t, project, id).LSP.Enabled {
			t.Fatal("project shell did not enable services on the same actor")
		}
		peer := connect("second-file")
		if next := attach(t, peer, child, true); next != id || !fetch(t, project, id).LSP.Enabled {
			t.Fatal("file attachment disabled another shell's servers")
		}
	})
}
