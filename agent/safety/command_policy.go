package safety

import (
	"path/filepath"
	"strings"

	"crude/internal/logger"
	"crude/types/command"
	"crude/types/setting"
)

type CommandRule struct {
	Pattern string
	Risk    CommandRisk
}

type CommandPolicy struct {
	ConfirmAt           CommandRisk
	UnknownCommandRisk  CommandRisk
	ScriptExecutionRisk CommandRisk
	Rules               []CommandRule
	NotifyDangerous     bool
	// Syntax is the language of the shell that will run the command. The
	// checker parses bash; other syntaxes add the checks that bash parsing
	// cannot see. Empty is POSIX.
	Syntax command.Syntax
}

func DefaultCommandPolicy() CommandPolicy {
	return CommandPolicy{
		ConfirmAt: RiskUnknown, UnknownCommandRisk: RiskUnknown,
		ScriptExecutionRisk: RiskUnknown, NotifyDangerous: true,
	}
}

// CommandPolicyFromSettings builds the command policy from saved safety settings over the defaults.
func CommandPolicyFromSettings(settings setting.Safety) CommandPolicy {
	policy := DefaultCommandPolicy()
	if risk, ok := ParseCommandRisk(settings.ConfirmAt); ok {
		policy.ConfirmAt = risk
	}
	if risk, ok := ParseCommandRisk(settings.UnknownCommandRisk); ok {
		policy.UnknownCommandRisk = risk
	}
	if risk, ok := ParseCommandRisk(settings.ScriptExecutionRisk); ok {
		policy.ScriptExecutionRisk = risk
	}
	if settings.NotifyDangerous != "" {
		policy.NotifyDangerous = settings.NotifyDangerous != "off"
	}
	for _, rule := range settings.CommandRules {
		risk, ok := ParseCommandRisk(rule.Risk)
		if ok && strings.TrimSpace(rule.Pattern) != "" {
			policy.Rules = append(policy.Rules, CommandRule{Pattern: strings.TrimSpace(rule.Pattern), Risk: risk})
		}
	}
	return policy
}

func ParseCommandRisk(value string) (CommandRisk, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "unknown":
		return RiskUnknown, true
	case "safe":
		return RiskSafe, true
	case "project-write", "write":
		return RiskProjectWrite, true
	case "network":
		return RiskNetwork, true
	case "destructive", "dangerous":
		return RiskDestructive, true
	case "blocked", "block":
		return RiskBlocked, true
	default:
		return RiskUnknown, false
	}
}

func CheckCommandSafety(cmd string, lg *logger.Logger) *CommandSafetyResult {
	return CheckCommandSafetyWithPolicy(cmd, lg, DefaultCommandPolicy())
}

func CheckCommandSafetyWithPolicy(cmd string, lg *logger.Logger, policy CommandPolicy) *CommandSafetyResult {
	checked := cmd
	if policy.Syntax == command.SyntaxPowerShell {
		checked = withoutPowerShellLiteralAssignments(cmd)
	}
	result := checkCommandSafetyBase(checked, lg, policy)
	// Approvals and prompts name the command the shell receives.
	result.FullCommand = cmd
	if result.Risk.IsAlwaysBlocked() {
		return result
	}
	if !result.ratedByUnit {
		// A finding about the whole line: rate it as one command.
		line := CommandUnit{Name: result.BaseCommand, Text: strings.TrimSpace(cmd), Risk: result.Risk, Reason: result.Reason, Suggestion: result.Suggestion}
		policy.rateUnit(&line, result.PipedToShell)
		result.Risk, result.Reason, result.Suggestion, result.MatchedRule = line.Risk, line.Reason, line.Suggestion, line.MatchedRule
	} else if result.MatchedRule == "" {
		// A rule may name the whole line, such as `make check && make lint`.
		// It then decides the line, so an approval keeps the exact line.
		if rule, ok := policy.matchRule(strings.TrimSpace(cmd)); ok {
			result.Risk, result.MatchedRule = rule.Risk, rule.Pattern
			result.Reason = "Configured command rule " + rule.Pattern + ": " + rule.Risk.String()
			result.decomposed = false
		}
	}
	// After the rules: a rule names the commands the checker saw, so it cannot
	// vouch for code that the checker could not see.
	if policy.Syntax == command.SyntaxPowerShell && riskRank(result.Risk) < riskRank(RiskUnknown) && powerShellRunsHiddenCode(cmd) {
		result.Risk = RiskUnknown
		result.MatchedRule = ""
		result.Reason = "PowerShell runs code in parentheses, braces, block comments and & calls. Crude cannot check that code, so it needs approval."
	}
	if result.Unreadable && riskRank(result.Risk) < riskRank(RiskUnknown) {
		result.Risk = RiskUnknown
		result.MatchedRule = ""
	}
	result.ConfirmationRequired = result.Unreadable || riskRank(result.Risk) >= riskRank(policy.ConfirmAt) && result.Risk != RiskSafe
	return result
}

// rateUnit applies the configured policy to one command: the unknown-command
// policy, the local-script policy and then the first rule that names it. A
// hard block is never changed.
func (policy CommandPolicy) rateUnit(unit *CommandUnit, pipedToShell bool) {
	if unit.Risk.IsAlwaysBlocked() {
		return
	}
	if unit.Risk == RiskUnknown {
		unit.Risk = policy.UnknownCommandRisk
		unit.Reason = "Unknown command policy: " + unit.Risk.String()
	}
	if isScriptName(unit.Name, pipedToShell) {
		unit.Risk = policy.ScriptExecutionRisk
		unit.Reason = "Local script execution policy: " + unit.Risk.String()
	}
	for _, candidate := range []string{strings.TrimSpace(unit.Name), strings.TrimSpace(unit.Text)} {
		if rule, ok := policy.matchRule(candidate); ok {
			unit.Risk, unit.MatchedRule = rule.Risk, rule.Pattern
			unit.Reason = "Configured command rule " + rule.Pattern + ": " + rule.Risk.String()
			return
		}
	}
}

// matchRule returns the first rule whose glob or exact text names value, with
// or without a leading "./".
func (policy CommandPolicy) matchRule(value string) (CommandRule, bool) {
	if value == "" {
		return CommandRule{}, false
	}
	for _, rule := range policy.Rules {
		for _, candidate := range []string{value, strings.TrimPrefix(value, "./")} {
			if matched, _ := filepath.Match(rule.Pattern, candidate); matched || rule.Pattern == candidate {
				return rule, true
			}
		}
	}
	return CommandRule{}, false
}

func riskRank(risk CommandRisk) int {
	switch risk {
	case RiskSafe:
		return 0
	case RiskUnknown:
		return 1
	case RiskProjectWrite:
		return 2
	case RiskNetwork:
		return 3
	case RiskDestructive:
		return 4
	case RiskBlocked:
		return 5
	default:
		return 1
	}
}

// isScriptName reports a command that runs a local script: a *.sh, *.bash
// or *.zsh file, or a shell that reads a script rather than a pipe.
func isScriptName(name string, pipedToShell bool) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(strings.TrimSpace(name)))
	if strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".bash") || strings.HasSuffix(base, ".zsh") {
		return true
	}
	switch base {
	case "sh", "bash", "zsh", "dash", "fish", "source", ".":
		return !pipedToShell
	default:
		return false
	}
}
