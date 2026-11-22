package coreprotocol

import (
	"crude/types/insight"
)

// Insight is an independent acknowledged topic. Parsing cannot hold up text
// admission or document-delta ACKs, and text may overtake an analysis result.
const TopicInsight = "insight"

type InsightUpdate struct {
	View insight.DocumentInsight
}
