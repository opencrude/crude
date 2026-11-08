package session

import (
	"crude/hostshell"
	"crude/safety"
	"crude/types/command"
)

// agentShell resolves the shell that runs every agent command of this session
// now: the Settings › Agents choice, resolved on the core's machine. Command
// execution, command review and the system prompt all use this one value.
func (s *Session) agentShell() command.Shell {
	choice := ""
	if s != nil {
		s.mu.Lock()
		if s.cfg != nil {
			choice = s.cfg.Agents.Shell
		}
		s.mu.Unlock()
	}
	return hostshell.ResolveAgent(choice)
}

// commandPolicy is the configured command review policy for the syntax of
// the shell that runs a call.
func (s *Session) commandPolicy(tool string, params map[string]any) safety.CommandPolicy {
	policy := safety.DefaultCommandPolicy()
	if s.cfg != nil {
		policy = safety.CommandPolicyFromSettings(s.cfg.Safety)
	}
	policy.Syntax = safety.CallSyntax(tool, getStrParam(params, "shell"), s.agentShell().Syntax)
	return policy
}
