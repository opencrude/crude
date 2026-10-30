package lspclient

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func TestLSPScopedConfigurationUsesActualServerRequests(t *testing.T) {
	root := t.TempDir()
	uri := func(rel string) string { return string(protocol.URIFromPath(filepath.Join(root, rel))) }
	global := map[string]any{"engine": map[string]any{"mode": "root", "rootOnly": true}}
	moduleA := map[string]any{"engine": map[string]any{"mode": "A"}}
	scopes := []ConfigurationScope{
		{URI: uri("module-a"), Settings: moduleA},
		{URI: uri("module-b"), Settings: map[string]any{"engine": map[string]any{"mode": "B"}}},
	}
	l := NewLSPIntegration(os.Args[0], []string{"-test.run=^TestProtocolServerHelper$"}, uri(""),
		WithServerConfiguration(map[string]string{"CRUDE_PROTOCOL_HELPER": "scoped-configuration"}, global, nil),
		WithConfigurationScopes(scopes))
	t.Cleanup(func() {
		if err := l.Stop(); err != nil {
			t.Error(err)
		}
	})
	// Neither Start nor a later server request may observe mutable caller drafts.
	global["engine"].(map[string]any)["mode"] = "mutated"
	moduleA["engine"].(map[string]any)["mode"] = "mutated"
	scopes[1].URI = uri("wrong-module")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "main.go")
	openTestFile(t, l, ctx, source, "go", "package fixture\n")
	var trace []map[string]any
	for range 3 {
		text, err := l.RequestHover(ctx, source, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(text), &trace); err != nil {
			t.Fatal(err)
		}
		for _, message := range trace {
			if message["id"] != "configuration" || message["method"] != nil {
				continue
			}
			result, err := json.Marshal(message["result"])
			if err != nil {
				t.Fatal(err)
			}
			if string(result) != `["A","B","root",null,null,"root"]` {
				t.Fatalf("server configuration request ignored scope or reused another module: %s", result)
			}
			return
		}
	}
	t.Fatal("server never received its ordered scope-specific configuration response")
}

func TestLSPInvalidConfigurationNeverAttemptsStartAdmission(t *testing.T) {
	calls := 0
	l := NewLSPIntegration("never-construct-this-program", nil, "file:///project",
		WithConfigurationScopes([]ConfigurationScope{{URI: "file:///foreign"}}),
		WithStartAdmission(func() error { calls++; return nil }))
	if err := l.Start(context.Background()); !errors.Is(err, lsp.ErrConfiguration) {
		t.Fatalf("invalid scope reached program construction: %v", err)
	}
	if calls != 0 {
		t.Fatal("invalid configuration reached runtime launch admission")
	}
	if err := l.Stop(); err != nil {
		t.Fatal(err)
	}
}
