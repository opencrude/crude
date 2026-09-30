package session

import (
	"testing"

	"crude/provider"
)

func TestCategoryProvidersKeepDefaultAndAeronCapacities(t *testing.T) {
	const model = "category-shared-capacity-model"
	cfg := accountRoutingConfig()
	profile := cfg.Agents.Profiles["default"]
	profile.Categories[CategoryDefault] = CategoryConfig{AccountID: "work", Model: model, ModelContextWindow: 64_000}
	profile.Categories[CategoryAeron] = CategoryConfig{AccountID: "personal", Model: model, ModelContextWindow: 128_000}
	cfg.Agents.Profiles["default"] = profile

	mainCategory := cfg.GetActiveDefaultCategory()
	aeronCategory := cfg.GetCategoryConfig(CategoryAeron)
	main, err := cfg.providerForCategory(mainCategory)
	if err != nil {
		t.Fatal(err)
	}
	aeron, err := cfg.providerForCategory(aeronCategory)
	if err != nil {
		t.Fatal(err)
	}
	if got := main.ContextWindow(model); got != 64_000 {
		t.Errorf("default provider context = %d, want 64000", got)
	}
	if got := aeron.ContextWindow(model); got != 128_000 {
		t.Errorf("Aeron compaction provider context = %d, want 128000", got)
	}
	if got := main.ContextWindow(model); got != 64_000 {
		t.Errorf("Aeron creation changed default provider context to %d", got)
	}
	if got := main.ContextWindow("category-other-capacity-model"); got != provider.LookupContextWindow("category-other-capacity-model") {
		t.Errorf("default override applied to another model: %d", got)
	}
	unset, err := cfg.providerForCategory(CategoryConfig{AccountID: "personal", Model: model})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := unset.ContextWindow(model), provider.LookupContextWindow(model); got != want {
		t.Errorf("unconfigured provider context = %d, want lookup %d", got, want)
	}
}
