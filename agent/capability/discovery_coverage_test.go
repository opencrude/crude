package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"crude/agent/skills"
	captypes "crude/types/capability"
)

func TestCapabilitySearchFindsKeywordsAcrossNameAndDescriptionAndReusesInstalled(t *testing.T) {
	service, _, sourcePath := capabilityFixture(t)
	query := "review procedure evidence verification"
	actor := captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}}
	result, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_search",
		map[string]any{"query": query, "kind": "skill", "limit": 1})
	var found captypes.SearchResult
	if err != nil || len(result.Content) != 1 || json.Unmarshal([]byte(result.Content[0].Text), &found) != nil ||
		len(found.Results) != 1 || found.Results[0].ID != "team:review" {
		t.Fatalf("natural keyword discovery lost the relevant skill: %+v %v", result, err)
	}
	_, err = service.Activate(t.Context(), actor, found.Results[0].ID, false)
	var approval *captypes.ApprovalRequired
	if !errors.As(err, &approval) {
		t.Fatalf("search unexpectedly installed or granted the skill: %v", err)
	}
	if _, err := service.Activate(approval.CapabilityApprovalContext(t.Context()), actor, found.Results[0].ID, false); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(sourcePath)); err != nil {
		t.Fatal(err)
	}
	installed, err := service.Search(t.Context(), query, captypes.KindSkill, 1, true)
	if err != nil || len(installed) != 1 || installed[0].ID != "team:review" || !installed[0].Installed {
		t.Fatalf("installed-first keyword search touched the removed acquisition source: %+v %v", installed, err)
	}
	empty, err := service.Search(t.Context(), "zznonexistent987", captypes.KindSkill, 1, false)
	if err != nil || len(empty) != 0 {
		t.Fatalf("unrelated query returned an installed capability: %+v %v", empty, err)
	}
}

func TestCapabilityKeywordSearchRanksBeforeApplyingLimit(t *testing.T) {
	service, settings, sourcePath := capabilityFixture(t)
	exact, err := skills.Create(filepath.Join(filepath.Dir(sourcePath), "review-evidence"), "review-evidence", "Receipt audit")
	if err != nil {
		t.Fatal(err)
	}
	settings.Skills = map[string]captypes.SkillConfig{
		"a:partial": {ID: "a:partial", Name: "review", Path: sourcePath, Mode: "automatic"},
		"z:exact":   {ID: "z:exact", Name: exact.Name, Path: exact.Path, Mode: "automatic"},
	}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Search(t.Context(), "review evidence", captypes.KindSkill, 1, false)
	if err != nil || len(entries) != 1 || entries[0].ID != "z:exact" {
		t.Fatalf("early lexical limit hid the exact normalized name: %+v %v", entries, err)
	}
}

func TestCapabilityActivationRejectsGuessedNameWithExactIdentityGuidance(t *testing.T) {
	service, _, _ := capabilityFixture(t)
	if _, err := service.Search(t.Context(), "review", captypes.KindSkill, 10, true); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}}
	_, err := service.Execute(t.Context(), actor, captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": "review"})
	if err == nil || !strings.Contains(err.Error(), "returned ID unchanged") || len(service.Snapshot().Entries) != 0 {
		t.Fatalf("a guessed display name was installed or lacked exact-identity recovery: %v", err)
	}
}

func TestDiscoverInterleavesSourcesBeyondFirstSourceLimit(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	first := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var skills []map[string]any
		for i := range 15 {
			skills = append(skills, map[string]any{"id": fmt.Sprintf("publisher/repo/skill-%d", i), "source": "publisher/repo", "skillId": fmt.Sprintf("skill-%d", i), "name": fmt.Sprintf("skill-%d", i)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"skills": skills})
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"skills": []any{map[string]any{"id": "other/repo/skill", "source": "other/repo", "skillId": "skill", "name": "skill"}}})
	}))
	defer second.Close()
	settings.Sources = map[string]captypes.SourceConfig{
		"a": {ID: "a", Kind: "skill-registry", Location: first.URL},
		"b": {ID: "b", Kind: "skill-registry", Location: second.URL},
	}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Discover(t.Context(), "skill", captypes.KindSkill, 5)
	if err != nil || len(entries) != 5 || entries[0].Source != "a" || entries[1].Source != "b" {
		t.Fatalf("interleaved entries=%+v err=%v", entries, err)
	}
	entries, err = service.DiscoverSource(t.Context(), "skill", captypes.KindSkill, 5, "b")
	if err != nil || len(entries) != 1 || entries[0].Source != "b" {
		t.Fatalf("selected source entries=%+v err=%v", entries, err)
	}
	for _, sourceID := range []string{"missing", "a"} {
		kind := captypes.KindSkill
		if sourceID == "a" {
			kind = captypes.KindMCP
		}
		if _, err := service.DiscoverSource(t.Context(), "skill", kind, 5, sourceID); err == nil {
			t.Fatalf("invalid selected source %q succeeded", sourceID)
		}
	}
	second.Close()
	entries, err = service.Discover(t.Context(), "skill", captypes.KindSkill, 5)
	if len(entries) != 5 || err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("partial source failure entries=%+v err=%v", entries, err)
	}
	result, err := service.Execute(t.Context(), captypes.RuntimeActor{Effects: []captypes.Effect{captypes.EffectRead}},
		captypes.RuntimeSnapshot{}, "capability_search", map[string]any{"kind": "skill", "query": "skill", "limit": 3})
	var partial captypes.SearchResult
	if err != nil || json.Unmarshal([]byte(result.Content[0].Text), &partial) != nil || len(partial.Results) != 3 || partial.Warning == "" {
		t.Fatalf("agent-facing search discarded partial results: %+v %v", result, err)
	}
}

