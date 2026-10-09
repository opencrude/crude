package workspace

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/localhistory"
	"crude/provider"
	"crude/session"
	ttypes "crude/types"
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
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Tools []struct {
				Type     string         `json:"type"`
				Custom   map[string]any `json:"custom"`
				Function map[string]any `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		found := false
		for _, tool := range request.Tools {
			if tool.Custom != nil {
				t.Error("Chat gateway received unsupported custom tool")
			}
			if tool.Type == "function" && tool.Function["name"] == "apply_patch" {
				found = true
			}
			if tool.Function["name"] == "edit_file" || tool.Function["name"] == "write_file" {
				t.Error("wrong editor interface")
			}
		}
		if !found {
			t.Error("patch tool not offered")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if requests.Add(1) == 1 {
			args, _ := json.Marshal(map[string]string{"input": patch})
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"patch-1\",\"type\":\"function\",\"function\":{\"name\":\"apply_patch\",\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", string(args))
		} else {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"patched\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
		}
	}))
	defer server.Close()
	cfg := historyE2EConfig(server.URL)
	profile := cfg.Agents.Profiles["default"]
	cat := profile.Categories[session.CategoryDefault]
	cat.Model = "gpt-5.5"
	profile.Categories[session.CategoryDefault] = cat
	ws := NewWorkspace(project, "gpt-5.5", "tester", ttypes.Settings{}, &testLogger{})
	t.Cleanup(ws.Close)
	ws.SetAppDefaults(ttypes.AppSettings{LocalHistory: ttypes.LocalHistorySettings{Enabled: "on", UseGitIgnore: "on", CaptureExternalChanges: "on", SafetySnapshotBeforeRestore: "on", MaxFileSizeMB: 10}})
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
