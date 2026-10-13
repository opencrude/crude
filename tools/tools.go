package tools

import (
	"bytes"

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
	return out
}
