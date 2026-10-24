package core

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
	"time"

	"crude/coreprotocol"
	"crude/provider"
	"crude/types"
	"crude/workspace"
)

func TestSettingsCaptureAndPreparationBypassHeldSQL(t *testing.T) {
	db, err := workspace.OpenWorkspaceDB(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ws := &workspace.Workspace{WorkspaceDB: db, Logger: coreTestLogger{}}
	ws.SetAppDefaults(types.AppSettings{Accounts: map[string]provider.Account{
		"account": {ID: "account", APIKey: "private", Connection: provider.Template{ProviderURL: "https://user:private@example.test/api?token=private"}},
	}})
	if err := db.SetSetting("workspace_settings", `{"editor":{"tab_size":8},"capabilities":{"connections":{"custom":{"url":"https://example.test?token=private","headers":{"Authorization":"private"},"disabled":false}}}}`); err != nil {
		t.Fatal(err)
	}
	db.DB.SetMaxOpenConns(1)
	connection, err := db.DB.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	a := &actor{ws: ws}
	done := make(chan func(context.Context) coreprotocol.Response, 1)
	go func() {
		out, _ := a.captureSettings()
		if out.Effective.Editor.TabSize != 8 || !out.Accounts["account"].APIKeyPresent || out.Accounts["account"].Connection.ProviderURL != "https://example.test/api" {
			t.Error("capture lost committed settings or account redaction")
		}
		if out.TerminalShells != nil {
			t.Error("pure actor capture performed shell discovery")
		}
		out.Overrides.Editor[0] = '!'
		again, _ := a.captureSettings()
		if string(again.Overrides.Editor) != `{"tab_size":8}` {
			t.Error("capture aliases committed sparse bytes")
		}
		run, _, ok := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.SettingsFetch, Payload: emptyPayload})
		if !ok {
			t.Error("settings fetch not prepared")
		}
		done <- run
	}()
	var run func(context.Context) coreprotocol.Response
	select {
	case run = <-done:
	case <-time.After(time.Second):
		t.Fatal("actor capture/preparation blocked on SQL")
	}
	var out coreprotocol.SettingsFetchResult
	if err := coreprotocol.Decode(run(t.Context()).Payload, &out); err != nil {
		t.Fatal(err)
	}
	if out.Effective.Editor.TabSize != 8 || !reflect.DeepEqual(out.TerminalShells, workspace.DetectTerminalShells()) {
		t.Fatal("worker did not deliver detached committed settings with core-host shells")
	}
}

func TestSettingsShellDiscoveryOccursOnlyWhenWorkerRuns(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic POSIX shell fixture")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	t.Setenv("SHELL", "")
	a := &actor{ws: &workspace.Workspace{Logger: coreTestLogger{}}}
	out, _ := a.captureSettings()
	if out.TerminalShells != nil {
		t.Fatal("capture included host discovery")
	}
	run, _, ok := a.prepareSettings(coreprotocol.Request{Method: coreprotocol.SettingsFetch, Payload: emptyPayload})
	if !ok {
		t.Fatal("settings fetch not prepared")
	}
	// Nothing existed at capture/preparation time. Worker discovery must see the
	// newly installed executable, not replay an actor-side observation.
	shell := filepath.Join(dir, "bash")
	if err := os.WriteFile(shell, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := coreprotocol.Decode(run(t.Context()).Payload, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.TerminalShells) != 1 || out.TerminalShells[0].Path != shell {
		t.Fatalf("worker did not discover newly installed core-host shell: %+v", out.TerminalShells)
	}
}
