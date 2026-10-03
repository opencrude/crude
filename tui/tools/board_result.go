package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"crude/board"
)

func buildBoardResult(name, content, input string) StructuredResult {
	if name == "board_post" {
		var result board.PostResult
		if json.Unmarshal([]byte(content), &result) != nil || result.Message.ID == "" {
			return BuildReadableResult(content)
		}
		summary := fmt.Sprintf("#%d · stored", result.Message.Sequence)
		if result.Duplicate {
			summary += " · already recorded"
		}
		section := boardMessageSection(result.Message)
		section.Metadata = "Stored on Board · not delivered or read"
		section.Details = append(section.Details, ResultField{Label: "Duplicate", Value: fmt.Sprint(result.Duplicate)})
		return StructuredResult{Summary: summary, Unit: "messages", Sections: []ResultSection{section}}
	}
	var page board.Page
	if json.Unmarshal([]byte(content), &page) != nil || page.BoardID == "" {
		return BuildReadableResult(content)
	}
	summary := resultCount(len(page.Messages), "message", "messages") + " · " + resultCount(len(page.Roster), "participant", "participants")
	if page.HasMore {
		summary += " · more available"
	}
	result := StructuredResult{Summary: summary, Unit: "messages"}
	for _, message := range page.Messages {
		result.Sections = append(result.Sections, boardMessageSection(message))
	}
	if len(page.Messages) == 0 {
		result.Sections = append(result.Sections, ResultSection{Body: "No Board messages in this page."})
	}
	pageSection := ResultSection{Title: "Board page", DetailOnly: true, Details: []ResultField{
		{"Board", page.BoardID}, {"Project", page.ProjectID},
		{"Cursor", fmt.Sprint(page.Cursor)}, {"Latest sequence", fmt.Sprint(page.LatestSequence)},
		{"Context accepted through", fmt.Sprint(page.AcceptedThrough)},
		{"Context admission offered through", fmt.Sprint(page.AcceptThrough)},
		{"Human read through", fmt.Sprint(page.HumanReadThrough)},
		{"More available", fmt.Sprint(page.HasMore)}, {"Bytes", fmt.Sprint(page.Bytes)},
	}}
	if thread := ExtractArg(input, "thread"); thread != "" {
		pageSection.Details = append(pageSection.Details, ResultField{"Requested thread", thread})
	}
	result.Sections = append(result.Sections, pageSection)
	for _, member := range page.Roster {
		result.Sections = append(result.Sections, ResultSection{
			Title: member.Name, Metadata: "Participant · " + member.Availability, DetailOnly: true,
			Details: []ResultField{{"Agent", member.ID}, {"Parent", member.ParentID}},
		})
	}
	return result
}

func boardMessageSection(message board.Message) ResultSection {
	sender := message.SenderName
	if sender == "" {
		sender = message.SenderID
	}
	metadata := []string{"Peer data"}
	if message.ReplyTo != "" {
		metadata = append(metadata, "reply")
	}
	if len(message.References) > 0 {
		metadata = append(metadata, fmt.Sprintf("%d references", len(message.References)))
	}
	if message.RecipientAvailability == board.AvailabilityFinished {
		metadata = append(metadata, "recipient finished")
	}
	if message.Archived {
		metadata = append(metadata, "archived")
	}
	section := ResultSection{
		Title:    fmt.Sprintf("#%d %s · %s → %s", message.Sequence, message.Type, sender, boardRecipient(message)),
		Metadata: strings.Join(metadata, " · "), Body: message.Body,
		Details: []ResultField{
			{"Message", message.ID}, {"Board", message.BoardID}, {"Project", message.ProjectID},
			{"Sender", message.SenderID}, {"Addressed to", message.To}, {"Recipient", message.RecipientID},
			{"Recipient availability", message.RecipientAvailability}, {"Thread", message.ThreadID},
			{"Reply to", message.ReplyTo}, {"Archived", fmt.Sprint(message.Archived)},
		},
	}
	if !message.CreatedAt.IsZero() {
		section.Details = append(section.Details, ResultField{"Posted", message.CreatedAt.Format(time.RFC3339Nano)})
	}
	for _, recipient := range message.Recipients {
		section.Details = append(section.Details, ResultField{"Recipient", fmt.Sprintf("%s (%s) · %s", recipient.Name, recipient.ID, recipient.Availability)})
	}
	for _, reference := range message.References {
		section.Details = append(section.Details, ResultField{"Reference · " + reference.Kind, reference.ID})
	}
	return section
}

func boardRecipient(message board.Message) string {
	if message.RecipientID == "ALL" {
		return "all participants"
	}
	for _, recipient := range message.Recipients {
		if recipient.ID == message.RecipientID && recipient.Name != "" {
			return recipient.Name
		}
	}
	if message.To != "" {
		return message.To
	}
	return message.RecipientID
}

// BoardPostOperand uses only the immutable result's resolved participant
// identity. A conflicting result cannot rename the requested recipient.
func BoardPostOperand(content, input string) string {
	var result board.PostResult
	if json.Unmarshal([]byte(content), &result) != nil || result.Message.ID == "" {
		return ""
	}
	if to := ExtractArg(input, "to"); to != "" && to != result.Message.To {
		return ""
	}
	return result.Message.Type + " → " + boardRecipient(result.Message)
}
