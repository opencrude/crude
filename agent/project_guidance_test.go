package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	rootprompt "crude/prompt"
	"crude/provider"
	"crude/tools"
)

func TestRunReadsWholeGuidanceBeforeFirstRequest(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", "SOUL.md"} {
		content := name + "-start\n" + strings.Repeat("Complete guidance, not a preview.\n", 2200) + name + "-end"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		var text strings.Builder
		for _, message := range request.Messages {
			text.WriteString(message.ContentString())
		}
		for _, name := range []string{"AGENTS.md", "CLAUDE.md", "SOUL.md"} {
			if !strings.Contains(text.String(), name+"-start") || !strings.Contains(text.String(), name+"-end") {
				t.Errorf("first request lacks complete %s", name)
			}
		}
		if strings.LastIndex(text.String(), "<intake_status>") < strings.LastIndex(text.String(), "SOUL.md-end") {
			t.Error("startup completion notice did not follow the supplied source content")
		}
		writeChatSSE(w, "Guidance received before project work.", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "whole-guidance-model"
	provider.SetModelContext(p.Model, 1_000_000)
	a := newTestAgent(t, p, p.Model)
	a.WorkDir = dir
	a.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "Inspect this project")},
		rootprompt.BuildRoot(rootprompt.RootOptions{WorkDir: dir, Model: p.Model}), nil, nil, "", nil)
	if calls.Load() != 1 {
		t.Fatalf("provider calls=%d want one", calls.Load())
	}
}

func TestOversizedGuidanceIsFullyCompiledBeforeBoundedNormalRequest(t *testing.T) {
	dir := t.TempDir()
	source := "first-required-rule\n" + strings.Repeat("Preserve exact source requirements and evidence.\n", 5000) + "last-required-rule"
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("README-MUST-STAY-SELECTIVE"), 0600); err != nil {
		t.Fatal(err)
	}
	var calls, compiles, normal atomic.Int32
	chunks := make(map[int]guidanceChunk)
	systems := make(map[string]string)
	chunkPattern := regexp.MustCompile(`(?s)<source_chunk_json>\n(.*?)\n</source_chunk_json>`)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.ToolChoice == "none" {
			compiles.Add(1)
			if len(request.Tools) != 0 {
				t.Error("instruction compiler received tools")
			}
			user := request.Messages[len(request.Messages)-1].ContentString()
			if strings.Contains(user, "<verify_source_chunk_json>") {
				writeChatSSE(w, "COVERED", "stop")
				return
			}
			match := chunkPattern.FindStringSubmatch(user)
			if len(match) != 2 {
				t.Error("compiler request lacks exact source chunk")
				return
			}
			var chunk guidanceChunk
			if err := json.Unmarshal([]byte(match[1]), &chunk); err != nil {
				t.Error(err)
				return
			}
			chunks[chunk.StartByte] = chunk
			writeChatSSE(w, fmt.Sprintf("- source %s bytes %d-%d: all requirements retained", chunk.SHA256, chunk.StartByte, chunk.EndByte), "stop")
			return
		}
		normal.Add(1)
		if len(request.Messages) == 0 {
			t.Error("normal request has no system prompt")
			return
		}
		system := request.Messages[0].ContentString()
		if previous, ok := systems[request.Model]; ok && previous != system {
			t.Error("unchanged compiled guidance changed after a target model switch")
		}
		systems[request.Model] = system
		if !strings.Contains(system, `mode="compiled"`) || !strings.Contains(system, "complete=\"true\"") ||
			strings.Contains(system, "README-MUST-STAY-SELECTIVE") || strings.Contains(system, "first-required-rule") || strings.Contains(system, "last-required-rule") {
			t.Errorf("normal prompt is not a bounded compiled ledger")
		}
		if tokens := provider.EstimateMessagesTotal(request.Messages, request.Model); tokens >= 16_000 {
			t.Errorf("normal request consumed unbounded context: %d", tokens)
		}
		writeChatSSE(w, "Normal work started after complete guidance intake.", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "bounded-guidance-model"
	p.SetContextWindow(16_000)
	cache := make(map[string]string)
	run := func(model string) {
		p.Model = model
		a := newTestAgent(t, p, p.Model)
		a.WorkDir = dir
		a.MaxOutputTokens = 2048
		a.GuidanceCacheLoad = func(key string) (string, bool) { value, ok := cache[key]; return value, ok }
		a.GuidanceCacheStore = func(key, value string) { cache[key] = value }
		a.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "Inspect the project")},
			rootprompt.BuildRoot(rootprompt.RootOptions{WorkDir: dir, Model: p.Model}), nil, nil, "", nil)
	}
	run("bounded-guidance-model")
	firstCompiles := compiles.Load()
	if firstCompiles < 2 || normal.Load() != 1 {
		t.Fatalf("compile calls=%d normal=%d", firstCompiles, normal.Load())
	}
	var rebuilt strings.Builder
	for start := 0; start < len(source); {
		chunk, ok := chunks[start]
		if !ok || chunk.Path != path || chunk.StartByte != start || chunk.EndByte <= start {
			t.Fatalf("source coverage breaks at byte %d: %+v", start, chunk)
		}
		rebuilt.WriteString(chunk.Content)
		start = chunk.EndByte
	}
	if rebuilt.String() != source {
		t.Fatal("compiler did not receive every guidance byte exactly once")
	}
	run("bounded-guidance-model")
	if compiles.Load() != firstCompiles || normal.Load() != 2 || calls.Load() != firstCompiles+2 {
		t.Fatalf("compiled ledger cache missed: calls=%d compiles=%d normal=%d", calls.Load(), compiles.Load(), normal.Load())
	}
	run("other-bounded-guidance-model")
	otherCompiles := compiles.Load()
	if otherCompiles <= firstCompiles {
		t.Fatal("another target model reused an incompatible compiler-cache identity")
	}
	run("bounded-guidance-model")
	if compiles.Load() != otherCompiles || normal.Load() != 4 {
		t.Fatal("returning to the original model unnecessarily recompiled unchanged guidance")
	}
}

