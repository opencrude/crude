package tools

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"strings"

	captypes "crude/types/capability"
)

// IsMCPTool reports a complete external MCP tool identity, not a streamed prefix.
func IsMCPTool(name string) bool { _, _, ok := mcpToolParts(name); return ok }

func mcpToolParts(name string) (string, string, bool) {
	rest, ok := strings.CutPrefix(name, "mcp__")
	server, tool, split := strings.Cut(rest, "__")
	return server, tool, ok && split && server != "" && tool != ""
}

// CapabilityToolLabel presents a dynamic MCP identity without pretending that
// its generated suffix is the server's original name.
func CapabilityToolLabel(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return name
	}
	server, tool, ok := mcpToolParts(name)
	if !ok {
		return "MCP operation"
	}
	return "MCP " + strings.ReplaceAll(tool, "_", " ") + " · " + server
}

func buildCapabilityResult(name, content, input string) StructuredResult {
	switch name {
	case "capability_search":
		return buildCapabilitySearch(content)
	case "capability_activate":
		return buildCapabilityActivation(content, input)
	case "mcp_read_resource", "mcp_get_prompt":
		return buildMCPContent(name, content, input)
	default:
		return buildDynamicMCPResult(content)
	}
}

func buildCapabilitySearch(content string) StructuredResult {
	var found captypes.SearchResult
	if json.Unmarshal([]byte(content), &found) != nil || found.Results == nil {
		result := BuildReadableResult(content)
		result.Summary = "Capability search result unavailable"
		return result
	}
	entries, warning := found.Results, found.Warning
	result := StructuredResult{Summary: resultCount(len(entries), "capability", "capabilities"), Unit: "capabilities"}
	if len(entries) == 0 {
		result.Summary = "No capabilities found"
		result.Sections = []ResultSection{{Body: "No installed or source capabilities matched this query."}}
		return result
	}
	for _, entry := range entries {
		title := entry.Name
		if title == "" {
			title = entry.ID
		} else if title != entry.ID && entry.ID != "" {
			title += " (" + entry.ID + ")"
		}
		section := ResultSection{Title: title, Metadata: strings.Join(nonempty(string(entry.Kind), entry.State, entry.Source), " · "), Body: entry.Description}
		if entry.Reason != "" {
			section.Body += "\n" + entry.Reason
		}
		for _, field := range []ResultField{
			{"ID", entry.ID}, {"Name", entry.Name}, {"Kind", string(entry.Kind)}, {"Installed", fmt.Sprint(entry.Installed)},
			{"State", entry.State}, {"Reason", entry.Reason}, {"Source", entry.Source}, {"Transport", entry.Transport},
			{"Package", entry.Package}, {"Revision", entry.Version}, {"Repository", entry.Repository},
		} {
			if field.Value != "" {
				section.Details = append(section.Details, field)
			}
		}
		result.Sections = append(result.Sections, section)
	}
	if warning != "" {
		result.Sections = append(result.Sections, ResultSection{Title: "Partial discovery", Body: warning})
	}
	return result
}

func nonempty(values ...string) []string {
	var out []string
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func resultInputField(input, key string) string {
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(input), &fields) != nil {
		return ""
	}
	var value string
	_ = json.Unmarshal(fields[key], &value)
	return value
}

