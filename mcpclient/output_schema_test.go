package mcpclient

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	captypes "crude/types/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDeclaredOutputSchemaAcknowledgedEvidence(t *testing.T) {
	for _, test := range []struct {
		name  string
		value any
		valid bool
	}{
		{"valid", map[string]any{"count": 2}, true},
		{"wrong type", map[string]any{"count": "secret-output"}, false},
		{"missing required", map[string]any{}, false},
		{"missing structured", nil, false},
		{"oversized", map[string]any{"count": strings.Repeat("z", maxResultBytes)}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := new(atomic.Int32)
			server := mcp.NewServer(&mcp.Implementation{Name: "schema", Version: "1"}, nil)
			server.AddTool(&mcp.Tool{Name: "write", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object", "required": []string{"count"}, "properties": map[string]any{"count": map[string]any{"type": "integer"}}}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
				calls.Add(1)
				return &mcp.CallToolResult{StructuredContent: test.value, Content: []mcp.Content{&mcp.TextContent{Text: "mutation ACK"}, &mcp.ImageContent{Data: []byte{1, 2}, MIMEType: "image/png"}}}, nil
			})
			endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
			defer endpoint.Close()
			manager := New(t.TempDir())
			defer manager.Close()
			config := captypes.ConnectionConfig{ID: "schema", Name: "Schema", Transport: "http", URL: endpoint.URL}
			manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
			if err := manager.Connect(t.Context(), config.ID); err != nil {
				t.Fatal(err)
			}
			tool := manager.States()[0].Tools[0]
			if len(tool.OutputSchema) == 0 {
				t.Fatal("output metadata missing")
			}
			config.Grants = map[string]captypes.ToolGrant{"write": {Fingerprint: tool.Fingerprint, Effect: captypes.EffectWrite, Scope: "fixture"}}
			manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
			if err := manager.Connect(t.Context(), config.ID); err != nil {
				t.Fatal(err)
			}
			result, err := manager.Call(t.Context(), manager.States()[0].Tools[0], nil)
			// Oversized HTTP bodies are transport failures, before the SDK exposes
			// an ACK. Local schema failures must retain media and forbid replay.
			if test.name == "oversized" {
				if err == nil || calls.Load() != 1 {
					t.Fatalf("oversized response admitted: %v", err)
				}
				return
			}
			if (err == nil) != test.valid || result.Unknown || calls.Load() != 1 || len(result.Content) < 2 || result.Content[0].Text != "mutation ACK" || result.Content[1].Type != "image" {
				t.Fatalf("ACK/evidence lost: %#v %v calls=%d", result, err, calls.Load())
			}
			if !test.valid && (!strings.Contains(err.Error(), "LOCAL") || strings.Contains(err.Error(), "secret-output")) {
				t.Fatalf("unsafe/nonlocal error: %v", err)
			}
		})
	}
}

func TestOutputOnlyChangeRevokesGrant(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "schema", Version: "1"}, nil)
	add := func(kind string) {
		server.AddTool(&mcp.Tool{Name: "write", InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": kind}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			return &mcp.CallToolResult{}, nil
		})
	}
	add("object")
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer endpoint.Close()
	manager := New(t.TempDir())
	defer manager.Close()
	config := captypes.ConnectionConfig{ID: "schema", Name: "Schema", Transport: "http", URL: endpoint.URL}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(t.Context(), config.ID); err != nil {
		t.Fatal(err)
	}
	old := manager.States()[0].Tools[0]
	config.Grants = map[string]captypes.ToolGrant{"write": {Fingerprint: old.Fingerprint, Effect: captypes.EffectWrite, Scope: "fixture"}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(t.Context(), config.ID); err != nil {
		t.Fatal(err)
	}
	old = manager.States()[0].Tools[0]
	add("array")
	if err := manager.Refresh(t.Context(), config.ID); err != nil {
		t.Fatal(err)
	}
	next := manager.States()[0].Tools[0]
	if next.Fingerprint == old.Fingerprint || next.Selected {
		t.Fatal("output-only change retained grant")
	}
	if _, err := manager.Call(t.Context(), old, nil); err == nil {
		t.Fatal("stale output grant executed")
	}
}

func TestOutputSchemaResolverAndLocalMalformed(t *testing.T) {
	for _, wire := range []string{`{"type":17}`, `{"$ref":"https://unfetched.invalid/schema"}`} {
		if _, err := resolveOutputSchema([]byte(wire)); err == nil {
			t.Fatalf("invalid/external schema admitted: %s", wire)
		}
	}
	schema, err := resolveOutputSchema([]byte(`{"type":"object"}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{json.RawMessage(`{"broken":`), map[string]any{"big": strings.Repeat("z", maxResultBytes)}} {
		if err := validateStructuredOutput(schema, value); err == nil {
			t.Fatal("invalid/oversized structured admitted")
		}
	}
}