func TestRegistryPaginationFuzzyResultsMetadataAndUnsupported(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("search") != "git" {
			t.Errorf("search=%q", r.URL.Query().Get("search"))
		}
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"server": map[string]any{"name": "vendor/fuzzy-match", "title": "Remote fuzzy", "version": "1.0", "description": "No literal query substring", "remotes": []any{map[string]any{"type": "streamable-http", "url": "https://example.org/mcp"}}}, "_meta": map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "active", "isLatest": true}}}}, "metadata": map[string]any{"nextCursor": "page2"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": []any{
			map[string]any{"server": map[string]any{"name": "vendor/local", "title": "Local fuzzy", "version": "2", "packages": []any{map[string]any{"registryType": "npm", "identifier": "fuzzy-package", "version": "2", "transport": map[string]any{"type": "stdio"}, "environmentVariables": []any{map[string]any{"name": "TOKEN", "isRequired": true}}}}}},
			map[string]any{"server": map[string]any{"name": "vendor/interactive", "version": "3", "packages": []any{map[string]any{"registryType": "npm", "identifier": "interactive", "version": "3", "transport": map[string]any{"type": "stdio"}, "packageArguments": []any{map[string]any{"type": "positional", "isRequired": true}}}}}},
			map[string]any{"server": map[string]any{"name": "vendor/unknown", "version": "4", "packages": []any{map[string]any{"registryType": "oci", "identifier": "unknown", "version": "4", "transport": map[string]any{"type": "stdio"}}}}},
			map[string]any{"server": map[string]any{"name": "vendor/sse", "version": "5", "remotes": []any{map[string]any{"type": "sse", "url": "https://example.org/sse"}}}},
		}, "metadata": map[string]any{"nextCursor": ""}})
	}))
	defer endpoint.Close()
	settings.Sources = map[string]captypes.SourceConfig{"registry": {ID: "registry", Kind: "registry", Location: endpoint.URL, Trusted: true}}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Discover(t.Context(), "git", captypes.KindMCP, 8)
	if err != nil || len(entries) != 5 {
		t.Fatalf("registry entries=%+v err=%v", entries, err)
	}
	byName := make(map[string]captypes.Entry)
	var unsupported []captypes.Entry
	for _, entry := range entries {
		byName[entry.Name] = entry
		if entry.State == "unsupported" {
			unsupported = append(unsupported, entry)
		}
	}
	if entries[0].Name != "Remote fuzzy" || byName["Local fuzzy"].State != "setup required" || !strings.Contains(byName["Local fuzzy"].Reason, "TOKEN") ||
		!strings.Contains(byName["vendor/interactive"].Reason, "interactive") || !strings.Contains(byName["vendor/unknown"].Reason, "oci") ||
		byName["vendor/sse"].State != "not acquired" || byName["vendor/sse"].Transport != "remote" || len(unsupported) != 2 {
		t.Fatalf("metadata/setup/unsupported entries=%+v", entries)
	}
	if preview, err := service.PreviewAcquisition(t.Context(), byName["vendor/sse"].ID, captypes.ScopeWorkspace, "install"); err != nil || preview.Candidate.ID == "" {
		t.Fatalf("legacy SSE remote must be acquirable: %+v %v", preview, err)
	}
	for _, entry := range unsupported {
		if _, err := service.PreviewAcquisition(t.Context(), entry.ID, captypes.ScopeWorkspace, "install"); err == nil || !strings.Contains(err.Error(), entry.Reason) {
			t.Fatalf("unsupported preview %q: %v", entry.ID, err)
		}
		if _, err := service.Acquire(t.Context(), entry.ID, captypes.ScopeWorkspace); err == nil || !strings.Contains(err.Error(), entry.Reason) {
			t.Fatalf("unsupported acquisition %q: %v", entry.ID, err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Discover(ctx, "git", captypes.KindMCP, 8); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestRegistryUnsupportedPageDoesNotCrowdOutNextPage(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"server": map[string]any{"name": "vendor/unsupported", "packages": []any{map[string]any{"registryType": "oci", "identifier": "image", "version": "1", "transport": map[string]any{"type": "stdio"}}}}}}, "metadata": map[string]any{"nextCursor": "next"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"server": map[string]any{"name": "vendor/ready", "remotes": []any{map[string]any{"type": "streamable-http", "url": "https://example.org/mcp"}}}}}})
	}))
	defer endpoint.Close()
	settings.Sources = map[string]captypes.SourceConfig{"registry": {ID: "registry", Kind: "registry", Location: endpoint.URL}}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Discover(t.Context(), "fuzzy", captypes.KindMCP, 1)
	if err != nil || len(entries) != 1 || entries[0].Publisher != "vendor/ready" {
		t.Fatalf("ready next-page candidate=%+v err=%v", entries, err)
	}
}

