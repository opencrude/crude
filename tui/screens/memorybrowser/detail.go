package memorybrowser

import (
	"fmt"
	"strings"
	"time"

	"crude/memory"
)

func detailText(detail DetailData) string {
	var text strings.Builder
	if detail.Record.ID == "" {
		episode := detail.Episode
		fmt.Fprintf(&text, "Episode · %s\nID: %s\nSession: %s · turn %d\nObserved: %s\n\nSituation\n%s\n\nAttempted method\n%s\n\nOutcome\n%s\n\nConditions\n%s\n\nEvidence\n%s\n\nEpisodes are scoped observations, not verified general knowledge.\n", episode.Method, episode.ID, episode.SessionID, episode.Turn, memoryTime(episode.CreatedAt), episode.Situation, episode.Method, episode.Outcome, missing(episode.Conditions), evidenceText(episode.Evidence))
		return text.String()
	}
	writeRecord(&text, detail.Record)
	text.WriteString("\nImmutable revision history\n")
	if len(detail.Revisions) == 0 {
		text.WriteString("Not loaded. Reload detail to inspect the durable history.\n")
	}
	for _, revision := range detail.Revisions {
		fmt.Fprintf(&text, "\nRevision %d · %s · actor %s · session %s\n", revision.Revision, revision.Op, missing(revision.Actor), missing(revision.SessionID))
		writeRecord(&text, revision.Record)
	}
	text.WriteString("\nRevision-linked feedback (latest 100)\n")
	if len(detail.Feedback) == 0 {
		text.WriteString("No feedback recorded. Usefulness is unknown.\n")
	}
	for _, feedback := range detail.Feedback {
		fmt.Fprintf(&text, "\nr%d · %s · actor %s · session %s · %s\n%s\n", feedback.Revision, feedback.Kind, missing(feedback.Actor), missing(feedback.SessionID), memoryTime(feedback.At), evidenceText(feedback.Evidence))
	}
	text.WriteString("\nRetrieval exposure (latest 100; not usefulness)\n")
	if len(detail.Exposures) == 0 {
		text.WriteString("No recorded retrieval exposures. Browsing does not record exposure.\n")
	}
	for _, exposure := range detail.Exposures {
		fmt.Fprintf(&text, "r%d · %s · session %s · %s\n", exposure.Revision, exposure.Kind, missing(exposure.SessionID), memoryTime(exposure.At))
	}
	return text.String()
}

func writeRecord(text *strings.Builder, record memory.Record) {
	fmt.Fprintf(text, "Knowledge · %s\nID: %s\nRevision: %d · %s · %s\nImportance: %d · pinned: %t\nCreated: %s\nUpdated: %s\nSource: %s · actor: %s\nSource session: %s\nProvenance: %s\n\nContent\n%s\n\nApplicability\n%s\nScope paths: %s\n\nEvidence\n%s\n", record.Title, record.ID, record.Revision, record.Kind, record.Status, record.Importance, record.Pinned, memoryTime(record.CreatedAt), memoryTime(record.UpdatedAt), missing(record.Source), missing(record.Actor), missing(record.SourceSessionID), missing(record.Provenance), record.Content, missing(record.AppliesWhen), missing(strings.Join(record.ScopePaths, ", ")), evidenceText(record.Evidence))
	if record.SupersededBy != "" {
		fmt.Fprintf(text, "Replacement: %s\n", record.SupersededBy)
	}
}

func evidenceText(evidence []memory.Evidence) string {
	if len(evidence) == 0 {
		return "No evidence recorded; do not infer verification."
	}
	// One heading line per item; a link line only when one was recorded, so
	// absent identities do not cost a row each.
	var text strings.Builder
	for index, item := range evidence {
		fmt.Fprintf(&text, "%d. %s · %s", index+1, strings.ReplaceAll(string(item.Kind), "_", " "), item.Outcome)
		if item.SessionID != "" {
			fmt.Fprintf(&text, " · session %s", item.SessionID)
		}
		text.WriteByte('\n')
		var links []string
		if item.MessageID != "" {
			links = append(links, "message "+item.MessageID)
		}
		if item.ToolCallID != "" {
			links = append(links, "tool call "+item.ToolCallID)
		}
		if len(links) > 0 {
			fmt.Fprintf(&text, "   %s\n", strings.Join(links, " · "))
		}
		if summary := strings.TrimSpace(item.Summary); summary != "" {
			fmt.Fprintf(&text, "   %s\n", summary)
		}
	}
	return strings.TrimSuffix(text.String(), "\n")
}

// evidenceRows sizes the read-only evidence box to its text, within limits;
// it scrolls past the limit.
func evidenceRows(text string) int {
	return min(8, max(2, strings.Count(text, "\n")+1))
}

func missing(value string) string {
	if strings.TrimSpace(value) == "" {
		return "Not recorded"
	}
	return value
}

// browseHint names the keys that act on the selected row.
func browseHint(mode string) string {
	if mode == ModeEpisodes {
		return "Enter open · Delete remove · / search"
	}
	return "Enter edit · Delete remove · / search"
}

func memoryDay(timestamp int64) string {
	if timestamp <= 0 {
		return "unknown"
	}
	return time.Unix(0, timestamp).Format(time.DateOnly)
}

func memoryTime(timestamp int64) string {
	if timestamp <= 0 {
		return "Not recorded"
	}
	return time.Unix(0, timestamp).Format(time.RFC3339)
}
