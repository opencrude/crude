package provider

import (
	"encoding/json"
	"net/url"
	"strings"
)

// codexClientVersion is the client_version query parameter the ChatGPT codex
// backend requires on the /models endpoint. It mirrors the Codex CLI version;
// without it the backend rejects the request with HTTP 400. A var so tests can
// pin a value.
var codexClientVersion = "0.148.0"

// CodexStandard is the OpenAI Responses wire protocol spoken by the ChatGPT
// subscription backend at https://chatgpt.com/backend-api/codex. Chat, request
// encoding, streaming, and auth are identical to the Responses API; only the
// model-listing shape differs (the Codex /models endpoint returns
// {"models":[{"slug","display_name","context_window",...}]} instead of the
// standard OpenAI {"data":[...]} envelope). The OAuth bearer token and the
// required chatgpt-account-id / originator / session-id headers are supplied by
// Provider.Headers() from the stored OAuth credentials, so this standard stays
// unaware of authentication.
type CodexStandard struct {
	OpenAIResponsesStandard
}

func (s *CodexStandard) ID() string { return "codex" }

// EncodeRequest is the Responses envelope adapted to the stricter ChatGPT codex
// backend contract, verified against the live endpoint:
//   - store must be false (else 400 "Store must be set to false")
//   - stream must be true (else 400 "Stream must be set to true")
//   - max_output_tokens / max_completion_tokens are unsupported (400) and are
//     stripped; the backend caps output server-side
//   - a top-level instructions string is required (else 400 "Instructions are
//     required"); the system/developer message is lifted out of input to it
func (s *CodexStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	body, err := s.OpenAIResponsesStandard.EncodeRequest(req)
	if err != nil {
		return nil, err
	}
	var wire map[string]any
	if err := json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	wire["store"] = false
	wire["stream"] = true
	delete(wire, "max_output_tokens")
	delete(wire, "max_completion_tokens")
	delete(wire, "max_tokens")
	if _, ok := wire["instructions"]; !ok {
		wire["instructions"], wire["input"] = codexExtractInstructions(wire["input"])
	}
	return json.Marshal(wire)
}

// codexExtractInstructions pulls the system/developer text out of the Responses
// input array into a top-level instructions string and returns the remaining
// input. The codex backend requires the instructions field and rejects a
// system role inside the input array.
func codexExtractInstructions(input any) (string, any) {
	items, ok := input.([]any)
	if !ok {
		return "", input
	}
	var instructions strings.Builder
	out := make([]any, 0, len(items))
	for _, item := range items {
		msg, ok := item.(map[string]any)
		if !ok {
			out = append(out, item)
			continue
		}
		role, _ := msg["role"].(string)
		if role != "system" && role != "developer" {
			out = append(out, item)
			continue
		}
		if content, ok := msg["content"].([]any); ok {
			for _, part := range content {
				p, ok := part.(map[string]any)
				if !ok || p["type"] != "input_text" {
					continue
				}
				if text, ok := p["text"].(string); ok && text != "" {
					if instructions.Len() > 0 {
						instructions.WriteString("\n")
					}
					instructions.WriteString(text)
				}
			}
		}
	}
	return instructions.String(), out
}

// ModelsEndpoint returns the codex URL with the required
// client_version query parameter. The ChatGPT backend rejects the request with
// HTTP 400 when it is missing.
func (s *CodexStandard) ModelsEndpoint(baseURL string) string {
	endpoint := strings.TrimRight(baseURL, "/") + "/models"
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}
	q := u.Query()
	q.Set("client_version", codexClientVersion)
	u.RawQuery = q.Encode()
	return u.String()
}

func (s *CodexStandard) DecodeModels(body []byte) ([]ModelInfo, error) {
	var result struct {
		Models []struct {
			Slug          string `json:"slug"`
			DisplayName   string `json:"display_name"`
			ContextWindow int    `json:"context_window"`
			ID            string `json:"id"`
			Name          string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		// Fall back to the OpenAI shape in case the endpoint replies with it.
		return (&OpenAIResponsesStandard{}).DecodeModels(body)
	}
	if len(result.Models) == 0 {
		return (&OpenAIResponsesStandard{}).DecodeModels(body)
	}
	models := make([]ModelInfo, 0, len(result.Models))
	for _, m := range result.Models {
		id := m.Slug
		if id == "" {
			id = m.ID
		}
		if id == "" {
			id = m.Name
		}
		if id == "" {
			continue
		}
		name := m.DisplayName
		if name == "" {
			name = id
		}
		models = append(models, ModelInfo{ID: id, Name: name, ContextWindow: m.ContextWindow})
	}
	return models, nil
}
