package shellport

import (
	"context"
	"crude/types/insight"
)

// SelectionFormatter returns a bounded plan, never writes text or disk. The
// invoking editor must retain its exact source and private ranges until apply.
type SelectionFormatter interface {
	RequestSelectionFormatting(context.Context, insight.LSPFormattingRequest) (insight.LSPFormattingResult, error)
}
