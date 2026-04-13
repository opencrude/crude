package xmlcodec

import (
	"fmt"
	"strings"

	"golang.org/x/net/html"
)

type Element struct {
	Type     string
	Name     string
	Key      int
	Attribs  map[string]string
	Children []*Element
	Parent   *Element
	Closed   bool
	Text     string
	Data     string
	Prev     *Element
	Next     *Element
}

type Node struct {
	tagName      string
	text         string
	children     []*Node
	key          int
	closed       bool
	attr         map[string]string
	isNodeObj    bool
	aggregate    string
	length       int
	deduped      bool
	dedupedChild []*Node
}

func NewNode(name string, options map[string]any) *Node {
	node := &Node{
		tagName:   name,
		children:  []*Node{},
		attr:      make(map[string]string),
		isNodeObj: true,
	}

	if text, ok := options["text"].(string); ok {
		node.text = text
	}
	if children, ok := options["children"].([]*Node); ok {
		node.children = children
	}
	if key, ok := options["key"].(int); ok {
		node.key = key
	}
	if closed, ok := options["closed"].(bool); ok {
		node.closed = closed
	}
	if agg, ok := options["aggregateText"].(string); ok {
		node.aggregate = agg
	}
	if attrs, ok := options["attr"].(map[string]string); ok {
		for k, v := range attrs {
			node.attr[k] = v
		}
	}

	node.length = 0
	return node
}

func (n *Node) TagName() string         { return n.tagName }
func (n *Node) Text() string            { return n.text }
func (n *Node) Children() []*Node       { return n.children }
func (n *Node) Key() int                { return n.key }
func (n *Node) Closed() bool            { return n.closed }
func (n *Node) Attr() map[string]string { return n.attr }
func (n *Node) IsNodeObj() bool         { return n.isNodeObj }
func (n *Node) AggregateText() string {
	if n.aggregate != "" {
		return n.aggregate
	}
	return n.Text()
}
func (n *Node) Length() int              { return n.length }
func (n *Node) Deduped() bool            { return n.deduped }
func (n *Node) DedupedChildren() []*Node { return n.dedupedChild }

func (n *Node) Get(field string) any {
	switch field {
	case "$$text":
		return n.text
	case "$$tagname":
		return n.tagName
	case "$$children":
		return n.children
	case "$$tagkey":
		return n.key
	case "$$tagclosed":
		return n.closed
	case "$$attr":
		return n.attr
	default:
		return nil
	}
}

type Parser interface {
	Add(chunk string)
	Select(selector string, includeOpenTags bool) []*Node
	DedupeSelect(selector string, includeOpenTags bool) []*Node
	MapSelect(mapping map[string]any, includeOpenTags bool, doDedupe bool) map[string]any
	MapSelectClosed(mapping map[string]any) map[string]any
	MakeMapSelectScaffold(schema map[string]any, hints map[string]any) string
	MakeArrayScaffold(tag, content string) string
	MakeObjectScaffold(tag string, opts map[string]any) string
	GenerateScaffold(schema map[string]any, hints map[string]any, indent int) string
	Flush() string
}

type AbstractIncomingParserSelectorEngine struct {
	buffer                    string
	position                  int
	elementIndex              int
	parsedData                []*Element
	openElements              []*Element
	returnedElementSignatures map[string]string
	normalizedSchemaCache     map[any]any
}

func NewAbstractIncomingParserSelectorEngine() *AbstractIncomingParserSelectorEngine {
	return &AbstractIncomingParserSelectorEngine{
		parsedData:                make([]*Element, 0),
		openElements:              make([]*Element, 0),
		returnedElementSignatures: make(map[string]string),
		normalizedSchemaCache:     make(map[any]any),
	}
}

func (p *AbstractIncomingParserSelectorEngine) Add(chunk string) {
	p.buffer += chunk
}

func (p *AbstractIncomingParserSelectorEngine) Flush() string {
	result := p.buffer
	p.buffer = ""
	return result
}

func (p *AbstractIncomingParserSelectorEngine) Select(selector string, includeOpenTags bool) []*Node {
	return nil
}

func (p *AbstractIncomingParserSelectorEngine) DedupeSelect(selector string, includeOpenTags bool) []*Node {
	return nil
}

func (p *AbstractIncomingParserSelectorEngine) MapSelect(mapping map[string]any, includeOpenTags bool, doDedupe bool) map[string]any {
	return nil
}

func (p *AbstractIncomingParserSelectorEngine) MapSelectClosed(mapping map[string]any) map[string]any {
	return p.MapSelect(mapping, false, true)
}

