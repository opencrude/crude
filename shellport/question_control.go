package shellport

import (
	"context"
	"crude/coreprotocol"
)

type QuestionControl interface {
	ResolveQuestionControl(context.Context, coreprotocol.QuestionControlRequest) (coreprotocol.QuestionControlResult, error)
}

// QuestionReceipt is the ordinary human UI acknowledgment path. Its boolean
// reports the backend decision, never local queue admission.
type QuestionReceipt interface {
	ResolveQuestionReceipt(context.Context, string, string, map[string]string, map[string]string, bool) (coreprotocol.QuestionControlResult, error)
}