// A batch that reaches newly governed scope defers its effects until the model
// has seen the complete guidance; pure reads in the same batch cannot violate
// it and run immediately instead of costing a round trip.
func TestRunDefersScopedEffectsButRunsReadsUntilWholeGuidanceIsVisible(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte(strings.Repeat("scoped rules\n", 900)+"scoped-final-rule"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests, reads, writes atomic.Int32
	writeCall := func(w http.ResponseWriter, id string) {
		_, _ = fmt.Fprintf(w, "{\"index\":%d,\"id\":\"%s\",\"type\":\"function\",\"function\":{\"name\":\"write_file\",\"arguments\":\"{\\\"path\\\":\\\"nested/out.txt\\\",\\\"content\\\":\\\"x\\\"}\"}}", map[bool]int{true: 1, false: 0}[id == "write-1"], id)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := requests.Add(1)
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		switch call {
		case 1:
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"read-1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"nested/file.txt\\\"}\"}},")
			writeCall(w, "write-1")
			_, _ = fmt.Fprint(w, "]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		case 2:
			if writes.Load() != 0 {
				t.Error("write executed before scoped guidance reached the model")
			}
			if reads.Load() != 1 {
				t.Errorf("read in the deferred batch ran %d times, want once", reads.Load())
			}
			var visible strings.Builder
			for _, message := range request.Messages {
				visible.WriteString(message.ContentString())
			}
			if !strings.Contains(visible.String(), "scoped-final-rule") || !strings.Contains(visible.String(), "not executed") || !strings.Contains(visible.String(), "File inspected.") {
				text := visible.String()
				t.Errorf("deferred batch did not deliver complete guidance with the read result: rule=%t deferred=%t read=%t", strings.Contains(text, "scoped-final-rule"), strings.Contains(text, "not executed"), strings.Contains(text, "File inspected."))
			}
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[")
			writeCall(w, "write-2")
			_, _ = fmt.Fprint(w, "]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
		default:
			writeChatSSE(w, "Finished after reading all applicable rules.", "stop")
		}
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "scoped-guidance-model"
	provider.SetModelContext(p.Model, 200_000)
	a := newTestAgent(t, p, p.Model)
	a.WorkDir, a.Yolo = dir, true
	a.ToolFn = func(_ context.Context, name string, _ map[string]any) (tools.ToolResult, error) {
		if name == "read_file" {
			reads.Add(1)
			return tools.ToolResult{Content: "File inspected."}, nil
		}
		writes.Add(1)
		return tools.ToolResult{Content: "Written."}, nil
	}
	a.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "Inspect and update nested files")}, "system", nil, nil, "", nil)
	if requests.Load() != 3 || reads.Load() != 1 || writes.Load() != 1 {
		t.Fatalf("requests=%d reads=%d writes=%d want one deferred write, an immediate read and one later write", requests.Load(), reads.Load(), writes.Load())
	}
}

func TestCompactorKeepsGuidanceDiscoveredDuringTheTurn(t *testing.T) {
	dir := t.TempDir()
	nested := filepath.Join(dir, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root-rule"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "AGENTS.md"), []byte("nested-rule-discovered-later"), 0600); err != nil {
		t.Fatal(err)
	}
	p := provider.FromPreset("openai-chat", "key", "http://127.0.0.1")
	p.Model = "dynamic-guidance-compaction"
	p.SetContextWindow(128_000)
	a := newTestAgent(t, p, p.Model)
	a.WorkDir = dir
	rootPrompt, err := a.loadProjectGuidance(context.Background(), "system")
	if err != nil {
		t.Fatal(err)
	}
	changed, err := a.guidance.loadPaths(context.Background(), []string{filepath.Join(nested, "file.go")})
	if err != nil || !changed {
		t.Fatalf("discover nested guidance: changed=%v err=%v", changed, err)
	}
	if _, err := a.refreshProjectGuidance(context.Background(), rootPrompt); err != nil {
		t.Fatal(err)
	}
	compactor := a.contextCompactor(128_000)
	compactionPrompt, err := compactor.loadProjectGuidance(context.Background(), rootPrompt)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(compactionPrompt, "nested-rule-discovered-later") || !strings.Contains(compactionPrompt, a.guidance.digest) ||
		strings.Count(compactionPrompt, "<loaded_project_guidance ") != 1 {
		t.Fatal("compaction replaced current scoped guidance with the boot-only set")
	}
}

func TestUnreadableGuidanceStopsBeforeProviderAndTools(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "AGENTS.md"), 0700); err != nil {
		t.Fatal(err)
	}
	var requests, executed atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		writeChatSSE(w, "Should not be requested.", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	a := newTestAgent(t, p, "blocked-guidance")
	a.WorkDir = dir
	a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
		executed.Add(1)
		return tools.ToolResult{}, nil
	}
	var events []AgentEvent
	a.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "Modify this project")}, "system", func(event AgentEvent) { events = append(events, event) }, nil, "", nil)
	if requests.Load() != 0 || executed.Load() != 0 || !hasEvent(events, "error") {
		t.Fatalf("unreadable guidance did not fail closed: requests=%d tools=%d events=%v", requests.Load(), executed.Load(), events)
	}
}

