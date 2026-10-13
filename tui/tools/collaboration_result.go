package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"crude/collaboration"
)

func buildCollaborationResult(name, content, input string) StructuredResult {
	switch name {
	case "agent_start":
		var started struct {
			AgentID    string `json:"agent_id"`
			Name       string `json:"name"`
			Category   string `json:"category"`
			Status     string `json:"status"`
			SessionID  string `json:"session_id"`
			RunID      string `json:"run_id"`
			RunVersion uint64 `json:"run_version"`
		}
		if json.Unmarshal([]byte(content), &started) != nil || started.AgentID == "" {
			return BuildReadableResult(content)
		}
		return StructuredResult{
			Summary: "Started " + started.Name, Unit: "agents",
			Sections: []ResultSection{{
				Title: started.Name, Metadata: started.Category + " · " + started.Status,
				Details: []ResultField{
					{"Agent", started.AgentID}, {"Run", started.RunID},
					{"Run version", fmt.Sprint(started.RunVersion)}, {"Session", started.SessionID},
				},
			}},
		}
	case "agent_send", "agent_respond", "agent_control":
		var result collaboration.PublishResult
		if json.Unmarshal([]byte(content), &result) != nil || result.Event.ID == "" {
			return BuildReadableResult(content)
		}
		summary := eventKindLabel(result.Event.Kind) + " · " +
			resultCount(len(result.Deliveries), "recipient", "recipients")
		if result.Duplicate {
			summary += " · unchanged retry"
		}
		section := collaborationEventSection(result.Event)
		pending := 0
		for _, delivery := range result.Deliveries {
			if delivery.State == collaboration.DeliveryPending {
				pending++
			}
			section.Details = append(section.Details, ResultField{
				Label: "Delivery · " + delivery.RecipientID,
				Value: fmt.Sprintf("%s · version %d", delivery.State, delivery.Version),
			})
		}
		section.Metadata = fmt.Sprintf("Durable workspace event · %d pending", pending)
		return StructuredResult{Summary: summary, Unit: "events", Sections: []ResultSection{section}}
	case "board_read":
		var page collaboration.Page
		if json.Unmarshal([]byte(content), &page) != nil {
			return BuildReadableResult(content)
		}
		summary := resultCount(len(page.Events), "event", "events")
		if page.HasMore {
			summary += " · more in snapshot"
		}
		result := StructuredResult{Summary: summary, Unit: "events"}
		for _, event := range page.Events {
			result.Sections = append(result.Sections, collaborationEventSection(event))
		}
		if len(page.Events) == 0 {
			result.Sections = append(result.Sections, ResultSection{Body: "No workspace communication matched this page."})
		}
		result.Sections = append(result.Sections, ResultSection{
			Title: "Workspace page", DetailOnly: true,
			Details: []ResultField{
				{"Cursor", fmt.Sprint(page.Cursor)},
				{"Snapshot", fmt.Sprint(page.Snapshot)},
				{"Latest sequence", fmt.Sprint(page.LatestSequence)},
				{"Human read through", fmt.Sprint(page.HumanReadThrough)},
				{"More available", fmt.Sprint(page.HasMore)},
				{"Bytes", fmt.Sprint(page.Bytes)},
			},
		})
		return result
	case "publish_report":
		var published collaboration.PublishReportResult
		if json.Unmarshal([]byte(content), &published) != nil || published.Artifact.ID == "" {
			return BuildReadableResult(content)
		}
		section := reportSection(published.Artifact)
		section.Metadata += fmt.Sprintf(" · %d recipients", len(published.Publish.Deliveries))
		return StructuredResult{
			Summary: fmt.Sprintf("Report revision %d published", published.Artifact.Revision),
			Unit:    "reports", Sections: []ResultSection{section},
		}
	case "artifact_read":
		var artifact collaboration.Artifact
		if json.Unmarshal([]byte(content), &artifact) != nil || artifact.ID == "" {
			return BuildReadableResult(content)
		}
		return StructuredResult{
			Summary: fmt.Sprintf("Report revision %d", artifact.Revision),
			Unit:    "reports", Sections: []ResultSection{reportSection(artifact)},
		}
	case "agent_list":
		var agents []collaboration.Agent
		if json.Unmarshal([]byte(content), &agents) != nil {
			return BuildReadableResult(content)
		}
		result := StructuredResult{Summary: resultCount(len(agents), "agent", "agents"), Unit: "agents"}
		for _, agent := range agents {
			result.Sections = append(result.Sections, agentSection(agent))
		}
		if len(agents) == 0 {
			result.Sections = append(result.Sections, ResultSection{Body: "No workspace agents matched."})
		}
		return result
	case "agent_inspect":
		var inspected struct {
			Agent   collaboration.Agent      `json:"agent"`
			Run     *collaboration.Run       `json:"run"`
			Reports []collaboration.Artifact `json:"reports"`
			Claims  []collaboration.Claim    `json:"claims"`
		}
		if json.Unmarshal([]byte(content), &inspected) != nil || inspected.Agent.ID == "" {
			return BuildReadableResult(content)
		}
		result := StructuredResult{Summary: inspected.Agent.Name + " · " + string(inspected.Agent.State), Unit: "agents"}
		section := agentSection(inspected.Agent)
		if inspected.Run != nil {
			section.Details = append(section.Details,
				ResultField{"Run", inspected.Run.ID},
				ResultField{"Run state", string(inspected.Run.State)},
				ResultField{"Run version", fmt.Sprint(inspected.Run.Version)},
			)
		}
		for _, claim := range inspected.Claims {
			section.Details = append(section.Details, ResultField{"Owned path", claim.Path})
		}
		result.Sections = append(result.Sections, section)
		for _, report := range inspected.Reports {
			report := reportSection(report)
			report.DetailOnly = true
			result.Sections = append(result.Sections, report)
		}
		return result
	case "agent_update_claims", "agent_narrow_claims":
		var claims struct {
			Released []collaboration.Claim `json:"released"`
			Claims   []collaboration.Claim `json:"claims"`
			Audit    collaboration.Event   `json:"audit"`
		}
		if json.Unmarshal([]byte(content), &claims) != nil || claims.Released == nil && claims.Claims == nil {
			return BuildReadableResult(content)
		}
		result := StructuredResult{
			Summary: resultCount(len(claims.Claims), "owned path", "owned paths") + " · " + fmt.Sprint(len(claims.Released)) + " released",
			Unit:    "claims",
		}
		section := ResultSection{Title: "File scope"}
		var paths []string
		slices.SortFunc(claims.Claims, func(a, b collaboration.Claim) int { return strings.Compare(a.Path, b.Path) })
		slices.SortFunc(claims.Released, func(a, b collaboration.Claim) int { return strings.Compare(a.Path, b.Path) })
		for _, claim := range claims.Claims {
			paths = append(paths, claim.Path)
			section.Details = append(section.Details, ResultField{"Claim · " + claim.Path, fmt.Sprintf("%s · version %d", claim.ID, claim.Version)})
		}
		for _, claim := range claims.Released {
			section.Details = append(section.Details, ResultField{"Released · " + claim.Path, claim.ID})
		}
		section.Body = strings.Join(paths, "\n")
		if section.Body == "" {
			section.Body = "No file scope retained."
		}
		result.Sections = append(result.Sections, section)
		if claims.Audit.ID != "" {
			audit := collaborationEventSection(claims.Audit)
			audit.DetailOnly = true
			result.Sections = append(result.Sections, audit)
		}
		return result
	case "agent_wait":
		if result := buildWaitResult(content); result.Summary != "" {
			return result
		}
		return BuildReadableResult(content)
	default:
		return BuildReadableResult(content)
	}
}

