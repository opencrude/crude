package lsp

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/charmbracelet/x/powernap/pkg/lsp/protocol"
)

func configurationReply(t *testing.T, c Configuration, items ...protocol.ConfigurationItem) string {
	t.Helper()
	params, err := json.Marshal(protocol.ConfigurationParams{Items: items})
	if err != nil {
		t.Fatal(err)
	}
	reply, err := c.answer(params)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(reply)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func configurationItem(scope, section string) protocol.ConfigurationItem {
	return protocol.ConfigurationItem{ScopeURI: &scope, Section: section}
}

func TestConfigurationScopeUsesDeepestCompleteEffectiveObject(t *testing.T) {
	global := map[string]any{"engine": map[string]any{"mode": "root", "rootOnly": true}}
	a := map[string]any{"engine": map[string]any{"mode": "A"}}
	c, err := NewConfiguration("file:///project", global, []ConfigurationScope{
		{URI: "file:///project/modules/a", Settings: a},
		{URI: "file:///project/modules/a/nested", Settings: map[string]any{"engine": map[string]any{"mode": "nested"}}},
		{URI: "file:///project/modules/b", Settings: map[string]any{"engine": map[string]any{"mode": "B"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	global["engine"].(map[string]any)["mode"] = "mutated global"
	a["engine"].(map[string]any)["mode"] = "mutated scope"
	got := configurationReply(t, c,
		configurationItem("file:///project/modules/a/main.cc", "engine.mode"),
		configurationItem("file:///project/modules/b/main.cc", "engine.mode"),
		configurationItem("file:///project/modules/a/nested/main.cc", "engine.mode"),
		configurationItem("file:///project/modules/a-other/main.cc", "engine.mode"),
		configurationItem("file:///project/modules/a/../b/main.cc", "engine.mode"),
		configurationItem("file:///project/modules/a/main.cc", "engine.rootOnly"),
		protocol.ConfigurationItem{Section: "engine.mode"},
		configurationItem("file:///project-other/main.cc", "engine.mode"),
		configurationItem("file://other/project/main.cc", "engine.mode"),
		configurationItem("", "engine.mode"),
		configurationItem("untitled:main.cc", "engine.mode"),
	)
	const want = `["A","B","nested","root","B",null,"root",null,null,null,null]`
	if got != want {
		t.Fatalf("scope-specific settings lost or leaked: got %s want %s", got, want)
	}
}

func TestConfigurationURINormalizationDoesNotGuessNativeFilesystemIdentity(t *testing.T) {
	for _, tc := range []struct {
		root, directory, resource string
	}{
		{"file:///project", "file:///project/雪%20space", "file://localhost/project/%E9%9B%AA%20space/source.cc"},
		{"file:///c:/project", "file:///C%3A/project/a", "file:///c:/project/a/source.cc"},
		{"file://HOST/share/project", "file://host/share/project/a", "file://HOST/share/project/a/source.cc"},
		{"file:///", "file:///project", "file:///project/source.cc"},
	} {
		c, err := NewConfiguration(tc.root, nil, []ConfigurationScope{{URI: tc.directory, Settings: map[string]any{"mode": "scoped"}}})
		if err != nil {
			t.Fatal(err)
		}
		if got := configurationReply(t, c, configurationItem(tc.resource, "mode")); got != `["scoped"]` {
			t.Fatalf("URI syntax normalization failed for %s: %s", tc.resource, got)
		}
	}
	// Path case, unlike the drive letter and host, is not a portable identity.
	c, err := NewConfiguration("file:///C:/project", nil,
		[]ConfigurationScope{{URI: "file:///C:/project/A", Settings: map[string]any{"mode": "A"}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := configurationReply(t, c, configurationItem("file:///C:/project/a/main.cc", "mode")); got != `[null]` {
		t.Fatalf("guessed case-insensitive native path identity: %s", got)
	}
}

func TestConfigurationTableAndRepliesAreDetached(t *testing.T) {
	c, err := NewConfiguration("file:///project", map[string]any{
		"engine.option": "literal-key",
		"engine":        map[string]any{"option": "nested-key"},
		"items":         []string{"one", "two"},
		"number":        uint64(9007199254740993),
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	const want = `["literal-key",["one","two"],9007199254740993,null]`
	items := []protocol.ConfigurationItem{{Section: "engine.option"}, {Section: "items"}, {Section: "number"}, {Section: "absent"}}
	if got := configurationReply(t, c, items...); got != want {
		t.Fatalf("section or JSON value changed: %s", got)
	}
	raw, err := c.answer(json.RawMessage(`{"items":[{"section":"items"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw[0].(json.RawMessage) {
		raw[0].(json.RawMessage)[i] = 'x'
	}
	if got := configurationReply(t, c, items...); got != want {
		t.Fatal("reply mutation changed retained launch configuration")
	}
	if !c.boundTo("file://localhost/project/.") || c.boundTo("file:///other") {
		t.Fatal("configuration root binding lost")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if got := configurationReply(t, c, items...); got != want {
					t.Error("concurrent read changed immutable configuration")
				}
			}
		}()
	}
	wg.Wait()
}

func TestConfigurationRejectsAmbiguousBindingsAndInvalidJSONBeforeProcessConstruction(t *testing.T) {
	for _, scopes := range [][]ConfigurationScope{
		{{URI: "file:///outside"}},
		{{URI: "file:///project-other"}},
		{{URI: "file:///project/a"}, {URI: "file://localhost/project/a/."}},
		{{URI: "https://host/project"}},
		{{URI: "file:///project/a?query"}},
		{{URI: "file:///project/a#fragment"}},
		{{URI: "file:///project/a%00b"}},
		{{URI: "file:///project/%ff"}},
		{{URI: "file:///project/a%5Cb"}},
		{{URI: "file://user@host/project"}},
		{{URI: "file:///project/a", Settings: map[string]any{"bad": math.Inf(1)}}},
	} {
		if _, err := NewConfiguration("file:///project", nil, scopes); !errors.Is(err, ErrConfiguration) {
			t.Fatalf("unsafe binding accepted: %v", err)
		}
	}
	if _, err := NewConfiguration("", nil, []ConfigurationScope{{URI: "file:///project"}}); !errors.Is(err, ErrConfiguration) {
		t.Fatal("unbound scoped table accepted")
	}
	c, err := NewConfiguration("file:///project", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewClient(ClientConfig{RootURI: "file:///other", Configuration: c, Command: "never-construct-this-program"}); !errors.Is(err, ErrConfiguration) {
		t.Fatalf("foreign-root table reached program construction: %v", err)
	}
}

func TestConfigurationBoundsFailWithoutPartialReply(t *testing.T) {
	if _, err := NewConfiguration("file:///project", map[string]any{"large": strings.Repeat("a", MaxConfigurationBytes)}, nil); !errors.Is(err, ErrConfiguration) {
		t.Fatal("oversized settings accepted")
	}
	if _, err := NewConfiguration("file:///project", nil, make([]ConfigurationScope, MaxConfigurationScopes+1)); !errors.Is(err, ErrConfiguration) {
		t.Fatal("too many scopes accepted")
	}
	c, err := NewConfiguration("file:///project", map[string]any{"large": strings.Repeat("a", MaxConfigurationBytes/2)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []string{
		`not-json`,
		strings.Repeat(" ", MaxConfigurationRequestBytes+1),
		`{"items":[` + strings.Repeat(`{},`, MaxConfigurationItems) + `{}]}`,
		`{"items":[{"section":"` + strings.Repeat("x", maxConfigurationSectionBytes+1) + `"}]}`,
		`{"items":[{"section":"large"},{"section":"large"}]}`,
	} {
		result, err := c.answer(json.RawMessage(params))
		if !errors.Is(err, ErrConfiguration) || result != nil {
			t.Fatalf("bound exposed a partial response: rows=%d err=%v", len(result), err)
		}
	}
	if got := configurationReply(t, Configuration{}, protocol.ConfigurationItem{}); got != `[null]` {
		t.Fatal("zero table invented settings")
	}
}
