package types

import (
	"slices"
	"testing"

	"crude/provider"
)

func TestModelEntriesRetainFullDiscoveredCard(t *testing.T) {
	const model = "settings-card-round-trip"
	provider.LoadModelMetas(map[string]provider.ModelMeta{model: {
		ContextWindow: 128000, MaxOutput: 8192, SupportsTools: true,
		Modalities: []string{provider.ModalityImage, provider.ModalityPDF},
	}})
	entries := ModelEntriesFromInfos([]provider.ModelInfo{{ID: model, Name: "Model card"}})
	got := entries[0]
	if got.ContextWindow != 128000 || got.MaxOutput != 8192 || !got.SupportsTools || !slices.Equal(got.Modalities, []string{provider.ModalityImage, provider.ModalityPDF}) {
		t.Fatalf("provider-to-UI conversion discarded discovered capabilities: %+v", got)
	}
}
