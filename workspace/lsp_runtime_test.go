package workspace

import (
	"errors"
	"testing"

	model "crude/types"

	"github.com/charmbracelet/x/powernap/pkg/lsp"
)

func TestDefaultLSPRuntimeConstructionIsInert(t *testing.T) {
	client := (defaultLSPRuntime{}).NewClient(model.LSPServerSpec{
		Command:               "nonexistent-lsp-server-12345",
		Args:                  []string{"--stdio"},
		Environment:           map[string]string{"CRUDE_LSP_TEST": "1"},
		Settings:              map[string]any{"test": true},
		InitializationOptions: map[string]any{"option": true},
	}, "file:///unused", func() error { return nil })
	t.Cleanup(func() {
		if err := client.Stop(); err != nil {
			t.Error(err)
		}
	})
	concrete := client.(*runtimeLSPClient)
	if concrete.ServerCmd() != "nonexistent-lsp-server-12345" || concrete.RootURI() != "file:///unused" {
		t.Fatal("runtime lost server identity")
	}
	if client.Done() != nil {
		t.Fatal("constructing a client started its transport")
	}
	if err := client.Start(t.Context()); err == nil {
		t.Fatal("explicit startup did not attempt configured server")
	}
}

func TestDefaultLSPRuntimeForwardsRootBoundConfiguration(t *testing.T) {
	admissions := 0
	client := (defaultLSPRuntime{}).NewClient(model.LSPServerSpec{
		Command: "never-construct-this-program",
		ConfigurationScopes: []model.LSPConfigurationScope{{
			URI: "file:///foreign/module", Settings: map[string]any{"engine": "must-not-launch"},
		}},
	}, "file:///project", func() error { admissions++; return nil })
	if err := client.Start(t.Context()); !errors.Is(err, lsp.ErrConfiguration) {
		t.Fatalf("workspace runtime dropped scoped launch configuration: %v", err)
	}
	if admissions != 0 || client.Done() != nil {
		t.Fatal("invalid configuration crossed admission or constructed a transport")
	}
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultLSPRuntimeRefusesUnsafeEnvironmentBeforeAdmission(t *testing.T) {
	admissions := 0
	client := (defaultLSPRuntime{}).NewClient(model.LSPServerSpec{
		Command: "never-construct-this-program", Environment: map[string]string{"PATH": "."},
	}, "file:///project", func() error { admissions++; return nil })
	if err := client.Start(t.Context()); !errors.Is(err, lsp.ErrProcessEnvironment) {
		t.Fatalf("workspace runtime dropped environment validation: %v", err)
	}
	if admissions != 0 || client.Done() != nil {
		t.Fatal("unsafe environment crossed admission or constructed a transport")
	}
	if err := client.Stop(); err != nil {
		t.Fatal(err)
	}
}
