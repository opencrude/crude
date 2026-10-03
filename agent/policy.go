package agent

import (
	"crude/safety"
	"crude/tools"
	captypes "crude/types/capability"
)

func ComposePolicy(parentPolicy safety.Policy, specPolicyName string, category string) safety.Policy {
	child := categoryPolicy(category)

	if specPolicyName != "" {
		switch specPolicyName {
		case "read-only":
			child = safety.ReadOnlyPolicy()
		case "no-delete":
			child = safety.DefaultPolicy()
			child.AllowedCapabilities[safety.CanDelete] = false
		case "no-execute":
			child = safety.DefaultPolicy()
			child.AllowedCapabilities[safety.CanExecute] = false
		case "default":
			child = safety.DefaultPolicy()
		}
	}

	for cap := range child.AllowedCapabilities {
		if !parentPolicy.AllowedCapabilities[cap] {
			child.AllowedCapabilities[cap] = false
		}
	}

	catPolicy := categoryPolicy(category)
	for cap := range child.AllowedCapabilities {
		if !catPolicy.AllowedCapabilities[cap] {
			child.AllowedCapabilities[cap] = false
		}
	}

	for cap, needed := range parentPolicy.RequireConfirmation {
		if needed {
			child.RequireConfirmation[cap] = true
		}
	}

	return child
}

func categoryPolicy(category string) safety.Policy {
	switch category {
	case CatResearcher:
		return safety.ReadOnlyPolicy()
	case CatCrafter:
		p := safety.DefaultPolicy()
		p.AllowedCapabilities[safety.CanDelete] = false
		return p
	case CatAeron:
		p := safety.DefaultPolicy()
		p.AllowedCapabilities[safety.CanDelete] = false
		p.AllowedCapabilities[safety.CanExecute] = false
		return p
	default:
		return safety.DefaultPolicy()
	}
}

func PolicyAllows(policy safety.Policy, toolName string) bool {
	metadata, exists := tools.NativeMetadata(toolName)
	if !exists {
		return false
	}
	if toolName == "exec_command" || toolName == "write_stdin" || toolName == "execute_command" || toolName == "async_execute_command" {
		return policy.IsAllowed(safety.OpExecute) || policy.IsAllowed(safety.OpExecuteReadOnly)
	}
	return PolicyAllowsMetadata(policy, metadata)
}

func PolicyAllowsMetadata(policy safety.Policy, metadata tools.ToolMetadata) bool {
	if metadata.Name == "" || metadata.Scope == "" || metadata.Dynamic && metadata.Fingerprint == "" {
		return false
	}
	switch metadata.Effect {
	case captypes.EffectRead, captypes.EffectCommunication:
		return policy.IsAllowed(safety.OpRead)
	case captypes.EffectWrite:
		return policy.IsAllowed(safety.OpUpdate)
	case captypes.EffectDelete:
		return policy.IsAllowed(safety.OpDelete)
	case captypes.EffectExecute:
		return policy.IsAllowed(safety.OpExecute)
	default:
		return false
	}
}