func (p *AbstractIncomingParserSelectorEngine) MakeMapSelectScaffold(schema map[string]any, hints map[string]any) string {
	return p.GenerateScaffold(schema, hints, 2)
}

func (p *AbstractIncomingParserSelectorEngine) GenerateScaffold(schema map[string]any, hints map[string]any, indent int) string {
	return ""
}

func (p *AbstractIncomingParserSelectorEngine) MakeArrayScaffold(tag, content string) string {
	ind := "  "
	return ind + "<" + tag + ">" + content + "</" + tag + ">\n" +
		ind + "<" + tag + ">" + content + "</" + tag + ">\n" +
		ind + "/*etc.*/"
}

func (p *AbstractIncomingParserSelectorEngine) MakeObjectScaffold(tag string, opts map[string]any) string {
	return fmt.Sprintf("<%s>...</%s>", tag, tag)
}

func (p *AbstractIncomingParserSelectorEngine) GEN_ATTRIBUTE_MARKER() string {
	return "$"
}

func (p *AbstractIncomingParserSelectorEngine) GEN_TYPE_HINT(type_, hint string) string {
	return fmt.Sprintf("...%s...", type_)
}

func (p *AbstractIncomingParserSelectorEngine) GEN_OPEN_TAG(name string, attrs map[string]any, hints map[string]any) string {
	return fmt.Sprintf("<%s>", name)
}

func (p *AbstractIncomingParserSelectorEngine) GEN_CLOSE_TAG(name string) string {
	return fmt.Sprintf("</%s>", name)
}

func (p *AbstractIncomingParserSelectorEngine) GEN_CDATA_OPEN() string {
	return "<![CDATA["
}

func (p *AbstractIncomingParserSelectorEngine) GEN_CDATA_CLOSE() string {
	return "]]>"
}

func (p *AbstractIncomingParserSelectorEngine) GEN_ATTRIBUTE(key, value string) string {
	return key + `="` + value + `"`
}

var xmlSelfClosingTags = map[string]bool{}

type IncomingXMLParserSelectorEngine struct {
	*AbstractIncomingParserSelectorEngine
	elementIndex          int
	normalizedSchemaCache map[any]any
	elementKey            int
	leftover              string
}

func NewIncomingXMLParserSelectorEngine() *IncomingXMLParserSelectorEngine {
	engine := &IncomingXMLParserSelectorEngine{
		AbstractIncomingParserSelectorEngine: NewAbstractIncomingParserSelectorEngine(),
		elementIndex:                         0,
		normalizedSchemaCache:                make(map[any]any),
		elementKey:                           0,
		leftover:                             "",
	}
	return engine
}

var ReservedProperties = map[string]bool{
	"$$tagname":     true,
	"$$tagkey":      true,
	"$$children":    true,
	"$$tagclosed":   true,
	"$$attr":        true,
	"__isNodeObj__": true,
}

func (p *IncomingXMLParserSelectorEngine) Add(chunk string) {
	p.buffer += chunk

	input := p.leftover + chunk
	reader := strings.NewReader(input)
	z := html.NewTokenizer(reader)
	z.AllowCDATA(true)

	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			err := z.Err()
			if err != nil && err.Error() != "EOF" {
				fmt.Printf("Tokenizer error: %v\n", err)
			}
			if err != nil && err.Error() == "EOF" {
				p.leftover = input[len(input)-len(z.Buffered()):]
			} else {
				p.leftover = ""
			}
			break
		}
		p.processToken(z, tt)
	}
}

