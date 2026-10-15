package core

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"crude/coreprotocol"
	"crude/types"
	captypes "crude/types/capability"
	"crude/workspace"
)

func TestCapabilityFailurePreservesReasonWithoutPublishing(t *testing.T) {
	ws := workspace.NewWorkspace(t.TempDir(), "", "tester", types.Settings{}, coreTestLogger{})
	defer ws.Close()
	if _, err := ws.CapabilitySnapshot(t.Context()); err != nil {
		t.Fatal(err)
	}
	a := &actor{ws: ws}
	fetched := fetchSettingsForTest(t, a)
	in := coreprotocol.CapabilityActionRequest{ExpectedRevision: fetched.CapabilitiesRevision,
		Action: captypes.Action{Operation: "preview-install", ID: "missing:skill", Kind: captypes.KindSkill, Scope: captypes.ScopeApplication}}
	raw, _ := json.Marshal(in)
	run, apply, _ := a.prepareApplicationSettings(coreprotocol.Request{Method: coreprotocol.CapabilityAction, Payload: raw})
	out := run(context.Background())
	if out.Error == nil || out.Error.Code != "capability_failed" || !strings.Contains(out.Error.Message, "refresh Discover") {
		t.Fatalf("lost actionable acquisition diagnostic: %+v", out.Error)
	}
	apply(out)
	if got := fetchSettingsForTest(t, a); got.CapabilitiesRevision != fetched.CapabilitiesRevision {
		t.Fatal("failed preview published settings")
	}
}

func TestCapabilityDiagnosticsRedactPrivateOperands(t *testing.T) {
	settings := types.AppSettings{Capabilities: captypes.Normalize(captypes.Settings{
		Sources: map[string]captypes.SourceConfig{"private": {Location: "https://example.test/catalog?token=source-secret"}},
		Connections: map[string]captypes.ConnectionConfig{"private": {
			URL: "https://example.test/mcp?token=endpoint-secret", Args: []string{"argument-secret"},
			Environment: map[string]string{"TOKEN": "environment-secret"}, Headers: map[string]string{"Authorization": "header-secret"},
		}},
	})}
	settings.Network.ProxyURL = "http://user:proxy-secret@localhost:8000"
	err := errors.New("download failed https://example.test/catalog?token=source-secret through http://user:proxy-secret@localhost:8000 endpoint-secret argument-secret environment-secret header-secret")
	text := publicCapabilityFailure(err, settings, captypes.Action{})
	if !strings.Contains(text, "download failed") || !strings.Contains(text, "https://example.test/catalog") {
		t.Fatalf("diagnostic became unactionable: %s", text)
	}
	if strings.Contains(text, "-secret") {
		t.Fatalf("private operands escaped into diagnostics: %s", text)
	}
}
