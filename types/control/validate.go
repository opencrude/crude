package control

import (
	"encoding/json"
	"strings"
	"unicode"
	"unicode/utf8"
)

func invalid(field, message string) *Fault {
	return &Fault{Code: Invalid, Field: field, Message: message}
}

func id(field, value string, required bool) *Fault {
	if value == "" && !required {
		return nil
	}
	if strings.TrimSpace(value) == "" || len(value) > MaxIDBytes || !utf8.ValidString(value) || strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return invalid(field, "identity must be nonblank, valid UTF-8, bounded and free of control characters")
	}
	return nil
}

func jsonValue(field string, value json.RawMessage, limit int, optional bool) *Fault {
	if len(value) == 0 && optional {
		return nil
	}
	if len(value) > limit || !json.Valid(value) {
		return invalid(field, "JSON is missing, invalid or oversized")
	}
	return nil
}

func (s ShellOwner) Validate() *Fault {
	if f := id("shell.client_id", s.ClientID, true); f != nil {
		return f
	}
	if s.Epoch == 0 {
		return invalid("shell.epoch", "attachment epoch must be positive")
	}
	return nil
}

func (t Target) Validate() *Fault {
	for _, v := range []struct {
		field, value string
		required     bool
	}{
		{"workspace_id", t.WorkspaceID, true}, {"execution_id", t.ExecutionID, false},
		{"session_id", t.SessionID, false}, {"resource_id", t.ResourceID, false},
	} {
		if f := id(v.field, v.value, v.required); f != nil {
			return f
		}
	}
	if t.Shell != nil {
		return t.Shell.Validate()
	}
	return nil
}

func (c Caller) Validate() *Fault {
	switch c.Kind {
	case AgentCaller:
		if c.ClientID != "" {
			return invalid("caller.client_id", "agent attribution cannot also be external-client attribution")
		}
		if f := id("caller.agent_id", c.AgentID, true); f != nil {
			return f
		}
		return id("caller.run_id", c.RunID, true)
	case ExternalCaller:
		if c.AgentID != "" || c.RunID != "" {
			return invalid("caller", "external-client attribution cannot also be agent attribution")
		}
		return id("caller.client_id", c.ClientID, true)
	default:
		return invalid("caller.kind", "unknown caller kind")
	}
}

func (s Schema) Validate() *Fault {
	if f := id("schema.id", s.ID, true); f != nil {
		return f
	}
	if s.Revision == 0 {
		return invalid("schema.revision", "schema revision must be positive")
	}
	if f := jsonValue("schema.document", s.Document, MaxSchemaBytes, false); f != nil {
		return f
	}
	// JSON Schema permits an object or boolean, never arrays/strings/numbers.
	v := strings.TrimSpace(string(s.Document))
	if v[0] != '{' && v != "true" && v != "false" {
		return invalid("schema.document", "schema must be an object or boolean")
	}
	return nil
}

func (a Availability) Validate() *Fault {
	if len(a.Message) > MaxValueBytes || !utf8.ValidString(a.Message) {
		return invalid("availability.message", "invalid or oversized message")
	}
	if a.Available {
		if a.Reason != "" {
			return invalid("availability.reason", "available command cannot have an unavailable reason")
		}
		return nil
	}
	switch a.Reason {
	case Unsupported, TargetUnavailable, HumanRequired, PolicyDenied, PreconditionFailed:
		return nil
	default:
		return invalid("availability.reason", "unavailable command needs a known reason")
	}
}

func (d Descriptor) Validate() *Fault {
	if f := id("command_id", d.ID, true); f != nil {
		return f
	}
	if d.Scope != Domain && d.Scope != Shell {
		return invalid("scope", "unknown scope")
	}
	switch d.Effect {
	case Read, Write, Delete, Execute, Communication, Authority:
	default:
		return invalid("effect", "actual effect must be explicitly resolved")
	}
	if f := d.Input.Validate(); f != nil {
		return f
	}
	if f := d.Output.Validate(); f != nil {
		return f
	}
	return d.Availability.Validate()
}

