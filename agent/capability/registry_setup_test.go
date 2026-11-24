package capability

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	captypes "crude/types/capability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestRegistryRequiredHeadersInstallThenConnectAfterSetup(t *testing.T) {
	service, settings, _ := capabilityFixture(t)
	var requests atomic.Int32
	server := mcp.NewServer(&mcp.Implementation{Name: "registry-fixture", Version: "1"}, nil)
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, nil)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer fixture" || r.Header.Get("X-Mode") != "read" {
			http.Error(w, "missing configured headers", http.StatusUnauthorized)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer endpoint.Close()
	defer service.Close()
	// Matches the required secret header declared by ae.easyvoice/tts in the
	// official registry. A fixed header tests the other half of conversion.
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": []any{map[string]any{"server": map[string]any{
			"name": "ae.easyvoice/tts", "version": "1.0.0", "remotes": []any{map[string]any{
				"type": "streamable-http", "url": endpoint.URL,
				"headers": []any{
					map[string]any{"name": "Authorization", "isRequired": true, "isSecret": true, "description": "Bearer API key from account/keys"},
					map[string]any{"name": "X-Mode", "value": "read"},
				},
			}},
		}}}})
	}))
	defer registry.Close()
	settings.Sources = map[string]captypes.SourceConfig{"registry": {ID: "registry", Kind: "registry", Location: registry.URL, Trusted: true}}
	if err := service.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := service.store.CommitRegistration(t.Context(), "", settings, settings); err != nil {
		t.Fatal(err)
	}
	entries, err := service.Discover(t.Context(), "easyvoice", captypes.KindMCP, 10)
	if err != nil || len(entries) != 1 || entries[0].State != "setup required" {
		t.Fatalf("required header disappeared at discovery: %+v %v", entries, err)
	}
	id := entries[0].ID
	preview, err := service.PreviewAcquisition(t.Context(), id, captypes.ScopeApplication, "install")
	if err != nil || !strings.Contains(strings.Join(preview.Requirements, "\n"), "account/keys") {
		t.Fatalf("missing setup guidance in preview: %+v %v", preview, err)
	}
	acquired, err := service.AcquireReviewed(t.Context(), preview)
	if err != nil {
		t.Fatalf("missing credentials blocked passive installation: %v", err)
	}
	if err := service.publishRegistration(t.Context(), id, settings, acquired); err != nil {
		t.Fatal(err)
	}
	durable := service.store.(*testRegistrationStore).load(t)
	if err := service.Configure(t.Context(), durable); err != nil {
		t.Fatal(err)
	}
	entry := service.Snapshot().Entries[0]
	if !entry.Installed || entry.State != "setup required" || !strings.Contains(entry.Reason, "Authorization") {
		t.Fatalf("setup state lost across reload: %+v", entry)
	}
	if err := service.Connect(t.Context(), id); err == nil || !strings.Contains(err.Error(), "Authorization") || requests.Load() != 0 {
		t.Fatalf("incomplete connection attempted network or lost reason: %v (%d requests)", err, requests.Load())
	}
	t.Setenv("CRUDE_REGISTRY_HEADER_TEST", "Bearer fixture")
	repaired := captypes.Clone(durable)
	connection := repaired.Connections[id]
	connection.SecretRefs = map[string]string{"Authorization": "env:CRUDE_REGISTRY_HEADER_TEST"}
	repaired.Connections[id] = connection
	if err := service.publishRegistration(t.Context(), id, durable, repaired); err != nil {
		t.Fatal(err)
	}
	if err := service.Connect(t.Context(), id); err != nil {
		t.Fatalf("same install did not connect after explicit repair: %v", err)
	}
	if requests.Load() == 0 || service.Snapshot().Connections[0].State != "connected" {
		t.Fatal("repair did not reach the real SDK server")
	}
}

