package session

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"crude/provider"
	"crude/tools"
	captypes "crude/types/capability"
)

// capabilityMediaResult projects attributed remote evidence; URIs remain labels,
// never authority to open a local file or make a second network request.
func capabilityMediaResult(result captypes.Result, original error, policy *tools.MediaReadPolicy) (tools.ToolResult, error) {
	var text strings.Builder
	out := tools.ToolResult{ContentParts: make([]provider.ContentPart, 0)}
	var blockText strings.Builder
	flush := func() {
		if value := strings.TrimSpace(blockText.String()); value != "" {
			out.ContentParts = append(out.ContentParts, provider.ContentPart{Type: "text", Text: value})
			text.WriteString(value)
			text.WriteByte('\n')
		}
		blockText.Reset()
	}
	var refused []error
	for _, block := range result.Content {
		if block.Text != "" {
			blockText.WriteString(block.Text)
			blockText.WriteByte('\n')
		}
		if block.URI != "" {
			fmt.Fprintf(&blockText, "MCP %s URI: %s\n", block.Type, block.URI)
		}
		if block.Data != "" {
			// The MCP client enforces 1 MiB result content; retain a local boundary for
			// alternate Runtime implementations before allocating decoded bytes.
			var mediaErr error
			if len(block.Data) > base64.StdEncoding.EncodedLen(1<<20) {
				mediaErr = errors.New("MCP media exceeds 1 MiB byte limit")
			} else {
				data, err := base64.StdEncoding.DecodeString(block.Data)
				if err != nil {
					mediaErr = fmt.Errorf("invalid base64 media: %w", err)
				} else {
					fmt.Fprintf(&blockText, "Binary %s · %s · %d bytes\n", block.Type, block.MIMEType, len(data))
					switch block.Type {
					case "image", "audio", "resource":
						part, err := policy.AdmitBytes("mcp-attachment", block.MIMEType, data)
						if err != nil {
							mediaErr = err
						} else {
							flush()
							out.Attachments = append(out.Attachments, part)
							out.ContentParts = append(out.ContentParts, part)
						}
					default:
						mediaErr = fmt.Errorf("unsupported MCP binary block type %q", block.Type)
					}
				}
			}
			if mediaErr != nil {
				fmt.Fprintf(&blockText, "Media refused: %v\n", mediaErr)
				refused = append(refused, mediaErr)
			}
		} else if block.Type == "image" || block.Type == "audio" || (block.Text == "" && (block.Type != "resource_link" || block.URI == "")) {
			err := fmt.Errorf("empty or malformed MCP %q block", block.Type)
			fmt.Fprintf(&blockText, "Media refused: %v\n", err)
			refused = append(refused, err)
		}
		flush()
	}
	if len(result.Structured) > 0 {
		blockText.Write(result.Structured)
		blockText.WriteByte('\n')
	}
	if result.Unknown {
		blockText.WriteString("\nResult unknown: the remote operation may have taken effect. Do not retry blindly.")
	}
	if result.IsError {
		blockText.WriteString("\nMCP server reported a tool failure.")
		if original == nil {
			original = errors.New("MCP server reported a tool failure")
		} else {
			fmt.Fprintf(&blockText, "\nMCP operation failed: %v", original)
		}
	} else if original != nil {
		fmt.Fprintf(&blockText, "\nMCP operation failed: %v", original)
	}
	flush()
	out.Content = strings.TrimSpace(text.String())
	return out, errors.Join(append([]error{original}, refused...)...)
}
