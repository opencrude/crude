package safety

import (
	"path/filepath"
	"strings"

	"crude/logger"
	"crude/types"
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
}

func DefaultCommandPolicy() CommandPolicy {
	return CommandPolicy{
		ConfirmAt: RiskUnknown, UnknownCommandRisk: RiskUnknown,
		ScriptExecutionRisk: RiskUnknown, NotifyDangerous: true,
	}
}

func CommandPolicyFromSettings(settings types.SafetySettings) CommandPolicy {
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
	result := checkCommandSafetyBase(cmd, lg)
	if result.Risk.IsAlwaysBlocked() {
		return result
	}
	if result.Risk == RiskUnknown {
		result.Risk = policy.UnknownCommandRisk
		result.Reason = "Unknown command policy: " + result.Risk.String()
	}
	if isScriptExecution(result) {
		result.Risk = policy.ScriptExecutionRisk
		result.Reason = "Local script execution policy: " + result.Risk.String()
	}
	candidate := strings.TrimSpace(result.BaseCommand)
	if len(result.Calls) > 0 {
		candidate = strings.Join(result.Calls, " ")
	}
	for _, rule := range policy.Rules {
		candidates := []string{candidate, strings.TrimPrefix(candidate, "./"), strings.TrimSpace(cmd), strings.TrimPrefix(strings.TrimSpace(cmd), "./")}
		matched := false
		for _, value := range candidates {
			globMatch, _ := filepath.Match(rule.Pattern, value)
			matched = matched || globMatch || rule.Pattern == value
		}
		if matched {
			result.Risk = rule.Risk
			result.MatchedRule = rule.Pattern
			result.Reason = "Configured command rule " + rule.Pattern + ": " + rule.Risk.String()
			break
		}
	}
	result.ConfirmationRequired = riskRank(result.Risk) >= riskRank(policy.ConfirmAt) && result.Risk != RiskSafe
	return result
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

func isScriptExecution(result *CommandSafetyResult) bool {
	if result == nil {
		return false
	}
	if strings.TrimSpace(result.BaseCommand) == "" {
		return false
	}
	base := strings.ToLower(filepath.Base(result.BaseCommand))
	if strings.HasSuffix(base, ".sh") || strings.HasSuffix(base, ".bash") || strings.HasSuffix(base, ".zsh") {
		return true
	}
	switch base {
	case "sh", "bash", "zsh", "dash", "fish", "source", ".":
		return !result.PipedToShell
	default:
		return false
	}
}
