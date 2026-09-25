package agent

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"crude/provider"
	"crude/tools"
)

func TestInterruptedPatchNeverExecutesEvenWithCompleteSyntax(t *testing.T) {
	const patch = "*** Begin Patch\n*** Add File: must-not-exist\n+x\n*** End Patch"
	for _, protocol := range []string{"openai", "openai-chat"} {
		t.Run(protocol, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				if protocol == "openai" {
					_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"custom_tool_call\",\"call_id\":\"p\",\"name\":\"apply_patch\",\"input\":%q}}\n\ndata: [DONE]\n\n", patch)
				} else {
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"p\",\"type\":\"custom\",\"custom\":{\"name\":\"apply_patch\",\"input\":%q}}]}}]}\n\ndata: [DONE]\n\n", patch)
				}
			}))
			defer server.Close()
			p := provider.FromPreset(protocol, "key", server.URL)
			p.Model = "gpt-test"
			a := newTestAgent(t, p, p.Model)
			a.Yolo = true
			calls := 0
			a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				calls++
				return tools.ToolResult{}, nil
			}
			// Test the real stream boundary and execution loop. A complete-looking
			// patch in an interrupted response must not reach ToolFn.
			if _, err := completeForTest(t, a); err == nil {
				t.Fatal("unfinished stream accepted")
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			a.Run(ctx, nil, "system", func(event AgentEvent) {
				if event.Type == "continuation" || event.Type == "error" {
					cancel()
				}
			}, nil, "", nil)
			if calls != 0 {
				t.Fatal("incomplete patch executed")
			}
		})
	}
}
