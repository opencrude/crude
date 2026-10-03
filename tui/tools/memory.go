package tools

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// buildMemoryResult projects the exact tool result and authored input. Mutation
// acknowledgements do not echo the assertion, so the input supplies that part
// of the audit trail. Terminal geometry belongs to the caller, not this parser.
func buildMemoryResult(name, content, input string) StructuredResult {
	result, ok := memoryObject(content)
	if !ok {
		return BuildReadableResult(content)
	}
	args, _ := memoryObject(input)
	switch name {
	case "remember", "update_memory", "forget_memory":
		return memoryMutationResult(name, result, args, content)
	case "recall_memory":
		return memoryRecallResult(result, content)
	case "memory_feedback":
		return memoryFeedbackResult(result, args, content)
	case "search_history":
		return memoryHistoryResult(result, content)
	}
	return BuildReadableResult(content)
}

type memoryFields map[string]json.RawMessage

func memoryObject(raw string) (memoryFields, bool) {
	var fields memoryFields
	if err := json.Unmarshal([]byte(raw), &fields); err != nil || fields == nil {
		return nil, false
	}
	return fields, true
}

func (f memoryFields) text(key string) string {
	var value string
	_ = json.Unmarshal(f[key], &value)
	return value
}

func (f memoryFields) number(key string) string {
	var n json.Number
	if err := json.Unmarshal(f[key], &n); err == nil {
		return n.String()
	}
	return ""
}

func (f memoryFields) boolean(key string) string {
	var value bool
	if err := json.Unmarshal(f[key], &value); err == nil && len(f[key]) > 0 && string(f[key]) != "null" {
		return strconv.FormatBool(value)
	}
	return ""
}

func (f memoryFields) list(key string) string {
	var values []string
	if err := json.Unmarshal(f[key], &values); err == nil {
		return strings.Join(values, ", ")
	}
	// History stores tool/file names as space-joined strings, while session
	// summaries and authored paths arrive as arrays.
	return f.text(key)
}

func addMemoryDetail(section *ResultSection, label, value string) {
	if value != "" {
		section.Details = append(section.Details, ResultField{Label: label, Value: value})
	}
}

func addMemoryEvidence(section *ResultSection, raw json.RawMessage) {
	var items []memoryFields
	if err := json.Unmarshal(raw, &items); err != nil {
		return
	}
	for i, item := range items {
		var parts []string
		for _, field := range []struct{ label, key string }{
			{"Kind", "kind"}, {"Outcome", "outcome"}, {"Session", "session_id"},
			{"Message", "message_id"}, {"Tool call", "tool_call_id"}, {"Summary", "summary"},
		} {
			if value := item.text(field.key); value != "" {
				parts = append(parts, field.label+": "+value)
			}
		}
		if len(parts) != 0 {
			addMemoryDetail(section, fmt.Sprintf("Evidence %d", i+1), strings.Join(parts, "\n"))
		}
	}
}

func memoryMutationResult(name string, result, args memoryFields, content string) StructuredResult {
	status := map[string]string{"remember": "remembered", "update_memory": "updated", "forget_memory": "forgotten"}[name]
	if result.text("status") != status || result.text("id") == "" {
		return BuildReadableResult(content)
	}
	verb := map[string]string{"remember": "Remembered", "update_memory": "Updated memory", "forget_memory": "Forgot memory"}[name]
	section := ResultSection{Title: firstMemoryText(args, result, "title"), Body: args.text("content")}
	if section.Title == "" {
		section.Title = verb
	}
	addMemoryDetail(&section, "ID", result.text("id"))
	addMemoryDetail(&section, "Revision", result.number("revision"))
	addMemoryDetail(&section, "Previous revision", result.number("previous_revision"))
	addMemoryDetail(&section, "Expected revision", args.number("expected_revision"))
	addMemoryDetail(&section, "Status", result.text("status"))
	addMemoryDetail(&section, "Kind", firstMemoryText(args, result, "kind"))
	addMemoryDetail(&section, "Title", args.text("title"))
	addMemoryDetail(&section, "Importance", args.number("importance"))
	addMemoryDetail(&section, "Pinned", args.boolean("pinned"))
	addMemoryDetail(&section, "Applies when", args.text("applies_when"))
	addMemoryDetail(&section, "Paths", args.list("paths"))
	addMemoryEvidence(&section, args["evidence"])
	section.Metadata = firstMemoryText(args, result, "kind")
	if name == "forget_memory" {
		section.Body = "Retired from active recall · revision history retained"
	}
	summary := "revision " + result.number("revision")
	if previous := result.number("previous_revision"); previous != "" {
		summary = "revision " + previous + " → " + result.number("revision")
	}
	return StructuredResult{Summary: summary, Unit: "memories", Sections: []ResultSection{section}}
}

func firstMemoryText(a, b memoryFields, key string) string {
	if value := a.text(key); value != "" {
		return value
	}
	return b.text(key)
}