func buildCapabilityActivation(content, input string) StructuredResult {
	id := resultInputField(input, "id")
	if id == "" {
		id = resultInputField(input, "connection_id")
	}
	if strings.HasPrefix(strings.TrimSpace(content), "<skill ") {
		// Only the opening tag is XML: the service embeds the instruction
		// document verbatim, which may contain unescaped XML/Markdown itself.
		open := strings.Index(content, ">")
		if open >= 0 && strings.HasSuffix(strings.TrimSpace(content), "</skill>") {
			var tag struct {
				XMLName  xml.Name `xml:"skill"`
				Source   string   `xml:"source,attr"`
				Revision string   `xml:"revision,attr"`
				Digest   string   `xml:"digest,attr"`
				Path     string   `xml:"path,attr"`
			}
			if xml.Unmarshal([]byte(content[:open+1]+"</skill>"), &tag) == nil && tag.Source != "" && (id == "" || id == tag.Source) {
				body := strings.TrimPrefix(content[open+1:strings.LastIndex(content, "</skill>")], "\n")
				body = strings.TrimSuffix(body, "\n")
				details := []ResultField{{"Source", tag.Source}, {"Revision", tag.Revision}, {"Digest", tag.Digest}}
				if tag.Path != "" {
					details = append(details, ResultField{"Installed path", tag.Path})
				}
				return StructuredResult{Summary: "Skill instructions loaded", Unit: "skill", Sections: []ResultSection{{Title: tag.Source, Metadata: "Source instructions · not an access grant", Body: body, Details: details}}}
			}
		}
		return StructuredResult{Summary: "Skill instructions unavailable", Sections: []ResultSection{{Body: "Skill attribution could not be verified."}}}
	}
	var activation captypes.ActivationResult
	if json.Unmarshal([]byte(content), &activation) == nil && activation.ConnectionID != "" &&
		(id == "" || id == activation.ConnectionID) {
		result := StructuredResult{Summary: fmt.Sprintf("MCP %s · %d usable tools", activation.State, len(activation.Usable)), Unit: "tools"}
		result.Sections = append(result.Sections, ResultSection{Title: activation.ConnectionID, Body: activation.Next})
		for _, tool := range activation.Usable {
			result.Sections = append(result.Sections, ResultSection{
				Title: tool.Tool, Metadata: strings.Join(nonempty("Available", string(tool.Effect), tool.Scope), " · "),
				Details: []ResultField{{"Name", tool.Name}, {"Effect", string(tool.Effect)}, {"Scope", tool.Scope}},
			})
		}
		for _, notice := range activation.Notices {
			result.Sections = append(result.Sections, ResultSection{Title: "Request limit", Body: notice})
		}
		return result
	}
	result := BuildReadableResult(content)
	result.Summary = "Activation result"
	if id != "" {
		result.Sections = append([]ResultSection{{Title: id, Metadata: "Requested capability"}}, result.Sections...)
	}
	return result
}

func buildMCPContent(name, content, input string) StructuredResult {
	var envelope struct {
		Content []captypes.Content `json:"Content"`
	}
	result := StructuredResult{Summary: "MCP source content", Unit: "source"}
	if name == "mcp_get_prompt" {
		result.Summary = "MCP prompt content"
	}
	connection := resultInputField(input, "connection_id")
	requested := resultInputField(input, "uri")
	if name == "mcp_get_prompt" {
		requested = resultInputField(input, "name")
	}
	if err := json.Unmarshal([]byte(content), &envelope); err == nil && envelope.Content != nil {
		for _, item := range envelope.Content {
			section := ResultSection{Title: requested, Metadata: strings.Join(nonempty("External source", connection, item.URI, item.MIMEType, item.Type), " · "), Body: item.Text}
			if item.URI != "" {
				section.Details = append(section.Details, ResultField{"URI", item.URI})
			}
			if item.MIMEType != "" {
				section.Details = append(section.Details, ResultField{"MIME type", item.MIMEType})
			}
			if item.Data != "" {
				section.Details = append(section.Details, ResultField{"Encoded data", item.Data})
			}
			result.Sections = append(result.Sections, section)
		}
		if len(result.Sections) == 0 {
			result.Summary = "MCP source returned no content"
		}
		return result
	}
	// The current session flattens parts to plain text. Preserve that text and
	// attribute it to the requested connection/resource, not to the user.
	readable := BuildReadableResult(content)
	readable.Summary, readable.Unit = result.Summary, result.Unit
	for i := range readable.Sections {
		readable.Sections[i].Metadata = strings.Join(nonempty("External source", connection, requested), " · ")
	}
	return readable
}

func buildDynamicMCPResult(content string) StructuredResult {
	result := BuildReadableResult(content)
	result.Summary = "MCP operation result"
	if strings.Contains(content, "Result unknown: the remote operation may have taken effect. Do not retry blindly.") {
		result.Summary = "MCP result unknown"
	}
	return result
}
