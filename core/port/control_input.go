package shellport

import (
	"context"
	"crude/core/protocol"
)

// ControlInput awaits durable admission rather than simulating human Enter.
type ControlInput interface {
	SubmitControlInput(context.Context, coreprotocol.ControlInputRequest) (coreprotocol.InputResult, error)
}