func collaborationEventSection(event collaboration.Event) ResultSection {
	sender := event.SenderName
	if sender == "" {
		sender = event.SenderID
	}
	metadata := []string{"Attributed " + string(event.Origin)}
	if event.Broadcast {
		metadata = append(metadata, "running-agent broadcast")
	}
	if event.ReplyTo != "" {
		metadata = append(metadata, "reply")
	}
	if event.Archived {
		metadata = append(metadata, "archived")
	}
	section := ResultSection{
		Title: fmt.Sprintf("#%d %s · %s → %s",
			event.Sequence, eventKindLabel(event.Kind), sender, collaborationRecipients(event.Recipients)),
		Metadata: strings.Join(metadata, " · "), Body: event.Body,
		Details: []ResultField{
			{"Event", event.ID}, {"Thread", event.ThreadID},
			{"Sender", event.SenderID}, {"Reply to", event.ReplyTo},
			{"Request", event.RequestID}, {"Request revision", fmt.Sprint(event.RequestRevision)},
			{"Run", event.RunID}, {"Supersedes", event.Supersedes},
		},
	}
	if !event.CreatedAt.IsZero() {
		section.Details = append(section.Details, ResultField{"Published", event.CreatedAt.Format(time.RFC3339Nano)})
	}
	for _, reference := range event.References {
		value := reference.ID
		if reference.Revision > 0 {
			value += fmt.Sprintf(" · revision %d", reference.Revision)
		}
		section.Details = append(section.Details, ResultField{"Reference · " + reference.Kind, value})
	}
	return section
}

