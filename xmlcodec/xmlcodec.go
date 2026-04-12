// Package xmlcodec provides XML encoding/decoding with CDATA support for LLM-generated structured output.
// It replaces JSON as the primary format for DAG plans, grounding responses, and tool arguments.
package xmlcodec

import (
	"encoding/xml"
	"fmt"
	"strings"

	"crude/xmlrepair"
)

// stripXMLFences removes markdown XML code fences (```xml ... ``` or just ``` ... ```).
func StripXMLFences(s string) string {
	s = strings.TrimSpace(s)
	// Remove ```xml or ``` fence at start
	for _, prefix := range []string{"```xml", "```XML", "```"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			// Remove everything up to and including the first newline
			if idx := strings.Index(s, "\n"); idx >= 0 {
				s = s[idx+1:]
			}
			break
		}
	}
	// Remove closing ``` fence
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

// CDATAString is a string that marshals/unmarshals as CDATA in XML.
type CDATAString string

func (c CDATAString) MarshalXML(e *xml.Encoder, start xml.StartElement) error {
	return e.EncodeElement(xml.CharData([]byte(c)), start)
}

func (c *CDATAString) UnmarshalXML(d *xml.Decoder, start xml.StartElement) error {
	var data string
	if err := d.DecodeElement(&data, &start); err != nil {
		return err
	}
	*c = CDATAString(data)
	return nil
}

// Param represents a single named parameter in XML.
type Param struct {
	XMLName xml.Name    `xml:"param"`
	Name    string      `xml:"name,attr"`
	Value   CDATAString `xml:",chardata"`
}

// Parameters is a collection of named parameters.
type Parameters struct {
	XMLName xml.Name `xml:"parameters"`
	Params  []Param  `xml:"param"`
}

// ToMap converts XML parameters to a map[string]string for tool dispatching.
func (p *Parameters) ToMap() map[string]string {
	result := make(map[string]string)
	for _, param := range p.Params {
		result[param.Name] = string(param.Value)
	}
	return result
}

// MapToParams converts a map[string]string to XML Parameters.
func MapToParams(m map[string]string) Parameters {
	params := make([]Param, 0, len(m))
	for k, v := range m {
		params = append(params, Param{
			Name:  k,
			Value: CDATAString(v),
		})
	}
	return Parameters{Params: params}
}

// PlanXML is the top-level XML structure for a DAG execution plan.
type PlanXML struct {
	XMLName xml.Name   `xml:"plan"`
	Nodes   []NodeXML  `xml:"node"`
}

// NodeXML represents a single node in an XML execution plan.
type NodeXML struct {
	XMLName     xml.Name    `xml:"node"`
	ID          string      `xml:"id,attr"`
	Operation   string      `xml:"operation,attr"`
	Description string      `xml:"description,attr"`
	Tool        string      `xml:"tool,attr"`
	DependsOn   string      `xml:"depends_on,attr"`
	Question    string      `xml:"block_question,attr,omitempty"`
	Parameters  Parameters  `xml:"parameters"`
}

