package session

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent"
	"crude/capability"
	"crude/provider"
	"crude/tools"
	captypes "crude/types/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func mcpMediaFixture(t *testing.T) (*capability.Service, captypes.Settings, string, string) {
	t.Helper()
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	payload := base64.StdEncoding.EncodeToString(encoded.Bytes())
	server := mcp.NewServer(&mcp.Implementation{Name: "media-fixture", Version: "1"}, nil)
	server.AddResource(&mcp.Resource{URI: "fixture://image", Name: "Image", MIMEType: "image/png"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{{URI: "fixture://image", MIMEType: "image/png", Blob: encoded.Bytes()}}}, nil
	})
	server.AddPrompt(&mcp.Prompt{Name: "image-prompt"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{{Role: "user", Content: &mcp.ImageContent{MIMEType: "image/png", Data: encoded.Bytes()}}}}, nil
	})
	server.AddTool(&mcp.Tool{Name: "failed-image", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "remote tool failed"}, &mcp.ImageContent{MIMEType: "image/png", Data: encoded.Bytes()}}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(endpoint.Close)
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "workspace.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	root := t.TempDir()
	runtime, err := capability.New(root, t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(runtime.Close)
	settings := captypes.Normalize(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"media": {ID: "media", Name: "Media", Transport: "http", URL: endpoint.URL, Scope: captypes.ScopeWorkspace}}})
	if err := runtime.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(t.Context(), "media"); err != nil {
		t.Fatal(err)
	}
	return runtime, settings, payload, root
}

func TestMCPMediaSDKPromptAttributionAndFailedTool(t *testing.T) {
	runtime, settings, payload, _ := mcpMediaFixture(t)
	observed := runtime.Snapshot().Connections[0].Tools[0]
	connection := settings.Connections["media"]
	connection.Grants = map[string]captypes.ToolGrant{"failed-image": {Fingerprint: observed.Fingerprint, Effect: captypes.EffectRead, Scope: "workspace"}}
	settings.Connections["media"] = connection
	if err := runtime.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(t.Context(), "media"); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "fixture", Effects: []captypes.Effect{captypes.EffectRead}}
	snapshot, err := runtime.Prepare(t.Context(), actor)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"mcp_get_prompt", observed.Name} {
		t.Run(name, func(t *testing.T) {
			params := map[string]any{"connection_id": "media", "name": "image-prompt"}
			raw, remoteErr := runtime.Execute(t.Context(), actor, snapshot, name, params)
			result, err := capabilityMediaResult(raw, remoteErr, &tools.MediaReadPolicy{Model: "fixture", Standard: "openai", Modalities: []string{"image"}})
			if len(result.Attachments) != 1 || result.Attachments[0].ImageURL.URL != "data:image/png;base64,"+payload {
				t.Fatalf("SDK media lost: %+v err=%v", result, err)
			}
			if name == "mcp_get_prompt" {
				if err != nil || !strings.Contains(result.Content, `"mcp_connection_id":"media"`) || !strings.Contains(result.Content, `"source_role":"user"`) {
					t.Fatalf("prompt attribution lost: %s %v", result.Content, err)
				}
			} else if err == nil || !strings.Contains(result.Content, "remote tool failed") {
				t.Fatalf("failure became success: %s %v", result.Content, err)
			}
		})
	}
}

