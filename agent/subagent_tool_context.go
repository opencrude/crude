package agent

// SubAgentToolCallIDKey correlates a launched child to the exact parent
// run_sub_agent call. The tool dispatcher carries it in the execution context
// rather than guessing from the most recent parent transcript row.
type SubAgentToolCallIDKey struct{}