func (p *IncomingXMLParserSelectorEngine) processToken(z *html.Tokenizer, tt html.TokenType) {
	switch tt {
	case html.StartTagToken:
		name, hasAttr := z.TagName()
		tagName := string(name)
		if tagName == "" {
			return
		}

		attrs := make(map[string]string)
		if hasAttr {
			for {
				key, val, more := z.TagAttr()
				if len(key) == 0 {
					break
				}
				attrs[string(key)] = string(val)
				if !more {
					break
				}
			}
		}

		isSelfClosing := xmlSelfClosingTags[tagName]

		element := &Element{
			Type:     "tag",
			Name:     tagName,
			Key:      p.elementKey,
			Attribs:  attrs,
			Children: []*Element{},
			Closed:   isSelfClosing,
			Data:     "",
			Prev:     nil,
			Next:     nil,
		}
		p.elementKey++

		if len(p.openElements) > 0 {
			parent := p.openElements[len(p.openElements)-1]
			element.Parent = parent
			if len(parent.Children) > 0 {
				element.Prev = parent.Children[len(parent.Children)-1]
				if element.Prev != nil {
					element.Prev.Next = element
				}
			}
			parent.Children = append(parent.Children, element)
		} else {
			element.Parent = nil
			p.parsedData = append(p.parsedData, element)
		}

		if !isSelfClosing {
			p.openElements = append(p.openElements, element)
		}

	case html.EndTagToken:
		name, _ := z.TagName()
		tagName := string(name)
		if tagName == "" {
			return
		}

		if len(p.openElements) > 0 {
			lastOpen := p.openElements[len(p.openElements)-1]
			if lastOpen.Name == tagName {
				lastOpen.Closed = true
				p.openElements = p.openElements[:len(p.openElements)-1]
			}
		}

	case html.TextToken:
		text := string(z.Text())
		if len(p.openElements) > 0 {
			parent := p.openElements[len(p.openElements)-1]
			textNode := &Element{
				Type:   "text",
				Name:   "",
				Key:    -1,
				Data:   text,
				Parent: parent,
				Closed: true,
			}
			if len(parent.Children) > 0 {
				textNode.Prev = parent.Children[len(parent.Children)-1]
				if textNode.Prev != nil {
					textNode.Prev.Next = textNode
				}
			}
			parent.Children = append(parent.Children, textNode)
		}

	case html.SelfClosingTagToken:
		name, hasAttr := z.TagName()
		tagName := string(name)
		if tagName == "" {
			return
		}

		attrs := make(map[string]string)
		if hasAttr {
			for {
				key, val, more := z.TagAttr()
				if len(key) == 0 {
					break
				}
				attrs[string(key)] = string(val)
				if !more {
					break
				}
			}
		}

		element := &Element{
			Type:     "tag",
			Name:     tagName,
			Key:      p.elementKey,
			Attribs:  attrs,
			Children: []*Element{},
			Closed:   true,
			Data:     "",
			Prev:     nil,
			Next:     nil,
		}
		p.elementKey++

		if len(p.openElements) > 0 {
			parent := p.openElements[len(p.openElements)-1]
			element.Parent = parent
			if len(parent.Children) > 0 {
				element.Prev = parent.Children[len(parent.Children)-1]
				if element.Prev != nil {
					element.Prev.Next = element
				}
			}
			parent.Children = append(parent.Children, element)
		} else {
			element.Parent = nil
			p.parsedData = append(p.parsedData, element)
		}
	}
}

func (p *IncomingXMLParserSelectorEngine) GEN_ATTRIBUTE_MARKER() string {
	return "$"
}

func (p *IncomingXMLParserSelectorEngine) GEN_TYPE_HINT(type_, hint string) string {
	if hint != "" {
		return type_ + " " + hint
	}
	return "{" + type_ + "}"
}

func (p *IncomingXMLParserSelectorEngine) GEN_OPEN_TAG(name string, attrs map[string]any, hints map[string]any) string {
	var attrStr string
	for k, v := range attrs {
		if k == "$$text" {
			continue
		}
		hintVal := ""
		if hints != nil {
			if h, ok := hints["$"+k]; ok {
				hintVal = " " + h.(string)
			}
		}
		if fn, ok := v.(func() string); ok {
			attrStr += " " + k + `="` + p.GEN_TYPE_HINT(fn(), hintVal) + `"`
		} else if str, ok := v.(string); ok {
			attrStr += " " + k + `="` + str + `"`
		} else {
			attrStr += " " + k + `="..."`
		}
	}
	return "<" + name + attrStr + ">"
}

func (p *IncomingXMLParserSelectorEngine) GEN_CLOSE_TAG(name string) string {
	return "</" + name + ">"
}

func (p *IncomingXMLParserSelectorEngine) GEN_CDATA_OPEN() string {
	return "<![CDATA["
}

func (p *IncomingXMLParserSelectorEngine) GEN_CDATA_CLOSE() string {
	return "]]>"
}

func (p *IncomingXMLParserSelectorEngine) GEN_ATTRIBUTE(key, value string) string {
	return key + `="` + value + `"`
}

func (p *IncomingXMLParserSelectorEngine) getAttributeString(obj map[string]any, hints map[string]any) string {
	if obj == nil {
		return ""
	}

	var attrs string
	for k, v := range obj {
		if k == "$$text" {
			continue
		}
		if !strings.HasPrefix(k, "$") {
			continue
		}
		attrName := k[1:]
		var value string
		var hint string

		if hints != nil {
			if h, ok := hints[k]; ok {
				hint = h.(string)
			}
		}

		if fn, ok := v.(func() string); ok {
			value = p.GEN_TYPE_HINT(fn(), hint)
		} else if str, ok := v.(string); ok {
			value = str
		} else {
			value = "..."
		}

		if hint != "" {
			attrs += " " + attrName + `="` + hint + `"`
		} else {
			attrs += " " + attrName + `="` + value + `"`
		}
	}
	return attrs
}

