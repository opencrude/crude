package safety

import "regexp"

type DangerousPattern struct {
	Pattern    *regexp.Regexp
	Reason     string
	Suggestion string
}

// dangerousPatterns are patterns that should NEVER be auto-allowed.
// These catch edge cases the AST-based command checker might miss or
// patterns that require combined analysis (e.g., specific flag+arg combos).
var dangerousPatterns = []DangerousPattern{
	// Fork bombs
	{
		Pattern:    regexp.MustCompile(`:\(\)\s*\{`),
		Reason:     "Fork bomb — would crash the system by exhausting process limits",
		Suggestion: "Never execute fork bombs",
	},
	// Miners / known malware
	{
		Pattern:    regexp.MustCompile(`\b(minerd|xmrig|cpuminer|ccminer|ethminer|sgminer|cgminer)\b`),
		Reason:     "Cryptocurrency miner — likely malware",
		Suggestion: "Do not run cryptocurrency miners",
	},
	// Privilege escalation tools
	{
		Pattern:    regexp.MustCompile(`\b(peass|linpeas|linenum|linux-exploit-suggester|dirtycow|pwnkit|dirtypipe)\b`),
		Reason:     "Privilege escalation tool or exploit — attempting to gain root access",
		Suggestion: "Do not run privilege escalation exploits",
	},
	// curl/wget to system path
	{
		Pattern:    regexp.MustCompile(`\b(curl|wget)\s+.*\s+-[a-zA-Z]*o\s+/etc/`),
		Reason:     "Downloading files to system configuration directory — possible persistence mechanism",
		Suggestion: "Download to the project directory, not system paths",
	},
}

func matchDangerousPattern(cmd string, res *CommandSafetyResult) *DangerousPattern {
	for _, dp := range dangerousPatterns {
		if dp.Pattern.MatchString(cmd) {
			return &dp
		}
	}
	return nil
}
