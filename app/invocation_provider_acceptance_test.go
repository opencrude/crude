package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/core"
	"crude/coreclient"
	"crude/coreprotocol"
	"crude/provider"
	"crude/session"
	"crude/types"
	"crude/workspace"
)

func TestInvocationProviderWireKeepsPolicyAndEphemeralAuth(t *testing.T) {
	profileHome := t.TempDir()
	t.Setenv("HOME", profileHome)
	t.Setenv("USERPROFILE", profileHome)
	const key = "acceptance-ephemeral-write-only-key"
	type observed struct {
		model, system string
		auth          bool
	}
	requests := make(chan observed, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			return
		}
		var request struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			t.Error(err)
			return
		}
		var system string
		for _, msg := range request.Messages {
			if msg.Role == "system" {
				system += msg.Content
			}
		}
		requests <- observed{request.Model, system, r.Method == http.MethodPost && r.Header.Get("Authorization") == "Bearer "+key}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"accepted\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	home := t.TempDir()
	accounts := map[string]provider.Account{"saved": {ID: "saved", ProviderID: "openai", APIKey: "saved-key", Connection: provider.Template{Name: "fixture", ProviderURL: server.URL, APIStandard: "openai", AuthType: provider.AuthProtocol}}}
	agents := session.DefaultAgentsConfig()
	p := agents.Profiles["default"]
	p.Categories["default"] = session.CategoryConfig{AccountID: "saved", Model: "saved-model", Instructions: "retained profile instruction"}
	agents.Profiles["default"] = p
	cfg := &session.Config{Accounts: accounts, Agents: agents}
	runtime, err := core.New(func(root string) (*workspace.Workspace, error) {
		ws := workspace.NewWorkspace(root, "saved-model", "tester", types.Settings{}, nil)
		ws.SetAppDefaults(types.AppSettings{LocalHistory: types.LocalHistorySettings{Enabled: "off"}})
		ws.SetBackendConfig(cfg, home)
		return ws, nil
	}, filepath.Join(home, "receipts.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := runtime.Close(); err != nil {
			t.Error(err)
		}
	}()
	peer, wire := net.Pipe()
	go func() { _ = core.ServeConnection(t.Context(), runtime, peer) }()
	client, err := coreclient.Dial(t.Context(), coreclient.SingleCarrier(wire), "invocation-provider")
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	var attached coreprotocol.AttachResult
	if err := client.Call(t.Context(), "", coreprotocol.Attach, coreprotocol.AttachRequest{Root: t.TempDir()}, &attached); err != nil {
		t.Fatal(err)
	}
	selection := invocationSelection(CLIFlags{Account: "saved", Model: "selected-wire-model", Provider: "openai", APIKey: key, SystemPrompt: "extra <unsafe> & instruction"})
	var created workspace.NewSessionResult
	if err := client.Call(t.Context(), attached.WorkspaceID, coreprotocol.NewSession, &coreprotocol.InvocationSessionRequest{Selection: selection}, &created); err != nil {
		t.Fatal(err)
	}
	if created.Error != "" {
		t.Fatal(created.Error)
	}
	var admitted coreprotocol.InputResult
	if err := client.Call(t.Context(), attached.WorkspaceID, coreprotocol.SubmitInput, &coreprotocol.InputRequest{AgentID: created.OrchID, Text: "wire acceptance", DraftRevision: 1}, &admitted); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-requests:
		if !got.auth || got.model != "selected-wire-model" {
			t.Fatal("actual POST ignored invocation model/auth")
		}
		for _, want := range []string{"retained profile instruction", "extra &lt;unsafe&gt; &amp; instruction", "tool_contract", "instruction_priority"} {
			if !strings.Contains(got.system, want) {
				t.Fatalf("provider system is missing required additive policy span %q", want)
			}
		}
	case <-time.After(10 * time.Second):
		t.Fatal("actual invocation provider POST did not arrive")
	}
	if cfg.GetActiveDefaultCategory().Model != "saved-model" || accounts["saved"].APIKey != "saved-key" || len(accounts) != 1 {
		t.Fatal("invocation mutated shared defaults/accounts")
	}
	// Release Windows' exclusive journal lock before auditing persisted bytes.
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if err := filepath.WalkDir(home, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, []byte(key)) {
			t.Errorf("ephemeral credential persisted in %s", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
