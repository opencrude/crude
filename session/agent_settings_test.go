package session

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"crude/agent"
	"crude/provider"
	"crude/tools"
	"crude/types"
)

func TestAgentSettingsRoundTripAndRuntimeRestrictions(t *testing.T) {
	cfg := &Config{Agents: DefaultAgentsConfig()}
	settings := ConfigToSettings(cfg).Agents
	settings.Categories[CategoryDefault] = types.CategorySettings{Instructions: "Explain uncertainty", DeniedTools: []string{"execute_command", "write_file", "edit_file"}}
	settings.AgentProfiles = map[string]types.AgentProfileSettings{"audit": {Category: CategoryResearcher, Instructions: "Review safety", Description: "Independent review", DeniedTools: []string{"web_fetch"}}}
	ApplyWorkspaceAgentDefaults(cfg, settings)
	got := ConfigToSettings(cfg).Agents
	if got.Categories[CategoryDefault].Instructions != "Explain uncertainty" || got.AgentProfiles["audit"].Instructions != "Review safety" {
		t.Fatalf("agent settings round-trip lost data: %+v", got)
	}
	for _, tool := range configuredOrchestratorTools(cfg, true, "gpt-test") {
		if tool.Function.Name == "exec_command" || tool.Function.Name == "write_stdin" || tool.Function.Name == "apply_patch" {
			t.Fatalf("native tool alias bypassed restrictions: %s", tool.Function.Name)
		}
	}
	runner := &SubAgentRunner{effectiveCfg: cfg}
	spec := agent.SubAgentSpec{Category: CategoryCrafter, AllowedTools: []string{"web_fetch"}, Instructions: "Task-specific instructions"}
	instructions := runner.resolveProfile("audit", CategoryCrafter, &spec)
	if spec.Category != CategoryResearcher || !strings.Contains(instructions, "Review safety") || spec.Instructions != "Task-specific instructions" {
		t.Fatalf("named profile resolution lost its role or prompt: %+v %q", spec, instructions)
	}
	for _, tool := range agent.FilterTools(tools.AgentTools, spec.AllowedTools, spec.DeniedTools, spec.Category, true) {
		if tool.Function.Name == "web_fetch" {
			t.Fatal("model-supplied allow list bypassed configured block")
		}
	}
	if !strings.Contains(agentProfileCatalog(cfg), `name="audit"`) {
		t.Fatal("custom agent is not discoverable to delegation")
	}
}

func TestNamedAgentProfilePinsItsConfiguredConnection(t *testing.T) {
	percent, rolePercent := 72, 80
	cfg := &Config{Agents: DefaultAgentsConfig()}
	profile := cfg.Agents.Profiles["default"]
	profile.Categories[CategoryResearcher] = CategoryConfig{Model: "role-model", AccountID: "personal", ContextCompactionPercent: &rolePercent}
	cfg.Agents.Profiles["default"] = profile
	cfg.Agents.AgentProfiles["reviewer"] = AgentProfileConfig{Category: CategoryResearcher, Model: "review-model", AccountID: "work", ContextCompactionPercent: &percent}
	runner := &SubAgentRunner{effectiveCfg: cfg}
	for _, spec := range []agent.SubAgentSpec{{}, {Model: "requested-model", AccountID: "requested-account"}} {
		runner.resolveProfile("reviewer", CategoryResearcher, &spec)
		if spec.Model != "review-model" || spec.AccountID != "work" || spec.ContextCompactionPercent == nil || *spec.ContextCompactionPercent != percent {
			t.Fatalf("named profile did not own its configured connection: %+v", spec)
		}
	}
}

