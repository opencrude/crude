package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strconv"
	"strings"

	captypes "crude/types/capability"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (m *Manager) toolGrant(session *mcp.ClientSession, epoch uint64, tool captypes.Tool) (*jsonschema.Resolved, context.Context, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := m.connections[tool.ConnectionID]
	if !m.closed && c != nil && !c.config.Disabled && c.scopeChallenge == nil && c.session == session && c.epoch == epoch && c.discovered && !c.state.Cached && c.serverRevision == tool.ServerRevision {
		for _, current := range c.state.Tools {
			if current.Name == tool.Name && current.Fingerprint == tool.Fingerprint && current.OriginalName == tool.OriginalName && current.Effect == tool.Effect && current.Scope == tool.Scope && current.Selected {
				return c.outputSchemas[current.Fingerprint], c.lifetime, nil
			}
		}
	}
	return nil, nil, errors.New("MCP operation changed or discovery is unverified; refresh and review access before execution")
}

// callWithForms advances only acknowledged, input-required tool results.
// A timeout, dropped connection, transport error or busy server never causes
// another call. The SDK allocates each RPC ID and owns the wire representation.
func (m *Manager) callWithForms(ctx context.Context, session *mcp.ClientSession, lifetime context.Context, epoch uint64, config captypes.ConnectionConfig, tool captypes.Tool, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
	ask, guard := captypes.FormHandler(ctx, tool.Name)
	if session.InitializeResult().ProtocolVersion < "2026-07-28" {
		ask = nil
	}
	if ask != nil {
		// This version advertises capabilities per request. Do not enable forms
		// on browse, legacy, shell-dispatched or unrelated calls.
		params.Meta[mcp.MetaKeyClientCapabilities] = map[string]any{"elicitation": mcp.ElicitationCapabilities{Form: &mcp.FormElicitationCapabilities{}}}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(lifetime, cancel)
		defer stop()
	}
	check := func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if guard != nil {
			if err := guard(ctx, tool.ConnectionID); err != nil {
				return err
			}
		}
		_, _, err := m.toolGrant(session, epoch, tool)
		return err
	}
	if ask != nil {
		// Freeze caller-owned arguments before a human wait.
		if params.Arguments == nil {
			params.Arguments = map[string]any{}
		}
		raw, err := json.Marshal(params.Arguments)
		if err != nil {
			return nil, err
		}
		params.Arguments = json.RawMessage(raw)
	}
	declined := false
	for {
		if params.InputResponses != nil {
			if err := check(); err != nil {
				return nil, err
			}
		}
		// Limit each server exchange, not the person's decision between them.
		// Stop, disconnect and the caller's deadline still cover the entire call.
		rpc, cancel := context.WithTimeout(ctx, requestTimeout(config))
		result, err := session.CallTool(rpc, params)
		cancel()
		if err != nil || !result.NeedsInput() {
			return result, err
		}
		if ask == nil {
			return nil, errors.New("The MCP server requested user input. This call does not have a human question handler.")
		}
		if declined || len(result.InputRequests) == 0 || result.IsError || len(result.Content) != 0 || result.StructuredContent != nil {
			return nil, errors.New("The MCP tool did not complete. Crude did not repeat the call.")
		}
		// Validate the whole request set before displaying a question. A
		// mixed form/sampling request cannot extract answers or invoke a model.
		forms := make(map[string]*serverForm, len(result.InputRequests))
		for id, input := range result.InputRequests {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			request, ok := input.(*mcp.ElicitParams)
			if !ok || request == nil || request.Mode != "" && request.Mode != "form" || request.URL != "" || request.ElicitationID != "" {
				return nil, errors.New("The MCP tool requested unsupported input. No answers were sent.")
			}
			f, err := newServerForm(config.ID, tool.OriginalName, request)
			if err != nil {
				return nil, err
			}
			if m.privateFormData(config, f.form) {
				return nil, errors.New("The MCP form contains private data. No answers were sent.")
			}
			forms[id] = f
		}
		answers := make(mcp.InputResponseMap, len(forms))
		for _, id := range slices.Sorted(maps.Keys(forms)) {
			if err := check(); err != nil {
				return nil, err
			}
			f := forms[id]
			reply := ask(ctx, f.form)
			answer, err := f.answer(reply)
			if err != nil {
				return nil, err
			}
			if answer.Action == "accept" && m.privateFormData(config, answer.Content) {
				return nil, errors.New("The MCP answer looks like private data. No answers were sent.")
			}
			declined = declined || answer.Action != "accept"
			answers[id] = answer
		}
		next := *params
		next.RequestState, next.InputResponses = result.RequestState, answers
		params = &next
	}
}

type serverForm struct {
	form   captypes.Form
	schema *jsonschema.Resolved
}

