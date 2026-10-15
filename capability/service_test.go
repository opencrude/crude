package capability

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"crude/skills"
	captypes "crude/types/capability"
)

func capabilityFixture(t *testing.T) (*Service, captypes.Settings, string) {
	t.Helper()
	root := t.TempDir()
	sourcePath := filepath.Join(root, "catalog")
	if err := os.Mkdir(sourcePath, 0700); err != nil {
		t.Fatal(err)
	}
	skill, err := skills.Create(filepath.Join(sourcePath, "review"), "review", "Review exact evidence before changing a file")
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "capabilities.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	service, err := New(root, filepath.Join(t.TempDir(), "artifacts"), db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(service.Close)
	settings := captypes.Normalize(captypes.Settings{Sources: map[string]captypes.SourceConfig{"team": {ID: "team", Name: "Team", Kind: "local", Location: sourcePath, Trusted: true, Scope: captypes.ScopeWorkspace}}})
	if err := service.Configure(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	service.SetRegistrationStore(newTestRegistrationStore(t, db, settings))
	return service, settings, skill.Path
}

func TestCapabilityApprovalLocalAcquisitionAndPassiveResume(t *testing.T) {
	service, settings, source := capabilityFixture(t)
	ctx := context.Background()
	entries, err := service.Search(ctx, "review", captypes.KindSkill, 10, true)
	if err != nil || len(entries) != 1 {
		t.Fatalf("discovery: %#v %v", entries, err)
	}
	actor := captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}}
	_, err = service.Execute(ctx, actor, captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": entries[0].ID})
	var approval *captypes.ApprovalRequired
	if !errors.As(err, &approval) || len(service.Snapshot().Entries) != 0 {
		t.Fatalf("acquired without approval: %v", err)
	}
	approved := approval.CapabilityApprovalContext(ctx)
	result, err := service.Execute(approved, actor, captypes.RuntimeSnapshot{}, "capability_activate", map[string]any{"id": entries[0].ID})
	if err != nil || len(result.Content) != 1 || !strings.Contains(result.Content[0].Text, "source instructions") {
		t.Fatalf("activation: %#v %v", result, err)
	}
	installed := service.Snapshot().Settings.Skills[entries[0].ID]
	if !installed.Managed || installed.Path == source || installed.Digest != entries[0].Digest {
		t.Fatalf("not an immutable selected copy: %#v", installed)
	}
	store := service.store.(*testRegistrationStore)
	if err := service.Configure(ctx, store.load(t)); err != nil {
		t.Fatal(err)
	}
	if len(service.Snapshot().Entries) != 1 {
		t.Fatal("selected installation was not durable")
	}
	denied := actor
	denied.DeniedTools = []string{"capability_activate"}
	if _, err := service.Activate(ctx, denied, entries[0].ID, true); err == nil {
		t.Fatal("explicit user invocation widened denied tools")
	}
	if err := os.WriteFile(filepath.Join(installed.Path, "SKILL.md"), []byte("---\nname: review\ndescription: Altered package\n---\nchanged"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Activate(ctx, actor, entries[0].ID, true); err == nil {
		t.Fatal("altered managed bytes activated")
	}
	if err := service.Configure(ctx, settings); err != nil || len(service.Snapshot().Entries) != 0 {
		t.Fatalf("removed selection resurfaced: %v", err)
	}
}

func TestCapabilityApprovalPinsCandidateAndSourcePreimage(test *testing.T) {
	service, settings, _ := capabilityFixture(test)
	entries, err := service.Search(test.Context(), "review", captypes.KindSkill, 10, true)
	if err != nil || len(entries) != 1 {
		test.Fatalf("discovery: %+v %v", entries, err)
	}
	actor := captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}}
	_, err = service.Activate(test.Context(), actor, entries[0].ID, false)
	var approval *captypes.ApprovalRequired
	if !errors.As(err, &approval) {
		test.Fatal("exact approval was not requested")
	}
	source := settings.Sources["team"]
	source.Name = "Changed source owner"
	settings.Sources["team"] = source
	if err := service.Configure(test.Context(), settings); err != nil {
		test.Fatal(err)
	}
	if _, err := service.Activate(approval.CapabilityApprovalContext(test.Context()), actor, entries[0].ID, false); err == nil || !strings.Contains(err.Error(), "fresh exact approval") {
		test.Fatalf("old approval widened to changed source: %v", err)
	}
	if len(service.Snapshot().Entries) != 0 {
		test.Fatal("stale approval published an installed binding")
	}
}

