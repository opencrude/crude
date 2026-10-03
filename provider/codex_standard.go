package provider

import (
	"encoding/json"
	"net/http"
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

func (s *CodexStandard) RequestExtras(modelID, thinkingEffort string, settings ProtocolSettings) map[string]any {
	extras := s.OpenAIResponsesStandard.RequestExtras(modelID, thinkingEffort, settings)
	extras["store"] = false
	return extras
}

func (s *CodexStandard) Info() ProtocolInfo {
	info := s.OpenAIResponsesStandard.Info()
	info.ID = "codex"
	info.Name = "ChatGPT Codex"
	info.Description = "ChatGPT-subscription Responses backend (Sign in with ChatGPT)."
	info.Root = "" // the base URL already is the backend prefix
	info.ChatPath = "responses"
	info.ModelsPath = "models"
	// The codex backend forces store:false itself; only the summary knob
	// is user-selectable.
	info.Options = ProtocolOptions{ReasoningSummaries: true}
	return info
}

// EncodeRequest is the Responses envelope adapted to the stricter ChatGPT codex
// backend contract, verified against the live endpoint:
//   - store must be false (else 400 "Store must be set to false")
//   - stream must be true (else 400 "Stream must be set to true")
//   - max_output_tokens / max_completion_tokens are unsupported (400) and are
//     stripped; the backend caps output server-side
//   - a top-level instructions string is required (else 400 "Instructions are
//     required"); the system/developer message is lifted out of input to it
func (s *CodexStandard) EncodeRequest(req *ChatRequest) ([]byte, error) {
	if req != nil && req.ResponseContinuationScope != "" {
		copy := *req
		copy.ResponseContinuationScope = ""
		req = &copy
	}
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
	// Stateless Codex requests need the encrypted replay item to retain the
	// provider's reasoning continuity across tool turns.
	wire["include"] = []string{"reasoning.encrypted_content"}
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

// ModelsURL implements ModelLister, adding the client_version query
// parameter the ChatGPT backend requires (HTTP 400 without it).
func (s *CodexStandard) ModelsURL(base *url.URL, cursor string) *url.URL {
	endpoint := joinPath(base, "models")
	query := endpoint.Query()
	query.Set("client_version", codexClientVersion)
	endpoint.RawQuery = query.Encode()
	return endpoint
}

// DecodeModelsPage implements ModelLister for the codex
// {"models":[{"slug","display_name","context_window",...}]} shape, falling
// back to the standard OpenAI envelope in case the endpoint replies with it.
func (s *CodexStandard) DecodeModelsPage(body []byte) (ModelPage, error) {
	var result struct {
		Models []struct {
			Slug          string `json:"slug"`
			DisplayName   string `json:"display_name"`
			ContextWindow int    `json:"context_window"`
			ID            string `json:"id"`
			Name          string `json:"name"`
		} `json:"models"`
	}
	if err := json.Unmarshal(body, &result); err != nil || len(result.Models) == 0 {
		// Fall back to the OpenAI shape in case the endpoint replies with it.
		return s.OpenAIResponsesStandard.DecodeModelsPage(body)
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
	return ModelPage{Models: models}, nil
}

// SessionHeaders implements SessionScope: the ChatGPT subscription backend
// requires originator, beta, account, and stable session/thread identifiers
// on every request.
func (s *CodexStandard) SessionHeaders(auth AuthContext, sessionID string) http.Header {
	headers := http.Header{}
	headers.Set("originator", openAIOAuthOrigin)
	headers.Set("openai-beta", "responses=experimental")
	headers.Set("session-id", sessionID)
	headers.Set("thread-id", sessionID)
	headers.Set("User-Agent", "crude/"+codexClientVersion)
	if auth.OAuth != nil && auth.OAuth.AccountID != "" {
		headers.Set("chatgpt-account-id", auth.OAuth.AccountID)
	}
	return headers
}
