package main

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

type frame struct {
	event, id string
	obj       map[string]any
	done      bool
}

func set(list string) map[string]bool {
	m := map[string]bool{}
	for _, x := range strings.Fields(list) {
		m[x] = true
	}
	return m
}

// Only protocol field names enter YAML. User maps, arbitrary annotation keys,
// model-generated prose, SSE comments and free-form fields are not recordings.
var structure = set(`access_programs cache_creation cache_write_tokens_details candidates choices completed_at
completion_tokens_details content content_block created created_at delta error format
incomplete_details input_tokens_details instructions interaction item message model
modelVersion output output_tokens_details part parts prompt_cache_retention
prompt_tokens_details reasoning refusal response server_tool_use step stop_sequence summary
text tool_use tools_used updated usage usageMetadata`)
var literals = set(`assistant model stop STOP end_turn auto default disabled message output_text text text_delta
completed in_progress interaction model_output chat.completion.chunk text_completion
response response.created response.in_progress response.completed response.output_item.added
response.output_item.done response.content_part.added response.content_part.done
response.output_text.delta response.output_text.done ping message_start message_stop
message_delta content_block_start content_block_delta content_block_stop interaction.created
interaction.completed interaction.status_update step.start step.delta step.stop done low
medium high none`)
var opaque = set(`id item_id request_id event_id responseId system_fingerprint obfuscation signature
thoughtSignature encrypted_content`)
var enumFields = set(`object type role finish_reason finishReason status stop_reason done_reason event_type
tool_choice service_tier truncation effort verbosity`)

// No open-ended metadata, opaque histories, extra tools, or arbitrary prose. Fail closed on unfamiliar shapes.
var emptyOnly = set(`metadata annotations logprobs tools user prompt_cache_key safety_identifier`)

type scrubber struct {
	ids   map[string]string
	model string
}

func (s *scrubber) alias(v string) string {
	if v == "" {
		return ""
	}
	if s.ids[v] == "" {
		s.ids[v] = fmt.Sprintf("fixture_%d", len(s.ids)+1)
	}
	return s.ids[v]
}
func (s *scrubber) clean(key string, v any) (any, error) {
	if v == nil {
		return nil, nil
	}
	if emptyOnly[key] {
		switch x := v.(type) {
		case map[string]any:
			if len(x) == 0 {
				return x, nil
			}
		case []any:
			if len(x) == 0 {
				return x, nil
			}
		}
		return nil, errors.New("nonempty private field")
	}
	if opaque[key] {
		raw, ok := v.(string)
		if !ok {
			return nil, errors.New("opaque field has unexpected type")
		}
		alias := s.alias(raw)
		switch key {
		case "signature", "thoughtSignature", "encrypted_content":
			return base64.StdEncoding.EncodeToString([]byte(alias)), nil
		}
		return alias, nil
	}
	switch x := v.(type) {
	case map[string]any:
		// Stable traversal keeps cross-frame aliases stable between captures.
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if !(structure[k] || opaque[k] || emptyOnly[k] || boolFields[k] || numberFields[k] || enumFields[k]) {
				return nil, errors.New("unrecognized response field")
			}
			clean, err := s.clean(k, x[k])
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			x[k] = clean
		}
		return x, nil
	case []any:
		for i, a := range x {
			clean, err := s.clean(key, a)
			if err != nil {
				return nil, err
			}
			x[i] = clean
		}
		return x, nil
	case string:
		switch {
		case key == "model" || key == "modelVersion":
			return s.model, nil
		case key == "created" || key == "created_at" || key == "updated":
			return "2025-01-01T00:00:00Z", nil
		case key == "instructions" && x == prompt:
			return x, nil
		case (key == "content" || key == "text" || key == "delta") && strings.Contains("OK", x):
			return x, nil
		case enumFields[key] && literals[x]:
			return x, nil
		}
		return nil, errors.New("unexpected free text")
	case float64:
		if key == "created" || key == "created_at" || key == "completed_at" {
			return 1735689600, nil
		}
		if !numberFields[key] {
			return nil, errors.New("unexpected numeric field")
		}
		return x, nil
	case bool:
		if !boolFields[key] {
			return nil, errors.New("unexpected boolean")
		}
		return x, nil
	default:
		return nil, errors.New("unexpected response value")
	}
}

