package tools

import (
	"bytes"
	"encoding/json"

	"crude/prompt"
	"crude/provider"
)

// ResultContentParts is the single current tool-result content composition
// boundary. Explicit ordered parts are authoritative (including an empty slice);
// native text/attachments compose once when no ordered result was supplied.
func ResultContentParts(result ToolResult) []provider.ContentPart {
	parts := result.ContentParts
	if parts == nil {
		parts = make([]provider.ContentPart, 0, 1+len(result.Attachments))
		if result.Content != "" {
			parts = append(parts, provider.ContentPart{Type: "text", Text: result.Content})
		}
		parts = append(parts, result.Attachments...)
	}
	out := make([]provider.ContentPart, len(parts))
	for i, part := range parts {
		out[i] = part
		out[i].Data = bytes.Clone(part.Data)
		if part.ImageURL != nil {
			image := *part.ImageURL
			out[i].ImageURL = &image
		}
	}
	if result.AgentHint != "" {
		hint := prompt.ToolHint(result.AgentHint)
		// Native structured results stay one valid object. Consumers, including
		// parents handling agent_wait, must not parse JSON followed by prose.
		var object map[string]json.RawMessage
		if result.ContentParts == nil && len(out) == 1 && out[0].Type == "text" && json.Unmarshal([]byte(out[0].Text), &object) == nil && object != nil && object["agent_hint"] == nil {
			object["agent_hint"], _ = json.Marshal(hint)
			encoded, _ := json.Marshal(object)
			out[0].Text = string(encoded)
		} else if len(out) > 0 && out[len(out)-1].Type == "text" {
			out[len(out)-1].Text += "\n\n" + hint
		} else {
			out = append(out, provider.ContentPart{Type: "text", Text: hint})
		}
	}
	return out
}