func (p *IncomingXMLParserSelectorEngine) MakeArrayScaffold(tag, content string) string {
	ind := "  "
	return ind + "<" + tag + ">" + content + "</" + tag + ">\n" +
		ind + "<" + tag + ">" + content + "</" + tag + ">\n" +
		ind + "/*etc.*/"
}

func (p *IncomingXMLParserSelectorEngine) MakeObjectScaffold(tag string, opts map[string]any) string {
	var result strings.Builder
	result.WriteString("<" + tag + ">")

	if attrs, ok := opts["attributes"].([][2]string); ok {
		for _, attr := range attrs {
			result.WriteString("<$" + attr[0] + ">" + attr[1] + "</$" + attr[0] + ">")
		}
	}

	if text, ok := opts["textContent"].(string); ok && text != "" {
		result.WriteString(text)
	} else {
		result.WriteString("...")
	}

	if props, ok := opts["properties"].(map[string]any); ok {
		for k := range props {
			result.WriteString("<" + k + ">...")
			result.WriteString("</" + k + ">")
		}
	}

	result.WriteString("</" + tag + ">")
	return result.String()
}

func (p *IncomingXMLParserSelectorEngine) Select(selector string, includeOpenTags bool) []*Node {
	var results []*Node

	if selector == "" || selector == "*" {
		results = p.selectAllElements(p.parsedData)
	} else if strings.HasPrefix(selector, "/") {
		results = p.selectByXPath(selector)
	} else {
		results = p.selectByTagName(selector, p.parsedData)
	}

	if !includeOpenTags {
		var filtered []*Node
		for _, n := range results {
			if n.closed {
				filtered = append(filtered, n)
			}
		}
		results = filtered
	}

	return results
}

func (p *IncomingXMLParserSelectorEngine) selectAllElements(elements []*Element) []*Node {
	var result []*Node
	for _, el := range elements {
		result = append(result, p.formatElement(el, true))
		result = append(result, p.selectAllElements(el.Children)...)
	}
	return result
}

func (p *IncomingXMLParserSelectorEngine) selectByTagName(tagName string, elements []*Element) []*Node {
	var result []*Node
	for _, el := range elements {
		if el.Type == "tag" && (tagName == "*" || el.Name == tagName) {
			result = append(result, p.formatElement(el, true))
		}
		result = append(result, p.selectByTagName(tagName, el.Children)...)
	}
	return result
}

func (p *IncomingXMLParserSelectorEngine) selectByXPath(xpath string) []*Node {
	parts := strings.Split(xpath, "/")
	var result []*Node
	p.matchXPath(parts, 0, p.parsedData, &result)
	return result
}

func (p *IncomingXMLParserSelectorEngine) matchXPath(parts []string, idx int, elements []*Element, result *[]*Node) {
	if idx >= len(parts) {
		for _, el := range elements {
			*result = append(*result, p.formatElement(el, true))
		}
		return
	}

	part := parts[idx]
	if part == "" || part == "." {
		p.matchXPath(parts, idx+1, elements, result)
		return
	}

	for _, el := range elements {
		if el.Type == "tag" && (part == "*" || el.Name == part) {
			if idx+1 < len(parts) {
				p.matchXPath(parts, idx+1, el.Children, result)
			} else {
				*result = append(*result, p.formatElement(el, true))
			}
		}
	}
}

func (p *IncomingXMLParserSelectorEngine) formatElement(el *Element, includeOpenTags bool) *Node {
	if el.Type == "text" {
		text := el.Data
		if text == "" {
			return nil
		}
		return NewNode("TEXT_NODE", map[string]any{
			"key":      -1,
			"text":     text,
			"closed":   true,
			"children": []*Node{},
			"attr":     map[string]string{},
		})
	}

	aggregateText := p.getTextContent(el)
	closed := el.Closed

	var children []*Node
	for _, child := range el.Children {
		formatted := p.formatElement(child, includeOpenTags)
		if formatted != nil {
			children = append(children, formatted)
		}
	}

	textContent := aggregateText

	node := NewNode(el.Name, map[string]any{
		"key":           el.Key,
		"attr":          el.Attribs,
		"aggregateText": aggregateText,
		"text":          textContent,
		"closed":        closed,
		"children":      children,
	})

	return node
}

func (p *IncomingXMLParserSelectorEngine) getTextContent(el *Element) string {
	var sb strings.Builder
	for _, child := range el.Children {
		if child.Type == "text" {
			sb.WriteString(child.Data)
		} else {
			sb.WriteString(p.getTextContent(child))
		}
	}
	return sb.String()
}

