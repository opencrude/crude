package providersim

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestEmbeddedSchemaBundles(t *testing.T) {
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	files, err := schemaFiles.ReadDir("schemas")
	if err != nil {
		t.Fatal(err)
	}
	for wire, source := range map[Wire]string{
		Chat: "openai", Responses: "openai", Codex: "codex", Messages: "anthropic", Gemini: "genai", Interactions: "genai", Ollama: "ollama",
	} {
		name := string(wire) + ".json"
		if record := manifest.Bundles[name]; record.Source != source || len(record.Roots) == 0 {
			t.Errorf("missing or incorrect bundle for %s: source %q, want %q", wire, record.Source, source)
		}
	}
	names := make(map[string]string)
	for name, record := range manifest.Bundles {
		names[name] = record.SHA256
		if _, ok := manifest.Sources[record.Source]; !ok {
			t.Errorf("bundle %s has unknown source %q", name, record.Source)
		}
		for _, source := range record.AdditionalSources {
			if _, ok := manifest.Sources[source]; !ok {
				t.Errorf("bundle %s has unknown additional source %q", name, source)
			}
		}
	}
	for name, source := range manifest.Sources {
		if source.LicenseFile == "" || source.LicenseSHA256 == "" {
			t.Errorf("source %s has no pinned license file", name)
			continue
		}
		names[source.LicenseFile] = source.LicenseSHA256
	}
	for _, file := range files {
		name := file.Name()
		if name == "manifest.json" {
			continue
		}
		expected, ok := names[name]
		if !ok {
			t.Errorf("embedded file %s is absent from the manifest", name)
			continue
		}
		delete(names, name)
		raw, err := schemaFiles.ReadFile(schemaDirectory + name)
		if err != nil {
			t.Fatal(err)
		}
		checkSchemaDigest(t, name, raw, expected)
	}
	if len(names) != 0 {
		t.Errorf("manifest names missing embedded files: %v", names)
	}
	for name := range manifest.Bundles {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := wireBundle(Wire(strings.TrimSuffix(name, ".json"))); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func checkSchemaDigest(t *testing.T, name string, raw []byte, expected string) {
	t.Helper()
	if bytes.Contains(raw, []byte("\r\n")) {
		t.Errorf("%s has non-LF line endings", name)
	}
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != expected {
		t.Errorf("digest %s = %s, want %s", name, got, expected)
	}
}

func BenchmarkColdChatBundle(b *testing.B) {
	manifest, err := readManifest()
	if err != nil {
		b.Fatal(err)
	}
	record := manifest.Bundles["chat.json"]
	b.ReportAllocs()
	for b.Loop() {
		if _, err := loadBundle("chat.json", record); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkChatRequestValidation(b *testing.B) {
	bundle, err := wireBundle(Chat)
	if err != nil {
		b.Fatal(err)
	}
	b.Run("text", func(b *testing.B) { benchmarkValidation(b, bundle, plainChat) })
	b.Run("tools_and_image", func(b *testing.B) { benchmarkValidation(b, bundle, richChat) })
}

func benchmarkValidation(b *testing.B, bundle *bundle, data string) {
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	for b.Loop() {
		value, err := decodeJSON([]byte(data))
		if err != nil {
			b.Fatal(err)
		}
		object, _ := value.(map[string]any)
		if _, err := validateChat(bundle, value, object, ""); err != nil {
			b.Fatal(err)
		}
	}
}

func TestResponsesToolNameRegexAdaptation(t *testing.T) {
	const component = "ToolSearchOutputFunctionToolParam"
	manifest, err := readManifest()
	if err != nil {
		t.Fatal(err)
	}
	// Resolve this normalized definition directly using the bundle definitions.
	dependencies := manifest.Bundles["responses.json"].Components
	b, err := loadBundle("responses.json", bundleRecord{Roots: map[string][]string{component: dependencies}})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		valid bool
	}{
		{"get.v1_build-7", true}, {"", false}, {"a\nb", false}, {"end\n", false}, {"end\r", false}, {"é", false}, {"with space", false},
	} {
		err := b.validate(component, map[string]any{"type": "function", "name": tc.name})
		if (err == nil) != tc.valid {
			t.Errorf("name %q (valid %v): %v", tc.name, tc.valid, err)
		}
	}
}

func TestResponsesInputItemOverlap(t *testing.T) {
	b, err := wireBundle(Responses)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name  string
		body  string
		valid bool
	}{
		{"overlap", `{"model":"gpt-5","input":[{"role":"user","content":[{"type":"input_text","text":"Hello"}]}]}`, true},
		{"easy message only", `{"model":"gpt-5","input":[{"role":"user","content":"Hello"}]}`, true},
		{"no matching arm", `{"model":"gpt-5","input":[{"role":"user","content":[{"type":"input_text","text":false}]}]}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, err := decodeJSON([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if err := b.validate("CreateResponse", value); (err == nil) != tc.valid {
				t.Errorf("valid %v: %v", tc.valid, err)
			}
		})
	}
}

type schemaCase struct {
	name  string
	root  string
	body  string
	valid bool
}

func checkSchemaCases(t *testing.T, wire Wire, cases []schemaCase) {
	t.Helper()
	b, err := wireBundle(wire)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			value, err := decodeJSON([]byte(tc.body))
			if err != nil {
				t.Fatal(err)
			}
			if err := b.validate(tc.root, value); (err == nil) != tc.valid {
				t.Errorf("%s valid %v: %v", tc.root, tc.valid, err)
			}
		})
	}
}

func TestMessagesSDKSchemaProjection(t *testing.T) {
	checkSchemaCases(t, Messages, []schemaCase{
		{"stable automatic cache", "MessageCreateParams", `{"model":"claude-opus-5-5","max_tokens":100,"messages":[{"role":"user","content":"Hello"}],"cache_control":{"type":"ephemeral","ttl":"5m"}}`, true},
		{"stable invalid cache TTL", "MessageCreateParams", `{"model":"claude-opus-5-5","max_tokens":100,"messages":[],"cache_control":{"type":"ephemeral","ttl":"tomorrow"}}`, false},
		{"on-demand", "BetaMessageCreateParams", `{"model":"claude-opus-5-5","max_tokens":100,"messages":[{"role":"user","content":"Hello"}],"compaction":{"type":"summarize"}}`, true},
		{"threshold", "BetaMessageCreateParams", `{"model":"claude-opus-5-5","max_tokens":100,"messages":[{"role":"user","content":"Hello"}],"stream":true,"context_management":{"edits":[{"type":"compact_20260112","trigger":{"type":"input_tokens","value":150000},"pause_after_compaction":true}]}}`, true},
		{"exclusive compactions", "BetaMessageCreateParams", `{"model":"claude-opus-5-5","max_tokens":100,"messages":[],"compaction":{"type":"summarize"},"context_management":{"edits":[{"type":"compact_20260112"}]}}`, false},
		{"bad trigger type", "BetaCompact20260112EditParam", `{"type":"compact_20260112","trigger":{"type":"time","value":1}}`, false},
		{"signed output", "BetaCompactionBlock", `{"type":"compaction","content":"Summary","signature":"opaque"}`, true},
		{"nonstring signature", "BetaCompactionBlock", `{"type":"compaction","content":"Summary","signature":{"invalid":true}}`, false},
		{"message start", "MessageStreamEvent", `{"type":"message_start","message":{"id":"msg_1","content":[],"model":"claude-opus-5-5","role":"assistant","stop_reason":null,"stop_sequence":null,"type":"message","usage":{"input_tokens":1,"output_tokens":0}}}`, true},
		{"block start", "MessageStreamEvent", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`, true},
		{"block delta", "MessageStreamEvent", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`, true},
		{"block stop", "MessageStreamEvent", `{"type":"content_block_stop","index":0}`, true},
		{"message delta usage", "MessageStreamEvent", `{"type":"message_delta","delta":{"stop_reason":"end_turn","stop_sequence":null},"usage":{"output_tokens":1,"cache_creation_input_tokens":100,"cache_read_input_tokens":50}}`, true},
		{"missing delta usage", "MessageStreamEvent", `{"type":"message_delta","delta":{"stop_reason":"end_turn"}}`, false},
		{"message stop", "MessageStreamEvent", `{"type":"message_stop"}`, true},
		{"ping", "MessageStreamEvent", `{"type":"ping"}`, true},
		{"beta ping", "BetaMessageStreamEvent", `{"type":"ping"}`, true},
		{"stream error", "MessageStreamEvent", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, true},
		{"beta stream error", "BetaMessageStreamEvent", `{"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`, true},
		{"unknown stream event", "MessageStreamEvent", `{"type":"unknown_event"}`, false},
	})
	b, err := wireBundle(Messages)
	if err != nil {
		t.Fatal(err)
	}
	const root = "BetaMessageCreateParams"
	if name, unknown := b.unknownField(root, map[string]any{"compaction": nil, "stream": false, "betas": []string{"beta"}}, ""); !unknown || name != "betas" {
		t.Errorf("body must not accept SDK header argument: unknown %v, name %q", unknown, name)
	}
	if name := b.missingField(root, map[string]any{"messages": nil, "model": "claude-opus-5-5"}); name != "max_tokens" {
		t.Errorf("common required key = %q, want max_tokens", name)
	}
	if name := b.missingField(root, map[string]any{"messages": nil, "model": "claude-opus-5-5", "max_tokens": 100}); name != "" {
		t.Errorf("stream is required in only one branch: unexpected required key %q", name)
	}
}

func TestGeminiSDKSchemaProjection(t *testing.T) {
	checkSchemaCases(t, Gemini, []schemaCase{
		{"REST request", "GenerateContentRequest", `{"contents":[{"role":"user","parts":[{"text":"Hello"}]}],"systemInstruction":{"parts":[{"text":"Be concise"}]},"tools":[{"functionDeclarations":[{"name":"query","parameters":{"type":"OBJECT","properties":{"name":{"type":"STRING"}}}}]}],"toolConfig":{"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":["query"]}},"generationConfig":{"topK":3,"thinkingConfig":{"thinkingLevel":"HIGH","includeThoughts":true}},"cachedContent":"cachedContents/cache-1"}`, true},
		{"missing contents", "GenerateContentRequest", `{"model":"gemini-3.8-flash"}`, false},
		{"SDK shorthand contents", "GenerateContentRequest", `{"contents":"Hello"}`, false},
		{"SDK spelling", "GenerateContentRequest", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinking_level":"HIGH"}}}`, false},
		{"noncanonical enum", "GenerateContentRequest", `{"contents":[],"generationConfig":{"thinkingConfig":{"thinkingLevel":"high"}}}`, false},
		{"fractional topK", "GenerateContentRequest", `{"contents":[],"generationConfig":{"topK":3.5}}`, false},
		{"response", "GenerateContentResponse", `{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"Hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":42,"cachedContentTokenCount":17,"candidatesTokenCount":5,"totalTokenCount":47}}`, true},
		{"SDK transport field", "GenerateContentResponse", `{"sdkHttpResponse":null}`, false},
		{"cache snake case", "GenerateContentResponse", `{"usageMetadata":{"cached_content_token_count":17}}`, false},
		{"retry details", "ErrorResponse", `{"error":{"code":429,"message":"Too many requests","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.QuotaFailure","violations":[]},{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"34.4s"}]}}`, true},
		{"wrong delay alias", "ErrorResponse", `{"error":{"code":429,"message":"Too many requests","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retry_delay":"34.4s"}]}}`, false},
		{"wrong duration format", "ErrorResponse", `{"error":{"code":429,"message":"Too many requests","status":"RESOURCE_EXHAUSTED","details":[{"@type":"type.googleapis.com/google.rpc.RetryInfo","retryDelay":"34000ms"}]}}`, false},
		{"wrong status type", "ErrorResponse", `{"error":{"code":429,"message":"Too many requests","status":8}}`, false},
	})
	b, err := wireBundle(Gemini)
	if err != nil {
		t.Fatal(err)
	}
	const root = "GenerateContentRequest"
	for _, key := range []string{"system_instruction", "model", "config"} {
		if _, unknown := b.unknownField(root, map[string]any{key: nil}, ""); !unknown {
			t.Errorf("%q is not a REST request body field", key)
		}
	}
}

func TestMessagesModelInfoSchema(t *testing.T) {
	const model = `{"id":"claude-opus-5-5","created_at":"2026-11-24T00:00:00Z","display_name":"Claude Opus","lifecycle":"active","type":"model"}`
	checkSchemaCases(t, Messages, []schemaCase{
		{"stable model", "ModelInfo", model, true},
		{"beta model", "BetaModelInfo", model, true},
		{"beta fallback models", "BetaModelInfo", strings.TrimSuffix(model, "}") + `,"allowed_fallback_models":["claude-sonnet"]}`, true},
		{"beta fallback models must be strings", "BetaModelInfo", strings.TrimSuffix(model, "}") + `,"allowed_fallback_models":[7]}`, false},
		{"stable lifecycle required", "ModelInfo", `{"id":"claude-opus-5-5","created_at":"2026-11-24T00:00:00Z","display_name":"Claude Opus","type":"model"}`, false},
		{"beta type is model", "BetaModelInfo", strings.Replace(model, `"type":"model"`, `"type":"message"`, 1), false},
	})
}

func TestGeminiModelInventorySources(t *testing.T) {
	const revision = "4b6ea83bc2d87be6f3b8ce19f070b649bd7f4638"
	const base = "https://raw.githubusercontent.com/googleapis/googleapis/" + revision + "/"
	var manifest struct {
		Bundles map[string]bundleRecord `json:"bundles"`
		Sources map[string]struct {
			Revision string `json:"revision"`
			License  string `json:"license"`
			Files    map[string]struct {
				URL    string `json:"url"`
				SHA256 string `json:"sha256"`
			} `json:"files"`
		} `json:"sources"`
	}
	raw, err := schemaFiles.ReadFile(schemaDirectory + "manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if got := manifest.Bundles["gemini.json"].AdditionalSources; !slices.Equal(got, []string{"googleapis"}) {
		t.Errorf("Gemini additional sources = %v, want only googleapis", got)
	}
	source := manifest.Sources["googleapis"]
	if source.Revision != revision || source.License != "Apache-2.0" {
		t.Errorf("Google APIs revision/license = %s / %s, want %s / Apache-2.0", source.Revision, source.License, revision)
	}
	for file, want := range map[string]string{
		"model.proto":         "a59bc3fa01cf7869de8e5a26da6f3db3ea26c495e4a6d8e1e8f91c5b58a9c0b8",
		"model_service.proto": "546286783cd6338bae05aa639a8db8a364a90c7a4a7649362d5c9078c94604df",
	} {
		path := "google/ai/generativelanguage/v1beta/" + file
		if got := source.Files[path]; got.URL != base+path || got.SHA256 != want {
			t.Errorf("%s pin = %s / %s, want %s / %s", file, got.URL, got.SHA256, base+path, want)
		}
	}
}

func TestGeminiModelInventorySchema(t *testing.T) {
	const model = `{"name":"models/gemini-3.8-flash","baseModelId":"gemini-3.8-flash","version":"001","displayName":"Gemini Flash","description":"A model","inputTokenLimit":1048576,"outputTokenLimit":8192,"topK":3,"topP":0.9,"temperature":0.8,"maxTemperature":2.0,"thinking":true,"supportedGenerationMethods":["generateContent"]}`
	checkSchemaCases(t, Gemini, []schemaCase{
		{"model", "Model", model, true},
		{"omitted model properties", "Model", `{}`, true},
		{"model list", "ListModelsResponse", `{"models":[` + model + `],"nextPageToken":"page-2"}`, true},
		{"empty final page", "ListModelsResponse", `{"models":[]}`, true},
		{"omitted list properties", "ListModelsResponse", `{}`, true},
		{"fractional topK", "Model", `{"topK":3.5}`, false},
		{"fractional output token limit", "Model", `{"outputTokenLimit":1.5}`, false},
		{"string max temperature", "Model", `{"maxTemperature":"1.5"}`, false},
		{"description must be a string", "Model", `{"description":false}`, false},
		{"invalid model in list", "ListModelsResponse", `{"models":[{"supportedGenerationMethods":[true]}]}`, false},
		{"page token must be a string", "ListModelsResponse", `{"nextPageToken":2}`, false},
	})
	b, err := wireBundle(Gemini)
	if err != nil {
		t.Fatal(err)
	}
	var properties map[string]any
	if err := json.Unmarshal([]byte(model), &properties); err != nil {
		t.Fatal(err)
	}
	if got := len(b.fields["Model"]); got != 13 {
		t.Errorf("Model has %d properties, want 13", got)
	}
	if got := len(b.fields["ListModelsResponse"]); got != 2 {
		t.Errorf("ListModelsResponse has %d properties, want 2", got)
	}
	if key, unknown := b.unknownField("Model", properties, ""); unknown {
		t.Errorf("Model must know canonical REST property %q", key)
	}
	if _, unknown := b.unknownField("Model", map[string]any{"input_token_limit": nil}, ""); !unknown {
		t.Error("input_token_limit is not a canonical REST model property")
	}
}

func TestOllamaShowSchema(t *testing.T) {
	checkSchemaCases(t, Ollama, []schemaCase{
		{"model and verbose", "ShowRequest", `{"model":"qwen3","verbose":true}`, true},
		{"model only", "ShowRequest", `{"model":"qwen3"}`, true},
		{"model required", "ShowRequest", `{"verbose":true}`, false},
		{"verbose must be boolean", "ShowRequest", `{"model":"qwen3","verbose":"true"}`, false},
		{"thinking and model metadata", "ShowResponse", `{"thinking":{"values":[false,"high"],"default":"high"},"capabilities":["completion","thinking"],"model_info":{"general.architecture":"qwen3","qwen3.context_length":32768},"details":{"family":"qwen3"}}`, true},
		{"thinking default required", "ShowResponse", `{"thinking":{"values":[false,true]}}`, false},
		{"thinking level must be boolean or string", "ShowResponse", `{"thinking":{"values":[false,1],"default":false}}`, false},
	})
}

func TestInteractionsSDKSchemaProjection(t *testing.T) {
	checkSchemaCases(t, Interactions, []schemaCase{
		{"model", "CreateInteraction", `{"model":"gemini-3.8-flash","input":"Hello","generation_config":{"thinking_level":"high","thinking_summaries":"auto"}}`, true},
		{"agent", "CreateInteraction", `{"agent":"deep-research-preview-04-2026","input":"Hello","background":true}`, true},
		{"agent missing input", "CreateInteraction", `{"agent":"deep-research-preview-04-2026"}`, false},
		{"both identifiers", "CreateInteraction", `{"model":"gemini-3.8-flash","agent":"deep-research-preview-04-2026","input":"Hello"}`, false},
		{"bad agent cannot become model request", "CreateInteraction", `{"model":"gemini-3.8-flash","agent":false,"input":"Hello"}`, false},
		{"wrong thinking enum case", "CreateInteraction", `{"model":"gemini-3.8-flash","input":"Hello","generation_config":{"thinking_level":"HIGH"}}`, false},
		{"missing identifier", "CreateInteraction", `{"input":"Hello"}`, false},
		{"full interaction", "Interaction", `{"id":"id_1","status":"completed","created":"2026-11-25T00:00:00Z","updated":"2026-11-25T00:00:01Z","steps":[],"usage":{"total_input_tokens":17,"total_cached_tokens":12}}`, true},
		{"partial is not full", "Interaction", `{"id":"id_1","status":"completed"}`, false},
		{"partial created", "InteractionSSEEvent", `{"event_type":"interaction.created","interaction":{"id":"id_1","status":"in_progress"}}`, true},
		{"partial completed", "InteractionSSEEvent", `{"event_type":"interaction.completed","interaction":{"id":"id_1","status":"completed"}}`, true},
		{"status update", "InteractionSSEEvent", `{"event_type":"interaction.status_update","interaction_id":"id_1","status":"requires_action"}`, true},
		{"step start", "InteractionSSEEvent", `{"event_type":"step.start","index":0,"step":{"type":"model_output","content":[]}}`, true},
		{"step delta", "InteractionSSEEvent", `{"event_type":"step.delta","index":0,"delta":{"type":"text","text":"Hello"}}`, true},
		{"step stop", "InteractionSSEEvent", `{"event_type":"step.stop","index":0}`, true},
		{"missing event tag", "InteractionSSEEvent", `{"index":0}`, false},
		{"unknown event tag", "InteractionSSEEvent", `{"event_type":"unknown_event","index":0}`, false},
		{"error event", "InteractionSSEEvent", `{"event_type":"error","error":{"code":"not_found","message":"Not found"}}`, true},
		{"HTTP error", "ErrorResponse", `{"error":{"code":"rate_limit_exceeded","message":"Too many requests"}}`, true},
		{"wrong HTTP error code type", "ErrorResponse", `{"error":{"code":429,"message":"Too many requests","status":"RESOURCE_EXHAUSTED"}}`, false},
	})
	b, err := wireBundle(Interactions)
	if err != nil {
		t.Fatal(err)
	}
	const root = "CreateInteraction"
	if name, unknown := b.unknownField(root, map[string]any{"agent": "x", "generation_config": nil}, ""); unknown {
		t.Errorf("union must remember keys from both arms; does not know %q", name)
	}
	if name := b.missingField(root, map[string]any{}); name != "" {
		t.Errorf("not every branch requires %q", name)
	}
}

func TestCodexResponseItemDraftMigration(t *testing.T) {
	checkSchemaCases(t, Codex, []schemaCase{
		{"assistant item without public API fields", "ResponseItem", `{"type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello"}]}`, true},
		{"reasoning without id", "ResponseItem", `{"type":"reasoning","summary":[],"encrypted_content":"opaque"}`, true},
		{"compaction trigger input", "ResponseItem", `{"type":"compaction_trigger"}`, true},
		{"compaction artifact", "ResponseItem", `{"type":"compaction","encrypted_content":"opaque"}`, true},
		{"bad nested text", "ResponseItem", `{"type":"message","role":"assistant","content":[{"type":"output_text","text":false}]}`, false},
		{"missing artifact bytes", "ResponseItem", `{"type":"compaction"}`, false},
	})
}