func memoryRecallResult(result memoryFields, content string) StructuredResult {
	var items []memoryFields
	if err := json.Unmarshal(result["results"], &items); err != nil || items == nil {
		return BuildReadableResult(content)
	}
	projection := StructuredResult{Summary: resultCount(len(items), "memory", "memories"), Unit: "memories"}
	if len(items) == 0 {
		projection.Sections = []ResultSection{{Body: "No matching workspace memories."}}
	}
	for _, item := range items {
		section := ResultSection{Title: item.text("title"), Body: item.text("content")}
		if section.Title == "" {
			section.Title = "Memory " + item.text("id")
		}
		section.Metadata = strings.Join(nonemptyMemoryValues(item.text("kind"), item.text("status"), item.text("last_updated")), " · ")
		addMemoryDetail(&section, "ID", item.text("id"))
		addMemoryDetail(&section, "Revision", item.number("revision"))
		addMemoryDetail(&section, "Importance", item.number("importance"))
		addMemoryDetail(&section, "Pinned", item.boolean("pinned"))
		addMemoryDetail(&section, "Source", item.text("source"))
		addMemoryDetail(&section, "From session", item.text("from_session"))
		addMemoryDetail(&section, "Applies when", item.text("applies_when"))
		addMemoryDetail(&section, "Evidence count", item.number("evidence_count"))
		addMemoryEvidence(&section, item["evidence"])
		projection.Sections = append(projection.Sections, section)
	}
	return projection
}

func nonemptyMemoryValues(values ...string) []string {
	var result []string
	for _, value := range values {
		if value != "" {
			result = append(result, value)
		}
	}
	return result
}

func memoryFeedbackResult(result, args memoryFields, content string) StructuredResult {
	if result.text("status") != "recorded" {
		return BuildReadableResult(content)
	}
	count, _ := strconv.Atoi(result.number("count"))
	projection := StructuredResult{Summary: resultCount(count, "item", "items"), Unit: "items"}
	var items []memoryFields
	if err := json.Unmarshal(args["items"], &items); err == nil {
		for _, item := range items {
			section := ResultSection{Title: "Memory " + item.text("id"), Metadata: item.text("kind")}
			var evidence []memoryFields
			if json.Unmarshal(item["evidence"], &evidence) == nil {
				for _, e := range evidence {
					if summary := e.text("summary"); summary != "" {
						section.Body += summary + "\n"
					}
				}
				section.Body = strings.TrimSuffix(section.Body, "\n")
			}
			addMemoryDetail(&section, "ID", item.text("id"))
			addMemoryDetail(&section, "Revision", item.number("revision"))
			addMemoryDetail(&section, "Feedback", item.text("kind"))
			addMemoryEvidence(&section, item["evidence"])
			projection.Sections = append(projection.Sections, section)
		}
	}
	if len(projection.Sections) == 0 {
		projection.Sections = []ResultSection{{Title: "Feedback recorded", Details: []ResultField{{Label: "Count", Value: result.number("count")}}}}
	}
	return projection
}

func memoryHistoryResult(result memoryFields, content string) StructuredResult {
	var turns, sessions []memoryFields
	if err := json.Unmarshal(result["turns"], &turns); err != nil || turns == nil {
		return BuildReadableResult(content)
	}
	if err := json.Unmarshal(result["sessions"], &sessions); err != nil || sessions == nil {
		return BuildReadableResult(content)
	}
	projection := StructuredResult{Summary: resultCount(len(sessions), "session", "sessions") + " · " + resultCount(len(turns), "turn", "turns"), Unit: "results"}
	if len(sessions) == 0 && len(turns) == 0 {
		projection.Sections = []ResultSection{{Body: "No matching workspace history."}}
	}
	for _, item := range sessions {
		section := ResultSection{Title: item.text("title"), Metadata: "Session · " + item.text("when"), Body: item.text("summary")}
		if section.Title == "" {
			section.Title = "Session " + item.text("session_id")
		}
		addMemoryDetail(&section, "Session ID", item.text("session_id"))
		addMemoryDetail(&section, "Outcome", item.text("outcome"))
		addMemoryDetail(&section, "Key files", item.list("key_files"))
		projection.Sections = append(projection.Sections, section)
	}
	for _, item := range turns {
		section := ResultSection{Title: "Turn " + item.number("turn"), Metadata: "Session " + item.text("session_id") + " · " + item.text("when"), Body: item.text("user")}
		if section.Body != "" {
			section.Body = "User: " + section.Body
		}
		if assistant := item.text("assistant"); assistant != "" {
			if section.Body != "" {
				section.Body += "\n"
			}
			section.Body += "Assistant: " + assistant
		}
		addMemoryDetail(&section, "Session ID", item.text("session_id"))
		addMemoryDetail(&section, "Turn", item.number("turn"))
		addMemoryDetail(&section, "User", item.text("user"))
		addMemoryDetail(&section, "Assistant", item.text("assistant"))
		addMemoryDetail(&section, "Tools", item.list("tools"))
		addMemoryDetail(&section, "Files", item.list("files"))
		projection.Sections = append(projection.Sections, section)
	}
	return projection
}