// Validate checks request structure and descriptor requirements, not authority,
// input conformance to JSON Schema, existence, or freshness. HumanOnly is retained
// for admission policy; an external client is not automatically a human.
func (r Request) Validate(d Descriptor) *Fault {
	if f := d.Validate(); f != nil {
		return f
	}
	if f := id("request_id", r.ID, true); f != nil {
		return f
	}
	if r.CommandID != d.ID {
		return invalid("command_id", "request does not name the resolved descriptor")
	}
	if f := r.Caller.Validate(); f != nil {
		return f
	}
	if f := r.Target.Validate(); f != nil {
		return f
	}
	if d.Scope == Shell && r.Target.Shell == nil {
		return invalid("shell", "shell-scoped command requires an exact attachment")
	}
	for _, v := range []struct {
		field, value string
		required     bool
	}{
		{"execution_id", r.Target.ExecutionID, d.Requires.Execution},
		{"session_id", r.Target.SessionID, d.Requires.Session},
		{"resource_id", r.Target.ResourceID, d.Requires.Resource},
	} {
		if f := id(v.field, v.value, v.required); f != nil {
			return f
		}
	}
	if d.Requires.Revision && r.Preconditions.Revision == nil {
		return invalid("revision", "required revision is missing")
	}
	if d.Requires.OpenEpoch && r.Preconditions.OpenEpoch == nil {
		return invalid("open_epoch", "required open epoch is missing")
	}
	if r.Preconditions.OpenEpoch != nil && *r.Preconditions.OpenEpoch == 0 {
		return invalid("open_epoch", "open epoch must be positive")
	}
	return jsonValue("input", r.Input, MaxValueBytes, false)
}

// ValidateAgainst checks every supplied identity and fence against a trusted
// owner observation. Owners must repeat this immediately before applying effects;
// this function alone provides no atomicity, permission or receipt persistence.
func (r Request) ValidateAgainst(d Descriptor, current Current) *Fault {
	if f := r.Validate(d); f != nil {
		return f
	}
	if f := current.Target.Validate(); f != nil {
		return &Fault{Code: Unavailable, Field: f.Field, Message: "current target is unavailable"}
	}
	for _, v := range []struct{ field, want, got string }{
		{"workspace_id", r.Target.WorkspaceID, current.Target.WorkspaceID},
		{"execution_id", r.Target.ExecutionID, current.Target.ExecutionID},
		{"session_id", r.Target.SessionID, current.Target.SessionID},
		{"resource_id", r.Target.ResourceID, current.Target.ResourceID},
	} {
		if v.want != "" && v.want != v.got {
			return &Fault{Code: Unavailable, Field: v.field, Message: "exact target does not match current owner"}
		}
	}
	if r.Target.Shell != nil {
		if current.Target.Shell == nil {
			return &Fault{Code: Unavailable, Field: "shell", Message: "shell is detached or missing"}
		}
		if r.Target.Shell.ClientID != current.Target.Shell.ClientID {
			return &Fault{Code: Unavailable, Field: "shell.client_id", Message: "foreign shell client"}
		}
		if r.Target.Shell.Epoch != current.Target.Shell.Epoch {
			return &Fault{Code: Stale, Field: "shell.epoch", Message: "stale shell attachment"}
		}
	}
	for _, v := range []struct {
		field     string
		want, got *uint64
	}{
		{"revision", r.Preconditions.Revision, current.Preconditions.Revision},
		{"open_epoch", r.Preconditions.OpenEpoch, current.Preconditions.OpenEpoch},
	} {
		if v.want == nil {
			continue
		}
		if v.got == nil {
			return &Fault{Code: Unavailable, Field: v.field, Message: "current precondition is unavailable"}
		}
		if *v.want != *v.got {
			return &Fault{Code: Stale, Field: v.field, Message: "precondition changed"}
		}
	}
	if !d.Availability.Available {
		return &Fault{Code: Unavailable, Field: "availability", Message: string(d.Availability.Reason)}
	}
	return nil
}

