package providersim

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

func validateChat(b *bundle, value any, obj map[string]any, gateway ChatGateway) (string, error) {
	// Unknown public Chat top-level fields are server policy rather than a
	// schema assertion. Do not close nested objects or other wires' schemas.
	var extra string
	if gateway == DeepSeek {
		extra = "thinking"
	}
	if field, unknown := b.unknownField(chatRequestSchema, obj, extra); unknown {
		return field, fmt.Errorf("%s: unknown top-level field %q", jsonPointer("", field), field)
	}
	if err := b.validate(chatRequestSchema, value); err != nil {
		if missing := b.missingField(chatRequestSchema, obj); obj != nil && missing != "" {
			return missing, fmt.Errorf("%s: %w", jsonPointer("", missing), err)
		}
		return "", err
	}
	if gateway == DeepSeek {
		// Gateway extension (not the OpenAI schema): the thinking guide documents
		// a top-level toggle and string reasoning_content on assistant messages.
		if thinking, supplied := obj["thinking"]; supplied {
			toggle, _ := thinking.(map[string]any)
			switch jsonField[string](toggle, "type") {
			case "enabled", "disabled":
			default:
				return "thinking", fmt.Errorf("/thinking/type: expected enabled or disabled")
			}
		}
		for i, item := range jsonField[[]any](obj, "messages") {
			msg, _ := item.(map[string]any)
			if jsonField[string](msg, "role") != "assistant" {
				continue
			}
			if reasoning := msg["reasoning_content"]; reasoning != nil {
				if _, ok := reasoning.(string); !ok {
					return "messages", fmt.Errorf("/messages/%d/reasoning_content: expected a string", i)
				}
			}
		}
	}
	return "", nil
}

func decodeChat(req *Request, obj map[string]any, gateway ChatGateway) (bool, []block) {
	req.Stream = jsonField[bool](obj, "stream")
	if store, ok := obj["store"].(bool); ok {
		req.Store = &store
	}
	req.OutputLimit = jsonField[json.Number](obj, "max_completion_tokens")
	if req.OutputLimit == "" {
		req.OutputLimit = jsonField[json.Number](obj, "max_tokens")
	}
	if choice, ok := obj["tool_choice"]; ok {
		req.ToolChoice = jsonBytes(choice)
	} else if choice, ok := obj["function_call"]; ok {
		req.ToolChoice = jsonBytes(choice)
	}
	for _, key := range []string{"prompt_cache_key", "prompt_cache_retention", "prompt_cache_options"} {
		if mark, present := obj[key]; present {
			req.CacheMarkers = append(req.CacheMarkers, CacheMarker{Path: jsonPointer("", key), Value: jsonBytes(mark)})
		}
	}

	tools := jsonField[[]any](obj, "tools")
	functions := jsonField[[]any](obj, "functions")
	messages := jsonField[[]any](obj, "messages")
	blocks := make([]block, 0, len(messages)+2)
	// Synthetic rendering: one declaration block, then a block per message
	// in wire order. An empty tool block costs no tokens and makes adding or
	// removing tools a change to /tools rather than a shift in message paths.
	// The public prompt-caching guide names tools and messages but does not
	// expose the provider's exact prompt template or token boundaries.
	toolBlock := block{path: "/tools"}
	if len(tools) != 0 {
		toolBlock.text = string(jsonBytes(tools))
	}
	blocks = append(blocks, toolBlock)
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		name := jsonField[string](tool, "type")
		req.Tools = append(req.Tools, chatDeclaration(jsonField[map[string]any](tool, name), tool))
	}
	if len(functions) != 0 {
		blocks = append(blocks, block{path: "/functions", text: string(jsonBytes(functions))})
		for _, item := range functions {
			tool, _ := item.(map[string]any)
			req.Tools = append(req.Tools, chatDeclaration(tool, tool))
		}
	}
	_, hasTools := obj["tools"]
	replayReasoning := gateway == DeepSeek && hasTools
	for i, item := range messages {
		value, _ := item.(map[string]any)
		path := jsonPointer("/messages", strconv.Itoa(i))
		prompt := value
		if _, hasReasoning := value["reasoning_content"]; hasReasoning && !replayReasoning {
			prompt = maps.Clone(value)
			delete(prompt, "reasoning_content")
		}
		blocks = append(blocks, block{path: path, text: string(jsonBytes(prompt))})
		msg := decodeChatMessage(req, value, path, gateway)
		if msg.Role == "system" || msg.Role == "developer" {
			for _, part := range msg.Parts {
				part.Role = msg.Role
				req.System = append(req.System, part)
			}
		} else {
			req.Messages = append(req.Messages, msg)
		}
	}
	req.Effective = req.Conversation
	if gateway == DeepSeek && !replayReasoning {
		// The documented gateway ignores past reasoning when tools are absent.
		copied := false
		for i, msg := range req.Messages {
			if len(msg.Parts) != 0 && msg.Parts[0].Kind == PartReasoning {
				if !copied {
					req.Effective.Messages = slices.Clone(req.Messages)
					copied = true
				}
				req.Effective.Messages[i].Parts = msg.Parts[1:]
			}
		}
	}
	options := jsonField[map[string]any](obj, "stream_options")
	return jsonField[bool](options, "include_usage"), blocks
}

