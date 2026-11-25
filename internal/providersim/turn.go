package providersim

import "slices"

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
	Finish FinishReason
	fault  fault
	gate   *Gate
}

// Text builds a turn with visible text.
func Text(text string) Turn { return Turn{Text: text} }

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
	t.Usage = &usage
	return t
}

// WithFinish overrides the inferred finish reason for t.
func (t Turn) WithFinish(reason FinishReason) Turn {
	t.Finish = reason
	return t
}

// WithGate waits for gate release before writing t's first event or body.
func (t Turn) WithGate(gate *Gate) Turn {
	t.gate = gate
	return t
}

func (t Turn) snapshot() Turn {
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
