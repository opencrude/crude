package capability

import (
	"context"
	"strings"
	"unicode"
)

// Form describes one request for user data. The connection and tool names
// come from Crude's admitted call, not from the requesting server.
type Form struct {
	ConnectionID, Tool, Message string
	Fields                      []FormField
}

// FormField describes one flat MCP form input. Choices contain the values
// that will be shared, rather than server-supplied aliases for those values.
type FormField struct {
	Name, Type, Title, Description string
	Choices                        []string
	Required                       bool
}

// JSONChoiceInput reports whether the question's choice list would lose a
// value. JSON preserves delimiters, whitespace, and the empty string exactly.
func (f FormField) JSONChoiceInput() bool {
	if len(f.Choices) > 5 {
		return true
	}
	for _, v := range f.Choices {
		if v == "" || strings.TrimSpace(v) != v || strings.IndexFunc(v, unicode.IsControl) >= 0 ||
			strings.Contains(v, ", ") && f.Type == "array" {
			return true
		}
	}
	return false
}

// FormReply contains only values the human chose to send in this form.
type FormReply struct {
	Action string
	Values map[string]string
}

type formOwner struct {
	name  string
	ask   func(context.Context, Form) FormReply
	check func(context.Context, string) error
}

type formKey struct{}

// WithFormHandler binds a human question lane to one named tool invocation.
// Do not attach it to a run, connection, job or transport lifetime.
func WithFormHandler(ctx context.Context, tool string, ask func(context.Context, Form) FormReply) context.Context {
	return context.WithValue(ctx, formKey{}, formOwner{name: tool, ask: ask})
}

// WithFormGuard adds a live authority owner to the originating call. Guards
// run again after human input, without replaying tool execution for admission.
func WithFormGuard(ctx context.Context, guard func(context.Context, string) error) context.Context {
	owner, _ := ctx.Value(formKey{}).(formOwner)
	if owner.ask == nil {
		return ctx
	}
	previous := owner.check
	owner.check = func(ctx context.Context, id string) error {
		if previous != nil {
			if err := previous(ctx, id); err != nil {
				return err
			}
		}
		return guard(ctx, id)
	}
	return context.WithValue(ctx, formKey{}, owner)
}

// FormHandler returns the user and authority callbacks for this exact tool.
func FormHandler(ctx context.Context, tool string) (func(context.Context, Form) FormReply, func(context.Context, string) error) {
	owner, _ := ctx.Value(formKey{}).(formOwner)
	if owner.name != tool {
		return nil, nil
	}
	return owner.ask, owner.check
}
