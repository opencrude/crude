package providersim

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type googleObject = map[string]any

func googleObjectAt(obj googleObject, key string) googleObject {
	value, _ := obj[key].(googleObject)
	return value
}

func googleString(obj googleObject, key string) string {
	value, _ := obj[key].(string)
	return value
}

func googleArray(value any) []any {
	items, _ := value.([]any)
	return items
}

func googleOptionalJSON(value any) json.RawMessage {
	if value == nil {
		return nil
	}
	return jsonBytes(value)
}

func googleRequestObject(raw []byte) (googleObject, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	obj, ok := value.(googleObject)
	if !ok {
		return nil, fmt.Errorf("/: expected a JSON object")
	}
	return obj, nil
}

func validateGemini(b *bundle, obj googleObject) error {
	if field, unknown := b.unknownField(geminiRequestSchema, obj, ""); unknown {
		return fmt.Errorf("%s: unknown field", jsonPointer("", field))
	}
	config := googleObjectAt(obj, "generationConfig")
	if field, unknown := b.unknownField("GenerationConfig", config, ""); unknown {
		return fmt.Errorf("%s: unknown field", jsonPointer("/generationConfig", field))
	}
	if path, unknown := unknownGoogleField(googleObjectAt(config, "thinkingConfig"), "/generationConfig/thinkingConfig", "includeThoughts", "thinkingBudget", "thinkingLevel"); unknown {
		return fmt.Errorf("%s: unknown field", path)
	}
	if err := b.validate(geminiRequestSchema, obj); err != nil {
		return err
	}
	if len(googleArray(obj["contents"])) == 0 {
		return fmt.Errorf("/contents: supply at least one content item")
	}
	return nil
}

func decodeGemini(req *Request, obj googleObject) []block {
	var blocks []block
	add := func(path string, value any) {
		if value != nil {
			blocks = append(blocks, block{path: path, text: string(jsonBytes(value))})
		}
	}
	config := googleObjectAt(obj, "generationConfig")
	req.OutputLimit, _ = config["maxOutputTokens"].(json.Number)
	req.Reasoning = googleOptionalJSON(config["thinkingConfig"])
	req.Store = googleBoolPointer(obj["store"])
	toolConfig := googleObjectAt(obj, "toolConfig")
	req.ToolChoice = googleOptionalJSON(toolConfig["functionCallingConfig"])
	if cached := obj["cachedContent"]; cached != nil {
		req.CacheMarkers = []CacheMarker{{Path: "/cachedContent", Value: jsonBytes(cached)}}
		add("/cachedContent", cached)
	}
	// Synthetic rendering: system, tools, tool config, then content in wire
	// order. Whole content items retain part and role boundaries.
	if system := googleObjectAt(obj, "systemInstruction"); system != nil {
		req.System = geminiParts(system, "/systemInstruction")
		for i := range req.System {
			req.System[i].Role = "system"
		}
		add("/systemInstruction", system)
	}
	for i, value := range googleArray(obj["tools"]) {
		path := indexedGooglePath("/tools", i)
		add(path, value)
		tool, _ := value.(googleObject)
		if declarations := googleArray(tool["functionDeclarations"]); len(declarations) > 0 {
			for _, raw := range declarations {
				fn, _ := raw.(googleObject)
				schema := fn["parametersJsonSchema"]
				if schema == nil {
					schema = fn["parameters"]
				}
				req.Tools = append(req.Tools, Tool{Name: googleString(fn, "name"), Description: googleString(fn, "description"), Schema: googleOptionalJSON(schema), Raw: jsonBytes(fn)})
			}
		} else {
			req.Tools = append(req.Tools, Tool{Raw: jsonBytes(value)})
		}
	}
	add("/toolConfig", obj["toolConfig"])
	for i, value := range googleArray(obj["contents"]) {
		path := indexedGooglePath("/contents", i)
		add(path, value)
		content, _ := value.(googleObject)
		req.Messages = append(req.Messages, Message{Role: googleString(content, "role"), Parts: geminiParts(content, path)})
	}
	req.Effective = req.Conversation
	return blocks
}

func indexedGooglePath(path string, i int) string { return path + "/" + strconv.Itoa(i) }

func googleBoolPointer(value any) *bool {
	if b, ok := value.(bool); ok {
		return &b
	}
	return nil
}

func geminiParts(content googleObject, path string) []Part {
	var parts []Part
	for i, value := range googleArray(content["parts"]) {
		obj, _ := value.(googleObject)
		part := Part{Kind: PartOpaque, Path: indexedGooglePath(path+"/parts", i), Signature: googleString(obj, "thoughtSignature"), Raw: jsonBytes(obj)}
		if thought, _ := obj["thought"].(bool); thought {
			part.Kind, part.Text = PartReasoning, googleString(obj, "text")
		} else if text, ok := obj["text"].(string); ok {
			part.Kind, part.Text = PartText, text
		} else if call := googleObjectAt(obj, "functionCall"); call != nil {
			part.Kind = PartToolCall
			part.Call = ToolCall{ID: googleString(call, "id"), Name: googleString(call, "name"), Arguments: string(googleOptionalJSON(call["args"]))}
		} else if result := googleObjectAt(obj, "functionResponse"); result != nil {
			part.Kind, part.CallID, part.Text = PartToolResult, googleString(result, "id"), string(googleOptionalJSON(result["response"]))
		} else if file := googleObjectAt(obj, "fileData"); strings.HasPrefix(googleString(file, "mimeType"), "image/") {
			part.Kind, part.ImageURL = PartImage, googleString(file, "fileUri")
		} else if inline := googleObjectAt(obj, "inlineData"); strings.HasPrefix(googleString(inline, "mimeType"), "image/") {
			part.Kind = PartImage
		}
		parts = append(parts, part)
	}
	return parts
}

func googleArguments(call ToolCall) (googleObject, error) {
	if call.Arguments == "" {
		return googleObject{}, nil
	}
	return googleRequestObject([]byte(call.Arguments))
}