func (f Fault) Validate() *Fault {
	switch f.Code {
	case Invalid, Unavailable, Stale, Denied, OperationFailed, UnknownOutcome:
	default:
		return invalid("fault.code", "unknown fault code")
	}
	if e := id("fault.field", f.Field, false); e != nil {
		return e
	}
	if strings.TrimSpace(f.Message) == "" || len(f.Message) > MaxValueBytes || !utf8.ValidString(f.Message) {
		return invalid("fault.message", "message must be nonblank, valid and bounded")
	}
	return nil
}

func (r Result) Validate() *Fault {
	if f := id("request_id", r.RequestID, true); f != nil {
		return f
	}
	if f := id("operation_id", r.OperationID, true); f != nil {
		return f
	}
	switch r.State {
	case Admitted, Committed, Applied, PendingDecision:
	case Partial, Failed, Uncertain:
		if r.Fault == nil {
			return invalid("fault", "non-success outcome requires an explicit fault")
		}
	default:
		return invalid("state", "unknown result state")
	}
	if len(r.ResourceIDs) > MaxPageLimit {
		return invalid("resource_ids", "too many resource identities")
	}
	for _, value := range r.ResourceIDs {
		if f := id("resource_id", value, true); f != nil {
			return f
		}
	}
	if r.Fault != nil {
		if f := r.Fault.Validate(); f != nil {
			return f
		}
	}
	return jsonValue("output", r.Output, MaxValueBytes, true)
}

func (n Node) Validate() *Fault {
	if f := id("node.id", n.ID, true); f != nil {
		return f
	}
	if f := id("node.parent_id", n.ParentID, false); f != nil {
		return f
	}
	if n.ParentID == n.ID {
		return invalid("node.parent_id", "node cannot parent itself")
	}
	if f := id("node.name", n.Name, true); f != nil {
		return f
	}
	switch n.Role {
	case Container, Button, Field, List, Tree, Item, Tab, Window, Text, Scrollbar, Divider:
	default:
		return invalid("node.role", "unknown role")
	}
	if n.OpenEpoch == 0 {
		return invalid("node.open_epoch", "node lifetime epoch must be positive")
	}
	if f := n.Target.Validate(); f != nil {
		return f
	}
	if n.Bounds != nil && (n.Bounds.X < 0 || n.Bounds.Y < 0 || n.Bounds.Width < 0 || n.Bounds.Height < 0) {
		return invalid("node.bounds", "terminal bounds cannot be negative")
	}
	if len(n.Actions) > MaxPageLimit {
		return invalid("node.actions", "too many actions")
	}
	seen := make(map[string]bool, len(n.Actions))
	for _, d := range n.Actions {
		if f := d.Validate(); f != nil {
			return f
		}
		if seen[d.ID] {
			return invalid("node.actions", "duplicate command identity")
		}
		seen[d.ID] = true
		if d.Scope == Shell && n.Target.Shell == nil {
			return invalid("node.target.shell", "shell action needs exact node attachment")
		}
	}
	return jsonValue("node.value", n.Value, MaxValueBytes, true)
}

func (p PageRequest) Validate() *Fault {
	if p.Limit < 1 || p.Limit > MaxPageLimit {
		return invalid("limit", "page limit must be between 1 and 128")
	}
	return id("cursor", p.Cursor, false)
}

func (p Page) Validate(request PageRequest) *Fault {
	if f := request.Validate(); f != nil {
		return f
	}
	if len(p.Nodes) > request.Limit {
		return invalid("nodes", "page exceeds requested limit")
	}
	if f := id("next_cursor", p.NextCursor, false); f != nil {
		return f
	}
	if p.NextCursor != "" && (p.NextCursor == request.Cursor || len(p.Nodes) == 0) {
		return invalid("next_cursor", "continuation must advance with a nonempty page")
	}
	seen := make(map[string]bool, len(p.Nodes))
	for _, n := range p.Nodes {
		if f := n.Validate(); f != nil {
			return f
		}
		if seen[n.ID] {
			return invalid("nodes", "duplicate node identity")
		}
		seen[n.ID] = true
	}
	return nil
}
