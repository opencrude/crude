package workspace

import (
	"testing"

	model "crude/types"
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
