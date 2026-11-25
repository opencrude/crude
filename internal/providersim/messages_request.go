package providersim

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strings"
	"time"
)

// Request shapes: https://platform.claude.com/docs/en/api/messages/create.md
// and anthropic-sdk-python@f1edcd7d751a4cd6d72cf8b6454930d8aab2c97c.
// See schemas/manifest.json for the independently generated bundle and its pins.
type messageObject = map[string]any

const (
	demandBeta    = "compact-2026-09-04"
	thresholdBeta = "compact-2026-01-12"
)

func hasMessageBeta(h http.Header, beta string) bool {
	for _, line := range h.Values("Anthropic-Beta") {
		for b := range strings.SplitSeq(line, ",") {
			if strings.TrimSpace(b) == beta {
				return true
			}
		}
	}
	return false
}

type messageViolation struct{ path, message, code string }

func (v messageViolation) Error() string    { return v.path + ": " + v.message }
func invalidMessage(path, msg string) error { return messageViolation{path: path, message: msg} }

type compactConfig struct {
	demand, pause bool
	threshold     int
}

func messageParams(req *Request, obj messageObject, b *bundle) (compactConfig, error) {
	var cfg compactConfig
	root := "MessageCreateParams"
	if len(req.Header.Values("Anthropic-Beta")) > 0 {
		root = "Beta" + root
	}
	if obj["compaction"] != nil && !hasMessageBeta(req.Header, demandBeta) {
		return cfg, invalidMessage("/compaction", "The compaction parameter requires anthropic-beta: "+demandBeta)
	}
	if field, unknown := b.unknownField(root, obj, ""); unknown {
		return cfg, invalidMessage(jsonPointer("", field), "Extra inputs are not permitted")
	}
	if err := b.validate(root, obj); err != nil {
		if field := b.missingField(root, obj); field != "" {
			return cfg, invalidMessage(jsonPointer("", field), "Field required")
		}
		return cfg, fmt.Errorf("%s: %w", root, err)
	}
	req.Model = jsonField[string](obj, "model")
	req.OutputLimit = jsonField[json.Number](obj, "max_tokens")
	req.Stream = jsonField[bool](obj, "stream")
	if x := obj["tool_choice"]; x != nil {
		req.ToolChoice = jsonBytes(x)
	}
	if x := obj["thinking"]; x != nil {
		req.Reasoning = jsonBytes(x)
	}
	cfg.demand = obj["compaction"] != nil
	if v, err := req.OutputLimit.Int64(); err != nil || v < 0 {
		return cfg, invalidMessage("/max_tokens", "Expected a non-negative integer")
	}
	if cfg.demand {
		if !messageModelProfile(req.Model).demand {
			return cfg, invalidMessage("/compaction", "This model does not support on-demand compaction")
		}
		if obj["stop_sequences"] != nil {
			return cfg, invalidMessage("/stop_sequences", "Cannot be combined with compaction")
		}
		choice := jsonField[string](jsonField[messageObject](obj, "tool_choice"), "type")
		if choice == "any" || choice == "tool" {
			return cfg, invalidMessage("/tool_choice", "Cannot force a tool on a compaction request")
		}
		if jsonField[messageObject](obj, "output_config")["format"] != nil {
			return cfg, invalidMessage("/output_config/format", "Cannot be combined with compaction")
		}
	}
	for i, raw := range jsonField[[]any](jsonField[messageObject](obj, "context_management"), "edits") {
		edit := raw.(messageObject)
		if edit["type"] != "compact_20260112" {
			continue
		}
		path := fmt.Sprintf("/context_management/edits/%d", i)
		if !hasMessageBeta(req.Header, thresholdBeta) {
			return cfg, invalidMessage(path, "This edit requires anthropic-beta: "+thresholdBeta)
		}
		if !messageModelProfile(req.Model).threshold {
			return cfg, invalidMessage(path, "This model does not support threshold compaction")
		}
		cfg.threshold = 150000
		cfg.pause = jsonField[bool](edit, "pause_after_compaction")
		if trigger := jsonField[messageObject](edit, "trigger"); trigger != nil {
			v, err := jsonField[json.Number](trigger, "value").Int64()
			if err != nil || v < 50000 {
				return cfg, invalidMessage(path+"/trigger/value", "The threshold must be at least 50000 input tokens")
			}
			cfg.threshold = int(v)
		}
	}
	return cfg, nil
}

