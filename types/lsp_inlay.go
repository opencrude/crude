package types

import "time"

const (
	LSPInlayHints       = "hints"
	LSPInlayNull        = "null"
	LSPInlayEmpty       = "empty"
	LSPInlayUnsupported = "unsupported"
)

// LSPInlayPosition uses zero-based UTF-16 source coordinates, never display cells.
type LSPInlayPosition struct {
	Line, Character int
}

type LSPInlayRange struct {
	Start, End LSPInlayPosition
}

// LSPInlayResponse is detached transport evidence. Standard responses report no
// source version; WireVersion identifies the synchronized request source. Only
// the coordinator can supply canonical source, context and producer qualification.
type LSPInlayResponse struct {
	Boundary                      InsightBoundary
	WireVersion                   int
	JSON                          []byte
	Unsupported, ResolveSupported bool
}

// LSPInlayMarkup retains absence and string-versus-MarkupContent distinctions.
// Value is inert evidence, not terminal input or an executable action.
type LSPInlayMarkup struct {
	Present           bool
	Form, Kind, Value string
}

// LSPInlayLocation is unversioned metadata, not authority to open a target or a
// substitute for qualified navigation.
type LSPInlayLocation struct {
	URI   string
	Range LSPInlayRange
}

type LSPInlayLabelPart struct {
	Value      string
	Tooltip    LSPInlayMarkup
	Location   *LSPInlayLocation
	HasCommand bool
}

// LSPInlayHint summarizes one original response item. Index retains response
// order even at equal anchors. SourceByte is an exact offset in Boundary.Source,
// not inserted display text. Commands, edits and opaque data stay inert with the
// core's original retained set, including when this summary is changed by a caller.
type LSPInlayHint struct {
	Index, SourceByte         int
	Position                  LSPInlayPosition
	Label                     string
	Parts                     []LSPInlayLabelPart
	LabelIsParts              bool
	Kind                      int
	HasKind                   bool
	PaddingLeft, PaddingRight bool
	Tooltip                   LSPInlayMarkup
	HasTextEdits, HasData     bool
}

// LSPInlaySet is detached, range-scoped evidence, never a document mutation or
// proof of project cleanliness. The owner/view sequence prevents a late request
// from replacing newer evidence. ID/digest select the immutable original set.
type LSPInlaySet struct {
	ID, Digest, ViewID string
	Sequence           uint64
	Boundary           InsightBoundary
	WireVersion        int
	Range              LSPInlayRange
	Answer             string
	ResolveSupported   bool
	Hints              []LSPInlayHint
	ExpiresAt          time.Time
	Detail             string
}

// LSPInlayInspection exposes selected original JSON as bounded inert evidence.
// Tooltip-only resolution cannot change source, hint geometry or actions.
type LSPInlayInspection struct {
	SetID, Digest              string
	Index                      int
	Boundary                   InsightBoundary
	WireVersion                int
	Range                      LSPInlayRange
	Hint                       LSPInlayHint
	OriginalJSON, ResolvedJSON []byte
	ResolveState, Detail       string
}
