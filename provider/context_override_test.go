package provider

import "testing"

func TestProviderContextWindowKeepsExplicitCapacityPerConnection(t *testing.T) {
	const model = "capacity-connection-model"
	first := &Provider{ID: "openrouter", Model: model}
	second := &Provider{ID: "openrouter", Model: model}
	defaultProvider := &Provider{ID: "openrouter", Model: model}
	fallback := LookupContextWindow(model)

	first.SetContextWindow(64_000)
	second.SetContextWindow(128_000)
	for _, tc := range []struct {
		name string
		p    *Provider
		want int
	}{
		{"first account", first, 64_000},
		{"second account", second, 128_000},
		{"unset account", defaultProvider, fallback},
	} {
		if got := tc.p.ContextWindow(model); got != tc.want {
			t.Errorf("%s window = %d, want %d", tc.name, got, tc.want)
		}
	}
	if got := first.ContextWindow("capacity-other-model"); got != LookupContextWindow("capacity-other-model") {
		t.Errorf("query for another model = %d, want protocol metadata", got)
	}
	first.SetContextWindow(0)
	if got := first.ContextWindow(model); got != fallback {
		t.Errorf("cleared override = %d, want %d", got, fallback)
	}
	second.SetContextWindow(-1)
	if got := second.ContextWindow(model); got != fallback {
		t.Errorf("negative override = %d, want %d", got, fallback)
	}
}

func TestProviderContextWindowUnconfiguredFollowsCatalogDiscovery(t *testing.T) {
	const model = "capacity-discovered-model"
	catalog := &ModelCatalog{}
	catalog.snapshot.Store(&catalogSnapshot{models: map[string]ModelMeta{model: {ContextWindow: 72_000}}})
	previous := activeModelCatalog.Swap(catalog)
	t.Cleanup(func() { activeModelCatalog.Store(previous) })

	unconfigured := &Provider{ID: "openrouter", Model: model}
	configured := &Provider{ID: "openrouter", Model: model}
	configured.SetContextWindow(96_000)
	if got := unconfigured.ContextWindow(model); got != 72_000 {
		t.Fatalf("unconfigured discovery = %d, want 72000", got)
	}
	if got := configured.ContextWindow(model); got != 96_000 {
		t.Fatalf("configured discovery = %d, want 96000", got)
	}
	catalog.snapshot.Store(&catalogSnapshot{models: map[string]ModelMeta{model: {ContextWindow: 80_000}}})
	if got := unconfigured.ContextWindow(model); got != 80_000 {
		t.Errorf("unconfigured refreshed discovery = %d, want 80000", got)
	}
	if got := configured.ContextWindow(model); got != 96_000 {
		t.Errorf("configured refreshed discovery = %d, want 96000", got)
	}
}
