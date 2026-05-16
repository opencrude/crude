package safety

import (
	"regexp"

	"crude/logger"
)

type DangerousPattern struct {
	Pattern    *regexp.Regexp
	Reason     string
	Suggestion string
}

var dangerousPatterns = []DangerousPattern{
	{
		Pattern:    regexp.MustCompile(`:\(\)\s*\{`),
		Reason:     "Fork bomb — would crash the system by exhausting process limits",
		Suggestion: "Never execute fork bombs",
	},
	{
		Pattern:    regexp.MustCompile(`\b(minerd|xmrig|cpuminer|ccminer|ethminer|sgminer|cgminer)\b`),
		Reason:     "Cryptocurrency miner — likely malware",
		Suggestion: "Do not run cryptocurrency miners",
	},
	{
		Pattern:    regexp.MustCompile(`\b(peass|linpeas|linenum|linux-exploit-suggester|dirtycow|pwnkit|dirtypipe)\b`),
		Reason:     "Privilege escalation tool or exploit — attempting to gain root access",
		Suggestion: "Do not run privilege escalation exploits",
	},
	{
		Pattern:    regexp.MustCompile(`\b(curl|wget)\s+.*\s+-[a-zA-Z]*o\s+/etc/`),
		Reason:     "Downloading files to system configuration directory — possible persistence mechanism",
		Suggestion: "Download to the project directory, not system paths",
	},
}

func matchDangerousPattern(cmd string, res *CommandSafetyResult) *DangerousPattern {
	for _, dp := range dangerousPatterns {
		if dp.Pattern.MatchString(cmd) {
			safetyLogIf(func(lg *logger.Logger) {
				lg.Warn("SAFETY", "dangerous_pattern", "pattern", dp.Pattern.String(), "reason", dp.Reason, "base_cmd", res.BaseCommand)
			})
			return &dp
		}
	}
	return nil
}
