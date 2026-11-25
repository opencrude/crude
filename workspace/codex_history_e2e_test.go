package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/internal/providersim"
	"crude/types/setting"
	"crude/workspace/localhistory"
)

func TestCodexPatchProductionUndoRestoresMoveOverwriteAndDelete(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	project := t.TempDir()
	before := map[string]string{"source": "old\r\n", "destination": "destination preimage\n", "delete": "restore deleted\n"}
	for name, text := range before {
		if err := os.WriteFile(filepath.Join(project, name), []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	const patch = "*** Begin Patch\n*** Update File: source\n*** Move to: destination\n-old\n+new\n*** Delete File: delete\n*** Add File: added\n+created\n*** End Patch"
	server := providersim.New(t)
	server.Respond(func(request providersim.Request) providersim.Turn {
		found := false
		for _, tool := range request.Tools {
			var wire struct {
				Type     string         `json:"type"`
				Custom   map[string]any `json:"custom"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if err := json.Unmarshal(tool.Raw, &wire); err != nil {
				t.Error(err)
				return providersim.Turn{}
			}
			if wire.Custom != nil {
				t.Error("Chat gateway received unsupported custom tool")
			}
			if wire.Type == "function" && tool.Name == "apply_patch" {
				found = true
			}
			if wire.Function.Name == "edit_file" || wire.Function.Name == "write_file" {
				t.Error("wrong editor interface")
			}
		}
		if !found {
			t.Error("patch tool not offered")
		}
		if request.Index == 1 {
			args, _ := json.Marshal(map[string]string{"input": patch})
			return providersim.CallTools(providersim.ToolCall{ID: "patch-1", Name: "apply_patch", Arguments: string(args)})
		}
		return providersim.Text("patched")
	})
	cfg := historyE2EConfig(server.BaseURL(providersim.Chat))
	profile := cfg.Agents.Profiles["default"]
	cat := profile.Categories[session.CategoryDefault]
	cat.Model = "gpt-5.5"
	profile.Categories[session.CategoryDefault] = cat
	ws := NewWorkspace(project, "gpt-5.5", "tester", setting.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(setting.App{LocalHistory: setting.LocalHistory{Enabled: "on", UseGitIgnore: "on", CaptureExternalChanges: "on", SafetySnapshotBeforeRestore: "on", MaxFileSizeMB: 10}})
	frontend, err := ws.CreateSession(cfg, home)
	if err != nil {
		t.Fatal(err)
	}
	frontend.backend.SetYolo(true)
	sender := &historyE2ESender{events: make(chan historyE2EEvent, 128)}
	orch, err := ws.StartOrchestrator(frontend.SessionID, sender)
	if err != nil {
		t.Fatal(err)
	}
	if !frontend.backend.SubmitInput(orch.ID, provider.PlainInput("apply the patch")) {
		t.Fatal("not admitted")
	}
	waitHistoryAgentDone(t, sender, orch.ID)
	data, err := os.ReadFile(filepath.Join(project, "destination"))
	if err != nil || string(data) != "new\r\n" {
		t.Fatalf("move failed: %q %v", data, err)
	}
	for _, path := range []string{"source", "delete"} {
		if _, err := os.Stat(filepath.Join(project, path)); !os.IsNotExist(err) {
			t.Fatalf("not removed: %s %v", path, err)
		}
	}
	scopes := waitAgentHistoryScopes(t, ws.HistoryService, frontend.SessionID, orch.ID)
	plan, err := ws.HistoryService.PlanAgentRestore(context.Background(), frontend.SessionID, scopes[0].MessageID, orch.ID)
	if err != nil {
		t.Fatal(err)
	}
	var choices []localhistory.RestoreChoice
	for _, change := range plan.Changes {
		if change.ConflictReason != "" {
			t.Fatalf("unexpected conflict %+v", change)
		}
		choices = append(choices, localhistory.RestoreChoice{Path: change.Path})
	}
	if len(choices) != 4 {
		t.Fatalf("restore paths %+v", choices)
	}
	if _, err := ws.ApplyHistoryRestoreChoices(t.Context(), plan.ID, choices); err != nil {
		t.Fatal(err)
	}
	for path, want := range before {
		got, err := os.ReadFile(filepath.Join(project, path))
		if err != nil || string(got) != want {
			t.Fatalf("undo %s: %q %v", path, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(project, "added")); !os.IsNotExist(err) {
		t.Fatal("added file survived undo")
	}
	foundSnapshot := false
	for _, row := range frontend.backend.MessagesWithMeta() {
		for _, snap := range row.Meta.Snapshots {
			if snap.ToolName == "apply_patch" {
				foundSnapshot = true
				if snap.ToolStatus != "success" || !strings.Contains(snap.Content, "```diff") {
					t.Fatalf("snapshot %+v", snap)
				}
			}
		}
	}
	if !foundSnapshot {
		t.Fatal("patch result missing from durable transcript")
	}
}