func TestUnstableCompiledGuidanceStopsBeforeNormalWork(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(strings.Repeat("A distinct mandatory rule.\n", 6000)), 0600); err != nil {
		t.Fatal(err)
	}
	var compilerCalls, normalCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if request.ToolChoice != "none" {
			normalCalls.Add(1)
			writeChatSSE(w, "Normal work must not start.", "stop")
			return
		}
		call := compilerCalls.Add(1)
		writeChatSSE(w, fmt.Sprintf("- replacement ledger revision %d", call), "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "unstable-guidance-model"
	p.SetContextWindow(16_000)
	a := newTestAgent(t, p, p.Model)
	a.WorkDir = dir
	a.MaxOutputTokens = 2048
	var events []AgentEvent
	a.Run(context.Background(), []provider.ChatMessage{provider.NewTextMessage("user", "Modify the project")}, "system",
		func(event AgentEvent) { events = append(events, event) }, nil, "", nil)
	if compilerCalls.Load() <= guidanceVerificationPasses || normalCalls.Load() != 0 || !hasEvent(events, "error") {
		t.Fatalf("unstable guidance did not fail closed: compiler=%d normal=%d events=%v", compilerCalls.Load(), normalCalls.Load(), events)
	}
}

func TestRunGuidanceRereadsPreserveCachePrefix(t *testing.T) {
	dir := t.TempDir()
	const source = "Read this file completely before project work."
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	requests, executed := 0, 0
	var system string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		if requests == 1 {
			system = request.Messages[0].ContentString()
		} else if request.Messages[0].ContentString() != system {
			t.Error("unchanged guidance reread rewrote the cache prefix")
		}
		projections := 0
		for _, message := range request.Messages {
			projections += strings.Count(message.ContentString(), "<loaded_project_guidance ")
		}
		if projections != 1 {
			t.Errorf("unchanged reread republished guidance: projections=%d", projections)
		}
		if requests <= 2 {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"read-%d\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"AGENTS.md\\\",\\\"encoded\\\":true}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", requests)
		} else {
			writeChatSSE(w, "Source inspected.", "stop")
		}
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "reread-guidance-model"
	a := newTestAgent(t, p, p.Model)
	a.WorkDir = dir
	a.ToolFn = func(_ context.Context, name string, params map[string]any) (tools.ToolResult, error) {
		executed++
		if name != "read_file" || params["encoded"] != true {
			t.Error("explicit hashline read was altered")
		}
		return tools.ToolResult{Content: source}, nil
	}
	a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Inspect the exact guidance source")},
		rootprompt.BuildRoot(rootprompt.RootOptions{WorkDir: dir}), nil, nil, "", nil)
	if requests != 3 || executed != 2 {
		t.Fatalf("legitimate reread was blocked: requests=%d executed=%d", requests, executed)
	}
}

