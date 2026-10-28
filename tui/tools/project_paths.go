package tools

import (
	"strings"

	"crude/types"
)

// ProjectPaths formats title operands against the workspace root supplied by
// the core. It is lexical and performs no filesystem access: a shell can be on
// a different OS from the workspace, and path display is not an access check.
type ProjectPaths struct {
	root    string
	windows bool
	unc     bool
	parts   []string
}

// NewProjectPaths prepares a project root once per renderer. An unavailable,
// relative or ambiguous root disables shortening rather than guessing.
func NewProjectPaths(root string) *ProjectPaths {
	drive := len(root) >= 3 && isDriveLetter(root[0]) && root[1] == ':' && isWindowsSeparator(root[2])
	unc := len(root) >= 2 && isWindowsSeparator(root[0]) && isWindowsSeparator(root[1])
	if drive || unc {
		start := 0
		if unc {
			start = 2
		}
		parts := strings.FieldsFunc(root[start:], func(r rune) bool { return r == '/' || r == '\\' })
		if len(parts) == 0 || unc && len(parts) < 2 || hasDotPart(parts) {
			return nil
		}
		return &ProjectPaths{root: root, windows: true, unc: unc, parts: parts}
	}
	if !strings.HasPrefix(root, "/") || hasParentPart(root, false) {
		return nil
	}
	root = strings.TrimRight(root, "/")
	if root == "" {
		root = "/"
	}
	return &ProjectPaths{root: root}
}

func isDriveLetter(c byte) bool      { return c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' }
func isWindowsSeparator(c byte) bool { return c == '/' || c == '\\' }

func hasDotPart(parts []string) bool {
	for _, part := range parts {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}

func hasParentPart(value string, windows bool) bool {
	start := 0
	for i := 0; i <= len(value); i++ {
		if i == len(value) || value[i] == '/' || windows && value[i] == '\\' {
			if value[start:i] == ".." {
				return true
			}
			start = i + 1
		}
	}
	return false
}

// Relative shortens only absolute paths lexically inside the project, retaining
// their original suffix and separators. Uncertain, external and parent-traversal
// paths remain exact; no symlink resolution or filesystem I/O is attempted.
func (p *ProjectPaths) Relative(value string) string {
	if p == nil || value == "" {
		return value
	}
	if !p.windows {
		original := value
		if p.root == "/" {
			if !strings.HasPrefix(value, "/") {
				return value
			}
			value = strings.TrimLeft(value, "/")
		} else {
			if !strings.HasPrefix(value, p.root) || len(value) > len(p.root) && value[len(p.root)] != '/' {
				return value
			}
			value = strings.TrimLeft(value[len(p.root):], "/")
		}
		if hasParentPart(value, false) {
			return original
		}
		if value == "" {
			return "."
		}
		return value
	}

	i := 0
	if p.unc {
		if len(value) < 2 || !isWindowsSeparator(value[0]) || !isWindowsSeparator(value[1]) {
			return value
		}
		i = 2
	} else if len(value) < 3 || !isDriveLetter(value[0]) || value[1] != ':' || !isWindowsSeparator(value[2]) {
		return value
	}
	for _, part := range p.parts {
		for i < len(value) && isWindowsSeparator(value[i]) {
			i++
		}
		start := i
		for i < len(value) && !isWindowsSeparator(value[i]) {
			i++
		}
		if start == i || !strings.EqualFold(part, value[start:i]) {
			return value
		}
	}
	for i < len(value) && isWindowsSeparator(value[i]) {
		i++
	}
	if hasParentPart(value[i:], true) {
		return value
	}
	if i == len(value) {
		return "."
	}
	return value[i:]
}

// ToolOperand applies path formatting only where the tool schema identifies a
// path. Commands, queries, result bodies and other arbitrary text are literal.
func (p *ProjectPaths) ToolOperand(msg *types.ChatMessage, operand string) string {
	if p == nil || msg == nil || operand == "" {
		return operand
	}
	switch msg.ToolName {
	case "read_file", "write_file", "edit_file", "delete_file", "list_directory":
		return p.Relative(operand)
	case "grep_files":
		if msg.ToolInput != "" && msg.ToolScope != "" && operand == `"`+msg.ToolInput+`" in `+msg.ToolScope {
			return `"` + msg.ToolInput + `" in ` + p.Relative(msg.ToolScope)
		}
		if msg.ToolScope != "" && operand == msg.ToolScope {
			return p.Relative(msg.ToolScope)
		}
	case "move_file", "copy_file":
		source, dest, found := strings.Cut(operand, " → ")
		if !found {
			return p.Relative(operand)
		}
		if !strings.Contains(dest, " → ") {
			return p.Relative(source) + " → " + p.Relative(dest)
		}
	case "apply_patch":
		if !strings.Contains(operand, ", ") {
			return p.Relative(operand)
		}
		paths := strings.Split(operand, ", ")
		for i := range paths {
			paths[i] = p.Relative(paths[i])
		}
		return strings.Join(paths, ", ")
	case "lsp":
		// The semantic operand is operation [· symbol] · path.
		if index := strings.LastIndex(operand, " · "); index >= 0 {
			return operand[:index+len(" · ")] + p.Relative(operand[index+len(" · "):])
		}
		// An LSP streaming preview publishes its path separately from the
		// eventual operation/symbol summary. A bare summary might instead be
		// a symbol, so do not reinterpret it as a path.
		if operand == msg.ToolProgressLabel && operand != msg.ToolArgs {
			return p.Relative(operand)
		}
	}
	return operand
}