func reportSection(artifact collaboration.Artifact) ResultSection {
	section := ResultSection{
		Title:    artifact.Summary,
		Metadata: fmt.Sprintf("%s report · revision %d · %s", artifact.Kind, artifact.Revision, artifact.AuthorID),
		Body:     artifact.Body,
		Details: []ResultField{
			{"Report", artifact.ID}, {"Revision", fmt.Sprint(artifact.Revision)},
			{"Digest", artifact.Digest}, {"Run", artifact.RunID},
			{"Request", artifact.RequestID}, {"Supersedes revision", fmt.Sprint(artifact.SupersedesRevision)},
		},
	}
	for _, reference := range artifact.References {
		section.Details = append(section.Details, ResultField{"Evidence · " + reference.Kind, reference.ID})
	}
	return section
}

func agentSection(agent collaboration.Agent) ResultSection {
	metadata := string(agent.State) + " · " + string(agent.Presence)
	return ResultSection{
		Title: agent.Name, Metadata: metadata,
		Details: []ResultField{
			{"Agent", agent.ID}, {"Session", agent.SessionID}, {"Parent", agent.ParentID},
			{"Category", agent.Category}, {"Runtime epoch", fmt.Sprint(agent.RuntimeEpoch)},
			{"Current run", agent.CurrentRunID}, {"Current request", agent.CurrentRequestID},
		},
	}
}

func buildWaitResult(content string) StructuredResult {
	var run collaboration.Run
	if json.Unmarshal([]byte(content), &run) == nil && run.ID != "" && run.AgentID != "" {
		return StructuredResult{
			Summary: "Run " + string(run.State), Unit: "runs",
			Sections: []ResultSection{{
				Title: run.ID, Metadata: run.AgentID + " · version " + fmt.Sprint(run.Version),
				Body: run.Outcome, Details: []ResultField{{"Context commit", run.ContextCommitID}},
			}},
		}
	}
	var artifact collaboration.Artifact
	if json.Unmarshal([]byte(content), &artifact) == nil && artifact.ID != "" && artifact.Revision > 0 {
		return StructuredResult{Summary: "Report available", Unit: "reports", Sections: []ResultSection{reportSection(artifact)}}
	}
	var agent collaboration.Agent
	if json.Unmarshal([]byte(content), &agent) == nil && agent.ID != "" {
		return StructuredResult{Summary: agent.Name + " · " + string(agent.State), Unit: "agents", Sections: []ResultSection{agentSection(agent)}}
	}
	return StructuredResult{}
}

