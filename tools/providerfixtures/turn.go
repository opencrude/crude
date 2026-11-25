package main

import (
	"errors"
	"strings"
)

func child(m map[string]any, k string) map[string]any { v, _ := m[k].(map[string]any); return v }
func first(m map[string]any, k string) map[string]any {
	a, _ := m[k].([]any)
	if len(a) != 1 {
		return nil
	}
	v, _ := a[0].(map[string]any)
	return v
}
func text(m map[string]any, k string) string { v, _ := m[k].(string); return v }

// Derive the turn only from the recording, not from the simulator. Require the
// fixed literal, native terminal and no events after completion (apart from a
// wire's usage-only chunk and/or explicit [DONE] sentinel).
func inspectTurn(s scenario, frames []frame) ([]string, error) {
	var parts []string
	var terminal, marker, usageOnly bool
	for _, f := range frames {
		if marker {
			return nil, errors.New("frame after end of stream")
		}
		if f.done {
			if !terminal || (f.event != "" && !(s.name == "interactions" && f.event == "done")) {
				return nil, errors.New("stream ended before its finish event")
			}
			marker = true
			continue
		}
		var part string
		m := f.obj
		var kind string
		if s.name == "interactions" {
			kind = text(m, "event_type")
		} else {
			kind = text(m, "type")
		}
		if f.event != "" && kind != f.event {
			return nil, errors.New("SSE name differs from event body")
		}
		if terminal {
			choices, ok := m["choices"].([]any)
			if usageOnly || (s.name != "chat" && s.name != "openai-completions") || !ok || len(choices) != 0 || m["usage"] == nil {
				return nil, errors.New("unexpected frame after finish")
			}
			usageOnly = true
			continue
		}
		switch s.name {
		case "chat", "openai-completions", "deepseek-fim", "codestral-fim":
			c := first(m, "choices")
			if c == nil {
				return nil, errors.New("missing choice")
			}
			if s.name == "openai-completions" || s.name == "deepseek-fim" {
				part = text(c, "text")
			} else {
				part = text(child(c, "delta"), "content")
			}
			terminal = text(c, "finish_reason") == "stop"
		case "responses", "codex":
			if kind == "response.output_text.delta" {
				part = text(m, "delta")
			}
			terminal = kind == "response.completed" && text(child(m, "response"), "status") == "completed"
		case "messages":
			if kind == "content_block_delta" && text(child(m, "delta"), "type") == "text_delta" {
				part = text(child(m, "delta"), "text")
			}
			terminal = kind == "message_stop"
		case "gemini":
			c := first(m, "candidates")
			p, _ := child(c, "content")["parts"].([]any)
			for _, v := range p {
				item, _ := v.(map[string]any)
				part += text(item, "text")
			}
			terminal = text(c, "finishReason") == "STOP"
		case "interactions":
			if kind == "step.delta" {
				part = text(child(m, "delta"), "text")
			}
			terminal = kind == "interaction.completed" && text(child(m, "interaction"), "status") == "completed"
		case "ollama":
			part = text(child(m, "message"), "content")
			terminal = m["done"] == true && text(m, "done_reason") == "stop"
		}
		if part != "" {
			parts = append(parts, part)
		}
	}
	wantsMarker := s.name == "chat" || s.name == "openai-completions" || s.name == "deepseek-fim" || s.name == "codestral-fim" || s.name == "interactions"
	if !terminal || marker != wantsMarker || strings.Join(parts, "") != "OK" {
		return nil, errors.New("expected complete OK turn and native stream terminator")
	}
	return parts, nil
}
