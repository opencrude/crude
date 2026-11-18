package shellport

import (
	"context"
	"crude/types"
)

// SelectionFormatter returns a bounded plan, never writes text or disk. The
// invoking editor must retain its exact source and private ranges until apply.
type SelectionFormatter interface {
	RequestSelectionFormatting(context.Context, types.LSPFormattingRequest) (types.LSPFormattingResult, error)
}
