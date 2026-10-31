package lspclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"crude/lspedit"
	"crude/types"
)

func renameProtocolResponse(mode, uri string) string {
	if mode == "rename-null" || mode == "rename-stale-null" {
		return "null"
	}
	version := `"version":0,`
	switch mode {
	case "rename-version-null":
		version = `"version":null,`
	case "rename-version-missing":
		version = ""
	}
	replacement := "new"
	if mode == "rename-oversize" {
		replacement = strings.Repeat("x", lspedit.MaxRawBytes)
	}
	return fmt.Sprintf(`{"documentChanges":[{"textDocument":{%s"uri":%q},"edits":[{"range":{"start":{"line":0,"character":3},"end":{"line":0,"character":6}},"newText":%q}]}]}`, version, uri, replacement)
}

func TestRenameProtocolRetainsOriginalNullableVersionAndUTF16Source(t *testing.T) {
	for _, mode := range []string{"rename-exact", "rename-version-null", "rename-version-missing", "rename-null", "rename-oversize"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			integration := protocolIntegration(t, mode, root)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := integration.Start(ctx); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "held-out.fixture")
			openTestFile(t, integration, ctx, path, "held-out", "a😀old\r\n")
			out, err := integration.RequestRename(ctx, path, 0, 3, "new")
			if mode == "rename-oversize" {
				if err == nil || len(out.JSON) != 0 {
					t.Fatal("oversized original edit escaped transport bounds")
				}
				return
			}
			if err != nil || out.WireVersion <= 0 || !reflect.DeepEqual(out.Boundary, types.InsightBoundary{}) {
				t.Fatalf("rename lost exact transport version or invented authority: %+v %v", out, err)
			}
			if mode == "rename-version-null" && !strings.Contains(string(out.JSON), `"version":null`) ||
				mode == "rename-exact" && !strings.Contains(string(out.JSON), `"version":0`) ||
				mode == "rename-version-missing" && strings.Contains(string(out.JSON), `"version"`) {
				t.Fatal("typed decode collapsed the original version token", string(out.JSON))
			}
			targets, parseErr := lspedit.Parse(out.JSON)
			if mode == "rename-version-missing" {
				if parseErr == nil {
					t.Fatal("missing identity became a valid null/zero version")
				}
			} else if mode == "rename-null" {
				if parseErr != nil || len(targets) != 0 {
					t.Fatal("supported no-change response lost its shape")
				}
			} else if parseErr != nil || len(targets) != 1 {
				t.Fatal(targets, parseErr)
			}
			if mode == "rename-exact" {
				if targets[0].Version == nil || *targets[0].Version != 0 {
					t.Fatal("explicit numeric zero was not retained")
				}
				text, err := lspedit.Apply("a😀old\r\n", targets[0].Edits)
				if err != nil || text != "a😀new\r\n" {
					t.Fatal("literal UTF-16 edit changed independent expected bytes", text, err)
				}
			}
			// Outgoing positions are wire UTF-16, never terminal columns. A
			// split surrogate must be refused before probing the provider.
			if _, err := integration.RequestRename(ctx, path, 0, 2, "new"); err == nil {
				t.Fatal("split-surrogate rename source reached provider")
			}
		})
	}
}

func TestRenameUnsupportedProviderDoesNotProbe(t *testing.T) {
	root := t.TempDir()
	integration := protocolIntegration(t, "normal", root)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := integration.Start(ctx); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "held-out.fixture")
	openTestFile(t, integration, ctx, path, "held-out", "old")
	if _, err := integration.RequestRename(ctx, path, 0, 0, "new"); err == nil {
		t.Fatal("unsupported rename became successful empty evidence")
	}
	trace, err := integration.RequestHover(ctx, path, 0, 0)
	if err != nil || strings.Contains(trace, "textDocument/rename") {
		t.Fatal("unsupported provider was probed", trace, err)
	}
}

func TestRenameReplyIncludingNullCannotSurviveEqualTextReopen(t *testing.T) {
	for _, mode := range []string{"rename-stale", "rename-stale-null"} {
		t.Run(mode, func(t *testing.T) {
			root := t.TempDir()
			integration := protocolIntegration(t, mode, root)
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			if err := integration.Start(ctx); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "held-out.fixture")
			openTestFile(t, integration, ctx, path, "held-out", "a😀old")
			done := make(chan error, 1)
			go func() { _, err := integration.RequestRename(ctx, path, 0, 3, "new"); done <- err }()
			waitProtocolFile(t, filepath.Join(root, "requested"))
			integration.CloseFile(ctx, path)
			openTestFile(t, integration, ctx, path, "held-out", "a😀old")
			if err := os.WriteFile(filepath.Join(root, "release"), nil, 0600); err != nil {
				t.Fatal(err)
			}
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal("equal-text reopen retained the former rename effect or negative evidence", err)
			}
		})
	}
}
