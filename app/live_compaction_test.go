package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"crude/agent/provider"
	"crude/agent/session"
	"crude/agent/tools"
	"crude/agent/turnloop"
	"crude/internal/network"
)

// TestLiveManualCompaction is an opt-in measurement, not protocol certification.
// Only a synthetic history is sent to the explicitly selected Errand account.
// The small input planning window exercises the multi-part path without paying
// to upload a real session. Nothing is written to the user's home.
func TestLiveManualCompaction(t *testing.T) {
	path, id, model := os.Getenv("CRUDE_COMPACT_CONFIG"), os.Getenv("CRUDE_COMPACT_ACCOUNT"), os.Getenv("CRUDE_COMPACT_MODEL")
	if path == "" || id == "" || model == "" {
		t.Skip("set the config file, account and model for a billable manual compaction")
	}
	if !filepath.IsAbs(path) {
		t.Fatal("config path must be absolute")
	}
	cfg, err := loadFileConfigFrom(path)
	if err != nil {
		t.Fatal("cannot load selected configuration")
	}
	settings := FileConfigToSession(cfg)
	category := settings.CategoryConfig(session.CategoryErrand)
	if category.AccountID != id || category.Model != model || cfg.Accounts[id].APIKey == "" || cfg.Accounts[id].OAuth != nil {
		t.Fatal("select the configured API-key Errand account and model")
	}
	old := network.Current()
	t.Cleanup(func() { _ = network.ConfigureDefault(old) })
	if err := network.ConfigureDefault(FileConfigToAppSettings(cfg).Network); err != nil {
		t.Fatal("cannot apply configured network policy")
	}
	p, err := settings.ConnectionProvider(id)
	if err != nil {
		t.Fatal("cannot bind selected account")
	}
	p.Model = model
	p.SetThinkingEffort(category.ThinkingEffort)
	p.SetSampling(category.ModelTemperature, category.ModelTopP)
	p.SetServiceTier(category.ServiceTier)
	a := turnloop.NewAgent(p, model, nil)
	a.UseErrandCheckpointWriter()
	a.WorkDir = t.TempDir()
	a.FilteredTools = turnloop.FilterTools(tools.AgentTools, []string{"read_file"}, nil, "", false)
	a.CompactionTargetWindow = 64_000
	a.CompactionAgentProfile = "synthetic-live-acceptance"
	a.MaxOutputTokens = category.ModelMaxOutput
	// The source is longer than a single part. Keep the actual output allowance
	// of the configured model; the test window limits input planning only.
	if err := provider.RecordRequestLimit(a.CompactionAgentProfile, p, model, provider.LearnedInput, 5500, time.Minute); err != nil {
		t.Fatal(err)
	}
	var source []provider.ChatMessage
	for i, datum := range []string{"The release identifier is Juniper-917.", "The regression test is named TestKeepAmberRows.", "The required next change is in orchid/ledger.go."} {
		source = append(source, provider.NewTextMessage("assistant", fmt.Sprintf("Batch %d audit: %s\n%s", i, strings.Repeat("row checked; no changes. ", 650), datum)))
	}
	source = append(source, provider.NewTextMessage("user", "Keep the exact release identifier, regression test name and file path in the checkpoint."))
	ctx, stop := context.WithTimeout(t.Context(), 4*time.Minute)
	defer stop()
	started := time.Now()
	requests := 0
	parts := 0
	result, err := a.Compact(ctx, source, "Preserve exact project facts.", func(e turnloop.AgentEvent) {
		if e.Type == "usage" {
			requests++
		}
		if d, ok := e.Data.(map[string]any); ok {
			if n, ok := d["chunk_count"].(int); ok {
				parts = max(parts, n)
			}
		}
	})
	if err != nil {
		t.Fatalf("manual compaction of the synthetic fixture failed after %s: %v", time.Since(started).Round(time.Second), err)
	}
	if len(result.Messages) == 0 || parts < 2 || result.ModelID != model || result.Writer != "errand" {
		t.Fatalf("model=%q parts=%d messages=%d", result.ModelID, parts, len(result.Messages))
	}
	for _, fact := range []string{"Juniper-917", "TestKeepAmberRows", "orchid/ledger.go"} {
		if !strings.Contains(result.Messages[0].ContentString(), fact) {
			t.Errorf("checkpoint lost %s", fact)
		}
	}
	t.Logf("model=%s wire=%s wall=%s parts=%d requests=%d before=%d after=%d input=%d cached=%d output=%d", model, p.ProtocolID(), time.Since(started).Round(time.Millisecond), parts, requests, result.BeforeTokens, result.AfterTokens, result.Usage.PromptTokens, result.Usage.PromptTokensCached, result.Usage.CompletionTokens)
}
