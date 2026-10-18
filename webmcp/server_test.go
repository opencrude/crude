package webmcp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"crude/mcpclient"
	"crude/tools"
	captypes "crude/types/capability"
)

func TestDescriptorListsExactlyTheServedTools(t *testing.T) {
	descriptor, ok := captypes.Builtin(captypes.WebConnectionID)
	if !ok || !slices.Equal(descriptor.Tools, ToolNames()) {
		t.Fatalf("descriptor tools=%v served=%v", descriptor.Tools, ToolNames())
	}
	_, effects := New(nil, 1<<20)
	for _, name := range ToolNames() {
		if effects[name] != captypes.EffectRead {
			t.Fatalf("%s effect=%q", name, effects[name])
		}
	}
}

func TestBuiltinFetchUsesOrdinaryMCPPathWithPagesAndActionableFailures(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/blocked" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/markdown")
		_, _ = w.Write([]byte("# Guide\n\n" + strings.Repeat("page content\n", 20_000)))
	}))
	defer server.Close()
	mcpclient.RegisterBuiltin("web-regression", New)
	manager := mcpclient.New(t.TempDir())
	manager.SetCredentialDirectory(t.TempDir())
	t.Cleanup(manager.Close)
	manager.Configure(map[string]captypes.ConnectionConfig{"web": {ID: "web", Name: "Web", Transport: captypes.TransportBuiltin, Command: "web-regression"}})
	if err := manager.Connect(t.Context(), "web"); err != nil {
		t.Fatal(err)
	}
	var fetch captypes.Tool
	for _, tool := range manager.States()[0].Tools {
		if tool.Name == "web_fetch" {
			fetch = tool
		}
	}
	if fetch.Name == "" || !fetch.Selected {
		t.Fatal("built-in not usable without activation")
	}
	for _, offset := range []int{1, 32769} {
		result, err := manager.Call(t.Context(), fetch, map[string]any{"url": server.URL, "offset": offset})
		if err != nil || result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "of 260009") {
			t.Fatalf("page lost through ordinary MCP projection: %+v %v", result, err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("paging refetched %d times", calls.Load())
	}
	result, err := manager.Call(t.Context(), fetch, map[string]any{"url": server.URL + "/blocked"})
	if err != nil || !result.IsError || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "HTTP 403") {
		t.Fatalf("site failure lost: %+v %v", result, err)
	}
	// One tool error cannot poison the built-in connection or future calls.
	result, err = manager.Call(context.Background(), fetch, map[string]any{"url": server.URL, "offset": 260010})
	if err != nil || result.IsError || !strings.Contains(result.Content[0].Text, "past the end") {
		t.Fatalf("recovery lost: %+v %v", result, err)
	}
}

func TestBuiltinToolNamesNeverShadowNativeTools(t *testing.T) {
	for _, set := range [][]tools.ToolDef{tools.AgentTools, tools.CollaborationTools, tools.CapabilityTools, tools.ControlTools} {
		for _, definition := range set {
			if slices.Contains(ToolNames(), definition.Function.Name) {
				t.Fatalf("built-in web tool %q shadows a native tool", definition.Function.Name)
			}
		}
	}
}
