package control

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func number(v uint64) *uint64 { return &v }

func fixture() (Descriptor, Request, Current) {
	d := Descriptor{ID: "tree.set_visible", Scope: Shell, Effect: Write,
		Input:        Schema{ID: "tree.input", Revision: 1, Document: json.RawMessage(`{"type":"object"}`)},
		Output:       Schema{ID: "tree.output", Revision: 1, Document: json.RawMessage(`true`)},
		Requires:     Requirements{Resource: true, Revision: true, OpenEpoch: true},
		Availability: Availability{Available: true}}
	r := Request{ID: "request-1", CommandID: "tree.set_visible", Caller: Caller{Kind: AgentCaller, AgentID: "agent-1", RunID: "run-1"},
		Target:        Target{WorkspaceID: "workspace-1", ExecutionID: "execution-1", SessionID: "session-1", ResourceID: "tree-1", Shell: &ShellOwner{ClientID: "shell-1", Epoch: 7}},
		Preconditions: Preconditions{Revision: number(0), OpenEpoch: number(3)}, Input: json.RawMessage(`{"visible":false}`)}
	return d, r, Current{Target: r.Target.Clone(), Preconditions: r.Preconditions.Clone()}
}

func TestExactTargetsAndPreconditions(t *testing.T) {
	d, r, c := fixture()
	if f := r.ValidateAgainst(d, c); f != nil {
		t.Fatal(f)
	}
	tests := []struct {
		name   string
		change func(*Descriptor, *Request, *Current)
		code   FaultCode
		field  string
	}{
		{"missing workspace", func(_ *Descriptor, r *Request, _ *Current) { r.Target.WorkspaceID = "" }, Invalid, "workspace_id"},
		{"foreign workspace", func(_ *Descriptor, _ *Request, c *Current) { c.Target.WorkspaceID = "workspace-2" }, Unavailable, "workspace_id"},
		{"foreign execution", func(_ *Descriptor, _ *Request, c *Current) { c.Target.ExecutionID = "execution-2" }, Unavailable, "execution_id"},
		{"foreign session", func(_ *Descriptor, _ *Request, c *Current) { c.Target.SessionID = "session-2" }, Unavailable, "session_id"},
		{"foreign resource", func(_ *Descriptor, _ *Request, c *Current) { c.Target.ResourceID = "tree-2" }, Unavailable, "resource_id"},
		{"missing shell", func(_ *Descriptor, r *Request, _ *Current) { r.Target.Shell = nil }, Invalid, "shell"},
		{"missing client", func(_ *Descriptor, r *Request, _ *Current) { r.Target.Shell.ClientID = "" }, Invalid, "shell.client_id"},
		{"zero epoch", func(_ *Descriptor, r *Request, _ *Current) { r.Target.Shell.Epoch = 0 }, Invalid, "shell.epoch"},
		{"foreign shell", func(_ *Descriptor, _ *Request, c *Current) { c.Target.Shell.ClientID = "shell-2" }, Unavailable, "shell.client_id"},
		{"stale shell", func(_ *Descriptor, _ *Request, c *Current) { c.Target.Shell.Epoch = 8 }, Stale, "shell.epoch"},
		{"detached shell", func(_ *Descriptor, _ *Request, c *Current) { c.Target.Shell = nil }, Unavailable, "shell"},
		{"missing resource", func(_ *Descriptor, r *Request, _ *Current) { r.Target.ResourceID = "" }, Invalid, "resource_id"},
		{"required execution", func(d *Descriptor, r *Request, _ *Current) { d.Requires.Execution = true; r.Target.ExecutionID = "" }, Invalid, "execution_id"},
		{"required session", func(d *Descriptor, r *Request, _ *Current) { d.Requires.Session = true; r.Target.SessionID = "" }, Invalid, "session_id"},
		{"missing revision", func(_ *Descriptor, r *Request, _ *Current) { r.Preconditions.Revision = nil }, Invalid, "revision"},
		{"changed revision", func(_ *Descriptor, _ *Request, c *Current) { c.Preconditions.Revision = number(1) }, Stale, "revision"},
		{"unavailable revision", func(_ *Descriptor, _ *Request, c *Current) { c.Preconditions.Revision = nil }, Unavailable, "revision"},
		{"missing open epoch", func(_ *Descriptor, r *Request, _ *Current) { r.Preconditions.OpenEpoch = nil }, Invalid, "open_epoch"},
		{"zero open epoch", func(_ *Descriptor, r *Request, _ *Current) { r.Preconditions.OpenEpoch = number(0) }, Invalid, "open_epoch"},
		{"stale open epoch", func(_ *Descriptor, _ *Request, c *Current) { c.Preconditions.OpenEpoch = number(4) }, Stale, "open_epoch"},
		{"missing caller run", func(_ *Descriptor, r *Request, _ *Current) { r.Caller.RunID = "" }, Invalid, "caller.run_id"},
		{"different command", func(_ *Descriptor, r *Request, _ *Current) { r.CommandID = "terminal.input" }, Invalid, "command_id"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			d, r, c := fixture()
			test.change(&d, &r, &c)
			f := r.ValidateAgainst(d, c)
			if f == nil || f.Code != test.code || f.Field != test.field {
				t.Fatalf("got %#v, want %s on %s", f, test.code, test.field)
			}
		})
	}
	// Domain reads remain headless; optional fences are still checked when sent.
	d.Scope = Domain
	r.Target.Shell = nil
	c.Target.Shell = nil
	if f := r.ValidateAgainst(d, c); f != nil {
		t.Fatal(f)
	}
	d.Requires.Revision = false
	c.Preconditions.Revision = number(10)
	if f := r.ValidateAgainst(d, c); f == nil || f.Code != Stale {
		t.Fatalf("optional supplied fence ignored: %#v", f)
	}
}

