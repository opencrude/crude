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
	captypes "crude/types/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Deliberately assert the SDK's original sequence, not a sequence composed by
// ResultContentParts or capabilityMediaResult: both are part of the journey.
func TestMCPOrderedSDKActualRootChildRequestsAndResume(t *testing.T) {
	for _, child := range []bool{false, true} {
		for _, enabled := range []bool{true, false} {
			t.Run(fmt.Sprintf("child=%t/enabled=%t", child, enabled), func(t *testing.T) {
				testMCPOrderedJourney(t, child, enabled)
			})
		}
	}
}

func testMCPOrderedJourney(t *testing.T, child, enabled bool) {
	t.Helper()
	var imageBytes bytes.Buffer
	if err := png.Encode(&imageBytes, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	// Minimal PCM WAV header plus one sample, independently retained for assertions.
	wav := []byte{'R', 'I', 'F', 'F', 38, 0, 0, 0, 'W', 'A', 'V', 'E', 'f', 'm', 't', ' ', 16, 0, 0, 0, 1, 0, 1, 0, 0x40, 0x1f, 0, 0, 0x80, 0x3e, 0, 0, 2, 0, 16, 0, 'd', 'a', 't', 'a', 2, 0, 0, 0, 0, 0}
	imageURL := "data:image/png;base64," + base64.StdEncoding.EncodeToString(imageBytes.Bytes())
	audioBase64 := base64.StdEncoding.EncodeToString(wav)
	var effects atomic.Int32
	sdk := mcp.NewServer(&mcp.Implementation{Name: "ordered-sdk", Version: "1"}, nil)
	sdk.AddTool(&mcp.Tool{Name: "ordered", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		effects.Add(1)
		return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{
			&mcp.TextContent{Text: "ORDER_A"}, &mcp.ImageContent{MIMEType: "image/png", Data: imageBytes.Bytes()},
			&mcp.TextContent{Text: "ORDER_B"}, &mcp.AudioContent{MIMEType: "audio/wav", Data: wav},
			&mcp.TextContent{Text: "ORDER_C"},
		}, StructuredContent: map[string]any{"ordered_receipt": "STRUCTURED_LAST"}}, nil
	})
	mcpHTTP := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return sdk }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer mcpHTTP.Close()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "capabilities.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	root := t.TempDir()
	runtime, err := capability.New(root, t.TempDir(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	settings := captypes.Normalize(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"ordered": {ID: "ordered", Name: "Ordered", Transport: "http", URL: mcpHTTP.URL, Scope: captypes.ScopeWorkspace}}})
	if err := runtime.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(t.Context(), "ordered"); err != nil {
		t.Fatal(err)
	}
	observed := runtime.Snapshot().Connections[0].Tools[0]
	connection := settings.Connections["ordered"]
	connection.Grants = map[string]captypes.ToolGrant{"ordered": {Fingerprint: observed.Fingerprint, Effect: captypes.EffectRead, Scope: "workspace"}}
	settings.Connections["ordered"] = connection
	if err := runtime.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Connect(t.Context(), "ordered"); err != nil {
		t.Fatal(err)
	}
	requests := make(chan map[string]any, 4)
	var calls atomic.Int32
	release := make(chan struct{})
	modelHTTP := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
			return
		}
		requests <- body
		w.Header().Set("Content-Type", "text/event-stream")
		if calls.Add(1) == 1 {
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"ordered-call\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", observed.Name)
			return
		}
		if child {
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"inspected\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer modelHTTP.Close()
	cfg := modelAttributionConfig(modelHTTP.URL, "ordered-root")
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	modalities := []string{}
	if enabled {
		modalities = []string{provider.ModalityImage, provider.ModalityAudio}
	}
	main := profile.Categories[CategoryDefault]
	main.ModelModalities = modalities
	profile.Categories[CategoryDefault] = main
	profile.Categories[CategoryResearcher] = CategoryConfig{AccountID: "account", Model: "ordered-child", ModelModalities: modalities}
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	cfg.Capabilities = settings
	home, dir := t.TempDir(), t.TempDir()
	owner, err := NewWithSessionDir("ordered", cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close()
	owner.SetWorkDir(root)
	owner.SetCapabilities(runtime, agent.NewFileOwnership())
	owner.SetYolo(true)
	attachDelegationTestCollaboration(t, owner)
	var runner *SubAgentRunner
	var orch *Orchestrator
	var sender *mockEventSender
	if child {
		runner = NewSubAgentRunner(owner, 1)
		owner.subAgentSup = runner
		defer func() { runner.SessionCancel(); close(release); runner.Wait() }()
		if _, err := runner.StartAgent(t.Context(), map[string]any{"category": CategoryResearcher, "description": "Inspect ordered evidence", "isolation": "shared"}); err != nil {
			t.Fatal(err)
		}
	} else {
		sender = newMockEventSender()
		orch, err = owner.NewOrchestrator(sender)
		if err != nil {
			t.Fatal(err)
		}
		if !orch.SubmitInput(provider.PlainInput("Inspect ordered evidence")) {
			t.Fatal("input refused")
		}
	}
	_ = waitCapturedRequest(t, requests)
	next := waitCapturedRequest(t, requests)
	assertOrderedWireEvidence(t, next, enabled, imageURL, audioBase64)
	if effects.Load() != 1 {
		t.Fatalf("SDK effects=%d", effects.Load())
	}
	if child {
		runner.SessionCancel()
		return
	}
	waitTurnDone(t, sender, orch.ID)
	path := filepath.Join(dir, owner.SessionID()+".db")
	owner.Close()
	resumed, err := ResumeSessionWithDir(path, cfg, home, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	found := false
	for _, msg := range resumed.msgs {
		if msg.Role != "tool" || !strings.Contains(msg.ContentString(), "ORDER_A") {
			continue
		}
		found = true
		parts := msg.Content.Parts()
		if enabled {
			if len(parts) < 8 || parts[2].ImageURL == nil || parts[2].ImageURL.URL != imageURL || parts[5].Type != "audio" || !bytes.Equal(parts[5].Data, wav) {
				t.Fatalf("SQLite lost order: %+v", parts)
			}
			if !strings.Contains(parts[1].Text, "Binary image") || !strings.Contains(parts[4].Text, "Binary audio") {
				t.Fatalf("SQLite lost media descriptions: %+v", parts)
			}
		} else {
			for _, part := range parts {
				if part.Type != "text" || len(part.Data) != 0 || part.ImageURL != nil {
					t.Fatalf("SQLite retained disabled bytes: %+v", part)
				}
			}
		}
		if !strings.Contains(parts[len(parts)-1].Text, "STRUCTURED_LAST") || !strings.Contains(parts[len(parts)-1].Text, "MCP server reported a tool failure") {
			t.Fatalf("SQLite lost structured/failure tail: %+v", parts)
		}
	}
	if !found || calls.Load() != 2 || effects.Load() != 1 {
		t.Fatalf("passive resume found=%t calls=%d effects=%d", found, calls.Load(), effects.Load())
	}
}

func assertOrderedWireEvidence(t *testing.T, request map[string]any, enabled bool, imageURL, audioBase64 string) {
	t.Helper()
	// Exclude schemas/system policy: only actual accepted model evidence counts.
	var evidence []any
	for _, item := range request["messages"].([]any) {
		message := item.(map[string]any)
		if message["role"] == "tool" || message["role"] == "user" {
			if content, ok := message["content"].(string); ok && !strings.Contains(content, "ORDER_") {
				continue
			}
			evidence = append(evidence, message)
		}
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, "MCP server reported a tool failure") {
		t.Fatalf("remote isError lost from model evidence: %s", text)
	}
	if !enabled {
		if strings.Contains(text, imageURL) || strings.Contains(text, audioBase64) || !strings.Contains(text, "image input is disabled for model") || !strings.Contains(text, "audio input is disabled for model") {
			t.Fatalf("disabled bytes leaked or refusal missing: %s", text)
		}
		return
	}
	last := -1
	for _, needle := range []string{"ORDER_A", imageURL, "ORDER_B", audioBase64, "ORDER_C", "STRUCTURED_LAST"} {
		index := strings.Index(text, needle)
		if index <= last {
			t.Fatalf("ordered evidence missing/reordered %q at %d after %d: %s", needle, index, last, text)
		}
		last = index
	}
	if strings.Index(text, "MCP server reported a tool failure") <= last {
		t.Fatalf("failure evidence must follow original structured result: %s", text)
	}
	if !strings.Contains(text, "ordered-call") || !strings.Contains(text, "not new human input") || strings.Contains(text, "preceding read_file") {
		t.Fatalf("remote tool attribution missing or wrong: %s", text)
	}
}