// synthetic rendering: one role-qualified canonical JSON block for each tool,
// system or message content block. Cache controls direct storage, not content.
// Count tokens with the shared synthetic estimator. See
// https://platform.claude.com/docs/en/build-with-claude/prompt-caching.md.
type messagePrompt struct {
	blocks   []block
	metadata []messageCacheBlock
	marks    int
}
type messageCacheBlock struct {
	typ      string
	ttl      time.Duration
	eligible bool
}

func (p *messagePrompt) add(req *Request, role, path string, v messageObject) error {
	typ := jsonField[string](v, "type")
	eligible := typ != "thinking" && typ != "redacted_thinking" && (typ != "text" || v["text"] != "")
	m := messageCacheBlock{typ: typ, eligible: eligible}
	if mark := jsonField[messageObject](v, "cache_control"); mark != nil {
		if !eligible {
			return invalidMessage(path+"/cache_control", "This block cannot be a cache breakpoint")
		}
		m.ttl = messageTTL(mark)
		req.CacheMarkers = append(req.CacheMarkers, CacheMarker{path + "/cache_control", jsonBytes(mark)})
		p.marks++
		v = maps.Clone(v)
		delete(v, "cache_control")
	}
	p.blocks = append(p.blocks, block{path: path, text: role + ":" + string(jsonBytes(v))})
	p.metadata = append(p.metadata, m)
	return nil
}
func messageTTL(mark messageObject) time.Duration {
	if mark["ttl"] == "1h" {
		return time.Hour
	}
	return 5 * time.Minute
}
func (p *messagePrompt) finish(req *Request, obj messageObject) error {
	if p.marks > 4 {
		return invalidMessage("/cache_control", "A request can have at most 4 cache breakpoints")
	}
	if mark := jsonField[messageObject](obj, "cache_control"); mark != nil {
		ttl := messageTTL(mark)
		req.CacheMarkers = append(req.CacheMarkers, CacheMarker{"/cache_control", jsonBytes(mark)})
		for i := len(p.metadata) - 1; i >= 0; i-- {
			m := &p.metadata[i]
			if !m.eligible {
				continue
			}
			if m.ttl != 0 && m.ttl != ttl {
				return invalidMessage("/cache_control", "The TTL conflicts with the last block")
			}
			if m.ttl == 0 && p.marks == 4 {
				return invalidMessage("/cache_control", "A request can have at most 4 cache breakpoints")
			}
			m.ttl = ttl
			break
		}
	}
	prev := time.Hour
	for i, b := range p.metadata {
		if b.ttl == 0 {
			continue
		}
		if b.ttl > prev {
			return invalidMessage(p.blocks[i].path, "Place 1-hour breakpoints before 5-minute breakpoints")
		}
		prev = b.ttl
	}
	return nil
}

func messageParts(path string, content any, visit func(string, messageObject) error) error {
	switch c := content.(type) {
	case string:
		return visit(path, messageObject{"type": "text", "text": c})
	case []any:
		for i, raw := range c {
			if err := visit(fmt.Sprintf("%s/%d", path, i), raw.(messageObject)); err != nil {
				return err
			}
		}
	}
	return nil
}
func projectMessagePart(path string, v messageObject) Part {
	p := Part{Path: path, Raw: jsonBytes(v), Kind: PartOpaque}
	switch v["type"] {
	case "text":
		p.Kind = PartText
		p.Text = jsonField[string](v, "text")
	case "image":
		p.Kind = PartImage
		p.ImageURL = jsonField[string](jsonField[messageObject](v, "source"), "url")
	case "thinking":
		p.Kind = PartReasoning
		p.Text = jsonField[string](v, "thinking")
		p.Signature = jsonField[string](v, "signature")
	case "redacted_thinking":
		p.Kind = PartReasoning
		p.Encrypted = jsonField[string](v, "data")
	case "tool_use":
		p.Kind = PartToolCall
		p.Call = ToolCall{jsonField[string](v, "id"), jsonField[string](v, "name"), string(jsonBytes(v["input"]))}
	case "tool_result":
		p.Kind = PartToolResult
		p.CallID = jsonField[string](v, "tool_use_id")
		var text strings.Builder
		switch c := v["content"].(type) {
		case string:
			text.WriteString(c)
		case []any:
			for _, part := range c {
				x, _ := part.(messageObject)
				text.WriteString(jsonField[string](x, "text"))
			}
		}
		p.Text = text.String()
	}
	return p
}