func TestCapabilityConfigureIsMetadataOnlyAndInvocationIsIndependent(t *testing.T) {
	service, settings, source := capabilityFixture(t)
	if err := os.WriteFile(filepath.Join(source, "oversized-resource"), bytes.Repeat([]byte("x"), skills.MaxFileBytes+1), 0600); err != nil {
		t.Fatal(err)
	}
	settings.Skills["local:review"] = captypes.SkillConfig{ID: "local:review", Path: source, Mode: "manual"}
	if err := service.Configure(context.Background(), settings); err != nil || service.Snapshot().Entries[0].State != "available" {
		t.Fatalf("catalog loaded resource bodies: %v", err)
	}
	actor := captypes.RuntimeActor{ID: "root", Effects: []captypes.Effect{captypes.EffectRead}}
	if _, err := service.Activate(context.Background(), actor, "local:review", false); err == nil {
		t.Fatal("manual-only skill activated automatically")
	}
	if _, err := service.Activate(context.Background(), actor, "local:review", true); !errors.Is(err, skills.ErrLimit) {
		t.Fatalf("explicit activation did not validate resource limits: %v", err)
	}
	if err := os.Remove(filepath.Join(source, "oversized-resource")); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Activate(context.Background(), actor, "local:review", true); err != nil {
		t.Fatal(err)
	}
	actor.SkillModes = map[string]string{"local:review": "disabled"}
	if _, err := service.Activate(context.Background(), actor, "local:review", true); err == nil {
		t.Fatal("explicit invocation bypassed disabled agent mode")
	}
}

func TestCapabilityAcquisitionTrustAndChangedLocalSource(t *testing.T) {
	service, settings, source := capabilityFixture(t)
	ctx := context.Background()
	entries, err := service.Search(ctx, "review", captypes.KindSkill, 10, true)
	if err != nil {
		t.Fatal(err)
	}
	untrusted := settings.Sources["team"]
	untrusted.Trusted = false
	settings.Sources["team"] = untrusted
	if err := service.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Acquire(ctx, entries[0].ID, captypes.ScopeWorkspace); err == nil {
		t.Fatal("untrusted acquisition succeeded")
	}
	untrusted.Trusted = true
	settings.Sources["team"] = untrusted
	if err := service.Configure(ctx, settings); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "added"), []byte("different revision"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Acquire(ctx, entries[0].ID, captypes.ScopeWorkspace); err == nil {
		t.Fatal("changed source accepted without new discovery")
	}
}

type archiveFixtureEntry struct {
	name string
	body string
	kind byte
}

func skillArchive(t *testing.T, entries ...archiveFixtureEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		if err := archive.WriteHeader(&tar.Header{Name: entry.name, Size: int64(len(entry.body)), Typeflag: kind, Mode: 0644}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestCapabilityArchiveRejectsUnsafePathsAndIgnoresUnselectedLinks(t *testing.T) {
	for _, entry := range []archiveFixtureEntry{{name: "repo/elsewhere/../../escape"}, {name: "/absolute"}, {name: "repo/other\\escape"}} {
		t.Run(entry.name, func(t *testing.T) {
			if err := extractSkill(context.Background(), skillArchive(t, entry), "skills/review", t.TempDir(), 1024); err == nil {
				t.Fatal("unsafe unselected entry escaped validation")
			}
		})
	}
	if err := extractSkill(context.Background(), skillArchive(t,
		archiveFixtureEntry{name: "repo/other/link", kind: tar.TypeSymlink},
		archiveFixtureEntry{name: "repo/skills/review/SKILL.md", body: "---\nname: review\ndescription: Review evidence\n---\nUse exact versions.\n"},
	), "skills/review", t.TempDir(), 1024); err != nil {
		t.Fatalf("safe unselected link blocked the selected skill: %v", err)
	}
	if err := extractSkill(context.Background(), skillArchive(t,
		archiveFixtureEntry{name: "repo/skills/review/link", kind: tar.TypeSymlink},
	), "skills/review", t.TempDir(), 1024); err == nil {
		t.Fatal("selected skill link was accepted")
	}
	if err := extractSkill(context.Background(), skillArchive(t), "../bad", t.TempDir(), 1024); err == nil {
		t.Fatal("unsafe subtree accepted")
	}
}

func TestCapabilityConcurrentAcquireDetachesCancellation(t *testing.T) {
	service, _, _ := capabilityFixture(t)
	archive := skillArchive(t, archiveFixtureEntry{name: "repo/skills/review/SKILL.md", body: "---\nname: review\ndescription: Review evidence\n---\nUse exact versions.\n"})
	started, release := make(chan struct{}, 1), make(chan struct{})
	var downloads atomic.Int32
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		downloads.Add(1)
		started <- struct{}{}
		select {
		case <-release:
			_, _ = writer.Write(archive)
		case <-request.Context().Done():
		}
	}))
	defer endpoint.Close()
	service.candidates = map[string]Candidate{"team:review": {Entry: captypes.Entry{ID: "team:review", Name: "review", Kind: captypes.KindSkill, Source: "team", Version: "pinned"}, ArchiveURL: endpoint.URL, Subpath: "skills/review"}}
	cancelled, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := service.Acquire(cancelled, "team:review", captypes.ScopeWorkspace); first <- err }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("download never started")
	}
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled requester: %v", err)
	}
	close(release)
	settings, err := service.Acquire(context.Background(), "team:review", captypes.ScopeWorkspace)
	if err != nil || downloads.Load() != 1 || settings.Skills["team:review"].Digest == "" {
		t.Fatalf("shared artifact corrupted: downloads=%d %v", downloads.Load(), err)
	}
}

