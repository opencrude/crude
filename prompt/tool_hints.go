package prompt

import (
	"fmt"
	"strings"
)

// Tool failures may carry one trailing hint addressed to the model's next
// call. It is model instruction text, so it lives here, and it is always
// separated from the failure by ToolHintSeparator: presentation strips
// everything after the separator (types.StripToolHint), so a hint never
// reaches a transcript row.
const ToolHintSeparator = "\n\nHint: "

// WithToolHint appends a model-facing hint to a tool failure.
func WithToolHint(failure, hint string) string {
	return failure + ToolHintSeparator + hint
}

const (
	HintVerifyEditContent = "use read_file to verify the exact content before retrying."
	HintNoChangeEdit      = "match the file's current bytes exactly: read_file the section first, and make old_string and new_string differ."
	HintDiscoverNames     = "file not found. Use glob_files or list_directory to discover actual names."
)

// HintEmptyDirectory explains a missing file whose nearest directory is empty.
func HintEmptyDirectory(dir string) string {
	return fmt.Sprintf("file not found; %s is empty.", dir)
}

// HintNearbyEntries names the closest entries of the nearest existing
// directory of a missing file.
func HintNearbyEntries(dir string, entries []string) string {
	return fmt.Sprintf("file not found. Entries in %s (closest first): %s", dir, strings.Join(entries, ", "))
}
