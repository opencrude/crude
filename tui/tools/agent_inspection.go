package tools

import (
	"encoding/json"
	"fmt"
	"time"

	"crude/collaboration"
)

func buildAgentInspectionResult(content string) StructuredResult {
	var inspected struct {
		collaboration.AgentInspection
		Claims     []collaboration.Claim `json:"claims"`
		Trigger    json.RawMessage       `json:"trigger"`
		RecentWork json.RawMessage       `json:"recent_work"`
		Recovery   json.RawMessage       `json:"recovery"`
	}
	if json.Unmarshal([]byte(content), &inspected) != nil || inspected.Agent.ID == "" {
		return BuildReadableResult(content)
	}
	activity := "no current run"
	if inspected.Run != nil {
		activity = string(inspected.Run.State)
	}
	result := StructuredResult{Summary: inspected.Agent.Name + " · " + activity, Unit: "sections"}
	section := agentSection(inspected.Agent)
	section.Body = "Current activity: " + activity
	for _, claim := range inspected.Claims {
		section.Details = append(section.Details, ResultField{"Owned path", claim.Path})
	}
	result.Sections = append(result.Sections, section)
	for _, attempt := range []struct {
		title string
		run   *collaboration.Run
	}{{"Current run", inspected.Run}, {"Latest run", inspected.LatestRun}} {
		section := ResultSection{Title: attempt.title, Body: "None recorded."}
		if run := attempt.run; run != nil {
			section.Metadata, section.Body = string(run.State), run.Outcome
			section.Details = []ResultField{{"Run", run.ID}, {"Origin", string(run.Origin)}, {"Version", fmt.Sprint(run.Version)}, {"Started", run.StartedAt.Format(time.RFC3339Nano)}}
			if !run.EndedAt.IsZero() {
				section.Details = append(section.Details, ResultField{"Ended", run.EndedAt.Format(time.RFC3339Nano)})
			}
		}
		result.Sections = append(result.Sections, section)
	}
	for _, evidence := range []struct {
		title string
		raw   json.RawMessage
	}{{"Trigger", inspected.Trigger}, {"Recent work", inspected.RecentWork}, {"Recovery", inspected.Recovery}} {
		if len(evidence.raw) == 0 || string(evidence.raw) == "null" {
			continue
		}
		for _, section := range BuildReadableResult(string(evidence.raw)).Sections {
			if section.Title == "" {
				section.Title = evidence.title
			} else {
				section.Title = joinResultPath(evidence.title, section.Title)
			}
			result.Sections = append(result.Sections, section)
		}
	}
	for _, report := range inspected.Reports {
		result.Sections = append(result.Sections, ResultSection{Title: report.Summary,
			Metadata: fmt.Sprintf("%s report · revision %d", report.Kind, report.Revision), DetailOnly: true,
			Details: []ResultField{{"Report", report.ID}, {"Revision", fmt.Sprint(report.Revision)}, {"Author", report.AuthorID}, {"Run", report.RunID}},
		})
	}
	return result
}
