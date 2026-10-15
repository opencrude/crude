package agent

// CompactionOff disables automatic compaction for an agent.
const CompactionOff = -1

// CompactionPercentSetting maps the persisted setting to the agent's choice:
// unset lets Crude decide, 0 turns automatic compaction off, and any other
// value is an explicit percent of the context window.
func CompactionPercentSetting(value *int) int {
	if value == nil {
		return 0
	}
	if *value <= 0 {
		return CompactionOff
	}
	return min(100, *value)
}

// RetentionSetting maps the persisted verbatim-retention setting; unset sizes
// retention adaptively.
func RetentionSetting(value *int) int {
	if value == nil || *value <= 0 {
		return 0
	}
	return *value
}
