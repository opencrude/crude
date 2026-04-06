package dag

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Parse converts a JSON string (from LLM output) into a validated DAG.
// Accepts {"nodes": [...]} or [...] top-level format; strips markdown fences.
func Parse(jsonStr string) (*DAG, error) {
	jsonStr = stripFences(jsonStr)

	var nodes []*Node

	type nodesWrapper struct {
		Nodes []*Node `json:"nodes"`
	}
	var wrapper nodesWrapper
	if err := json.Unmarshal([]byte(jsonStr), &wrapper); err == nil && len(wrapper.Nodes) > 0 {
		nodes = wrapper.Nodes
	} else if err := json.Unmarshal([]byte(jsonStr), &nodes); err != nil {
		// Try repairing common JSON issues before giving up
		repaired := repairJSON(jsonStr)
		if repaired != "" {
			if err2 := json.Unmarshal([]byte(repaired), &wrapper); err2 == nil && len(wrapper.Nodes) > 0 {
				nodes = wrapper.Nodes
			} else if err2 := json.Unmarshal([]byte(repaired), &nodes); err2 != nil {
				return nil, fmt.Errorf("dag: parse: %w", err)
			}
		} else {
			return nil, fmt.Errorf("dag: parse: %w", err)
		}
	}

	if len(nodes) == 0 {
		return &DAG{
			Nodes:      make(map[string]*Node),
			Dependents: make(map[string][]string),
		}, nil
	}

	d := &DAG{
		Nodes:      make(map[string]*Node, len(nodes)),
		Dependents: make(map[string][]string, len(nodes)),
	}
	for _, n := range nodes {
		if n.ID == "" {
			return nil, fmt.Errorf("dag: node missing id")
		}
		if _, dup := d.Nodes[n.ID]; dup {
			return nil, fmt.Errorf("dag: duplicate node id: %q", n.ID)
		}
		n.Status = Pending
		d.Nodes[n.ID] = n
	}

	// Build reverse dependency index.
	for _, n := range d.Nodes {
		for _, dep := range n.DependsOn {
			d.Dependents[dep] = append(d.Dependents[dep], n.ID)
		}
	}

	if err := d.Validate(); err != nil {
		return nil, err
	}
	return d, nil
}

// repairJSON attempts to fix common JSON issues from LLM output.
// Returns repaired JSON or empty string if unrecoverable.
func repairJSON(s string) string {
	s = strings.TrimSpace(s)

	// Strategy 1: Find the largest valid JSON substring by scanning for balanced braces/brackets
	repaired := findLargestValidJSON(s)
	if repaired != "" {
		// Validate it actually parses
		var test any
		if json.Unmarshal([]byte(repaired), &test) == nil {
			return repaired
		}
	}

	// Strategy 2: Try to fix unescaped control characters in string values
	repaired = fixUnescapedChars(s)
	if repaired != "" {
		var test any
		if json.Unmarshal([]byte(repaired), &test) == nil {
			return repaired
		}
	}

	return ""
}

// findLargestValidJSON scans for the largest balanced JSON object/array.
func findLargestValidJSON(s string) string {
	// Try finding {...} blocks
	best := ""
	for i := 0; i < len(s); i++ {
		if s[i] == '{' || s[i] == '[' {
			open := s[i]
			close := byte('}')
			if s[i] == '[' {
				close = ']'
			}
			depth := 0
			inString := false
			escaped := false
			start := i
			for j := i; j < len(s); j++ {
				c := s[j]
				if escaped {
					escaped = false
					continue
				}
				if c == '\\' && inString {
					escaped = true
					continue
				}
				if c == '"' {
					inString = !inString
					continue
				}
				if inString {
					continue
				}
				if c == open {
					depth++
				} else if c == close {
					depth--
					if depth == 0 {
						candidate := s[start : j+1]
						if len(candidate) > len(best) {
							best = candidate
						}
						break
					}
				}
			}
		}
	}
	return best
}