func TestCapabilityRegistryAcquisitionProducesValidStableConnection(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"servers": []any{
					map[string]any{"server": map[string]any{"name": "vendor/service", "version": "1", "remotes": []any{map[string]any{"type": "streamable-http", "url": "https://example.test/mcp"}}}, "_meta": map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "active", "isLatest": false}}},
					map[string]any{"server": map[string]any{"name": "vendor/local-only", "version": "1"}},
				},
				"metadata": map[string]any{"nextCursor": "next-page"},
			})
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"servers": []any{map[string]any{
				"server": map[string]any{"name": "vendor/service", "title": "Vendor Service", "description": "Read vendor data", "version": "2", "remotes": []any{map[string]any{"type": "streamable-http", "url": "https://example.test/mcp"}}},
				"_meta":  map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "active", "isLatest": true}},
			}},
		})
	}))
	defer endpoint.Close()
	settings.Sources = map[string]captypes.SourceConfig{"registry": {ID: "registry", Name: "Registry", Kind: "registry", Location: endpoint.URL, Trusted: true}}
	if err := service.Configure(context.Background(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Search(context.Background(), "", captypes.KindMCP, 10, true)
	if err != nil || len(entries) != 2 || entries[0].Name != "Vendor Service" || entries[0].Version != "2" || entries[1].State != "unsupported" || entries[1].Reason == "" {
		t.Fatalf("registry: %#v %v", entries, err)
	}
	acquired, err := service.Acquire(context.Background(), entries[0].ID, captypes.ScopeWorkspace)
	if err != nil || acquired.Connections[entries[0].ID].ID != entries[0].ID {
		t.Fatalf("unusable installed identity: %#v %v", acquired.Connections, err)
	}
}

func TestCapabilityRegistryFindsPinnedPackageServersThatNeedSetup(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("search") != "ghidra" {
			t.Errorf("search query=%q", request.URL.Query().Get("search"))
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"servers": []any{map[string]any{
				"server": map[string]any{
					"name": "io.github.example/ghidra-retro-mcp", "title": "Ghidra Retro MCP",
					"description": "Headless reverse engineering", "version": "1.0.0",
					"repository": map[string]any{"url": "https://github.com/example/ghidra-retro-mcp", "source": "github"},
					"packages": []any{map[string]any{
						"registryType": "pypi", "identifier": "ghidra-retro-mcp", "version": "1.0.0",
						"transport": map[string]any{"type": "stdio"},
						"environmentVariables": []any{map[string]any{
							"name": "GHIDRA_INSTALL_DIR", "isRequired": true, "description": "Ghidra installation",
						}},
					}},
				},
				"_meta": map[string]any{"io.modelcontextprotocol.registry/official": map[string]any{"status": "active", "isLatest": true}},
			}},
		})
	}))
	defer endpoint.Close()
	settings.Sources = map[string]captypes.SourceConfig{"registry": {
		ID: "registry", Name: "Registry", Kind: "registry", Location: endpoint.URL, Trusted: true,
	}}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Discover(t.Context(), "ghidra", captypes.KindMCP, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("package discovery: %#v %v", entries, err)
	}
	entry := entries[0]
	if entry.Name != "Ghidra Retro MCP" || entry.Transport != "local package" ||
		entry.PackageType != "pypi" || entry.State != "setup required" {
		t.Fatalf("package entry=%+v", entry)
	}
	preview, err := service.PreviewAcquisition(t.Context(), entry.ID, captypes.ScopeWorkspace, "install")
	if err != nil || !strings.Contains(strings.Join(preview.Requirements, "\n"), "GHIDRA_INSTALL_DIR") {
		t.Fatalf("package preview=%+v %v", preview, err)
	}
	acquired, err := service.AcquireReviewed(t.Context(), preview)
	if err != nil {
		t.Fatal(err)
	}
	connection := acquired.Connections[entry.ID]
	if connection.Command != "uvx" || len(connection.Args) != 1 ||
		connection.Args[0] != "ghidra-retro-mcp==1.0.0" ||
		len(connection.RequiredEnv) != 1 || connection.RequiredEnv[0] != "GHIDRA_INSTALL_DIR" ||
		connection.LaunchApproved {
		t.Fatalf("package connection=%+v", connection)
	}
}

