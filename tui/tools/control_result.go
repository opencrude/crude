package tools

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"

	"crude/types/control"
	"crude/types/inspect"
)

// buildControlResult projects Crude self-control discovery and receipts. The
// exact schemas and owner output remain in expanded details, never the title.
func buildControlResult(name, content, input string) StructuredResult {
	if name == "crude_commands" {
		var descriptors []control.Descriptor
		if json.Unmarshal([]byte(content), &descriptors) != nil {
			return BuildReadableResult(content)
		}
		result := StructuredResult{Summary: resultCount(len(descriptors), "command", "commands"), Unit: "commands"}
		if len(descriptors) == 0 {
			result.Sections = []ResultSection{{Body: "No Crude commands matched this query."}}
			return result
		}
		for _, descriptor := range descriptors {
			result.Sections = append(result.Sections, controlDescriptorSection(descriptor))
		}
		return result
	}
	var receipt control.Result
	if err := json.Unmarshal([]byte(content), &receipt); err != nil {
		return StructuredResult{Summary: "Control result unavailable", Unit: "operations", Sections: []ResultSection{{
			Body: "The control result was not valid JSON.", Details: []ResultField{{"Retained result", content}},
		}}}
	}
	if receipt.RequestID == "" {
		return StructuredResult{Summary: "Control result incomplete", Unit: "operations", Sections: []ResultSection{{
			Body: "The control result did not include its request identity.", Details: []ResultField{{"Retained result", content}},
		}}}
	}
	command := ExtractArg(input, "command")
	result := StructuredResult{Summary: controlStateLabel(receipt.State), Unit: "operations"}
	section := ResultSection{Title: command, Metadata: controlStateLabel(receipt.State)}
	if receipt.Fault != nil {
		section.Body = receipt.Fault.Message
		if receipt.Fault.Field != "" {
			section.Body = receipt.Fault.Field + ": " + section.Body
		}
		result.Summary += " · " + string(receipt.Fault.Code)
	}
	section.Details = append(section.Details, ResultField{"Request", receipt.RequestID})
	if receipt.OperationID != "" && receipt.OperationID != receipt.RequestID {
		section.Details = append(section.Details, ResultField{"Operation", receipt.OperationID})
	}
	if receipt.Revision != nil {
		section.Details = append(section.Details, ResultField{"Revision", fmt.Sprint(*receipt.Revision)})
	}
	if receipt.FrameRevision != nil {
		section.Details = append(section.Details, ResultField{"Frame revision", fmt.Sprint(*receipt.FrameRevision)})
	}
	for _, id := range receipt.ResourceIDs {
		section.Details = append(section.Details, ResultField{"Resource", id})
	}
	result.Sections = append(result.Sections, section)
	if len(receipt.Output) > 0 && string(receipt.Output) != "null" {
		if appendDiagnosticResult(&result, command, receipt.Output) {
			return result
		}
		output := BuildReadableResult(string(receipt.Output))
		for _, part := range output.Sections {
			part.Title = joinResultPath("Output", part.Title)
			result.Sections = append(result.Sections, part)
		}
	}
	return result
}

// Diagnostic receipts keep their bounded data intact, but common readings are
// readable before a user expands source/continuation metadata and raw samples.
// This is presentation only: it never samples, aggregates or diagnoses again.
func appendDiagnosticResult(result *StructuredResult, command string, raw json.RawMessage) bool {
	if !strings.HasPrefix(command, "performance.") && !strings.HasPrefix(command, "diagnostics.") {
		return false
	}
	var page inspect.Page
	if json.Unmarshal(raw, &page) != nil || page.SnapshotID == "" {
		return false
	}
	result.Summary += fmt.Sprintf(" · %d/%d records", page.Returned, page.Total)
	if !page.Complete {
		result.Summary += " · more available"
	}
	for _, record := range page.Records {
		var reading struct{ Title, Detail, Label, Value, Verdict, Role string }
		_ = json.Unmarshal(record.Value, &reading)
		section := ResultSection{Title: record.ID, Metadata: record.Kind}
		switch record.Kind {
		case "verdict":
			section.Title = reading.Role + " performance"
			section.Body = reading.Verdict
		case "finding":
			section.Title, section.Body = reading.Title, reading.Detail
		case "headline":
			section.Title, section.Body = reading.Label, reading.Value
		default:
			output := BuildReadableResult(string(record.Value))
			for _, part := range output.Sections {
				part.Title = joinResultPath(record.ID, part.Title)
				part.Metadata = record.Kind
				part.DetailOnly = true
				result.Sections = append(result.Sections, part)
			}
			continue
		}
		result.Sections = append(result.Sections, section)
	}
	metadata := page
	metadata.Records = nil
	encoded, _ := json.Marshal(metadata)
	for _, section := range BuildReadableResult(string(encoded)).Sections {
		section.Title = joinResultPath("Observation", section.Title)
		section.DetailOnly = true
		result.Sections = append(result.Sections, section)
	}
	return true
}

