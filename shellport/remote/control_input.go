package remote

import (
	"context"
	"crude/coreprotocol"
	"crude/shellport"
)

var _ shellport.ControlInput = (*Workspace)(nil)

func (w *Workspace) SubmitControlInput(ctx context.Context, input coreprotocol.ControlInputRequest) (coreprotocol.InputResult, error) {
	var out coreprotocol.InputResult
	err := w.call(ctx, coreprotocol.SubmitControlInput, &input, &out)
	return out, err
}
