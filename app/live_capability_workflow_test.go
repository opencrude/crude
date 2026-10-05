package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/session"
	"crude/skills"
	ttypes "crude/types"
	captypes "crude/types/capability"
	"crude/workspace"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	liveSkillMarker = "LIVE_SKILL_EVIDENCE"
	liveMCPMarker   = "LIVE_MCP_EVIDENCE"
)

// TestLiveCapabilityWorkflow is deliberately opt-in: it uses the configured
// main model and proves the complete model-driven skill/MCP workflow rather
// than substituting a scripted provider.
func TestLiveCapabilityWorkflow(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_CAPABILITY_WORKFLOW") != "1" {
		t.Skip("CRUDE_LIVE_CAPABILITY_WORKFLOW=1 not set")
	}

	// Credentials must be loaded from the real user config before the process
	// home is isolated. They remain in memory and are never copied to evidence.
	fileConfig := LoadFileConfig()
	cfg := FileConfigToSession(fileConfig)
	main := cfg.GetActiveDefaultCategory()
	if main.AccountID == "" || main.Model == "" {
		t.Skip("main agent account and model are not configured")
	}
	if _, err := cfg.ProviderForAccount(main.AccountID, main.Model); err != nil {
		t.Skipf("configured main agent is unavailable: %v", err)
	}

	proofDir := liveCapabilityProofDir(t)
	home := filepath.Join(proofDir, "home")
	project := filepath.Join(proofDir, "project")
	catalog := filepath.Join(proofDir, "skill-catalog")
	for _, path := range []string{home, project, catalog} {
		if err := os.MkdirAll(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	skillDir := filepath.Join(catalog, "live-evidence")
	if _, err := skills.Create(skillDir, "live-evidence",
		"Read the bundled file evidence.txt with the native read_file tool. Report its exact marker; do not infer or invent it."); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "evidence.txt"), []byte(liveSkillMarker+"\n"), 0600); err != nil {
		t.Fatal(err)
	}

	var sdkCalls, mcpRequests, registryRequests atomic.Int32
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "Crude live capability fixture", Version: "1"}, nil)
	mcpServer.AddTool(&mcp.Tool{
		Name: "lookup", Description: "Read the exact public fixture evidence",
		InputSchema: map[string]any{"type": "object", "additionalProperties": false},
	}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		sdkCalls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: liveMCPMarker}}}, nil
	})
	mcpHandler := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{Stateless: true},
	)
	mcpEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mcpRequests.Add(1)
		mcpHandler.ServeHTTP(w, r)
	}))
	t.Cleanup(mcpEndpoint.Close)

	const registrySourceID = "live-mcp-registry"
	const registryServerName = "crude/live-evidence"
	registryEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		registryRequests.Add(1)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"servers": []any{map[string]any{
				"server": map[string]any{
					"name": registryServerName, "title": "Live evidence MCP", "version": "1.0.0",
					"description": "Exact local read-only live acceptance fixture",
					"remotes":     []any{map[string]any{"type": "streamable-http", "url": mcpEndpoint.URL}},
				},
				"_meta": map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{
					"status": "active", "isLatest": true,
				}},
			}},
		})
	}))
	t.Cleanup(registryEndpoint.Close)

	sources := captypes.DefaultSources()
	for id, source := range sources {
		source.Disabled = true
		sources[id] = source
	}
	sources["live-skill"] = captypes.SourceConfig{
		ID: "live-skill", Name: "Live local skill fixture", Kind: "local",
		Location: catalog, Trusted: true, Scope: captypes.ScopeWorkspace,
	}
	sources[registrySourceID] = captypes.SourceConfig{
		ID: registrySourceID, Name: "Live local MCP registry fixture", Kind: "registry",
		Location: registryEndpoint.URL, Trusted: true, Scope: captypes.ScopeWorkspace,
	}
	capabilitySettings := captypes.Normalize(captypes.Settings{
		Sources: sources,
		Acquisition: captypes.AcquisitionSettings{
			Mode: "ask", AutomaticSelection: "on", TimeoutSeconds: 20,
		},
	})
	fileConfig.Capabilities = &capabilitySettings
	fileConfig.Memory = &ttypes.MemorySettings{
		AutoRecall: "off", AutoExtract: "off", Consolidation: "off",
	}
	cfg = FileConfigToSession(fileConfig)
	disableLiveMultiAgent(cfg)
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	rootCategory := profile.Categories[session.CategoryDefault]
	rootCategory.AllowedTools = []string{"capability_search", "capability_activate", "mcp_request_access", "read_file", "reply",
		"mcp__" + liveRegistryConnectionID(registrySourceID, registryServerName, "remote", mcpEndpoint.URL) + "__lookup"}
	profile.Categories[session.CategoryDefault] = rootCategory
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	fileConfig.Agents = &cfg.Agents

	appShell, err := New(Opts{FC: fileConfig, Home: home, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	ws := liveCapabilityWorkspace(t, appShell, cfg, project)
	firstSession, err := ws.Bootstrap(cfg, home, "")
	if err != nil {
		ws.Close()
		t.Fatal(err)
	}
	firstSender := &liveCapabilitySender{events: make(chan liveCapabilityEvent, 256)}
	firstOrchestrator, err := ws.StartOrchestrator(firstSender)
	if err != nil {
		ws.Close()
		t.Fatal(err)
	}
	ws.AgentMgr.AddOrchestrator(firstOrchestrator.ID,
		workspace.NewAgentState(firstOrchestrator.ID, workspace.NewConversation(), &appLogger{}))
	if !ws.SetYoloForAgent(firstOrchestrator.ID, true) {
		ws.Close()
		t.Fatal("failed to enable isolated read-only YOLO policy")
	}

	expectedSkillID := "live-skill:live-evidence"
	expectedMCPID := liveRegistryConnectionID(registrySourceID, registryServerName, "remote", mcpEndpoint.URL)
	firstPrompt := `Complete this exact local acceptance workflow without delegating and without commands or writes:
1. capability_search kind=skill for "live-evidence", choose the exact matching discovered entry, then capability_activate that returned identity. If approval is requested, wait for it. Follow the activated skill and read its bundled evidence.txt with native read_file.
2. capability_search kind=mcp for "live evidence", then capability_activate the matching "Live evidence MCP" entry. If activation reports observed tools but no usable tools, call mcp_request_access for that same connection_id with grants [{tool:"lookup", fingerprint:<the exact observed fingerprint>, effect:"read", scope:"fixture evidence only"}], and wait for exact approval.
3. Call the newly usable MCP lookup tool.
4. Finish only after reporting both exact markers. Do not use shell, write, edit, acquisition sources other than these fixtures, or any unrelated tool.`
	if !ws.SubmitAgentInput(firstOrchestrator.ID, provider.PlainInput(firstPrompt)) {
		ws.Close()
		t.Fatal("first live capability task was not admitted")
	}
	firstObserved := waitLiveCapabilityTurn(t, ws, firstOrchestrator.ID, firstSender.events, expectedSkillID, expectedMCPID, true)
	firstCanonical := liveCanonicalText(ws.BackendSession())
	if !strings.Contains(firstCanonical, liveSkillMarker) || !strings.Contains(firstCanonical, liveMCPMarker) {
		ws.Close()
		t.Fatalf("first canonical transcript omitted evidence markers: skill=%t mcp=%t",
			strings.Contains(firstCanonical, liveSkillMarker), strings.Contains(firstCanonical, liveMCPMarker))
	}
	if !firstObserved.skillApproval || !firstObserved.mcpAcquisitionApproval || !firstObserved.mcpAccessApproval {
		ws.Close()
		t.Fatalf("missing exact approval journey: %+v", firstObserved)
	}
	if !firstObserved.skillSearch || !firstObserved.mcpSearch || !firstObserved.nativeSkillRead {
		ws.Close()
		t.Fatalf("model bypassed discovery or native skill evidence read: %+v", firstObserved)
	}
	if sdkCalls.Load() == 0 {
		ws.Close()
		t.Fatal("model did not execute the real MCP SDK lookup tool")
	}
	firstSessionID := firstSession.SessionID
	ws.Close()

	// Reopen the complete workspace and database. Merely taking a snapshot must
	// not resume a provider turn, reconnect MCP, or replay its tool.
	passiveSDK, passiveHTTP := sdkCalls.Load(), mcpRequests.Load()
	reopenedApp, err := New(Opts{FC: fileConfig, Home: home, ProjectDir: project})
	if err != nil {
		t.Fatal(err)
	}
	reopened := liveCapabilityWorkspace(t, reopenedApp, cfg, project)
	snapshot, err := reopened.CapabilitySnapshot(t.Context())
	if err != nil {
		reopened.Close()
		t.Fatal(err)
	}
	skillRegistration, skillOK := snapshot.Settings.Skills[expectedSkillID]
	connectionRegistration, mcpOK := snapshot.Settings.Connections[expectedMCPID]
	grant, grantOK := connectionRegistration.Grants["lookup"]
	if !skillOK || !skillRegistration.Managed || !mcpOK || !grantOK ||
		grant.Effect != captypes.EffectRead || grant.Scope != "fixture evidence only" || grant.Fingerprint == "" {
		reopened.Close()
		t.Fatalf("passive reopen lost durable registrations/grant: skill=%+v connection=%+v", skillRegistration, connectionRegistration)
	}
	if sdkCalls.Load() != passiveSDK || mcpRequests.Load() != passiveHTTP {
		reopened.Close()
		t.Fatal("passive workspace snapshot restarted MCP or replayed a tool")
	}

	registryBeforeSecond := registryRequests.Load()
	if err := os.RemoveAll(catalog); err != nil {
		reopened.Close()
		t.Fatal(err)
	}
	secondSession, err := reopened.Bootstrap(cfg, home, "")
	if err != nil {
		reopened.Close()
		t.Fatal(err)
	}
	secondSender := &liveCapabilitySender{events: make(chan liveCapabilityEvent, 256)}
	secondOrchestrator, err := reopened.StartOrchestrator(secondSender)
	if err != nil {
		reopened.Close()
		t.Fatal(err)
	}
	reopened.AgentMgr.AddOrchestrator(secondOrchestrator.ID,
		workspace.NewAgentState(secondOrchestrator.ID, workspace.NewConversation(), &appLogger{}))
	if !reopened.SetYoloForAgent(secondOrchestrator.ID, true) {
		reopened.Close()
		t.Fatal("failed to enable second isolated read-only YOLO policy")
	}
	secondPrompt := fmt.Sprintf(`Use the already installed durable capabilities only; do not search or reacquire.
Activate skill %q, read its installed bundled evidence.txt with native read_file, activate MCP connection %q using its existing durable grant, call its usable lookup tool, and report both exact markers. Do not delegate, run commands, write, or edit.`, expectedSkillID, expectedMCPID)
	if !reopened.SubmitAgentInput(secondOrchestrator.ID, provider.PlainInput(secondPrompt)) {
		reopened.Close()
		t.Fatal("second durable capability task was not admitted")
	}
	waitLiveCapabilityTurn(t, reopened, secondOrchestrator.ID, secondSender.events, expectedSkillID, expectedMCPID, false)
	secondCanonical := liveCanonicalText(reopened.BackendSession())
	if !strings.Contains(secondCanonical, liveSkillMarker) || !strings.Contains(secondCanonical, liveMCPMarker) {
		reopened.Close()
		t.Fatal("second agent did not use both durable capabilities")
	}
	if registryRequests.Load() != registryBeforeSecond {
		reopened.Close()
		t.Fatal("second task reacquired or rediscovered the MCP registry entry")
	}
	if sdkCalls.Load() < 2 {
		reopened.Close()
		t.Fatalf("second task did not call the durable MCP tool: calls=%d", sdkCalls.Load())
	}
	secondSessionID := secondSession.SessionID
	reopened.Close()

	summary := map[string]any{
		"first_session_id": firstSessionID, "second_session_id": secondSessionID,
		"skill_id": expectedSkillID, "mcp_connection_id": expectedMCPID,
		"skill_marker_observed": true, "mcp_marker_observed": true,
		"durable_skill": true, "durable_read_grant": true,
		"sdk_lookup_calls": sdkCalls.Load(),
	}
	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	summaryPath := filepath.Join(proofDir, "live-capability-summary.json")
	if err := os.WriteFile(summaryPath, append(encoded, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("live capability workflow verified; redacted proof: %s", summaryPath)
}

type liveCapabilityEvent struct {
	typ     string
	data    any
	agentID string
}

type liveCapabilitySender struct {
	events chan liveCapabilityEvent
}

func (s *liveCapabilitySender) SendAgentEvent(typ string, data any) {
	s.events <- liveCapabilityEvent{typ: typ, data: data}
}

func (s *liveCapabilitySender) SendAgentEventWithID(typ string, data any, agentID string) {
	s.events <- liveCapabilityEvent{typ: typ, data: data, agentID: agentID}
}

type liveCapabilityObserved struct {
	skillApproval          bool
	mcpAcquisitionApproval bool
	mcpAccessApproval      bool
	skillSearch            bool
	mcpSearch              bool
	nativeSkillRead        bool
}

func waitLiveCapabilityTurn(t *testing.T, ws *workspace.Workspace, agentID string, events <-chan liveCapabilityEvent, skillID, mcpID string, approve bool) liveCapabilityObserved {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	var observed liveCapabilityObserved
	for {
		select {
		case <-ctx.Done():
			t.Fatalf("live capability turn timed out: %v", ctx.Err())
		case event := <-events:
			if event.agentID != "" && event.agentID != agentID {
				continue
			}
			switch event.typ {
			case "tool_call":
				data, _ := event.data.(map[string]any)
				name, _ := data["name"].(string)
				args, _ := data["args"].(string)
				if name == "capability_search" {
					var parameters struct {
						Kind string `json:"kind"`
					}
					if json.Unmarshal([]byte(args), &parameters) == nil {
						observed.skillSearch = observed.skillSearch || parameters.Kind == string(captypes.KindSkill)
						observed.mcpSearch = observed.mcpSearch || parameters.Kind == string(captypes.KindMCP)
					}
				}
			case "tool_result":
				data, _ := event.data.(map[string]any)
				name, _ := data["name"].(string)
				result, _ := data["result"].(string)
				observed.nativeSkillRead = observed.nativeSkillRead ||
					name == "read_file" && strings.Contains(result, liveSkillMarker)
			case "blocking":
				data, ok := event.data.(map[string]string)
				if !ok {
					t.Fatalf("unexpected blocking payload: %#v", event.data)
				}
				blockID, target, reason := data["block_id"], data["target"], data["reason"]
				allowed := false
				switch {
				case target == skillID && strings.Contains(reason, "scope workspace"):
					observed.skillApproval, allowed = true, true
				case target == mcpID && strings.Contains(reason, "Source Live local MCP registry fixture"):
					observed.mcpAcquisitionApproval, allowed = true, true
				case target == mcpID && strings.Contains(reason, `"tool":"lookup"`) &&
					strings.Contains(reason, `"effect":"read"`) &&
					strings.Contains(reason, `"scope":"fixture evidence only"`):
					observed.mcpAccessApproval, allowed = true, true
				}
				if !approve || !allowed {
					_ = ws.ResolveConfirmation(agentID, blockID, "deny")
					t.Fatalf("refused unexpected capability approval: target=%q reason=%q", target, reason)
				}
				if !ws.ResolveConfirmation(agentID, blockID, "allow") {
					t.Fatalf("could not resolve approved capability request %q", blockID)
				}
			case "done":
				return observed
			case "error":
				t.Fatalf("live capability agent error: %v", event.data)
			}
		}
	}
}

func liveCapabilityWorkspace(t *testing.T, application *App, cfg *session.Config, project string) *workspace.Workspace {
	t.Helper()
	owner := workspace.NewWorkspace(project, cfg.GetActiveDefaultCategory().Model, session.ResolveUsername(), application.initialSettings(), &appLogger{})
	application.configureWorkspace(owner)
	t.Cleanup(owner.Close)
	return owner
}

func disableLiveMultiAgent(cfg *session.Config) {
	cfg.Agents.Enabled = false
	profile := cfg.Agents.Profiles[cfg.Agents.ActiveProfile]
	disabled := false
	profile.MultiAgent = &disabled
	cfg.Agents.Profiles[cfg.Agents.ActiveProfile] = profile
	cfg.Memory.AutoRecall = "off"
	cfg.Memory.AutoExtract = "off"
	cfg.Memory.Consolidation = "off"
}

func liveCanonicalText(backend *session.Session) string {
	if backend == nil {
		return ""
	}
	var text strings.Builder
	for _, message := range backend.MessagesWithMeta() {
		text.WriteString(message.Msg.Content.String())
		text.WriteByte('\n')
	}
	return text.String()
}

func liveRegistryConnectionID(sourceID, serverName, variant, identity string) string {
	digest := sha256.Sum256([]byte(sourceID + "\n" + serverName + "\n" + variant + "\n" + identity))
	return "registry_" + hex.EncodeToString(digest[:])[:20]
}

func liveCapabilityProofDir(t *testing.T) string {
	t.Helper()
	requested := strings.TrimSpace(os.Getenv("CRUDE_CAPABILITY_PROOF_DIR"))
	if requested == "" {
		return t.TempDir()
	}
	parent := strings.TrimSpace(os.Getenv("CRUDE_TEMP_DIR"))
	if parent == "" {
		t.Fatal("CRUDE_CAPABILITY_PROOF_DIR requires CRUDE_TEMP_DIR")
	}
	if !filepath.IsAbs(requested) {
		requested = filepath.Join(parent, requested)
	}
	parent, requested = filepath.Clean(parent), filepath.Clean(requested)
	relative, err := filepath.Rel(parent, requested)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		t.Fatalf("proof directory must be a new child of CRUDE_TEMP_DIR: %q", requested)
	}
	if _, err := os.Stat(requested); !os.IsNotExist(err) {
		t.Fatalf("proof directory must not already exist: %q", requested)
	}
	if err := os.MkdirAll(requested, 0700); err != nil {
		t.Fatal(err)
	}
	return requested
}
