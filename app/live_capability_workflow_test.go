package app

import (
	"context"
	"crude/agent"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"crude/provider"
	"crude/session"
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
// selected model and proves the complete model-driven skill/MCP workflow rather
// than substituting a scripted provider.
func TestLiveCapabilityWorkflow(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_CAPABILITY_WORKFLOW") != "1" {
		t.Skip("CRUDE_LIVE_CAPABILITY_WORKFLOW=1 not set")
	}

	// Credentials must be loaded from the real user config before the process
	// home is isolated. They remain in memory and are never copied to evidence.
	fileConfig := LoadFileConfig()
	if category := strings.TrimSpace(os.Getenv("CRUDE_LIVE_CAPABILITY_CATEGORY")); category != "" {
		configured := FileConfigToSession(fileConfig)
		profile := configured.Agents.Profiles[configured.Agents.ActiveProfile]
		if _, exists := profile.Categories[category]; !exists {
			t.Fatalf("live capability category %q is not configured", category)
		}
		profile.Categories[session.CategoryDefault] = configured.GetCategoryConfig(category)
		configured.Agents.Profiles[configured.Agents.ActiveProfile] = profile
		fileConfig.Agents = &configured.Agents
	}
	cfg := FileConfigToSession(fileConfig)
	main := cfg.GetActiveDefaultCategory()
	if main.AccountID == "" || main.Model == "" {
		t.Skip("main agent account and model are not configured")
	}
	if _, err := cfg.ConnectionProvider(main.AccountID); err != nil {
		t.Skipf("configured main agent is unavailable: %v", err)
	}
	// Explicit test transport selection, never an automatic gateway fallback
	// or a mutation of the user's account. Some compatible endpoints accept
	// response IDs but fail the separate stored-context acceptance test.
	if value := strings.TrimSpace(os.Getenv("CRUDE_LIVE_CAPABILITY_STORE")); value != "" {
		store, err := strconv.ParseBool(value)
		if err != nil {
			t.Fatal("CRUDE_LIVE_CAPABILITY_STORE must be a boolean")
		}
		account := provider.CloneAccount(fileConfig.Accounts[main.AccountID])
		template := account.Connection
		if template.Protocol == nil {
			template.Protocol = &provider.ProtocolSettings{}
		}
		template.Protocol.Store = &store
		account.Connection.Protocol = template.Protocol
		fileConfig.Accounts[main.AccountID] = account
	}
	t.Logf("live capability model=%s account=%s thinking=%s", main.Model, main.AccountID, main.ThinkingEffort)

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

	nonce := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	recordID := "audit-" + hex.EncodeToString(nonce)
	skillEvidence := liveSkillMarker + "_" + hex.EncodeToString(nonce)
	mcpEvidence := liveMCPMarker + "_" + hex.EncodeToString(nonce)
	skillDir := filepath.Join(catalog, "live-evidence")
	if err := os.MkdirAll(skillDir, 0700); err != nil {
		t.Fatal(err)
	}
	// A real procedure, not Create's unfinished authoring template. Neither
	// the receipt's identifier nor its verification codes appear in the task.
	definition := `---
name: live-evidence
description: Audit live evidence by checking a bundled receipt and independently served verification data.
---
# Live evidence audit

Read the bundled evidence.txt from this procedure's installed directory.
It contains the record_id and the local verification code. Relative bundled
paths belong to the installed directory, not the project.

Obtain independent verification for that record from the Live evidence MCP
integration. Perform a fresh remote lookup for each audit. Report both exact
verification codes; never invent a record identifier or reuse an old remote code.
`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	receipt, err := json.Marshal(map[string]string{"record_id": recordID, "verification": skillEvidence})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "evidence.txt"), receipt, 0600); err != nil {
		t.Fatal(err)
	}

	var sdkCalls, mcpRequests, registryRequests atomic.Int32
	var remoteRevision atomic.Int32
	remoteRevision.Store(1)
	mcpServer := mcp.NewServer(&mcp.Implementation{Name: "Crude live capability fixture", Version: "1"}, nil)
	mcpServer.AddTool(&mcp.Tool{
		Name: "lookup", Description: "Read current independently served verification for the exact record_id from an audit receipt",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"record_id": map[string]any{"type": "string"}},
			"required":   []string{"record_id"},
		},
	}, func(_ context.Context, request *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		var parameters struct {
			RecordID string `json:"record_id"`
		}
		if json.Unmarshal(request.Params.Arguments, &parameters) != nil || parameters.RecordID != recordID {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "Unknown record: read the exact audit receipt first."}}}, nil
		}
		sdkCalls.Add(1)
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: fmt.Sprintf("%s_%d", mcpEvidence, remoteRevision.Load())}}}, nil
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
					"description": "Read current independently served verification for live evidence audit receipts",
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
	firstOrchestrator, err := ws.StartOrchestrator(firstSession.SessionID, firstSender)
	if err != nil {
		ws.Close()
		t.Fatal(err)
	}
	visual := startLiveCapabilityVisual(t, ws, firstOrchestrator.ID, firstSender)
	if visual == nil {
		ws.AgentMgr.AddOrchestrator(firstOrchestrator.ID,
			workspace.NewAgentState(firstOrchestrator.ID, workspace.NewConversation(), &appLogger{}))
	}
	if !ws.SetYoloForAgent(firstOrchestrator.ID, true) {
		ws.Close()
		t.Fatal("failed to enable isolated read-only YOLO policy")
	}

	expectedSkillID := "live-skill:live-evidence"
	expectedMCPID := liveRegistryConnectionID(registrySourceID, registryServerName, "remote", mcpEndpoint.URL)
	// This is the user's outcome, not a tool-use recipe: no function names,
	// entry IDs, fingerprints, paths, record identifier or expected codes.
	firstPrompt := `Audit the live evidence available to this workspace using the live-evidence procedure: obtain its bundled receipt verification and the current independently served verification for the same record. Report both codes exactly, not guesses or placeholders. Leave the required procedure and integration registered for later agents in this workspace. Work read-only, without commands, writes, edits or delegation. Any installation or new access is subject to review.`
	submitLiveCapabilityInput(t, ws, firstOrchestrator.ID, firstPrompt, visual)
	firstObserved := waitLiveCapabilityTurn(t, ws, firstOrchestrator.ID, firstSender.events, expectedSkillID, expectedMCPID, true)
	firstCanonical := liveLastAnswer(ws.BackendSession(firstSession.SessionID))
	if !strings.Contains(firstCanonical, skillEvidence) || !strings.Contains(firstCanonical, mcpEvidence+"_1") {
		ws.Close()
		t.Fatalf("first canonical transcript omitted evidence markers: skill=%t mcp=%t",
			strings.Contains(firstCanonical, skillEvidence), strings.Contains(firstCanonical, mcpEvidence+"_1"))
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

	// Remove the acquisition source before later rounds: only the installed,
	// immutable copy may satisfy the procedure now.
	if err := os.RemoveAll(catalog); err != nil {
		t.Fatal(err)
	}
	repeatPrompt := `Repeat that audit now. Recheck the receipt and obtain fresh independently served evidence; report both current verification codes. Keep it read-only.`
	beforeRepeat := sdkCalls.Load()
	remoteRevision.Store(2)
	submitLiveCapabilityInput(t, ws, firstOrchestrator.ID, repeatPrompt, visual)
	repeatObserved := waitLiveCapabilityTurn(t, ws, firstOrchestrator.ID, firstSender.events, expectedSkillID, expectedMCPID, false)
	requireLiveCapabilityAnswer(t, ws, firstSession.SessionID, skillEvidence, mcpEvidence+"_2")
	if sdkCalls.Load() <= beforeRepeat {
		t.Fatal("same-agent follow-up reused old remote evidence")
	}
	if visual != nil {
		if err := os.WriteFile(filepath.Join(proofDir, "visual-chat-ready"), []byte("Both composer-submitted rounds completed.\n"), 0600); err != nil {
			t.Fatal(err)
		}
		// Keep the completed, still-owned workspace available for inspection.
		// The terminal operator closes it with the normal Quit control.
		visual.Wait()
	}
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
		grant.Effect != captypes.EffectRead || grant.Scope != firstObserved.accessScope || grant.Fingerprint == "" {
		reopened.Close()
		t.Fatalf("passive reopen lost durable registrations/grant: skill=%+v connection=%+v", skillRegistration, connectionRegistration)
	}
	if sdkCalls.Load() != passiveSDK || mcpRequests.Load() != passiveHTTP {
		reopened.Close()
		t.Fatal("passive workspace snapshot restarted MCP or replayed a tool")
	}

	// Resume the original session passively, then explicitly continue it. The
	// source is gone, so a replay/reacquisition cannot hide lost durability.
	registryBeforeSecond := registryRequests.Load()
	resumedSession, err := reopened.Bootstrap(cfg, home, firstSessionID)
	if err != nil {
		t.Fatal(err)
	}
	if sdkCalls.Load() != passiveSDK || mcpRequests.Load() != passiveHTTP {
		t.Fatal("passive session resume reconnected MCP or replayed a tool")
	}
	resumedSender := &liveCapabilitySender{events: make(chan liveCapabilityEvent, 256)}
	resumedOrchestrator, err := reopened.StartOrchestrator(resumedSession.SessionID, resumedSender)
	if err != nil {
		t.Fatal(err)
	}
	reopened.AgentMgr.AddOrchestrator(resumedOrchestrator.ID,
		workspace.NewAgentState(resumedOrchestrator.ID, workspace.NewConversation(), &appLogger{}))
	if !reopened.SetYoloForAgent(resumedOrchestrator.ID, true) {
		t.Fatal("failed to enable resumed read-only policy")
	}
	remoteRevision.Store(3)
	if !reopened.SubmitAgentInput(resumedOrchestrator.ID, provider.PlainInput(repeatPrompt)) {
		t.Fatal("explicit resumed follow-up was not admitted")
	}
	resumedObserved := waitLiveCapabilityTurn(t, reopened, resumedOrchestrator.ID, resumedSender.events, expectedSkillID, expectedMCPID, false)
	requireLiveCapabilityAnswer(t, reopened, resumedSession.SessionID, skillEvidence, mcpEvidence+"_3")
	if sdkCalls.Load() <= passiveSDK {
		t.Fatal("resumed agent did not use fresh remote evidence")
	}
	secondSession, err := reopened.Bootstrap(cfg, home, "")
	if err != nil {
		reopened.Close()
		t.Fatal(err)
	}
	secondSender := &liveCapabilitySender{events: make(chan liveCapabilityEvent, 256)}
	secondOrchestrator, err := reopened.StartOrchestrator(secondSession.SessionID, secondSender)
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
	remoteRevision.Store(4)
	beforeNewAgent := sdkCalls.Load()
	if !reopened.SubmitAgentInput(secondOrchestrator.ID, provider.PlainInput(firstPrompt)) {
		reopened.Close()
		t.Fatal("second durable capability task was not admitted")
	}
	secondObserved := waitLiveCapabilityTurn(t, reopened, secondOrchestrator.ID, secondSender.events, expectedSkillID, expectedMCPID, false)
	requireLiveCapabilityAnswer(t, reopened, secondSession.SessionID, skillEvidence, mcpEvidence+"_4")
	if registryRequests.Load() != registryBeforeSecond {
		reopened.Close()
		t.Fatal("second task reacquired or rediscovered the MCP registry entry")
	}
	if sdkCalls.Load() <= beforeNewAgent {
		reopened.Close()
		t.Fatalf("second task did not call the durable MCP tool: calls=%d", sdkCalls.Load())
	}
	secondSessionID := secondSession.SessionID
	reopened.Close()

	summary := map[string]any{
		"model": main.Model, "thinking": main.ThinkingEffort,
		"response_store_override": os.Getenv("CRUDE_LIVE_CAPABILITY_STORE"),
		"first_session_id":        firstSessionID, "second_session_id": secondSessionID,
		"skill_id": expectedSkillID, "mcp_connection_id": expectedMCPID,
		"skill_marker_observed": true, "mcp_marker_observed": true,
		"durable_skill": true, "durable_read_grant": true,
		"sdk_lookup_calls":     sdkCalls.Load(),
		"same_agent_follow_up": true, "passive_resume": true, "resumed_follow_up": true,
		"first_turn": firstObserved, "same_agent_turn": repeatObserved, "resumed_turn": resumedObserved, "new_agent_turn": secondObserved,
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
	events  chan liveCapabilityEvent
	forward session.EventSender
}

func (s *liveCapabilitySender) SendAgentEvent(typ string, data any) {
	s.events <- liveCapabilityEvent{typ: typ, data: data}
	if s.forward != nil {
		s.forward.SendAgentEvent(typ, data)
	}
}

func (s *liveCapabilitySender) SendAgentEventWithID(typ string, data any, agentID string) {
	s.events <- liveCapabilityEvent{typ: typ, data: data, agentID: agentID}
	if s.forward != nil {
		s.forward.SendAgentEventWithID(typ, data, agentID)
	}
}

type liveCapabilityObserved struct {
	skillApproval          bool
	mcpAcquisitionApproval bool
	mcpAccessApproval      bool
	skillSearch            bool
	mcpSearch              bool
	nativeSkillRead        bool
	accessScope            string
	Tools                  []string
	ToolErrors             []string
	Duration               time.Duration
}

func waitLiveCapabilityTurn(t *testing.T, ws *workspace.Workspace, agentID string, events <-chan liveCapabilityEvent, skillID, mcpID string, approve bool) liveCapabilityObserved {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	var observed liveCapabilityObserved
	started := time.Now()
	var requestedGrants []captypes.AccessRequest
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
				t.Logf("live capability tool: %s", name)
				observed.Tools = append(observed.Tools, name)
				if name == "mcp_request_access" {
					var parameters struct {
						ConnectionID string                   `json:"connection_id"`
						Grants       []captypes.AccessRequest `json:"grants"`
					}
					if json.Unmarshal([]byte(args), &parameters) == nil && parameters.ConnectionID == mcpID {
						requestedGrants = parameters.Grants
					}
				}
			case "tool_result":
				data, _ := event.data.(map[string]any)
				name, _ := data["name"].(string)
				result, _ := data["result"].(string)
				if name == "capability_search" {
					// kind is optional: a legitimate untyped discovery can
					// return both kinds. Assert the delivered identities.
					var entries []captypes.Entry
					if json.Unmarshal([]byte(result), &entries) != nil {
						var partial captypes.SearchResult
						if json.Unmarshal([]byte(result), &partial) == nil {
							entries = partial.Entries
						}
					}
					for _, entry := range entries {
						observed.skillSearch = observed.skillSearch || entry.ID == skillID && entry.Kind == captypes.KindSkill
						observed.mcpSearch = observed.mcpSearch || entry.ID == mcpID && entry.Kind == captypes.KindMCP
					}
				}
				observed.nativeSkillRead = observed.nativeSkillRead ||
					name == "read_file" && strings.Contains(result, liveSkillMarker)
				if data["error"] == "true" {
					observed.ToolErrors = append(observed.ToolErrors, name+": "+result)
					t.Logf("live capability tool error: %s: %.400s", name, result)
				}
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
				case target == mcpID && data["tool"] == "mcp_request_access" &&
					len(requestedGrants) == 1 && requestedGrants[0].Tool == "lookup" &&
					requestedGrants[0].Effect == captypes.EffectRead &&
					requestedGrants[0].Fingerprint != "" && requestedGrants[0].Scope != "":
					// This fixture exposes exactly one read-only record. Review
					// the exact immutable operation, not a coached scope label.
					observed.mcpAccessApproval, allowed = true, true
					observed.accessScope = requestedGrants[0].Scope
				}
				if !approve || !allowed {
					_ = ws.ResolveConfirmation(agentID, blockID, "deny")
					t.Fatalf("refused unexpected capability approval: target=%q reason=%q", target, reason)
				}
				if os.Getenv("CRUDE_VISUAL_CAPABILITIES") == "1" {
					// Give the dedicated capture workflow time to inspect the
					// real owner-scoped approval surface before resolving it.
					select {
					case <-ctx.Done():
						t.Fatal(ctx.Err())
					case <-time.After(3 * time.Second):
					}
				}
				if !ws.ResolveConfirmation(agentID, blockID, "allow") {
					t.Fatalf("could not resolve approved capability request %q", blockID)
				}
				t.Logf("approved exact capability request: %s", target)
			case "done":
				observed.Duration = time.Since(started)
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

func liveLastAnswer(backend *session.Session) string {
	if backend == nil {
		return ""
	}
	var text string
	for _, message := range backend.MessagesWithMeta() {
		if message.Msg.Role == "assistant" && len(message.Msg.ToolCalls) == 0 && message.Msg.Content.String() != "" {
			text = message.Msg.Content.String()
		}
	}
	return text
}

func requireLiveCapabilityAnswer(t *testing.T, ws *workspace.Workspace, sessionID, skillEvidence, mcpEvidence string) {
	t.Helper()
	answer := liveLastAnswer(ws.BackendSession(sessionID))
	if !strings.Contains(answer, skillEvidence) || !strings.Contains(answer, mcpEvidence) {
		t.Fatalf("terminal answer did not report both current verification codes: %s", answer)
	}
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

func (s *liveCapabilitySender) SendAgentEventEnvelope(event agent.AgentEvent) {
	s.SendAgentEventWithID(event.Type, event.Data, event.AgentID)
}