// ParseDependsOn splits the comma-separated depends_on attribute into a slice.
func (n *NodeXML) ParseDependsOn() []string {
	if n.DependsOn == "" {
		return nil
	}
	parts := strings.Split(n.DependsOn, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}

// GroundingXML is the XML structure for grounding phase responses.
type GroundingXML struct {
	XMLName      xml.Name       `xml:"grounding"`
	Thinking     string         `xml:"thinking"`
	Observations []ObservationXML `xml:"observation"`
}

// ObservationXML is a single read operation during grounding.
type ObservationXML struct {
	XMLName    xml.Name   `xml:"observation"`
	Tool       string     `xml:"tool"`
	Parameters Parameters `xml:"parameters"`
}

// ParsePlan parses an XML execution plan into a PlanXML structure.
// It handles markdown fences, common malformations, and repair.
func ParsePlan(raw string) (*PlanXML, error) {
	raw = StripXMLFences(raw)

	// Try direct parse first.
	var plan PlanXML
	if err := xml.Unmarshal([]byte(raw), &plan); err == nil {
		if len(plan.Nodes) > 0 {
			return &plan, nil
		}
	}

	// Try repair.
	repaired := xmlrepair.Repair(raw)
	if repaired != "" {
		if err := xml.Unmarshal([]byte(repaired), &plan); err == nil {
			if len(plan.Nodes) > 0 {
				return &plan, nil
			}
		}
	}

	// Try fixing unclosed CDATA sections.
	fixed := xmlrepair.FixUnclosedCDATA(raw)
	if fixed != "" {
		if err := xml.Unmarshal([]byte(fixed), &plan); err == nil {
			if len(plan.Nodes) > 0 {
				return &plan, nil
			}
		}
	}

	return nil, fmt.Errorf("xmlcodec: failed to parse plan XML")
}

// ParseGrounding parses an XML grounding response.
func ParseGrounding(raw string) (*GroundingXML, error) {
	raw = StripXMLFences(raw)

	var g GroundingXML
	if err := xml.Unmarshal([]byte(raw), &g); err == nil {
		return &g, nil
	}

	repaired := xmlrepair.Repair(raw)
	if repaired != "" {
		if err := xml.Unmarshal([]byte(repaired), &g); err == nil {
			return &g, nil
		}
	}

	fixed := xmlrepair.FixUnclosedCDATA(raw)
	if fixed != "" {
		if err := xml.Unmarshal([]byte(fixed), &g); err == nil {
			return &g, nil
		}
	}

	return nil, fmt.Errorf("xmlcodec: failed to parse grounding XML")
}

// ParseParams parses a small XML fragment into a map of parameters.
// Used for tool call arguments: <args><param name="x">val</param></args>
func ParseParams(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	// Wrap in root element if not already wrapped.
	if !strings.HasPrefix(raw, "<") {
		return nil, fmt.Errorf("xmlcodec: invalid XML args")
	}
	if !strings.Contains(raw, "<args>") && !strings.Contains(raw, "<parameters>") {
		raw = "<args>" + raw + "</args>"
	}

	var container struct {
		XMLName    xml.Name   `xml:"args"`
		Parameters Parameters `xml:"parameters"`
		Params     []Param    `xml:"param"`
	}
	if err := xml.Unmarshal([]byte(raw), &container); err != nil {
		return nil, err
	}

	// Prefer explicit <parameters> block.
	if len(container.Parameters.Params) > 0 {
		return container.Parameters.ToMap(), nil
	}
	// Fall back to flat <param> list.
	if len(container.Params) > 0 {
		result := make(map[string]string)
		for _, p := range container.Params {
			result[p.Name] = string(p.Value)
		}
		return result, nil
	}

	return make(map[string]string), nil
}

// TermsXML parses a simple XML list of terms for RAG enrichment.
type TermsXML struct {
	XMLName xml.Name `xml:"terms"`
	Terms   []string `xml:"term"`
}

// ParseTerms parses <terms><term>t1</term><term>t2</term></terms> or similar.
func ParseTerms(raw string) ([]string, error) {
	raw = StripXMLFences(raw)
	raw = strings.TrimSpace(raw)

	// Wrap in root if needed.
	if !strings.Contains(raw, "<terms>") && !strings.HasPrefix(raw, "<") {
		return nil, nil
	}
	if !strings.Contains(raw, "<terms>") {
		raw = "<terms>" + raw + "</terms>"
	}

	var t TermsXML
	if err := xml.Unmarshal([]byte(raw), &t); err != nil {
		return nil, err
	}

	// Filter empty.
	var result []string
	for _, term := range t.Terms {
		term = strings.TrimSpace(term)
		if term != "" {
			result = append(result, term)
		}
	}
	return result, nil
}
