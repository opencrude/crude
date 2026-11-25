package providersim

import (
	"encoding/json"
	"fmt"
	"strings"
)

func validateResponse(b *bundle, component string, obj responseObject) (string, error) {
	if field, unknown := b.unknownField(component, obj, ""); unknown {
		return field, fmt.Errorf("%s: unknown top-level field", jsonPointer("", field))
	}
	if err := b.validate(component, obj); err != nil {
		return b.missingField(component, obj), err
	}
	// The schema allows prompt templates to supply a model. This simulator has
	// no remote prompt-template store; this synthetic profile needs an explicit model.
	if jsonField[string](obj, "model") == "" {
		return "model", fmt.Errorf("/model: supply a model ID")
	}
	return "", nil
}

func decodeResponse(req *Request, obj responseObject) []responseObject {
	req.Model = jsonField[string](obj, "model")
	req.Stream = jsonField[bool](obj, "stream")
	if store, ok := obj["store"].(bool); ok {
		req.Store = &store
	}
	req.PreviousResponseID = jsonField[string](obj, "previous_response_id")
	req.OutputLimit = jsonField[json.Number](obj, "max_output_tokens")
	if value, ok := obj["tool_choice"]; ok {
		req.ToolChoice = jsonBytes(value)
	}
	for _, key := range []string{"prompt_cache_key", "prompt_cache_options", "prompt_cache_retention"} {
		if mark, ok := obj[key]; ok {
			req.CacheMarkers = append(req.CacheMarkers, CacheMarker{Path: "/" + key, Value: jsonBytes(mark)})
		}
	}
	var items []responseObject
	switch in := obj["input"].(type) {
	case string:
		items = []responseObject{{"role": "user", "content": in}}
	case []any:
		for _, value := range in {
			item, _ := value.(responseObject)
			items = append(items, item)
		}
	}
	req.Conversation = projectResponse(obj, items)
	for i, item := range items {
		for j, raw := range jsonField[[]any](item, "content") {
			part, _ := raw.(responseObject)
			if mark, ok := part["prompt_cache_breakpoint"]; ok {
				req.CacheMarkers = append(req.CacheMarkers, CacheMarker{Path: fmt.Sprintf("/input/%d/content/%d/prompt_cache_breakpoint", i, j), Value: jsonBytes(mark)})
			}
		}
	}
	return items
}

func projectResponse(obj responseObject, items []responseObject) Conversation {
	var c Conversation
	if v, ok := obj["instructions"].(string); ok {
		c.System = append(c.System, Part{Kind: PartText, Role: "system", Path: "/instructions", Text: v})
	}
	for _, v := range jsonField[[]any](obj, "tools") {
		tool, _ := v.(responseObject)
		c.Tools = append(c.Tools, chatDeclaration(tool, tool))
	}
	for i, item := range items {
		path := fmt.Sprintf("/input/%d", i)
		msg := responseMessage(item, path)
		if msg.Role == "system" || msg.Role == "developer" {
			for _, p := range msg.Parts {
				p.Role = msg.Role
				c.System = append(c.System, p)
			}
		} else {
			c.Messages = append(c.Messages, msg)
		}
	}
	return c
}

func responseMessage(item responseObject, path string) Message {
	role := jsonField[string](item, "role")
	if role != "" {
		return Message{Role: role, Parts: responseParts(path+"/content", item["content"])}
	}
	p := Part{Kind: PartOpaque, Path: path, Raw: jsonBytes(item)}
	role = "assistant"
	switch item["type"] {
	case "function_call", "custom_tool_call":
		field := "arguments"
		if item["type"] == "custom_tool_call" {
			field = "input"
		}
		p.Kind = PartToolCall
		p.Call = ToolCall{ID: jsonField[string](item, "call_id"), Name: jsonField[string](item, "name"), Arguments: jsonField[string](item, field)}
	case "function_call_output", "custom_tool_call_output":
		role = "tool"
		p.Kind = PartToolResult
		p.CallID = jsonField[string](item, "call_id")
		p.Text = joinResponseText(responseParts(path+"/output", item["output"]))
	case "reasoning":
		p.Kind = PartReasoning
		p.Encrypted = jsonField[string](item, "encrypted_content")
		p.Text = joinResponseText(responseParts(path+"/summary", item["summary"]))
		if p.Text == "" {
			p.Text = joinResponseText(responseParts(path+"/content", item["content"]))
		}
	}
	return Message{Role: role, Parts: []Part{p}}
}

func responseParts(path string, value any) []Part {
	if text, ok := value.(string); ok {
		return []Part{{Kind: PartText, Path: path, Text: text}}
	}
	var parts []Part
	values, _ := value.([]any)
	for i, raw := range values {
		v, _ := raw.(responseObject)
		p := Part{Kind: PartOpaque, Path: fmt.Sprintf("%s/%d", path, i), Raw: jsonBytes(raw)}
		switch v["type"] {
		case "input_text", "output_text", "summary_text", "reasoning_text":
			p.Kind = PartText
			p.Text = jsonField[string](v, "text")
		case "refusal":
			p.Kind = PartRefusal
			p.Text = jsonField[string](v, "refusal")
		case "input_image":
			p.Kind = PartImage
			p.ImageURL = jsonField[string](v, "image_url")
		}
		parts = append(parts, p)
	}
	return parts
}

func joinResponseText(parts []Part) string {
	var b strings.Builder
	for _, part := range parts {
		b.WriteString(part.Text)
	}
	return b.String()
}

// Synthetic cache rendering: declarations, per-request instructions, then
// effective wire items (opaque compacted blocks cost their rendered size).
func responseBlocks(obj responseObject, items []responseObject) []block {
	blocks := []block{{path: "/tools"}, {path: "/instructions"}}
	if tools := obj["tools"]; tools != nil {
		blocks[0].text = string(jsonBytes(tools))
	}
	if instructions := obj["instructions"]; instructions != nil {
		blocks[1].text = string(jsonBytes(instructions))
	}
	for i, it := range items {
		blocks = append(blocks, block{path: fmt.Sprintf("/input/%d", i), text: string(jsonBytes(it))})
	}
	return blocks
}

func responseThreshold(obj responseObject, tokens int) bool {
	for _, raw := range jsonField[[]any](obj, "context_management") {
		cfg, _ := raw.(responseObject)
		if cfg["type"] == "compaction" {
			n, err := jsonField[json.Number](cfg, "compact_threshold").Int64()
			if err == nil && int64(tokens) > n {
				return true
			}
		}
	}
	return false
}