func TestRunGuidanceMutationsPublishOneUpdateAfterExecution(t *testing.T) {
	for _, action := range []string{"create", "edit", "delete"} {
		t.Run(action, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "AGENTS.md")
			if action != "create" {
				if err := os.WriteFile(path, []byte("original-rule"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			requests, executed := 0, 0
			var initialSystem string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				var request provider.ChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				if requests == 1 {
					initialSystem = request.Messages[0].ContentString()
					name := "write_file"
					if action == "delete" {
						name = "delete_file"
					}
					w.Header().Set("Content-Type", "text/event-stream")
					_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"mutate\",\"type\":\"function\",\"function\":{\"name\":%q,\"arguments\":\"{\\\"path\\\":\\\"AGENTS.md\\\",\\\"content\\\":\\\"current-rule\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", name)
					return
				}
				if executed != 1 || request.Messages[0].ContentString() != initialSystem {
					t.Error("guidance mutation was deferred unnecessarily or rewrote accepted prefix")
				}
				updates := 0
				for _, message := range request.Messages[1:] {
					if strings.Contains(message.ContentString(), "<loaded_project_guidance ") {
						updates++
						if strings.Contains(message.ContentString(), "original-rule") ||
							(action != "delete" && !strings.Contains(message.ContentString(), "current-rule")) ||
							(action == "delete" && strings.Contains(message.ContentString(), "<file ")) {
							t.Error("next request did not receive the exact current guidance")
						}
					}
				}
				if updates != 1 {
					t.Errorf("guidance updates=%d want one", updates)
				}
				writeChatSSE(w, "Guidance changed.", "stop")
			}))
			defer server.Close()
			p := provider.FromPreset("openai-chat", "key", server.URL)
			p.Model = "mutate-guidance-model"
			a := newTestAgent(t, p, p.Model)
			a.WorkDir, a.Yolo = dir, true
			a.ToolFn = func(context.Context, string, map[string]any) (tools.ToolResult, error) {
				executed++
				var err error
				if action == "delete" {
					err = os.Remove(path)
				} else {
					err = os.WriteFile(path, []byte("current-rule"), 0600)
				}
				return tools.ToolResult{Content: "Mutation completed."}, err
			}
			history := a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "Change project guidance")},
				rootprompt.BuildRoot(rootprompt.RootOptions{WorkDir: dir}), nil, nil, "", nil)
			for _, message := range history[1:] {
				if strings.Contains(message.ContentString(), "<loaded_project_guidance ") &&
					(message.EffectiveOrigin() != provider.OriginRuntimeContext || message.EffectiveVisibility() != provider.VisibilityContextOnly) {
					t.Error("guidance update was attributed as human input or visible conversation")
				}
			}
			if requests != 2 || executed != 1 {
				t.Fatalf("requests=%d executed=%d", requests, executed)
			}
		})
	}
}

