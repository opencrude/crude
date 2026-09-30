package provider

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
)

// ModelSource is a public dataset, independent of configured provider accounts.
type ModelSource struct{ ID, URL, Format string }

func modelsDevEndpoint() string {
	if endpoint := strings.TrimSpace(os.Getenv("MODELS_DEV_URL")); endpoint != "" {
		return endpoint
	}
	return "https://models.dev/api.json"
}

func DefaultModelSources() []ModelSource {
	return []ModelSource{
		{ID: "openrouter", URL: "https://openrouter.ai/api/v1/models", Format: "openrouter"},
		{ID: "models.dev", URL: modelsDevEndpoint(), Format: "models.dev"},
		{ID: "litellm", URL: "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json", Format: "litellm"},
	}
}

func parseModelSource(format string, body []byte) (map[string]ModelMeta, error) {
	models := map[string]ModelMeta{}
	add := func(id string, meta ModelMeta) {
		id = strings.ToLower(strings.TrimSpace(id))
		if id == "" || len(id) > 512 {
			return
		}
		for _, modality := range KnownModalities() {
			if meta.ModalitySupport[modality] {
				meta.Modalities = append(meta.Modalities, modality)
			}
		}
		models[id] = meta
	}
	inputSupport := func(input []string) map[string]bool {
		if input == nil {
			return nil
		}
		out := map[string]bool{}
		for _, modality := range KnownModalities() {
			out[modality] = slices.Contains(input, modality)
		}
		// Generic file input does not establish native PDF support.
		if slices.Contains(input, "file") && !slices.Contains(input, ModalityPDF) {
			delete(out, ModalityPDF)
		}
		return out
	}
	switch format {
	case "models.dev":
		var data map[string]struct {
			Models map[string]struct {
				Cost *struct {
					Input, Output float64
					CacheRead     float64 `json:"cache_read"`
				} `json:"cost"`
				Limit      struct{ Context, Output int } `json:"limit"`
				Modalities struct{ Input []string }      `json:"modalities"`
				Tools      *bool                         `json:"tool_call"`
			}
		}
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, err
		}
		// Sorting keeps duplicate provider entries deterministic. Differing
		// endpoint variants remain separate and ambiguous aliases are rejected.
		providers := make([]string, 0, len(data))
		for id := range data {
			providers = append(providers, id)
		}
		slices.Sort(providers)
		for _, providerID := range providers {
			for id, entry := range data[providerID].Models {
				meta := ModelMeta{ContextWindow: entry.Limit.Context, MaxOutput: entry.Limit.Output, ModalitySupport: inputSupport(entry.Modalities.Input)}
				if entry.Cost != nil {
					meta.InputPrice, meta.OutputPrice, meta.CacheReadPrice, meta.PricingKnown = entry.Cost.Input/1e6, entry.Cost.Output/1e6, entry.Cost.CacheRead/1e6, true
				}
				if entry.Tools != nil {
					meta.ToolsKnown, meta.SupportsTools = true, *entry.Tools
				}
				if _, exists := models[strings.ToLower(id)]; !exists {
					add(id, meta)
				}
			}
		}
	case "openrouter":
		var data struct {
			Data []struct {
				ID           string
				Context      int `json:"context_length"`
				Architecture struct {
					Input []string `json:"input_modalities"`
				}
				Pricing  map[string]json.RawMessage
				Provider struct {
					Output int `json:"max_completion_tokens"`
				} `json:"top_provider"`
				Parameters []string `json:"supported_parameters"`
			}
		}
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, err
		}
		for _, entry := range data.Data {
			meta := ModelMeta{ContextWindow: entry.Context, MaxOutput: entry.Provider.Output, ModalitySupport: inputSupport(entry.Architecture.Input), ToolsKnown: entry.Parameters != nil, SupportsTools: slices.Contains(entry.Parameters, "tools")}
			parsePrice := func(raw json.RawMessage) (float64, error) {
				text := string(raw)
				if len(raw) > 0 && raw[0] == '"' {
					if err := json.Unmarshal(raw, &text); err != nil {
						return 0, err
					}
				}
				return strconv.ParseFloat(text, 64)
			}
			input, inErr := parsePrice(entry.Pricing["prompt"])
			output, outErr := parsePrice(entry.Pricing["completion"])
			if inErr == nil && outErr == nil && input >= 0 && output >= 0 {
				meta.InputPrice, meta.OutputPrice, meta.PricingKnown = input, output, true
			}
			meta.CacheReadPrice, _ = parsePrice(entry.Pricing["input_cache_read"])
			add(entry.ID, meta)
		}
	case "litellm":
		var data map[string]json.RawMessage
		if err := json.Unmarshal(body, &data); err != nil {
			return nil, err
		}
		for id, raw := range data {
			if id == "sample_spec" {
				continue
			}
			var entry struct {
				Context     int      `json:"max_input_tokens"`
				Output      int      `json:"max_output_tokens"`
				InputPrice  *float64 `json:"input_cost_per_token"`
				OutputPrice *float64 `json:"output_cost_per_token"`
				CachePrice  float64  `json:"cache_read_input_token_cost"`
				Image       *bool    `json:"supports_vision"`
				PDF         *bool    `json:"supports_pdf_input"`
				Audio       *bool    `json:"supports_audio_input"`
				Video       *bool    `json:"supports_video_input"`
				Tools       *bool    `json:"supports_function_calling"`
			}
			if err := json.Unmarshal(raw, &entry); err != nil {
				return nil, fmt.Errorf("model %s: %w", id, err)
			}
			meta := ModelMeta{ContextWindow: entry.Context, MaxOutput: entry.Output, ModalitySupport: map[string]bool{}}
			for modality, value := range map[string]*bool{ModalityImage: entry.Image, ModalityPDF: entry.PDF, ModalityAudio: entry.Audio, ModalityVideo: entry.Video} {
				if value != nil {
					meta.ModalitySupport[modality] = *value
				}
			}
			if entry.InputPrice != nil && entry.OutputPrice != nil && *entry.InputPrice >= 0 && *entry.OutputPrice >= 0 {
				meta.InputPrice, meta.OutputPrice, meta.CacheReadPrice, meta.PricingKnown = *entry.InputPrice, *entry.OutputPrice, entry.CachePrice, true
			}
			if entry.Tools != nil {
				meta.ToolsKnown, meta.SupportsTools = true, *entry.Tools
			}
			add(id, meta)
		}
	default:
		return nil, fmt.Errorf("unknown model source format %q", format)
	}
	if len(models) == 0 || len(models) > 100000 {
		return nil, fmt.Errorf("invalid source model count %d", len(models))
	}
	return models, nil
}
