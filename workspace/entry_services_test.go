package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"crude/types/setting"
	"crude/workspace/document"
)

func TestFolderServicesFollowLocationAndExplicitChoices(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("CRUDE_HOME", t.TempDir())
	child := filepath.Join(home, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{home, child} {
		t.Run(filepath.Base(root), func(t *testing.T) {
			w := NewWorkspace(root, "", "", setting.Settings{}, testLogger{})
			defer w.Close()
			w.SetAppDefaults(setting.App{})
			want := root == child
			if (w.LocalHistory != nil) != want || w.LSPMgr.Settings().Enabled != want {
				t.Fatalf("folder services: store=%t, LSP=%t, want both %t", w.LocalHistory != nil, w.LSPMgr.Settings().Enabled, want)
			}
			if w.WorkspaceDB.Setting(workspaceSettingsKey) != "" {
				t.Fatal("opening stored implicit choices")
			}
			factory := &lifecycleRuntime{}
			w.LSPMgr.SetRuntime(factory)
			trustAnalysis(t, w.LSPMgr, root)
			path := filepath.Join(root, "main.go")
			createFile(t, path, "package main")
			if _, err := w.LiveDocuments.Open(document.Owner{ClientID: "editor", Epoch: 1}, path); err != nil {
				t.Fatal(err)
			}
			if want {
				awaitLSP(t, func() bool { return len(factory.snapshot()) == 1 })
				return
			}
			w.LSPMgr.Close()
			if len(factory.snapshot()) != 0 {
				t.Fatal("home constructed a language server after opening Go source")
			}
		})
	}
}