func TestGuidanceCallPathsIncludeNativeAndParallelPatches(t *testing.T) {
	const patch = "*** Begin Patch\n*** Update File: nested/AGENTS.md\n*** Move to: nested/CLAUDE.md\n@@\n-old\n+new\n*** End Patch"
	args, err := json.Marshal(map[string]any{"tool_uses": []any{map[string]any{"recipient_name": "functions.apply_patch", "parameters": map[string]any{"input": patch}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range []provider.ToolCall{
		{Type: "custom", Function: provider.FunctionCall{Name: "apply_patch", Arguments: patch}},
		{Type: "function", Function: provider.FunctionCall{Name: "multi_tool_use.parallel", Arguments: string(args)}},
	} {
		paths := guidanceCallPaths([]provider.ToolCall{call})
		if strings.Join(paths, ",") != "nested/AGENTS.md,nested/CLAUDE.md" {
			t.Fatalf("patch operands missing from guidance preflight: %v", paths)
		}
	}
}

// A glob operand contributes its literal directory prefix as a guidance
// scope. It must not fail the batch: one sibling's operand once turned every
// valid read in a parallel batch into the same wildcard error.
func TestGuidanceCallPathsUseGlobLiteralPrefix(t *testing.T) {
	calls := []provider.ToolCall{
		{Function: provider.FunctionCall{Name: "grep_files", Arguments: `{"pattern":"needle","path":"tui/ui/text*"}`}},
		{Function: provider.FunctionCall{Name: "read_file", Arguments: `{"path":"valid.go"}`}},
		{Function: provider.FunctionCall{Name: "grep_files", Arguments: `{"pattern":"x","path":"nested/**/*_test.go"}`}},
	}
	if got := strings.Join(guidanceCallPaths(calls), ","); got != "tui/ui,valid.go,nested" {
		t.Fatalf("glob operands did not reduce to literal prefixes: %s", got)
	}
}

func TestGuidanceCallPathsTreatsGlobPatternSeparatelyFromConcreteBase(t *testing.T) {
	paths := guidanceCallPaths([]provider.ToolCall{{Function: provider.FunctionCall{Name: "glob_files", Arguments: `{"pattern":"**/*.go","base_dir":"tui/ui"}`}}})
	if strings.Join(paths, ",") != "tui/ui" {
		t.Fatalf("glob pattern leaked into guidance scopes: %v", paths)
	}
}

func TestRunGlobOperandExecutesEveryCallInBatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("root rule"), 0600); err != nil {
		t.Fatal(err)
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := requests.Add(1)
		if call == 1 {
			_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"glob-path\",\"type\":\"function\",\"function\":{\"name\":\"grep_files\",\"arguments\":\"{\\\"pattern\\\":\\\"needle\\\",\\\"path\\\":\\\"tui/ui/text*\\\"}\"}},{\"index\":1,\"id\":\"sibling\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"AGENTS.md\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n")
			return
		}
		var request provider.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		joined := ""
		for _, message := range request.Messages {
			joined += message.ContentString()
		}
		if strings.Count(joined, "executed ") != 2 {
			t.Errorf("both calls should have executed: %s", joined)
		}
		writeChatSSE(w, "Recovered without parking.", "stop")
	}))
	defer server.Close()
	p := provider.FromPreset("openai-chat", "key", server.URL)
	p.Model = "guidance-path-model"
	a := newTestAgent(t, p, p.Model)
	a.WorkDir = dir
	expectedSet, err := rootprompt.DiscoverGuidance(t.Context(), dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	expectedDigest := expectedSet.Digest()
	var executed atomic.Int32
	a.ToolFn = func(_ context.Context, name string, _ map[string]any) (tools.ToolResult, error) {
		executed.Add(1)
		return tools.ToolResult{Content: "executed " + name}, nil
	}
	events := make([]AgentEvent, 0)
	history := a.Run(t.Context(), []provider.ChatMessage{provider.NewTextMessage("user", "search")}, rootprompt.BuildRoot(rootprompt.RootOptions{WorkDir: dir}), func(event AgentEvent) {
		events = append(events, event)
	}, nil, "", nil)
	if requests.Load() != 2 || executed.Load() != 2 || a.RunFailure() != nil {
		t.Fatalf("requests=%d executed=%d failure=%v history=%v", requests.Load(), executed.Load(), a.RunFailure(), history)
	}
	for _, event := range events {
		if event.Type == "recovery" {
			t.Fatalf("glob operand parked the agent: %+v", event)
		}
	}
	if a.guidance == nil || a.guidance.digest != expectedDigest {
		t.Fatal("glob operand changed accepted guidance snapshot")
	}
}

func TestGuidanceCallPathsReduceNativeAndParallelGlobOperands(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: tui/ui/text*\n@@\n-old\n+new\n*** End Patch"
	parallel, err := json.Marshal(map[string]any{"tool_uses": []any{
		map[string]any{"recipient_name": "functions.read_file", "parameters": map[string]any{"path": "valid.go"}},
		map[string]any{"recipient_name": "functions.grep_files", "parameters": map[string]any{"pattern": "x", "path": "tui/ui/text*"}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	for call, want := range map[*provider.ToolCall]string{
		{Type: "custom", Function: provider.FunctionCall{Name: "apply_patch", Arguments: patch}}:                          "tui/ui",
		{Type: "function", Function: provider.FunctionCall{Name: "multi_tool_use.parallel", Arguments: string(parallel)}}: "valid.go,tui/ui",
	} {
		if got := strings.Join(guidanceCallPaths([]provider.ToolCall{*call}), ","); got != want {
			t.Fatalf("call=%s paths=%s want %s", call.Function.Name, got, want)
		}
	}
}
