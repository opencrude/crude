package providersim

import (
	"slices"
	"strings"
)

// ToolCall is a tool invocation. Arguments is the encoded input (JSON for a
// function tool), not a second parsed copy that could change stream fragments.
type ToolCall struct {
	// ID is the call identifier, allocated by the wire if empty.
	ID string
	// Name is the tool name.
	Name string
	// Arguments is the serialized tool input.
	Arguments string
}

// Thought is reasoning text and its optional wire replay payload.
type Thought struct {
	// Text is the reasoning text exposed to the client.
	Text string
	// Signature authenticates replay on wires that support it.
	Signature string
	// Encrypted carries opaque reasoning on wires that support it.
	Encrypted string
}

// Usage is the neutral token accounting for a successful turn.
type Usage struct {
	// Input is the entire logical input, including cache reads and writes.
	Input int
	// Output is the generated output, including reasoning tokens.
	Output int
	// Cached is the cache-read subset of input.
	Cached int
	// OmitCacheRead omits the wire's cache-read counter where the schema allows it.
	OmitCacheRead bool
	// CacheWrite is the cache-write subset of input.
	CacheWrite int
	// Reasoning is the reasoning subset of output.
	Reasoning int
}

// FinishReason is the neutral reason a model stopped generating.
type FinishReason string

const (
	// FinishStop indicates a natural stop.
	FinishStop FinishReason = "stop"
	// FinishLength indicates the output limit was reached.
	FinishLength FinishReason = "length"
	// FinishToolCalls indicates the model asked the client to run tools.
	FinishToolCalls FinishReason = "tool_calls"
	// FinishFiltered indicates content was filtered.
	FinishFiltered FinishReason = "content_filter"
)

// Turn describes a model reply or an intentionally injected fault. Its zero
// value produces an empty successful reply. Use the builders to compose one.
type Turn struct {
	// Text is the model's visible answer.
	Text string
	// Reasoning is the model's reasoning, on wires that expose it.
	Reasoning Thought
	// Tools is the list of tool calls sent by the model.
	Tools []ToolCall
	// Refusal is the model's refusal text.
	Refusal string
	// Usage overrides all synthetic usage counters when non-nil.
	Usage *Usage
	// Finish overrides the inferred finish reason when non-empty.
	Finish     FinishReason
	fault      fault
	gate       *Gate
	gateAfter  int
	omitUsage  bool
	textDeltas []string
}

// Text builds a turn with visible text.
func Text(text string) Turn { return Turn{}.WithText(text) }

// WithText replaces t's visible answer and resets any scripted text deltas.
func (t Turn) WithText(text string) Turn {
	t.Text, t.textDeltas = text, nil
	return t
}

// WithTextDeltas replaces t's visible answer with the joined parts. A stream
// sends one text delta per part; a non-streaming request gets the whole answer.
func (t Turn) WithTextDeltas(parts ...string) Turn {
	t.Text, t.textDeltas = strings.Join(parts, ""), slices.Clone(parts)
	return t
}

// mergedDeltas preserves the refusal on wires that encode it as visible text.
func (t Turn) mergedDeltas() []string {
	if len(t.textDeltas) == 0 || t.Refusal == "" {
		return t.textDeltas
	}
	return append(slices.Clip(t.textDeltas), t.Refusal)
}

// textChunks keeps the wire's original whole-field boundary unless a scenario
// explicitly specifies a sequence of text events.
func textChunks(parts []string, whole string) []string {
	if len(parts) == 0 {
		return []string{whole}
	}
	return parts
}

// Reasoning builds a turn with reasoning and optional replay payloads.
func Reasoning(text, signature, encrypted string) Turn {
	return Turn{}.WithReasoning(text, signature, encrypted)
}

// CallTools builds a turn with the given tool calls.
func CallTools(calls ...ToolCall) Turn { return Turn{}.WithTools(calls...) }

// Refusal builds a turn with refusal text.
func Refusal(text string) Turn { return Turn{Refusal: text} }

// WithReasoning adds reasoning and optional replay payloads to t.
func (t Turn) WithReasoning(text, signature, encrypted string) Turn {
	t.Reasoning = Thought{Text: text, Signature: signature, Encrypted: encrypted}
	return t
}

// WithTools replaces t's tool calls.
func (t Turn) WithTools(calls ...ToolCall) Turn {
	t.Tools = slices.Clone(calls)
	return t
}

// WithRefusal adds refusal text to t.
func (t Turn) WithRefusal(text string) Turn {
	t.Refusal = text
	return t
}

// WithUsage replaces all synthetic token counters for t.
func (t Turn) WithUsage(usage Usage) Turn {
	t.omitUsage = false
	t.Usage = &usage
	return t
}

// WithoutUsage omits usage counts where the wire's schema allows it.
// A wire that requires usage reports a fixture error.
func (t Turn) WithoutUsage() Turn {
	t.Usage = nil
	t.omitUsage = true
	return t
}

// WithFinish overrides the inferred finish reason for t.
func (t Turn) WithFinish(reason FinishReason) Turn {
	t.Finish = reason
	return t
}

// WithGate waits for gate release before writing t's first event or body.
func (t Turn) WithGate(gate *Gate) Turn {
	return t.WithGateAfter(0, gate)
}

// WithGateAfter waits at gate after events successful stream events have been
// written and flushed. Zero waits before the first event, like WithGate.
// A non-streamed body or HTTP error waits before it is written.
func (t Turn) WithGateAfter(events int, gate *Gate) Turn {
	t.gate = gate
	t.gateAfter = events
	return t
}

func (t Turn) snapshot() Turn {
	// Text is public. A direct replacement after WithTextDeltas also wins.
	if len(t.textDeltas) > 0 && strings.Join(t.textDeltas, "") != t.Text {
		t.textDeltas = nil
	}
	t.Tools = slices.Clone(t.Tools)
	if t.Usage != nil {
		t = t.WithUsage(*t.Usage)
	}
	return t
}

func (t Turn) finish() FinishReason {
	if t.Finish != "" {
		return t.Finish
	}
	if len(t.Tools) != 0 {
		return FinishToolCalls
	}
	return FinishStop
}
