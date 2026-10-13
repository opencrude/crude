package tools

import (
	"encoding/json"
	"fmt"
	"strings"

	"crude/types/control"
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
		output := BuildReadableResult(string(receipt.Output))
		for _, part := range output.Sections {
			part.Title = joinResultPath("Output", part.Title)
			result.Sections = append(result.Sections, part)
		}
	}
	return result
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
	if len(schema.Required) > 0 {
		section.Details = append(section.Details, ResultField{"Required", strings.Join(schema.Required, ", ")})
	}
	if descriptor.Availability.Message != "" {
		section.Details = append(section.Details, ResultField{"Availability", descriptor.Availability.Message})
	}
	section.Details = append(section.Details, ResultField{"Input schema", string(descriptor.Input.Document)})
	return section
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
