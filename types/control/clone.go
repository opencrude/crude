package control

import "bytes"

func cloneUint(value *uint64) *uint64 {
	if value == nil {
		return nil
	}
	v := *value
	return &v
}

func (t Target) Clone() Target {
	if t.Shell != nil {
		owner := *t.Shell
		t.Shell = &owner
	}
	return t
}

func (p Preconditions) Clone() Preconditions {
	p.Revision = cloneUint(p.Revision)
	p.OpenEpoch = cloneUint(p.OpenEpoch)
	return p
}

func (d Descriptor) Clone() Descriptor {
	d.Input.Document = bytes.Clone(d.Input.Document)
	d.Output.Document = bytes.Clone(d.Output.Document)
	return d
}

func (r Request) Clone() Request {
	r.Target = r.Target.Clone()
	r.Preconditions = r.Preconditions.Clone()
	r.Input = bytes.Clone(r.Input)
	return r
}

func (r Result) Clone() Result {
	if r.ResourceIDs != nil {
		r.ResourceIDs = append([]string{}, r.ResourceIDs...)
	}
	r.Revision = cloneUint(r.Revision)
	r.FrameRevision = cloneUint(r.FrameRevision)
	r.Output = bytes.Clone(r.Output)
	if r.Fault != nil {
		f := *r.Fault
		r.Fault = &f
	}
	return r
}

func (n Node) Clone() Node {
	n.Value = bytes.Clone(n.Value)
	n.Target = n.Target.Clone()
	if n.Bounds != nil {
		b := *n.Bounds
		n.Bounds = &b
	}
	if n.Actions != nil {
		actions := make([]Descriptor, len(n.Actions))
		for i, action := range n.Actions {
			actions[i] = action.Clone()
		}
		n.Actions = actions
	}
	return n
}

func (p Page) Clone() Page {
	if p.Nodes != nil {
		nodes := make([]Node, len(p.Nodes))
		for i, node := range p.Nodes {
			nodes[i] = node.Clone()
		}
		p.Nodes = nodes
	}
	return p
}
