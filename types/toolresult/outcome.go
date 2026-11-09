// Package toolresult carries operation facts, independently of provider and UI.
package toolresult

// State describes an operation, not the success of a tool that inspected it.
type State string

const (
	Succeeded State = "success"
	Failed    State = "error"
	Denied    State = "denied"
	Cancelled State = "cancelled"
	NotRun    State = "interrupted"
	Uncertain State = "uncertain"
)

// Outcome is immutable, presentation-neutral evidence. Summary contains facts,
// never instructions addressed to the model. ID correlates an operation across
// its initial result and later owner observations; it is not inferred from text.
type Outcome struct {
	State State  `json:"state,omitempty"`
	Code  string `json:"code,omitempty"`
	// CallID is Crude's stable call identity, not a provider's reusable ID.
	CallID  string `json:"call_id,omitempty"`
	Summary string `json:"summary,omitempty"`
	ID      string `json:"id,omitempty"`
}

func (o Outcome) Unsuccessful() bool {
	return o.State != "" && o.State != Succeeded
}

// Status preserves the established conversation lifecycle vocabulary.
func (o Outcome) Status(failed bool) string {
	switch o.State {
	case Denied, Uncertain, Failed:
		return string(Failed)
	case Succeeded, Cancelled, NotRun:
		return string(o.State)
	}
	if failed {
		return string(Failed)
	}
	return string(Succeeded)
}

func (o Outcome) PreviewPhase(failed bool) string {
	switch o.Status(failed) {
	case string(Failed):
		return "error"
	default:
		return "done"
	}
}