var numberFields = set(`index sequence_number output_index content_index temperature top_p promptTokenCount
candidatesTokenCount totalTokenCount cachedContentTokenCount thoughtsTokenCount
prompt_eval_count prompt_eval_cached_count eval_count total_duration load_duration
prompt_eval_duration eval_duration total_input_tokens total_output_tokens
total_thought_tokens total_tokens total_cached_tokens input_tokens output_tokens
reasoning_tokens cached_tokens cache_write_tokens cache_creation_input_tokens
cache_read_input_tokens ephemeral_5m_input_tokens ephemeral_1h_input_tokens
completion_tokens prompt_tokens prompt_cache_hit_tokens prompt_cache_miss_tokens
max_output_tokens max_tool_calls top_logprobs frequency_penalty presence_penalty text_tokens
audio_tokens accepted_prediction_tokens rejected_prediction_tokens cache_creation_tokens`)
var boolFields = set(`parallel_tool_calls done store background`)

func parse(s scenario, body string) ([]frame, error) {
	if len(body) > maxBytes {
		return nil, errors.New("response exceeds fixture limit")
	}
	body = strings.ReplaceAll(body, "\r\n", "\n")
	var frames []frame
	var f frame
	var lines []string
	scrub := scrubber{map[string]string{}, s.fixtureModel}
	emit := func() error {
		data := strings.Join(lines, "\n")
		if data == "[DONE]" && s.media == sse {
			f.done = true
		} else {
			if err := json.Unmarshal([]byte(data), &f.obj); err != nil || f.obj == nil {
				return errors.New("expected a JSON object frame")
			}
			if _, err := scrub.clean("", f.obj); err != nil {
				return err
			}
		}
		frames = append(frames, f)
		f = frame{}
		lines = nil
		return nil
	}
	if s.media == ndjson {
		for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
			lines = []string{line}
			if err := emit(); err != nil {
				return nil, err
			}
		}
	} else {
		// Only complete SSE events; do not convert a truncated capture into success.
		for _, line := range strings.Split(strings.TrimSuffix(body, "\n"), "\n") {
			switch {
			case line == "":
				if len(lines) > 0 {
					if err := emit(); err != nil {
						return nil, err
					}
				} else {
					f = frame{}
				}
			case strings.HasPrefix(line, ":"):
				continue
			default:
				k, v, _ := strings.Cut(line, ":")
				v = strings.TrimPrefix(v, " ")
				switch k {
				case "event":
					if !literals[v] {
						return nil, errors.New("unexpected SSE event")
					}
					f.event = v
				case "data":
					lines = append(lines, v)
				case "id":
					f.id = scrub.alias(v)
				default:
					return nil, errors.New("unsupported SSE field")
				}
			}
		}
		if len(lines) > 0 {
			return nil, errors.New("incomplete SSE event")
		}
	}
	if len(frames) == 0 {
		return nil, errors.New("empty stream")
	}
	return frames, nil
}
func marshalFrames(s scenario, frames []frame) string {
	var b strings.Builder
	for _, f := range frames {
		if s.media == sse {
			if f.event != "" {
				fmt.Fprintf(&b, "event: %s\n", f.event)
			}
			if f.id != "" {
				fmt.Fprintf(&b, "id: %s\n", f.id)
			}
			b.WriteString("data: ")
		}
		if f.done {
			b.WriteString("[DONE]\n")
		} else {
			data, _ := json.Marshal(f.obj)
			b.Write(data)
			b.WriteByte('\n')
		}
		if s.media == sse {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
