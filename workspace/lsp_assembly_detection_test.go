package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"crude/insight/catalog"
	"crude/third_party/pathwalk"
	model "crude/types"
)

func TestAssemblyDiscoveryIgnoreScope(t *testing.T) {
	root := t.TempDir() // No .git directory: ignore rules still apply.
	for path, text := range map[string]string{
		".gitignore":        "*.log\nignored/\n*.php\n!keep.php\n",
		"keep.php":          "<?php",
		"drop.php":          "<?php",
		"tmp/paste-123.log": "log",
		"ignored/source.go": "package ignored",
		"site/node_modules/flatted/php/flatted.php": "<?php",
		"build/generated.go":                        "package generated",
		"src/.gitignore":                            "!nested.php\nlocal.go\n",
		"src/nested.php":                            "<?php",
		"src/local.go":                              "package ignored",
		"src/main.go":                               "package main",
	} {
		writeTree(t, filepath.Join(root, filepath.FromSlash(path)), text)
	}
	found := discoverProjectLanguages(context.Background(), root, model.DefaultLSPSettings())
	if found.err != nil || found.limited {
		t.Fatalf("discovery failed or limited: %+v", found)
	}
	if found.seen != 3 {
		t.Fatalf("files seen = %d, want 3 first-party files", found.seen)
	}
	php := found.languages["php"]
	if php == nil || php.files != 2 || php.sample != filepath.Join(root, "keep.php") {
		t.Fatalf("first-party PHP evidence: %+v", php)
	}
	goFiles := found.languages["go"]
	if goFiles == nil || goFiles.files != 1 || goFiles.sample != filepath.Join(root, "src", "main.go") {
		t.Fatalf("first-party source evidence: %+v", goFiles)
	}
	for _, language := range found.languages {
		if strings.Contains(language.sample, "paste-") || strings.Contains(language.sample, "node_modules") {
			t.Fatalf("ignored file became language evidence: %+v", language)
		}
	}
}

func TestAssemblyDiscoveryDoesNotFollowIgnoreSymlink(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "rules")
	writeTree(t, outside, "*.php\n")
	writeTree(t, filepath.Join(root, "real.php"), "<?php")
	if err := os.Symlink(outside, filepath.Join(root, ".gitignore")); err != nil {
		t.Skipf("native symlinks unavailable: %v", err)
	}
	found := discoverProjectLanguages(context.Background(), root, model.DefaultLSPSettings())
	if found.err != nil || found.languages["php"] == nil {
		t.Fatalf("external ignore link changed project evidence: %+v", found)
	}
}

func TestAssemblyDiscoveryDoesNotFollowDirectorySymlink(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeTree(t, filepath.Join(outside, "external.php"), "<?php")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Skipf("native symlinks unavailable: %v", err)
	}
	found := discoverProjectLanguages(context.Background(), root, model.DefaultLSPSettings())
	if found.err != nil || len(found.languages) != 0 {
		t.Fatalf("external source link became project evidence: %+v", found)
	}
}

func TestAssemblyDiscoveryIgnoreBoundsAndCancellation(t *testing.T) {
	root := t.TempDir()
	writeTree(t, filepath.Join(root, "main.go"), "package main")
	writeTree(t, filepath.Join(root, ".gitignore"), strings.Repeat("#", assemblyIgnoreBytes+1))
	found := discoverProjectLanguages(context.Background(), root, model.DefaultLSPSettings())
	if found.err == nil {
		t.Fatal("oversized ignore input claimed complete discovery")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	found = discoverProjectLanguages(ctx, root, model.DefaultLSPSettings())
	if found.err == nil {
		t.Fatal("cancelled discovery claimed complete observation")
	}
}

func TestAssemblyDiscoveryListingErrorsAreNotComplete(t *testing.T) {
	ctx := context.Background()
	if err := assemblyListingError(ctx, pathwalk.Catalog{Skipped: 12}, nil); err != nil {
		t.Fatalf("intentional exclusions became failures: %v", err)
	}
	found := &assemblyDiscovery{err: assemblyListingError(ctx, pathwalk.Catalog{Errors: 1}, nil)}
	plan := resolveAssembly(t.TempDir(), model.DefaultLSPSettings(), found, func(string, *catalog.Download) (string, string) {
		t.Fatal("partial observation proceeded to resolve tools")
		return "", ""
	})
	if plan.State != model.AssemblyFailed || !strings.Contains(plan.Message, "could not list") {
		t.Fatalf("failed traversal claimed complete assembly: %+v", plan)
	}
}

func TestAssemblyDiscoveryReportsTruncatedListing(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		writeTree(t, filepath.Join(root, name), "package main")
	}
	found := discoverProjectLanguagesBounded(context.Background(), root, model.DefaultLSPSettings(), 1)
	if found.err != nil || !found.limited {
		t.Fatalf("truncated scan not reported: %+v", found)
	}
	plan := resolveAssembly(root, model.DefaultLSPSettings(), found, func(string, *catalog.Download) (string, string) {
		return "", ""
	})
	if !plan.Limited || !strings.Contains(plan.Message, "only part") {
		t.Fatalf("truncated plan not reported: %+v", plan)
	}
}