func TestMCPMediaActualStartedChildUsesOwnSelection(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		t.Run(fmt.Sprint(enabled), func(t *testing.T) {
			runtime, settings, payload, root := mcpMediaFixture(t)
			requests := make(chan map[string]any, 4)
			release := make(chan struct{})
			var count atomic.Int32
			endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					return
				}
				requests <- body
				w.Header().Set("Content-Type", "text/event-stream")
				if count.Add(1) == 1 {
					_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"child-media\",\"type\":\"function\",\"function\":{\"name\":\"mcp_read_resource\",\"arguments\":\"{\\\"connection_id\\\":\\\"media\\\",\\\"uri\\\":\\\"fixture://image\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
					return
				}
				select {
				case <-release:
				case <-r.Context().Done():
				}
			}))
			defer endpoint.Close()
			cfg := modelAttributionConfig(endpoint.URL, "parent-media")
			profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
			main := profile.Categories[CategoryDefault]
			main.ModelModalities = []string{provider.ModalityAudio}
			profile.Categories[CategoryDefault] = main
			modalities := []string{}
			if enabled {
				modalities = []string{provider.ModalityImage}
			}
			profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "child-media", ModelModalities: modalities}
			cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
			cfg.Capabilities = settings
			home := t.TempDir()
			owner, err := NewWithSessionDir("", cfg, home, filepath.Join(home, "sessions"))
			if err != nil {
				t.Fatal(err)
			}
			defer owner.Close()
			owner.SetWorkDir(root)
			owner.SetCapabilities(runtime, agent.NewFileOwnership())
			owner.SetYolo(true)
			attachDelegationTestCollaboration(t, owner)
			runner := NewSubAgentRunner(owner, 1)
			owner.subAgentSup = runner
			defer func() { runner.SessionCancel(); close(release); runner.Wait() }()
			// This standalone media journey has no workspace execution owner.
			_, err = runner.StartAgent(t.Context(), map[string]any{"category": CategoryResearcher, "description": "Inspect actual MCP image", "isolation": "shared"})
			if err != nil {
				t.Fatal(err)
			}
			_ = waitCapturedRequest(t, requests)
			next := waitCapturedRequest(t, requests)
			body, _ := json.Marshal(next)
			if next["model"] != "child-media" {
				t.Fatalf("foreign model: %v", next["model"])
			}
			attached := strings.Contains(string(body), "data:image/png;base64,"+payload)
			if attached != enabled {
				t.Fatalf("child enabled=%v attached=%v", enabled, attached)
			}
			if !enabled && !strings.Contains(string(body), "image input is disabled for model") {
				t.Fatal("explicit child denial lost refusal")
			}
			// Cancel the real child rather than fabricate its mandatory report.
			runner.SessionCancel()
		})
	}
}

func TestMCPMediaActualRootRequestAndPassiveResume(t *testing.T) {
	runtime, settings, payload, root := mcpMediaFixture(t)
	requests := make(chan map[string]any, 4)
	var count atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if count.Add(1) == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"media-call\",\"type\":\"function\",\"function\":{\"name\":\"mcp_read_resource\",\"arguments\":\"{\\\"connection_id\\\":\\\"media\\\",\\\"uri\\\":\\\"fixture://image\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Inspected image\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer endpoint.Close()
	cfg := modelAttributionConfig(endpoint.URL, "mcp-root-media")
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	main := profile.Categories[CategoryDefault]
	main.ModelModalities = []string{provider.ModalityImage}
	profile.Categories[CategoryDefault] = main
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	cfg.Capabilities = settings
	home, dir := t.TempDir(), t.TempDir()
	owner, err := NewWithSessionDir("media", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	owner.SetWorkDir(root)
	owner.SetCapabilities(runtime, agent.NewFileOwnership())
	owner.SetYolo(true)
	sender := newMockEventSender()
	orch, err := owner.NewOrchestrator(sender)
	if err != nil {
		t.Fatal(err)
	}
	if !orch.SubmitInput(provider.PlainInput("Inspect fixture image")) {
		t.Fatal("input refused")
	}
	_ = waitCapturedRequest(t, requests)
	next := waitCapturedRequest(t, requests)
	body, _ := json.Marshal(next)
	waitTurnDone(t, sender, orch.ID)
	if !strings.Contains(string(body), "data:image/png;base64,"+payload) {
		t.Fatalf("actual next model request has no SDK image attachment: %s", body)
	}
	path := filepath.Join(dir, owner.SessionID()+".db")
	owner.Close()
	resumed, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	found := false
	for _, message := range resumed.msgs {
		for _, part := range message.Content.Parts() {
			if part.ImageURL != nil && part.ImageURL.URL == "data:image/png;base64,"+payload {
				found = true
			}
		}
	}
	if !found || count.Load() != 2 {
		t.Fatalf("persisted media=%v provider calls=%d", found, count.Load())
	}
}
