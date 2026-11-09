package types

import (
	"slices"
	"time"
)

const (
	LSPCompletionItems       = "items"
	LSPCompletionNull        = "null"
	LSPCompletionEmpty       = "empty"
	LSPCompletionUnsupported = "unsupported"
)

// LSPCompletionResponse is detached transport evidence over the synchronized
// request source. A standard completion answer has no server source version.
// Only the manager supplies the canonical boundary; no raw item crosses to a
// shell or grants permission to execute provider commands.
type LSPCompletionResponse struct {
	Boundary                      InsightBoundary
	WireVersion                   int
	FeatureGeneration             uint64
	JSON                          []byte
	Unsupported, ResolveSupported bool
}

// LSPCompletionDocumentation preserves absence, empty evidence and the standard
// string/MarkupContent distinction. All text is inert; a markup kind is not a
// grant to run links, terminal escapes or commands.
type LSPCompletionDocumentation struct {
	HasDetail, Present        bool
	Detail, Form, Kind, Value string
}

// LSPCompletionEntry identifies the original response index independently of
// labels. Insertion is the initially validated payload, never a resolved edit.
// Documentation resolution cannot replace that insertion or snippet plan.
type LSPCompletionEntry struct {
	ID            string
	Index         int
	Insertion     CompletionItem
	Documentation LSPCompletionDocumentation
	// Prefix comes from the canonical primary range up to the request cursor.
	// Filtering/ranking is display-only and cannot alter retained insertion.
	Prefix, FilterText, SortText string
}

// LSPCompletionSet is one bounded, ephemeral, owner/view-sequenced invocation
// over acknowledged canonical source. ID/digest select the retained original
// objects, not caller-authored items. Incomplete and unsupported counts remain
// visible instead of presenting a truncated list as complete.
type LSPCompletionSet struct {
	ID, Digest, ViewID string
	Sequence           uint64
	Boundary           InsightBoundary
	WireVersion        int
	FeatureGeneration  uint64
	Line, Col          int
	Answer             string
	Incomplete         bool
	UnsupportedItems   int
	ResolveSupported   bool
	Entries            []LSPCompletionEntry
	ExpiresAt          time.Time
	Detail             string
}

// LSPCompletionInspection contains documentation for one exact original row.
// Neither original opaque data nor any newly resolved edit is sent to the shell.
type LSPCompletionInspection struct {
	SetID, Digest, ItemID string
	Boundary              InsightBoundary
	WireVersion           int
	FeatureGeneration     uint64
	Documentation         LSPCompletionDocumentation
	ResolveState, Detail  string
}

// CloneCompletionItem detaches the initial edit/snippet payload. Documentation
// resolution never writes through these pointers or reconstructs them.
func CloneCompletionItem(in CompletionItem) CompletionItem {
	if in.TextEdit != nil {
		edit := *in.TextEdit
		in.TextEdit = &edit
	}
	in.AdditionalTextEdits = slices.Clone(in.AdditionalTextEdits)
	if in.Snippet != nil {
		plan := *in.Snippet
		plan.Occurrences = slices.Clone(plan.Occurrences)
		for i := range plan.Occurrences {
			plan.Occurrences[i].Choices = slices.Clone(plan.Occurrences[i].Choices)
		}
		in.Snippet = &plan
	}
	return in
}