func controlDescriptorSection(descriptor control.Descriptor) ResultSection {
	var schema struct {
		Description string         `json:"description"`
		Properties  map[string]any `json:"properties"`
		Required    []string       `json:"required"`
	}
	_ = json.Unmarshal(descriptor.Input.Document, &schema)
	metadata := []string{string(descriptor.Scope)}
	if descriptor.Effect != "" {
		metadata = append(metadata, string(descriptor.Effect))
	}
	if descriptor.HumanOnly {
		metadata = append(metadata, "human only")
	}
	if !descriptor.Availability.Available {
		metadata = append(metadata, "unavailable")
	}
	section := ResultSection{Title: descriptor.ID, Metadata: strings.Join(metadata, " · "), Body: schema.Description}
	if descriptor.Availability.Message != "" {
		section.Details = append(section.Details, ResultField{"Availability", descriptor.Availability.Message})
	}
	if inputs := controlSchemaFields(descriptor.Input.Document, schema.Required); len(inputs) > 0 {
		section.Details = append(section.Details, inputs...)
	} else if len(schema.Required) > 0 {
		section.Details = append(section.Details, ResultField{"Required", strings.Join(schema.Required, ", ")})
	}
	return section
}

// controlSchemaFields describes each input property in words (type, bounds,
// choices, description). The exact schema stays with the tool contract; a
// transcript row never paints it as raw JSON.
func controlSchemaFields(document json.RawMessage, required []string) []ResultField {
	var schema struct {
		Properties map[string]controlSchemaProperty `json:"properties"`
	}
	if json.Unmarshal(document, &schema) != nil || len(schema.Properties) == 0 {
		return nil
	}
	names := make([]string, 0, len(schema.Properties))
	for name := range schema.Properties {
		names = append(names, name)
	}
	sort.Strings(names)
	fields := make([]ResultField, 0, len(names))
	for _, name := range names {
		property := schema.Properties[name]
		parts := []string{property.typeLabel()}
		if slices.Contains(required, name) {
			parts = append(parts, "required")
		}
		if property.Minimum != nil {
			parts = append(parts, fmt.Sprintf("min %v", *property.Minimum))
		}
		if property.Maximum != nil {
			parts = append(parts, fmt.Sprintf("max %v", *property.Maximum))
		}
		if len(property.Enum) > 0 {
			choices := make([]string, len(property.Enum))
			for i, choice := range property.Enum {
				choices[i] = fmt.Sprint(choice)
			}
			parts = append(parts, "one of "+strings.Join(choices, ", "))
		}
		value := strings.Join(parts, " · ")
		if property.Description != "" {
			value += ": " + property.Description
		}
		fields = append(fields, ResultField{Label: "Input · " + name, Value: value})
	}
	return fields
}

type controlSchemaProperty struct {
	Type        any      `json:"type"`
	Description string   `json:"description"`
	Enum        []any    `json:"enum"`
	Minimum     *float64 `json:"minimum"`
	Maximum     *float64 `json:"maximum"`
	Items       *struct {
		Type any `json:"type"`
	} `json:"items"`
}

func (p controlSchemaProperty) typeLabel() string {
	label := schemaTypeLabel(p.Type)
	if label == "array" && p.Items != nil {
		if item := schemaTypeLabel(p.Items.Type); item != "value" {
			return "list of " + item
		}
	}
	return label
}

func schemaTypeLabel(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case []any:
		names := make([]string, 0, len(typed))
		for _, name := range typed {
			if text, ok := name.(string); ok && text != "null" {
				names = append(names, text)
			}
		}
		if len(names) > 0 {
			return strings.Join(names, " or ")
		}
	}
	return "value"
}

func controlStateLabel(state control.ResultState) string {
	switch state {
	case control.Applied:
		return "Applied"
	case control.Committed:
		return "Committed"
	case control.Admitted:
		return "Admitted"
	case control.PendingDecision:
		return "Pending decision"
	case control.Partial:
		return "Partial"
	case control.Failed:
		return "Failed"
	case control.Uncertain:
		return "Outcome uncertain"
	default:
		return "Result"
	}
}
