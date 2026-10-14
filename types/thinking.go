package types

// ThinkingLevels is the canonical reasoning-effort ladder shared by settings
// forms, session overrides and agent tools. The empty value means "provider
// default" and is not listed.
var ThinkingLevels = []string{"none", "low", "medium", "high", "xhigh", "max"}

// ValidThinkingLevel reports whether level is empty (provider default) or one
// of ThinkingLevels.
func ValidThinkingLevel(level string) bool {
	if level == "" {
		return true
	}
	for _, known := range ThinkingLevels {
		if level == known {
			return true
		}
	}
	return false
}
