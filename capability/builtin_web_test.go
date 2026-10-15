package capability

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	captypes "crude/types/capability"
	"crude/webmcp"
)

func preparedNames(t *testing.T, service *Service, actor captypes.RuntimeActor) ([]string, captypes.RuntimeSnapshot) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	prepared, err := service.Prepare(ctx, actor)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range prepared.Tools {
		names = append(names, tool.Name)
	}
	return names, prepared
}

func TestBuiltinWebToolsReachAgentsWithoutActivationAndHonorUserSwitches(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	settings = captypes.WithDefaults(settings)
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", SessionID: "s", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead}}
	names, _ := preparedNames(t, service, actor)
	for _, want := range webmcp.ToolNames() {
		if !slices.Contains(names, want) {
			t.Fatalf("built-in web tool %q missing from %v", want, names)
		}
	}
	for _, name := range names {
		if strings.HasPrefix(name, "mcp__") {
			t.Fatalf("built-in tools must keep plain names: %v", names)
		}
	}

	researcher := actor
	researcher.ID, researcher.AllowedTools = "researcher", []string{"web_search", "read_file"}
	if names, _ := preparedNames(t, service, researcher); !slices.Equal(names, []string{"web_search"}) {
		t.Fatalf("allow-list must select built-in tools by plain name: %v", names)
	}

	off := actor
	off.ID, off.SessionDisabled = "off", []string{captypes.WebConnectionID}
	if names, _ := preparedNames(t, service, off); len(names) != 0 {
		t.Fatalf("session switch did not remove built-in tools: %v", names)
	}

	web := settings.Connections[captypes.WebConnectionID]
	web.Grants = map[string]captypes.ToolGrant{"arxiv_search": {Disabled: true}}
	settings.Connections[captypes.WebConnectionID] = web
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if names, _ := preparedNames(t, service, actor); slices.Contains(names, "arxiv_search") || !slices.Contains(names, "web_fetch") {
		t.Fatalf("per-tool switch not honored: %v", names)
	}

	web.Disabled = true
	settings.Connections[captypes.WebConnectionID] = web
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	names, prepared := preparedNames(t, service, actor)
	if len(names) != 0 {
		t.Fatalf("disabled built-in still offered: %v", names)
	}
	if _, err := service.Execute(t.Context(), actor, prepared, "web_search", map[string]any{"query": "x"}); err == nil {
		t.Fatal("disabled built-in executed")
	}
	for _, entry := range service.Snapshot().Entries {
		if entry.ID == captypes.WebConnectionID && (entry.State != "disabled" || entry.Publisher != "Crude") {
			t.Fatalf("built-in entry=%+v", entry)
		}
	}
}

func TestSettingSecretsRestartsBuiltinAndReportsOnlyNames(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	settings = captypes.WithDefaults(settings)
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	actor := captypes.RuntimeActor{ID: "root", SessionID: "s", RunID: "run", Effects: []captypes.Effect{captypes.EffectRead}}
	preparedNames(t, service, actor)
	if err := service.SetSecrets(t.Context(), map[string]string{"EXA_API_KEY": "exa-secret"}); err != nil {
		t.Fatal(err)
	}
	snapshot := service.Snapshot()
	if !slices.Equal(snapshot.StoredSecrets, []string{"EXA_API_KEY"}) {
		t.Fatalf("stored secret names=%v", snapshot.StoredSecrets)
	}
	for _, state := range snapshot.Connections {
		if state.ID == captypes.WebConnectionID && state.State != "connected" {
			t.Fatalf("built-in did not come back after a secret change: %+v", state)
		}
	}
	if err := service.SetSecrets(t.Context(), nil); err == nil {
		t.Fatal("empty secret update accepted")
	}
}
