package mcpclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"reflect"
	"strings"
	"unicode/utf8"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	captypes "crude/types/capability"
)

var errContentBudget = errors.New("MCP result exceeds the 1 MiB output budget")

func resolveOutputSchema(encoded []byte) (*jsonschema.Resolved, error) {
	var schema jsonschema.Schema
	if err := json.Unmarshal(encoded, &schema); err != nil {
		return nil, err
	}
	// The SDK's resolver rejects external references with a nil Loader: discovery
	// is not authority to fetch another schema or access local files.
	return schema.Resolve(nil)
}

func validateStructuredOutput(schema *jsonschema.Resolved, value any) error {
	if value == nil {
		return errors.New("MCP declared output schema but returned no structured content")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("MCP structured content is not valid JSON")
	}
	if len(encoded) > maxResultBytes {
		return errContentBudget
	}
	var instance any
	if err := json.Unmarshal(encoded, &instance); err != nil {
		return errors.New("MCP structured content is not valid JSON")
	}
	if err := schema.Validate(instance); err != nil {
		// Do not echo unredacted values or schema-authored diagnostics.
		return errors.New("MCP structured content does not satisfy the declared output schema")
	}
	return nil
}

// The neutral contract has no role field. Keep attribution separately from
// content text so an external prompt cannot impersonate local user/system input.
type promptAttribution struct {
	ConnectionID string                     `json:"mcp_connection_id"`
	Name         string                     `json:"mcp_prompt_name"`
	Messages     []promptMessageAttribution `json:"mcp_source_messages"`
}

type promptMessageAttribution struct {
	Role         string `json:"source_role"`
	ContentIndex int    `json:"content_index"`
}

type contentProjection struct {
	manager *Manager
	config  captypes.ConnectionConfig
	result  captypes.Result
	used    int
}

func newContentProjection(manager *Manager, config captypes.ConnectionConfig) *contentProjection {
	// Reserve the enclosing Result fields and array delimiters.
	return &contentProjection{manager: manager, config: config, used: 1024}
}

func (p *contentProjection) append(content captypes.Content, data []byte) error {
	if len(content.Text) > maxResultBytes-p.used {
		return errContentBudget
	}
	content.Text = p.manager.redact(p.config, content.Text)
	return p.appendSanitized(content, data)
}

func (p *contentProjection) appendSanitized(content captypes.Content, data []byte) error {
	// Avoid allocating a huge base64 string or escaped JSON string just to reject it.
	remaining := maxResultBytes - p.used
	if len(data) > remaining/4*3 || len(content.Text)+len(content.URI)+len(content.MIMEType)+len(content.Type) > remaining {
		return errContentBudget
	}
	content.Data = base64.StdEncoding.EncodeToString(data)
	content.URI = p.manager.redact(p.config, content.URI)
	encoded, err := json.Marshal(content)
	if err != nil {
		return err
	}
	if len(encoded)+1 > remaining {
		return errContentBudget
	}
	p.used += len(encoded) + 1
	p.result.Content = append(p.result.Content, content)
	return nil
}

func (p *contentProjection) appendResource(resource *mcp.ResourceContents) error {
	if resource == nil {
		return errors.New("MCP content contains a nil embedded resource")
	}
	blob := resource.Blob
	mediaType, _, err := mime.ParseMediaType(resource.MIMEType)
	if err == nil && (strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") || mediaType == "application/xml" || strings.HasSuffix(mediaType, "+xml")) && len(blob) > 0 {
		if len(blob) > maxResultBytes-p.used {
			return errContentBudget
		}
		if !utf8.Valid(blob) {
			return errors.New("MCP text resource blob is not UTF-8")
		}
		if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
			blob, err = p.redactJSON(blob)
			if err != nil {
				return err
			}
		} else {
			blob = []byte(p.manager.redact(p.config, string(blob)))
		}
	}
	return p.append(captypes.Content{Type: "resource", Text: resource.Text, URI: resource.URI, MIMEType: resource.MIMEType}, blob)
}

