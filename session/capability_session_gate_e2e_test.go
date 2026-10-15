package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"crude/agent"
	"crude/capability"
	"crude/collaboration"
	"crude/provider"
	"crude/sessiondb"
	captypes "crude/types/capability"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	_ "modernc.org/sqlite"
)

// A session switch turns an MCP server off for a run already in progress: its
// tools leave the next request, calls from an earlier request and
// re-activation are refused, and switching it back on restores the tool
// without touching the registration. The gate persists across resume.
func TestSessionCapabilityGateAppliesToRunningAgent(test *testing.T) {
	root := test.TempDir()
	var calls atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "Gate fixture", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "lookup", Description: "Read exact evidence", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "gate evidence"}}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer endpoint.Close()
	database, err := sql.Open("sqlite", filepath.Join(test.TempDir(), "workspace.db")+"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=journal_mode(WAL)")
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	database.SetMaxOpenConns(1)
	runtime, err := capability.New(root, filepath.Join(test.TempDir(), "artifacts"), database)
	if err != nil {
		test.Fatal(err)
	}
	defer runtime.Close()
	connection := captypes.ConnectionConfig{ID: "task", Name: "Gate fixture", Transport: "http", URL: endpoint.URL, Scope: captypes.ScopeWorkspace}
	settings := captypes.Normalize(captypes.Settings{Connections: map[string]captypes.ConnectionConfig{"task": connection}, Acquisition: captypes.AcquisitionSettings{Mode: "automatic"}})
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

	var owner *Session
	gate := func(enabled bool) {
		if _, err := owner.SetCapabilityEnabled("task", enabled); err != nil {
			test.Error(err)
		}
	}
	type step struct {
		before    func()
		name      string
		arguments map[string]any
	}
	steps := []step{
		{name: "capability_activate", arguments: map[string]any{"id": "task"}},
		{name: "mcp_request_access", arguments: map[string]any{"connection_id": "task", "grants": []captypes.AccessRequest{{Tool: "lookup", Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: tool.Fingerprint}}}},
		// Offered in this request; switched off before the call executes.
		{before: func() { gate(false) }, name: tool.Name},
		{name: "capability_activate", arguments: map[string]any{"id": "task"}},
		{before: func() { gate(true) }, name: "capability_search", arguments: map[string]any{"query": "gate"}},
		{name: tool.Name},
	}
	requests := make(chan map[string]any, 16)
	var requestCount atomic.Int32
	providerServer := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return
		}
		requests <- body
		writer.Header().Set("Content-Type", "text/event-stream")
		index := int(requestCount.Add(1)) - 1
		if index >= len(steps) {
			_, _ = fmt.Fprint(writer, "data: {\"choices\":[{\"delta\":{\"content\":\"Done.\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		current := steps[index]
		if current.before != nil {
			current.before()
		}
		arguments := current.arguments
		if arguments == nil {
			arguments = map[string]any{}
		}
		encoded, _ := json.Marshal(arguments)
		_, _ = fmt.Fprintf(writer, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":%q,\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":%q}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", fmt.Sprintf("gate-%d", index), current.name, string(encoded))
	}))
	defer providerServer.Close()
	config := modelAttributionConfig(providerServer.URL, "capability-gate-model")
	config.Capabilities = settings
	home, directory := test.TempDir(), filepath.Join(test.TempDir(), "sessions")
	owner, err = NewWithSessionDir("capability-gate", config, home, directory)
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
	owner.SetCollaboration(store, "capability-gate-fixture")
	runtime.SetRuntimeAuthority(sessionAutonomyAuthority{store: store})
	owner.SetYolo(true)
	sender := newMockEventSender()
	orchestrator, err := owner.NewOrchestrator(sender)
	if err != nil {
		test.Fatal(err)
	}
	if !orchestrator.SubmitInput(provider.PlainInput("Use the gate fixture")) {
		test.Fatal("task was not admitted")
	}
	offered := func(body map[string]any) bool {
		tools, _ := body["tools"].([]any)
		for _, raw := range tools {
			definition, _ := raw.(map[string]any)
			function, _ := definition["function"].(map[string]any)
			if function["name"] == tool.Name {
				return true
			}
		}
		return false
	}
	var wire []string
	for index := 0; index <= len(steps); index++ {
		body := waitCapturedRequest(test, requests)
		encoded, _ := json.Marshal(body)
		wire = append(wire, string(encoded))
		switch index {
		case 2:
			if !offered(body) {
				test.Fatalf("granted tool was not offered before the switch:\n%s", encoded)
			}
		case 3, 4:
			if offered(body) {
				test.Fatalf("request %d still offered a tool the session turned off", index+1)
			}
			if !strings.Contains(string(encoded), "turned off for this session") {
				test.Fatalf("request %d did not carry the session refusal:\n%s", index+1, encoded)
			}
			if calls.Load() != 0 {
				test.Fatal("a session-disabled MCP tool executed")
			}
		case 5:
			if !offered(body) {
				test.Fatal("switching the server back on did not restore its tool")
			}
		}
	}
	waitTurnDone(test, sender, orchestrator.ID)
	if calls.Load() != 1 {
		test.Fatalf("tool calls = %d, want exactly the re-enabled call\n%s", calls.Load(), strings.Join(wire, "\n"))
	}
	if runtime.Snapshot().Settings.Connections["task"].Disabled {
		test.Fatal("session switch changed the registration")
	}

	// The gate is session state: it persists, survives resume and is
	// projected into the settings snapshot shells install.
	snapshot, err := owner.SetCapabilityEnabled("task", false)
	if err != nil || !slices.Equal(snapshot.Effective.SessionDisabledCapabilities, []string{"task"}) {
		test.Fatalf("snapshot gate = %v, %v", snapshot.Effective.SessionDisabledCapabilities, err)
	}
	if again, err := owner.SetCapabilityEnabled("task", false); err != nil || again.Revision != snapshot.Revision {
		test.Fatal("idempotent switch published a new revision", err)
	}
	if persisted, err := sessiondb.LoadDisabledCapabilities(owner.db, owner.SessionID()); err != nil || !slices.Equal(persisted, []string{"task"}) {
		test.Fatalf("persisted gate = %v, %v", persisted, err)
	}
	path := filepath.Join(directory, owner.SessionID()+".db")
	owner.Close()
	resumed, err := ResumeSessionWithDir(path, config, home, directory)
	if err != nil {
		test.Fatal(err)
	}
	defer resumed.Close()
	if !slices.Equal(resumed.DisabledCapabilities(), []string{"task"}) || !slices.Equal(resumed.SettingsSnapshot().SessionDisabledCapabilities, []string{"task"}) {
		test.Fatal("resume lost the session capability gate", resumed.DisabledCapabilities())
	}
}
