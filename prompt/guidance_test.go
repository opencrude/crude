package prompt

import (
	"context"
	"encoding/xml"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuidanceReadsOnlyNamedFilesInDeterministicPrecedence(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "nested")
	if err := os.Mkdir(nested, 0700); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"SOUL.md":          "root persona",
		"CLAUDE.md":        "root model guidance",
		"AGENTS.md":        "root repository policy",
		"README.md":        "MUST-NOT-BE-AUTO-LOADED",
		"nested/SOUL.md":   "nested persona",
		"nested/AGENTS.md": "nested repository policy",
		"nested/DESIGN.md": "MUST-NOT-BE-AUTO-LOADED-EITHER",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(name)), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	set, err := DiscoverGuidance(context.Background(), root, []string{filepath.Join(nested, "work.go")})
	if err != nil {
		t.Fatal(err)
	}
	documents := set.Documents()
	if len(documents) != 5 {
		t.Fatalf("documents=%d want five named guidance files", len(documents))
	}
	var contents []string
	for _, document := range documents {
		contents = append(contents, document.Content)
	}
	want := []string{"root persona", "root model guidance", "root repository policy", "nested persona", "nested repository policy"}
	if strings.Join(contents, "|") != strings.Join(want, "|") {
		t.Fatalf("guidance precedence=%q want %q", contents, want)
	}
	projection := set.Verbatim()
	if strings.Contains(projection, "MUST-NOT-BE-AUTO-LOADED") {
		t.Fatal("ordinary referenced documentation became mandatory prompt cargo")
	}
	for _, document := range documents {
		if !strings.Contains(projection, document.SHA256) || !strings.Contains(projection, document.Content) {
			t.Fatalf("verbatim projection omitted complete source %s", document.Path)
		}
	}
}

func TestCanonicalBehaviorPromptDoesNotForkByModel(t *testing.T) {
	options := RootOptions{MultiAgent: true, WorkDir: "/workspace", Model: "gpt-6-sol"}
	gpt := BuildRoot(options)
	options.Model = "claude-opus"
	claude := BuildRoot(options)
	options.Model = "gemini-pro"
	gemini := BuildRoot(options)
	if gpt != claude || gpt != gemini {
		t.Fatal("model family changed canonical behavioral policy")
	}
	for _, required := range []string{"Tool names, schemas", "provider-native patch", "bounded complete-source chunks", "not mandatory prompt cargo"} {
		if !strings.Contains(gpt, required) {
			t.Errorf("canonical prompt missing %q", required)
		}
	}
	profile := ProfileInstructions("Prefer <small> & direct changes")
	if !strings.Contains(profile, "priority=\"below_current_human_and_project_policy\"") ||
		!strings.Contains(profile, "&lt;small&gt; &amp; direct") {
		t.Fatalf("editable profile overlay is not separated and escaped: %q", profile)
	}
	composed := ApplyRootOverlays(gpt, "Prefer <small> & direct changes", "Aster", `<available_agent_profiles></available_agent_profiles>`)
	if !strings.HasSuffix(composed, "</crude_agent>") || strings.Count(composed, "</crude_agent>") != 1 ||
		!strings.Contains(composed, profile) || !strings.Contains(composed, "<agent_identity>Your name is Aster.</agent_identity>") {
		t.Fatalf("runtime overlays escaped the canonical root envelope: %q", composed)
	}
	var document struct{ XMLName xml.Name }
	if err := xml.Unmarshal([]byte(composed), &document); err != nil || document.XMLName.Local != "crude_agent" {
		t.Fatalf("composed root prompt is not one valid XML document: %v", err)
	}
}

func TestGuidanceRefreshIsContentBasedAndTransactional(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "AGENTS.md")
	if err := os.WriteFile(path, []byte("original rule"), 0600); err != nil {
		t.Fatal(err)
	}
	set, err := DiscoverGuidance(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	check := func(wantChanged bool, want string) {
		t.Helper()
		changed, err := set.AddPaths(t.Context(), []string{path, root})
		if err != nil || changed != wantChanged {
			t.Fatalf("refresh: changed=%v want=%v err=%v", changed, wantChanged, err)
		}
		docs := set.Documents()
		if want == "" {
			if len(docs) != 0 || set.TotalBytes() != 0 || !strings.Contains(set.Verbatim(), `complete="true"`) {
				t.Fatal("empty current snapshot did not revoke deleted guidance")
			}
		} else if len(docs) != 1 || docs[0].Content != want || set.TotalBytes() != len(want) {
			t.Fatalf("current guidance=%+v bytes=%d", docs, set.TotalBytes())
		}
	}
	before := set.Verbatim()
	check(false, "original rule")
	if set.Verbatim() != before {
		t.Fatal("unchanged reread changed the cached prompt bytes")
	}
	if err := os.WriteFile(path, []byte("modified rule"), 0600); err != nil {
		t.Fatal(err)
	}
	check(true, "modified rule")
	before = set.Verbatim()
	if err := os.Mkdir(filepath.Join(root, "CLAUDE.md"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := set.AddPaths(t.Context(), []string{root}); err == nil || set.Verbatim() != before {
		t.Fatal("invalid guidance partially changed the accepted snapshot")
	}
	if err := os.Remove(filepath.Join(root, "CLAUDE.md")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	check(true, "")
	check(false, "")
	if err := os.WriteFile(path, []byte("new rule"), 0600); err != nil {
		t.Fatal(err)
	}
	check(true, "new rule")
	check(false, "new rule")
}

func TestGuidanceManifestIsIndependentOfPathDiscoveryOrder(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"a", "b"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "AGENTS.md"), []byte(dir+" rules"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	first, err := DiscoverGuidance(t.Context(), root, []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := DiscoverGuidance(t.Context(), root, []string{"b", "a", "a"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Verbatim() != second.Verbatim() || first.Digest() != second.Digest() {
		t.Fatal("the same sources produced different cache prefixes")
	}
}

func TestGuidanceIntakeNoticeFollowsVerbatimAndCompiledContent(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte("Read this file before starting."), 0600); err != nil {
		t.Fatal(err)
	}
	set, err := DiscoverGuidance(t.Context(), root, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, projection := range []string{set.Verbatim(), CompiledGuidance(set.Digest(), set.TotalBytes(), set.Manifest(), "Read this file before starting.")} {
		var document struct {
			Version  string `xml:"version,attr"`
			Complete bool   `xml:"complete,attr"`
			Status   string `xml:"intake_status"`
		}
		if err := xml.Unmarshal([]byte(projection), &document); err != nil {
			t.Fatal(err)
		}
		if document.Version != "1" || !document.Complete || !strings.Contains(document.Status, "already loaded") ||
			strings.Index(projection, "<intake_status>") < strings.LastIndex(projection, "Read this file before starting.") {
			t.Fatal("guidance representation did not explicitly complete source startup requirements")
		}
	}
}

func BenchmarkGuidanceRefreshUnchanged(b *testing.B) {
	root := b.TempDir()
	if err := os.WriteFile(filepath.Join(root, "AGENTS.md"), []byte(strings.Repeat("complete project policy\n", 3000)), 0600); err != nil {
		b.Fatal(err)
	}
	set, err := DiscoverGuidance(b.Context(), root, nil)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if changed, err := set.AddPaths(b.Context(), []string{root}); err != nil || changed {
			b.Fatalf("unchanged refresh: changed=%v err=%v", changed, err)
		}
	}
}
