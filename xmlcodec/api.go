package xmlcodec

import (
	"encoding/xml"
	"fmt"
	"strings"
)

type Param struct {
	XMLName xml.Name    `xml:"param"`
	Name    string      `xml:"name,attr"`
	Value   CDATAString `xml:",chardata"`
}

type Parameters struct {
	XMLName xml.Name `xml:"parameters"`
	Params  []Param  `xml:"param"`
}

func (p *Parameters) ToMap() map[string]string {
	result := make(map[string]string)
	for _, param := range p.Params {
		result[param.Name] = string(param.Value)
	}
	return result
}

// extractParams extracts <param> elements from a <parameters> child of a node,
// falling back to direct <param> children of the node itself.
func extractParams(n *Node) []Param {
	for _, child := range n.Children() {
		if child.TagName() == "parameters" {
			params := make([]Param, 0)
			for _, pc := range child.Children() {
				if pc.TagName() == "param" {
					params = append(params, Param{
						Name:  pc.Attr()["name"],
						Value: CDATAString(pc.Text()),
					})
				}
			}
			if len(params) > 0 {
				return params
			}
		}
	}
	params := make([]Param, 0)
	for _, child := range n.Children() {
		if child.TagName() == "param" {
			params = append(params, Param{
				Name:  child.Attr()["name"],
				Value: CDATAString(child.Text()),
			})
		}
	}
	return params
}

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

type PlanXML struct {
	XMLName xml.Name  `xml:"plan"`
	Nodes   []NodeXML `xml:"node"`
}

type NodeXML struct {
	XMLName     xml.Name   `xml:"node"`
	ID          string     `xml:"id,attr"`
	Operation   string     `xml:"operation,attr"`
	Description string     `xml:"description,attr"`
	Tool        string     `xml:"tool,attr"`
	DependsOn   string     `xml:"depends_on,attr"`
	Question    string     `xml:"block_question,attr,omitempty"`
	Parameters  Parameters `xml:"parameters"`
}

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

type GroundingXML struct {
	XMLName      xml.Name         `xml:"grounding"`
	Thinking     string           `xml:"thinking"`
	Observations []ObservationXML `xml:"observation"`
}

type ObservationXML struct {
	XMLName    xml.Name   `xml:"observation"`
	Tool       string     `xml:"tool"`
	Parameters Parameters `xml:"parameters"`
}

type TermsXML struct {
	XMLName xml.Name `xml:"terms"`
	Terms   []string `xml:"term"`
}

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

func StripXMLFences(s string) string {
	s = strings.TrimSpace(s)
	for _, prefix := range []string{"```xml", "```XML", "```"} {
		if strings.HasPrefix(s, prefix) {
			s = strings.TrimPrefix(s, prefix)
			if idx := strings.Index(s, "\n"); idx >= 0 {
				s = s[idx+1:]
			}
			break
		}
	}
	if idx := strings.LastIndex(s, "```"); idx >= 0 {
		s = s[:idx]
	}
	return strings.TrimSpace(s)
}

func ParsePlan(raw string) (*PlanXML, error) {
	raw = StripXMLFences(raw)

	parser := NewIncomingXMLParserSelectorEngine()
	parser.Add(raw)

	nodes := parser.Select("node", false)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("xmlcodec: failed to parse plan XML")
	}

	plan := &PlanXML{Nodes: make([]NodeXML, 0, len(nodes))}
	for _, n := range nodes {
		nodeXML := NodeXML{
			ID:          n.Attr()["id"],
			Operation:   n.Attr()["operation"],
			Description: n.Attr()["description"],
			Tool:        n.Attr()["tool"],
			DependsOn:   n.Attr()["depends_on"],
			Question:    n.Attr()["block_question"],
		}

		params := extractParams(n)
		nodeXML.Parameters = Parameters{Params: params}
		plan.Nodes = append(plan.Nodes, nodeXML)
	}

	return plan, nil
}

func ParseGrounding(raw string) (*GroundingXML, error) {
	raw = StripXMLFences(raw)

	parser := NewIncomingXMLParserSelectorEngine()
	parser.Add(raw)

	groundingNodes := parser.Select("grounding", false)
	if len(groundingNodes) == 0 {
		return nil, fmt.Errorf("xmlcodec: no grounding element found")
	}

	var thinking string
	var obs []ObservationXML

	thinkingNodes := parser.Select("thinking", false)
	if len(thinkingNodes) > 0 {
		thinking = thinkingNodes[0].Text()
	}

	obsNodes := parser.Select("observation", false)
	for _, n := range obsNodes {
		var tool string
		for _, child := range n.Children() {
			if child.TagName() == "tool" {
				tool = strings.TrimSpace(child.Text())
				break
			}
		}
		if tool == "" {
			continue
		}

		obs = append(obs, ObservationXML{
			Tool:       tool,
			Parameters: Parameters{Params: extractParams(n)},
		})
	}

	return &GroundingXML{
		Thinking:     thinking,
		Observations: obs,
	}, nil
}

func ParseParams(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if !strings.HasPrefix(raw, "<") {
		return nil, fmt.Errorf("xmlcodec: invalid XML args")
	}
	if !strings.Contains(raw, "<args>") && !strings.Contains(raw, "<parameters>") {
		raw = "<args>" + raw + "</args>"
	}

	parser := NewIncomingXMLParserSelectorEngine()
	parser.Add(raw)

	result := make(map[string]string)

	params := parser.Select("param", false)
	for _, n := range params {
		name := n.Attr()["name"]
		if name != "" {
			result[name] = n.Text()
		}
	}

	if len(result) == 0 {
		params = parser.Select("parameters/param", false)
		for _, n := range params {
			name := n.Attr()["name"]
			if name != "" {
				result[name] = n.Text()
			}
		}
	}

	return result, nil
}

func ParseTerms(raw string) ([]string, error) {
	raw = StripXMLFences(raw)
	raw = strings.TrimSpace(raw)

	if !strings.Contains(raw, "<terms>") && !strings.HasPrefix(raw, "<") {
		return nil, nil
	}
	if !strings.Contains(raw, "<terms>") {
		raw = "<terms>" + raw + "</terms>"
	}

	parser := NewIncomingXMLParserSelectorEngine()
	parser.Add(raw)

	termNodes := parser.Select("term", false)
	var result []string
	for _, n := range termNodes {
		term := strings.TrimSpace(n.Text())
		if term != "" {
			result = append(result, term)
		}
	}

	return result, nil
}
