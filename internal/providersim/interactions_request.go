package providersim

import (
	"encoding/json"
	"fmt"
	"slices"
)

func validateInteraction(b *bundle, obj googleObject) (string, error) {
	// Select the request branch before checking extra fields. The SDK's
	// open objects do not encode the server's parameter_unknown behavior.
	root := "CreateModelInteraction"
	if _, agent := obj["agent"]; agent {
		if _, model := obj["model"]; model {
			return "invalid_request", fmt.Errorf("/model: supply a model or an agent, not both")
		}
		root = "CreateAgentInteraction"
	}
	if field, unknown := b.unknownField(root, obj, ""); unknown {
		return "parameter_unknown", fmt.Errorf("%s: unknown parameter", jsonPointer("", field))
	}
	if path, unknown := unknownGoogleField(googleObjectAt(obj, "generation_config"), "/generation_config", "image_config", "max_output_tokens", "seed", "speech_config", "stop_sequences", "temperature", "thinking_level", "thinking_summaries", "tool_choice", "top_p", "transcription_config", "video_config"); unknown {
		return "parameter_unknown", fmt.Errorf("%s: unknown parameter", path)
	}
	// Validate the selected, mutually exclusive arm, not the union wrapper:
	// the arm preserves the property's path in the schema error.
	if err := b.validate(root, obj); err != nil {
		return "invalid_request", err
	}
	if obj["background"] == true && obj["store"] == false {
		return "invalid_request", fmt.Errorf("/store: background interactions must be stored")
	}
	return "", nil
}

func decodeInteraction(req *Request, obj googleObject) []interactionStep {
	req.Model = googleString(obj, "model")
	if req.Model == "" {
		req.Model = googleString(obj, "agent")
	}
	req.Stream, _ = obj["stream"].(bool)
	req.Store = googleBoolPointer(obj["store"])
	req.PreviousResponseID = googleString(obj, "previous_interaction_id")
	if system, ok := obj["system_instruction"].(string); ok {
		req.System = []Part{{Kind: PartText, Role: "system", Text: system, Path: "/system_instruction", Raw: jsonBytes(system)}}
	}
	for _, raw := range googleArray(obj["tools"]) {
		tool, _ := raw.(googleObject)
		name := googleString(tool, "name")
		if name == "" {
			name = googleString(tool, "type")
		}
		req.Tools = append(req.Tools, Tool{Name: name, Description: googleString(tool, "description"), Schema: googleOptionalJSON(tool["parameters"]), Raw: jsonBytes(tool)})
	}
	config := googleObjectAt(obj, "generation_config")
	req.OutputLimit, _ = config["max_output_tokens"].(json.Number)
	req.ToolChoice = googleOptionalJSON(config["tool_choice"])
	thinking := googleObject{}
	for _, key := range []string{"thinking_level", "thinking_summaries"} {
		if value, ok := config[key]; ok {
			thinking[key] = value
		}
	}
	if len(thinking) != 0 {
		req.Reasoning = jsonBytes(thinking)
	}
	if cached := obj["cached_content"]; cached != nil {
		req.CacheMarkers = []CacheMarker{{Path: "/cached_content", Value: jsonBytes(cached)}}
	}
	in := interactionInput(obj["input"])
	req.Messages = projectInteraction(in)
	return in
}

type interactionStep struct {
	body googleObject
	path string
}

func interactionInput(input any) []interactionStep {
	switch value := input.(type) {
	case string:
		return []interactionStep{{interactionUser([]any{interactionText(value)}), "/input"}}
	case googleObject:
		return []interactionStep{{interactionUser([]any{value}), "/input"}}
	case []any:
		if len(value) == 0 {
			return nil
		}
		first, _ := value[0].(googleObject)
		switch googleString(first, "type") {
		case "text", "image", "audio", "video", "document":
			return []interactionStep{{interactionUser(value), "/input"}}
		}
		steps := make([]interactionStep, 0, len(value))
		for i, raw := range value {
			step, _ := raw.(googleObject)
			steps = append(steps, interactionStep{step, indexedGooglePath("/input", i)})
		}
		return steps
	}
	return nil
}

func interactionUser(content []any) googleObject {
	return googleObject{"type": "user_input", "content": content}
}

func interactionText(text string) googleObject { return googleObject{"type": "text", "text": text} }

func projectInteraction(steps []interactionStep) []Message {
	var messages []Message
	for _, step := range steps {
		obj, path := step.body, step.path
		kind := googleString(obj, "type")
		message := Message{Role: "model"}
		part := Part{Path: path, Kind: PartOpaque, Raw: jsonBytes(obj), Signature: googleString(obj, "signature")}
		switch kind {
		case "user_input", "model_output":
			if kind == "user_input" {
				message.Role = "user"
			}
			for i, item := range googleArray(obj["content"]) {
				content, _ := item.(googleObject)
				p := Part{Path: indexedGooglePath(path+"/content", i), Kind: PartOpaque, Raw: jsonBytes(content)}
				switch googleString(content, "type") {
				case "text":
					p.Kind, p.Text = PartText, googleString(content, "text")
				case "image":
					p.Kind, p.ImageURL = PartImage, googleString(content, "uri")
				}
				message.Parts = append(message.Parts, p)
			}
		case "thought":
			part.Kind = PartReasoning
			for _, item := range googleArray(obj["summary"]) {
				content, _ := item.(googleObject)
				part.Text += googleString(content, "text")
			}
		case "function_call":
			part.Kind = PartToolCall
			part.Call = ToolCall{ID: googleString(obj, "id"), Name: googleString(obj, "name"), Arguments: string(googleOptionalJSON(obj["arguments"]))}
		case "function_result":
			message.Role = "user"
			part.Kind, part.CallID = PartToolResult, googleString(obj, "call_id")
			if text, ok := obj["result"].(string); ok {
				part.Text = text
			} else {
				part.Text = string(googleOptionalJSON(obj["result"]))
			}
		}
		if kind != "model_output" && kind != "user_input" {
			message.Parts = []Part{part}
		}
		messages = append(messages, message)
	}
	return messages
}

func interactionBlocks(obj googleObject, steps []interactionStep) []block {
	var blocks []block
	// Synthetic rendering: current system and tools, then the resolved steps
	// in order. Normalize string/content shortcuts to user_input steps so a
	// stateless replay and its equivalent stateful continuation share a prefix.
	for _, key := range []string{"cached_content", "system_instruction"} {
		if value := obj[key]; value != nil {
			blocks = append(blocks, block{path: "/" + key, text: string(jsonBytes(value))})
		}
	}
	for i, tool := range googleArray(obj["tools"]) {
		blocks = append(blocks, block{path: indexedGooglePath("/tools", i), text: string(jsonBytes(tool))})
	}
	if choice := googleObjectAt(obj, "generation_config")["tool_choice"]; choice != nil {
		blocks = append(blocks, block{path: "/generation_config/tool_choice", text: string(jsonBytes(choice))})
	}
	for _, step := range steps {
		blocks = append(blocks, block{path: step.path, text: string(jsonBytes(step.body))})
	}
	return blocks
}

func interactionStepBodies(steps []interactionStep) []any {
	bodies := make([]any, 0, len(steps))
	for _, step := range steps {
		bodies = append(bodies, step.body)
	}
	return bodies
}

func unknownGoogleField(obj googleObject, parent string, allowed ...string) (string, bool) {
	var first string
	var found bool
	for key := range obj {
		if !slices.Contains(allowed, key) && (!found || key < first) {
			first, found = key, true
		}
	}
	return jsonPointer(parent, first), found
}