func TestRegistryVariablesAndArgumentsAreProjectedLiterally(t *testing.T) {
	input := registryInput{Value: "https://{host}/mcp/{literal}", Variables: map[string]registryInput{"host": {Default: "example.org"}}}
	if got, err := registryValue(input); err != nil || got != "https://example.org/mcp/{literal}" {
		t.Fatalf("declared/default or undeclared literal lost: %q %v", got, err)
	}
	input.Variables["host"] = registryInput{Description: "Your service hostname"}
	if _, err := registryValue(input); err == nil || !strings.Contains(err.Error(), "hostname") {
		t.Fatalf("missing variable guessed or guidance lost: %v", err)
	}
	args, err := fixedRegistryArguments([]registryInput{
		{Type: "named", Name: "--path", Value: "{path}", Variables: map[string]registryInput{"path": {Default: "/with spaces"}}},
		{Value: "{literal}"},
	})
	if err != nil || !reflect.DeepEqual(args, []string{"--path=/with spaces", "{literal}"}) {
		t.Fatalf("registry argv altered: %q %v", args, err)
	}
	if _, err := fixedRegistryArguments([]registryInput{{Value: "{secret}", Variables: map[string]registryInput{"secret": {Required: true}}}}); err == nil {
		t.Fatal("unresolved declared command variable became a literal argument")
	}
}

func TestPublishedPackageLaunchKeepsUpstreamArguments(t *testing.T) {
	source := captypes.DefaultSources()[captypes.OfficialMCPRegistrySourceID]
	server := registryServer{Name: "io.github.CursorTouch/Windows-MCP", Version: "1.0.1"}
	pkg := registryPackage{RegistryType: "pypi", Identifier: "windows-mcp", Version: "0.8.1", RuntimeHint: "uvx", Transport: registryTransport{Type: "stdio"}}
	for _, want := range []string{"serve", "publisher-subcommand"} {
		if want != "serve" {
			pkg.PackageArguments = []registryInput{{Type: "positional", Value: want}}
		}
		cfg, _, err := registryPackageConnection(source, server, pkg, "fixture")
		if err != nil || !reflect.DeepEqual(cfg.Args, []string{"windows-mcp==0.8.1", want}) {
			t.Fatal(cfg.Args, err)
		}
	}
}

// Opt-in handshake only: do not call Screenshot, Clipboard or any desktop operation.
func TestPublishedWindowsPackageHandshake(t *testing.T) {
	if os.Getenv("CRUDE_TEST_PUBLISHED_WINDOWS_MCP") != "1" || runtime.GOOS != "windows" {
		t.Skip("requires explicit real package launch")
	}
	s, settings, _ := capabilityFixture(t)
	source := captypes.DefaultSources()[captypes.OfficialMCPRegistrySourceID]
	settings.Sources[source.ID] = source
	if err := s.Configure(t.Context(), settings); err != nil {
		t.Fatal(err)
	}
	entries, err := s.DiscoverSource(t.Context(), "Windows-MCP", captypes.KindMCP, 30, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Publisher != "io.github.CursorTouch/Windows-MCP" {
			continue
		}
		preview, err := s.PreviewAcquisition(t.Context(), entry.ID, captypes.ScopeWorkspace, "install")
		if err != nil {
			t.Fatal(err)
		}
		acquired, err := s.AcquireReviewed(t.Context(), preview)
		if err != nil {
			t.Fatal(err)
		}
		config := acquired.Connections[entry.ID]
		a, err := s.Test(t.Context(), config)
		if err != nil {
			t.Fatal(err)
		}
		b, err := s.Test(t.Context(), config)
		if err != nil || len(a.Tools) == 0 || len(a.Tools) != len(b.Tools) {
			t.Fatal("discovery failed", err)
		}
		for i, tool := range a.Tools {
			if tool.Fingerprint != b.Tools[i].Fingerprint {
				t.Fatalf("%s changed under identical pin", tool.OriginalName)
			}
		}
		t.Logf("%s %q: %d stable tool definitions", config.Command, config.Args, len(a.Tools))
		return
	}
	t.Fatal("published entry not returned")
}