func (p *IncomingXMLParserSelectorEngine) DedupeSelect(selector string, includeOpenTags bool) []*Node {
	results := p.Select(selector, includeOpenTags)
	if results == nil {
		return nil
	}

	var deduped []*Node
	for _, el := range results {
		d := p.dedupeElement(el, includeOpenTags)
		if d != nil {
			deduped = append(deduped, d)
		}
	}

	return deduped
}

func (p *IncomingXMLParserSelectorEngine) getElementSignature(el *Node, deep bool) string {
	if el == nil {
		return ""
	}
	parts := []string{el.tagName}
	for k, v := range el.attr {
		parts = append(parts, k+"="+v)
	}
	if !deep {
		return strings.Join(parts, "|")
	}
	if el.aggregate != "" {
		parts = append(parts, el.aggregate)
	}
	return strings.Join(parts, "|")
}

func (p *IncomingXMLParserSelectorEngine) dedupeElement(el *Node, includeOpenTags bool) *Node {
	if el == nil {
		return nil
	}

	signature := p.getElementSignature(el, true)
	fullSignature := p.getElementSignature(el, false)

	if !el.closed {
		return el
	}

	existing, ok := p.returnedElementSignatures[signature]
	if !ok {
		p.returnedElementSignatures[signature] = fullSignature
		return el
	}

	if !el.closed && existing != fullSignature {
		p.returnedElementSignatures[signature] = fullSignature
		return el
	}

	return nil
}

func (p *IncomingXMLParserSelectorEngine) MapSelect(mapping map[string]any, includeOpenTags bool, doDedupe bool) map[string]any {
	result := make(map[string]any)

	for key, schema := range mapping {
		var nodes []*Node
		if doDedupe {
			nodes = p.DedupeSelect(key, includeOpenTags)
		} else {
			nodes = p.Select(key, includeOpenTags)
		}

		var values []any
		for _, node := range nodes {
			val := p.applySchema(node, schema)
			values = append(values, val)
		}
		result[key] = values
	}

	return result
}

func (p *IncomingXMLParserSelectorEngine) applySchema(node *Node, schema any) any {
	if node == nil {
		return nil
	}

	switch s := schema.(type) {
	case string:
		if s == "String" {
			return node.Text()
		}
		if s == "Number" {
			return node.Text()
		}
		if s == "Boolean" {
			return node.Text() == "true"
		}
		return node.Text()
	case map[string]any:
		obj := make(map[string]any)
		for k, v := range s {
			if strings.HasPrefix(k, "$") {
				attrName := k[1:]
				obj[k] = node.attr[attrName]
			} else if k == "$$text" {
				obj["text"] = node.Text()
			} else if childNodes := node.Children(); len(childNodes) > 0 {
				for _, child := range childNodes {
					if child.tagName == k {
						obj[k] = p.applySchema(child, v)
						break
					}
				}
			}
		}
		return obj
	default:
		return node.Text()
	}
}

func (p *IncomingXMLParserSelectorEngine) MakeMapSelectScaffold(schema map[string]any, hints map[string]any) string {
	return p.GenerateScaffold(schema, hints, 2)
}

func (p *IncomingXMLParserSelectorEngine) GenerateScaffold(schema map[string]any, hints map[string]any, indent int) string {
	if schema == nil {
		return ""
	}

	var lines []string
	ind := strings.Repeat("  ", indent)

	for key, value := range schema {
		if strings.HasPrefix(key, "$") {
			continue
		}

		hint := ""
		if hints != nil {
			if h, ok := hints[key]; ok {
				if hs, ok := h.(string); ok {
					hint = hs
				}
			}
		}

		switch v := value.(type) {
		case string:
			typeHint := p.GEN_TYPE_HINT(v, hint)
			lines = append(lines, ind+"<"+key+">"+typeHint+"</"+key+">")
		case map[string]any:
			lines = append(lines, ind+"<"+key+">")
			subIndent := indent + 1
			for subKey := range v {
				if strings.HasPrefix(subKey, "$") {
					lines = append(lines, strings.Repeat("  ", subIndent)+"<"+subKey+">...</"+subKey+">")
				} else {
					lines = append(lines, strings.Repeat("  ", subIndent)+"<"+subKey+">...</"+subKey+">")
				}
			}
			lines = append(lines, ind+"</"+key+">")
		default:
			lines = append(lines, ind+"<"+key+">...</"+key+">")
		}
	}

	return strings.Join(lines, "\n")
}