func TestSkillsDirectoryAddsBroadRankedGitHubResults(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("q") != "ghidra" {
			t.Errorf("query=%q", request.URL.Query().Get("q"))
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{"skills": []any{
			map[string]any{
				"id": "trailofbits/skills-curated/ghidra-headless", "source": "trailofbits/skills-curated",
				"skillId": "ghidra-headless", "name": "ghidra-headless", "installs": 419,
			},
			map[string]any{"id": "not-a-github-source/skill", "source": "skills.example", "skillId": "skill", "name": "skill"},
		}})
	}))
	defer endpoint.Close()
	settings.Sources = map[string]captypes.SourceConfig{"directory": {
		ID: "directory", Name: "Skills Directory", Kind: "skill-registry", Location: endpoint.URL, Trusted: true,
	}}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Discover(t.Context(), "ghidra", captypes.KindSkill, 10)
	if err != nil || len(entries) != 1 {
		t.Fatalf("skill directory: %#v %v", entries, err)
	}
	if entries[0].Name != "ghidra-headless" || entries[0].Popularity != 419 ||
		entries[0].Repository != "https://github.com/trailofbits/skills-curated" {
		t.Fatalf("skill directory entry=%+v", entries[0])
	}
}

func TestFindSkillSubpathPrefersTheShallowExactDirectory(t *testing.T) {
	entries := []struct {
		Path string `json:"path"`
		Type string `json:"type"`
	}{
		{Path: "examples/ghidra/SKILL.md", Type: "blob"},
		{Path: "skills/security/ghidra/SKILL.md", Type: "blob"},
		{Path: "skills/other/SKILL.md", Type: "blob"},
	}
	path, err := findSkillSubpath(entries, "ghidra")
	if err != nil || path != "examples/ghidra" {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

func TestLiveDefaultCapabilitySources(t *testing.T) {
	if os.Getenv("CRUDE_LIVE_CAPABILITY_SOURCES") != "1" {
		t.Skip("set CRUDE_LIVE_CAPABILITY_SOURCES=1 for pinned public source acceptance")
	}
	service, _, _ := capabilityFixture(t)
	settings := captypes.WithDefaults(captypes.Settings{})
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	skills, err := service.Discover(t.Context(), "pdf", captypes.KindSkill, 10)
	if err != nil || len(skills) == 0 || skills[0].Source != captypes.AnthropicSkillsSourceID {
		t.Fatalf("Anthropic source: %#v %v", skills, err)
	}
	acquiredSkills, err := service.Acquire(t.Context(), skills[0].ID, captypes.ScopeWorkspace)
	if err != nil || acquiredSkills.Skills[skills[0].ID].Digest == "" {
		t.Fatalf("Anthropic skill acquisition: %#v %v", acquiredSkills.Skills, err)
	}
	servers, err := service.Discover(t.Context(), "docs", captypes.KindMCP, 10)
	if err != nil || len(servers) == 0 || servers[0].Source != captypes.OfficialMCPRegistrySourceID {
		t.Fatalf("MCP Registry source: %#v %v", servers, err)
	}
	acquired, err := service.Acquire(t.Context(), servers[0].ID, captypes.ScopeWorkspace)
	if err != nil || acquired.Connections[servers[0].ID].URL == "" {
		t.Fatalf("MCP Registry acquisition: %#v %v", acquired.Connections, err)
	}
}