func (p *contentProjection) appendContent(item mcp.Content) error {
	if item == nil || reflect.ValueOf(item).Kind() == reflect.Pointer && reflect.ValueOf(item).IsNil() {
		return errors.New("MCP content contains a nil block")
	}
	switch content := item.(type) {
	case *mcp.TextContent:
		return p.append(captypes.Content{Type: "text", Text: content.Text}, nil)
	case *mcp.ImageContent:
		return p.append(captypes.Content{Type: "image", MIMEType: content.MIMEType}, content.Data)
	case *mcp.AudioContent:
		return p.append(captypes.Content{Type: "audio", MIMEType: content.MIMEType}, content.Data)
	case *mcp.EmbeddedResource:
		return p.appendResource(content.Resource)
	case *mcp.ResourceLink:
		// Links are inspectable source references, not permission to fetch their URI.
		metadata := struct {
			ConnectionID string `json:"mcp_connection_id"`
			Name         string `json:"name"`
			Title        string `json:"title,omitempty"`
			Description  string `json:"description,omitempty"`
			Size         *int64 `json:"size,omitempty"`
		}{p.config.ID, p.manager.redact(p.config, content.Name), p.manager.redact(p.config, content.Title), p.manager.redact(p.config, content.Description), content.Size}
		if len(metadata.ConnectionID)+len(metadata.Name)+len(metadata.Title)+len(metadata.Description) > maxResultBytes-p.used {
			return errContentBudget
		}
		encoded, err := json.Marshal(metadata)
		if err != nil {
			return err
		}
		return p.appendSanitized(captypes.Content{Type: "resource_link", URI: content.URI, MIMEType: content.MIMEType, Text: string(encoded)}, nil)
	default:
		// Sampling-only/future SDK types are never rendered as raw JSON (which
		// could contain nested media or executable-looking tool arguments).
		return p.append(captypes.Content{Type: "unsupported", Text: fmt.Sprintf("Unsupported MCP content type %T", item)}, nil)
	}
}

func (p *contentProjection) structured(value any) error {
	if value == nil {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return errors.New("MCP structured content is not valid JSON")
	}
	if len(encoded) > maxResultBytes-p.used {
		return errContentBudget
	}
	// Protocol-authored provenance has fixed keys, roles and indices. Only the
	// external prompt name is text; credential substrings must not corrupt IDs.
	if attribution, ok := value.(promptAttribution); ok {
		attribution.Name = p.manager.redact(p.config, attribution.Name)
		encoded, err = json.Marshal(attribution)
	} else {
		encoded, err = p.redactJSON(encoded)
	}
	if err != nil {
		return err
	}
	if len(encoded) > maxResultBytes-p.used {
		return errContentBudget
	}
	p.used += len(encoded)
	p.result.Structured = encoded
	return nil
}

// External keys are redacted too. Colliding redacted keys fail closed rather
// than silently overwriting evidence. Numeric credentials become JSON strings;
// ordinary numbers, booleans and null retain their JSON types.
func (p *contentProjection) redactJSON(encoded []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil || !json.Valid(encoded) {
		return nil, errors.New("MCP structured content is not valid JSON")
	}
	var walk func(any) (any, error)
	walk = func(value any) (any, error) {
		switch v := value.(type) {
		case string:
			return p.manager.redact(p.config, v), nil
		case json.Number:
			if p.manager.redact(p.config, string(v)) != string(v) {
				return "[redacted]", nil
			}
		case []any:
			for i, item := range v {
				next, err := walk(item)
				if err != nil {
					return nil, err
				}
				v[i] = next
			}
		case map[string]any:
			out := make(map[string]any, len(v))
			for key, item := range v {
				key = p.manager.redact(p.config, key)
				if _, exists := out[key]; exists {
					return nil, errors.New("MCP structured keys collide after credential redaction")
				}
				next, err := walk(item)
				if err != nil {
					return nil, err
				}
				out[key] = next
			}
			return out, nil
		}
		return value, nil
	}
	value, err := walk(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(value)
}

func (p *contentProjection) acknowledgedFailure(effect captypes.Effect, cause error) (captypes.Result, error) {
	status := "success"
	if p.result.IsError {
		status = "error"
	}
	message := fmt.Sprintf("MCP server acknowledged %s (effect: %s). LOCAL evidence projection failed; some evidence was omitted. Do not replay this operation.", status, effect)
	// This reserved, locally authored status never goes through secret replacement.
	p.result.Content = append(p.result.Content, captypes.Content{Type: "text", Text: message})
	result, err := p.finish()
	if err != nil {
		return result, err
	}
	return result, fmt.Errorf("%s: %w", message, cause)
}

func (p *contentProjection) finish() (captypes.Result, error) {
	encoded, err := json.Marshal(p.result)
	if err != nil {
		return captypes.Result{IsError: p.result.IsError}, err
	}
	if len(encoded) > maxResultBytes {
		return captypes.Result{IsError: p.result.IsError}, errContentBudget
	}
	return p.result, nil
}