// fixUnescapedChars attempts to fix unescaped newlines, tabs, and backslashes in JSON strings.
func fixUnescapedChars(s string) string {
	// Fix unescaped backslashes: replace \ that isn't part of a valid escape sequence
	// Valid escapes: \", \\, \/, \b, \f, \n, \r, \t, \uXXXX
	validEscape := regexp.MustCompile(`\\["\\/bfnrtu]`)

	// First normalize valid escapes to placeholders
	s = validEscape.ReplaceAllStringFunc(s, func(match string) string {
		return "\x00ESC" + match[1:]
	})

	// Now fix remaining bare backslashes
	s = strings.ReplaceAll(s, `\`, `\\`)

	// Restore valid escapes
	s = strings.ReplaceAll(s, "\x00ESC", `\`)

	return s
}

// Validate checks that all depends_on IDs exist and the graph has no cycles.
func (d *DAG) Validate() error {
	for id, n := range d.Nodes {
		for _, dep := range n.DependsOn {
			if _, ok := d.Nodes[dep]; !ok {
				return fmt.Errorf("dag: node %q depends on unknown node %q", id, dep)
			}
		}
	}
	return d.detectCycles()
}

// TopologicalOrder returns nodes in topological order (dependencies first).
func (d *DAG) TopologicalOrder() ([]*Node, error) {
	inDeg := make(map[string]int, len(d.Nodes))
	for id := range d.Nodes {
		inDeg[id] = 0
	}
	for _, n := range d.Nodes {
		inDeg[n.ID] = len(n.DependsOn)
	}

	var queue []string
	for id, deg := range inDeg {
		if deg == 0 {
			queue = append(queue, id)
		}
	}

	var order []*Node
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		order = append(order, d.Nodes[id])

		for _, depID := range d.Dependents[id] {
			inDeg[depID]--
			if inDeg[depID] == 0 {
				queue = append(queue, depID)
			}
		}
	}

	if len(order) != len(d.Nodes) {
		return nil, fmt.Errorf("dag: cycle detected during topological sort")
	}
	return order, nil
}

// ReadyNodes returns all nodes whose dependencies are all Successful.
func (d *DAG) ReadyNodes() []*Node {
	var ready []*Node
	for _, n := range d.Nodes {
		if n.Status != Pending {
			continue
		}
		allDepsSuccessful := true
		for _, dep := range n.DependsOn {
			if d.Nodes[dep].Status != Successful {
				allDepsSuccessful = false
				break
			}
		}
		if allDepsSuccessful {
			ready = append(ready, n)
		}
	}
	return ready
}

// SkipDependents marks all transitive dependents of the given node as Skipped.
func (d *DAG) SkipDependents(nodeID string) {
	var skip func(id string)
	skip = func(id string) {
		for _, depID := range d.Dependents[id] {
			dep := d.Nodes[depID]
			if dep.Status == Pending {
				dep.Status = Skipped
				dep.RunErr = fmt.Errorf("skipped: dependency %q failed", id)
				skip(depID)
			}
		}
	}
	skip(nodeID)
}

// Summary returns a human-readable summary of the DAG execution results.
func (d *DAG) Summary() string {
	var sb strings.Builder
	for _, n := range d.Nodes {
		switch n.Status {
		case Successful:
			// result := n.Result
			// if len(result) > 200 {
			// 	result = result[:200] + "…"
			// }
			fmt.Fprintf(&sb, "✓ %s (%s): %s\n", n.ID, n.Tool, n.Result)
		case Err:
			errMsg := ""
			if n.RunErr != nil {
				errMsg = n.RunErr.Error()
			}
			fmt.Fprintf(&sb, "✗ %s (%s): ERROR: %s\n", n.ID, n.Tool, errMsg)
		case Skipped:
			fmt.Fprintf(&sb, "- %s (%s): skipped\n", n.ID, n.Tool)
		default:
			fmt.Fprintf(&sb, "? %s (%s): %s\n", n.ID, n.Tool, n.Status)
		}
	}
	return sb.String()
}

// AuditLog returns audit entries for all executed nodes.
func (d *DAG) AuditLog() []AuditEntry {
	order, err := d.TopologicalOrder()
	if err != nil {
		// Fallback: unsorted.
		order = make([]*Node, 0, len(d.Nodes))
		for _, n := range d.Nodes {
			order = append(order, n)
		}
	}

	var entries []AuditEntry
	for _, n := range order {
		if n.Status == Pending {
			continue
		}
		entry := AuditEntry{
			NodeID:      n.ID,
			Operation:   n.Operation,
			Tool:        n.Tool,
			Description: n.Description,
			Parameters:  n.Parameters,
			StartTime:   n.Started,
			EndTime:     n.Finished,
			Duration:    n.Duration().String(),
			Status:      n.Status.String(),
			Result:      n.Result,
		}
		if n.RunErr != nil {
			entry.Error = n.RunErr.Error()
		}
		entries = append(entries, entry)
	}
	return entries
}

// detectCycles uses DFS with white/gray/black coloring.
func (d *DAG) detectCycles() error {
	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int, len(d.Nodes))

	var dfs func(id string) error
	dfs = func(id string) error {
		color[id] = gray
		for _, dep := range d.Nodes[id].DependsOn {
			switch color[dep] {
			case gray:
				return fmt.Errorf("dag: cycle detected involving node %q", dep)
			case white:
				if err := dfs(dep); err != nil {
					return err
				}
			}
		}
		color[id] = black
		return nil
	}

	for id := range d.Nodes {
		if color[id] == white {
			if err := dfs(id); err != nil {
				return err
			}
		}
	}
	return nil
}

// stripFences removes markdown code fences (```json ... ```) from a string.
func stripFences(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		lines := strings.SplitN(s, "\n", 2)
		if len(lines) == 2 {
			s = lines[1]
		}
		if idx := strings.LastIndex(s, "```"); idx >= 0 {
			s = s[:idx]
		}
		s = strings.TrimSpace(s)
	}
	return s
}
