package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent"
	"crude/capability"
	"crude/collaboration"
	"crude/provider"
	"crude/skills"
	"crude/types"
	captypes "crude/types/capability"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

func TestAutoReviewAcquiresLaunchesGrantsAndUsesMCPWithoutHumanResponse(test *testing.T) {
	root := test.TempDir()
	skill, err := skills.Create(filepath.Join(root, "review-task"), "review-task", "Inspect the task evidence before completion.")
	if err != nil {
		test.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(test.TempDir(), "workspace.db")+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)")
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	// Match the product workspace DB's connection policy for the shared
	// collaboration and capability authorization writers.
	database.SetMaxOpenConns(1)
	runtime, err := capability.New(root, filepath.Join(test.TempDir(), "artifacts"), database)
	if err != nil {
		test.Fatal(err)
	}
	defer runtime.Close()
	connection := captypes.ConnectionConfig{ID: "task", Name: "Task fixture", Transport: "stdio", Command: executable, Args: []string{"-test.run=^TestSessionAutonomyStdioHelper$"}, Environment: map[string]string{"CRUDE_SESSION_AUTONOMY_HELPER": "1"}, Scope: captypes.ScopeWorkspace, LaunchApproved: true}
	settings := captypes.Normalize(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"task": connection}, Sources: map[string]captypes.SourceConfig{"local": {ID: "local", Name: "Task source", Kind: "local", Location: root, Trusted: true}}, Acquisition: captypes.AcquisitionSettings{Mode: "ask"}})
	if err := runtime.Configure(test.Context(), settings); err != nil {
		test.Fatal(err)
	}
	runtime.SetRegistrationStore(&capabilityTaskRegistration{settings: &settings})
	if err := runtime.Connect(test.Context(), "task"); err != nil {
		test.Fatal(err)
	}
	tool := runtime.Snapshot().Connections[0].Tools[0]
	if err := runtime.Disconnect("task"); err != nil {
		test.Fatal(err)
	}
	connection.LaunchApproved = false
	settings.Connections["task"] = connection
	if err := runtime.Configure(test.Context(), settings); err != nil {
		test.Fatal(err)
	}
	requests := make(chan map[string]any, 8)
	var requestCount atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			if request.Context().Err() == nil {
				test.Error(err)
			}
			return
		}
		if body["model"] == "review-model" {
			if body["tools"] != nil {
				test.Error("reviewer was given tools")
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(writer, `{"choices":[{"message":{"role":"assistant","content":"{\"decision\":\"allow\",\"reason\":\"exact task fixture operation\"}"},"finish_reason":"stop"}]}`)
			return
		}
		requests <- body
		writer.Header().Set("Content-Type", "text/event-stream")
		var name string
		arguments := map[string]any{}
		switch requestCount.Add(1) {
		case 1:
			name, arguments = "capability_search", map[string]any{"query": skill.Name, "kind": "skill"}
		case 2:
			name, arguments = "capability_activate", map[string]any{"id": "local:" + skill.Name}
		case 3:
			name, arguments = "capability_activate", map[string]any{"id": "task"}
		case 4:
			name, arguments = "mcp_request_access", map[string]any{"connection_id": "task", "grants": []captypes.AccessRequest{
				{Tool: "lookup", Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: tool.Fingerprint},
			}}
		case 5:
			name = tool.Name
		default:
			_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"Task used source instructions and exact SDK evidence.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		encoded, _ := json.Marshal(arguments)
		_, _ = fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", fmt.Sprintf("task-%d", requestCount.Load()), name, string(encoded))
	}))
	defer providerServer.Close()
	config := modelAttributionConfig(providerServer.URL, "capability-task-model")
	config.Capabilities = settings
	config.Safety.AutoPermission = types.AutoPermissionSettings{Enabled: "on", Provider: "openrouter", AccountID: "account", Model: "review-model", DenyMode: "deny", ThinkingEffort: "low", ResponseFormat: types.AutoPermissionFormatPrompt, TimeoutSeconds: 8, MaxOutputTokens: 96, MaxAttempts: 1, RetryTokenGrowth: types.AutoPermissionGrowthFixed, MaxRetryOutputTokens: 96, MaxInputBytes: 64 << 10}
	home, directory := test.TempDir(), filepath.Join(test.TempDir(), "sessions")
	owner, err := NewWithSessionDir("capability-task", config, home, directory)
	if err != nil {
		test.Fatal(err)
	}
	defer owner.Close()
	owner.SetWorkDir(root)
	owner.SetCapabilities(runtime, agent.NewFileOwnership())
	store, err := collaboration.Attach(database, root, filepath.Join(home, "locks"))
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	owner.SetCollaboration(store, "auto-review-capability-fixture")
	runtime.SetRuntimeAuthority(sessionAutonomyAuthority{store: store})
	sender := newMockEventSender()
	orchestrator, err := owner.NewOrchestrator(sender)
	if err != nil {
		test.Fatal(err)
	}
	if !orchestrator.SubmitInput(provider.PlainInput("Use review-task to inspect exact fixture evidence")) {
		test.Fatal("task was not admitted")
	}
	var activationContext, dynamicDefinition, actualResult bool
	for requestIndex := 0; requestIndex < 6; requestIndex++ {
		body := waitCapturedRequest(test, requests)
		encoded, _ := json.Marshal(body)
		activationContext = activationContext || strings.Contains(string(encoded), "source instructions, subordinate")
		actualResult = actualResult || strings.Contains(string(encoded), "real SDK tool evidence")
		if requestIndex >= 4 {
			for _, raw := range body["tools"].([]any) {
				definition, _ := raw.(map[string]any)
				function, _ := definition["function"].(map[string]any)
				dynamicDefinition = dynamicDefinition || function["name"] == tool.Name
			}
		}
	}
	waitTurnDone(test, sender, orchestrator.ID)
	waitAssistantCount(test, owner, 1)
	if !activationContext || !dynamicDefinition || !actualResult {
		test.Fatalf("task workflow: skill=%t definition=%t result=%t ", activationContext, dynamicDefinition, actualResult)
	}
	if len(runtime.Snapshot().Settings.Connections["task"].Grants) != 0 {
		test.Fatal("Auto Review synthesized durable tool trust")
	}
	if !runtime.Snapshot().Settings.Skills["local:"+skill.Name].Managed {
		test.Fatal("task acquisition did not publish an immutable managed artifact")
	}
	var phases int
	if err := database.QueryRow(`SELECT COUNT(DISTINCT phase) FROM capability_authorizations WHERE origin='reviewer' AND run_id != ''`).Scan(&phases); err != nil || phases != 3 {
		test.Fatalf("reviewer acquisition/launch/access audit: phases=%d err=%v", phases, err)
	}
	path := filepath.Join(directory, owner.SessionID()+".db")
	owner.Close()
	resumed, err := ResumeSessionWithDir(path, config, home, directory)
	if err != nil {
		test.Fatal(err)
	}
	defer resumed.Close()
	if requestCount.Load() != 6 {
		test.Fatal("resume started a provider request or replayed a tool")
	}
}

func TestSessionAutonomyStdioHelper(t *testing.T) {
	if os.Getenv("CRUDE_SESSION_AUTONOMY_HELPER") != "1" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "session-autonomy", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", Description: "Read exact evidence", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "real SDK tool evidence"}}}, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type sessionAutonomyAuthority struct{ store *collaboration.Store }

func (authority sessionAutonomyAuthority) ValidateRuntime(ctx context.Context, actor captypes.RuntimeActor) error {
	participant, err := authority.store.Agent(ctx, actor.ID)
	if err != nil {
		return err
	}
	run, err := authority.store.Run(ctx, actor.RunID)
	if err != nil {
		return err
	}
	if participant.CurrentRunID != actor.RunID || participant.RuntimeEpoch != actor.RuntimeEpoch || participant.Presence != collaboration.RuntimeAttached || run.State.Terminal() {
		return collaboration.ErrStale
	}
	return ctx.Err()
}