func TestExplicitEffectsAndMetadata(t *testing.T) {
	d, _, _ := fixture()
	for _, effect := range []Effect{"read", "write", "delete", "execute", "communication", "authority"} {
		d.Effect = effect
		if f := d.Validate(); f != nil {
			t.Fatal(f)
		}
	}
	for _, effect := range []Effect{"", "generic", "Read"} {
		d.Effect = effect
		if f := d.Validate(); f == nil || f.Field != "effect" {
			t.Fatalf("unresolved effect accepted: %q", effect)
		}
	}
	d, _, _ = fixture()
	d.HumanOnly = true
	d.Availability = Availability{Reason: HumanRequired, Message: "Human approval only"}
	if f := d.Validate(); f != nil {
		t.Fatal(f)
	}
	var decoded Descriptor
	data, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.HumanOnly || decoded.Effect != Write || decoded.Availability.Reason != HumanRequired {
		t.Fatalf("classification lost: %#v", decoded)
	}
	for _, c := range []Caller{{}, {Kind: AgentCaller, AgentID: "a"}, {Kind: ExternalCaller}, {Kind: ExternalCaller, ClientID: "c", RunID: "r"}} {
		if f := c.Validate(); f == nil {
			t.Fatalf("invalid caller accepted: %#v", c)
		}
	}
	if f := (Caller{Kind: ExternalCaller, ClientID: "external-1"}).Validate(); f != nil {
		t.Fatal(f)
	}
}

func TestBoundedSchemasIDsAndPages(t *testing.T) {
	d, r, _ := fixture()
	r.ID = strings.Repeat("x", 65537)
	if f := r.Validate(d); f == nil {
		t.Fatal("oversized request identity accepted")
	}
	r.ID = "bad\nidentity"
	if f := r.Validate(d); f == nil {
		t.Fatal("control character accepted")
	}
	for _, document := range []string{`[]`, `null`, `"schema"`, `{`, `{"x":"` + strings.Repeat("x", 65536) + `"}`} {
		d.Input.Document = json.RawMessage(document)
		if f := d.Validate(); f == nil {
			t.Fatal("invalid/oversized schema accepted")
		}
	}
	for _, limit := range []int{-1, 0, 129} {
		if f := (PageRequest{Limit: limit}).Validate(); f == nil {
			t.Fatalf("invalid limit accepted: %d", limit)
		}
	}
	for _, limit := range []int{1, 128} {
		if f := (PageRequest{Limit: limit}).Validate(); f != nil {
			t.Fatal(f)
		}
	}
	n := Node{ID: "row-1", Role: Item, Name: "Offscreen row", Target: Target{WorkspaceID: "w"}, OpenEpoch: 2}
	p := Page{Nodes: []Node{n}, NextCursor: "next"}
	if f := p.Validate(PageRequest{Limit: 1}); f != nil {
		t.Fatal(f)
	}
	if f := p.Validate(PageRequest{Limit: 1, Cursor: "next"}); f == nil {
		t.Fatal("nonadvancing cursor accepted")
	}
	if f := (Page{NextCursor: "next"}).Validate(PageRequest{Limit: 1}); f == nil {
		t.Fatal("empty continuation accepted")
	}
	p.Nodes = append(p.Nodes, n)
	if f := p.Validate(PageRequest{Limit: 1}); f == nil {
		t.Fatal("oversized page accepted")
	}
	if f := p.Validate(PageRequest{Limit: 2}); f == nil {
		t.Fatal("duplicate node accepted")
	}
	n.Bounds = &Bounds{X: -1}
	if f := n.Validate(); f == nil {
		t.Fatal("negative bounds accepted")
	}
}