func TestSelectedRootAgentPersistsAndFollowsProfileWithoutLosingAccountPin(t *testing.T) {
	home := t.TempDir()
	cfg := testConfig()
	cfg.Accounts["work"] = cfg.Accounts["account"]
	work := cfg.Accounts["work"]
	work.ID = "work"
	cfg.Accounts["work"] = work
	cfg.Agents.AgentProfiles["reviewer"] = AgentProfileConfig{Category: CategoryResearcher, AccountID: "work", Model: "review-v1", Instructions: "Check edge cases"}
	sess, err := NewWithSessionDir("", cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	settings := sess.SettingsSnapshot()
	settings.SessionOverrides = sess.SessionOverridesSnapshot()
	settings.SessionMainAgent = "profile/reviewer"
	settings.SessionOverrides["root/profile/reviewer"] = types.CategorySettings{AccountID: "work"}
	if err := sess.HandleSettingsSave(settings); err != nil {
		t.Fatal(err)
	}
	if got := sess.RuntimeProfile().GetEffectiveConfig("root"); got.GetActiveDefaultCategory().Model != "review-v1" || got.GetActiveDefaultCategory().AccountID != "work" || got.SelectedAgent != "profile/reviewer" {
		t.Fatalf("root request still uses Main Agent: %+v", got.GetActiveDefaultCategory())
	}
	if got := sess.SettingsSnapshot(); got.SessionMainAgent != "profile/reviewer" || got.Model != "review-v1" {
		t.Fatalf("session projection does not match request: %+v", got)
	}
	if prompt := sess.systemPromptForModel(true, "review-v1"); !strings.Contains(prompt, "Check edge cases") {
		t.Fatalf("selected agent instructions did not reach the next request: %q", prompt)
	}
	id := sess.SessionID()
	// Empty sessions are discarded on Close; retain one fixture message to
	// exercise the real persisted-session resume boundary.
	sess.msgs = []provider.ChatMessage{provider.NewTextMessage("user", "Keep this session")}
	sess.Close()
	profile := cfg.Agents.AgentProfiles["reviewer"]
	profile.Model, profile.Instructions = "review-v2", "New instructions"
	cfg.Agents.AgentProfiles["reviewer"] = profile
	resumed, err := ResumeSessionWithDir(filepath.Join(home, id+".db"), cfg, home, home)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close()
	got := resumed.SettingsSnapshot()
	if got.SessionMainAgent != "profile/reviewer" || got.Model != "review-v2" || got.AccountID != "work" {
		t.Fatalf("selected agent did not survive resume/profile update: %+v", got)
	}
	if err := resumed.RuntimeProfile().GetEffectiveConfig("root").ValidateSelectedAgent(); err != nil {
		t.Fatal(err)
	}
	profile.Enabled = new(bool)
	cfg.Agents.AgentProfiles["reviewer"] = profile
	resumed.UpdateWorkspaceDefaults(types.AppSettings{Agents: ConfigToSettings(cfg).Agents, Accounts: cfg.Accounts, Providers: cfg.Providers})
	if err := resumed.RuntimeProfile().GetEffectiveConfig("root").ValidateSelectedAgent(); err == nil {
		t.Fatal("disabled selected agent silently fell back to Main Agent")
	}
}

func TestMemoryAgentDisableStopsBackgroundJobsAndRecall(t *testing.T) {
	cfg := testConfig()
	off := false
	cfg.Memory.MemoryCategory.Enabled = &off
	sess := &Session{cfg: cfg}
	settings := sess.memorySettings()
	if settings.AutoRecallEnabled() || settings.AutoExtractEnabled() || settings.ConsolidationEnabled() {
		t.Fatalf("disabled Memory Agent still works in background: %+v", settings)
	}
	if _, err := sess.memoryCompleter(); err == nil {
		t.Fatal("disabled Memory Agent still constructed a provider request")
	}
}

func TestMemoryAgentUsesEditedInstructionsAndOutputLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request map[string]any
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if request["model"] != "memory-model" || request["max_tokens"] != float64(56) || !strings.Contains(fmt.Sprint(request["messages"]), "Additional user-configured Memory Agent instructions:") || !strings.Contains(fmt.Sprint(request["messages"]), "Use factual summaries") {
			t.Errorf("Memory Agent settings were ignored: %+v", request)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer server.Close()
	cfg := testConfig()
	template := cfg.Providers["openrouter"]
	template.ProviderURL = server.URL + "/v1"
	cfg.Providers["openrouter"] = template
	cfg.Memory.MemoryCategory = types.CategorySettings{Model: "memory-model", AccountID: "account", Instructions: "Use factual summaries", ModelMaxOutput: 56}
	sess := &Session{cfg: cfg}
	complete, err := sess.memoryCompleter()
	if err != nil {
		t.Fatal(err)
	}
	if result, err := complete(context.Background(), "Extract memory", "observations"); err != nil || result != "done" {
		t.Fatalf("memory completion: %q, %v", result, err)
	}
}

func TestExplicitNoModalitiesSurvivesConfigurationRoundTrip(t *testing.T) {
	cfg := testConfig()
	settings := ConfigToSettings(cfg).Agents
	main := settings.Categories[CategoryDefault]
	main.ModelModalities = []string{}
	settings.Categories[CategoryDefault] = main
	ApplyWorkspaceAgentDefaults(cfg, settings)
	encoded, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var restored Config
	if err := json.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if got := restored.GetActiveDefaultCategory().ModelModalities; got == nil || len(got) != 0 {
		t.Fatalf("explicit No became automatic discovery after restart: %#v", got)
	}
}
