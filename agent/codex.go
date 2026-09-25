package agent

import (
	"fmt"

	"crude/codexio"
	"crude/safety"
	"crude/tools"
)

func (a *Agent) configureModelTools() {
	if !codexio.IsGPT(a.Model) {
		return
	}
	defs := a.FilteredTools
	if defs == nil {
		defs = tools.WithModelCommands(tools.AgentTools, a.Model)
	}
	a.FilteredTools = tools.ForModel(defs, a.Model)
	if len(a.FilteredTools) > 0 && findToolDef("multi_tool_use.parallel", a.FilteredTools) == nil {
		a.FilteredTools = append(append([]tools.ToolDef(nil), a.FilteredTools...), tools.ParallelTool())
	}
}

func (a *Agent) validatePatchPermissions(params map[string]any) string {
	if err := CheckPatchPolicy(a.Policy, getStr(params, "input")); err != nil {
		return "Error: " + err.Error()
	}
	return ""
}

// CheckPatchPolicy applies explicit filesystem authorization, not a restricted
// patch dialect. Move-with-edit is an update, irrespective of move_file exposure.
func CheckPatchPolicy(policy safety.Policy, input string) error {
	patch, err := codexio.Parse(input)
	if err != nil {
		return err
	}
	for _, op := range patch.Operations {
		capability := safety.OpUpdate
		switch op.Kind {
		case codexio.AddFile:
			capability = safety.OpCreate
		case codexio.DeleteFile:
			capability = safety.OpDelete
		}
		if !policy.IsAllowed(capability) {
			return fmt.Errorf("[POLICY BLOCK] patch operation %s is not allowed", op.Kind)
		}
	}
	return nil
}