func (s *messageState) decode(req *Request, obj messageObject, cfg compactConfig) (messagePrompt, error) {
	var prompt messagePrompt
	for i, raw := range jsonField[[]any](obj, "tools") {
		v := raw.(messageObject)
		t := Tool{Name: jsonField[string](v, "name"), Description: jsonField[string](v, "description"), Raw: jsonBytes(v)}
		if in := v["input_schema"]; in != nil {
			t.Schema = jsonBytes(in)
		}
		req.Tools = append(req.Tools, t)
		if err := prompt.add(req, "tool", fmt.Sprintf("/tools/%d", i), v); err != nil {
			return prompt, err
		}
	}
	if err := messageParts("/system", obj["system"], func(path string, v messageObject) error {
		p := projectMessagePart(path, v)
		p.Role = "system"
		req.System = append(req.System, p)
		return prompt.add(req, "system", path, v)
	}); err != nil {
		return prompt, err
	}
	// Synthetic: put request-level message controls before the dialogue so that
	// changing them invalidates message cache entries without changing tool keys.
	controls := messageObject{}
	for _, k := range []string{"tool_choice", "thinking"} {
		if obj[k] != nil {
			controls[k] = obj[k]
		}
	}
	if effort := jsonField[messageObject](obj, "output_config")["effort"]; effort != nil {
		controls["effort"] = effort
	}
	if len(controls) > 0 {
		prompt.blocks = append(prompt.blocks, block{path: "/messages", text: string(jsonBytes(controls))})
		prompt.metadata = append(prompt.metadata, messageCacheBlock{})
	}
	base := len(prompt.blocks)
	req.Effective.System = req.System
	req.Effective.Tools = req.Tools
	for i, raw := range jsonField[[]any](obj, "messages") {
		v := raw.(messageObject)
		role := jsonField[string](v, "role")
		msg := Message{Role: role}
		effective := Message{Role: role}
		err := messageParts(fmt.Sprintf("/messages/%d/content", i), v["content"], func(path string, v messageObject) error {
			p := projectMessagePart(path, v)
			partIndex := len(msg.Parts)
			msg.Parts = append(msg.Parts, p)
			if err := s.verifyBlock(req, cfg, i, partIndex, path, v); err != nil {
				return err
			}
			if v["type"] == "compaction" {
				// Both modes pass the summary to the model. For threshold compaction,
				// only the last block and its suffix survive, even within one message.
				req.Effective.Messages = nil
				effective.Parts = nil
				prompt.blocks = prompt.blocks[:base]
				prompt.metadata = prompt.metadata[:base]
				p = Part{Path: path, Kind: PartText, Text: jsonField[string](v, "content")}
			}
			effective.Parts = append(effective.Parts, p)
			return prompt.add(req, role, path, v)
		})
		if err != nil {
			return prompt, err
		}
		req.Messages = append(req.Messages, msg)
		req.Effective.Messages = append(req.Effective.Messages, effective)
	}
	if cfg.demand {
		if len(req.Messages) == 0 {
			return prompt, messageViolation{"/messages", "There is no conversation to summarize", "compaction_nothing_to_summarize"}
		}
		last := req.Messages[len(req.Messages)-1]
		if last.Role == "assistant" {
			for _, p := range last.Parts {
				if p.Kind == PartToolCall {
					return prompt, invalidMessage(p.Path, "Send the tool result before compaction")
				}
			}
		}
	}
	return prompt, prompt.finish(req, obj)
}