var sensitiveForm = regexp.MustCompile(`(?i)pass[\s_-]?(?:word|phrase)|\bsecrets?\b|(?:client|api)[\s_-]?secret|(?:api|private|signing|access|encryption)[\s_-]?key|(?:auth|api|bearer|access|refresh|session|security)[\s_-]?token|(?:enter|paste|provide|share)\s+(?:your\s+|the\s+)?token[.!?:]?(?:$|\n)|\bcredentials?\b|payment|credit[\s_-]?card|debit[\s_-]?card|card[\s_-]?(?:number|security)|\bcvv\b|bank[\s_-]?account|security[\s_-]?code|one[\s_-]?time[\s_-]?(?:code|pass)|recovery[\s_-]?(?:key|code|phrase)|seed[\s_-]?phrase`)
var credentialValue = regexp.MustCompile(`(?i)\bbearer\s+\S+|-----BEGIN [^-]*KEY-----|\b(?:password|api[_-]?key|access[_-]?token)\s*[:=]\s*\S+|\b(?:sk-|gh[pousr]_|github_pat_|AKIA)[A-Za-z0-9_-]{12,}`)

// Check the data that will actually be shown or sent, not the user's JSON
// input notation. This also handles escaped strings and multi-select values.
func (m *Manager) privateFormData(config captypes.ConnectionConfig, data any) bool {
	b, err := json.Marshal(data)
	if err != nil || credentialValue.Match(b) {
		return true
	}
	s := string(b)
	return m.redact(config, s) != s
}

func newServerForm(connectionID, tool string, request *mcp.ElicitParams) (*serverForm, error) {
	invalid := errors.New("The MCP form is not supported. No answers were sent.")
	raw, err := json.Marshal(request.RequestedSchema)
	if err != nil || strings.TrimSpace(request.Message) == "" {
		return nil, invalid
	}
	if sensitiveForm.Match(raw) || sensitiveForm.MatchString(request.Message) {
		return nil, errors.New("The MCP form asks for private data. No answers were sent.")
	}
	schema, err := mcp.ValidateElicitSchema(request.RequestedSchema)
	if err != nil || schema == nil || schema.Type != "object" {
		return nil, invalid
	}
	resolved, err := schema.Resolve(nil) // No external schema loader.
	if err != nil {
		return nil, invalid
	}
	f := &serverForm{form: captypes.Form{ConnectionID: connectionID, Tool: tool, Message: request.Message}, schema: resolved}
	for _, name := range slices.Sorted(maps.Keys(schema.Properties)) {
		p := schema.Properties[name]
		if p == nil || p.WriteOnly || name == "" || p.Ref != "" {
			return nil, invalid
		}
		if strings.EqualFold(name, "token") || strings.EqualFold(name, "pin") {
			return nil, errors.New("The MCP form asks for private data. No answers were sent.")
		}
		field := captypes.FormField{Name: name, Type: p.Type, Title: p.Title, Description: p.Description, Required: slices.Contains(schema.Required, name)}
		choices := slices.Clone(p.Enum)
		titled := p.OneOf
		switch p.Type {
		case "boolean":
			field.Choices = []string{"false", "true"}
		case "array":
			if p.Items == nil {
				return nil, invalid
			}
			choices, titled = slices.Clone(p.Items.Enum), p.Items.AnyOf
		case "string", "number", "integer":
		default:
			return nil, invalid
		}
		var labels strings.Builder
		for _, item := range titled {
			if item == nil || item.Const == nil {
				return nil, invalid
			}
			choices = append(choices, *item.Const)
			fmt.Fprintf(&labels, "\n%q: %q", *item.Const, item.Title)
		}
		field.Description += labels.String()
		for _, c := range choices {
			v, ok := c.(string)
			if !ok {
				return nil, invalid
			}
			field.Choices = append(field.Choices, v)
		}
		f.form.Fields = append(f.form.Fields, field)
	}
	return f, nil
}

func (f *serverForm) answer(reply captypes.FormReply) (*mcp.ElicitResult, error) {
	out := &mcp.ElicitResult{Action: reply.Action}
	switch reply.Action {
	case "cancel", "decline":
		return out, nil
	case "accept":
	default:
		return nil, errors.New("No human form answer was supplied.")
	}
	out.Content = make(map[string]any)
	for _, field := range f.form.Fields {
		raw, ok := reply.Values[field.Name]
		if !ok {
			continue // Never fill omitted answers from server-provided defaults.
		}
		var v any = raw
		var err error
		switch field.Type {
		case "string":
			if field.JSONChoiceInput() {
				var text *string
				err = json.Unmarshal([]byte(raw), &text)
				if text == nil {
					err = errors.New("missing string")
				} else {
					v = *text
				}
			}
		case "boolean":
			if raw != "true" && raw != "false" {
				err = errors.New("invalid boolean")
			}
			v = raw == "true"
		case "number":
			v, err = strconv.ParseFloat(raw, 64)
		case "integer":
			v, err = strconv.ParseInt(raw, 10, 64)
		case "array":
			if field.JSONChoiceInput() {
				var entries []*string
				err = json.Unmarshal([]byte(raw), &entries)
				if entries == nil {
					err = errors.New("missing list")
				}
				values := make([]string, 0, len(entries))
				for _, entry := range entries {
					if entry == nil {
						err = errors.New("missing string")
						break
					}
					values = append(values, *entry)
				}
				v = values
			} else {
				v = strings.Split(raw, ", ")
			}
		}
		if err != nil {
			return nil, fmt.Errorf("The answer for %q is not a %s. No answers were sent.", field.Name, field.Type)
		}
		out.Content[field.Name] = v
	}
	if len(reply.Values) != len(out.Content) || f.schema.Validate(out.Content) != nil {
		return nil, errors.New("The answers do not match the MCP form. No answers were sent.")
	}
	return out, nil
}
