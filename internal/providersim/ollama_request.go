package providersim

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
)

func (s *Server) readOllama(w http.ResponseWriter, r *http.Request, schema string) (Request, map[string]any, bool) {
	req, err := receive(Ollama, r)
	if err != nil {
		s.rejectOllama(w, req, err)
		return req, nil, false
	}
	b, err := wireBundle(Ollama)
	if err != nil {
		s.report("simulate Ollama: %v", err)
		writeJSON(w, http.StatusInternalServerError, ollamaError("Ollama schema is unavailable."))
		return req, nil, false
	}
	obj, err := validateOllama(b, schema, req.Raw)
	req.Model = jsonField[string](obj, "model")
	if err != nil {
		s.rejectOllama(w, req, err)
		return req, nil, false
	}
	return req, obj, true
}

func (s *Server) rejectOllama(w http.ResponseWriter, req Request, err error) {
	req.Violation = err.Error()
	s.observe(req)
	s.report("simulate Ollama %s: %v", req.Path, err)
	writeJSON(w, http.StatusBadRequest, ollamaError(err.Error()))
}

func validateOllama(b *bundle, schema string, raw []byte) (map[string]any, error) {
	value, err := decodeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("/: %w", err)
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("/: expected an object")
	}
	// Gin's typed decoder ignores unknown top-level fields. Exclude them from
	// schema number conversion too; even arbitrarily large unknown numbers
	// need not be decoded to a Go number.
	known := make(map[string]any, len(b.fields[schema]))
	for key := range b.fields[schema] {
		if v, exists := obj[key]; exists {
			known[key] = v
		}
	}
	if err := b.validate(schema, known); err != nil {
		if missing := b.missingField(schema, known); missing != "" {
			return obj, fmt.Errorf("%s: %w", jsonPointer("", missing), err)
		}
		return obj, err
	}
	if jsonField[string](obj, "model") == "" {
		return obj, fmt.Errorf("/model: model is required")
	}
	if schema == ollamaChatRequest {
		return obj, validateOllamaMessages(jsonField[[]any](obj, "messages"))
	}
	return obj, nil
}

// The pinned API type defines replay fields that the OpenAPI request
// components omit. Check their types without closing the objects.
func validateOllamaMessages(messages []any) error {
	for i, item := range messages {
		msg, _ := item.(map[string]any)
		path := jsonPointer("/messages", strconv.Itoa(i))
		for _, field := range []string{"thinking", "tool_name", "tool_call_id"} {
			if v, supplied := msg[field]; supplied {
				if _, ok := v.(string); !ok {
					return fmt.Errorf("%s: expected a string", jsonPointer(path, field))
				}
			}
		}
		for j, item := range jsonField[[]any](msg, "tool_calls") {
			call, _ := item.(map[string]any)
			callPath := jsonPointer(path+"/tool_calls", strconv.Itoa(j))
			if id, supplied := call["id"]; supplied {
				if _, ok := id.(string); !ok {
					return fmt.Errorf("%s/id: expected a string", callPath)
				}
			}
			if index, supplied := jsonField[map[string]any](call, "function")["index"]; supplied {
				number, _ := index.(json.Number)
				if _, err := number.Int64(); err != nil {
					return fmt.Errorf("%s/function/index: expected an integer", callPath)
				}
			}
		}
	}
	return nil
}

func decodeOllama(req *Request, obj map[string]any) []block {
	stream, specified := obj["stream"].(bool)
	req.Stream = !specified || stream
	if think, supplied := obj["think"]; supplied {
		req.Reasoning = jsonBytes(think)
	}
	req.OutputLimit = jsonField[json.Number](jsonField[map[string]any](obj, "options"), "num_predict")
	tools := jsonField[[]any](obj, "tools")
	messages := jsonField[[]any](obj, "messages")
	blocks := make([]block, 0, len(messages)+1)
	// Synthetic cache layout: one declaration block then one block per message
	// in wire order. This is not a model-specific chat template.
	declarations := block{path: "/tools"}
	if len(tools) > 0 {
		declarations.text = string(jsonBytes(tools))
	}
	blocks = append(blocks, declarations)
	for _, item := range tools {
		tool, _ := item.(map[string]any)
		fn := jsonField[map[string]any](tool, "function")
		req.Tools = append(req.Tools, Tool{
			Name: jsonField[string](fn, "name"), Description: jsonField[string](fn, "description"),
			Schema: jsonBytes(fn["parameters"]), Raw: jsonBytes(tool),
		})
	}
	for i, item := range messages {
		value, _ := item.(map[string]any)
		path := jsonPointer("/messages", strconv.Itoa(i))
		blocks = append(blocks, block{path: path, text: string(jsonBytes(value))})
		msg := decodeOllamaMessage(value, path)
		if msg.Role == "system" {
			for _, part := range msg.Parts {
				part.Role = msg.Role
				req.System = append(req.System, part)
			}
		} else {
			req.Messages = append(req.Messages, msg)
		}
	}
	req.Effective = req.Conversation
	return blocks
}

func decodeOllamaMessage(value map[string]any, path string) Message {
	msg := Message{Role: jsonField[string](value, "role"), Name: jsonField[string](value, "tool_name")}
	if thinking, ok := value["thinking"].(string); ok && thinking != "" {
		msg.Parts = append(msg.Parts, Part{Kind: PartReasoning, Path: path + "/thinking", Text: thinking})
	}
	text := jsonField[string](value, "content")
	switch {
	case msg.Role == "tool":
		msg.Parts = append(msg.Parts, Part{
			Kind: PartToolResult, Path: path + "/content", Text: text, CallID: jsonField[string](value, "tool_call_id"),
		})
	case text != "":
		msg.Parts = append(msg.Parts, Part{Kind: PartText, Path: path + "/content", Text: text})
	}
	for i, image := range jsonField[[]any](value, "images") {
		msg.Parts = append(msg.Parts, Part{
			Kind: PartImage, Path: jsonPointer(path+"/images", strconv.Itoa(i)), Raw: jsonBytes(image),
		})
	}
	for i, item := range jsonField[[]any](value, "tool_calls") {
		call, _ := item.(map[string]any)
		fn := jsonField[map[string]any](call, "function")
		args := json.RawMessage(`{}`)
		if v, ok := fn["arguments"]; ok {
			args = jsonBytes(v)
		}
		msg.Parts = append(msg.Parts, Part{
			Kind: PartToolCall, Path: jsonPointer(path+"/tool_calls", strconv.Itoa(i)), Raw: jsonBytes(call),
			Call: ToolCall{ID: jsonField[string](call, "id"), Name: jsonField[string](fn, "name"), Arguments: string(args)},
		})
	}
	return msg
}