type collaborationWaitOutcome struct {
	operand string
	summary string
	known   bool
	success bool
	failed  bool
}

func parseCollaborationWaitOutcome(content string) collaborationWaitOutcome {
	var run collaboration.Run
	if json.Unmarshal([]byte(content), &run) == nil && run.ID != "" && run.AgentID != "" {
		terminal := run.State.Terminal()
		return collaborationWaitOutcome{
			operand: run.ID,
			summary: "Run " + string(run.State),
			known:   terminal,
			success: run.State == collaboration.RunSucceeded,
			failed:  run.State == collaboration.RunFailed || run.State == collaboration.RunInterrupted,
		}
	}
	var artifact collaboration.Artifact
	if json.Unmarshal([]byte(content), &artifact) == nil && artifact.ID != "" && artifact.Revision > 0 {
		operand := artifact.ID
		if artifact.Summary != "" {
			operand = fmt.Sprintf("%s · revision %d", artifact.Summary, artifact.Revision)
		}
		return collaborationWaitOutcome{operand: operand, summary: "Report available", known: true, success: true}
	}
	var agent collaboration.Agent
	if json.Unmarshal([]byte(content), &agent) == nil && agent.ID != "" {
		success := agent.State == collaboration.AgentAvailable &&
			agent.Presence == collaboration.RuntimeAttached &&
			agent.CurrentRunID == ""
		return collaborationWaitOutcome{
			operand: subAgentIdentityLabel(agent.Name, agent.ID, agent.Category),
			summary: agent.Name + " · " + string(agent.State),
			known:   true,
			success: success,
			failed:  agent.State == collaboration.AgentClosed || agent.Presence == collaboration.RuntimeUnresponsive,
		}
	}
	return collaborationWaitOutcome{}
}

// CollaborationResultSucceeded classifies typed collaboration results without
// inferring state from narrative text. Unknown shapes fail closed.
func CollaborationResultSucceeded(name, content string) (known, success bool) {
	if name != "agent_wait" {
		return false, false
	}
	outcome := parseCollaborationWaitOutcome(content)
	return outcome.known, outcome.success
}

func collaborationRecipients(recipients []collaboration.Recipient) string {
	names := make([]string, 0, len(recipients))
	for _, recipient := range recipients {
		name := recipient.Name
		if name == "" {
			name = recipient.ID
		}
		names = append(names, name)
	}
	return strings.Join(names, ", ")
}

func eventKindLabel(kind collaboration.EventKind) string {
	switch kind {
	case collaboration.EventWorkRequest:
		return "Work request"
	case collaboration.EventWorkRevision:
		return "Work revision"
	case collaboration.EventRequestDisposition:
		return "Request response"
	case collaboration.EventReportPublished:
		return "Report"
	case collaboration.EventRunOutcome:
		return "Run outcome"
	case collaboration.EventLifecycleControl:
		return "Control"
	case collaboration.EventControlOutcome:
		return "Control outcome"
	case collaboration.EventCommandOutcome:
		return "Command outcome"
	case collaboration.EventPeerReply:
		return "Reply"
	default:
		return "Message"
	}
}

func CollaborationOperand(name, content, input string) string {
	switch name {
	case "agent_send", "agent_respond", "agent_control":
		var result collaboration.PublishResult
		if json.Unmarshal([]byte(content), &result) != nil || result.Event.ID == "" {
			return ""
		}
		return eventKindLabel(result.Event.Kind) + " → " + collaborationRecipients(result.Event.Recipients)
	case "publish_report":
		var result collaboration.PublishReportResult
		if json.Unmarshal([]byte(content), &result) != nil || result.Artifact.ID == "" {
			return ""
		}
		return fmt.Sprintf("%s · revision %d", result.Artifact.Summary, result.Artifact.Revision)
	case "artifact_read":
		if id := ExtractArg(input, "artifact_id"); id != "" {
			return id
		}
	case "agent_wait":
		return parseCollaborationWaitOutcome(content).operand
	}
	return ""
}
