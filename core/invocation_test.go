package core

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

func TestInvocationTypedCoreJourney(t *testing.T) {
	setCoreTestHome(t, t.TempDir())
	r, err := New(func(root string) (*workspace.Workspace, error) {
		w := workspace.NewWorkspace(root, "saved-model", "tester", types.Settings{}, coreTestLogger{})
		w.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		agents := session.DefaultAgentsConfig()
		p := agents.Profiles["default"]
		p.Categories["default"] = session.CategoryConfig{AccountID: "saved", Model: "saved-model"}
		agents.Profiles["default"] = p
		w.SetBackendConfig(&session.Config{Agents: agents, Accounts: map[string]provider.Account{"saved": {ID: "saved", ProviderID: "openai", APIKey: "saved-key", Connection: provider.Template{Name: "test", ProviderURL: "http://127.0.0.1:1", APIStandard: "openai", AuthType: provider.AuthProtocol}}}}, t.TempDir())
		return w, nil
	}, filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	c, err := r.Connect("invocation-client")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	attach := callCore(t, c, "attach", "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: t.TempDir()})
	var attached coreprotocol.AttachResult
	if err := json.Unmarshal(attach.Payload, &attached); err != nil {
		t.Fatal(err)
	}
	created := callCore(t, c, "create", attached.WorkspaceID, coreprotocol.NewSession, coreprotocol.InvocationSessionRequest{Selection: coreprotocol.InvocationSelection{AccountID: "saved", Model: "selected-model", APIKey: "write-only-key", Instructions: "extra"}})
	var identity workspace.NewSessionResult
	if err := json.Unmarshal(created.Payload, &identity); err != nil {
		t.Fatal(err)
	}
	if identity.Error != "" || identity.SessionID == "" {
		t.Fatalf("admission: %+v", identity)
	}
	actor := r.workspaces[attached.WorkspaceID]
	if actor.ws.BackendSession(identity.SessionID).ModelName() != "selected-model" {
		t.Fatal("typed selector ignored")
	}
	callCore(t, c, "warm", attached.WorkspaceID, coreprotocol.OpenSession, coreprotocol.InvocationSessionRequest{SessionID: identity.SessionID})
	raw, _ := json.Marshal(coreprotocol.InvocationSessionRequest{SessionID: identity.SessionID, Selection: coreprotocol.InvocationSelection{Model: "other"}})
	refused := c.Call(t.Context(), coreprotocol.Request{ID: "incompatible", Owner: c.Owner, WorkspaceID: attached.WorkspaceID, Method: coreprotocol.OpenSession, Payload: raw})
	if refused.Error == nil {
		t.Fatal("live selection mutated peer backend")
	}
	if actor.ws.BackendSession(identity.SessionID).ModelName() != "selected-model" {
		t.Fatal("refusal mutated session")
	}
	workspaceDir := actor.ws.WorkspaceDir
	c.Close()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(workspaceDir, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(data, []byte("write-only-key")) {
			t.Errorf("ephemeral key persisted in %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
