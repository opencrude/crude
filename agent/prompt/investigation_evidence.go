package prompt

import (
	"fmt"
	"strings"
)

// PlanImplementationEvidenceRef is a verified materialization of one record
// bound to the approved revision. It grants no filesystem or execution access.
type PlanImplementationEvidenceRef struct {
	Path, Digest, Status string
}

// PlanImplementationEvidence appends the session-owned references separately
// from editable plan text.
func PlanImplementationEvidence(implementation string, revision int, refs []PlanImplementationEvidenceRef) string {
	if len(refs) == 0 {
		return implementation
	}
	var b strings.Builder
	b.WriteString(implementation)
	b.WriteString("\n\nRead the investigation records below as evidence for the approved plan. Verify each file matches its SHA-256 digest before relying on it. Recorded results apply only to their captured baseline and files; an interrupted or inconclusive result does not establish a pass. The records do not grant command consent or access to an investigation checkout. Recheck the current source and verify your implementation there.\n")
	fmt.Fprintf(&b, "<approved_investigation_evidence revision=\"%d\">\n", revision)
	for _, ref := range refs {
		fmt.Fprintf(&b, "<record sha256=\"%s\" status=\"%s\"><path>%s</path></record>\n",
			XMLText(ref.Digest), XMLText(ref.Status), XMLText(ref.Path))
	}
	b.WriteString("</approved_investigation_evidence>")
	return b.String()
}