// The official registry matches one substring of a server name, so a keyword
// query must reach it as separate terms and as names join them. An installed
// entry that matches one common word is not an answer: the registry is still
// asked, and entries no agent can install are not offered to agents.
func TestAgentSearchReachesTheRegistryByEachKeyword(t *testing.T) {
	service, settings, sourcePath := capabilityFixture(t)
	settings.Skills["installed"] = captypes.SkillConfig{ID: "installed", Path: sourcePath, Name: "review"}
	var mu sync.Mutex
	asked := map[string]bool{}
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		term := r.URL.Query().Get("search")
		mu.Lock()
		asked[term] = true
		mu.Unlock()
		if r.URL.Query().Get("version") != "latest" {
			t.Errorf("registry was asked for every version of %q", term)
		}
		var servers []any
		if term == "computer-use" {
			servers = append(servers, map[string]any{"server": map[string]any{"name": "io.github.example/computer-use-omni", "title": "Computer use", "version": "1.0.0", "description": "See the desktop, click and type",
				"packages": []any{map[string]any{"registryType": "pypi", "identifier": "computer-use-omni", "version": "1.0.0", "transport": map[string]any{"type": "stdio"}}}}})
		}
		if term == "desktop" {
			servers = append(servers,
				map[string]any{"server": map[string]any{"name": "com.example/quickbooks-desktop", "version": "2", "description": "Accounting data",
					"remotes": []any{map[string]any{"type": "streamable-http", "url": "https://example.org/books"}}}},
				map[string]any{"server": map[string]any{"name": "com.example/desktop-bundle", "version": "3",
					"packages": []any{map[string]any{"registryType": "mcpb", "identifier": "bundle", "version": "3", "transport": map[string]any{"type": "stdio"}}}}})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": servers})
	}))
	defer registry.Close()
	settings.Sources["registry"] = captypes.SourceConfig{ID: "registry", Name: "Registry", Kind: "registry", Location: registry.URL, Trusted: true}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), captypes.RuntimeActor{Effects: []captypes.Effect{captypes.EffectRead}}, captypes.RuntimeSnapshot{}, "capability_search",
		map[string]any{"query": "review computer use desktop"})
	var found captypes.SearchResult
	if err != nil || json.Unmarshal([]byte(result.Content[0].Text), &found) != nil {
		t.Fatalf("search: %+v %v", result, err)
	}
	for _, term := range []string{"computer-use", "use-desktop", "computer", "desktop"} {
		if !asked[term] {
			t.Errorf("registry was not asked for %q; asked %v", term, asked)
		}
	}
	if len(found.Results) < 3 || found.Results[0].ID != "installed" || !found.Results[0].Installed {
		t.Fatalf("installed entry is not first or the registry was skipped: %+v", found.Results)
	}
	if found.Results[1].Name != "Computer use" || found.Results[1].State != "not installed" || found.Results[1].Package != "pypi" {
		t.Fatalf("the server matching most keywords is not ranked first among discoveries: %+v", found.Results)
	}
	for _, hit := range found.Results {
		if strings.Contains(hit.Name, "bundle") || hit.State == "unsupported" {
			t.Fatalf("an entry no agent can install was offered: %+v", hit)
		}
	}
	if strings.Contains(result.Content[0].Text, `"Digest"`) || strings.Contains(result.Content[0].Text, `"Popularity"`) {
		t.Fatalf("search result carries fields an agent cannot use: %s", result.Content[0].Text)
	}
}