func TestRoundtripAndDetachedClones(t *testing.T) {
	d, r, _ := fixture()
	request := r.Clone()
	request.Target.Shell.Epoch = 99
	*request.Preconditions.Revision = 9
	*request.Preconditions.OpenEpoch = 8
	request.Input[0] = '['
	if r.Target.Shell.Epoch != 7 || *r.Preconditions.Revision != 0 || *r.Preconditions.OpenEpoch != 3 || string(r.Input) != `{"visible":false}` {
		t.Fatal("request clone aliases source")
	}
	n := Node{ID: "tree-1", Role: Tree, Name: "Files", Value: json.RawMessage(`"filter"`), Target: r.Target, Revision: 0, OpenEpoch: 3,
		State: NodeState{Enabled: true, Visible: true}, Actions: []Descriptor{d}, Bounds: &Bounds{X: 1, Y: 2, Width: 20, Height: 10}}
	p := Page{Nodes: []Node{n}, NextCursor: "next"}
	copy := p.Clone()
	copy.Nodes[0].Target.Shell.Epoch = 20
	copy.Nodes[0].Bounds.Width = 99
	copy.Nodes[0].Value[1] = 'X'
	copy.Nodes[0].Actions[0].Input.Document[0] = '['
	copy.Nodes[0].Actions[0].Output.Document[0] = 'X'
	if p.Nodes[0].Target.Shell.Epoch != 7 || p.Nodes[0].Bounds.Width != 20 || string(n.Value) != `"filter"` || string(d.Input.Document) != `{"type":"object"}` || string(d.Output.Document) != `true` {
		t.Fatal("nested page clone aliases source")
	}
	result := Result{RequestID: "request-1", OperationID: "operation-1", State: Partial, ResourceIDs: []string{"terminal-1"}, Revision: number(4), FrameRevision: number(5), Output: json.RawMessage(`{"mounted":false}`), Fault: &Fault{Code: Unavailable, Message: "Shell detached"}}
	rc := result.Clone()
	rc.ResourceIDs[0] = "other"
	*rc.Revision = 20
	*rc.FrameRevision = 21
	rc.Output[0] = '['
	rc.Fault.Message = "other"
	if result.ResourceIDs[0] != "terminal-1" || *result.Revision != 4 || *result.FrameRevision != 5 || string(result.Output) != `{"mounted":false}` || result.Fault.Message != "Shell detached" {
		t.Fatal("result clone aliases source")
	}
	for _, pair := range []struct {
		source      any
		destination any
	}{{r, &Request{}}, {d, &Descriptor{}}, {p, &Page{}}, {result, &Result{}}} {
		data, err := json.Marshal(pair.source)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, pair.destination); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(pair.source, reflect.ValueOf(pair.destination).Elem().Interface()) {
			t.Fatalf("roundtrip changed %T", pair.source)
		}
	}
	if f := result.Validate(); f != nil {
		t.Fatal(f)
	}
}

func TestResultStatesFailClosed(t *testing.T) {
	for _, state := range []ResultState{"admitted", "committed", "applied", "pending_decision", "partial", "failed", "uncertain"} {
		r := Result{RequestID: "r", OperationID: "o", State: state}
		if state == "partial" || state == "failed" || state == "uncertain" {
			if f := r.Validate(); f == nil {
				t.Fatalf("%s accepted without fault", state)
			}
			r.Fault = &Fault{Code: UnknownOutcome, Message: "Outcome needs reconciliation"}
		}
		if f := r.Validate(); f != nil {
			t.Fatal(f)
		}
	}
	if f := (Result{RequestID: "r", OperationID: "o", State: "success"}).Validate(); f == nil {
		t.Fatal("unknown state accepted")
	}
}
