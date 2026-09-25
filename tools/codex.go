package tools

import (
	"context"
	"fmt"
	"strings"

	"crude/codexio"
	"crude/diff"
	"crude/fileio"
)

func ApplyPatchTool() ToolDef {
	t := codexio.CodexTool()
	return ToolDef{Type: t.Type, Function: ToolFuncDef{Name: t.Name, Description: t.Description}, Format: &t.Format}
}

// ForModel replaces write/edit with the complete native patch tool. Its
// operations are not restricted by the availability of separate move/delete tools.
func ForModel(defs []ToolDef, model string) []ToolDef {
	if !codexio.IsGPT(model) {
		return defs
	}
	result := make([]ToolDef, 0, len(defs))
	hasPatch := false
	for _, def := range defs {
		if def.Function.Name == "apply_patch" {
			hasPatch = true
		}
	}
	for _, def := range defs {
		if def.Function.Name == "write_file" || def.Function.Name == "edit_file" {
			if !hasPatch {
				patch := ApplyPatchTool()
				result = append(result, patch)
				hasPatch = true
			}
			continue
		}
		if def.Function.Name == "update_plan" {
			native := codexio.UpdatePlanTool()
			def.Function = ToolFuncDef{Name: native.Name, Description: native.Description, Parameters: native.Parameters}
		}
		result = append(result, def)
	}

	return result
}

// WithModelCommands adds the native execution contract before permission filters.
func WithModelCommands(defs []ToolDef, model string) []ToolDef {
	if !codexio.IsGPT(model) {
		return defs
	}
	result := append([]ToolDef(nil), defs...)
	for _, def := range defs {
		if def.Function.Name == "execute_command" {
			for _, native := range []codexio.FunctionTool{codexio.ExecCommandTool(), codexio.WriteStdinTool()} {
				found := false
				for _, existing := range result {
					if existing.Function.Name == native.Name {
						found = true
						break
					}
				}
				if !found {
					result = append(result, ToolDef{Type: "function", Function: ToolFuncDef{Name: native.Name, Description: native.Description, Parameters: native.Parameters}})
				}
			}
			break
		}
	}
	return result
}

func executeApplyPatch(ctx context.Context, params map[string]any, ed *fileio.Editor, opts *ToolOptions) ToolResult {
	patch, err := codexio.Parse(GetStrArg(params, "input", ""))
	if err != nil {
		return ToolResult{Content: "Error: " + err.Error()}
	}
	if ed == nil {
		return ToolResult{Content: "Error: apply_patch requires the tracked editor"}
	}
	root := ""
	if opts != nil {
		root = opts.WorkDir
	}
	changes, err := ed.ApplyCodexPatch(ctx, root, patch, codexio.PreserveLineEndings, mutationPreparer(opts))
	var summary, diffs strings.Builder
	if err != nil {
		fmt.Fprintf(&summary, "Error: %v\n", err)
		if len(changes) > 0 {
			summary.WriteString("Earlier operations committed and remain undoable:\n")
		}
	} else {
		summary.WriteString("Success. Updated the following files:\n")
		for _, kind := range []codexio.OperationKind{codexio.AddFile, codexio.UpdateFile, codexio.DeleteFile} {
			for _, op := range patch.Operations {
				if op.Kind != kind {
					continue
				}
				path, letter := op.Path, "M"
				if op.MoveTo != "" {
					path = op.MoveTo
				}
				if kind == codexio.AddFile {
					letter = "A"
				}
				if kind == codexio.DeleteFile {
					letter = "D"
				}
				fmt.Fprintf(&summary, "%s %s\n", letter, path)
			}
		}
	}
	result := ToolResult{}
	for _, change := range changes {
		kind := "M"
		if change.Before.Kind == fileio.MutationAbsent {
			kind = "A"
		}
		if change.After.Kind == fileio.MutationAbsent {
			kind = "D"
		}
		if err != nil {
			fmt.Fprintf(&summary, "%s %s\n", kind, change.Path)
		}
		text, added, removed := diff.GenerateDiff(string(change.Before.Bytes), string(change.After.Bytes), change.Path)
		result.Added += added
		result.Removed += removed
		if !diff.IsEmptyDiff(text) {
			diffs.WriteString(text)
			diffs.WriteByte('\n')
		}
	}
	if diffs.Len() > 0 {
		fmt.Fprintf(&summary, "\n```diff\n%s```\n", diffs.String())
	}
	result.Content = summary.String()
	return result
}
