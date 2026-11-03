package workspace

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/insight/catalog"
	"crude/toolstore"
)

func TestManagedToolRequiresExactCatalogIdentity(t *testing.T) {
	d := catalog.Download{Package: "pkg", Version: "1", Source: "source", URL: "https://example.test/tool", Archive: "zip", Bin: "bin/tool", Command: "tool"}
	tool := toolstore.Tool{Package: "pkg", Version: "1", Source: "source", URL: "https://example.test/tool", Archive: "zip", Bin: "bin/tool", Command: "tool"}
	if !managedToolMatches(tool, d) {
		t.Fatal("matching artifact was rejected")
	}
	for name, change := range map[string]func(*toolstore.Tool){
		"package": func(v *toolstore.Tool) { v.Package = "other" },
		"version": func(v *toolstore.Tool) { v.Version = "2" },
		"source":  func(v *toolstore.Tool) { v.Source = "other" },
		"url":     func(v *toolstore.Tool) { v.URL += "?other" },
		"archive": func(v *toolstore.Tool) { v.Archive = "raw" },
		"bin":     func(v *toolstore.Tool) { v.Bin = "other/tool" },
		"command": func(v *toolstore.Tool) { v.Command = "other" },
	} {
		t.Run(name, func(t *testing.T) {
			changed := tool
			change(&changed)
			if managedToolMatches(changed, d) {
				t.Fatal("different artifact was selected")
			}
		})
	}
}

func TestLocateToolDoesNotUseMatchingBasenameFromAnotherPackage(t *testing.T) {
	t.Setenv("PATH", "")
	root := t.TempDir()
	digest := strings.Repeat("a", 64)
	dir := filepath.Join(root, "wrong-package", "1-"+digest[:12])
	bin := filepath.Join(dir, "files", "bin", "tool")
	if err := os.MkdirAll(filepath.Dir(bin), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bin, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"id": "wrong-package/1-" + digest[:12], "package": "wrong-package",
		"version": "1", "sha256": digest, "digest_origin": "first-download",
		"executable": "bin/tool", "command": "tool", "archive": "zip",
		"source": "source", "url": "https://example.test/tool",
		"installed_at": "2026-01-01T00:00:00Z", "installed_by": "user", "approval": "review",
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := toolstore.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	d := catalog.Download{Package: "expected", Version: "1", Source: "source", URL: "https://example.test/tool", Archive: "zip", Bin: "bin/tool", Command: "tool"}
	if path, _ := locateTool(t.Context(), "tool", t.TempDir(), store, &d); path != "" {
		t.Fatalf("selected unrelated artifact %q", path)
	}
	d.Package = "wrong-package"
	if path, source := locateTool(t.Context(), "tool", t.TempDir(), store, &d); path != bin || source != "tool-store" {
		t.Fatalf("matching artifact = %q, %q", path, source)
	}
}