func chatDeclaration(definition, raw map[string]any) Tool {
	tool := Tool{
		Name: jsonField[string](definition, "name"), Description: jsonField[string](definition, "description"),
		Raw: jsonBytes(raw),
	}
	if schema, present := definition["parameters"]; present {
		tool.Schema = jsonBytes(schema)
	}
	return tool
}

func decodeChatMessage(req *Request, value map[string]any, path string, gateway ChatGateway) Message {
	msg := Message{Role: jsonField[string](value, "role"), Name: jsonField[string](value, "name")}
	if text, ok := value["reasoning_content"].(string); ok && gateway == DeepSeek && msg.Role == "assistant" {
		msg.Parts = append(msg.Parts, Part{Kind: PartReasoning, Path: path + "/reasoning_content", Text: text})
	}
	contentPath := path + "/content"
	content := chatContent(req, contentPath, value["content"])
	if msg.Role == "tool" || msg.Role == "function" {
		var text strings.Builder
		for _, part := range content {
			text.WriteString(part.Text)
		}
		msg.Parts = append(msg.Parts, Part{
			Kind: PartToolResult, Path: contentPath, Text: text.String(), Raw: jsonBytes(value["content"]),
			CallID: jsonField[string](value, "tool_call_id"),
		})
	} else {
		msg.Parts = append(msg.Parts, content...)
	}
	if refusal, ok := value["refusal"].(string); ok && msg.Role == "assistant" {
		msg.Parts = append(msg.Parts, Part{Kind: PartRefusal, Path: path + "/refusal", Text: refusal})
	}
	if audio := value["audio"]; audio != nil && msg.Role == "assistant" {
		msg.Parts = append(msg.Parts, Part{Kind: PartOpaque, Path: path + "/audio", Raw: jsonBytes(audio)})
	}
	for i, item := range jsonField[[]any](value, "tool_calls") {
		v, _ := item.(map[string]any)
		typ := jsonField[string](v, "type")
		definition := jsonField[map[string]any](v, typ)
		part := Part{Kind: PartToolCall, Path: jsonPointer(path+"/tool_calls", strconv.Itoa(i))}
		part.Call = chatInputCall(jsonField[string](v, "id"), definition, typ)
		msg.Parts = append(msg.Parts, part)
	}
	if call := jsonField[map[string]any](value, "function_call"); call != nil && msg.Role == "assistant" {
		msg.Parts = append(msg.Parts, Part{Kind: PartToolCall, Path: path + "/function_call", Call: chatInputCall("", call, "function")})
	}
	return msg
}

func chatContent(req *Request, path string, value any) []Part {
	switch content := value.(type) {
	case string:
		return []Part{{Kind: PartText, Path: path, Text: content}}
	case []any:
		parts := make([]Part, 0, len(content))
		for i, item := range content {
			partPath := jsonPointer(path, strconv.Itoa(i))
			v, _ := item.(map[string]any)
			parts = append(parts, chatContentPart(partPath, v))
			if mark, present := v["prompt_cache_breakpoint"]; present {
				req.CacheMarkers = append(req.CacheMarkers, CacheMarker{Path: partPath + "/prompt_cache_breakpoint", Value: jsonBytes(mark)})
			}
		}
		return parts
	default:
		return nil
	}
}

func chatContentPart(path string, value map[string]any) Part {
	switch jsonField[string](value, "type") {
	case "text":
		return Part{Kind: PartText, Path: path, Text: jsonField[string](value, "text")}
	case "refusal":
		return Part{Kind: PartRefusal, Path: path, Text: jsonField[string](value, "refusal")}
	case "image_url":
		image := jsonField[map[string]any](value, "image_url")
		return Part{Kind: PartImage, Path: path, ImageURL: jsonField[string](image, "url"), Raw: jsonBytes(image)}
	default:
		return Part{Kind: PartOpaque, Path: path, Raw: jsonBytes(value)}
	}
}

func chatInputCall(id string, obj map[string]any, typ string) ToolCall {
	argsProperty := "arguments"
	if typ == "custom" {
		argsProperty = "input"
	}
	return ToolCall{ID: id, Name: jsonField[string](obj, "name"), Arguments: jsonField[string](obj, argsProperty)}
}

func jsonField[T any](obj map[string]any, name string) T {
	value, _ := obj[name].(T)
	return value
}
