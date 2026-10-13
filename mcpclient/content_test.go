package mcpclient

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	captypes "crude/types/capability"
)

func TestMixedContentThroughSDK(t *testing.T) {
	image, audio, blob := []byte{0, 1, 255}, []byte{2, 3, 254}, []byte{4, 5, 253}
	server := mcp.NewServer(&mcp.Implementation{Name: "mixed", Version: "1"}, nil)
	server.AddTool(&mcp.Tool{Name: "mixed", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return &mcp.CallToolResult{IsError: true, StructuredContent: map[string]any{"credential": "fixture-secret", "count": 2}, Content: []mcp.Content{
			&mcp.TextContent{Text: "first fixture-secret"},
			&mcp.ImageContent{Data: image, MIMEType: "image/png"},
			&mcp.AudioContent{Data: audio, MIMEType: "audio/wav"},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "fixture://embedded", MIMEType: "application/octet-stream", Blob: blob}},
			&mcp.EmbeddedResource{Resource: &mcp.ResourceContents{URI: "fixture://text", MIMEType: "text/plain", Text: "resource fixture-secret"}},
			&mcp.ResourceLink{URI: "https://unfetched.invalid/resource", Name: "linked", Title: "Reference", Description: "fixture-secret", MIMEType: "text/plain"},
		}}, nil
	})
	server.AddPrompt(&mcp.Prompt{Name: "media"}, func(context.Context, *mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
		return &mcp.GetPromptResult{Messages: []*mcp.PromptMessage{
			{Role: "user", Content: &mcp.ImageContent{Data: image, MIMEType: "image/png"}},
			{Role: "assistant", Content: &mcp.TextContent{Text: "external fixture-secret"}},
			{Role: "user", Content: &mcp.AudioContent{Data: audio, MIMEType: "audio/wav"}},
		}}, nil
	})
	server.AddResource(&mcp.Resource{URI: "fixture://read", Name: "read"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: []*mcp.ResourceContents{
			{URI: "fixture://read", MIMEType: "audio/wav", Blob: audio},
			{URI: "fixture://reference-only", MIMEType: "text/plain"},
		}}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(endpoint.Close)
	manager := New(t.TempDir())
	t.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "mixed", Name: "Mixed", Transport: "http", URL: endpoint.URL, Headers: map[string]string{"X-Fixture": "fixture-secret"}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(t.Context(), config.ID); err != nil {
		t.Fatal(err)
	}
	tool := manager.States()[0].Tools[0]
	config.Grants = map[string]captypes.ToolGrant{"mixed": {Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: tool.Fingerprint}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(t.Context(), config.ID); err != nil {
		t.Fatal(err)
	}
	result, err := manager.Call(t.Context(), manager.States()[0].Tools[0], nil)
	if err != nil || !result.IsError || result.Unknown || len(result.Content) != 6 {
		t.Fatalf("mixed result: %#v %v", result, err)
	}
	wantTypes := []string{"text", "image", "audio", "resource", "resource", "resource_link"}
	for index, want := range wantTypes {
		if result.Content[index].Type != want {
			t.Fatalf("block %d: %#v", index, result.Content[index])
		}
	}
	if result.Content[0].Text != "first [redacted]" || result.Content[1].Data != base64.StdEncoding.EncodeToString(image) || result.Content[2].Data != base64.StdEncoding.EncodeToString(audio) || result.Content[3].Data != base64.StdEncoding.EncodeToString(blob) || result.Content[4].Text != "resource [redacted]" {
		t.Fatalf("mixed bytes/text lost: %#v", result)
	}
	link := result.Content[5]
	if link.URI != "https://unfetched.invalid/resource" || link.Data != "" || !strings.Contains(link.Text, `"mcp_connection_id":"mixed"`) || !strings.Contains(link.Text, "[redacted]") {
		t.Fatalf("unfetched source reference: %#v", link)
	}
	if !json.Valid(result.Structured) || strings.Contains(string(result.Structured), "fixture-secret") || !strings.Contains(string(result.Structured), `"count":2`) {
		t.Fatalf("structured data/redaction: %s", result.Structured)
	}
	prompt, err := manager.GetPrompt(t.Context(), config.ID, "media", nil)
	if err != nil || len(prompt.Content) != 3 || prompt.Content[0].Type != "image" || prompt.Content[0].Data != base64.StdEncoding.EncodeToString(image) || prompt.Content[1].Text != "external [redacted]" || prompt.Content[2].Type != "audio" {
		t.Fatalf("prompt media: %#v %v", prompt, err)
	}
	var attribution promptAttribution
	if err := json.Unmarshal(prompt.Structured, &attribution); err != nil || attribution.ConnectionID != "mixed" || attribution.Name != "media" || len(attribution.Messages) != 3 || attribution.Messages[0].Role != "user" || attribution.Messages[1].Role != "assistant" || attribution.Messages[2].ContentIndex != 2 {
		t.Fatalf("external source roles: %s %v", prompt.Structured, err)
	}
	resource, err := manager.ReadResource(t.Context(), config.ID, "fixture://read")
	if err != nil || len(resource.Content) != 2 || resource.Content[0].Data != base64.StdEncoding.EncodeToString(audio) || resource.Content[1].URI != "fixture://reference-only" || resource.Content[1].Data != "" {
		t.Fatalf("resource bytes/reference: %#v %v", resource, err)
	}
}

func TestContentProjectionBoundsAndMalformed(t *testing.T) {
	manager := New(t.TempDir())
	t.Cleanup(manager.Close)
	config := captypes.ConnectionConfig{ID: "fixture", Headers: map[string]string{"X-Secret": "x"}}
	for _, test := range []struct {
		name string
		item mcp.Content
	}{
		{"nil", nil},
		{"typed nil", (*mcp.ImageContent)(nil)},
		{"nil embedded resource", &mcp.EmbeddedResource{}},
		{"oversized image", &mcp.ImageContent{Data: make([]byte, maxResultBytes)}},
		{"oversized audio", &mcp.AudioContent{Data: make([]byte, maxResultBytes)}},
		{"oversized blob", &mcp.EmbeddedResource{Resource: &mcp.ResourceContents{Blob: make([]byte, maxResultBytes)}}},
		{"escaped text", &mcp.TextContent{Text: strings.Repeat("\x00", maxResultBytes/3)}},
		{"redaction expansion", &mcp.TextContent{Text: strings.Repeat("x", maxResultBytes/3)}},
	} {
		t.Run(test.name, func(t *testing.T) {
			projection := newContentProjection(manager, config)
			if err := projection.appendContent(test.item); err == nil || len(projection.result.Content) != 0 {
				t.Fatalf("malformed/oversized block admitted: %v", err)
			}
		})
	}
	projection := newContentProjection(manager, captypes.ConnectionConfig{ID: "fixture"})
	//nolint:staticcheck // Negative fixture deliberately exercises a deprecated optional content type without advertising its feature.
	if err := projection.appendContent(&mcp.ToolResultContent{Content: []mcp.Content{&mcp.ImageContent{Data: []byte("hidden payload")}}}); err != nil {
		t.Fatal(err)
	}
	if projection.result.Content[0].Type != "unsupported" || strings.Contains(projection.result.Content[0].Text, "payload") || projection.result.Content[0].Data != "" {
		t.Fatalf("unsupported nested content leaked: %#v", projection.result)
	}
	if err := projection.structured(json.RawMessage(`{"broken":`)); err == nil {
		t.Fatal("malformed structured content admitted")
	}
	if err := projection.structured(map[string]any{"oversized": strings.Repeat("z", maxResultBytes)}); !errors.Is(err, errContentBudget) {
		t.Fatalf("oversized structured content: %v", err)
	}
	for index := 0; index < 3; index++ {
		err := projection.appendContent(&mcp.ImageContent{Data: make([]byte, maxResultBytes/3), MIMEType: "image/png"})
		if index < 2 && err != nil || index == 2 && !errors.Is(err, errContentBudget) {
			t.Fatalf("aggregate bound at block %d: %v", index, err)
		}
	}
	result, err := projection.finish()
	encoded, marshalErr := json.Marshal(result)
	if err != nil || marshalErr != nil || len(encoded) > maxResultBytes {
		t.Fatalf("aggregate result exceeds encoded budget: %d %v %v", len(encoded), err, marshalErr)
	}
}

func TestSDKRejectsMalformedContentWire(t *testing.T) {
	for _, wire := range []string{
		`{"content":[{"type":"image","mimeType":"image/png","data":"not base64!"}]}`,
		`{"content":[{"type":"audio","mimeType":"audio/wav","data":"not base64!"}]}`,
		`{"content":[{"type":"resource","resource":{"uri":"fixture://blob","blob":"not base64!"}}]}`,
		`{"content":[{"type":"future_media","data":"aGlkZGVu"}]}`,
		`{"content":[null]}`,
	} {
		var result mcp.CallToolResult
		if err := json.Unmarshal([]byte(wire), &result); err == nil {
			t.Fatalf("SDK admitted malformed/unknown wire: %s", wire)
		}
	}
}

func TestStructuredRedactionPreservesJSONAndProvenance(t *testing.T) {
	manager := New(t.TempDir())
	defer manager.Close()
	config := captypes.ConnectionConfig{ID: "source1", Environment: map[string]string{"NUMBER": "1", "SECRET": "a\"b\n"}}
	for _, wire := range []string{`{"count":1,"other":2,"nested":["a\"b\n",true,null],"key1":"value"}`, `"a\"b\n"`, `1`, `true`, `null`} {
		p := newContentProjection(manager, config)
		if err := p.structured(json.RawMessage(wire)); err != nil {
			t.Fatalf("%s: %v", wire, err)
		}
		if !json.Valid(p.result.Structured) || strings.Contains(string(p.result.Structured), `a\"b`) {
			t.Fatalf("invalid/leaked JSON: %s", p.result.Structured)
		}
		if wire == `1` && string(p.result.Structured) != `"[redacted]"` {
			t.Fatalf("numeric credential: %s", p.result.Structured)
		}
	}
	p := newContentProjection(manager, config)
	if err := p.structured(map[string]any{"x1": "first", "x[redacted]": "second"}); err == nil {
		t.Fatal("colliding external keys silently overwritten")
	}
	if err := p.structured(promptAttribution{ConnectionID: "source1", Name: "prompt1", Messages: []promptMessageAttribution{{Role: "assistant", ContentIndex: 1}}}); err != nil {
		t.Fatal(err)
	}
	var attribution promptAttribution
	if err := json.Unmarshal(p.result.Structured, &attribution); err != nil || attribution.ConnectionID != "source1" || attribution.Messages[0].ContentIndex != 1 || attribution.Messages[0].Role != "assistant" || attribution.Name != "prompt[redacted]" {
		t.Fatalf("corrupted provenance: %s %v", p.result.Structured, err)
	}
	link := newContentProjection(manager, config)
	if err := link.appendContent(&mcp.ResourceLink{Name: "name1", Size: new(int64), MIMEType: "text/plain"}); err != nil {
		t.Fatal(err)
	}
	if !json.Valid([]byte(link.result.Content[0].Text)) || !strings.Contains(link.result.Content[0].Text, `"mcp_connection_id":"source1"`) || link.result.Content[0].MIMEType != "text/plain" {
		t.Fatalf("link provenance corrupted: %#v", link.result)
	}
}

func TestResourceBlobTextRedactionThroughSDK(t *testing.T) {
	secret := "blob-secret"
	server := mcp.NewServer(&mcp.Implementation{Name: "blobs", Version: "1"}, nil)
	resources := []*mcp.ResourceContents{
		{URI: "fixture://plain", MIMEType: "text/plain; charset=utf-8", Blob: []byte("hello " + secret)},
		{URI: "fixture://json", MIMEType: "application/vnd.fixture+json; charset=utf-8", Blob: []byte(`{"nested":["blob-secret"]}`)},
		{URI: "fixture://binary", MIMEType: "image/png", Blob: []byte(secret)},
		{URI: "fixture://audio", MIMEType: "audio/wav", Blob: []byte(secret)},
	}
	server.AddResource(&mcp.Resource{URI: "fixture://read", Name: "blobs"}, func(context.Context, *mcp.ReadResourceRequest) (*mcp.ReadResourceResult, error) {
		return &mcp.ReadResourceResult{Contents: resources}, nil
	})
	server.AddTool(&mcp.Tool{Name: "blobs", InputSchema: map[string]any{"type": "object"}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var content []mcp.Content
		for _, resource := range resources {
			content = append(content, &mcp.EmbeddedResource{Resource: resource})
		}
		return &mcp.CallToolResult{Content: content}, nil
	})
	endpoint := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true}))
	defer endpoint.Close()
	manager := New(t.TempDir())
	defer manager.Close()
	config := captypes.ConnectionConfig{ID: "blobs", Name: "Blobs", Transport: "http", URL: endpoint.URL, Headers: map[string]string{"X-Secret": secret}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(t.Context(), config.ID); err != nil {
		t.Fatal(err)
	}
	tool := manager.States()[0].Tools[0]
	config.Grants = map[string]captypes.ToolGrant{"blobs": {Effect: captypes.EffectRead, Scope: "workspace", Fingerprint: tool.Fingerprint}}
	manager.Configure(map[string]captypes.ConnectionConfig{config.ID: config})
	if err := manager.Connect(t.Context(), config.ID); err != nil {
		t.Fatal(err)
	}
	read, err := manager.ReadResource(t.Context(), config.ID, "fixture://read")
	if err != nil {
		t.Fatal(err)
	}
	call, err := manager.Call(t.Context(), manager.States()[0].Tools[0], nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []captypes.Result{read, call} {
		if len(result.Content) != 4 {
			t.Fatalf("lost resources: %#v", result)
		}
		for i, content := range result.Content {
			data, err := base64.StdEncoding.DecodeString(content.Data)
			if err != nil {
				t.Fatal(err)
			}
			if i < 2 && (strings.Contains(string(data), secret) || !strings.Contains(string(data), "[redacted]")) || i >= 2 && string(data) != secret {
				t.Fatalf("blob %d: %s", i, data)
			}
			if i == 1 && !json.Valid(data) {
				t.Fatalf("invalid JSON blob: %s", data)
			}
		}
	}
	p := newContentProjection(manager, config)
	if err := p.appendResource(&mcp.ResourceContents{MIMEType: "text/plain", Blob: []byte{255}}); err == nil {
		t.Fatal("invalid UTF8 text admitted")
	}
}
