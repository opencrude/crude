// Package reasoning defines the neutral stream and transcript contract shared
// by protocol adapters, domain actors, persistence, and presentation. It must
// not depend on provider implementations or a rendering framework.
package reasoning

type Kind string

const (
	Unknown Kind = ""
	Raw     Kind = "raw"
	Summary Kind = "summary"
	// Provisional is a visible flattened stream without enough evidence to
	// identify its contract. It has a compact live preview and a full trace.
	Provisional Kind = "provisional"
	// InferredSummary is an explicitly measured fallback for gateways that
	// flatten the wire type. It must never override a raw/text wire event.
	InferredSummary Kind = "summary_inferred"
	Encrypted       Kind = "encrypted"
)

func (k Kind) IsSummary() bool { return k == Summary || k == InferredSummary }

func (k Kind) HasCompactPreview() bool { return k == Provisional || k.IsSummary() }

// CondensedByUsage tests an observed response, not a model-name catalogue.
// Require a large mismatch; a mild difference is not evidence that a gateway
// condensed a visible trace. Callers need independent observations to make a
// durable serving-connection decision.
func CondensedByUsage(visibleRunes, reportedTokens int, known bool) bool {
	return known && visibleRunes >= 12 && reportedTokens >= 128 && reportedTokens >= 8*((visibleRunes+3)/4)
}

// Delta carries visible text. BlockID identifies a semantic block, not a
// network packet. Signatures and ciphertext never enter the visible trace.
type Delta struct {
	Text    string
	Kind    Kind
	BlockID string
}

// Display indexes the latest semantic block in the complete visible trace.
// It contains no protocol replay state, theme, animation, or layout policy.
type Display struct {
	Kind    Kind   `json:"kind,omitempty"`
	BlockID string `json:"block_id,omitempty"`
	Start   int    `json:"start,omitempty"`
}

func (d *Display) Append(text string, delta Delta) string {
	if delta.Text == "" || delta.Kind == Encrypted {
		return text
	}
	if text == "" {
		d.Kind = delta.Kind
	} else if d.Kind == Provisional && delta.Kind != Provisional && delta.Kind != Unknown {
		d.Kind = delta.Kind
	} else if d.Kind != delta.Kind {
		// Mixed and unlabelled streams never acquire summary-only treatment.
		d.Kind = Unknown
	}
	if delta.BlockID != "" && d.BlockID != delta.BlockID {
		if text != "" {
			text += "\n\n"
		}
		d.Start = len(text)
		d.BlockID = delta.BlockID
	}
	return text + delta.Text
}
